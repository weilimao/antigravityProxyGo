package account

import (
	"testing"
	"time"
)

func TestPoolConfigPreferLowTier_DefaultAndPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)

	if mgr.GetPreferLowTierAccount() {
		t.Fatalf("expected PreferLowTierAccount default to be false, got true")
	}

	if err := mgr.SetPreferLowTierAccount(true); err != nil {
		t.Fatalf("SetPreferLowTierAccount(true) failed: %v", err)
	}
	if !mgr.GetPreferLowTierAccount() {
		t.Fatalf("expected GetPreferLowTierAccount() == true after enabling")
	}

	reloaded := NewManager()
	reloaded.Init(tmpDir)
	if !reloaded.GetPreferLowTierAccount() {
		t.Fatalf("expected reloaded Manager to persist PreferLowTierAccount=true in accounts_pool.json")
	}

	if err := reloaded.SetPreferLowTierAccount(false); err != nil {
		t.Fatalf("SetPreferLowTierAccount(false) failed: %v", err)
	}
	reloaded2 := NewManager()
	reloaded2.Init(tmpDir)
	if reloaded2.GetPreferLowTierAccount() {
		t.Fatalf("expected reloaded2 Manager to persist PreferLowTierAccount=false")
	}
}

func TestAccountTierPriority_PreferLowTierFlashOverHydrogen(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)
	mgr.SetActiveChannel("antigravity")
	mgr.SetPoolMode(true)

	// accHigh 支持 gemini-3.8-flash 与高阶模型 gemini-4-hydrogen-high
	accHigh := &Account{
		ID:          "acc-high",
		Email:       "high@gmail.com",
		Provider:    "antigravity",
		AccessToken: "tok-high",
		Enabled:     true,
		Cooldowns:   map[string]int64{},
	}
	// accLow 仅支持 gemini-3.8-flash 与更旧的 gemini-2.5-flash，不支持 gemini-4-hydrogen
	accLow := &Account{
		ID:          "acc-low",
		Email:       "low@gmail.com",
		Provider:    "antigravity",
		AccessToken: "tok-low",
		Enabled:     true,
		Cooldowns:   map[string]int64{},
	}
	mgr.AddAccount(accHigh)
	mgr.AddAccount(accLow)
	mgr.SetAccountSupportedModels("acc-high", []string{"gemini-2.5-flash", "gemini-3.8-flash", "gemini-4-hydrogen-high"})
	mgr.SetAccountSupportedModels("acc-low", []string{"gemini-2.5-flash", "gemini-3.8-flash"})

	// 1. 开关默认关闭：SelectPriorityTierAccounts 不过滤高阶号，两号同等参与负载均衡
	availOff := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-3.8-flash")
	priorityOff := mgr.SelectPriorityTierAccounts(availOff, "antigravity", "gemini-3.8-flash")
	if len(priorityOff) != 2 {
		t.Fatalf("when PreferLowTierAccount is false, expected 2 priority candidates, got %d", len(priorityOff))
	}

	// 2. 开启开关：请求 gemini-3.8-flash 时，SelectPriorityTierAccounts 仅收敛到 accLow；
	// GetAvailableAccountsForChannel 仍保留两个账号（用于兜底），且 accLow 排在最前。
	if err := mgr.SetPreferLowTierAccount(true); err != nil {
		t.Fatalf("SetPreferLowTierAccount(true) failed: %v", err)
	}
	availOn := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-3.8-flash")
	if len(availOn) != 2 {
		t.Fatalf("GetAvailableAccountsForChannel should retain fallback account, got %d", len(availOn))
	}
	if availOn[0].ID != "acc-low" {
		t.Fatalf("expected acc-low to be ordered first in available list, got %s", availOn[0].ID)
	}

	priorityOn := mgr.SelectPriorityTierAccounts(availOn, "antigravity", "gemini-3.8-flash")
	if len(priorityOn) != 1 || priorityOn[0].ID != "acc-low" {
		t.Fatalf("expected only [acc-low] in priority tier for gemini-3.8-flash, got %+v", priorityOn)
	}

	// 单账号模式下（关闭负载均衡）GetNextAccount 也应优先命中 acc-low
	mgr.SetPoolMode(false)
	nextAcc := mgr.GetNextAccount("gemini-3.8-flash")
	if nextAcc == nil || nextAcc.ID != "acc-low" {
		t.Fatalf("expected GetNextAccount in single mode to pick acc-low, got %+v", nextAcc)
	}
	mgr.SetPoolMode(true)

	// 3. 当 accLow 进入 gemini 冷静期时，平滑降级回退到 accHigh
	mgr.SetAccountCooldown("acc-low", time.Now().Add(60*time.Second).UnixNano()/int64(time.Millisecond), "gemini-3.8-flash")
	availCooldown := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-3.8-flash")
	priorityCooldown := mgr.SelectPriorityTierAccounts(availCooldown, "antigravity", "gemini-3.8-flash")
	if len(priorityCooldown) != 1 || priorityCooldown[0].ID != "acc-high" {
		t.Fatalf("expected fallback to [acc-high] when acc-low is cooled, got %+v", priorityCooldown)
	}
	// 使用已过期时间(1ms)解除 gemini 类别冷静期
	mgr.SetAccountCooldown("acc-low", 1, "gemini-3.8-flash")

	// 4. 请求 gemini-4-hydrogen-high 时仍只允许 accHigh
	availHydrogen := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-4-hydrogen-high")
	priorityHydrogen := mgr.SelectPriorityTierAccounts(availHydrogen, "antigravity", "gemini-4-hydrogen-high")
	if len(priorityHydrogen) != 1 || priorityHydrogen[0].ID != "acc-high" {
		t.Fatalf("expected [acc-high] for gemini-4-hydrogen-high, got %+v", priorityHydrogen)
	}

	// 5. 关闭开关：立即失效，恢复两号均衡
	if err := mgr.SetPreferLowTierAccount(false); err != nil {
		t.Fatalf("SetPreferLowTierAccount(false) failed: %v", err)
	}
	availAfterOff := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-3.8-flash")
	priorityAfterOff := mgr.SelectPriorityTierAccounts(availAfterOff, "antigravity", "gemini-3.8-flash")
	if len(priorityAfterOff) != 2 {
		t.Fatalf("after disabling toggle, expected 2 candidates, got %d", len(priorityAfterOff))
	}
}

func TestAccountTierPriority_ArgonIsolationAndClaudeAndUnprobed(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)
	mgr.SetActiveChannel("antigravity")
	mgr.SetPoolMode(true)
	_ = mgr.SetPreferLowTierAccount(true)

	hasArgon := true
	accA := &Account{
		ID: "acc-a", Email: "a@gmail.com", Provider: "antigravity", AccessToken: "tok-a", Enabled: true, HasArgonBucket: &hasArgon,
	}
	accB := &Account{
		ID: "acc-b", Email: "b@gmail.com", Provider: "antigravity", AccessToken: "tok-b", Enabled: true, HasArgonBucket: &hasArgon,
	}
	accUnprobed := &Account{
		ID: "acc-unprobed", Email: "u@gmail.com", Provider: "antigravity", AccessToken: "tok-u", Enabled: true,
	}
	mgr.AddAccount(accA)
	mgr.AddAccount(accB)
	mgr.AddAccount(accUnprobed)

	// accA 拥有 gemini-4-hydrogen 与 claude-opus-5-5；accB 无此高阶但拥有 gemini-2.5-pro(低版本) 与 gemini-4-argon
	mgr.SetAccountSupportedModels("acc-a", []string{"gemini-3.8-flash", "gemini-4-hydrogen-high", "gemini-4-argon", "claude-sonnet-4-5", "claude-opus-5-5-high"})
	mgr.SetAccountSupportedModels("acc-b", []string{"gemini-2.5-pro", "gemini-3.8-flash", "gemini-4-argon", "claude-sonnet-4-5"})

	// 1. 配额族隔离：请求 gemini-4-argon（独立 argon 桶），不应因 accA 有 gemini-4-hydrogen 而被降级！
	availArgon := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-4-argon")
	prioArgon := mgr.SelectPriorityTierAccounts(availArgon, "antigravity", "gemini-4-argon")
	if len(prioArgon) != 2 {
		t.Fatalf("argon bucket must be isolated from gemini-4-hydrogen, expected 2 accounts, got %d", len(prioArgon))
	}

	// 2. 未探测账号不抢占已确认低阶账号；老版本 gemini-2.5-pro 不视为高于 3.8 的模型
	availFlash := mgr.GetAvailableAccountsForChannel("antigravity", "gemini-3.8-flash")
	prioFlash := mgr.SelectPriorityTierAccounts(availFlash, "antigravity", "gemini-3.8-flash")
	if len(prioFlash) != 1 || prioFlash[0].ID != "acc-b" {
		t.Fatalf("expected only confirmed low-tier [acc-b] ahead of unprobed and high-tier, got %+v", prioFlash)
	}

	// 3. Claude 配额族内：请求 claude-sonnet-4-5 优先仅支持 4.5 的 acc-b，保护 acc-a 的 claude-opus-5-5 配额
	availClaude := mgr.GetAvailableAccountsForChannel("antigravity", "claude-sonnet-4-5")
	prioClaude := mgr.SelectPriorityTierAccounts(availClaude, "antigravity", "claude-sonnet-4-5")
	if len(prioClaude) != 1 || prioClaude[0].ID != "acc-b" {
		t.Fatalf("expected [acc-b] for claude-sonnet-4-5, got %+v", prioClaude)
	}
}
