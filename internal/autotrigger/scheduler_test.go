package autotrigger

import (
	"path/filepath"
	"strings"
	"sync"
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

	var mu sync.Mutex
	var logs []string
	addLog := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
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

func TestScheduler_RunTask_PerAccountModelsAndGuard(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_scheduler_guard.db")
	if err := db.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	var mu sync.Mutex
	var logs []string
	addLog := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, msg)
	}

	accMgr := account.NewManager()
	accMgr.Init(t.TempDir())

	hasArgonTrue := true
	hasArgonFalse := false

	// Pro 账号: 拥有 Argon 配额桶
	accPro := &account.Account{
		ID:             "acc-pro",
		Email:          "pro@example.com",
		Provider:       "antigravity",
		AccessToken:    "tok-pro",
		Enabled:        true,
		HasArgonBucket: &hasArgonTrue,
	}
	accMgr.AddAccount(accPro)

	// Free 账号: 明确无 Argon 配额桶
	accFree := &account.Account{
		ID:             "acc-free",
		Email:          "free@example.com",
		Provider:       "antigravity",
		AccessToken:    "tok-free",
		Enabled:        true,
		HasArgonBucket: &hasArgonFalse,
	}
	accMgr.AddAccount(accFree)

	sched := NewScheduler(accMgr, nil, nil, addLog)

	task := db.AutoTriggerTask{
		ID:         101,
		Name:       "Test Per-Account Task",
		AccountIDs: []string{"acc-pro", "acc-free"},
		AccountModels: map[string][]string{
			"acc-pro":  {"gemini-4-argon-high"},
			"acc-free": {"gemini-4-argon-high", "gemini-2.5-flash"}, // 用户误配了 Argon 给 Free 账号
		},
		Prompt: "ping",
	}

	// 执行 runTask
	sched.runTask(task, "")

	// 等待协程执行（因 TriggerTestResponse 会因为无 mock 网络失败或结束）
	time.Sleep(300 * time.Millisecond)

	// 验证日志中 Free 账号被自动拦截跳过了 gemini-4-argon-high，规避了风控
	foundGuardLog := false
	for _, l := range logs {
		if strings.Contains(l, "free@example.com 暂不支持模型 gemini-4-argon-high，已自动跳过以规避风控") {
			foundGuardLog = true
			break
		}
	}

	if !foundGuardLog {
		t.Errorf("expected capability guard log for free account on argon model, logs: %v", logs)
	}
}
