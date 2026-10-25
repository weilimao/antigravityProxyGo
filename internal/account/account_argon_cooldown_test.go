package account

import (
	"os"
	"testing"
	"time"
)

// TestUpdateAccountCooldownFromQuota_ArgonDecoupled 验证 Gemini 4 Argon 独立配额耗尽与冷却隔离
func TestUpdateAccountCooldownFromQuota_ArgonDecoupled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_argon_cooldown_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// 1. 模型类别归属校验
	if cat := m.GetModelCategory("gemini-4-argon"); cat != "argon" {
		t.Fatalf("GetModelCategory(gemini-4-argon) = %q, want 'argon'", cat)
	}
	if cat := m.GetModelCategory("gemini-2.5-pro"); cat != "gemini" {
		t.Fatalf("GetModelCategory(gemini-2.5-pro) = %q, want 'gemini'", cat)
	}
	if cat := m.GetModelCategory("claude-sonnet-4-5"); cat != "claude" {
		t.Fatalf("GetModelCategory(claude-sonnet-4-5) = %q, want 'claude'", cat)
	}

	acc := &Account{
		ID:          "acc-argon-test",
		Email:       "argon@example.com",
		Provider:    "antigravity",
		AccessToken: "dummy-token",
		Enabled:     true,
	}
	m.AddAccount(acc)

	restoredCh := make(chan []string, 4)
	m.OnQuotaRestored = func(accountId string, categories []string) {
		restoredCh <- append([]string(nil), categories...)
	}

	// 2. 模拟 Argon 耗尽，但常规 Gemini 与 Claude 仍有额度
	argonExhaustedBuckets := []QuotaBucket{
		{
			Group:             "Gemini Models",
			ModelID:           "Weekly Limit Remaining",
			RemainingFraction: 0.8,
			RemainPercent:     80,
		},
		{
			Group:             "Claude and GPT models",
			ModelID:           "Weekly Limit Remaining",
			RemainingFraction: 0.5,
			RemainPercent:     50,
		},
		{
			Group:             "Gemini 4 Argon",
			ModelID:           "Gemini 4 Argon Weekly Limit",
			RemainingFraction: 0,
			RemainPercent:     0,
			ResetTime:         time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		},
	}

	changed := m.UpdateAccountCooldownFromQuota("acc-argon-test", argonExhaustedBuckets)
	if !changed {
		t.Fatalf("expected changed=true when argon is exhausted")
	}

	// 校验冷却隔离：argon 冷却，gemini 与 claude 正常
	m.RLock()
	argonCooldown := acc.Cooldowns["argon"]
	_, hasGemini := acc.Cooldowns["gemini"]
	_, hasClaude := acc.Cooldowns["claude"]
	m.RUnlock()

	if argonCooldown == 0 {
		t.Fatalf("expected Cooldowns[argon] > 0, got 0")
	}
	if hasGemini {
		t.Fatalf("unexpected Cooldowns[gemini] present when only argon is exhausted")
	}
	if hasClaude {
		t.Fatalf("unexpected Cooldowns[claude] present when only argon is exhausted")
	}

	// 验证可用账号选择：Gemini 与 Claude 模型可正常选中该账号，Argon 请求被过滤
	availGemini := m.GetAvailableAccountsForChannel("antigravity", "gemini-2.5-pro")
	if len(availGemini) != 1 {
		t.Fatalf("expected acc-argon-test to be available for gemini, got %d", len(availGemini))
	}
	availClaude := m.GetAvailableAccountsForChannel("antigravity", "claude-sonnet-4-5")
	if len(availClaude) != 1 {
		t.Fatalf("expected acc-argon-test to be available for claude, got %d", len(availClaude))
	}
	availArgon := m.GetAvailableAccountsForChannel("antigravity", "gemini-4-argon")
	if len(availArgon) != 0 {
		t.Fatalf("expected acc-argon-test to be filtered out for argon, got %d", len(availArgon))
	}

	// 3. 模拟 Argon 额度恢复
	argonRestoredBuckets := []QuotaBucket{
		{
			Group:             "Gemini Models",
			ModelID:           "Weekly Limit Remaining",
			RemainingFraction: 0.8,
			RemainPercent:     80,
		},
		{
			Group:             "Claude and GPT models",
			ModelID:           "Weekly Limit Remaining",
			RemainingFraction: 0.5,
			RemainPercent:     50,
		},
		{
			Group:             "Gemini 4 Argon",
			ModelID:           "Gemini 4 Argon Weekly Limit",
			RemainingFraction: 1.0,
			RemainPercent:     100,
		},
	}

	changed = m.UpdateAccountCooldownFromQuota("acc-argon-test", argonRestoredBuckets)
	if !changed {
		t.Fatalf("expected changed=true when argon recovers")
	}

	select {
	case cats := <-restoredCh:
		if len(cats) != 1 || cats[0] != "argon" {
			t.Fatalf("expected restored categories [argon], got %v", cats)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnQuotaRestored callback")
	}

	m.RLock()
	_, hasArgon := acc.Cooldowns["argon"]
	m.RUnlock()
	if hasArgon {
		t.Fatalf("expected Cooldowns[argon] removed after recovery")
	}

	// 恢复后 Argon 模型也重新可用
	availArgonAfter := m.GetAvailableAccountsForChannel("antigravity", "gemini-4-argon")
	if len(availArgonAfter) != 1 {
		t.Fatalf("expected acc-argon-test to be available for argon after recovery, got %d", len(availArgonAfter))
	}

	// 留出时间让异步落盘协程退出，避免 Windows 临时目录删除时产生文件句柄竞争
	time.Sleep(50 * time.Millisecond)
}
