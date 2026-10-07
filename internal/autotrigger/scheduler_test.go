package autotrigger

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/db"
)

func TestScheduler_TriggerTaskNow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_scheduler.db")
	if err := db.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	var logs []string
	addLog := func(msg string) {
		logs = append(logs, msg)
	}

	accMgr := account.NewManager()
	sched := NewScheduler(accMgr, nil, nil, addLog)

	// Case 1: 不存在的任务
	err := sched.TriggerTaskNow(999999)
	if err == nil || !strings.Contains(err.Error(), "task not found") {
		t.Fatalf("expected 'task not found' error, got %v", err)
	}

	// Case 2: 任务存在，模拟已经在运行中 (runningJobs[task.ID] = true)
	task := &db.AutoTriggerTask{
		Name:            "Scheduled Manual Trigger Task",
		AccountIDs:      []string{"acc-dummy"},
		ModelNames:      []string{"gemini-2.5-flash"},
		Prompt:          "ping",
		TriggerType:     "timer",
		IntervalSeconds: 600,
		Enabled:         true,
	}
	if err := db.SaveAutoTriggerTask(task); err != nil {
		t.Fatalf("SaveAutoTriggerTask failed: %v", err)
	}
	// 确保测试结束清理测试任务 (Teardown)
	defer func() {
		_ = db.DeleteAutoTriggerTask(task.ID)
	}()

	sched.runningMu.Lock()
	sched.runningJobs[task.ID] = true
	sched.runningMu.Unlock()

	errRunning := sched.TriggerTaskNow(task.ID)
	if errRunning == nil || !strings.Contains(errRunning.Error(), "task is already running") {
		t.Fatalf("expected 'task is already running' error, got %v", errRunning)
	}

	// 解锁以允许再次触发
	sched.runningMu.Lock()
	delete(sched.runningJobs, task.ID)
	sched.runningMu.Unlock()

	// Case 3: 触发执行（因 targetAccountIDs 无实际账号，runTask 内会平稳跳过或结束）
	errTrigger := sched.TriggerTaskNow(task.ID)
	if errTrigger != nil {
		t.Fatalf("TriggerTaskNow failed: %v", errTrigger)
	}

	// 等待协程执行完毕释放 runningJobs
	time.Sleep(100 * time.Millisecond)

	sched.runningMu.Lock()
	isRunning := sched.runningJobs[task.ID]
	sched.runningMu.Unlock()

	if isRunning {
		t.Fatalf("expected runningJobs to be released after runTask")
	}
}
