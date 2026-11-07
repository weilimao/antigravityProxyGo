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

func TestRepoAutoTrigger_AccountModelsStorageAndFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_account_models.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer CloseDB()

	// 1. 保存包含各个账号专属模型配置 (AccountModels) 的任务
	accModels := map[string][]string{
		"acc-pro":  {"gemini-2.5-flash", "gemini-4-argon-high", "claude-opus-5-5-high"},
		"acc-free": {"gemini-2.5-flash"},
	}

	task := &AutoTriggerTask{
		Name:            "Task Per Account Models",
		AccountIDs:      []string{"acc-pro", "acc-free"},
		AccountModels:   accModels,
		Prompt:          "test prompt",
		TriggerType:     "timer",
		IntervalSeconds: 1800,
		Enabled:         true,
	}

	if err := SaveAutoTriggerTask(task); err != nil {
		t.Fatalf("SaveAutoTriggerTask failed: %v", err)
	}
	if task.ID == 0 {
		t.Fatalf("expected non-zero ID")
	}
	// 验证全局 ModelNames 已自动汇聚两个账号模型的并集
	if len(task.ModelNames) != 3 {
		t.Errorf("expected 3 union model names, got: %v", task.ModelNames)
	}

	// 2. 读取任务并验证 AccountModels 持久化及各账号模型获取
	got, err := GetAutoTriggerTask(task.ID)
	if err != nil {
		t.Fatalf("GetAutoTriggerTask failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected task, got nil")
	}
	if len(got.AccountModels) != 2 {
		t.Fatalf("expected 2 accounts in AccountModels, got %d", len(got.AccountModels))
	}

	proModels := got.GetModelsForAccount("acc-pro")
	if len(proModels) != 3 {
		t.Errorf("expected 3 models for acc-pro, got %v", proModels)
	}

	freeModels := got.GetModelsForAccount("acc-free")
	if len(freeModels) != 1 || freeModels[0] != "gemini-2.5-flash" {
		t.Errorf("expected ['gemini-2.5-flash'] for acc-free, got %v", freeModels)
	}

	// 3. 验证未指定专属配置的未知账号优雅回退至 ModelNames
	fallbackModels := got.GetModelsForAccount("acc-other")
	if len(fallbackModels) != 3 {
		t.Errorf("expected fallback to ModelNames (3), got %v", fallbackModels)
	}

	// 4. 验证 ListAutoTriggerTasks 也正确反序列化 AccountModels
	list, err := ListAutoTriggerTasks()
	if err != nil {
		t.Fatalf("ListAutoTriggerTasks failed: %v", err)
	}
	if len(list) != 1 || len(list[0].AccountModels) != 2 {
		t.Errorf("expected list item to have 2 accounts in AccountModels")
	}

	// 5. 更新任务测试
	got.AccountModels["acc-free"] = append(got.AccountModels["acc-free"], "gemini-2.5-pro")
	got.ModelNames = nil // 清空触发重新汇聚
	if err := SaveAutoTriggerTask(got); err != nil {
		t.Fatalf("update SaveAutoTriggerTask failed: %v", err)
	}

	updated, _ := GetAutoTriggerTask(got.ID)
	if len(updated.GetModelsForAccount("acc-free")) != 2 {
		t.Errorf("expected acc-free to have 2 models after update, got %v", updated.GetModelsForAccount("acc-free"))
	}

	// Teardown 清理
	_ = DeleteAutoTriggerTask(task.ID)
}
