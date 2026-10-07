package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/db"
)

func TestApp_AutoTriggerRun_IPC(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_ipc_autotrigger.db")
	if err := db.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	app := &App{}

	// Case 1: 缺少有效 ID
	resp1, handled1, err1 := app.handleAutoTriggerIPC("autotrigger:run", []interface{}{map[string]interface{}{"id": 0}})
	if err1 != nil || !handled1 || !strings.Contains(resp1, "无效的任务 ID") {
		t.Fatalf("expected '无效的任务 ID', got resp=%s, handled=%v, err=%v", resp1, handled1, err1)
	}

	// Case 2: 调度器未初始化
	resp2, handled2, err2 := app.handleAutoTriggerIPC("autotrigger:run", []interface{}{map[string]interface{}{"id": 123}})
	if err2 != nil || !handled2 || !strings.Contains(resp2, "任务调度器未启动") {
		t.Fatalf("expected '任务调度器未启动', got resp=%s, handled=%v, err=%v", resp2, handled2, err2)
	}

	// Case 3: 调度器已就绪，但任务不存在
	accMgr := account.NewManager()
	app.autoTriggerScheduler = autotrigger.NewScheduler(accMgr, nil, nil, func(string) {})

	resp3, _, _ := app.handleAutoTriggerIPC("autotrigger:run", []interface{}{map[string]interface{}{"id": 99999}})
	if !strings.Contains(resp3, "task not found") {
		t.Fatalf("expected 'task not found', got %s", resp3)
	}

	// Case 4: 任务存在，成功触发
	task := &db.AutoTriggerTask{
		Name:            "IPC Test Task",
		AccountIDs:      []string{"acc-test"},
		ModelNames:      []string{"gemini-2.5-flash"},
		Prompt:          "ping",
		TriggerType:     "timer",
		IntervalSeconds: 300,
		Enabled:         true,
	}
	if err := db.SaveAutoTriggerTask(task); err != nil {
		t.Fatalf("SaveAutoTriggerTask failed: %v", err)
	}
	defer func() {
		_ = db.DeleteAutoTriggerTask(task.ID)
	}()

	resp4, handled4, err4 := app.handleAutoTriggerIPC("autotrigger:run", []interface{}{map[string]interface{}{"id": task.ID}})
	if err4 != nil || !handled4 {
		t.Fatalf("handleAutoTriggerIPC failed: %v", err4)
	}

	var resObj struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp4), &resObj); err != nil {
		t.Fatalf("unmarshal resp4 failed: %v", err)
	}
	if !resObj.Success {
		t.Fatalf("expected success: true, got error: %s", resObj.Error)
	}
}
