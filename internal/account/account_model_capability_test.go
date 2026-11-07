package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountModelCapabilityFilterAnd404Memory(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)
	mgr.SetActiveChannel("antigravity")
	mgr.SetPoolMode(true)

	// 1. 准备 3 个账号：
	// - accArgon: 经配额桶识别出 HasArgonBucket = true，且 SupportedModels 包含 gemini-4-argon-medium 与 claude-opus-5-5-high
	// - accNoArgon: 经配额桶识别出 HasArgonBucket = false，且 SupportedModels 仅包含 gemini-2.5-flash 与 claude-sonnet-4-6
	// - accUnknown: 尚未采集 SupportedModels 与 HasArgonBucket 的新账号
	accArgon := &Account{
		ID:          "acc-argon",
		Email:       "weilimao0714@gmail.com",
		Provider:    "antigravity",
		AccessToken: "tok-argon",
		Enabled:     true,
	}
	accNoArgon := &Account{
		ID:          "acc-no-argon",
		Email:       "weilimao666@gmail.com",
		Provider:    "antigravity",
		AccessToken: "tok-no-argon",
		Enabled:     true,
	}
	accUnknown := &Account{
		ID:          "acc-unknown",
		Email:       "unknown@gmail.com",
		Provider:    "antigravity",
		AccessToken: "tok-unknown",
		Enabled:     true,
	}
	mgr.AddAccount(accArgon)
	mgr.AddAccount(accNoArgon)
	mgr.AddAccount(accUnknown)

	// 通过配额刷新记录 HasArgonBucket
	mgr.UpdateAccountCooldownFromQuota("acc-argon", []QuotaBucket{
		{ModelID: "Weekly Limit", Group: "Gemini Models", RemainingFraction: 0.5, RemainPercent: 50},
		{ModelID: "Gemini 4 Argon Weekly Limit", Group: "Gemini 4 Argon", RemainingFraction: 0.33, RemainPercent: 33},
	})
	mgr.UpdateAccountCooldownFromQuota("acc-no-argon", []QuotaBucket{
		{ModelID: "Weekly Limit", Group: "Gemini Models", RemainingFraction: 0.59, RemainPercent: 59},
		{ModelID: "Weekly Limit", Group: "Claude and GPT models", RemainingFraction: 1.0, RemainPercent: 100},
	})

	// 通过 fetchAvailableModels 记录 SupportedModels
	mgr.SetAccountSupportedModels("acc-argon", []string{"gemini-2.5-flash", "gemini-4-argon-medium", "claude-opus-5-5-high", "claude-opus-5-5-medium"})
	mgr.SetAccountSupportedModels("acc-no-argon", []string{"gemini-2.5-flash", "claude-sonnet-4-6"})

	// 断言 1: 请求 gemini-4-argon-medium 时，只应优先返回明确支持 Argon 的 acc-argon，绝不把明确不支持的 acc-no-argon 纳入候选
	availArgon := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-4-argon-medium")
	if len(availArgon) != 1 || availArgon[0].ID != "acc-argon" {
		t.Fatalf("expected only acc-argon for gemini-4-argon-medium, got %d accounts: %+v", len(availArgon), availArgon)
	}

	// 断言 2: 请求 claude-opus-5-5-high 时，只应返回 SupportedModels 明确包含该模型的 acc-argon，剔除 acc-no-argon
	availOpus55 := mgr.GetAvailableAccountsForChannel("antigravity", "claude-opus-5-5-high")
	if len(availOpus55) != 1 || availOpus55[0].ID != "acc-argon" {
		t.Fatalf("expected only acc-argon for claude-opus-5-5-high, got %d accounts: %+v", len(availOpus55), availOpus55)
	}

	// 断言 3: 请求常规模型 gemini-2.5-flash 时，3 个账号全部可用
	availFlash := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-2.5-flash")
	if len(availFlash) != 3 {
		t.Fatalf("expected all 3 accounts available for gemini-2.5-flash, got %d", len(availFlash))
	}

	// 断言 4: 模拟 acc-unknown 在请求 claude-opus-5-5-medium 时遇到 404，调用 MarkAccountModelUnsupported
	mgr.MarkAccountModelUnsupported("acc-unknown", "claude-opus-5-5-medium", 24*time.Hour)
	availOpusMedium := mgr.GetAvailableAccountsForChannel("antigravity", "claude-opus-5-5-medium")
	for _, a := range availOpusMedium {
		if a.ID == "acc-unknown" || a.ID == "acc-no-argon" {
			t.Fatalf("expected acc-unknown and acc-no-argon to be filtered out for claude-opus-5-5-medium, got %s", a.ID)
		}
	}

	// 断言 5: ClearAccountCooldown 也会一并清理 UnsupportedModels
	if !mgr.ClearAccountCooldown("acc-unknown") {
		t.Fatalf("expected ClearAccountCooldown to return true when UnsupportedModels was non-empty")
	}
	if accUnknown.IsModelMarkedUnsupported("claude-opus-5-5-medium", time.Now().UnixNano()/int64(time.Millisecond)) {
		t.Fatalf("expected UnsupportedModels cleared after ClearAccountCooldown")
	}
}

func TestTriggerTestResponse_ReusesValidTokenAcrossModels(t *testing.T) {
	var refreshCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}`))
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
		ID:               "acc-valid-tok",
		Email:            "valid@gmail.com",
		Provider:         "antigravity",
		AccessToken:      "ya29.still-valid-token",
		TokenRefreshedAt: time.Now().Unix() - 120, // 2 分钟前刚刷新过 (< 3000s)
	}

	models := []string{"gemini-2.5-flash", "gemini-3.5-pro", "claude-sonnet-4-6"}
	for _, m := range models {
		_, err := TriggerTestResponse(
			context.Background(),
			acc,
			m,
			"ok",
			func(email string) string { return "proj-valid" },
			func(a *Account) (string, error) {
				atomic.AddInt32(&refreshCount, 1)
				return "ya29.refreshed", nil
			},
		)
		if err != nil {
			t.Fatalf("TriggerTestResponse for %s failed: %v", m, err)
		}
	}

	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("expected 0 OAuth token refreshes when AccessToken is still valid for 1h, got %d", got)
	}
}
