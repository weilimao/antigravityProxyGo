package externalconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTempCatalogAgent 在临时目录创建声明了 catalog 的 config.toml 并注册 codex Agent。
func setupTempCatalogAgent(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	configContent := "model_catalog_json = 'catalog.json'\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	m := NewManager()
	m.RegisterAgent("codex", "Codex", configPath)
	return m, filepath.Join(dir, "catalog.json")
}

const cleanCatalogJSON = `{
  "models": [
    {
      "slug": "test/model-a",
      "display_name": "Model A",
      "supported_reasoning_levels": [
        {"effort": "none", "description": "Disable Thinking"},
        {"effort": "high", "description": "High Thinking Effort"}
      ],
      "truncation_policy": {"limit": 10000, "mode": "bytes"}
    }
  ]
}`

const corruptedCatalogJSON = `{
  "models": [
    {
      "slug": "test/model-a",
      "display_name": "Model A",
      "supported_reasoning_levels": [
        "[object Object]",
        "[object Object]"
      ],
      "truncation_policy": "[object Object]"
    }
  ]
}`

// TestWriteModelCatalogFull_CleanJSON 验证合法 JSON 可正常落盘。
func TestWriteModelCatalogFull_CleanJSON(t *testing.T) {
	m, catalogPath := setupTempCatalogAgent(t)
	if err := m.WriteModelCatalogFull("codex", cleanCatalogJSON); err != nil {
		t.Fatalf("合法 JSON 应写入成功: %v", err)
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("catalog 文件应存在: %v", err)
	}
	if !strings.Contains(string(data), `"effort": "high"`) {
		t.Fatalf("catalog 内容应包含思考等级对象")
	}
}

// TestWriteModelCatalogFull_CorruptedJSON 验证含 "[object Object]" 的 JSON 被拒绝且不落盘。
func TestWriteModelCatalogFull_CorruptedJSON(t *testing.T) {
	m, catalogPath := setupTempCatalogAgent(t)
	err := m.WriteModelCatalogFull("codex", corruptedCatalogJSON)
	if err == nil {
		t.Fatal("损坏 JSON 应被拒绝写入")
	}
	if !strings.Contains(err.Error(), "[object Object]") {
		t.Fatalf("错误信息应提及 [object Object], got: %v", err)
	}
	if _, statErr := os.Stat(catalogPath); !os.IsNotExist(statErr) {
		t.Fatal("损坏 JSON 不得落盘")
	}
}

// TestWriteModelCatalogFull_BackupCreated 验证覆盖已有文件时生成 .bak 备份。
func TestWriteModelCatalogFull_BackupCreated(t *testing.T) {
	m, catalogPath := setupTempCatalogAgent(t)
	if err := m.WriteModelCatalogFull("codex", cleanCatalogJSON); err != nil {
		t.Fatalf("第一次写入: %v", err)
	}
	if err := m.WriteModelCatalogFull("codex", cleanCatalogJSON); err != nil {
		t.Fatalf("第二次写入: %v", err)
	}
	if _, err := os.Stat(catalogPath + ".bak"); err != nil {
		t.Fatalf("覆盖写入应生成 .bak 备份: %v", err)
	}
}

// TestContainsCorruptedObjectString 覆盖递归检测的各类值形态。
func TestContainsCorruptedObjectString(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		want bool
	}{
		{"plain string", "high", false},
		{"corrupted string", "[object Object]", true},
		{"corrupted with spaces", "  [object Object]  ", true},
		{"nested array", []interface{}{"a", []interface{}{"[object Object]"}}, true},
		{"nested map", map[string]interface{}{"k": map[string]interface{}{"k2": "[object Object]"}}, true},
		{"clean nested", map[string]interface{}{"k": []interface{}{"none", "high"}}, false},
		{"number", float64(3), false},
		{"boolean", true, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := containsCorruptedObjectString(c.val); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
