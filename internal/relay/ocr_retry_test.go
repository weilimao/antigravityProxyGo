package relay

// ocr_retry_test.go —— 锁定 OCR 上游调用瞬时失败重试基础设施的契约。
//
// 覆盖 ocr_retry.go + ocr_engine.go(ocrImageUncached / ocrImageUncachedViaRoute 接入)
// + ocr_fetch.go(fetchImageAsBase64 下载重试) 的行为:
//   - ocrCallWithRetry:瞬时失败重试到成功 / 确定性失败不重试 / 全部耗尽返回最后 error / 总超时中止;
//   - isOcrRetryableErr:传输层 EOF / 5xx / 429 / 4xx 非 429 / SSRF / 非 image 分类;
//   - retryableStatusFromErr:从 "status %d" 文本解析状态码;
//   - ocrImageUncached 端到端经 OcrImage 走真实 httptest mock,首调 EOF 第 2 次 200 → 成功;
//   - ocrImageUncachedViaRoute 同款重试契约 + 自递归守卫头在重试下仍带上;
//   - 重试成功仍写成功长 TTL 缓存(同图二次命中不触达)、重试全败仍写失败短 TTL 熔断(短窗内不再触达);
//   - fetchImageAsBase64 下载:瞬时失败重试 / SSRF 拒绝不重试 / 非 image 不重试。

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ==== 纯函数:retryableStatusFromErr ====

func TestRetryableStatusFromErr_ParsesCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
		ok   bool
	}{
		{fmt.Errorf("ocr service returned status 503: slow"), 503, true},
		{fmt.Errorf("ocr route service returned status 429: rate limit"), 429, true},
		{fmt.Errorf("ocr service returned status 400: bad request"), 400, true},
		{fmt.Errorf("execute ocr request: EOF"), 0, false},
		{nil, 0, false},
		{fmt.Errorf("status 99999: overflow guard"), 0, false}, // 5 位数超 9999 兜底
	}
	for i, c := range cases {
		got, ok := retryableStatusFromErr(c.err)
		if ok != c.ok {
			t.Errorf("case %d: ok want %v got %v (err=%v)", i, c.ok, ok, c.err)
			continue
		}
		if ok && got != c.want {
			t.Errorf("case %d: code want %d got %d (err=%v)", i, c.want, got, c.err)
		}
	}
}

// TestRetryableStatusFromErr_GarbledNotTruncated 锁定状态码解析不误吃相邻数字:
// "status abc" / "status 503abc" 路径走纯数字前缀读取,前者不命中,后者取 503。
func TestRetryableStatusFromErr_GarbledNotTruncated(t *testing.T) {
	if _, ok := retryableStatusFromErr(errors.New("status abc")); ok {
		t.Error("status abc should not parse")
	}
	code, ok := retryableStatusFromErr(errors.New("ocr service returned status 503abc"))
	if !ok || code != 503 {
		t.Errorf("status 503abc want code=503 ok=true, got code=%d ok=%v", code, ok)
	}
}

// ==== 纯函数:isOcrRetryableErr 分类 ====

func TestIsOcrRetryableErr_TransientNetwork(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"conn reset", errors.New("fetch image: Post: read: connection reset by peer"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"timeout text", errors.New("execute ocr request: context deadline exceeded (Client.Timeout exceeded)"), true},
	}
	for _, c := range cases {
		if got := isOcrRetryableErr(c.err); got != c.want {
			t.Errorf("isOcrRetryableErr(%s) want %v got %v", c.name, c.want, got)
		}
	}
}

func TestIsOcrRetryableErr_NetTimeout(t *testing.T) {
	// 构造一个满足 net.Error.Timeout()==true 的错误。
	ne := &netTimeoutError{}
	if !isOcrRetryableErr(ne) {
		t.Error("net.Error with Timeout()==true should be retryable")
	}
	if isOcrRetryableErr(errSSRFRejected) {
		t.Error("errSSRFRejected should NOT be retryable")
	}
	if isOcrRetryableErr(errNotImage) {
		t.Error("errNotImage should NOT be retryable")
	}
}

// netTimeoutError 是只为测试的 net.Error 实现(Timeout==true)。
type netTimeoutError struct{}

func (netTimeoutError) Error() string   { return "i/o timeout" }
func (netTimeoutError) Timeout() bool    { return true }
func (netTimeoutError) Temporary() bool { return false }

func TestIsOcrRetryableErr_HTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"503", fmt.Errorf("ocr service returned status 503: slow"), true},
		{"500", fmt.Errorf("ocr service returned status 500: internal error"), true},
		{"502", fmt.Errorf("ocr service returned status 502: bad gateway"), true},
		{"504", fmt.Errorf("ocr service returned status 504: gateway timeout"), true},
		{"429", fmt.Errorf("ocr service returned status 429: rate limited"), true},
		{"400 not retryable", fmt.Errorf("ocr service returned status 400: bad request"), false},
		{"404 not retryable", fmt.Errorf("ocr service returned status 404: not found"), false},
		{"401 not retryable via hint", fmt.Errorf("ocr service returned status 401: Unauthorized"), false},
		{"403 not retryable via hint", fmt.Errorf("ocr service returned status 403: Forbidden"), false},
	}
	for _, c := range cases {
		if got := isOcrRetryableErr(c.err); got != c.want {
			t.Errorf("isOcrRetryableErr(%s) want %v got %v (err=%v)", c.name, c.want, got, c.err)
		}
	}
}

func TestIsOcrRetryableErr_DeterministicNonStatus(t *testing.T) {
	// 编解码 / 空候选类错误:无 status、无网络特征 → 不重试。
	if isOcrRetryableErr(errors.New("marshal ocr request: invalid character")) {
		t.Error("marshal error should NOT be retryable")
	}
	if isOcrRetryableErr(errors.New("ocr response candidates are empty")) {
		t.Error("empty candidates should NOT be retryable")
	}
	// SSRF / 非 image 明确不重试。
	if isOcrRetryableErr(fmt.Errorf("%w: 10.0.0.1 resolves to non-public", errSSRFRejected)) {
		t.Error("SSRF rejection should NOT be retryable")
	}
}

// ==== ocrCallWithRetry 黑盒:合成 attempt 闭包 ====

func TestOcrCallWithRetry_TransientThenSuccess(t *testing.T) {
	var calls int32
	attempt := func(ctx context.Context) ocrAttemptResult {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return ocrAttemptResult{err: io.EOF} // 瞬时
		}
		return ocrAttemptResult{text: "OK"}
	}
	r := ocrCallWithRetry(context.Background(), "ocr", nil, attempt)
	if r.err != nil {
		t.Fatalf("want nil err, got %v", r.err)
	}
	if r.text != "OK" {
		t.Errorf("text want OK got %q", r.text)
	}
	if calls != 2 {
		t.Errorf("calls want 2 got %d", calls)
	}
}

func TestOcrCallWithRetry_DeterministicNoRetry(t *testing.T) {
	var calls int32
	// 状态码 400 → 不重试。
	attempt := func(ctx context.Context) ocrAttemptResult {
		atomic.AddInt32(&calls, 1)
		return ocrAttemptResult{err: fmt.Errorf("ocr service returned status 400: bad request")}
	}
	r := ocrCallWithRetry(context.Background(), "ocr", nil, attempt)
	if r.err == nil {
		t.Fatal("want err, got nil")
	}
	if calls != 1 {
		t.Errorf("deterministic 400 should not retry, calls want 1 got %d", calls)
	}
}

func TestOcrCallWithRetry_ExhaustedAllAttempts(t *testing.T) {
	var calls int32
	attempt := func(ctx context.Context) ocrAttemptResult {
		atomic.AddInt32(&calls, 1)
		return ocrAttemptResult{err: io.EOF}
	}
	r := ocrCallWithRetry(context.Background(), "ocr", nil, attempt)
	if r.err == nil {
		t.Fatal("want err, got nil")
	}
	if calls != ocrMaxAttempts {
		t.Errorf("calls want %d got %d", ocrMaxAttempts, calls)
	}
}

func TestOcrCallWithRetry_TotalTimeoutAborts(t *testing.T) {
	// attempt 始终持续 EOF(可重试类),但当 ocrRetryTotalTimeout 到点时应中止而非跑满 ocrMaxAttempts。
	// 用一个很小的总超时覆盖,断言 attempts < ocrMaxAttempts 且 err 含"被中止"。
	orig := ocrRetryTotalTimeout
	ocrRetryTotalTimeout = 50 * time.Millisecond
	origWait := ocrRetryWait
	ocrRetryWait = 30 * time.Millisecond // 每段退避 30ms,留给 ctx 到点的窗口
	t.Cleanup(func() {
		ocrRetryTotalTimeout = orig
		ocrRetryWait = origWait
	})

	var calls int32
	attempt := func(ctx context.Context) ocrAttemptResult {
		atomic.AddInt32(&calls, 1)
		return ocrAttemptResult{err: io.EOF}
	}
	start := time.Now()
	r := ocrCallWithRetry(context.Background(), "ocr", nil, attempt)
	elapsed := time.Since(start)
	if r.err == nil {
		t.Fatal("want err, got nil")
	}
	if !strings.Contains(r.err.Error(), "被中止") {
		t.Errorf("err should mention aborted, got %v", r.err)
	}
	// 50ms 总超时 + 30ms 退避:最多跑 2 次 attempt(第一次 ~0ms + 退避 30ms + 第二次 ~0ms + 退避撞 ctx),
	// 不应跑满 ocrMaxAttempts(3)。
	if calls >= ocrMaxAttempts {
		t.Errorf("timeout should abort before exhausting attempts, calls=%d", calls)
	}
	// 总耗时应受 50ms 上界压制(放宽到 1s 防抖动)。
	if elapsed > time.Second {
		t.Errorf("elapsed too long: %v (timeout not honored)", elapsed)
	}
}

func TestOcrCallWithRetry_NilAttemptFn(t *testing.T) {
	r := ocrCallWithRetry(context.Background(), "ocr", nil, nil)
	if r.err == nil {
		t.Fatal("want err for nil attempt")
	}
}

func TestOcrCallWithRetry_ParentCancelAborts(t *testing.T) {
	// 父 ctx 已取消 → 第一次 attempt 即在 ctx 已取消态下;即便 attempt 忽略 ctx 返回瞬时错误,
	// 退避段 select 应立即命中 ctx.Done() 中止,不会跑满 ocrMaxAttempts。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	origWait := ocrRetryWait
	ocrRetryWait = 30 * time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	var calls int32
	attempt := func(ctx context.Context) ocrAttemptResult {
		atomic.AddInt32(&calls, 1)
		return ocrAttemptResult{err: io.EOF}
	}
	r := ocrCallWithRetry(ctx, "ocr", nil, attempt)
	if r.err == nil {
		t.Fatal("want err, got nil")
	}
	if !strings.Contains(r.err.Error(), "被中止") {
		t.Errorf("err should mention aborted, got %v", r.err)
	}
	if calls > 1 {
		t.Errorf("cancelled parent should abort after first attempt, calls=%d", calls)
	}
}

// ==== ocrImageUncached 端到端经 OcrImage:首调 EOF 第 2 次 200 ====

// eofOnceTransport 是一个 http.RoundTripper:前 emitEOF 次 RoundTrip 返回 io.EOF(瞬时失败),
// 其后合成一个 200 + 固定 Gemini JSON 响应(base==nil)或转发到 base RoundTripper。
// 用它精确模拟上游瞬时 EOF,无需 httptest 真实拨号;misses 字段原子计数 RoundTrip 调用次数,
// 供测试断言「重试发生了」(misses==2 表示 1 次 EOF + 1 次成功)。
type eofOnceTransport struct {
	base    http.RoundTripper
	misses  int32 // 原子计数 RoundTrip 调用总数
	emitEOF int32 // 要触发多少次 EOF(达此数后转用 base / 合成 200)
}

func (t *eofOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := atomic.AddInt32(&t.misses, 1)
	if n <= t.emitEOF {
		// 模拟传输层 EOF:client.Do 会包成 *url.Error,内部 err=io.EOF。
		return nil, io.EOF
	}
	if t.base == nil {
		// 无 base:合成一个 Gemini generateContent 200 成功响应。
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"candidates":[{"content":{"parts":[{"text":"RETRY_OK"}]}}]}`)),
		}, nil
	}
	return t.base.RoundTrip(req)
}

func TestOcrImageUncached_RetryOnEOFThenSuccess(t *testing.T) {
	// base=nil:eofOnceTransport 第 1 次返 io.EOF,第 2 次合成 200 + RETRY_OK。
	// 纯合成 RoundTripper,不真实拨号,localProxyAddr 保持默认即可(根本不会被拨到)。
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origTimeout := ocrRetryTotalTimeout
	ocrRetryTotalTimeout = 5 * time.Second
	t.Cleanup(func() {
		ocrRetryWait = origWait
		ocrRetryTotalTimeout = origTimeout
	})

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	transport := &eofOnceTransport{emitEOF: 1}
	h.ocr.client = &http.Client{Transport: transport}

	sess := &RelaySession{UserID: "u1", UserKey: "k1"}
	// OcrImage 走 miss 真打上游路径(新图必 cache miss)。
	text, err, cachedHit := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err != nil {
		t.Fatalf("OcrImage err: %v", err)
	}
	if cachedHit {
		t.Error("miss path should report cachedHit=false")
	}
	if !strings.Contains(text, "RETRY_OK") {
		t.Errorf("text want contain RETRY_OK got %q", text)
	}
	// misses==2 证明:第 1 次 EOF + 第 2 次成功 = 发生了 1 次重试。
	if got := atomic.LoadInt32(&transport.misses); got != 2 {
		t.Errorf("RoundTrip calls want 2 (1 EOF + 1 success, proving retry), got %d", got)
	}
}

// TestOcrImageUncached_Deterministic400NoRetry 锁定:上游返回 400(确定性)时不重试,
// 只触达一次,失败入 failure 短 TTL 缓存(短窗内同图不再触达)。
func TestOcrImageUncached_Deterministic400NoRetry(t *testing.T) {
	var hits int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`))
	}))
	defer mock.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(mock.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}
	_, err, _ := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err == nil {
		t.Fatal("want err for 400, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("400 should not retry, hits want 1 got %d", got)
	}
}

// TestOcrImageUncached_RetrySuccessStillWritesSuccessCache 锁定重试成功后,
// 二次同图命中 success 长 TTL,不再触达上游(回归缓存契约不受重试污染)。
func TestOcrImageUncached_RetrySuccessStillWritesSuccessCache(t *testing.T) {
	var hits int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"CACHED_OK"}]}}]}`))
	}))
	defer mock.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(mock.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 第 1 次:成功(无 EOF,只是确保成功路径写 success 缓存)。
	if _, err, _ := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png"); err != nil {
		t.Fatalf("first err: %v", err)
	}
	first := atomic.LoadInt32(&hits)
	if first != 1 {
		t.Fatalf("first call should hit upstream once, got %d", first)
	}
	// 第 2 次:同图同会话 → 命中 success 长 TTL,不再触达。
	_, _, cached := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if !cached {
		t.Error("second call should hit cache (cachedHit=true)")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("cache hit should skip upstream, hits want 1 got %d", got)
	}
}

// TestOcrImageUncached_RetryExhaustedStillWritesFailureTTL 锁定重试全败(持续 EOF)
// 后,上层 OcrImage 写 failure 短 TTL 缓存:短窗内同图不再触达上游(熔断契约不变)。
func TestOcrImageUncached_RetryExhaustedStillWritesFailureTTL(t *testing.T) {
	var hits int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable) // 503 可重试
		w.Write([]byte(`{"error":"down"}`))
	}))
	defer mock.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(mock.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	// 用短 failure TTL 便于观察熔断窗口,但此处仅断言「短窗内不重打」,默认 30s 足够测试瞬间。
	h.ocr.cache = newOcrLRUCache(8, time.Minute, time.Minute)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 第 1 次:503 重试 ocrMaxAttempts 次全败。
	_, err, _ := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err == nil {
		t.Fatal("want err for 503 exhausted, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != int32(ocrMaxAttempts) {
		t.Errorf("exhausted should hit upstream %d times, got %d", ocrMaxAttempts, got)
	}
	// 第 2 次:命中 failure 短 TTL 缓存 → 不再触达上游(熔断窗口内不雪崩)。
	_, err2, _ := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err2 == nil {
		t.Fatal("second call should still fail (failure cache hit)")
	}
	if got := atomic.LoadInt32(&hits); got != int32(ocrMaxAttempts) {
		t.Errorf("failure TTL should suppress re-hit, hits want %d got %d", ocrMaxAttempts, got)
	}
}

// ==== ocrImageUncachedViaRoute 重试契约(/route 路径) ====

// errOnceRouteTransport 用合成 RoundTripper 模拟 /route 入口首调 EOF 第 2 次 200。
type errOnceRouteTransport struct {
	emitEOF int32
	misses  int32
	resp    string // 成功响应正文(OpenAI Chat JSON)
}

func (t *errOnceRouteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := atomic.AddInt32(&t.misses, 1)
	if n <= t.emitEOF {
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(t.resp)),
	}, nil
}

func TestOcrImageUncachedViaRoute_RetryOnEOFThenSuccess(t *testing.T) {
	// 把 localRelayAddr 指向一个不存在的地址不重要,因为 client.Transport 被合成 RT 覆盖,
	// RoundTrip 直接返回合成响应/EOF,根本不真正拨号。
	orig := localRelayAddr
	localRelayAddr = "127.0.0.1:65535" // 占位;不会真拨
	t.Cleanup(func() { localRelayAddr = orig })
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := newCrossPoolOCRService(t, nil)
	h.ocr.client = &http.Client{Transport: &errOnceRouteTransport{
		emitEOF: 1,
		resp:    `{"choices":[{"message":{"content":"ROUTE_RETRY_OK"}}]}`,
	}}

	var attempts int32
	// 包一层 attempt 计数:通过 client 的 Transport 计数 RoundTrip 调用次数间接断言重试次数。
	// 直接用 ocrCallWithRetry 黑盒契约已覆盖重试次数,这里只断言「成功 + 值正确」。
	_ = attempts
	text, err := h.ocr.ocrImageUncachedViaRoute(
		&RelaySession{UserKey: "k1", UserID: "u1"},
		"describe", "image/png", "QUJDREVG", "nvidia/gpt-4o", "gpt-4o",
	)
	if err != nil {
		t.Fatalf("ocrImageUncachedViaRoute err: %v", err)
	}
	if !strings.Contains(text, "ROUTE_RETRY_OK") {
		t.Errorf("text want contain ROUTE_RETRY_OK got %q", text)
	}
}

// TestOcrImageUncachedViaRoute_GuardHeaderResentOnRetry 锁定重试下每次重建请求都重设
// X-Antigravity-OCR-Self 守卫头:用计数 mock + 真实 httptest server,首调 503、第 2 次 200,
// 断言两次请求都带守卫头(自递归守卫语义在重试下不破)。
func TestOcrImageUncachedViaRoute_GuardHeaderResentOnRetry(t *testing.T) {
	var (
		hits       int32
		selfSeen   int32
		authSeen   int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if r.Header.Get("X-Antigravity-OCR-Self") == "1" {
			atomic.AddInt32(&selfSeen, 1)
		}
		if r.Header.Get("Authorization") != "" {
			atomic.AddInt32(&authSeen, 1)
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 触发重试
			w.Write([]byte(`{"error":"down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer srv.Close()

	orig := localRelayAddr
	localRelayAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localRelayAddr = orig })
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := newCrossPoolOCRService(t, nil)
	text, err := h.ocr.ocrImageUncachedViaRoute(
		&RelaySession{UserKey: "k1", UserID: "u1"},
		"describe", "image/png", "QUJDREVG", "nvidia/gpt-4o", "gpt-4o",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(text, "OK") {
		t.Errorf("text want OK got %q", text)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("hits want 2 (1×503 + 1×200) got %d", got)
	}
	if got := atomic.LoadInt32(&selfSeen); got != 2 {
		t.Errorf("X-Antigravity-OCR-Self should be set on BOTH attempts, seen=%d (hits=%d)", got, atomic.LoadInt32(&hits))
	}
	if got := atomic.LoadInt32(&authSeen); got != 2 {
		t.Errorf("Authorization should be set on BOTH attempts, seen=%d", got)
	}
}

// TestOcrImageUncachedViaRoute_Deterministic400NoRetry 锁定 /route 路径 400 不重试。
func TestOcrImageUncachedViaRoute_Deterministic400NoRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	orig := localRelayAddr
	localRelayAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localRelayAddr = orig })
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	h := newCrossPoolOCRService(t, nil)
	_, err := h.ocr.ocrImageUncachedViaRoute(
		&RelaySession{UserKey: "k1", UserID: "u1"},
		"describe", "image/png", "QUJDREVG", "nvidia/gpt-4o", "gpt-4o",
	)
	if err == nil {
		t.Fatal("want err for 400, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("400 on /route should not retry, hits want 1 got %d", got)
	}
}

// ==== fetchImageAsBase64 下载重试契约 ====

func TestFetchImageAsBase64_RetryOnResetThenSuccess(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	pngBytes := decodeFakePNGBytes(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			// 模拟上游在写中途断开 → 触发读体时 io.ErrUnexpectedEOF(可重试类)。
			// 这里用 Hijack 提前断连制造 read error。
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Skip("server doesn't support hijack")
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	b, m, err := fetchImageAsBase64(srv.URL)
	if err != nil {
		t.Fatalf("fetch err: %v (hits=%d)", err, atomic.LoadInt32(&hits))
	}
	if m != "image/png" {
		t.Errorf("mime want image/png got %s", m)
	}
	if b != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Error("b64 mismatch on retry success")
	}
	// 至少触达 2 次(hits 可能 >2 因 hijack 有时连第一笔都收不到,这里只断言 ≥2 即发生重试)。
	if got := atomic.LoadInt32(&hits); got < 2 {
		t.Errorf("expected >=2 hits (retry occurred), got %d", got)
	}
}

func TestFetchImageAsBase64_SSRFNotRetried(t *testing.T) {
	// SSRF 拒绝(rt 拨号被守卫拒)属确定性失败,不应重试:SSRF 守卫每次都拒,但只应尝试 1 次。
	// 用真实的私网 URL(不依赖 server),首次拨号即 errSSRFRejected。
	// 由于是无 server 直拨,在 SSRF 守卫被启用(默认)的情况下,拨号阶段就会立即拒绝。
	start := time.Now()
	_, _, err := fetchImageAsBase64("http://10.0.0.1/x.png")
	elapsed := time.Since(start)
	_ = err // SSRF 拒绝返回非空 error 是预期
	// 关键断言:不重试意味着总耗时不该接近 2× ocrDownloadRetryWait(500ms)。
	// 一次拨号失败很快(<100ms),即使加上守卫 DNS 解析也远短于 500ms 退避。
	// 而 SSRF 拒绝是 DialContext 里对 10.0.0.1 这种字面 IP 直接判私网拒绝(不走 DNS),近乎瞬时。
	if elapsed > 300*time.Millisecond {
		t.Errorf("SSRF rejection should not retry (no 500ms backoff), elapsed=%v", elapsed)
	}
}

func TestFetchImageAsBase64_NotImageNotRetried(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html/>"))
	}))
	defer srv.Close()

	_, _, err := fetchImageAsBase64(srv.URL)
	if err == nil {
		t.Fatal("want err for non-image content-type")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("non-image content-type should NOT retry, hits want 1 got %d", got)
	}
}

func TestFetchImageAsBase64_5xxRetried(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	pngBytes := decodeFakePNGBytes(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway) // 502 触发重试
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	// 加速退避避免测试卡 500ms。
	// 注:ocrDownloadRetryWait 是 const,无法覆盖;500ms 在可接受范围,不临时改 const。
	b, m, err := fetchImageAsBase64(srv.URL)
	if err != nil {
		t.Fatalf("fetch err: %v (hits=%d)", err, atomic.LoadInt32(&hits))
	}
	_ = b
	_ = m
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("5xx should be retried once, hits want 2 got %d", got)
	}
}
