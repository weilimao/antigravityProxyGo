package modelfetch

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestProbeAccountsModels_MultiAccountFilteringAndMissingTag(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)
	mgr.SetAntigravityCliVersion("2.25.0")

	// 账号 1: 全配额 Pro 账号，包含全部 Argon 及 Claude 5.5
	hasArgonTrue := true
	acc1 := &account.Account{
		ID:             "acc-1",
		Email:          "pro_full@example.com",
		Provider:       "antigravity",
		Tier:           "Pro",
		AccessToken:    "tok-acc1",
		Enabled:        true,
		HasArgonBucket: &hasArgonTrue,
	}
	mgr.AddAccount(acc1)

	// 账号 2: 普通 Free 账号，无 Argon 和 Claude 5.5
	hasArgonFalse := false
	acc2 := &account.Account{
		ID:             "acc-2",
		Email:          "free_standard@example.com",
		Provider:       "antigravity",
		Tier:           "Free",
		AccessToken:    "tok-acc2",
		Enabled:        true,
		HasArgonBucket: &hasArgonFalse,
	}
	mgr.AddAccount(acc2)

	// 账号 3: 具备 Claude 5.5 High 但明确无 Argon 桶的账号
	acc3 := &account.Account{
		ID:             "acc-3",
		Email:          "pro_no_argon@example.com",
		Provider:       "antigravity",
		Tier:           "Pro",
		AccessToken:    "tok-acc3",
		Enabled:        true,
		HasArgonBucket: &hasArgonFalse,
	}
	mgr.AddAccount(acc3)

	geminiInternalTransport = mockRTFunc(func(req *http.Request) (*http.Response, error) {
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		wHeader := make(http.Header)
		wHeader.Set("Content-Type", "application/json")

		var body string
		switch tok {
		case "tok-acc1":
			body = `{
				"models": {
					"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
					"gemini-4-argon-high": {"displayName": "Gemini 4 Argon High", "disabled": false},
					"gemini-4-argon-medium": {"displayName": "Gemini 4 Argon Medium", "disabled": false},
					"gemini-4-argon-low": {"displayName": "Gemini 4 Argon Low", "disabled": false},
					"claude-opus-5-5-high": {"displayName": "Claude Opus 5.5 High", "disabled": false},
					"claude-opus-5-5-medium": {"displayName": "Claude Opus 5.5 Medium", "disabled": false},
					"claude-opus-5-5-low": {"displayName": "Claude Opus 5.5 Low", "disabled": false},
					"claude-sonnet-5-5-high": {"displayName": "Claude Sonnet 5.5 High", "disabled": false}
				}
			}`
		case "tok-acc2":
			body = `{
				"models": {
					"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
					"gemini-2.5-pro": {"displayName": "Gemini 2.5 Pro", "disabled": false}
				}
			}`
		case "tok-acc3":
			body = `{
				"models": {
					"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
					"claude-opus-5-5-high": {"displayName": "Claude Opus 5.5 High", "disabled": false}
				}
			}`
		default:
			body = `{"models": {}}`
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

	resp := ProbeAccountsModels(mgr, nil, nil, "antigravity", nil)
	if !resp.Success {
		t.Fatalf("ProbeAccountsModels failed: %s", resp.Error)
	}

	if resp.Summary.TotalAccounts != 3 {
		t.Errorf("expected 3 total accounts, got %d", resp.Summary.TotalAccounts)
	}
	if resp.Summary.ArgonSupportedCount != 1 {
		t.Errorf("expected 1 account supporting Argon, got %d", resp.Summary.ArgonSupportedCount)
	}
	if resp.Summary.Opus55SupportedCount != 2 {
		t.Errorf("expected 2 accounts supporting Opus 5.5, got %d", resp.Summary.Opus55SupportedCount)
	}

	resultMap := make(map[string]AccountModelCapabilityInfo)
	for _, res := range resp.Results {
		resultMap[res.ID] = res
	}

	// 验证 acc-1 (仅缺 acc-2 独有的 gemini-2.5-pro，其余 8 个核心模型全部具备)
	res1 := resultMap["acc-1"]
	if len(res1.MissingModels) != 1 || res1.MissingModels[0] != "gemini-2.5-pro" {
		t.Errorf("acc-1 should dynamically only miss [gemini-2.5-pro], got: %v", res1.MissingModels)
	}
	if res1.SupportedCount != 8 {
		t.Errorf("acc-1 should have 8 supported models, got: %d", res1.SupportedCount)
	}

	// 验证 acc-2 (无 Argon 无 Claude)
	res2 := resultMap["acc-2"]
	if len(res2.MissingModels) != len(DefaultCoreGatedModels) {
		t.Errorf("acc-2 should miss all %d core gated models, got: %v", len(DefaultCoreGatedModels), res2.MissingModels)
	}

	// 验证 acc-3 (支持 opus-5-5-high，但缺失 argon 系列及其他 opus-5-5)
	res3 := resultMap["acc-3"]
	containsOpusHigh := false
	for _, m := range res3.MissingModels {
		if m == "claude-opus-5-5-high" {
			containsOpusHigh = true
		}
	}
	if containsOpusHigh {
		t.Errorf("acc-3 supports claude-opus-5-5-high, it should not be in MissingModels")
	}

	containsArgonHigh := false
	for _, m := range res3.MissingModels {
		if m == "gemini-4-argon-high" {
			containsArgonHigh = true
		}
	}
	if !containsArgonHigh {
		t.Errorf("acc-3 should miss gemini-4-argon-high, but missing list is: %v", res3.MissingModels)
	}

	// 验证 AccountManager 中的持久化 SupportedModels 已同步写入
	savedAcc1 := mgr.GetAccountByID("acc-1")
	if savedAcc1 == nil || len(savedAcc1.SupportedModels) != 8 {
		t.Errorf("acc-1 SupportedModels not updated in account manager")
	}
}

func TestProbeAccountsModels_401Retry(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)

	acc := &account.Account{
		ID:          "acc-expire",
		Email:       "expire@example.com",
		Provider:    "antigravity",
		AccessToken: "old-token",
		Enabled:     true,
	}
	mgr.AddAccount(acc)

	refreshCalled := false
	refreshTokenFunc := func(a *account.Account) (string, error) {
		refreshCalled = true
		return "refreshed-token", nil
	}

	callCount := 0
	geminiInternalTransport = mockRTFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		wHeader := make(http.Header)
		wHeader.Set("Content-Type", "application/json")

		if tok == "old-token" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     wHeader,
				Body:       io.NopCloser(strings.NewReader(`{"error": "Unauthorized"}`)),
			}, nil
		}

		body := `{"models": {"gemini-2.5-flash": {"displayName": "Flash"}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     wHeader,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		geminiInternalTransport = nil
	}()

	resp := ProbeAccountsModels(mgr, nil, refreshTokenFunc, "antigravity", []string{"acc-expire"})
	if !resp.Success {
		t.Fatalf("expected probe success after 401 refresh, got err: %s", resp.Error)
	}
	if !refreshCalled {
		t.Errorf("expected refreshToken to be called on 401")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (initial + retry), got %d", callCount)
	}
	if len(resp.Results) != 1 || resp.Results[0].SupportedCount != 1 {
		t.Errorf("expected 1 result with 1 model, got %+v", resp.Results)
	}
}

func TestProbeAccountsModels_TargetAccountFilter(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)

	mgr.AddAccount(&account.Account{ID: "acc-a", Email: "a@example.com", Provider: "antigravity", AccessToken: "tok-a", Enabled: true})
	mgr.AddAccount(&account.Account{ID: "acc-b", Email: "b@example.com", Provider: "antigravity", AccessToken: "tok-b", Enabled: true})

	geminiInternalTransport = mockRTFunc(func(req *http.Request) (*http.Response, error) {
		wHeader := make(http.Header)
		wHeader.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     wHeader,
			Body:       io.NopCloser(strings.NewReader(`{"models": {"gemini-2.5-flash": {}}}`)),
		}, nil
	})
	defer func() {
		geminiInternalTransport = nil
	}()

	// 仅探测 acc-b
	resp := ProbeAccountsModels(mgr, nil, nil, "antigravity", []string{"acc-b"})
	if !resp.Success {
		t.Fatalf("Probe failed: %s", resp.Error)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].ID != "acc-b" {
		t.Errorf("expected probed account ID 'acc-b', got '%s'", resp.Results[0].ID)
	}
}

func TestProbeAccountsModels_DynamicDifferentiatedModels(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tmpDir)
	mgr.SetAntigravityCliVersion("2.31.2")

	mgr.AddAccount(&account.Account{ID: "acc-new", Email: "new@example.com", Provider: "antigravity", Tier: "Pro", AccessToken: "tok-new", Enabled: true})
	mgr.AddAccount(&account.Account{ID: "acc-old", Email: "old@example.com", Provider: "antigravity", Tier: "Pro", AccessToken: "tok-old", Enabled: true})

	geminiInternalTransport = mockRTFunc(func(req *http.Request) (*http.Response, error) {
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		wHeader := make(http.Header)
		wHeader.Set("Content-Type", "application/json")
		var body string
		if tok == "tok-new" {
			body = `{
				"models": {
					"gemini-2.5-flash": {"disabled": false},
					"gemini-4-hydrogen-high": {"disabled": false},
					"gemini-4-hydrogen-medium": {"disabled": false},
					"gemini-4-hydrogen-low": {"disabled": false},
					"gemini-4-hydrogen-xhigh": {"disabled": false},
					"gemini-flash-worker-agy": {"disabled": false},
					"gemini-5-nova-pro": {"disabled": false}
				}
			}`
		} else {
			body = `{
				"models": {
					"gemini-2.5-flash": {"disabled": false},
					"claude-opus-4-6-thinking": {"disabled": false},
					"claude-sonnet-4-6": {"disabled": false}
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

	resp := ProbeAccountsModels(mgr, nil, nil, "antigravity", nil)
	if !resp.Success {
		t.Fatalf("ProbeAccountsModels failed: %s", resp.Error)
	}

	if resp.Summary.ValidProbedCount != 2 {
		t.Fatalf("expected ValidProbedCount=2, got %d", resp.Summary.ValidProbedCount)
	}
	if len(resp.Summary.AllPoolModels) != 9 {
		t.Fatalf("expected 9 pool models, got %d: %v", len(resp.Summary.AllPoolModels), resp.Summary.AllPoolModels)
	}
	if len(resp.Summary.CommonModels) != 1 || resp.Summary.CommonModels[0] != "gemini-2.5-flash" {
		t.Errorf("expected CommonModels=[gemini-2.5-flash], got %v", resp.Summary.CommonModels)
	}
	// 全池共有 8 个差异模型（含 Hydrogen 4档、worker、nova、以及反向 4.6 两款）
	if len(resp.Summary.DifferentiatedModels) != 8 {
		t.Errorf("expected 8 DifferentiatedModels, got %d: %v", len(resp.Summary.DifferentiatedModels), resp.Summary.DifferentiatedModels)
	}

	// 验证家族聚合是否包含 gemini-4-hydrogen (1/2)
	foundHydrogenFam := false
	for _, fam := range resp.Summary.FamilyCoverage {
		if fam.Family == "gemini-4-hydrogen" {
			foundHydrogenFam = true
			if !fam.IsDifferentiated || fam.SupportedCount != 1 || fam.TotalProbed != 2 || len(fam.Models) != 4 {
				t.Errorf("unexpected gemini-4-hydrogen family coverage: %+v", fam)
			}
		}
	}
	if !foundHydrogenFam {
		t.Errorf("expected gemini-4-hydrogen in FamilyCoverage, got %+v", resp.Summary.FamilyCoverage)
	}

	resMap := make(map[string]AccountModelCapabilityInfo)
	for _, r := range resp.Results {
		resMap[r.ID] = r
	}

	// acc-new 应缺失 claude-opus-4-6-thinking 和 claude-sonnet-4-6
	if len(resMap["acc-new"].MissingModels) != 2 {
		t.Errorf("acc-new expected 2 missing models (4.6 series), got %v", resMap["acc-new"].MissingModels)
	}
	// acc-old 应缺失 hydrogen 4档 + worker + nova 共 6 个新模型
	if len(resMap["acc-old"].MissingModels) != 6 {
		t.Errorf("acc-old expected 6 missing models, got %v", resMap["acc-old"].MissingModels)
	}
}
