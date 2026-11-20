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
					{"id": "hunyuan-2.0-instruct-custom"},
					{"id": "glm-5.1"},
					{"id": "deepseek-v4.1-flash"},
					{"id": "deepseek-r1-0528-lkeap"}, // 应被过滤的失效内部模型
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
					{"id": "gpt-5.5-custom"},
					{"id": "gemini-3.5-flash-custom"},
					{"id": "deepseek-v4.1-flash"},
					{"id": "kimi-k2-instruct-taiji"}, // 应被过滤的失效内部模型
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

	modelMap := make(map[string]bool)
	for _, m := range models {
		modelMap[m] = true
	}

	// 验证必须包含国内核心基准模型
	if !modelMap["deepseek-v4.1-flash"] {
		t.Errorf("missing expected model deepseek-v4.1-flash")
	}
	if !modelMap["deepseek-v4-flash"] {
		t.Errorf("missing expected model deepseek-v4-flash")
	}

	// 验证远端自定义模型被成功合并追加
	if !modelMap["hunyuan-2.0-instruct-custom"] {
		t.Errorf("missing remote merged model hunyuan-2.0-instruct-custom")
	}
	if !modelMap["gpt-5.5-custom"] {
		t.Errorf("missing remote merged model gpt-5.5-custom")
	}

	// 验证已失效的内部模型被严格过滤
	if modelMap["deepseek-r1-0528-lkeap"] {
		t.Errorf("stale model deepseek-r1-0528-lkeap should have been filtered out")
	}
	if modelMap["kimi-k2-instruct-taiji"] {
		t.Errorf("stale model kimi-k2-instruct-taiji should have been filtered out")
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
					{"id": "hunyuan-2.0-instruct-extra"},
					{"id": "deepseek-v4.1-flash"},
					{"id": "codewise-completions"}, // 应过滤
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
					{"id": "gpt-5.5-extra"},
					{"id": "deepseek-v4.1-flash"},
					{"id": "default-1.1"}, // 应过滤
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
	// 验证合并了基准模型且追加了额外远端模型
	domMap := make(map[string]bool)
	for _, m := range details.DomModels {
		domMap[m] = true
	}
	if !domMap["deepseek-v4.1-flash"] || !domMap["hunyuan-2.0-instruct-extra"] {
		t.Fatalf("国内模型缺失核心或额外模型: %v", details.DomModels)
	}
	if domMap["codewise-completions"] {
		t.Fatalf("国内模型未能过滤内部补全模型 codewise-completions")
	}

	intlMap := make(map[string]bool)
	for _, m := range details.IntlModels {
		intlMap[m] = true
	}
	if !intlMap["deepseek-v4.1-flash"] || !intlMap["gpt-5.5-extra"] {
		t.Fatalf("国际模型缺失核心或额外模型: %v", details.IntlModels)
	}
	if intlMap["default-1.1"] {
		t.Fatalf("国际模型未能过滤默认占位模型 default-1.1")
	}
}

func TestFetchWorkBuddyAvailableModelsDetailed_LiveIntegration(t *testing.T) {
	// 如果用户本地生产数据目录存在，执行一次真实的只读端到端验证
	dataDir := `D:\antigravityProxy\data`
	accountsFile := `D:\antigravityProxy\data\accounts_workbuddy.json`
	if _, err := os.Stat(accountsFile); os.IsNotExist(err) {
		t.Skip("本地 accounts_workbuddy.json 不存在，跳过线上集成验证")
	}

	mgr := account.NewManager()
	mgr.Init(dataDir)

	details, err := FetchWorkBuddyAvailableModelsDetailed(mgr)
	if err != nil {
		t.Fatalf("真实环境调用 FetchWorkBuddyAvailableModelsDetailed 失败: %v", err)
	}

	t.Logf("成功获取国内版模型数: %d, 国际版模型数: %d, 全量去重后模型数: %d",
		len(details.DomModels), len(details.IntlModels), len(details.AllModels))

	hasV41Flash := false
	for _, m := range details.AllModels {
		if m == "deepseek-v4.1-flash" {
			hasV41Flash = true
			break
		}
	}
	if !hasV41Flash {
		t.Fatalf("关键期望: 获取的模型列表中必须包含 deepseek-v4.1-flash，但未找到！当前模型: %v", details.AllModels)
	}

	// 确认失效模型已被成功剔除
	for _, m := range details.AllModels {
		if m == "deepseek-r1-0528-lkeap" || m == "kimi-k2-instruct-taiji" || m == "completion-gf" {
			t.Fatalf("列表中不应包含已失效的内部模型: %s", m)
		}
	}
}

func TestIsStaleOrInternalWorkBuddyModel(t *testing.T) {
	staleCases := []string{
		"deepseek-r1-0528-lkeap",
		"kimi-k2-instruct-taiji",
		"codewise-completions",
		"codewise-rewrite",
		"completion-gf",
		"hunyuan-completion",
		"test-completions",
		"default-1.1",
		"default-1.2",
		"",
	}
	for _, c := range staleCases {
		if !isStaleOrInternalWorkBuddyModel(c) {
			t.Errorf("isStaleOrInternalWorkBuddyModel(%q) = false, want true", c)
		}
	}

	validCases := []string{
		"deepseek-v4.1-flash",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"deepseek-v3-2-volc",
		"hunyuan-standard",
		"glm-5.1",
		"qwen3-coder-pro",
	}
	for _, c := range validCases {
		if isStaleOrInternalWorkBuddyModel(c) {
			t.Errorf("isStaleOrInternalWorkBuddyModel(%q) = true, want false", c)
		}
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
