package main

import (
	"encoding/json"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// TestRelayModelMapping_EmptyAccountPool 验证当号池无任何账号时，
// 中继模型映射 IPC (relay:get-model-mapping) 与 测速模型候选 IPC (benchmark:models)
// 均返回空列表，防止向前端模型下拉组件强塞默认的 70+ 个模型。
func TestRelayModelMapping_EmptyAccountPool(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	_, err := settings.EnsureConfigExists(tempDir)
	if err != nil {
		t.Fatalf("EnsureConfigExists failed: %v", err)
	}
	settingsMgr.Init(tempDir)

	accountMgr := account.NewManager()
	accountMgr.Init(tempDir)

	app := &App{
		settingsMgr: settingsMgr,
		accountMgr:  accountMgr,
	}

	// 验证号池为空判定
	if app.hasAccountsInPool() {
		t.Fatal("expected hasAccountsInPool() to be false when pool is empty")
	}

	// 1. 测试 relay:get-model-mapping 在空号池时返回 []
	respStr, handled, err := app.handleRelayConfigIPC("relay:get-model-mapping", nil)
	if err != nil {
		t.Fatalf("handleRelayConfigIPC err: %v", err)
	}
	if !handled {
		t.Fatal("expected channel relay:get-model-mapping to be handled")
	}

	var mappings []settings.ModelMappingEntry
	if err := json.Unmarshal([]byte(respStr), &mappings); err != nil {
		t.Fatalf("unmarshal mappings error: %v, raw: %s", err, respStr)
	}
	if len(mappings) != 0 {
		t.Fatalf("expected empty mappings when account pool is empty, got %d entries", len(mappings))
	}

	// 2. 测试 benchmark:models 在空号池时返回空 models 列表
	benchResp, benchHandled, err := app.handleBenchmarkIPC("benchmark:models", nil)
	if err != nil {
		t.Fatalf("handleBenchmarkIPC err: %v", err)
	}
	if !benchHandled {
		t.Fatal("expected channel benchmark:models to be handled")
	}

	var benchData struct {
		Success bool     `json:"success"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal([]byte(benchResp), &benchData); err != nil {
		t.Fatalf("unmarshal benchmark data error: %v, raw: %s", err, benchResp)
	}
	if !benchData.Success {
		t.Fatal("expected benchmark:models success to be true")
	}
	if len(benchData.Models) != 0 {
		t.Fatalf("expected empty benchmark models when account pool is empty, got %d models", len(benchData.Models))
	}
}

// TestRelayModelMapping_WithAccount 验证当号池存在有效账号时，
// relay:get-model-mapping 与 benchmark:models 能够正常返回模型列表。
func TestRelayModelMapping_WithAccount(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	_, err := settings.EnsureConfigExists(tempDir)
	if err != nil {
		t.Fatalf("EnsureConfigExists failed: %v", err)
	}
	settingsMgr.Init(tempDir)

	accountMgr := account.NewManager()
	accountMgr.Init(tempDir)

	app := &App{
		settingsMgr: settingsMgr,
		accountMgr:  accountMgr,
	}

	// 向号池添加一个账号
	_, err = accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID:   "test-group",
		GroupName: "测试组",
		BaseURL:   "https://api.openai.com/v1",
		APIKey:    "sk-test-key-1234",
		Formats:   []string{"openai"},
	})
	if err != nil {
		t.Fatalf("AddOtherAccount failed: %v", err)
	}

	// 验证号池非空判定
	if !app.hasAccountsInPool() {
		t.Fatal("expected hasAccountsInPool() to be true after adding account")
	}

	// 1. relay:get-model-mapping 此时应返回非空配置
	respStr, handled, err := app.handleRelayConfigIPC("relay:get-model-mapping", nil)
	if err != nil {
		t.Fatalf("handleRelayConfigIPC err: %v", err)
	}
	if !handled {
		t.Fatal("expected channel relay:get-model-mapping to be handled")
	}

	var mappings []settings.ModelMappingEntry
	if err := json.Unmarshal([]byte(respStr), &mappings); err != nil {
		t.Fatalf("unmarshal mappings error: %v", err)
	}
	if len(mappings) == 0 {
		t.Fatal("expected non-empty mappings when account exists in pool")
	}

	// 2. benchmark:models 此时应返回非空候选
	benchResp, benchHandled, err := app.handleBenchmarkIPC("benchmark:models", nil)
	if err != nil {
		t.Fatalf("handleBenchmarkIPC err: %v", err)
	}
	if !benchHandled {
		t.Fatal("expected channel benchmark:models to be handled")
	}

	var benchData struct {
		Success bool     `json:"success"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal([]byte(benchResp), &benchData); err != nil {
		t.Fatalf("unmarshal benchmark data error: %v", err)
	}
	if !benchData.Success {
		t.Fatal("expected benchmark:models success to be true")
	}
	if len(benchData.Models) == 0 {
		t.Fatal("expected non-empty benchmark models when account exists in pool")
	}
}
