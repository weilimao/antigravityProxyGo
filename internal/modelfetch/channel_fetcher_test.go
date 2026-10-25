package modelfetch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestFetchWorkBuddyAvailableModelsDetailed(t *testing.T) {
	domServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"models": []map[string]any{
					{"id": "hunyuan-2.0-instruct"},
					{"id": "deepseek-v4.1-flash"},
				},
			},
		})
	}))
	t.Cleanup(domServer.Close)

	intlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"models": []map[string]any{
					{"id": "gpt-5.5"},
					{"id": "deepseek-v4.1-flash"},
				},
			},
		})
	}))
	t.Cleanup(intlServer.Close)

	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)

	domAcc := account.NewWorkBuddyAccount(account.WorkBuddyAccountInput{
		Label:       "wb@qq.com (国内版)",
		AccessToken: "dom-token-xyz",
		BaseURL:     domServer.URL,
	})
	mgr.AddAccount(domAcc)

	intlAcc := account.NewWorkBuddyAccount(account.WorkBuddyAccountInput{
		Label:       "wb@gmail.com",
		AccessToken: "intl-token-xyz",
		BaseURL:     intlServer.URL,
	})
	mgr.AddAccount(intlAcc)

	details, err := FetchWorkBuddyAvailableModelsDetailed(mgr)
	if err != nil {
		t.Fatalf("FetchWorkBuddyAvailableModelsDetailed 失败: %v", err)
	}
	if !details.HasDomestic || !details.HasIntl {
		t.Fatalf("期望 HasDomestic 与 HasIntl 均为 true, 实际: dom=%v, intl=%v", details.HasDomestic, details.HasIntl)
	}
	if len(details.DomModels) != 2 || len(details.IntlModels) != 2 {
		t.Fatalf("期望双端各有 2 个模型, 实际: dom=%d, intl=%d", len(details.DomModels), len(details.IntlModels))
	}
	if len(details.AllModels) != 3 {
		t.Fatalf("期望聚合去重后共 3 个模型, 实际: %d (%v)", len(details.AllModels), details.AllModels)
	}
}


type mockRTFunc func(req *http.Request) (*http.Response, error)

func (f mockRTFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchChannelAvailableModels_GoogleArgonPriority(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)
	mgr.SetAntigravityCliVersion("2.25.0")

	// 账号 1: 排在前面的 Free 账号
	accFree := &account.Account{
		ID:          "acc-free",
		Email:       "free@example.com",
		Provider:    "antigravity",
		Tier:        "Free",
		AccessToken: "free-token",
		Enabled:     true,
	}
	mgr.AddAccount(accFree)

	// 账号 2: 排在后面的 Pro 账号
	accPro := &account.Account{
		ID:          "acc-pro",
		Email:       "pro@example.com",
		Provider:    "antigravity",
		Tier:        "Pro",
		AccessToken: "pro-token",
		Enabled:     true,
	}
	mgr.AddAccount(accPro)

	var capturedUAs []string
	var probedTokens []string

	geminiInternalTransport = mockRTFunc(func(req *http.Request) (*http.Response, error) {
		capturedUAs = append(capturedUAs, req.Header.Get("User-Agent"))
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		probedTokens = append(probedTokens, tok)

		wHeader := make(http.Header)
		wHeader.Set("Content-Type", "application/json")

		var body string
		if tok == "pro-token" {
			body = `{
				"models": {
					"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
					"gemini-4-argon-high": {"displayName": "Gemini 4 Argon (High)", "disabled": false},
					"gemini-4-argon-medium": {"displayName": "Gemini 4 Argon (Medium)", "disabled": false},
					"gemini-4-argon-low": {"displayName": "Gemini 4 Argon (Low)", "disabled": false}
				}
			}`
		} else {
			body = `{
				"models": {
					"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
					"gemini-4-argon-disabled": {"displayName": "Gemini 4 Argon", "disabled": true}
				}
			}`
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     wHeader,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		geminiInternalTransport = nil
	}()

	models, err := FetchChannelAvailableModels(mgr, "antigravity")
	if err != nil {
		t.Fatalf("FetchChannelAvailableModels failed: %v", err)
	}

	// 1. 验证 Pro 账号优先探测，发往上游的 UA 为当前配置的动态 Hub 2.25.0
	if len(capturedUAs) == 0 {
		t.Fatal("expected at least one upstream request, got 0")
	}
	expectedUA := mgr.GetAntigravityUserAgent()
	if capturedUAs[0] != expectedUA {
		t.Errorf("captured UA = %q, want %q", capturedUAs[0], expectedUA)
	}

	// 验证排在第一位探测的是 Pro 账号（pro-token）
	if probedTokens[0] != "pro-token" {
		t.Errorf("probed tokens: want pro-token first, got: %v", probedTokens)
	}

	// 2. 验证返回的可用模型包含了全部 3 个 Argon 模型，且剔除了 gemini-4-argon-disabled
	modelMap := make(map[string]bool)
	for _, m := range models {
		modelMap[m] = true
	}

	if !modelMap["gemini-4-argon-high"] {
		t.Errorf("missing gemini-4-argon-high in %v", models)
	}
	if !modelMap["gemini-4-argon-medium"] {
		t.Errorf("missing gemini-4-argon-medium in %v", models)
	}
	if !modelMap["gemini-4-argon-low"] {
		t.Errorf("missing gemini-4-argon-low in %v", models)
	}
	if modelMap["gemini-4-argon-disabled"] {
		t.Errorf("unexpected gemini-4-argon-disabled present in %v", models)
	}
}
