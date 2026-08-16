package externalconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterAndListAgents(t *testing.T) {
	m := NewManager()
	m.RegisterAgent("opencode", "OpenCode", "/tmp/opencode.json")
	m.RegisterAgent("claude-code", "Claude Code", "/tmp/claude.json")
	m.RegisterAgent("codex", "Codex", "/tmp/codex.json")

	agents := m.ListAgents()
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}

	agent, err := m.GetAgent("opencode")
	if err != nil {
		t.Fatalf("GetAgent opencode failed: %v", err)
	}
	if agent.DisplayName != "OpenCode" {
		t.Errorf("expected display name 'OpenCode', got '%s'", agent.DisplayName)
	}
	if agent.FileExists {
		t.Error("expected FileExists=false for non-existent file")
	}

	claudeAgent, err := m.GetAgent("claude-code")
	if err != nil {
		t.Fatalf("GetAgent claude-code failed: %v", err)
	}
	if claudeAgent.DisplayName != "Claude Code" {
		t.Errorf("expected display name 'Claude Code', got '%s'", claudeAgent.DisplayName)
	}

	codexAgent, err := m.GetAgent("codex")
	if err != nil {
		t.Fatalf("GetAgent codex failed: %v", err)
	}
	if codexAgent.DisplayName != "Codex" {
		t.Errorf("expected display name 'Codex', got '%s'", codexAgent.DisplayName)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	m := NewManager()
	_, err := m.GetAgent("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent agent")
	}
}

func TestReadConfigFileNotExists(t *testing.T) {
	m := NewManager()
	m.RegisterAgent("test", "Test", "/tmp/nonexistent-config-12345.json")

	data, err := m.ReadConfig("test")
	if err != nil {
		t.Fatalf("ReadConfig should not error for missing file: %v", err)
	}
	if data != "{}" {
		t.Errorf("expected '{}' for missing file, got '%s'", data)
	}
}

func TestWriteAndReadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	jsonStr := `{"$schema":"https://example.com/schema.json","model":"anthropic/claude-sonnet-4-5"}`

	if err := m.WriteConfig("test", jsonStr); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file should exist after write: %v", err)
	}

	readStr, err := m.ReadConfig("test")
	if err != nil {
		t.Fatalf("ReadConfig failed: %v", err)
	}
	if readStr != jsonStr {
		t.Errorf("read content mismatch:\nexpected: %s\ngot: %s", jsonStr, readStr)
	}
}

func TestWriteConfigCreatesBackup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "backup-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	originalJSON := `{"version":1}`
	if err := m.WriteConfig("test", originalJSON); err != nil {
		t.Fatalf("first WriteConfig failed: %v", err)
	}

	updatedJSON := `{"version":2}`
	if err := m.WriteConfig("test", updatedJSON); err != nil {
		t.Fatalf("second WriteConfig failed: %v", err)
	}

	bakPath := configPath + ".bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file should exist: %v", err)
	}
	if string(bakData) != originalJSON {
		t.Errorf("backup content mismatch:\nexpected: %s\ngot: %s", originalJSON, string(bakData))
	}

	curData, _ := os.ReadFile(configPath)
	if string(curData) != updatedJSON {
		t.Errorf("current content mismatch:\nexpected: %s\ngot: %s", updatedJSON, string(curData))
	}
}

func TestRestoreBackup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "restore-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	originalJSON := `{"original":true}`
	if err := m.WriteConfig("test", originalJSON); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	updatedJSON := `{"original":false}`
	if err := m.WriteConfig("test", updatedJSON); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	if err := m.RestoreBackup("test"); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	data, err := m.ReadConfig("test")
	if err != nil {
		t.Fatalf("ReadConfig after restore failed: %v", err)
	}
	if data != originalJSON {
		t.Errorf("restored content mismatch:\nexpected: %s\ngot: %s", originalJSON, data)
	}
}

func TestRestoreBackupNoBackup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "no-backup-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	err := m.RestoreBackup("test")
	if err == nil {
		t.Error("expected error when no backup exists")
	}
}

func TestWriteConfigInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid-json-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	err := m.WriteConfig("test", `{invalid json}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestWriteConfigCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "nested", "config.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	if err := m.WriteConfig("test", `{"ok":true}`); err != nil {
		t.Fatalf("WriteConfig should create nested directories: %v", err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
}

func TestReadConfigWithBOM(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "bom-test.json")

	// Write file with UTF-8 BOM
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := []byte(`{"key":"value"}`)
	fullContent := append(bom, content...)
	if err := os.WriteFile(configPath, fullContent, 0644); err != nil {
		t.Fatalf("failed to write BOM file: %v", err)
	}

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	data, err := m.ReadConfig("test")
	if err != nil {
		t.Fatalf("ReadConfig failed: %v", err)
	}
	expected := `{"key":"value"}`
	if data != expected {
		t.Errorf("BOM should be stripped:\nexpected: %s\ngot: %s", expected, data)
	}
}

func TestReloadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "reload-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	if err := m.WriteConfig("test", `{"v":1}`); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	data, err := m.ReloadConfig("test")
	if err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}
	if data != `{"v":1}` {
		t.Errorf("reload content mismatch: got %s", data)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "concurrent-test.json")

	m := NewManager()
	m.RegisterAgent("test", "Test", configPath)

	done := make(chan bool, 10)

	for i := 0; i < 5; i++ {
		go func(idx int) {
			jsonStr := `{"index":` + string(rune('0'+idx)) + `}`
			_ = m.WriteConfig("test", jsonStr)
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		go func() {
			_, _ = m.ReadConfig("test")
			_ = m.ListAgents()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestWriteAndReadTomlConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	m := NewManager()
	m.RegisterAgent("codex", "Codex", configPath)

	// 写入初始 JSON
	inputJSON := `{"model":"nvidia/z-ai/glm-5.2","model_provider":"custom","model_reasoning_effort":"max"}`
	if err := m.WriteConfig("codex", inputJSON); err != nil {
		t.Fatalf("WriteConfig for toml failed: %v", err)
	}

	// 验证磁盘上实际写入的是 TOML 格式文件
	rawToml, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read written toml file: %v", err)
	}
	tomlContent := string(rawToml)
	if tomlContent == "" {
		t.Fatal("written toml file should not be empty")
	}

	// 读取配置，应透明转为 JSON
	readJSON, err := m.ReadConfig("codex")
	if err != nil {
		t.Fatalf("ReadConfig for toml failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(readJSON), &parsed); err != nil {
		t.Fatalf("ReadConfig did not return valid JSON: %v", err)
	}

	if parsed["model"] != "nvidia/z-ai/glm-5.2" {
		t.Errorf("expected model 'nvidia/z-ai/glm-5.2', got '%v'", parsed["model"])
	}
	if parsed["model_provider"] != "custom" {
		t.Errorf("expected model_provider 'custom', got '%v'", parsed["model_provider"])
	}
}

func TestTomlConfigBackupAndRestore(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	m := NewManager()
	m.RegisterAgent("codex", "Codex", configPath)

	origJSON := `{"model":"gpt-4o"}`
	if err := m.WriteConfig("codex", origJSON); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	updatedJSON := `{"model":"o3-mini"}`
	if err := m.WriteConfig("codex", updatedJSON); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	// 恢复备份
	if err := m.RestoreBackup("codex"); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	readJSON, err := m.ReadConfig("codex")
	if err != nil {
		t.Fatalf("ReadConfig after restore failed: %v", err)
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal([]byte(readJSON), &parsed)
	if parsed["model"] != "gpt-4o" {
		t.Errorf("expected restored model 'gpt-4o', got '%v'", parsed["model"])
	}
}
