package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

// roundTripperAdapter 包装 *http.Transport，使 clientForAttempt 走单元测试自定义测试服务端，
// 避免写入真实 netutil 账号连接池映射。
type roundTripperAdapter struct {
	base http.RoundTripper
}

func (r *roundTripperAdapter) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.base.RoundTrip(req)
}

// threadSafeFlushWriter 为并发读写验证提供带锁缓冲的 io.Writer + http.Flusher 实现。
type threadSafeFlushWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	flushes int
	failErr error
}

func (w *threadSafeFlushWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failErr != nil {
		return 0, w.failErr
	}
	return w.buf.Write(p)
}

func (w *threadSafeFlushWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
}

func (w *threadSafeFlushWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *threadSafeFlushWriter) Flushes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

// parseSSEEvents 按标准 SSE 事件空行边界（\n\n 或 \r\n\r\n）拆解完整流。
func parseSSEEvents(raw string) []string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.Split(normalized, "\n\n")
	var events []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		events = append(events, p)
	}
	return events
}

func TestSSEBoundaryTracker_BoundariesAndCrossChunks(t *testing.T) {
	var tr sseBoundaryTracker
	if !tr.AtBoundary() {
		t.Fatal("初始空状态应允许发送心跳")
	}

	// 单 LF 未闭合，收到第二个 LF 才闭合
	tr.Advance([]byte(`data: {"a":1}` + "\n"))
	if tr.AtBoundary() {
		t.Fatal("仅单个 \\n 结尾时不应判定为帧边界")
	}
	tr.Advance([]byte("\n"))
	if !tr.AtBoundary() {
		t.Fatal("补齐第二个 \\n 后应处于帧边界")
	}

	// CRLF CRLF 跨包拆分: \r\n\r + \n
	tr.Advance([]byte(`data: {"b":2}` + "\r\n\r"))
	if tr.AtBoundary() {
		t.Fatal("半截 \\r\\n\\r 结尾时不应判定为帧边界")
	}
	tr.Advance([]byte("\n"))
	if !tr.AtBoundary() {
		t.Fatal("补齐 \\n 构成 \\r\\n\\r\\n 后应处于帧边界")
	}

	// 验证分隔符边界切分查找
	mixed := []byte("data: {\"x\":1}\n\ndata: {\"y\":2}\r\n\r\ndata: {\"z\":")
	gotSplit := findLastSSEFrameBoundary(mixed)
	wantPrefix := "data: {\"x\":1}\n\ndata: {\"y\":2}\r\n\r\n"
	if gotSplit != len(wantPrefix) {
		t.Fatalf("findLastSSEFrameBoundary 期望=%d, 实际=%d", len(wantPrefix), gotSplit)
	}

	// 验证超限直写残留尾与新 chunk 跨界闭合长度
	if n := findCrossBoundaryClosurePrefix([]byte("\r\n\r"), []byte("\ndata: {}")); n != 1 {
		t.Fatalf("期望跨界闭合长度为 1，实际 %d", n)
	}
	if n := findCrossBoundaryClosurePrefix([]byte("}\n"), []byte("\ndata: {}")); n != 1 {
		t.Fatalf("期望 LF 跨界闭合长度为 1，实际 %d", n)
	}
	if n := findCrossBoundaryClosurePrefix([]byte("}\r\n"), []byte("\r\ndata: {}")); n != 2 {
		t.Fatalf("期望 CRLF 跨界闭合长度为 2，实际 %d", n)
	}
}

func TestSSEStreamWriter_PrefillHeartbeatsNoPayloadPollution(t *testing.T) {
	tw := &threadSafeFlushWriter{}
	var emittedCount int
	var emittedMu sync.Mutex

	w := newSSEStreamWriter(sseStreamWriterConfig{
		writer:            tw,
		flusher:           tw,
		heartbeatInterval: 20 * time.Millisecond,
		onPayloadEmitted: func(b []byte) {
			emittedMu.Lock()
			emittedCount++
			emittedMu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	time.Sleep(70 * time.Millisecond)
	if err := w.FlushTrailing(); err != nil {
		t.Fatalf("FlushTrailing 失败: %v", err)
	}

	raw := tw.String()
	hbCount := strings.Count(raw, string(sseHeartbeatPayload))
	if hbCount < 2 {
		t.Fatalf("Prefill 空闲期间期望至少发送 2 次心跳，实际 %d 次，输出=%q", hbCount, raw)
	}
	emittedMu.Lock()
	defer emittedMu.Unlock()
	if emittedCount != 0 {
		t.Fatalf("仅发送心跳时不应触发业务数据回调，实际触发 %d 次", emittedCount)
	}
}

func TestSSEStreamWriter_PartialFrameBufferedWhileHeartbeatSafe(t *testing.T) {
	tw := &threadSafeFlushWriter{}
	var emittedBytes bytes.Buffer
	var emittedMu sync.Mutex

	w := newSSEStreamWriter(sseStreamWriterConfig{
		writer:            tw,
		flusher:           tw,
		heartbeatInterval: 20 * time.Millisecond,
		maxBufferBytes:    4096,
		onPayloadEmitted: func(b []byte) {
			emittedMu.Lock()
			emittedBytes.Write(b)
			emittedMu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// 发送半截 JSON 事件，不以 \n\n 结尾，随后模拟上游思考卡顿跨越多个心跳周期
	part1 := `data: {"candidates":[{"content":{"parts":[{"text":"hello `
	part2 := `world"}]}}]}` + "\n\n"
	if err := w.WriteUpstreamChunk([]byte(part1)); err != nil {
		t.Fatalf("写入 part1 失败: %v", err)
	}

	time.Sleep(70 * time.Millisecond)

	// 在尚未收到完整分隔符前，下游只能看到完整的心跳注释帧，绝不可出现半截 JSON
	midRaw := tw.String()
	if strings.Contains(midRaw, `"hello`) {
		t.Fatalf("半帧事件未闭合前不应提前冲刷到下游导致与心跳混写: %q", midRaw)
	}
	if strings.Count(midRaw, string(sseHeartbeatPayload)) < 2 {
		t.Fatalf("半帧缓冲期下游处于安全边界，期望继续发送心跳续命，实际输出=%q", midRaw)
	}

	// 补齐闭合帧并粘包下一帧开头，验证精准边界切分
	if err := w.WriteUpstreamChunk([]byte(part2 + `data: {"tail":1}` + "\r\n\r\n")); err != nil {
		t.Fatalf("写入 part2 失败: %v", err)
	}
	if err := w.FlushTrailing(); err != nil {
		t.Fatalf("FlushTrailing 失败: %v", err)
	}

	events := parseSSEEvents(tw.String())
	var jsonPayloads []string
	for _, ev := range events {
		if strings.HasPrefix(ev, ":") {
			continue
		}
		if !strings.HasPrefix(ev, "data: ") {
			t.Fatalf("发现异常非法 SSE 事件帧: %q", ev)
		}
		jsonStr := strings.TrimPrefix(ev, "data: ")
		var parsed map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			t.Fatalf("SSE 业务帧 JSON 被破坏 (%v): %q", err, ev)
		}
		jsonPayloads = append(jsonPayloads, jsonStr)
	}
	if len(jsonPayloads) != 2 {
		t.Fatalf("期望完整解析出 2 条业务 JSON 事件，实际 %d 条: %v", len(jsonPayloads), jsonPayloads)
	}

	emittedMu.Lock()
	defer emittedMu.Unlock()
	if !strings.Contains(emittedBytes.String(), `"hello world"`) {
		t.Fatalf("onPayloadEmitted 业务字节应完整记录，实际=%q", emittedBytes.String())
	}
}

func TestSSEStreamWriter_OversizedFramePausesHeartbeatUntilClosed(t *testing.T) {
	tw := &threadSafeFlushWriter{}

	w := newSSEStreamWriter(sseStreamWriterConfig{
		writer:            tw,
		flusher:           tw,
		heartbeatInterval: 20 * time.Millisecond,
		maxBufferBytes:    32, // 小上限以稳定触发超限透传降级路径
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// 超出 32B 上限的半截帧且末尾包含跨界分隔符前半段 "\r\n\r"
	bigPrefix := `data: {"payload":"` + strings.Repeat("A", 48) + `"}`
	unclosedChunk := []byte(bigPrefix + "\r\n\r")
	if err := w.WriteUpstreamChunk(unclosedChunk); err != nil {
		t.Fatalf("写入超限块失败: %v", err)
	}

	// 在未闭合期间等待超过 3 个心跳周期：严禁任何心跳插入！
	time.Sleep(75 * time.Millisecond)
	mid := tw.String()
	if strings.Contains(mid, ": keep-alive") {
		t.Fatalf("存在未闭合透传帧期间严禁写入心跳注释！实际输出=%q", mid)
	}
	if mid != string(unclosedChunk) {
		t.Fatalf("超限期间下游仅应包含原始透传前缀，期望 %q, 实际 %q", string(unclosedChunk), mid)
	}

	// 发送剩余闭合 byte "\n"（与前序 "\r\n\r" 共同闭合 \r\n\r\n）
	if err := w.WriteUpstreamChunk([]byte("\n")); err != nil {
		t.Fatalf("写入闭合换行失败: %v", err)
	}

	// 闭合后等待超过 2 个心跳周期：心跳应恢复工作且位于闭合之后
	time.Sleep(55 * time.Millisecond)
	if err := w.FlushTrailing(); err != nil {
		t.Fatalf("FlushTrailing 失败: %v", err)
	}

	full := tw.String()
	events := parseSSEEvents(full)
	if len(events) < 2 {
		t.Fatalf("期望至少包含 [超大业务帧 + 恢复后心跳帧]，实际事件=%v", events)
	}
	if !strings.HasPrefix(events[0], "data: ") {
		t.Fatalf("首帧应为完整业务帧，实际=%q", events[0])
	}
	payloadJSON := strings.TrimPrefix(events[0], "data: ")
	var obj map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &obj); err != nil {
		t.Fatalf("超大帧闭合后 JSON 仍必须可完整解析 (%v): %q", err, payloadJSON)
	}
	if events[1] != strings.TrimSuffix(string(sseHeartbeatPayload), "\n\n") {
		t.Fatalf("闭合后后续事件应为完整心跳注释，实际=%q", events[1])
	}
}

func TestSSEStreamWriter_ActivityResetsHeartbeatTimer(t *testing.T) {
	tw := &threadSafeFlushWriter{}
	nowMu := sync.Mutex{}
	now := time.Unix(1700000000, 0)
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advanceTime := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	w := newSSEStreamWriter(sseStreamWriterConfig{
		writer:            tw,
		flusher:           tw,
		heartbeatInterval: 10 * time.Second,
		nowFn:             nowFn,
	})

	// 刚经过 6s 时写出一帧完整业务数据，应刷新 lastWriteAt
	advanceTime(6 * time.Second)
	if err := w.WriteUpstreamChunk([]byte("data: {\"ok\":1}\n\n")); err != nil {
		t.Fatalf("写入完整帧失败: %v", err)
	}

	// 又过去 5s（距业务写入仅 5s < 10s）：即使触发检查也不应误发冗余心跳
	advanceTime(5 * time.Second)
	nextWait, keep := w.trySendHeartbeat()
	if !keep || nextWait != 5*time.Second {
		t.Fatalf("距上次写入仅 5s，期望等待剩余 5s，实际 keep=%v nextWait=%v", keep, nextWait)
	}
	if strings.Contains(tw.String(), ": keep-alive") {
		t.Fatalf("未满 10s 空闲周期不应发送冗余心跳: %q", tw.String())
	}

	// 再过去 5s（满 10s 空闲）：应触发心跳
	advanceTime(5 * time.Second)
	nextWait, keep = w.trySendHeartbeat()
	if !keep || nextWait != 10*time.Second {
		t.Fatalf("满 10s 后期望重置回 10s，实际 keep=%v nextWait=%v", keep, nextWait)
	}
	if !strings.Contains(tw.String(), ": keep-alive") {
		t.Fatalf("满 10s 空闲后期望写入心跳: %q", tw.String())
	}
}

func newSSETestProxyHandler(t *testing.T, upstreamURL string, hbInterval time.Duration, maxBuf int) *ProxyHandler {
	t.Helper()
	srvURL, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("解析上游 URL 失败: %v", err)
	}

	tempDir := t.TempDir()
	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)
	accMgr.AddAccount(&account.Account{
		ID:          "acc-sse-1",
		Email:       "sse-test@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.test_sse_token",
		Enabled:     true,
	})
	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[Proxy] %s", s) },
		nil,
		nil,
		func(string, string) {},
		func(string) string { return "" },
		func() int { return 1 },
		func() int { return 1 },
		func() int64 { return 1024 * 1024 },
		func() int { return 10 },
		nil,
		nil,
	)
	handler.sseHeartbeatInterval = hbInterval
	handler.sseMaxBufferBytes = maxBuf

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &roundTripperAdapter{
			base: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return net.Dial(network, srvURL.Host)
				},
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	handler.client = client
	return handler
}

func TestProxyHandler_SSEHeartbeatBoundaryIntegration(t *testing.T) {
	// 构造真实 TLS 上游，模拟 >4096B 大 SSE 事件被拆分并在拆包间隙停顿超过多个心跳周期
	largeText := strings.Repeat("X", 5000)
	fullJSON := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":"%s"}]}}]}`, largeText)
	splitPoint := 2500

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// 1. 先模拟 Prefill 思考等待（跨过 2 个 25ms 心跳周期）
		time.Sleep(60 * time.Millisecond)

		// 2. 分两次写入同一条 >4096B 的 JSON 事件，中间再次停顿 60ms
		_, _ = io.WriteString(w, "data: "+fullJSON[:splitPoint])
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(60 * time.Millisecond)

		_, _ = io.WriteString(w, fullJSON[splitPoint:]+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}

		// 3. 第二条短事件
		_, _ = io.WriteString(w, `data: {"done":true}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	handler := newSSETestProxyHandler(t, upstream.URL, 25*time.Millisecond, 256*1024)
	proxySrv := httptest.NewServer(handler)
	defer proxySrv.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		proxySrv.URL+"/v1internal:streamGenerateContent?alt=sse",
		strings.NewReader(`{"model":"gemini-2.5-pro","request":{"contents":[]}}`),
	)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Authorization", "Bearer test-token-unit")

	resp, err := proxySrv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求本地代理失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("期望 200 OK，实际 %d", resp.StatusCode)
	}

	// 按 SSE 帧逐帧解析，校验无任何半截 JSON 且 Prefill 心跳正常送达
	reader := bufio.NewReader(resp.Body)
	var eventBuf bytes.Buffer
	var heartbeats int
	var jsonEvents []map[string]any

	flushFrame := func() {
		raw := strings.TrimSpace(strings.ReplaceAll(eventBuf.String(), "\r\n", "\n"))
		eventBuf.Reset()
		if raw == "" {
			return
		}
		if strings.HasPrefix(raw, ":") {
			heartbeats++
			return
		}
		if !strings.HasPrefix(raw, "data: ") {
			t.Fatalf("收到非法非 data/comment SSE 帧: %q", raw)
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "data: ")), &obj); err != nil {
			t.Fatalf("下游收到被截断的 SSE JSON 帧 (%v): %q", err, raw)
		}
		jsonEvents = append(jsonEvents, obj)
	}

	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				flushFrame()
			} else {
				eventBuf.WriteString(line)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				t.Fatalf("读取流式响应失败: %v", readErr)
			}
			flushFrame()
			break
		}
	}

	if heartbeats < 2 {
		t.Fatalf("期望收到至少 2 条心跳帧，实际 %d", heartbeats)
	}
	if len(jsonEvents) != 2 {
		t.Fatalf("期望完整解析出 2 条业务 JSON 事件，实际 %d", len(jsonEvents))
	}
}

func TestProxyHandler_SSEHeartbeatPrefillInterruptRetries(t *testing.T) {
	var attempts int
	var attemptsMu sync.Mutex

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptsMu.Lock()
		attempts++
		cur := attempts
		attemptsMu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if cur == 1 {
			// 第 1 次尝试：声明更大的 Content-Length 后只发半帧且延迟跨过心跳周期再突然返回，
			// 使客户端 Go Transport 读到 io.ErrUnexpectedEOF。
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			time.Sleep(55 * time.Millisecond)
			_, _ = io.WriteString(w, `data: {"broken_attempt":1`)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"retried_ok":true}`+"\n\n")
	}))
	defer upstream.Close()

	handler := newSSETestProxyHandler(t, upstream.URL, 20*time.Millisecond, 256*1024)
	proxySrv := httptest.NewServer(handler)
	defer proxySrv.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		proxySrv.URL+"/v1internal:streamGenerateContent?alt=sse",
		strings.NewReader(`{"model":"gemini-2.5-pro","request":{"contents":[]}}`),
	)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Authorization", "Bearer test-token-retry")

	resp, err := proxySrv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求本地代理失败: %v", err)
	}
	defer resp.Body.Close()

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	raw := string(rawBytes)
	if strings.Contains(raw, "broken_attempt") {
		t.Fatalf("第 1 次尝试未闭合的半帧不应泄出到下游导致破坏重试响应: %q", raw)
	}

	var dataEvents []string
	for _, ev := range parseSSEEvents(raw) {
		if strings.HasPrefix(ev, ":") {
			continue
		}
		dataEvents = append(dataEvents, ev)
	}
	if len(dataEvents) != 1 || dataEvents[0] != `data: {"retried_ok":true}` {
		t.Fatalf("期望仅收到重试成功后的完整事件，实际=%v", dataEvents)
	}

	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if attempts < 2 {
		t.Fatalf("期望触发 STREAM_INTERRUPTED 重试（>=2 次上游请求），实际 %d 次", attempts)
	}
}
