package account

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTriggerTestResponse_AntigravityUserAgentAndArgonModel(t *testing.T) {
	var capturedUA string
	var capturedGoogClient string
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		capturedGoogClient = r.Header.Get("X-Goog-Api-Client")
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		// 模拟 Google 上游对 gemini-4-argon-* 的客户端版本门控：旧版 1.21.9 或 <2.25.0 返回 404
		if strings.Contains(capturedUA, "1.21.9") || !strings.HasPrefix(capturedUA, "antigravity/hub/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok_argon"}]}}]}}`))
	}))
	defer srv.Close()

	origEndpoint := triggerAntigravityEndpoint
	origClient := triggerHTTPClient
	triggerAntigravityEndpoint = srv.URL
	triggerHTTPClient = srv.Client()
	defer func() {
		triggerAntigravityEndpoint = origEndpoint
		triggerHTTPClient = origClient
	}()

	acc := &Account{
		ID:       "acc-argon-1",
		Email:    "weilimao0714@gmail.com",
		Provider: "antigravity",
	}

	// 1. 不传 getUserAgent 时，自动回退默认 DefaultAntigravityCliVersion (2.25.0)
	resp, err := TriggerTestResponse(
		context.Background(),
		acc,
		"gemini-4-argon-medium",
		"ok",
		func(email string) string { return "" },
		func(a *Account) (string, error) { return "test-access-token", nil },
	)
	if err != nil {
		t.Fatalf("expected TriggerTestResponse with default UA to succeed for gemini-4-argon-medium, got error: %v", err)
	}
	if resp != "ok_argon" {
		t.Errorf("expected response 'ok_argon', got %q", resp)
	}
	expectedDefaultUA := FormatAntigravityUserAgent(DefaultAntigravityCliVersion)
	if capturedUA != expectedDefaultUA {
		t.Errorf("expected default User-Agent %q, got %q", expectedDefaultUA, capturedUA)
	}
	if capturedGoogClient != "" {
		t.Errorf("expected X-Goog-Api-Client header to be absent, got %q", capturedGoogClient)
	}
	if capturedBody["project"] != "favorable-synapse-ttvcb" {
		t.Errorf("expected default fallback project 'favorable-synapse-ttvcb', got %v", capturedBody["project"])
	}
	if capturedBody["model"] != "gemini-4-argon-medium" {
		t.Errorf("expected model 'gemini-4-argon-medium', got %v", capturedBody["model"])
	}
	reqInner, _ := capturedBody["request"].(map[string]interface{})
	if reqInner == nil {
		t.Fatalf("expected inner request map, got %T", capturedBody["request"])
	}
	sid, _ := reqInner["sessionId"].(string)
	if !strings.HasPrefix(sid, "-") {
		t.Errorf("expected inner request.sessionId to be negative decimal int64 string matching OfficialSessionID, got %q", sid)
	}

	// 2. 传入 Manager.GetAntigravityUserAgent 动态版本 (如 2.26.0)
	m := NewManager()
	m.antigravityCliVersion = "2.26.0"

	resp2, err2 := TriggerTestResponse(
		context.Background(),
		acc,
		"gemini-4-argon-medium",
		"ok",
		func(email string) string { return "custom-proj-123" },
		func(a *Account) (string, error) { return "test-access-token", nil },
		m.GetAntigravityUserAgent,
	)
	if err2 != nil {
		t.Fatalf("expected TriggerTestResponse with custom UA 2.26.0 to succeed, got error: %v", err2)
	}
	if resp2 != "ok_argon" {
		t.Errorf("expected response 'ok_argon', got %q", resp2)
	}
	expectedCustomUA := FormatAntigravityUserAgent("2.26.0")
	if capturedUA != expectedCustomUA {
		t.Errorf("expected custom User-Agent %q, got %q", expectedCustomUA, capturedUA)
	}
	if capturedBody["project"] != "custom-proj-123" {
		t.Errorf("expected stored project 'custom-proj-123', got %v", capturedBody["project"])
	}
}
