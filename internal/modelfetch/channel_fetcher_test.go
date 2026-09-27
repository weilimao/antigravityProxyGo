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

func TestFetchChannelAvailableModels_WorkBuddyDualVersion(t *testing.T) {
	var domHeaderDomain, intlHeaderType string
	domServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domHeaderDomain = r.Header.Get("X-Domain")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"models": []map[string]any{
					{"id": "hunyuan-2.0-instruct"},
					{"id": "glm-5.1"},
					{"id": "deepseek-v4.1-flash"},
				},
			},
		})
	}))
	t.Cleanup(domServer.Close)

	intlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intlHeaderType = r.Header.Get("X-IDE-Type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"models": []map[string]any{
					{"id": "gpt-5.5"},
					{"id": "gemini-3.5-flash"},
					{"id": "deepseek-v4.1-flash"},
				},
			},
		})
	}))
	t.Cleanup(intlServer.Close)

	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)

	// 国内账号
	domAcc := account.NewWorkBuddyAccount(account.WorkBuddyAccountInput{
		Label:       "test@qq.com (国内版)",
		AccessToken: "dom-token",
		BaseURL:     domServer.URL,
	})
	mgr.AddAccount(domAcc)

	// 国际账号
	intlAcc := account.NewWorkBuddyAccount(account.WorkBuddyAccountInput{
		Label:       "user@gmail.com",
		AccessToken: "intl-token",
		BaseURL:     intlServer.URL,
	})
	mgr.AddAccount(intlAcc)

	models, err := FetchChannelAvailableModels(mgr, "workbuddy")
	if err != nil {
		t.Fatalf("FetchChannelAvailableModels failed: %v", err)
	}

	expectedModels := []string{"hunyuan-2.0-instruct", "glm-5.1", "deepseek-v4.1-flash", "gpt-5.5", "gemini-3.5-flash"}
	if len(models) != len(expectedModels) {
		t.Fatalf("expected %d models, got %d: %v", len(expectedModels), len(models), models)
	}

	modelMap := make(map[string]bool)
	for _, m := range models {
		modelMap[m] = true
	}
	for _, exp := range expectedModels {
		if !modelMap[exp] {
			t.Errorf("missing expected model: %s", exp)
		}
	}

	if domHeaderDomain != "www.workbuddy.cn" {
		t.Errorf("expected domestic header X-Domain to be www.workbuddy.cn, got %q", domHeaderDomain)
	}
	if intlHeaderType != "CodeBuddy" {
		t.Errorf("expected intl header X-IDE-Type to be CodeBuddy, got %q", intlHeaderType)
	}
}
