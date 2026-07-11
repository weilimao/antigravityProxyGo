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
