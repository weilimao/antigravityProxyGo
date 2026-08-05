package relay

// nvidia_cancel_test.go 锁定 NVIDIA 流式链路"取消即断 + 尾帧补发"的两个关键行为：
//   ① watchCancel(ctx, body): ctx 取消时立即 Close 上游 body,使 bufio.Scanner.Scan() 退出。
//   ② 三条流式回写路径(Anthropic / Responses / OpenAI 透传)在 ctx 取消后:
//      - scanner 及时退出(不再空跑上游);
//      - 下游补发协议级尾帧(Anthropic: message_delta(end_turn)+message_stop;
//        Responses: response.completed(stop); OpenAI: data: [DONE])。
//
// 全部用永不结束的慢速上游 reader 模拟"上游仍在吐帧"场景,确保是被 ctx 中断而非自然 EOF,
// 从而严格区分"取消即断"与"正常读完"两条路径。

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowNeverEndingReader 每次读阻塞一小段时间后才返回一帧,永不 EOF。
// 用于模拟"上游正在缓慢吐帧、客户端却取消"的场景,确保只有 ctx 中断能让 scanner 退出。
type slowNeverEndingReader struct {
	mu      sync.Mutex
	closed  bool
	frame   string // 单次读返回的 SSE 帧
	delays  int    // 已读次数,用于断言 reader 被读了几轮(未被提前关闭)
	delayMs int
}

func newSlowNeverEndingReader(frame string, delayMs int) *slowNeverEndingReader {
	return &slowNeverEndingReader{frame: frame, delayMs: delayMs}
}

func (s *slowNeverEndingReader) Read(p []byte) (int, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe // 模拟 body 被 Close 后的读错误
	}
	if s.delayMs > 0 {
		time.Sleep(time.Duration(s.delayMs) * time.Millisecond)
	}
	s.mu.Lock()
	s.delays++
	s.mu.Unlock()
	n := copy(p, []byte(s.frame))
	return n, nil
}

func (s *slowNeverEndingReader) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *slowNeverEndingReader) delaysCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delays
}

// === ① watchCancel 单元测试 ===

// TestWatchCancel_ClosesUpstreamBody 断言 ctx 取消后 resp.Body.Close 被调用一次。
func TestWatchCancel_ClosesUpstreamBody(t *testing.T) {
	body := newSlowNeverEndingReader("data: x\n\n", 5)
	ctx, cancel := context.WithCancel(context.Background())

	stop := watchCancel(ctx, body)
	t.Cleanup(func() { stop() })

	// 取消前:body 尚未被 close
	if body.closed {
		t.Fatalf("body should not be closed before ctx cancel")
	}

	cancel()

	// watchCancel 的 goroutine 需要被调度;给一个宽松上限等 Close 生效
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if body.closed {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !body.closed {
		t.Fatalf("body should be closed after ctx cancel")
	}
}

// TestWatchCancel_StopWithoutCancelNoClose 断言正常路径(未取消,调 stop())不会误关 body。
func TestWatchCancel_StopWithoutCancelNoClose(t *testing.T) {
	body := newSlowNeverEndingReader("data: x\n\n", 5)
	ctx := context.Background()

	stop := watchCancel(ctx, body)
	stop() // 正常收尾,未取消

	if body.closed {
		t.Fatalf("body must NOT be closed when ctx not cancelled (defer resp.Body.Close 兜底)")
	}
}

// === ② Anthropic 路径:ctx 取消即断 + 尾帧补发 ===

// TestOpenAIChatSSEToAnthropicSSE_ClientCancelEmitsTailFrames 断言:
// 上游永不 EOF 时取消 ctx → scanner 退出 → 下游收到 message_delta + message_stop 尾帧。
func TestOpenAIChatSSEToAnthropicSSE_ClientCancelEmitsTailFrames(t *testing.T) {
	// 慢速永不结束的上游:每 5ms 返回一帧文本增量,模拟上游持续吐字
	body := newSlowNeverEndingReader("data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n", 5)
	ctx, cancel := context.WithCancel(context.Background())

	var out bytes.Buffer
	bw := bufio.NewWriter(&out)
	done := make(chan struct{})
	var in, out2 int
	var err error
	go func() {
		defer close(done)
		in, out2, err = OpenAIChatSSEToAnthropicSSE(ctx, body, body, bw, "z-ai/glm-5.2", nil)
		bw.Flush()
	}()

	// 让上游吐几帧,确保进入正常流转,随后取消
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("OpenAIChatSSEToAnthropicSSE 未在 ctx 取消后 2s 内退出(scanner 卡死)")
	}

	// ctx 取消触发的 scanErr 不应作为 err 上抛(避免调用方误判为上游故障)
	if err != nil {
		t.Fatalf("ctx 取消路径不应返回 err, got: %v", err)
	}

	outStr := out.String()
	events := parseSSEEvents(outStr)
	requireEvent(t, events, "message_start")
	requireEvent(t, events, "message_delta")
	requireEvent(t, events, "message_stop")

	// stopReason 必须是 end_turn:从 message_delta 的 data 中校验
	for _, ev := range events {
		if ev.event == "message_delta" {
			if !strings.Contains(ev.data, `"stop_reason":"end_turn"`) {
				t.Errorf("ctx 取消补发的 message_delta 应为 end_turn 语义, got: %s", ev.data)
			}
		}
	}

	// 上游 reader 应已被 Close(watchCancel 触发)
	if !body.closed {
		t.Errorf("上游 body 应在 ctx 取消后被 watchCancel Close")
	}
	_ = in
	_ = out2
}

// === ③ Responses 路径:ctx 取消即断 + 尾帧补发 ===

// TestOpenAIChatSSEToResponsesSSE_ClientCancelEmitsCompleted 断言:
// 上游永不 EOF 时取消 ctx → scanner 退出 → 下游收到 response.completed(stop) 尾帧。
func TestOpenAIChatSSEToResponsesSSE_ClientCancelEmitsCompleted(t *testing.T) {
	body := newSlowNeverEndingReader("data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n", 5)
	ctx, cancel := context.WithCancel(context.Background())

	buf := &flushBuffer{}
	fw := newFlushWriter("test_resp", bufio.NewWriter(buf))
	done := make(chan struct{})
	var in, out int
	go func() {
		defer close(done)
		in, out = OpenAIChatSSEToResponsesSSE(ctx, body, body, fw, "z-ai/glm-5.2")
		fw.flush()
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("OpenAIChatSSEToResponsesSSE 未在 ctx 取消后 2s 内退出")
	}

	outStr := buf.String()
	events := parseSSEEvents(outStr)
	requireEvent(t, events, "response.created")
	requireEvent(t, events, "response.completed")

	// ctx 取消、上游无 finish_reason → stopReason 必为 stop
	for _, ev := range events {
		if ev.event == "response.completed" {
			if !strings.Contains(ev.data, `"stop_reason":"stop"`) {
				t.Errorf("ctx 取消补发的 response.completed 应为 stop 语义, got: %s", ev.data)
			}
		}
	}

	if !body.closed {
		t.Errorf("上游 body 应在 ctx 取消后被 watchCancel Close")
	}
	_ = in
	_ = out
}

// === ④ OpenAI 透传路径:ctx 取消即断 + [DONE] 补发 ===

// TestProxyNvidiaOpenAIPassthrough_ClientCancelEmitsDone 用 handler 的透传方法断言:
// 上游永不 EOF 时取消 ctx → scanner 退出 → 下游补发 data: [DONE]。
func TestProxyNvidiaOpenAIPassthrough_ClientCancelEmitsDone(t *testing.T) {
	body := newSlowNeverEndingReader("data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n", 5)
	ctx, cancel := context.WithCancel(context.Background())

	// 用 flushCounter(内嵌 httptest.ResponseRecorder + Flusher)捕获下游输出
	rr := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}

	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	done := make(chan struct{})
	var inU, outU int
	go func() {
		defer close(done)
		inU, outU = handler.proxyNvidiaOpenAIPassthrough(ctx, rr, resp, true, nil)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("proxyNvidiaOpenAIPassthrough 未在 ctx 取消后 3s 内退出")
	}

	outStr := rr.Body.String()
	if !strings.Contains(outStr, "data: [DONE]") {
		t.Errorf("ctx 取消后下游应补发 data: [DONE], got body:\n%s", outStr)
	}
	if !body.closed {
		t.Errorf("上游 body 应在 ctx 取消后被 watchCancel Close")
	}
	_ = inU
	_ = outU
}

// === ⑤ 正常(未取消)路径回归:不应误补尾帧语义 ===

// TestOpenAIChatSSEToAnthropicSSE_NormalNoLeakTailFrame 断言:
// 正常读完上游(自然 EOF)且上游已发 finish_reason 时,不应被 ctx 取消逻辑污染为 end_turn。
func TestOpenAIChatSSEToAnthropicSSE_NormalNoLeakTailFrame(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	var out bytes.Buffer
	bw := bufio.NewWriter(&out)
	in, out2, err := OpenAIChatSSEToAnthropicSSE(context.Background(), strings.NewReader(sse), nil, bw, "z-ai/glm-5.2", nil)
	bw.Flush()
	if err != nil {
		t.Fatalf("normal path unexpected err: %v", err)
	}
	if in != 5 || out2 != 3 {
		t.Errorf("usage mismatch: in=%d out=%d, want 5/3", in, out2)
	}

	outStr := out.String()
	events := parseSSEEvents(outStr)
	requireEvent(t, events, "message_start")
	requireEvent(t, events, "message_delta")
	requireEvent(t, events, "message_stop")

	// 上游给了 finish_reason=stop → determineStopReason 产出 end_turn(OpenAI stop→Anthropic end_turn),
	// 这与 ctx 取消路径的 end_turn 同值,但来源不同(此处是正常映射)。关键是消息序列完整闭合。
	// 该用例仅作"未取消时不报错、尾帧齐全"的回归护栏。
	_ = events
}
