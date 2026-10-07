package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRepoAutoTrigger_GetAutoTriggerTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_autotrigger.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer CloseDB()

	// 1. 测试不存在的 ID
	taskNone, err := GetAutoTriggerTask(99999)
	if err != nil {
		t.Fatalf("unexpected error for non-existent task: %v", err)
	}
	if taskNone != nil {
		t.Fatalf("expected nil for non-existent task, got %+v", taskNone)
	}

	// 2. 创建一个任务
	nextTime := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	task := &AutoTriggerTask{
		Name:            "Test Manual Task",
		AccountIDs:      []string{"acc-1", "acc-2"},
		ModelNames:      []string{"gemini-2.5-flash", "claude-3-7-sonnet"},
		Prompt:          "hello test",
		TriggerType:     "timer",
		IntervalSeconds: 3600,
		NextTriggerTime: &nextTime,
		Enabled:         true,
	}

	if err := SaveAutoTriggerTask(task); err != nil {
		t.Fatalf("SaveAutoTriggerTask failed: %v", err)
	}
	if task.ID == 0 {
		t.Fatalf("expected non-zero task ID after save")
	}

	// 3. 通过 GetAutoTriggerTask 查询
	got, err := GetAutoTriggerTask(task.ID)
	if err != nil {
		t.Fatalf("GetAutoTriggerTask failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected task, got nil")
	}

	if got.ID != task.ID || got.Name != task.Name || got.Prompt != task.Prompt {
		t.Fatalf("task fields mismatch: got %+v, want %+v", got, task)
	}
	if len(got.AccountIDs) != 2 || got.AccountIDs[0] != "acc-1" || got.AccountIDs[1] != "acc-2" {
		t.Fatalf("account IDs mismatch: got %+v", got.AccountIDs)
	}
	if len(got.ModelNames) != 2 || got.ModelNames[0] != "gemini-2.5-flash" {
		t.Fatalf("model names mismatch: got %+v", got.ModelNames)
	}
	if !got.Enabled {
		t.Fatalf("expected task to be enabled")
	}

	// Teardown: 显式删除测试记录并验证
	if err := DeleteAutoTriggerTask(task.ID); err != nil {
		t.Fatalf("cleanup DeleteAutoTriggerTask failed: %v", err)
	}
	delCheck, _ := GetAutoTriggerTask(task.ID)
	if delCheck != nil {
		t.Fatalf("expected task to be cleaned up, but still exists")
	}
}
