package account

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManager_WorkBuddyLBMode(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager()
	m.Init(tempDir)

	// 1. 默认值应为 round-robin
	if got := m.GetWorkBuddyLBMode(); got != "round-robin" {
		t.Errorf("default WorkBuddy LB mode: want round-robin, got %q", got)
	}

	// 2. 切换为 sticky
	m.SetWorkBuddyLBMode("sticky")
	if got := m.GetWorkBuddyLBMode(); got != "sticky" {
		t.Errorf("after setting sticky: want sticky, got %q", got)
	}

	// 3. 非法值应回退为 round-robin
	m.SetWorkBuddyLBMode("invalid-mode")
	if got := m.GetWorkBuddyLBMode(); got != "round-robin" {
		t.Errorf("after invalid mode: want round-robin, got %q", got)
	}

	// 4. 持久化并从盘重新载入验证
	m.SetWorkBuddyLBMode("sticky")
	m2 := NewManager()
	m2.Init(tempDir)
	if got := m2.GetWorkBuddyLBMode(); got != "sticky" {
		t.Errorf("reloaded WorkBuddy LB mode: want sticky, got %q", got)
	}
}

func TestManager_WorkBuddyMaxConcurrency(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager()
	m.Init(tempDir)

	// 1. 默认应回退为 defaultMaxConcurrency (10)
	if got := m.GetWorkBuddyMaxConcurrency(); got != defaultMaxConcurrency {
		t.Errorf("default max concurrency: want %d, got %d", defaultMaxConcurrency, got)
	}

	// 2. 设置为自定义值
	m.SetWorkBuddyMaxConcurrency(25)
	if got := m.GetWorkBuddyMaxConcurrency(); got != 25 {
		t.Errorf("after set: want 25, got %d", got)
	}

	// 3. 负数应置 0 并回退默认值
	m.SetWorkBuddyMaxConcurrency(-5)
	if got := m.GetWorkBuddyMaxConcurrency(); got != defaultMaxConcurrency {
		t.Errorf("negative should fallback to default: want %d, got %d", defaultMaxConcurrency, got)
	}

	// 4. 持久化重载验证
	m.SetWorkBuddyMaxConcurrency(18)
	m2 := NewManager()
	m2.Init(tempDir)
	if got := m2.GetWorkBuddyMaxConcurrency(); got != 18 {
		t.Errorf("reloaded max concurrency: want 18, got %d", got)
	}
}

func TestManager_WorkBuddyPartitionIsolation(t *testing.T) {
	// 验证 poolPartKind 仅更新 pool 分区，不污染 accounts_workbuddy.json
	tempDir := t.TempDir()
	m := NewManager()
	m.Init(tempDir)

	m.SetWorkBuddyLBMode("sticky")
	m.SetWorkBuddyMaxConcurrency(15)

	poolFile := filepath.Join(tempDir, "accounts_pool.json")
	wbFile := filepath.Join(tempDir, "accounts_workbuddy.json")

	if !fileExists(poolFile) {
		t.Errorf("accounts_pool.json should exist")
	}
	if fileExists(wbFile) {
		t.Errorf("accounts_workbuddy.json should not exist yet since no account was added")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
