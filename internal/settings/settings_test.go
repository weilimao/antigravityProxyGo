package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_RequestTimeout(t *testing.T) {
	// 创建临时工作目录
	tempDir, err := os.MkdirTemp("", "antigravity-settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 测试默认超时时间是否为 300 秒
	if timeout := mgr.GetRequestTimeout(); timeout != 300 {
		t.Errorf("Expected default RequestTimeout to be 300, got %d", timeout)
	}

	// 2. 测试 Setter/Getter 方法
	if err := mgr.SetRequestTimeout(120); err != nil {
		t.Fatalf("Failed to set request timeout: %v", err)
	}
	if timeout := mgr.GetRequestTimeout(); timeout != 120 {
		t.Errorf("Expected RequestTimeout to be 120, got %d", timeout)
	}

	// 3. 校验配置文件是否已落盘且值正确
	configPath := filepath.Join(tempDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}

	// 初始化一个新 Manager 加载它
	newMgr := NewManager()
	newMgr.Init(tempDir)
	if timeout := newMgr.GetRequestTimeout(); timeout != 120 {
		t.Errorf("Expected reloaded RequestTimeout to be 120, got %d (raw config: %s)", timeout, string(data))
	}

	// 4. 测试边界防御防呆逻辑（设为负数或 0 是否回弹为 300）
	if err := mgr.SetRequestTimeout(-10); err != nil {
		t.Fatalf("Failed to set invalid timeout: %v", err)
	}
	if timeout := mgr.GetRequestTimeout(); timeout != 300 {
		t.Errorf("Expected invalid timeout to fallback to 300, got %d", timeout)
	}
}

func TestSettings_RelayModelMappingRetention(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-mapping-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. Initially, we should have all default model mappings
	initialMappings := mgr.GetRelayModelMapping()
	if len(initialMappings) == 0 {
		t.Fatalf("Expected default model mappings to be loaded, got 0")
	}

	// Record original count of mappings
	origCount := len(initialMappings)
	targetToDelete := initialMappings[0].ClientModel

	// 2. Delete the first mapping
	var newMappings []ModelMappingEntry
	for _, entry := range initialMappings {
		if entry.ClientModel != targetToDelete {
			newMappings = append(newMappings, entry)
		}
	}
	if err := mgr.SetRelayModelMapping(newMappings); err != nil {
		t.Fatalf("Failed to set model mappings: %v", err)
	}

	// 3. Re-initialize a new manager to simulate app restart
	newMgr := NewManager()
	newMgr.Init(tempDir)

	reloadedMappings := newMgr.GetRelayModelMapping()
	if len(reloadedMappings) != origCount-1 {
		t.Errorf("Expected reloaded mappings count to be %d, got %d", origCount-1, len(reloadedMappings))
	}

	foundDeleted := false
	for _, entry := range reloadedMappings {
		if entry.ClientModel == targetToDelete {
			foundDeleted = true
			break
		}
	}
	if foundDeleted {
		t.Errorf("Expected model mapping for %q to remain deleted, but it was restored", targetToDelete)
	}

	// 4. Test deleting ALL mappings
	if err := newMgr.SetRelayModelMapping([]ModelMappingEntry{}); err != nil {
		t.Fatalf("Failed to clear all model mappings: %v", err)
	}

	// Re-initialize manager again
	finalMgr := NewManager()
	finalMgr.Init(tempDir)

	finalMappings := finalMgr.GetRelayModelMapping()
	if len(finalMappings) != 0 {
		t.Errorf("Expected model mappings to remain empty, but got %d entries", len(finalMappings))
	}
}

func TestSettings_PromptPrefix(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-prompt-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认值校验
	if prefix := mgr.GetPromptPrefix(); prefix != "" {
		t.Errorf("Expected default PromptPrefix to be empty, got %q", prefix)
	}

	// 2. 设置并校验
	testPrefix := "[Please reply in English] "
	if err := mgr.SetPromptPrefix(testPrefix); err != nil {
		t.Fatalf("Failed to set prompt prefix: %v", err)
	}
	if prefix := mgr.GetPromptPrefix(); prefix != testPrefix {
		t.Errorf("Expected PromptPrefix to be %q, got %q", testPrefix, prefix)
	}

	// 3. 重启加载校验
	newMgr := NewManager()
	newMgr.Init(tempDir)
	if prefix := newMgr.GetPromptPrefix(); prefix != testPrefix {
		t.Errorf("Expected reloaded PromptPrefix to be %q, got %q", testPrefix, prefix)
	}
}

func TestSettings_DefaultAllOff(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-alloff-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	if mgr.GetCustomModelOverrideEnabled() != false {
		t.Errorf("Expected CustomModelOverrideEnabled to default to false")
	}
	if mgr.GetCustomThinkingOverrideEnabled() != false {
		t.Errorf("Expected CustomThinkingOverrideEnabled to default to false")
	}
	if mgr.GetCustomThinkingSupports() != false {
		t.Errorf("Expected CustomThinkingSupports to default to false")
	}
	if mgr.GetCustomThinkingBudget() != 0 {
		t.Errorf("Expected CustomThinkingBudget to default to 0, got %d", mgr.GetCustomThinkingBudget())
	}
}

// TestNvidiaPreferredModels 覆盖全局级 NVIDIA 专属模型清单的默认值、Set/Get 往返、
// 去空去重、落盘与重载一致性、返回切片的内部隔离性。
func TestNvidiaPreferredModels(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-nvidia-preferred-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认应为空切片(非 nil),避免调用方判空歧义
	got := mgr.GetNvidiaPreferredModels()
	if len(got) != 0 {
		t.Errorf("Expected empty preferred models by default, got %v", got)
	}

	// 2. Set 后 Get 返回一致;去空去重
	want := []string{"moonshotai/kimi-k2.5", "  ", "nvidia/llama-3.1-nemotron-70b-instruct", "", "meta/llama-3.3-70b-instruct", "moonshotai/kimi-k2.5"}
	if err := mgr.SetNvidiaPreferredModels(want); err != nil {
		t.Fatalf("SetNvidiaPreferredModels failed: %v", err)
	}
	got = mgr.GetNvidiaPreferredModels()
	expected := []string{"moonshotai/kimi-k2.5", "nvidia/llama-3.1-nemotron-70b-instruct", "meta/llama-3.3-70b-instruct"}
	if len(got) != len(expected) {
		t.Fatalf("Expected %d deduped models, got %d: %v", len(expected), len(got), got)
	}
	for i, m := range expected {
		if got[i] != m {
			t.Errorf("Expected models[%d]=%q, got %q", i, m, got[i])
		}
	}

	// 3. 返回的是副本,外部修改不影响内存态
	got[0] = "tampered"
	again := mgr.GetNvidiaPreferredModels()
	if again[0] == "tampered" {
		t.Errorf("GetNvidiaPreferredModels should return a defensive copy, but mutation leaked")
	}

	// 4. 配置落盘,新 Manager 加载该清单
	configPath := filepath.Join(tempDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("Expected config.json persisted, stat err: %v", err)
	}
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	reloaded := mgr2.GetNvidiaPreferredModels()
	if len(reloaded) != len(expected) {
		t.Fatalf("Reloaded preferred models mismatch, got %v", reloaded)
	}
	for i, m := range expected {
		if reloaded[i] != m {
			t.Errorf("Reloaded models[%d]=%q, expected %q", i, reloaded[i], m)
		}
	}

	// 5. 清空清单
	if err := mgr2.SetNvidiaPreferredModels([]string{}); err != nil {
		t.Fatalf("Clear preferred models failed: %v", err)
	}
	if len(mgr2.GetNvidiaPreferredModels()) != 0 {
		t.Errorf("Expected empty after clear, got %v", mgr2.GetNvidiaPreferredModels())
	}
}

func TestEnableThinkingMode_AutoPersistToDisk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_test_persist_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.json")
	// 模拟旧版的 config.json(缺少 enableThinkingMode 属性)
	oldConfigJSON := []byte(`{
		"relayPort": "18444",
		"requestTimeout": 300
	}`)
	if err := os.WriteFile(configPath, oldConfigJSON, 0644); err != nil {
		t.Fatalf("Failed to write mock old config: %v", err)
	}

	// 实例化 Manager 并初始化(触发 loadConfig 自动补全与 SaveConfig 落盘)
	mgr := NewManager()
	mgr.Init(tempDir)

	if !mgr.GetEnableThinkingMode() {
		t.Errorf("Expected EnableThinkingMode == true in memory after loadConfig")
	}

	// 读取落盘后的 config.json 验证磁盘文件中已经真实持久化写入了 "enableThinkingMode": true
	diskData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read back config.json from disk: %v", err)
	}
	diskStr := string(diskData)
	if !strings.Contains(diskStr, `"enableThinkingMode": true`) {
		t.Errorf("Expected disk config.json to contain \"enableThinkingMode\": true, got:\n%s", diskStr)
	}
}

