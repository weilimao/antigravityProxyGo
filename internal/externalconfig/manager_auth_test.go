package externalconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupTempAuthAgent 在临时目录创建 config.toml 并注册 codex Agent。
func setupTempAuthAgent(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = 'test'\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	m := NewManager()
	m.RegisterAgent("codex", "Codex", configPath)
	return m, filepath.Join(dir, "auth.json")
}

// TestReadAuthFull_NotExist 验证文件不存在时返回空对象 "{}"。
func TestReadAuthFull_NotExist(t *testing.T) {
	m, _ := setupTempAuthAgent(t)
	content, err := m.ReadAuthFull("codex")
	if err != nil {
		t.Fatalf("读取不存在的 auth.json 不应报错: %v", err)
	}
	if content != "{}" {
		t.Fatalf("期望得到 \"{}\", 实际得到: %s", content)
	}
}

// TestWriteAndReadAuthFull 验证写入合法 auth.json 并能正确读取回显。
func TestWriteAndReadAuthFull(t *testing.T) {
	m, authPath := setupTempAuthAgent(t)
	rawJSON := `{"OPENAI_API_KEY":"sk-ant-test-key-123456"}`
	if err := m.WriteAuthFull("codex", rawJSON); err != nil {
		t.Fatalf("写入 auth.json 失败: %v", err)
	}

	readBack, err := m.ReadAuthFull("codex")
	if err != nil {
		t.Fatalf("读取 auth.json 失败: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(readBack), &parsed); err != nil {
		t.Fatalf("解析读回的 JSON 失败: %v", err)
	}
	if parsed["OPENAI_API_KEY"] != "sk-ant-test-key-123456" {
		t.Fatalf("OPENAI_API_KEY 不匹配: %v", parsed["OPENAI_API_KEY"])
	}

	// 再次写入，验证 .bak 备份生成
	rawJSON2 := `{"OPENAI_API_KEY":"sk-ant-new-key-789"}`
	if err := m.WriteAuthFull("codex", rawJSON2); err != nil {
		t.Fatalf("覆盖写入 auth.json 失败: %v", err)
	}

	bakData, err := os.ReadFile(authPath + ".bak")
	if err != nil {
		t.Fatalf("备份文件应存在: %v", err)
	}
	if string(bakData) != rawJSON {
		t.Fatalf("备份文件内容不匹配: %s", string(bakData))
	}
}

// TestWriteAuthFull_InvalidJSON 验证非法 JSON 写入被拒绝。
func TestWriteAuthFull_InvalidJSON(t *testing.T) {
	m, _ := setupTempAuthAgent(t)
	if err := m.WriteAuthFull("codex", "{invalid-json"); err == nil {
		t.Fatalf("非法 JSON 应该报错被拦截")
	}
}
