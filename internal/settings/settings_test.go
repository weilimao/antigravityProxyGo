package settings

import (
	"os"
	"path/filepath"
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

