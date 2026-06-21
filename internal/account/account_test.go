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
