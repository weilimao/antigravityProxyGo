package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestAccountSessionIDAndRequestIDFormat(t *testing.T) {
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	reSessionID := regexp.MustCompile(`^-[0-9]{19}$`)
	reReqID := regexp.MustCompile(`^agent/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	s1 := account.AccountSessionID("acc-alpha", "conv-seed-1")
	s1Repeat := account.AccountSessionID("acc-alpha", "conv-seed-1")
	s2 := account.AccountSessionID("acc-beta", "conv-seed-1")

	if !reSessionID.MatchString(s1) {
		t.Fatalf("expected 19-digit negative decimal sessionId, got %q", s1)
	}
	if _, err := strconv.ParseInt(s1, 10, 64); err != nil {
		t.Fatalf("sessionId %q is not a valid int64: %v", s1, err)
	}
	if s1 != s1Repeat {
		t.Fatalf("expected deterministic sessionId for same account+seed, got %q vs %q", s1, s1Repeat)
	}
	if s1 == s2 {
		t.Fatalf("expected distinct sessionIds across different accounts for the same client seed, both got %q", s1)
	}

	reqID1 := account.NewAntigravityRequestID("agent")
	reqID2 := account.NewAntigravityRequestID("agent")
	if !reReqID.MatchString(reqID1) {
		t.Fatalf("requestId %q does not match RFC 4122 UUIDv4 agent/<uuid> format", reqID1)
	}
	if reqID1 == reqID2 {
		t.Fatalf("expected unique requestIds across calls, both got %q", reqID1)
	}
}

func TestSanitizeConflictingClientHeaders(t *testing.T) {
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	h := make(http.Header)
	h.Set("Authorization", "Bearer ya29.valid")
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "antigravity/hub/2.25.0 (aidev_client; os_type=windows; arch=amd64)")
	h.Set("X-Client-Custom-Header", "keep-me")

	// 注入各类冲突浏览器头、第三方 SDK 头与代理痕迹头
	conflicting := map[string]string{
		"Sec-Ch-Ua":                `"Chromium";v="130"`,
		"Sec-Fetch-Mode":           "cors",
		"X-Client-Data":            "CJa2yQE=",
		"X-Stainless-Lang":         "go",
		"X-Stainless-Os":           "Windows",
		"Anthropic-Version":        "2023-06-01",
		"Anthropic-Beta":           "prompt-caching-2024-07-31",
		"OpenAI-Beta":              "assistants=v2",
		"X-Claude-Code-Session-Id": "claude-sess-1",
		"X-Codex-Turn-Metadata":    "turn-meta",
		"X-Session-Id":             "sess-123",
		"Session-Id":               "sess-456",
		"Thread-Id":                "thread-789",
		"X-Forwarded-For":          "10.0.0.1",
		"Via":                      "1.1 proxy",
		"Cf-Ray":                   "88888888-LAX",
	}
	for k, v := range conflicting {
		h.Set(k, v)
	}

	sanitizeConflictingClientHeaders(h)

	for k := range conflicting {
		if got := h.Get(k); got != "" {
			t.Errorf("expected conflicting header %q to be stripped, but still got %q", k, got)
		}
	}

	if h.Get("Authorization") != "Bearer ya29.valid" {
		t.Errorf("expected Authorization preserved, got %q", h.Get("Authorization"))
	}
	if h.Get("X-Client-Custom-Header") != "keep-me" {
		t.Errorf("expected X-Client-Custom-Header preserved, got %q", h.Get("X-Client-Custom-Header"))
	}
}

func TestRewriteAntigravityBodyIdentityOnRetry(t *testing.T) {
	tempDir := t.TempDir()
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)

	acc1 := &account.Account{
		ID:          "acc-retry-1",
		Email:       "retry1@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-retry-1",
		Enabled:     true,
	}
	acc2 := &account.Account{
		ID:          "acc-retry-2",
		Email:       "retry2@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-retry-2",
		Enabled:     true,
	}
	accMgr.AddAccount(acc1)
	accMgr.AddAccount(acc2)

	storedProjects := map[string]string{
		"retry1@example.com": "gcp-proj-acc-1",
		"retry2@example.com": "gcp-proj-acc-2",
	}

	type attemptCapture struct {
		auth      string
		project   string
		requestID string
		sessionID string
	}
	var captures []attemptCapture

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		var parsed struct {
			Project   string `json:"project"`
			RequestID string `json:"requestId"`
			Request   struct {
				SessionID string `json:"sessionId"`
			} `json:"request"`
		}
		_ = json.Unmarshal(b, &parsed)
		captures = append(captures, attemptCapture{
			auth:      req.Header.Get("Authorization"),
			project:   parsed.Project,
			requestID: parsed.RequestID,
			sessionID: parsed.Request.SessionID,
		})

		// 第 1 次尝试返回 429 触发切号重试；第 2 次尝试返回 200 OK
		if len(captures) == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":429,"message":"Resource has been exhausted"}}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}}`)),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		session.NewRouter(),
		stats.NewTracker(pricing.NewManager()),
		stats.NewUsageTracker(pricing.NewManager()),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(string) {},
		func(*account.Account) (*account.QuotaResult, error) { return nil, nil },
		nil,
		func(string, string) {},
		func(email string) string { return storedProjects[email] },
		func() int { return 2 },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 },
		nil, nil,
	)
	h.client = &http.Client{Transport: mockTransport}

	origReqID := "agent/11111111-2222-4333-8444-555555555555"
	origSessID := "-3750763034362895579"
	rawBody := []byte(`{"project":"client-orig-project","model":"gemini-2.5-flash","requestId":"` + origReqID + `","request":{"sessionId":"` + origSessID + `","contents":[{"parts":[{"text":"hello"}]}]}}`)

	req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(rawBody))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("User-Agent", "antigravity/hub/2.25.0 (aidev_client; os_type=windows; arch=amd64)")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ya29.client-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected retry to succeed with 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(captures))
	}

	c1, c2 := captures[0], captures[1]
	if c1.auth == c2.auth {
		t.Fatalf("expected retry to switch to a different account, both used %q", c1.auth)
	}
	if c1.project == c2.project || c1.project == "client-orig-project" || c2.project == "client-orig-project" {
		t.Fatalf("expected per-account project alignment, got attempt1=%q attempt2=%q", c1.project, c2.project)
	}
	if c1.sessionID == origSessID || c2.sessionID == origSessID || c1.sessionID == c2.sessionID {
		t.Fatalf("expected isolated per-account decimal sessionId, got attempt1=%q attempt2=%q (orig=%q)", c1.sessionID, c2.sessionID, origSessID)
	}
	if c1.requestID != origReqID {
		t.Fatalf("expected attempt 0 to preserve initial requestId %q, got %q", origReqID, c1.requestID)
	}
	if c2.requestID == origReqID || !strings.HasPrefix(c2.requestID, "agent/") {
		t.Fatalf("expected retry attempt 1 on backup account to regenerate fresh UUIDv4 requestId, got %q", c2.requestID)
	}
}

func TestV1InternalPeripheralGuard(t *testing.T) {
	tempDir := t.TempDir()
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)
	accMgr.AddAccount(&account.Account{
		ID:          "acc-guard-1",
		Email:       "guard1@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.pool-guard-token",
		ProjectID:   "guard-project-1",
		Enabled:     true,
	})

	upstreamHits := 0
	var lastUpstreamAuth string
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		upstreamHits++
		lastUpstreamAuth = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"cloudaicompanionProject":"guard-project-1"}`)),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		session.NewRouter(),
		stats.NewTracker(pricing.NewManager()),
		stats.NewUsageTracker(pricing.NewManager()),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(string) {},
		nil, nil,
		func(string, string) {},
		func(string) string { return "guard-project-1" },
		func() int { return 0 },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 },
		nil, nil,
	)
	h.client = &http.Client{Transport: mockTransport}

	// 1. recordCodeAssistMetrics 在携带真实 Google Token 时也必须被本地 200 OK 静默消化，严禁发往上游
	metricsReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:recordCodeAssistMetrics", strings.NewReader(`{"metrics":[]}`))
	metricsReq.Host = "daily-cloudcode-pa.googleapis.com"
	metricsReq.Header.Set("Authorization", "Bearer ya29.ide-logged-in-token")
	metricsRec := httptest.NewRecorder()
	h.ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for recordCodeAssistMetrics, got %d", metricsRec.Code)
	}
	if upstreamHits != 0 {
		t.Fatalf("expected recordCodeAssistMetrics to be intercepted locally, but hit upstream %d times", upstreamHits)
	}

	// 2. 免登录模式下调用 loadCodeAssist，应自动走号池账号 Token 而非将无效占位 Token 泄露给上游
	loadReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", strings.NewReader(`{"metadata":{"ideName":"antigravity"}}`))
	loadReq.Host = "daily-cloudcode-pa.googleapis.com"
	loadReq.Header.Set("Content-Type", "application/json")
	loadReq.Header.Set("Authorization", "Bearer dummy-non-google-token")
	loadRec := httptest.NewRecorder()
	h.ServeHTTP(loadRec, loadReq)

	if loadRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for login-free loadCodeAssist, got %d", loadRec.Code)
	}
	if upstreamHits != 1 || lastUpstreamAuth != "Bearer ya29.pool-guard-token" {
		t.Fatalf("expected login-free loadCodeAssist to use pool account token, got hits=%d auth=%q", upstreamHits, lastUpstreamAuth)
	}
}

func TestRewriteAntigravityNumericSessionID(t *testing.T) {
	acc := &account.Account{
		ID:    "acc-numeric-test",
		Email: "numeric@example.com",
	}

	// 模拟客户端原始请求中的 sessionId 为裸数字形式（如 -3750763034362895579）
	rawPayload := []byte(`{"project":"old-proj","request":{"sessionId":-3750763034362895579,"contents":[]}}`)

	rewritten, changed := rewriteNativePassthroughIdentity(
		rawPayload,
		acc,
		"new-proj",
		"old-proj",
		"client-seed",
		0,
		true,
	)

	if !changed {
		t.Fatalf("expected rewriteNativePassthroughIdentity to modify payload")
	}

	var parsed struct {
		Project string `json:"project"`
		Request struct {
			SessionID string `json:"sessionId"`
		} `json:"request"`
	}
	if err := json.Unmarshal(rewritten, &parsed); err != nil {
		t.Fatalf("unmarshal rewritten json failed: %v, raw=%s", err, string(rewritten))
	}

	if parsed.Project != "new-proj" {
		t.Errorf("expected project to be new-proj, got %s", parsed.Project)
	}

	expectedSessionID := account.AccountSessionID("acc-numeric-test", "client-seed")
	if parsed.Request.SessionID != expectedSessionID {
		t.Errorf("expected sessionId %q, got %q", expectedSessionID, parsed.Request.SessionID)
	}

	if !strings.HasPrefix(parsed.Request.SessionID, "-") || len(parsed.Request.SessionID) != 20 {
		t.Errorf("expected derived sessionId to be 20 chars with negative sign, got %s", parsed.Request.SessionID)
	}
}

