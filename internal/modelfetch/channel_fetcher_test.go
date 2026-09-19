package modelfetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestFetchOpenCodeModels_ServerSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "claude-sonnet-4-6"},
				{"id": "gpt-5"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	acc := &account.Account{
		BaseURL: ts.URL,
	}
	acc.SetAccessToken("sk-mock-token")

	models, err := fetchOpenCodeModels(acc)
	if err != nil {
		t.Fatalf("fetchOpenCodeModels returned error: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(models), models)
	}
	if models[0] != "claude-sonnet-4-6" || models[1] != "gpt-5" {
		t.Fatalf("unexpected models returned: %v", models)
	}
}

func TestFetchOpenCodeModels_FallbackToSupported(t *testing.T) {
	// Mock server that returns 404
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	acc := &account.Account{
		BaseURL: ts.URL,
	}
	acc.SetAccessToken("sk-mock-token")

	models, err := fetchOpenCodeModels(acc)
	if err != nil {
		t.Fatalf("fetchOpenCodeModels returned error on fallback: %v", err)
	}

	if len(models) != len(account.OpenCodeSupportedModels) {
		t.Fatalf("expected %d fallback models, got %d", len(account.OpenCodeSupportedModels), len(models))
	}
}

func TestFetchChannelAvailableModels_OpenCode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "opencode_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := account.NewManager()
	mgr.Init(tmpDir)

	in := account.OpenCodeAccountInput{
		BaseURL:     "",
		AccessToken: "sk-test-token-zen",
		Label:       "Zen Pool Account",
	}
	acc := account.NewOpenCodeAccount(in)
	mgr.AddAccount(acc)

	// Fetch via channel name "opencode"
	models, err := FetchChannelAvailableModels(mgr, "opencode")
	if err != nil {
		t.Fatalf("FetchChannelAvailableModels failed for opencode: %v", err)
	}

	if len(models) == 0 {
		t.Fatalf("expected models for opencode channel, got empty")
	}

	// Should include built-in fallback models like claude-sonnet-4-6
	foundClaude := false
	for _, m := range models {
		if m == "claude-sonnet-4-6" {
			foundClaude = true
			break
		}
	}
	if !foundClaude {
		t.Fatalf("expected claude-sonnet-4-6 in returned models, got: %v", models)
	}
}

func TestFetchChannelAvailableModels_NoAccount(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "opencode_empty_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := account.NewManager()
	mgr.Init(tmpDir)

	_, err = FetchChannelAvailableModels(mgr, "opencode")
	if err == nil {
		t.Fatalf("expected error when no account exists, got nil")
	}
}
