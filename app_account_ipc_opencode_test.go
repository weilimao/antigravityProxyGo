package main

import (
	"encoding/json"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestAppAccountIPC_OpenCode(t *testing.T) {
	dir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(dir)

	app := &App{
		accountMgr: mgr,
	}

	// 1. opencode:add - map 参数
	addRespStr, handled, err := app.handleAccountIPC("opencode:add", []interface{}{
		map[string]interface{}{
			"apiKey":       "sk-opencode-test-1234567890abcdef",
			"label":        "Test OpenCode Account",
			"defaultModel": "claude-sonnet-4-6",
			"note":         "IPC Test Note",
		},
	})
	if !handled || err != nil {
		t.Fatalf("opencode:add failed: handled=%v, err=%v", handled, err)
	}
	var addResp struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal([]byte(addRespStr), &addResp); err != nil || !addResp.Success {
		t.Fatalf("unexpected add response: %s", addRespStr)
	}
	accID := addResp.ID

	// 2. opencode:update - 位置参数
	upRespStr, handled, err := app.handleAccountIPC("opencode:update", []interface{}{
		accID, "", "Updated OpenCode Account", "gpt-5-codex",
	})
	if !handled || err != nil || !strings.Contains(upRespStr, `"success":true`) {
		t.Fatalf("opencode:update failed: handled=%v, err=%v, resp=%s", handled, err, upRespStr)
	}
	acc := mgr.GetAccountByID(accID)
	if acc == nil || acc.Email != "Updated OpenCode Account" || acc.DefaultModel != "gpt-5-codex" {
		t.Fatalf("account after update unexpected: %+v", acc)
	}

	// 3. opencode:batch-add
	batchRespStr, handled, err := app.handleAccountIPC("opencode:batch-add", []interface{}{
		"sk-key-1-abcdefghijklmnopqrstuvwxyz\nsk-key-2-abcdefghijklmnopqrstuvwxyz",
		"claude-sonnet-4-6",
	})
	if !handled || err != nil {
		t.Fatalf("opencode:batch-add failed: handled=%v, err=%v", handled, err)
	}
	var batchResp struct {
		Success  bool `json:"success"`
		Imported int  `json:"imported"`
	}
	if err := json.Unmarshal([]byte(batchRespStr), &batchResp); err != nil || batchResp.Imported != 2 {
		t.Fatalf("batch add failed: %s", batchRespStr)
	}

	// 4. opencode:toggle-enabled
	_, _, _ = app.handleAccountIPC("opencode:toggle-enabled", []interface{}{accID, false})
	acc = mgr.GetAccountByID(accID)
	if acc.Enabled {
		t.Fatalf("expected account disabled")
	}

	// 5. opencode:set-lb-mode & get-lb-mode
	_, _, _ = app.handleAccountIPC("opencode:set-lb-mode", []interface{}{"sticky"})
	if mgr.GetOpenCodeLBMode() != "sticky" {
		t.Fatalf("expected sticky LB mode, got %s", mgr.GetOpenCodeLBMode())
	}
	// IPCSend channel
	app.IPCSend("opencode:set-lb-mode", `["round-robin"]`)
	if mgr.GetOpenCodeLBMode() != "round-robin" {
		t.Fatalf("expected round-robin LB mode, got %s", mgr.GetOpenCodeLBMode())
	}

	// 6. opencode:set-max-concurrency & get-max-concurrency
	_, _, _ = app.handleAccountIPC("opencode:set-max-concurrency", []interface{}{5})
	if mgr.GetOpenCodeMaxConcurrency() != 5 {
		t.Fatalf("expected max concurrency 5, got %d", mgr.GetOpenCodeMaxConcurrency())
	}
	app.IPCSend("opencode:set-max-concurrency", `[8]`)
	if mgr.GetOpenCodeMaxConcurrency() != 8 {
		t.Fatalf("expected max concurrency 8, got %d", mgr.GetOpenCodeMaxConcurrency())
	}

	// 7. opencode:fetch-models
	modelsRespStr, handled, err := app.handleAccountIPC("opencode:fetch-models", nil)
	if !handled || err != nil {
		t.Fatalf("fetch-models failed")
	}
	var modelsResp struct {
		Success bool     `json:"success"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal([]byte(modelsRespStr), &modelsResp); err != nil || len(modelsResp.Models) == 0 {
		t.Fatalf("fetch-models empty: %s", modelsRespStr)
	}

	// 7.1 quota:fetch 对 OpenCode 账号应直接返回 Active，不走 GCP loadCodeAssist
	quotaRespStr, handled, qErr := app.handleAccountsInvokeIPC("quota:fetch", []interface{}{accID})
	if !handled || qErr != nil {
		t.Fatalf("quota:fetch not handled or error: handled=%v, err=%v", handled, qErr)
	}
	var quotaResp struct {
		Tier    string `json:"tier"`
		Buckets []any  `json:"buckets"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(quotaRespStr), &quotaResp); err != nil || quotaResp.Error != "" || quotaResp.Tier != "Active" {
		t.Fatalf("quota:fetch for opencode account failed or returned error: %s", quotaRespStr)
	}

	// 8. opencode:remove
	_, _, _ = app.handleAccountIPC("opencode:remove", []interface{}{accID})
	if mgr.GetAccountByID(accID) != nil {
		t.Fatalf("account should be removed")
	}
}
