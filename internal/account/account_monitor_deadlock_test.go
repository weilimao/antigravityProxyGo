package account

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// account_monitor_deadlock_test.go: 死锁回归测试套件。
// 针对曾导致应用全链路卡死的两类致命死锁场景进行严格回归验证：
// 1. CheckCooldownAccounts 刷新配额失败异常分支下的自死锁（持写锁调用 GetAccountByID）；
// 2. RefreshAccountTokenSync 在高并发读写竞争下的无死锁验证。

func TestCooldownMonitor_FetchQuotaError_NoDeadlock(t *testing.T) {
	m := NewManager()

	// 构造一个已处于冷静期的账号
	past := time.Now().Add(-10 * time.Minute).UnixMilli()
	acc := &Account{
		ID:            "test-cooldown-fail-acc",
		Email:         "cooling@example.com",
		Provider:      "antigravity",
		Enabled:       true,
		CooldownUntil: past,
		Cooldowns: map[string]int64{
			"gemini": past,
		},
		AccessToken: "dummy-token",
	}

	m.Lock()
	m.accounts = append(m.accounts, acc)
	m.Unlock()

	// 注册必失败的 FetchQuota 模拟网络中断/上游风控 429
	fetchErr := errors.New("simulated upstream 429 rate limit / network timeout")
	quotaCalled := make(chan struct{})
	var once sync.Once
	m.FetchQuota = func(a *Account) (*QuotaResult, error) {
		once.Do(func() {
			close(quotaCalled)
		})
		return nil, fetchErr
	}

	// 触发冷却账号扫描与刷新
	m.CheckCooldownAccounts()

	// 等待 FetchQuota 异步完成
	select {
	case <-quotaCalled:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for FetchQuota to be called")
	}

	// 等待一小段让 err != nil 的锁内逻辑执行完
	time.Sleep(50 * time.Millisecond)

	// 重点验证：在 FetchQuota 失败并延长冷却后，外部所有获取读写锁的调用必须瞬间正常返回，绝不能自死锁卡死
	done := make(chan struct{})
	go func() {
		defer close(done)

		// 1. 测试并发读锁接口
		for i := 0; i < 20; i++ {
			_ = m.GetRawAccounts()
			_ = m.GetAvailableAccountsForChannel("antigravity", "gemini-3.7-flash-high")
			got := m.GetAccountByID("test-cooldown-fail-acc")
			if got == nil {
				t.Errorf("expected account to exist")
				return
			}
			time.Sleep(2 * time.Millisecond)
		}

		// 2. 测试写锁接口
		m.ResetAccountError("test-cooldown-fail-acc")
	}()

	select {
	case <-done:
		// 成功，无死锁
	case <-time.After(3 * time.Second):
		t.Fatalf("DEADLOCK DETECTED: Manager methods blocked indefinitely after CooldownMonitor FetchQuota failure")
	}

	// 验证冷静期是否确实被向后延展了 5 分钟
	updatedAcc := m.GetAccountByID("test-cooldown-fail-acc")
	nowMs := time.Now().UnixMilli()
	if updatedAcc.CooldownUntil < nowMs {
		t.Errorf("expected CooldownUntil to be extended into the future, got %d, now %d", updatedAcc.CooldownUntil, nowMs)
	}
}

func TestRefreshAccountTokenSync_Concurrent_NoDeadlock(t *testing.T) {
	m := NewManager()

	acc := &Account{
		ID:          "concurrent-refresh-acc",
		Email:       "refresh@example.com",
		Provider:    "antigravity",
		Enabled:     true,
		AccessToken: "old-access-token",
	}

	m.Lock()
	m.accounts = append(m.accounts, acc)
	m.Unlock()

	var refreshCount int
	var mu sync.Mutex
	m.RefreshToken = func(a *Account) (string, error) {
		mu.Lock()
		refreshCount++
		c := refreshCount
		mu.Unlock()
		time.Sleep(5 * time.Millisecond) // 模拟网络延迟
		return fmt.Sprintf("new-token-%d", c), nil
	}

	// 并发启动 30 个协程同时刷新 token + 读取账号 + 更新配额
	const concurrentWorkers = 30
	var wg sync.WaitGroup
	wg.Add(concurrentWorkers)

	errChan := make(chan error, concurrentWorkers)

	for i := 0; i < concurrentWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()

			// 交替执行刷新与只读查询
			if workerID%2 == 0 {
				token, err := m.RefreshAccountTokenSync("concurrent-refresh-acc")
				if err != nil {
					errChan <- fmt.Errorf("worker %d refresh error: %w", workerID, err)
					return
				}
				if token == "" {
					errChan <- fmt.Errorf("worker %d got empty token", workerID)
					return
				}
			} else {
				_ = m.GetAccountByID("concurrent-refresh-acc")
				_ = m.GetRawAccounts()
				_ = m.GetAvailableAccountsForChannel("antigravity", "gemini-pro")
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 成功完成
	case <-time.After(5 * time.Second):
		t.Fatalf("DEADLOCK DETECTED: Concurrent RefreshAccountTokenSync workers deadlocked")
	}

	close(errChan)
	for err := range errChan {
		t.Errorf("worker returned error: %v", err)
	}
}
