package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateSettings_MultiEditorAndRestore(t *testing.T) {
	appData := t.TempDir()
	homeDir := t.TempDir()

	// 模拟创建 VS Code, Cursor, Antigravity IDE 的 settings.json
	editorDirs := []string{
		filepath.Join(appData, "Antigravity IDE", "User"),
		filepath.Join(appData, "Code", "User"),
		filepath.Join(appData, "Cursor", "User"),
		filepath.Join(appData, "Windsurf", "User"),
	}

	for _, dir := range editorDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create dir %s: %v", dir, err)
		}
		initialSettings := map[string]interface{}{
			"editor.fontSize": 14,
		}
		data, _ := json.Marshal(initialSettings)
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644); err != nil {
			t.Fatalf("failed to write settings: %v", err)
		}
	}

	// 1. 测试启用注入 (enable = true)
	if err := UpdateSettings(true, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(true) error: %v", err)
	}

	for _, dir := range editorDirs {
		content, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatalf("failed to read settings from %s: %v", dir, err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to unmarshal JSON from %s: %v", dir, err)
		}
		if url, ok := parsed["jetski.cloudCodeUrl"].(string); !ok || url != "http://127.0.0.1:18443" {
			t.Errorf("Expected jetski.cloudCodeUrl in %s to be http://127.0.0.1:18443, got %v", dir, parsed["jetski.cloudCodeUrl"])
		}
		if proxy, ok := parsed["http.proxy"].(string); !ok || proxy != "http://127.0.0.1:18443" {
			t.Errorf("Expected http.proxy in %s to be http://127.0.0.1:18443, got %v", dir, parsed["http.proxy"])
		}
		if noProxy, ok := parsed["http.noProxy"].([]interface{}); !ok {
			t.Errorf("Expected http.noProxy in %s to be a JSON array (IDE crashes on string), got %T: %v", dir, parsed["http.noProxy"], parsed["http.noProxy"])
		} else if len(noProxy) != 2 || noProxy[0] != "localhost" || noProxy[1] != "127.0.0.1" {
			t.Errorf("Expected http.noProxy in %s to be [localhost 127.0.0.1], got %v", dir, noProxy)
		}
		if fontSize, ok := parsed["editor.fontSize"].(float64); !ok || fontSize != 14 {
			t.Errorf("Expected editor.fontSize in %s to be preserved as 14, got %v", dir, parsed["editor.fontSize"])
		}
	}

	// 2. 测试停用恢复 (enable = false)
	if err := UpdateSettings(false, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(false) error: %v", err)
	}

	for _, dir := range editorDirs {
		content, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatalf("failed to read settings from %s: %v", dir, err)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("failed to unmarshal JSON from %s: %v", dir, err)
		}
		if _, exists := parsed["jetski.cloudCodeUrl"]; exists {
			t.Errorf("Expected jetski.cloudCodeUrl to be deleted in %s, but still exists", dir)
		}
		if _, exists := parsed["http.proxy"]; exists {
			t.Errorf("Expected http.proxy to be deleted in %s, but still exists", dir)
		}
		if _, exists := parsed["http.noProxy"]; exists {
			t.Errorf("Expected http.noProxy to be deleted in %s, but still exists", dir)
		}
		if fontSize, ok := parsed["editor.fontSize"].(float64); !ok || fontSize != 14 {
			t.Errorf("Expected editor.fontSize in %s to be preserved as 14, got %v", dir, parsed["editor.fontSize"])
		}
	}
}

// TestUpdateSettings_MigratesLegacyStringNoProxy 覆盖历史坏值迁移：
// 旧版本曾把 http.noProxy 写成逗号分隔字符串，IDE 读取时会在
// `getValue("http.noProxy").map(...)` 抛 TypeError 并无法创建窗口。
// 启用注入时必须把它纠正为数组，且不能丢失用户自定义的绕过主机。
func TestUpdateSettings_MigratesLegacyStringNoProxy(t *testing.T) {
	appData := t.TempDir()
	homeDir := t.TempDir()

	legacyDir := filepath.Join(appData, "Antigravity IDE", "User")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	settingsPath := filepath.Join(legacyDir, "settings.json")
	legacy := []byte(`{
  "http.proxy": "http://127.0.0.1:18443",
  "http.noProxy": "corp.internal, localhost,127.0.0.1",
  "editor.fontSize": 18
}`)
	if err := os.WriteFile(settingsPath, legacy, 0644); err != nil {
		t.Fatalf("failed to write legacy settings: %v", err)
	}

	if err := UpdateSettings(true, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(true) error: %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("failed to parse settings: %v", err)
	}

	noProxy, ok := parsed["http.noProxy"].([]interface{})
	if !ok {
		t.Fatalf("legacy string http.noProxy was not migrated to array, got %T: %v", parsed["http.noProxy"], parsed["http.noProxy"])
	}
	got := make([]string, 0, len(noProxy))
	for _, v := range noProxy {
		s, _ := v.(string)
		got = append(got, s)
	}
	want := []string{"corp.internal", "localhost", "127.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("expected %d hosts %v after migration, got %d hosts %v", len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("host[%d]: expected %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}
	if fontSize, ok := parsed["editor.fontSize"].(float64); !ok || fontSize != 18 {
		t.Errorf("expected editor.fontSize preserved as 18, got %v", parsed["editor.fontSize"])
	}

	// 停用后：用户自定义过主机列表，不应被整体删除
	if err := UpdateSettings(false, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(false) error: %v", err)
	}
	after, _ := os.ReadFile(settingsPath)
	var parsedAfter map[string]interface{}
	if err := json.Unmarshal(after, &parsedAfter); err != nil {
		t.Fatalf("failed to parse settings after disable: %v", err)
	}
	if _, exists := parsedAfter["http.noProxy"]; !exists {
		t.Errorf("expected custom http.noProxy to survive disable, but it was deleted")
	}
	if _, exists := parsedAfter["http.proxy"]; exists {
		t.Errorf("expected http.proxy to be removed on disable")
	}
}

// TestUpdateSettings_RemovesDefaultNoProxyOnDisable 覆盖停用时对「工具注入的默认值」的清理，
// 要求数组与历史字符串两种形态都能被识别并移除。
func TestUpdateSettings_RemovesDefaultNoProxyOnDisable(t *testing.T) {
	cases := map[string]string{
		"array":  `{"http.proxy":"http://127.0.0.1:18443","http.noProxy":["localhost","127.0.0.1"]}`,
		"string": `{"http.proxy":"http://127.0.0.1:18443","http.noProxy":"localhost,127.0.0.1"}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			appData := t.TempDir()
			homeDir := t.TempDir()
			dir := filepath.Join(appData, "Code", "User")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatalf("failed to create dir: %v", err)
			}
			settingsPath := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(settingsPath, []byte(raw), 0644); err != nil {
				t.Fatalf("failed to write settings: %v", err)
			}

			if err := UpdateSettings(false, appData, homeDir); err != nil {
				t.Fatalf("UpdateSettings(false) error: %v", err)
			}

			content, _ := os.ReadFile(settingsPath)
			var parsed map[string]interface{}
			if err := json.Unmarshal(content, &parsed); err != nil {
				t.Fatalf("failed to parse settings: %v", err)
			}
			if _, exists := parsed["http.noProxy"]; exists {
				t.Errorf("expected default http.noProxy to be removed on disable, got %v", parsed["http.noProxy"])
			}
		})
	}
}

func TestUpdateSettings_JSONCAndEdgeCases(t *testing.T) {
	appData := t.TempDir()
	homeDir := t.TempDir()

	// 1. JSONC 含单行与多行注释及尾随逗号的复杂 VS Code 配置
	codeDir := filepath.Join(appData, "Code", "User")
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	jsoncContent := []byte(`{
		// 开发者自定义配置
		/* 多行注释说明
		   代理服务设置 */
		"editor.tabSize": 2,
		"files.autoSave": "afterDelay",
	}`)
	if err := os.WriteFile(filepath.Join(codeDir, "settings.json"), jsoncContent, 0644); err != nil {
		t.Fatalf("failed to write settings: %v", err)
	}

	// 2. Cursor 空白文件 (0 字节)
	cursorDir := filepath.Join(appData, "Cursor", "User")
	if err := os.MkdirAll(cursorDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "settings.json"), []byte("   \n\t"), 0644); err != nil {
		t.Fatalf("failed to write empty settings: %v", err)
	}

	// 3. Windsurf 仅有目录无 settings.json 文件（自动初始化创建）
	windsurfDir := filepath.Join(appData, "Windsurf", "User")
	if err := os.MkdirAll(windsurfDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	// 执行启用注入
	if err := UpdateSettings(true, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(true) error: %v", err)
	}

	// 验证 1: JSONC 成功解析并注入
	codeContent, err := os.ReadFile(filepath.Join(codeDir, "settings.json"))
	if err != nil {
		t.Fatalf("failed to read Code settings: %v", err)
	}
	var parsedCode map[string]interface{}
	if err := json.Unmarshal(codeContent, &parsedCode); err != nil {
		t.Fatalf("failed to parse Code JSON after patch: %v", err)
	}
	if parsedCode["jetski.cloudCodeUrl"] != "http://127.0.0.1:18443" {
		t.Errorf("Expected jetski.cloudCodeUrl injected in Code, got %v", parsedCode["jetski.cloudCodeUrl"])
	}
	if parsedCode["editor.tabSize"] != float64(2) {
		t.Errorf("Expected editor.tabSize preserved as 2, got %v", parsedCode["editor.tabSize"])
	}

	// 验证 2: 空文件成功初始化并注入
	cursorContent, err := os.ReadFile(filepath.Join(cursorDir, "settings.json"))
	if err != nil {
		t.Fatalf("failed to read Cursor settings: %v", err)
	}
	var parsedCursor map[string]interface{}
	if err := json.Unmarshal(cursorContent, &parsedCursor); err != nil {
		t.Fatalf("failed to parse Cursor JSON after patch: %v", err)
	}
	if parsedCursor["jetski.cloudCodeUrl"] != "http://127.0.0.1:18443" {
		t.Errorf("Expected jetski.cloudCodeUrl in Cursor, got %v", parsedCursor["jetski.cloudCodeUrl"])
	}

	// 验证 3: Windsurf 自动初始化文件
	windsurfContent, err := os.ReadFile(filepath.Join(windsurfDir, "settings.json"))
	if err != nil {
		t.Fatalf("failed to read Windsurf settings: %v", err)
	}
	var parsedWindsurf map[string]interface{}
	if err := json.Unmarshal(windsurfContent, &parsedWindsurf); err != nil {
		t.Fatalf("failed to parse Windsurf JSON: %v", err)
	}
	if parsedWindsurf["jetski.cloudCodeUrl"] != "http://127.0.0.1:18443" {
		t.Errorf("Expected jetski.cloudCodeUrl in Windsurf, got %v", parsedWindsurf["jetski.cloudCodeUrl"])
	}

	// 执行清理恢复
	if err := UpdateSettings(false, appData, homeDir); err != nil {
		t.Fatalf("UpdateSettings(false) error: %v", err)
	}

	// 验证清理结果
	codeContentAfter, _ := os.ReadFile(filepath.Join(codeDir, "settings.json"))
	var codeAfter map[string]interface{}
	_ = json.Unmarshal(codeContentAfter, &codeAfter)
	if _, exists := codeAfter["jetski.cloudCodeUrl"]; exists {
		t.Errorf("Expected jetski.cloudCodeUrl removed from Code after teardown")
	}
	if _, exists := codeAfter["http.proxy"]; exists {
		t.Errorf("Expected http.proxy removed from Code after teardown")
	}
	if _, exists := codeAfter["http.noProxy"]; exists {
		t.Errorf("Expected http.noProxy removed from Code after teardown")
	}
}

func TestUpdateSettings_CustomDataDirAndRoamingResolution(t *testing.T) {
	customDataDir := t.TempDir()
	mockHomeDir := t.TempDir()

	// 模拟 Windows Roaming 目录与通用目录
	roamingCodeDir := filepath.Join(mockHomeDir, "AppData", "Roaming", "Code", "User")
	if err := os.MkdirAll(roamingCodeDir, 0755); err != nil {
		t.Fatalf("failed to create roaming code dir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(roamingCodeDir, "settings.json"), []byte(`{"editor.fontSize":12}`), 0644)

	customCodeDir := filepath.Join(customDataDir, "Code", "User")
	if err := os.MkdirAll(customCodeDir, 0755); err != nil {
		t.Fatalf("failed to create custom code dir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(customCodeDir, "settings.json"), []byte(`{"editor.fontSize":16}`), 0644)

	// 启用注入
	if err := UpdateSettings(true, customDataDir, mockHomeDir); err != nil {
		t.Fatalf("UpdateSettings failed: %v", err)
	}

	// 验证两处设置均已成功注入 jetski.cloudCodeUrl
	for name, path := range map[string]string{"roaming": roamingCodeDir, "custom": customCodeDir} {
		content, err := os.ReadFile(filepath.Join(path, "settings.json"))
		if err != nil {
			t.Fatalf("failed to read %s settings: %v", name, err)
		}
		var parsed map[string]interface{}
		_ = json.Unmarshal(content, &parsed)
		if parsed["jetski.cloudCodeUrl"] != "http://127.0.0.1:18443" {
			t.Errorf("Expected %s settings to have jetski.cloudCodeUrl, got %v", name, parsed["jetski.cloudCodeUrl"])
		}
	}

	// 停用清理
	if err := UpdateSettings(false, customDataDir, mockHomeDir); err != nil {
		t.Fatalf("UpdateSettings(false) failed: %v", err)
	}

	for name, path := range map[string]string{"roaming": roamingCodeDir, "custom": customCodeDir} {
		content, _ := os.ReadFile(filepath.Join(path, "settings.json"))
		var parsed map[string]interface{}
		_ = json.Unmarshal(content, &parsed)
		if _, exists := parsed["jetski.cloudCodeUrl"]; exists {
			t.Errorf("Expected %s settings to have jetski.cloudCodeUrl deleted", name)
		}
		if _, exists := parsed["http.proxy"]; exists {
			t.Errorf("Expected %s settings to have http.proxy deleted", name)
		}
		if _, exists := parsed["http.noProxy"]; exists {
			t.Errorf("Expected %s settings to have http.noProxy deleted", name)
		}
	}
}
