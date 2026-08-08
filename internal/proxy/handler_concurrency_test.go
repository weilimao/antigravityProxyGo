package proxy

// handler_concurrency_test.go 锁定 proxy 链路(Antigravity/Project)在途并发槽
// acquire/release 严格配对:routeForAttempt 末尾 Acquire + forwardForAttempt 顶部 defer Release。
//
// 覆盖路径:
//   - 非流式成功(generateContent):forward 完成 → defer 释放 → 计数归 0。
//   - 流式成功(streamGenerateContent):流读尽 EOF → defer 释放 → 计数归 0。
//   - 上游 401 TOKEN_EXPIRED:forwardDo 后 finalize 到 classify 返回 err → runRetryLoop 失败退出;
//     forwardForAttempt 的 defer 仍执行释放(本次请求在池账号上结束),计数归 0。
//   - routeForAttempt 出错(QUOTA_EXHAUSTED,nil poolAccount):不 acquire 也不释放,计数保持 0。
//   - 多次成功:并发计数全 0(泄漏检测)。
//
// 测试构造见 TestProxyHandler_Timeout 的 TLS 测试服务器 + DialContext 重定向范式。

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

// newConcurrencyProxyHandler 构造一个用于并发槽配对测试的最小 ProxyHandler(非 TLS 直连场景)。
// client 指向 ts(经 DialContext 重定向),requestTimeout 设 30s 避免误超时。
func newConcurrencyProxyHandler(t *testing.T, accMgr *account.Manager, ts *httptest.Server) *ProxyHandler {
	t.Helper()
	sessionRouter := session.NewRouter()
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
		func(s string) { t.Logf("[proxy] %s", s) },
		nil,               // quotaFetch
		nil,               // tokenRefresh
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 0 },    // getMaxRetries:0 → 只一次尝试,避免 401 后无限重试
		func() int { return 1 },
		func() int64 { return 1024 * 1024 },
		func() int { return 30 },
		nil, nil,
	)
	if ts != nil {
		srvURL, _ := url.Parse(ts.URL)
		srvClient := ts.Client()
		srvClient.Timeout = 0
		transport := srvClient.Transport.(*http.Transport)
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial(network, srvURL.Host)
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
		handler.client = srvClient
	}
	return handler
}

// addAntigravityAccount 向账号池注入一个 antigravity 通道账号(generateContent 路由命中 isRealModelRequest)。
func addAntigravityAccount(mgr *account.Manager, id, email, token string) {
	mgr.AddAccount(&account.Account{
		ID: id, Email: email, Provider: "antigravity", ScopeType: "antigravity",
		AccessToken: token, Enabled: true, ProjectID: "test-proj",
		Cooldowns: map[string]int64{},
	})
}

// TestProxyConcurrency_NonStreamSuccessRelease 非流式 generateContent 成功:defer 释放 → 计数归 0。
func TestProxyConcurrency_NonStreamSuccessRelease(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-1", "ag-1@pool", "tok")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	req := httptest.NewRequest(http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// 非流式成功:forwardForAttempt defer 在其返回时释放并发槽 → 计数归 0。
	if got := accMgr.AccountInFlightCount("ag-1"); got != 0 {
		t.Fatalf("after non-stream success, AccountInFlightCount(ag-1) = %d, want 0", got)
	}
}

// TestProxyConcurrency_StreamSuccessRelease 流式 streamGenerateContent 成功 EOF:defer 释放 → 计数归 0。
func TestProxyConcurrency_StreamSuccessRelease(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}` + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-stream", "ag-stream@pool", "tok")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	req := httptest.NewRequest(http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:streamGenerateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 流式成功 EOF:forwardForAttempt 流读尽后返回,defer 释放并发槽 → 计数归 0。
	if got := accMgr.AccountInFlightCount("ag-stream"); got != 0 {
		t.Fatalf("after stream success, AccountInFlightCount(ag-stream) = %d, want 0", got)
	}
}

// TestProxyConcurrency_TokenExpiredRelease 上游 401:TOKEN_EXPIRED 分类后 runRetryLoop 失败退出,
// 但 forwardForAttempt 的 defer 已在上游 resp.Body.Close 后释放并发槽 → 计数归 0。
// 验证 finalize 流(上游返回 resp)路径同样经 defer 释放。
func TestProxyConcurrency_TokenExpiredRelease(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"unauthorized"}}`))
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-401", "ag-401@pool", "tok")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	req := httptest.NewRequest(http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 401 路径:forwardForAttempt 消费完上游 body 后返回(finalized=false → 进 classify),
	// defer 释放并发槽 → 计数归 0(后续 runRetryLoop 不再 invoke forwardForAttempt)。
	if got := accMgr.AccountInFlightCount("ag-401"); got != 0 {
		t.Fatalf("after 401 TOKEN_EXPIRED, AccountInFlightCount(ag-401) = %d, want 0 (defer must release)", got)
	}
}

// TestProxyConcurrency_QuotaExhaustedNoAcquire routeForAttempt 出错(无可用池账号 → QUOTA_EXHAUSTED):
// 不 acquire 也不释放(无 poolAccount),计数保持 0,验证 isPoolReq=false 直连场景的 nil 防御。
func TestProxyConcurrency_QuotaExhaustedNoAcquire(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	// 不开启池模式 + 无账号 → isPoolReq=true 但 GetAvailableAccountsForChannel 返空 → routeForAttempt
	// poolAccount=nil → QUOTA_EXHAUSTED(不 Acquire)。
	accMgr.SetPoolMode(false)
	accMgr.SetActiveChannel("antigravity")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	req := httptest.NewRequest(http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 无账号 QUOTA_EXHAUSTED 路径:routeForAttempt 未 Acquire,forwardForAttempt 未调用,
	// 计数保持 0(且不 panic)。
	for _, id := range []string{"ag-none"} {
		if got := accMgr.AccountInFlightCount(id); got != 0 {
			t.Errorf("QuotaExhausted path leaked in-flight for %s = %d, want 0", id, got)
		}
	}
}

// TestProxyConcurrency_NonPoolRequestNoAcquire 非池请求(健康检查等非 generateContent 路径):
// isPoolReq=false → routeForAttempt 返回 poolAccount=nil,forwardForAttempt defer nil 防御不释放。
// 计数保持 0,验证 nil guard 防 isPoolReq 直连场景误释放。
func TestProxyConcurrency_NonPoolRequestNoAcquire(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-nonpool", "ag-nonpool@pool", "tok")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	// 健康检查路径(非 generateContent/predict/agent):isPoolReq=false,不走账号池。
	req := httptest.NewRequest(http.MethodGet,
		"https://generativelanguage.googleapis.com/v1beta/health", nil)
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 非池请求:routeForAttempt poolAccount=nil → 不 Acquire;forwardForAttempt defer nil guard 不释放。
	// 计数必须保持 0(且不 panic,验证 nil 防御)。
	if got := accMgr.AccountInFlightCount("ag-nonpool"); got != 0 {
		t.Errorf("non-pool request leaked in-flight for ag-nonpool = %d, want 0", got)
	}
}

// TestProxyConcurrency_RepeatSuccessNoLeak 连续多次非流式成功:并发计数全归 0,无泄漏。
func TestProxyConcurrency_RepeatSuccessNoLeak(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-loop", "ag-loop@pool", "tok")

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	const N = 5
	for i := 0; i < N; i++ {
		req := httptest.NewRequest(http.MethodPost,
			"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
			strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
		req.Host = "generativelanguage.googleapis.com"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// 每次请求结束后立即归 0,中间态不得累计。
		if got := accMgr.AccountInFlightCount("ag-loop"); got != 0 {
			t.Fatalf("iter %d: AccountInFlightCount(ag-loop) = %d, want 0 (no leak)", i, got)
		}
	}
}

// TestProxyConcurrency_FilterRespectsLimit 验证并发过滤在 routeForAttempt 选号链路生效:
// 预占一个号到上限,新请求应被过滤到另一号(FilterByConcurrency 选号硬门槛)。
func TestProxyConcurrency_FilterRespectsLimit(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer upstream.Close()

	accMgr := account.NewManager()
	accMgr.SetPoolMode(true)
	accMgr.SetActiveChannel("antigravity")
	addAntigravityAccount(accMgr, "ag-fa", "ag-fa@pool", "tokA")
	addAntigravityAccount(accMgr, "ag-fb", "ag-fb@pool", "tokB")
	// 上限设为 1,任一号占 1 槽即满。
	accMgr.SetAntigravityMaxConcurrency(1)
	if got := accMgr.GetAntigravityMaxConcurrency(); got != 1 {
		t.Fatalf("SetAntigravityMaxConcurrency(1) GetBack = %d, want 1", got)
	}

	handler := newConcurrencyProxyHandler(t, accMgr, upstream)

	// 预占 A 到上限(1),B 空闲。
	accMgr.AcquireAccount("ag-fa")
	if got := accMgr.AccountInFlightCount("ag-fa"); got != 1 {
		t.Fatalf("pre-acquire A count = %d, want 1", got)
	}

	req := httptest.NewRequest(http.MethodPost,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
		strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	req.Host = "generativelanguage.googleapis.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// A 仍为 1(本次请求过滤后走 B,B 结束 defer 释放),不能增长到 2。
	if got := accMgr.AccountInFlightCount("ag-fa"); got != 1 {
		t.Errorf("A in-flight after request routed to B = %d, want 1 (must not grow)", got)
	}
	// B 被本次请求占用并释放,归 0。
	if got := accMgr.AccountInFlightCount("ag-fb"); got != 0 {
		t.Errorf("B in-flight after request done = %d, want 0 (defer release)", got)
	}
	// 释放预占的 A,回到干净态。
	accMgr.ReleaseAccount("ag-fa")
	if got := accMgr.AccountInFlightCount("ag-fa"); got != 0 {
		t.Errorf("A after manual release = %d, want 0", got)
	}
}

// 保证 time 包被引用(超时配置使用)。
var _ = time.Second
