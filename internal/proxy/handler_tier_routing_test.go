package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"antigravity-proxy/internal/account"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestServeHTTP_PreferLowTierRouting_WithConcurrencyFallback 验证代理链路：
//   - 开启 PreferLowTierAccount 时，已有高阶粘性会话（或同客户端伴生请求 subSuffix==""）请求低阶模型仍留在原高阶号，杜绝同会话切号缓存失效；
//   - 全新独立低阶会话在无粘性绑定时优先分配给不支持高阶模型的低阶号 acc-low；
//   - 当低阶号并发打满时，新会话自动平滑回退到高阶号 acc-high；
//   - 关闭开关后新会话不再强行限定低阶子集。
func TestServeHTTP_PreferLowTierRouting_WithConcurrencyFallback(t *testing.T) {
	var mu sync.Mutex
	var observedAuths []string

	accMgr := account.NewManager()
	accMgr.Init(t.TempDir())
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)
	accMgr.SetAntigravityMaxConcurrency(1)

	addAntigravityAccount(accMgr, "acc-high", "high@pool", "tok-high")
	addAntigravityAccount(accMgr, "acc-low", "low@pool", "tok-low")
	accMgr.SetAccountSupportedModels("acc-high", []string{"gemini-3.5-flash-lite", "gemini-3.8-flash", "gemini-4-hydrogen-high"})
	accMgr.SetAccountSupportedModels("acc-low", []string{"gemini-3.5-flash-lite", "gemini-3.8-flash"})

	handler := newConcurrencyProxyHandler(t, accMgr, nil)
	handler.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			observedAuths = append(observedAuths, r.Header.Get("Authorization"))
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}}`)),
			}, nil
		}),
	}

	buildReq := func(authTok, model, extraSession string) *http.Request {
		body := `{"model":"` + model + `","request":{"contents":[{"parts":[{"text":"hi"}]}]}}`
		if extraSession != "" {
			body = `{"model":"` + model + `","sessionId":"` + extraSession + `","request":{"contents":[{"parts":[{"text":"hi"}]}]}}`
		}
		req := httptest.NewRequest(http.MethodPost,
			"https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent",
			strings.NewReader(body))
		req.Host = "daily-cloudcode-pa.googleapis.com"
		req.Header.Set("Authorization", "Bearer "+authTok)
		return req
	}

	sendReq := func(req *http.Request) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	if err := accMgr.SetPreferLowTierAccount(true); err != nil {
		t.Fatalf("SetPreferLowTierAccount(true) failed: %v", err)
	}

	// 1. 客户端 mainconv 先以高阶模型 gemini-4-hydrogen-high 建立对话，绑定到 acc-high
	sendReq(buildReq("user-same-sess", "gemini-4-hydrogen-high", "conv-main-1"))

	// 2. 同一对话触发无 sessionId 后缀的伴生低阶模型请求 gemini-3.5-flash-lite（截图中导致跨号错位的场景）：
	//    必须伴生收敛留在 acc-high，不得改切 acc-low！
	sendReq(buildReq("user-same-sess", "gemini-3.5-flash-lite", ""))

	// 3. 同一子会话直接降模型续跑 gemini-3.8-flash：同样必须留在 acc-high！
	sendReq(buildReq("user-same-sess", "gemini-3.8-flash", "conv-main-1"))

	// 4. 全新独立会话（未绑定且无活跃兄弟会话）请求 gemini-3.8-flash：优先分配给低阶号 acc-low
	sendReq(buildReq("user-fresh-low", "gemini-3.8-flash", "conv-fresh-1"))

	// 5. 全新独立会话遇到 acc-low 并发打满（limit=1）：平滑回退至 acc-high
	accMgr.AcquireAccount("acc-low")
	sendReq(buildReq("user-fresh-fallback", "gemini-3.8-flash", "conv-fresh-2"))
	accMgr.ReleaseAccount("acc-low")

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"Bearer tok-high", // 1. high-tier main conversation
		"Bearer tok-high", // 2. companion flash-lite stays on bound sibling account
		"Bearer tok-high", // 3. same child session keeps bound account
		"Bearer tok-low",  // 4. brand-new low-tier session routes to low-tier account
		"Bearer tok-high", // 5. fallback when low-tier account is concurrency-full
	}
	if len(observedAuths) != len(want) {
		t.Fatalf("observed %d upstream calls %v, want %v", len(observedAuths), observedAuths, want)
	}
	for i := range want {
		if observedAuths[i] != want[i] {
			t.Errorf("call[%d] auth = %q, want %q (all=%v)", i, observedAuths[i], want[i], observedAuths)
		}
	}
}
