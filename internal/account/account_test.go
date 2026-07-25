package account

import (
	"os"
	"testing"
	"time"
)

func TestAccountManager_OnAccountsUpdatedDeadlock(t *testing.T) {
	// Create a temp directory for settings/accounts JSON
	tempDir, err := os.MkdirTemp("", "account_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// Set callback that calls GetAccounts() (which attempts to acquire RLock)
	m.OnAccountsUpdated = func(accounts []*Account) {
		// Simulate a concurrent writer queuing up
		writerStarted := make(chan bool)
		writerDone := make(chan bool)
		go func() {
			close(writerStarted)
			// This attempts to acquire Lock() (writer), which would block if parent has RLock.
			// If we removed the outer RLock, this writer can execute or wait properly without causing cyclic dependency.
			m.UpdateAccountEnabled("test-id", false)
			close(writerDone)
		}()

		<-writerStarted
		// Give the writer thread a tiny bit of time to execute and block on Lock()
		time.Sleep(10 * time.Millisecond)

		// This will call RLock() internally.
		// If the parent thread holds RLock, and Goroutine 2 is waiting for Lock(),
		// this RLock call would block indefinitely due to writer starvation prevention, resulting in a deadlock.
		_ = m.GetAccounts()
	}

	acc := &Account{
		ID:       "test-id",
		Email:    "test@example.com",
		Provider: "antigravity",
		Enabled:  true,
	}

	m.AddAccount(acc)

	done := make(chan bool)
	go func() {
		m.UpdateAccountTier("test-id", "Pro")
		done <- true
	}()

	select {
	case <-done:
		// Success, no deadlock
	case <-time.After(3 * time.Second):
		t.Fatal("Deadlock detected in UpdateAccountTier with OnAccountsUpdated callback")
	}
}

func TestAccountManager_SetAccountCooldownDeadlock(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// Set callback that calls GetAccounts() (which attempts to acquire RLock)
	m.OnAccountsUpdated = func(accounts []*Account) {
		// Simulate a concurrent writer queuing up
		writerStarted := make(chan bool)
		writerDone := make(chan bool)
		go func() {
			close(writerStarted)
			// This attempts to acquire Lock() (writer), which would block if parent has RLock.
			m.UpdateAccountEnabled("test-id", false)
			close(writerDone)
		}()

		<-writerStarted
		time.Sleep(10 * time.Millisecond)

		// This will call RLock() internally.
		// If the parent thread holds RLock, and Goroutine 2 is waiting for Lock(),
		// this RLock call would block indefinitely due to writer starvation prevention, resulting in a deadlock.
		_ = m.GetAccounts()
	}

	acc := &Account{
		ID:       "test-id",
		Email:    "test@example.com",
		Provider: "antigravity",
		Enabled:  true,
	}
	m.AddAccount(acc)

	done := make(chan bool)
	go func() {
		m.SetAccountCooldown("test-id", time.Now().UnixNano()/1e6+10000, "gemini-1.5-pro")
		done <- true
	}()

	select {
	case <-done:
		// Success, no deadlock
	case <-time.After(3 * time.Second):
		t.Fatal("Deadlock detected in SetAccountCooldown with OnAccountsUpdated callback")
	}
}

func TestAccountManager_TokenRefreshMonitor(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_refresh_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	refreshCallCount := 0
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshCallCount++
		return "new-mocked-token-" + acc.ID, nil
	}

	// 1. 创建不需要刷新的账号 (刚刚刷新过，比如在 10 分钟前)
	accNormal := &Account{
		ID:               "acc-normal",
		Email:            "normal@example.com",
		AccessToken:      "token-normal",
		RefreshToken:     "refresh-normal",
		Provider:         "google",
		Enabled:          true,
		TokenRefreshedAt: time.Now().Unix() - 10*60, // 10分钟前
	}

	// 2. 创建过期需要刷新的账号 (在 60 分钟前刷新过)
	accExpired := &Account{
		ID:               "acc-expired",
		Email:            "expired@example.com",
		AccessToken:      "token-expired",
		RefreshToken:     "refresh-expired",
		Provider:         "google",
		Enabled:          true,
		TokenRefreshedAt: time.Now().Unix() - 60*60, // 60分钟前 (> 50分钟)
	}

	// 3. 创建已停用但过期账号 (不应被刷新)
	accDisabled := &Account{
		ID:               "acc-disabled",
		Email:            "disabled@example.com",
		AccessToken:      "token-disabled",
		RefreshToken:     "refresh-disabled",
		Provider:         "google",
		Enabled:          false,
		TokenRefreshedAt: time.Now().Unix() - 60*60,
	}

	// 4. 创建是 2fa 的过期账号 (2fa 不是 google OAuth, 不应刷新)
	acc2FA := &Account{
		ID:               "acc-2fa",
		Email:            "2fa@example.com",
		AccessToken:      "token-2fa",
		RefreshToken:     "refresh-2fa",
		Provider:         "2fa",
		Enabled:          true,
		TokenRefreshedAt: time.Now().Unix() - 60*60,
	}

	m.AddAccount(accNormal)
	m.AddAccount(accExpired)
	m.AddAccount(accDisabled)
	m.AddAccount(acc2FA)

	// 手动触发一次检查刷新
	m.CheckAndRefreshTokens()

	// 等待异步刷新完成 (CheckAndRefreshTokens 内部启动了 goroutine 来执行 RefreshToken 并在 UpdateAccessToken 中写回)
	time.Sleep(100 * time.Millisecond)

	// 验证：
	// 1. 只有 acc-expired 触发了 RefreshToken
	if refreshCallCount != 1 {
		t.Errorf("expected 1 refresh call, got %d", refreshCallCount)
	}

	// 2. 检查 acc-expired 的 AccessToken 已被更新
	refreshedAcc := m.GetAccountByID("acc-expired")
	if refreshedAcc == nil {
		t.Fatal("failed to find acc-expired")
	}
	if refreshedAcc.GetAccessToken() != "new-mocked-token-acc-expired" {
		t.Errorf("expected access token to be 'new-mocked-token-acc-expired', got '%s'", refreshedAcc.GetAccessToken())
	}

	// 3. 检查 acc-expired 的 TokenRefreshedAt 已经被更新为当前附近的时间 (比如当前时间的 5 秒内)
	now := time.Now().Unix()
	if now-refreshedAcc.GetTokenRefreshedAt() > 5 {
		t.Errorf("expected TokenRefreshedAt to be near current time, got %d (now is %d)", refreshedAcc.GetTokenRefreshedAt(), now)
	}
}

// TestGetAccountProviderMap 验证 GetAccountProviderMap 返回 AccountID -> Provider 的映射，
// 确保会话绑定弹窗按号池类型筛选时能正确拿到账号的 provider 信息。
// 覆盖点：多 provider 混合、含 nil 账号、账号删除后映射同步消失。
func TestGetAccountProviderMap(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_provider_map_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// 构造涵盖三种主要号池类型的账号集合
	accAntigravity := &Account{
		ID:       "acc-ag",
		Email:    "ag@example.com",
		Provider: "antigravity",
		Enabled:  true,
	}
	accGoogle := &Account{
		ID:       "acc-gg",
		Email:    "gg@example.com",
		Provider: "google",
		Enabled:  true,
	}
	accNvidia := &Account{
		ID:       "acc-nv",
		Email:    "nv@example.com",
		Provider: "nvidia",
		Enabled:  true,
	}
	m.AddAccount(accAntigravity)
	m.AddAccount(accGoogle)
	m.AddAccount(accNvidia)

	// 验证：三个 provider 各自命中正确的 ID
	providerMap := m.GetAccountProviderMap()
	if len(providerMap) != 3 {
		t.Fatalf("expected provider map length 3, got %d", len(providerMap))
	}
	cases := []struct {
		id       string
		expected string
	}{
		{"acc-ag", "antigravity"},
		{"acc-gg", "google"},
		{"acc-nv", "nvidia"},
	}
	for _, c := range cases {
		got, ok := providerMap[c.id]
		if !ok {
			t.Errorf("expected provider map to contain id %q, but missing", c.id)
			continue
		}
		if got != c.expected {
			t.Errorf("provider of %q = %q, expected %q", c.id, got, c.expected)
		}
	}

	// 验证：账号删除后映射同步消失，不再残留旧 provider，避免会话筛选误命中已删账号
	beforeDel := m.GetAccountProviderMap()
	if _, ok := beforeDel["acc-gg"]; !ok {
		t.Fatal("precondition: acc-gg should exist before delete")
	}
	m.RemoveAccount("acc-gg")
	afterDel := m.GetAccountProviderMap()
	if len(afterDel) != 2 {
		t.Errorf("expected provider map length 2 after delete, got %d", len(afterDel))
	}
	if _, ok := afterDel["acc-gg"]; ok {
		t.Errorf("expected acc-gg to be removed from provider map after deletion, but still present")
	}
}

// TestSetAccountCooldown_NvidiaUsesNvidiaCategory 验证 NVIDIA 账号写入冷静期时
// 使用独立的 "nvidia" 冷却类别，而非误归 gemini/claude，避免后续配额刷新
// 把满额 NVIDIA 配额误判为 "gemini 配额恢复"。
func TestSetAccountCooldown_NvidiaUsesNvidiaCategory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_nv_cooldown_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	acc := &Account{
		ID:       "acc-nv",
		Email:    "nv@example.com",
		Provider: "nvidia",
		Enabled:  true,
	}
	m.AddAccount(acc)

	until := time.Now().UnixNano()/1e6 + 60*1000
	m.SetAccountCooldownForChannel("acc-nv", until, "nvidia", "z-ai/glm-5.2")

	got := m.GetAccountByID("acc-nv")
	if got == nil {
		t.Fatal("failed to find acc-nv after cooldown set")
	}
	m.RLock()
	nvUntil, hasNvidia := got.Cooldowns["nvidia"]
	_, hasGemini := got.Cooldowns["gemini"]
	_, hasClaude := got.Cooldowns["claude"]
	cooldownUntil := got.CooldownUntil
	m.RUnlock()
	if !hasNvidia || nvUntil != until {
		t.Errorf("expected Cooldowns[nvidia]=%d, got ok=%v v=%d", until, hasNvidia, nvUntil)
	}
	if hasGemini {
		t.Errorf("expected no Cooldowns[gemini] for nvidia account, but present")
	}
	if hasClaude {
		t.Errorf("expected no Cooldowns[claude] for nvidia account, but present")
	}
	if cooldownUntil != until {
		t.Errorf("expected CooldownUntil=%d, got %d", until, cooldownUntil)
	}

	// 对照校验：GetModelCategoryByProvider 对 nvidia 直接返回 "nvidia"
	if cat := m.GetModelCategoryByProvider("nvidia", "z-ai/glm-5.2"); cat != "nvidia" {
		t.Errorf("GetModelCategoryByProvider(nvidia,%q)=%q, expected nvidia", "z-ai/glm-5.2", cat)
	}
	// 对照校验：非 nvidia provider 仍维持原 gemini/claude 二分
	if cat := m.GetModelCategoryByProvider("antigravity", "gemini-1.5-pro"); cat != "gemini" {
		t.Errorf("GetModelCategoryByProvider(antigravity,gemini)=%q, expected gemini", cat)
	}
	if cat := m.GetModelCategoryByProvider("antigravity", "claude-sonnet-4-5"); cat != "claude" {
		t.Errorf("GetModelCategoryByProvider(antigravity,claude)=%q, expected claude", cat)
	}
}

// TestUpdateAccountCooldownFromQuota_NvidiaNoSpuriousRestored 验证当 NVIDIA 账号
// 此前从未写入 gemini/claude 冷却键时，一次满额配额刷新(模拟 fetchNvidiaQuota 产出)
// 不应误触发 OnQuotaRestored，从而消除"自动触发"日志刷屏。
// 同时验证：若此前确有 nvidia 冷却键，满额刷新会正确地一次性恢复并携带 category="nvidia"，
// 而不再误报为 gemini。
func TestUpdateAccountCooldownFromQuota_NvidiaNoSpuriousRestored(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_nv_quota_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	acc := &Account{
		ID:       "acc-nv",
		Email:    "nv@example.com",
		Provider: "nvidia",
		Enabled:  true,
	}
	m.AddAccount(acc)

	// 模拟 fetchNvidiaQuota 的满额 buckets：Group 含 NVIDIA，满额不耗尽。
	fullNvidiaBuckets := []QuotaBucket{
		{
			Group:             "NVIDIA 第三方 API Key",
			ModelID:           "可用模型数 10 个 / 10 models",
			RemainingFraction: 1,
			RemainPercent:     100,
		},
	}

	restoredCh := make(chan []string, 4)
	m.OnQuotaRestored = func(accountId string, categories []string) {
		restoredCh <- append([]string(nil), categories...)
	}

	// 场景一：账号此前无任何冷却键，满额刷新不应触发 OnQuotaRestored。
	changed := m.UpdateAccountCooldownFromQuota("acc-nv", fullNvidiaBuckets)
	if changed {
		t.Errorf("expected changed=false for never-cooled nvidia full-quota refresh, got true")
	}
	// 短暂等待确认无异步回调命中
	select {
	case cats := <-restoredCh:
		t.Errorf("expected no OnQuotaRestored call for never-cooled nvidia account, got %v", cats)
	case <-time.After(100 * time.Millisecond):
		// 期望：超时未收到任何恢复回调
	}

	// 场景二：账号先因 429 写入 nvidia 冷却，再满额刷新应触发一次 OnQuotaRestored，
	// 且 categories 仅含 "nvidia"，绝不包含 "gemini"/"claude"。
	until := time.Now().UnixNano()/1e6 + 60*1000
	m.SetAccountCooldownForChannel("acc-nv", until, "nvidia", "z-ai/glm-5.2")
	changed = m.UpdateAccountCooldownFromQuota("acc-nv", fullNvidiaBuckets)
	if !changed {
		t.Errorf("expected changed=true after nvidia cooldown cleared, got false")
	}
	select {
	case cats := <-restoredCh:
		if len(cats) != 1 || cats[0] != "nvidia" {
			t.Errorf("expected restored categories [nvidia], got %v", cats)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected OnQuotaRestored call after nvidia cooldown cleared, but timed out")
	}
	got := m.GetAccountByID("acc-nv")
	if got == nil {
		t.Fatal("failed to find acc-nv after restore")
	}
	m.RLock()
	_, hasNvidia := got.Cooldowns["nvidia"]
	_, hasGemini := got.Cooldowns["gemini"]
	m.RUnlock()
	if hasNvidia {
		t.Errorf("expected Cooldowns[nvidia] removed after full-quota restore, still present")
	}
	if hasGemini {
		t.Errorf("unexpected Cooldowns[gemini] leaked into nvidia account")
	}
}
