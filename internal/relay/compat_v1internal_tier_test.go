package relay

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/session"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestCompatV1Internal_PreferLowTierRouting 验证直连 Antigravity Relay 路径：
//   - 已有粘性会话（如已绑定高阶号 acc-high）请求低阶模型时保持原账号不拆号；
//   - 开启 PreferLowTierAccount 后，全新会话请求 gemini-3.8-flash 优先分配低阶号 acc-low；
//   - 当 acc-low 返回 403 失败需切号重试时，同一请求可平滑回退高阶号 acc-high。
func TestCompatV1Internal_PreferLowTierRouting(t *testing.T) {
	accMgr := account.NewManager()
	accMgr.Init(t.TempDir())
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)

	accHigh := &account.Account{
		ID: "acc-high", Email: "high@gmail.com", Provider: "antigravity", AccessToken: "tok-high", Enabled: true, ProjectID: "proj-high",
	}
	accLow := &account.Account{
		ID: "acc-low", Email: "low@gmail.com", Provider: "antigravity", AccessToken: "tok-low", Enabled: true, ProjectID: "proj-low",
	}
	accMgr.AddAccount(accHigh)
	accMgr.AddAccount(accLow)
	accMgr.SetAccountSupportedModels("acc-high", []string{"gemini-3.8-flash", "gemini-4-hydrogen-high"})
	accMgr.SetAccountSupportedModels("acc-low", []string{"gemini-3.8-flash"})

	router := session.NewRouter()
	h := NewAPICompatHandler(nil, accMgr, router, nil, nil, nil, nil)

	var mu sync.Mutex
	var usedAccounts []string
	failLowOnce := false

	h.finalRequester = func(a *account.Account, method, targetURL string, reqBody []byte) (*http.Response, error) {
		mu.Lock()
		usedAccounts = append(usedAccounts, a.ID)
		shouldFail := failLowOnce && a.ID == "acc-low"
		mu.Unlock()
		if shouldFail {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"permission denied"}}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}

	callRelay := func(sessID, model string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1internal:generateContent",
			strings.NewReader(`{"model":"`+model+`","project":"default","request":{"contents":[{"parts":[{"text":"hi"}]}]}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Session-Id", sessID)
		rec := httptest.NewRecorder()
		h.handleV1Internal(rec, req, &RelaySession{UserID: "user-1", SessionKey: sessID})
		return rec.Code
	}

	if err := accMgr.SetPreferLowTierAccount(true); err != nil {
		t.Fatalf("SetPreferLowTierAccount(true) failed: %v", err)
	}

	// 1. 会话 sess-relay-bound 已先由高阶号服务：同会话调用低阶模型 gemini-3.8-flash 仍留在 acc-high
	router.GetOrAssignAccount("sess-relay-bound", []*account.Account{accHigh}, nil)
	if code := callRelay("sess-relay-bound", "gemini-3.8-flash"); code != http.StatusOK {
		t.Fatalf("call 1 expected 200, got %d", code)
	}

	// 2. 全新未绑定会话 sess-relay-new 请求 gemini-3.8-flash -> 优先分配给低阶号 acc-low
	if code := callRelay("sess-relay-new", "gemini-3.8-flash"); code != http.StatusOK {
		t.Fatalf("call 2 expected 200, got %d", code)
	}

	// 3. 当 acc-low 在新会话中返回 403 失败触发单请求内切号 -> 回退到 acc-high 成功
	mu.Lock()
	failLowOnce = true
	mu.Unlock()
	if code := callRelay("sess-relay-fallback", "gemini-3.8-flash"); code != http.StatusOK {
		t.Fatalf("call 3 expected fallback 200, got %d", code)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"acc-high", "acc-low", "acc-low", "acc-high"}
	if len(usedAccounts) != len(want) {
		t.Fatalf("usedAccounts = %v, want %v", usedAccounts, want)
	}
	for i := range want {
		if usedAccounts[i] != want[i] {
			t.Errorf("attempt[%d] = %q, want %q (all=%v)", i, usedAccounts[i], want[i], usedAccounts)
		}
	}
}
