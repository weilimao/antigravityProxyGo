package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

	var receivedUA, receivedClient, receivedSession, receivedRequest, receivedAuth string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		receivedClient = r.Header.Get("x-opencode-client")
		receivedSession = r.Header.Get("x-opencode-session")
		receivedRequest = r.Header.Get("x-opencode-request")
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
	if receivedClient != "desktop" {
		t.Errorf("expected x-opencode-client 'desktop', got %s", receivedClient)
	}
	if !opencodeSessionRe.MatchString(receivedSession) {
		t.Errorf("expected canonical x-opencode-session, got %s", receivedSession)
	}
	if !strings.HasPrefix(receivedRequest, "msg_") {
		t.Errorf("expected x-opencode-request with msg_, got %s", receivedRequest)
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

