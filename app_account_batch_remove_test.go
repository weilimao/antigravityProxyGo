package main

import (
	"encoding/json"
	"os"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestIPCAccountsBatchRemove(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "batch_remove_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := account.NewManager()
	mgr.Init(tempDir)

	// 创建 Grok, NVIDIA, Other 账号各一个
	grokAcc := &account.Account{
		ID:           "grok-1",
		Email:        "grok1@test.com",
		Provider:     "grok",
		AccessToken:  "token1",
		RefreshToken: "ref1",
		Enabled:      true,
	}
	nvidiaAcc := &account.Account{
		ID:           "nvidia-1",
		Email:        "nvidia1@test.com",
		Provider:     "nvidia",
		AccessToken:  "key1",
		BaseURL:      "https://integrate.api.nvidia.com",
		Enabled:      true,
	}
	otherAcc := &account.Account{
		ID:           "other-1",
		Email:        "other1@test.com",
		Provider:     "other",
		AccessToken:  "key2",
		BaseURL:      "https://api.openai.com/v1",
		Enabled:      true,
	}

	mgr.AddAccount(grokAcc)
	mgr.AddAccount(nvidiaAcc)
	mgr.AddAccount(otherAcc)

	app := &App{accountMgr: mgr}

	// 确认初始化 3 个账号存在
	if len(mgr.GetAccounts()) < 3 {
		t.Fatalf("expected at least 3 accounts initially, got %d", len(mgr.GetAccounts()))
	}

	// 1. 调用 IPCInvoke 测试 accounts:batch-remove
	batchIDs := []interface{}{grokAcc.ID, nvidiaAcc.ID}
	respStr, handled, err := app.handleAccountIPC("accounts:batch-remove", []interface{}{batchIDs})
	if err != nil {
		t.Fatalf("handleAccountIPC batch-remove returned error: %v", err)
	}
	if !handled {
		t.Fatalf("handleAccountIPC batch-remove should be handled")
	}

	var res struct {
		Success      bool `json:"success"`
		RemovedCount int  `json:"removedCount"`
		TotalCount   int  `json:"totalCount"`
	}
	if err := json.Unmarshal([]byte(respStr), &res); err != nil {
		t.Fatalf("unmarshal response error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success true, got false")
	}
	if res.RemovedCount != 2 {
		t.Fatalf("expected removedCount 2, got %d", res.RemovedCount)
	}

	// 验证 grok-1 与 nvidia-1 已从号池清理
	if mgr.GetAccountByID(grokAcc.ID) != nil {
		t.Errorf("grok-1 should be removed from pool")
	}

	// 2. 测试 handleAccountsSendIPC 兼容模式删除剩余账号
	allAccounts := mgr.GetAccounts()
	var remainingIDs []interface{}
	for _, a := range allAccounts {
		remainingIDs = append(remainingIDs, a.ID)
	}

	if len(remainingIDs) > 0 {
		handledSend := app.handleAccountsSendIPC("accounts:batch-remove", []interface{}{remainingIDs})
		if !handledSend {
			t.Fatalf("handleAccountsSendIPC batch-remove should be handled")
		}
	}

	if len(mgr.GetAccounts()) != 0 {
		t.Errorf("expected 0 accounts after batch remove, got %d", len(mgr.GetAccounts()))
	}
}
