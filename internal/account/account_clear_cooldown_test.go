package account

import (
	"sync"
	"testing"
	"time"
)

// account_clear_cooldown_test.go: ClearAccountCooldown 手动解冻回归测试。
// 覆盖用户报告的死锁类别(锁内调用 GetAccountByID → RWMutex 不可重入):
//  1. 有冷却时清除成功(Cooldowns 清空 / CooldownUntil 置 0 / 返回 true);
//  2. 幂等:清除后再清除返回 false 且无副作用;
//  3. 账号不存在返回 false;
//  4. OnAccountCooldownUpdated / OnQuotaRestored 回调按预期触发。
//
// 若实现重新引入锁内 GetAccountByID,测试会因死锁超时(go test 自带 10m 上限)而失败,
// 而非静默通过——这正是该回归要捕获的信号。

// seedCoolingGrokAccount 建一个处于冷却中的 Grok 账号并返回其 id。
func seedCoolingGrokAccount(t *testing.T) (*Manager, string) {
	t.Helper()
	m := NewManager()
	id, err := m.AddGrokAccount(validGrokInput())
	if err != nil {
		t.Fatalf("AddGrokAccount failed: %v", err)
	}
	future := time.Now().Add(20 * time.Hour).UnixMilli()
	m.SetAccountCooldown(id, future, "grok-2")
	return m, id
}

func TestManager_ClearAccountCooldown(t *testing.T) {
	m, id := seedCoolingGrokAccount(t)

	if !m.ClearAccountCooldown(id) {
		t.Fatalf("expected ClearAccountCooldown to return true for a cooling account")
	}

	acc := m.GetAccountByID(id)
	if acc == nil {
		t.Fatalf("account %q should still exist after clear", id)
	}
	if len(acc.Cooldowns) != 0 {
		t.Fatalf("expected Cooldowns to be empty after clear, got: %+v", acc.Cooldowns)
	}
	if acc.CooldownUntil != 0 {
		t.Fatalf("expected CooldownUntil to be 0 after clear, got: %d", acc.CooldownUntil)
	}
}

func TestManager_ClearAccountCooldown_Idempotent(t *testing.T) {
	m, id := seedCoolingGrokAccount(t)

	if !m.ClearAccountCooldown(id) {
		t.Fatalf("first clear should return true")
	}
	// 二次清除:已无冷却 → 幂等 false。
	if m.ClearAccountCooldown(id) {
		t.Fatalf("second clear on already-thawed account should return false(幂等)")
	}
}

func TestManager_ClearAccountCooldown_UnknownID(t *testing.T) {
	m := NewManager()
	if m.ClearAccountCooldown("no-such-id") {
		t.Fatalf("expected false for unknown account id")
	}
}

func TestManager_ClearAccountCooldown_Callbacks(t *testing.T) {
	m, id := seedCoolingGrokAccount(t)

	var mu sync.Mutex
	cooldownCalls := 0
	quotaCalls := 0
	var quotaCats []string
	m.OnAccountCooldownUpdated = func(accID, cat string, until int64) {
		mu.Lock()
		defer mu.Unlock()
		cooldownCalls++
		if accID != id || cat != "all" || until != 0 {
			t.Errorf("unexpected OnAccountCooldownUpdated args: id=%s cat=%s until=%d", accID, cat, until)
		}
	}
	m.OnQuotaRestored = func(accID string, categories []string) {
		mu.Lock()
		defer mu.Unlock()
		quotaCalls++
		quotaCats = categories
	}

	if !m.ClearAccountCooldown(id) {
		t.Fatalf("expected clear to succeed")
	}

	// 回调是异步 go 派发,给一点时间落地。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := cooldownCalls >= 1 && quotaCalls >= 1
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if cooldownCalls != 1 {
		t.Errorf("expected 1 OnAccountCooldownUpdated call, got %d", cooldownCalls)
	}
	if quotaCalls != 1 {
		t.Errorf("expected 1 OnQuotaRestored call, got %d", quotaCalls)
	}
	if len(quotaCats) == 0 || quotaCats[0] != "grok" {
		t.Errorf("expected restored categories to include grok, got: %v", quotaCats)
	}
}