package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

func TestOpenCodeCanonicalIdentifiers(t *testing.T) {
	// 1. Session ID generation
	for i := 0; i < 20; i++ {
		sid := generateOpenCodeSessionID()
		if !opencodeSessionRe.MatchString(sid) {
			t.Fatalf("Session ID %s does not match canonical regex", sid)
		}
		if len(sid) != 30 {
			t.Fatalf("Session ID %s length expected 30, got %d", sid, len(sid))
		}
	}

	// 2. Request ID generation
	for i := 0; i < 20; i++ {
		rid := generateOpenCodeRequestID()
		if !strings.HasPrefix(rid, "msg_") {
			t.Fatalf("Request ID %s does not start with msg_", rid)
		}
		if len(rid) != 30 {
			t.Fatalf("Request ID %s length expected 30, got %d", rid, len(rid))
		}
	}

	// 3. Translation
	valid := "ses_f534dfae8ffeCy4Ee4tLWNygDc"
	if translateToOpenCodeSession(valid) != valid {
		t.Errorf("expected already valid session to be preserved")
	}

	foreign := "claude:550e8400-e29b-41d4-a716-446655440000"
	tr1 := translateToOpenCodeSession(foreign)
	tr2 := translateToOpenCodeSession(foreign)
	if tr1 != tr2 {
		t.Errorf("expected deterministic translation, got %s vs %s", tr1, tr2)
	}
	if !opencodeSessionRe.MatchString(tr1) {
		t.Errorf("translated session %s does not match regex", tr1)
	}
}

func TestOpenCodePathPrefix(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/opencode/v1/chat/completions", true},
		{"/opencode/v1/models", true},
		{"/opencode", true},
		{"/oc/v1/chat/completions", true},
		{"/oc/v1/models", true},
		{"/oc", true},
		{"/opencodetest", false},
		{"/octest", false},
		{"/v1/chat/completions", false},
	}

	for _, tt := range tests {
		if got := opencodeAliasPrefixMatch(tt.path); got != tt.want {
			t.Errorf("opencodeAliasPrefixMatch(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestOpenCodeRelay_MockUpstream(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_relay_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	var receivedUA, receivedClient, receivedSession, receivedSessionID, receivedRequest, receivedAccept, receivedAuth string
	var hasSessionID, hasRequestHeader bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		receivedClient = r.Header.Get("x-opencode-client")
		receivedSession = r.Header.Get("x-opencode-session")
		receivedSessionID = r.Header.Get("x-opencode-session-id")
		receivedRequest = r.Header.Get("x-opencode-request")
		receivedAccept = r.Header.Get("Accept")
		hasSessionID = receivedSessionID != ""
		hasRequestHeader = receivedRequest != ""
		receivedAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "claude-sonnet-4-6",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Hello from OpenCode Zen mock!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		})
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)

	_, err = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-mock-key-12345",
		Label:        "MockAccount",
		DefaultModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("AddOpenCodeAccount failed: %v", err)
	}

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	reqBody := []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "hello"}],
		"stream": false
	}`)

	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test-sess"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if receivedUA != defaultOpenCodeUA {
		t.Errorf("expected User-Agent %s, got %s", defaultOpenCodeUA, receivedUA)
	}
	if !strings.Contains(receivedUA, "ai-sdk/provider-utils") || !strings.Contains(receivedUA, "runtime/bun") {
		t.Errorf("expected User-Agent to contain ai-sdk and runtime/bun, got %s", receivedUA)
	}
	if receivedClient != "cli" {
		t.Errorf("expected x-opencode-client 'cli', got %s", receivedClient)
	}
	if receivedAccept != "*/*" {
		t.Errorf("expected Accept '*/*', got %s", receivedAccept)
	}
	if !opencodeSessionRe.MatchString(receivedSession) {
		t.Errorf("expected canonical x-opencode-session, got %s", receivedSession)
	}
	if !hasSessionID {
		t.Errorf("expected x-opencode-session-id header to be present")
	}
	if receivedSessionID != receivedSession {
		t.Errorf("expected x-opencode-session-id to match x-opencode-session, got %s vs %s", receivedSessionID, receivedSession)
	}
	if !hasRequestHeader || !strings.HasPrefix(receivedRequest, "msg_") {
		t.Errorf("expected x-opencode-request header with msg_ prefix, got %s", receivedRequest)
	}
	if receivedAuth != "Bearer sk-test-mock-key-12345" {
		t.Errorf("expected Authorization 'Bearer sk-test-mock-key-12345', got %s", receivedAuth)
	}
}

func TestOpenCodeRelay_FailoverOn429(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_failover_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		auth := r.Header.Get("Authorization")
		if auth == "Bearer sk-key-1" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-failover",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "Success on Key 2"}}},
			"usage":   map[string]interface{}{"total_tokens": 10},
		})
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)

	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-key-1",
		Label:        "Account1",
		DefaultModel: "claude-sonnet-4-6",
	})
	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-key-2",
		Label:        "Account2",
		DefaultModel: "claude-sonnet-4-6",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	reqBody := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "sess-failover"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected failover to succeed with status 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Success on Key 2") {
		t.Errorf("expected response from Key 2, got %s", w.Body.String())
	}
}

func TestOpenCode_ModelLevelCooldown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_model_cd_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Model == "claude-sonnet-4-6" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"CreditsError","message":"No payment method. Add a payment method here: https://opencode.ai/billing"}}`))
			return
		}

		// 免费模型分支：按上游端点返回对应协议格式。
		// 说明(2026-09-27):
		//   - 非 muse-spark 免费模型走上游 /chat/completions, 中继把客户端非流式
		//     请求强制转流式并本地聚合, 故需返回 Chat SSE 帧;
		//   - muse-spark-* 实测仅支持 Responses API, 中继改用 /responses 端点并透传
		//     Responses 协议, 故需返回 Responses SSE 帧, 否则聚合器报 empty_stream。
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: {"type":"response.created","response":{"id":"resp_free","model":"muse-spark-1.3-contributor-free","created_at":1700000000}}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"Free model success"}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_free","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}` + "\n\n"))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl-free","object":"chat.completion.chunk","model":"mimo-v2.5-free","choices":[{"index":0,"delta":{"role":"assistant","content":"Free model success"},"finish_reason":null}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl-free","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)

	accID, err := mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-key",
		Label:        "SingleAccount",
		DefaultModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("AddOpenCodeAccount failed: %v", err)
	}

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	// 1. 请求收费模型，预期 401 失败并进入该模型的冷却
	paidReq := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)))
	w1 := httptest.NewRecorder()
	h.handleOpenCode(w1, paidReq, &RelaySession{Token: "sess-1"})

	acc := mgr.GetAccountByID(accID)
	if acc == nil {
		t.Fatalf("account not found")
	}
	if acc.NoQuota {
		t.Errorf("expected acc.NoQuota to remain false for model-level failure, got true")
	}
	if acc.Cooldowns["opencode:claude-sonnet-4-6"] == 0 {
		t.Errorf("expected cooldown on opencode:claude-sonnet-4-6, got 0")
	}

	// 2. 请求免费模型，同一账号应仍然可用且成功响应
	freeReq := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader([]byte(`{"model":"muse-spark-1.3-contributor-free","messages":[{"role":"user","content":"hi"}]}`)))
	w2 := httptest.NewRecorder()
	h.handleOpenCode(w2, freeReq, &RelaySession{Token: "sess-2"})

	if w2.Code != http.StatusOK {
		t.Fatalf("expected free model request to succeed with 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "Free model success") {
		t.Errorf("expected Free model success, got %s", w2.Body.String())
	}
}

func TestOpenCode_ClientHeadersPreserved(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_headers_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	var gotSession, gotRequest, gotClient, gotProject string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-opencode-session")
		gotRequest = r.Header.Get("x-opencode-request")
		gotClient = r.Header.Get("x-opencode-client")
		gotProject = r.Header.Get("x-opencode-project")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-headers",
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "ok"}}},
			"usage":   map[string]interface{}{"total_tokens": 5},
		})
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)

	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-key",
		Label:        "HeaderAccount",
		DefaultModel: "claude-sonnet-4-6",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("x-opencode-session", "ses_f42b98c78ffewlvAPaXLlNAbxm")
	req.Header.Set("x-opencode-request", "msg_0bd8a6d4d001Z3I78J2D7IMyWp")
	req.Header.Set("x-opencode-client", "cli")
	req.Header.Set("x-opencode-project", "my-project")
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotSession != "ses_f42b98c78ffewlvAPaXLlNAbxm" {
		t.Errorf("expected preserved session, got %s", gotSession)
	}
	if gotRequest != "msg_0bd8a6d4d001Z3I78J2D7IMyWp" {
		t.Errorf("expected preserved request, got %s", gotRequest)
	}
	if gotClient != "cli" {
		t.Errorf("expected preserved client 'cli', got %s", gotClient)
	}
	if gotProject != "my-project" {
		t.Errorf("expected preserved project 'my-project', got %s", gotProject)
	}
}

func TestOpenCodeRelay_AllAccounts429_PreservesError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_429_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage limit reached"}}`))
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)

	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-single",
		Label:        "SingleAccount",
		DefaultModel: "deepseek-v4.1-flash-free",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader([]byte(`{"model":"deepseek-v4.1-flash-free","messages":[{"role":"user","content":"hi"}]}`)))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test"})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 status code preserved, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "FreeUsageLimitError") {
		t.Errorf("expected FreeUsageLimitError preserved in response body, got %s", w.Body.String())
	}
}

func TestOpenCodeRelay_BaseURL_Normalization(t *testing.T) {
	// 验证 NormalizeOpenCodeBaseURL 行为：zen / zen/v1 及空串自动规整为官方最新推断端点 DefaultOpenCodeBaseURL
	expectedDefault := account.DefaultOpenCodeBaseURL
	if norm := account.NormalizeOpenCodeBaseURL("https://opencode.ai/zen/v1"); norm != expectedDefault {
		t.Errorf("expected zen/v1 to normalize to %s, got %s", expectedDefault, norm)
	}
	if norm := account.NormalizeOpenCodeBaseURL("https://opencode.ai/zen"); norm != expectedDefault {
		t.Errorf("expected zen to normalize to %s, got %s", expectedDefault, norm)
	}
	if norm := account.NormalizeOpenCodeBaseURL(""); norm != expectedDefault {
		t.Errorf("expected empty to normalize to %s, got %s", expectedDefault, norm)
	}
	custom := "https://my-custom-proxy.internal/v1"
	if norm := account.NormalizeOpenCodeBaseURL(custom); norm != custom {
		t.Errorf("expected custom url preserved %s, got %s", custom, norm)
	}
}

func TestOpenCodeRelay_Cooldown_Returns429_Not503(t *testing.T) {
	mgr := account.NewManager()
	accID, err := mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      "https://opencode.ai/zen/v1",
		AccessToken:  "oc_sk_test",
		DefaultModel: "deepseek-v4.1-flash-free",
	})
	if err != nil {
		t.Fatalf("failed to add account: %v", err)
	}

	future := time.Now().UnixNano()/1e6 + 60000
	mgr.SetAccountCooldownForChannel(accID, future, "opencode", "deepseek-v4.1-flash-free")

	handler := &APICompatHandler{
		accountMgr: mgr,
	}

	// 1. 普通请求在号池冷却时应返回 429 且带有 Retry-After 头，绝不返回误导性 503
	reqBody := []byte(`{"model":"deepseek-v4.1-flash-free","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.handleOpenCode(w, req, &RelaySession{UserID: "test_user"})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 status code when all accounts cooling down, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "opencode_pool_cooldown") {
		t.Errorf("expected opencode_pool_cooldown in error body, got %s", w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header present")
	}

	// 2. 测速请求（带 X-Antigravity-Benchmark: 1）在冷却时应具备探活特权，允许尝试上游
	var upstreamCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(sw http.ResponseWriter, sr *http.Request) {
		upstreamCalled = true
		sw.Header().Set("Content-Type", "text/event-stream")
		sw.WriteHeader(http.StatusOK)
		_, _ = sw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer ts.Close()

	testAcc := mgr.GetAccountByID(accID)
	testAcc.BaseURL = ts.URL

	benchReq := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	benchReq.Header.Set("Content-Type", "application/json")
	benchReq.Header.Set("X-Antigravity-Benchmark", "1")
	wBench := httptest.NewRecorder()

	handler.handleOpenCode(wBench, benchReq, &RelaySession{UserID: "test_user", IsBenchmark: true})

	if !upstreamCalled {
		t.Fatalf("expected benchmark request to probe upstream even during cooldown")
	}
	if wBench.Code != http.StatusOK {
		t.Fatalf("expected benchmark probe to succeed 200, got %d: %s", wBench.Code, wBench.Body.String())
	}
	// 探活成功后自愈清除了冷却
	if testAcc.CooldownUntil != 0 {
		t.Errorf("expected cooldown to be cleared on 200 OK, got CooldownUntil=%d", testAcc.CooldownUntil)
	}
}

func TestOpenCodeURLNormalizationInRelay(t *testing.T) {
	// 验证各种历史 baseUrl 均能统一归一化为官方 API 端点
	cases := []struct {
		input string
		want  string
	}{
		{"", account.DefaultOpenCodeBaseURL},
		{"https://opencode.ai/zen", account.DefaultOpenCodeBaseURL},
		{"https://opencode.ai/zen/v1", account.DefaultOpenCodeBaseURL},
		{"https://opencode.ai/zen/v1/", account.DefaultOpenCodeBaseURL},
		{"https://opencode.ai/inference/openai/v1", account.DefaultOpenCodeBaseURL},
		{"https://my-custom.endpoint.com/v1", "https://my-custom.endpoint.com/v1"},
	}

	for _, tc := range cases {
		got := account.NormalizeOpenCodeBaseURL(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeOpenCodeBaseURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}



