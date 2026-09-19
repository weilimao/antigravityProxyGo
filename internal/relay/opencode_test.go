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
