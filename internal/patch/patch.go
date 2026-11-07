package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// defaultNoProxyHosts 是注入到 IDE「本地代理绕过」设置中的默认主机列表。
//
// 必须以数组形式写入：VS Code / Antigravity IDE 的主进程按强类型数组读取该项
// （`(configurationService.getValue("http.noProxy") || []).map(...)`），一旦写入
// 逗号分隔的字符串，`.map` 不存在会抛 TypeError，导致 IDE 启动时在
// openInBrowserWindow 阶段中断且不创建任何窗口。
var defaultNoProxyHosts = []string{"localhost", "127.0.0.1"}

// parseNoProxyHosts 将任意形态的 http.noProxy 取值解析为去重后的主机切片。
// 兼容原生数组与历史遗留的逗号分隔字符串（旧版本曾写入字符串，会让 IDE 崩溃）。
func parseNoProxyHosts(raw interface{}) []string {
	var parts []string
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
	case []string:
		parts = append(parts, v...)
	case string:
		parts = append(parts, strings.Split(v, ",")...)
	}

	hosts := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		hosts = append(hosts, p)
	}
	return hosts
}

// mergeNoProxyHosts 返回在 existing 基础上补齐 required 后的切片，以及内容是否发生变化。
func mergeNoProxyHosts(existing []string, required ...string) ([]string, bool) {
	merged := append([]string(nil), existing...)
	seen := make(map[string]bool, len(existing))
	for _, h := range existing {
		seen[h] = true
	}
	changed := false
	for _, h := range required {
		if seen[h] {
			continue
		}
		merged = append(merged, h)
		seen[h] = true
		changed = true
	}
	return merged, changed
}

// isStringSlice 判断经 JSON 反序列化后得到的值是否为字符串切片。
// encoding/json 会把 JSON 数组解析为 []interface{}，内存中直接构造的值则是 []string，
// 两种形态都需要被识别为「已经是正确的数组类型」。
func isStringSlice(raw interface{}) bool {
	switch v := raw.(type) {
	case []string:
		return true
	case []interface{}:
		for _, item := range v {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// hostEditorDirNames 是插件宿主编辑器的配置目录名。
// 这些编辑器只需要 jetski.cloudCodeUrl 把 Cloud Code 指到本地代理。
// 禁止写入 http.proxy：那会把编辑器自己的流量（例如 Cursor 的 api2.cursor.sh）整机拖进 18443。
var hostEditorDirNames = []string{
	"Code",
	"Code - Insiders",
	"Cursor",
	"Windsurf",
	"Trae",
	"VSCodium",
	"Positron",
}

// isHostEditorSettings 判断路径是不是宿主编辑器的 User/settings.json。
func isHostEditorSettings(path string) bool {
	cleaned := filepath.Clean(path)
	for _, name := range hostEditorDirNames {
		suffix := string(filepath.Separator) + filepath.Join(name, "User", "settings.json")
		if len(cleaned) >= len(suffix) && strings.EqualFold(cleaned[len(cleaned)-len(suffix):], suffix) {
			return true
		}
	}
	return false
}

// isDefaultNoProxy 判断当前取值是否恰好等于工具注入的默认值，用于停用时安全移除，
// 避免误删用户自定义的绕过列表。
func isDefaultNoProxy(raw interface{}) bool {
	hosts := parseNoProxyHosts(raw)
	if len(hosts) != len(defaultNoProxyHosts) {
		return false
	}
	set := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		set[h] = true
	}
	for _, d := range defaultNoProxyHosts {
		if !set[d] {
			return false
		}
	}
	return true
}

// UpdateSettings updates jetski.cloudCodeUrl in IDE, plugin host editors, and Agent settings files
func UpdateSettings(enable bool, appData, homeDir string) error {
	var targetPaths []string

	// 收集候选的 AppData 根目录列表
	var appDataDirs []string
	if appData != "" {
		appDataDirs = append(appDataDirs, appData)
	}
	if runtime.GOOS == "windows" {
		if homeDir != "" {
			roaming := filepath.Join(homeDir, "AppData", "Roaming")
			found := false
			for _, d := range appDataDirs {
				if strings.EqualFold(filepath.Clean(d), filepath.Clean(roaming)) {
					found = true
					break
				}
			}
			if !found {
				appDataDirs = append(appDataDirs, roaming)
			}
		}
	}

	// 1. 桌面独立版 Antigravity IDE 及 Agent 设置路径
	for _, dir := range appDataDirs {
		targetPaths = append(targetPaths,
			filepath.Join(dir, "Antigravity IDE", "User", "settings.json"),
			filepath.Join(dir, "Antigravity", "User", "settings.json"),
			filepath.Join(dir, "Antigravity-Agent", "User", "settings.json"),
		)
	}

	// 2. 新版 IDE 插件运行的常见宿主编辑器（VS Code、Cursor、Windsurf、Trae、VSCodium 等）设置路径
	for _, dir := range appDataDirs {
		targetPaths = append(targetPaths,
			filepath.Join(dir, "Code", "User", "settings.json"),
			filepath.Join(dir, "Code - Insiders", "User", "settings.json"),
			filepath.Join(dir, "Cursor", "User", "settings.json"),
			filepath.Join(dir, "Windsurf", "User", "settings.json"),
			filepath.Join(dir, "Trae", "User", "settings.json"),
			filepath.Join(dir, "VSCodium", "User", "settings.json"),
			filepath.Join(dir, "Positron", "User", "settings.json"),
		)
	}

	if homeDir != "" {
		// macOS 平台 Application Support 路径
		targetPaths = append(targetPaths,
			filepath.Join(homeDir, "Library", "Application Support", "Antigravity IDE", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Antigravity", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Code", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Code - Insiders", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Windsurf", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Trae", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "VSCodium", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Positron", "User", "settings.json"),
		)

		// Linux 平台 .config 路径
		targetPaths = append(targetPaths,
			filepath.Join(homeDir, ".config", "Antigravity IDE", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Antigravity", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Code", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Code - Insiders", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Cursor", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Windsurf", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Trae", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "VSCodium", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Positron", "User", "settings.json"),
		)

		// CLI 与 Agent 工具路径
		targetPaths = append(targetPaths,
			filepath.Join(homeDir, ".antigravity", "settings.json"),
			filepath.Join(homeDir, ".antigravity-agent", "settings.json"),
			filepath.Join(homeDir, ".gemini", "antigravity-cli", "settings.json"),
			filepath.Join(homeDir, ".gemini", "settings.json"),
		)
	}

	// 路径去重，防止重复处理
	seenPaths := make(map[string]bool)
	var uniquePaths []string
	for _, p := range targetPaths {
		cleaned := filepath.Clean(p)
		if !seenPaths[cleaned] {
			seenPaths[cleaned] = true
			uniquePaths = append(uniquePaths, cleaned)
		}
	}
	targetPaths = uniquePaths

	updateFile := func(path string) bool {
		dir := filepath.Dir(path)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// 若目标文件不存在，但宿主编辑器目录存在，且当前为启用模式，则初始化创建配置
			if enable {
				editorBaseDir := filepath.Dir(dir)
				targetDirExists := false
				if _, dirErr := os.Stat(dir); dirErr == nil {
					targetDirExists = true
				} else if _, baseErr := os.Stat(editorBaseDir); baseErr == nil {
					if errMk := os.MkdirAll(dir, 0755); errMk == nil {
						targetDirExists = true
					}
				}
				if targetDirExists {
					initSettings := map[string]interface{}{
						"jetski.cloudCodeUrl":                "http://127.0.0.1:18443",
						"antigravity.serverStartupTimeoutMs": 60000,
					}
					if !isHostEditorSettings(path) {
						initSettings["http.proxy"] = "http://127.0.0.1:18443"
						initSettings["http.noProxy"] = append([]string(nil), defaultNoProxyHosts...)
					}
					if bytesData, err := json.MarshalIndent(initSettings, "", "  "); err == nil {
						_ = os.WriteFile(path, bytesData, 0644)
						return true
					}
				}
			}
			return false
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		var settings map[string]interface{}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			settings = make(map[string]interface{})
		} else if err := json.Unmarshal(data, &settings); err != nil {
			// VS Code / Cursor 等常用 settings.json 包含注释 (JSONC) 或尾部逗号，尝试清洗后反序列化
			cleaned := cleanJSONC(data)
			if errClean := json.Unmarshal(cleaned, &settings); errClean != nil {
				return false
			}
		}

		if settings == nil {
			settings = make(map[string]interface{})
		}

		changed := false
		if enable {
			if curr, ok := settings["jetski.cloudCodeUrl"].(string); !ok || curr != "http://127.0.0.1:18443" {
				settings["jetski.cloudCodeUrl"] = "http://127.0.0.1:18443"
				changed = true
			}
			if currTimeout, ok := settings["antigravity.serverStartupTimeoutMs"].(float64); !ok || currTimeout < 60000 {
				settings["antigravity.serverStartupTimeoutMs"] = 60000
				changed = true
			}
			if isHostEditorSettings(path) {
				// 历史版本曾把 http.proxy 写进 Cursor / VS Code 等宿主，这里顺手清掉。
				// 只删除本工具写入的 18443，用户自己的代理配置保留。
				if currProxy, ok := settings["http.proxy"].(string); ok && currProxy == "http://127.0.0.1:18443" {
					delete(settings, "http.proxy")
					changed = true
				}
				if _, hasProxy := settings["http.proxy"]; !hasProxy && isDefaultNoProxy(settings["http.noProxy"]) {
					delete(settings, "http.noProxy")
					changed = true
				}
			} else {
				if currProxy, ok := settings["http.proxy"].(string); !ok || currProxy != "http://127.0.0.1:18443" {
					settings["http.proxy"] = "http://127.0.0.1:18443"
					changed = true
				}
				// http.noProxy 必须是数组。历史版本写入的是逗号分隔字符串，会让 IDE 主进程
				// 在 getValue("http.noProxy").map(...) 处抛 TypeError 并无法创建窗口，
				// 因此这里无条件按数组回写，顺带修正已存在的历史坏值。
				currHosts := parseNoProxyHosts(settings["http.noProxy"])
				mergedHosts, hostsChanged := mergeNoProxyHosts(currHosts, defaultNoProxyHosts...)
				if hostsChanged || !isStringSlice(settings["http.noProxy"]) {
					settings["http.noProxy"] = mergedHosts
					changed = true
				}
			}
			if !changed {
				return true
			}
		} else {
			if _, exists := settings["jetski.cloudCodeUrl"]; exists {
				delete(settings, "jetski.cloudCodeUrl")
				changed = true
			}
			if _, exists := settings["antigravity.serverStartupTimeoutMs"]; exists {
				delete(settings, "antigravity.serverStartupTimeoutMs")
				changed = true
			}
			if currProxy, ok := settings["http.proxy"].(string); ok && currProxy == "http://127.0.0.1:18443" {
				delete(settings, "http.proxy")
				changed = true
			}
			if isDefaultNoProxy(settings["http.noProxy"]) {
				delete(settings, "http.noProxy")
				changed = true
			}
			if !changed {
				return true
			}
		}

		bytesData, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return false
		}

		_ = os.WriteFile(path, bytesData, 0644)
		return true
	}

	// 遍历更新所有检测到的现有配置文件，确保多编辑器/新旧插件环境均能生效
	for _, p := range targetPaths {
		_ = updateFile(p)
	}

	return nil
}

// cleanJSONC 过滤 JSON 中的单行注释(//)、多行注释(/* */)以及对象/数组尾随逗号(trailing commas)，
// 确保 VS Code / Cursor 的标准 settings.json 可以被 Go encoding/json 正常解析。
func cleanJSONC(data []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	n := len(data)

	for i := 0; i < n; i++ {
		b := data[i]

		if inString {
			out.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		if b == '"' {
			inString = true
			out.WriteByte(b)
			continue
		}

		// 单行注释 //
		if b == '/' && i+1 < n && data[i+1] == '/' {
			i += 2
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			if i < n {
				out.WriteByte(data[i])
			}
			continue
		}

		// 多行注释 /* */
		if b == '/' && i+1 < n && data[i+1] == '*' {
			i += 2
			for i+1 < n && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++ // 跳过结尾的 '/'
			continue
		}

		// 尾随逗号：若逗号后紧跟空白字符且遇到 '}' 或 ']'，则剔除该逗号
		if b == ',' {
			j := i + 1
			for j < n && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < n && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}

		out.WriteByte(b)
	}

	return out.Bytes()
}

// PatchAgentAsar handles local app.asar patching based on platform
func PatchAgentAsar(enable bool, homeDir, tempDir, caPath string, logCallback func(string)) error {
	var asarPath string

	if runtime.GOOS == "windows" {
		asarPath = filepath.Join(homeDir, "AppData", "Local", "Programs", "antigravity", "resources", "app.asar")
	} else if runtime.GOOS == "darwin" {
		asarPath = "/Applications/Antigravity.app/Contents/Resources/app.asar"
	}

	if asarPath == "" {
		return nil
	}

	if _, err := os.Stat(asarPath); os.IsNotExist(err) {
		logCallback("⚠️ Antigravity Agent app.asar not found. Skipping auto-patch.")
		return nil
	}

	if enable {
		logCallback("⚙️ Auto-patching Antigravity Agent app.asar...")
		err := PatchAsar(asarPath, caPath, logCallback)
		if err != nil {
			if os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "permission denied") {
				logCallback("❌ ASAR 补丁写入失败：权限不足 (Permission Denied)。")
				if runtime.GOOS == "darwin" {
					logCallback("💡 提示：在 macOS 上，请尝试在终端执行以下命令赋予该文件写入权限：")
					logCallback(fmt.Sprintf("   sudo chmod +w %s", asarPath))
				} else {
					logCallback("💡 提示：请尝试以管理员身份运行本程序。")
				}
			} else {
				logCallback(fmt.Sprintf("❌ ASAR Patching failed: %v", err))
			}
			return err
		}
		logCallback("✅ Antigravity Agent patched successfully.")
	} else {
		logCallback("[ASAR Patcher] Restoring original app.asar...")
		err := RestoreAsar(asarPath)
		if err != nil {
			if os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "permission denied") {
				logCallback("❌ ASAR 恢复失败：权限不足 (Permission Denied)。")
				if runtime.GOOS == "darwin" {
					logCallback("💡 提示：在 macOS 上，请尝试在终端执行以下命令赋予该文件写入权限：")
					logCallback(fmt.Sprintf("   sudo chmod +w %s", asarPath))
				} else {
					logCallback("💡 提示：请尝试以管理员身份运行本程序。")
				}
			} else {
				logCallback(fmt.Sprintf("❌ ASAR Restore failed: %v", err))
			}
			return err
		}
	}

	return nil
}

var patchMu sync.Mutex

// PatchAll applies all patches to integrate proxy into system environment
func PatchAll(enable bool, appData, homeDir, caPath string, logCallback func(string)) error {
	patchMu.Lock()
	defer patchMu.Unlock()

	// 优先根据 appData 结构解析宿主系统配置目录；
	// 若 appData 以 antigravity-proxy-desktop 结尾（如默认数据目录或沙箱测试环境），取其父目录；
	// 否则在 Windows 下回退到 homeDir/AppData/Roaming 或环境变量 APPDATA，避免自定义数据盘导致路径偏离。
	systemAppData := filepath.Dir(appData)
	cleanAppData := filepath.Clean(appData)
	if strings.HasSuffix(cleanAppData, "antigravity-proxy-desktop") {
		systemAppData = filepath.Dir(cleanAppData)
	} else if runtime.GOOS == "windows" {
		if homeDir != "" {
			systemAppData = filepath.Join(homeDir, "AppData", "Roaming")
		} else if envAppData := os.Getenv("APPDATA"); envAppData != "" {
			systemAppData = envAppData
		}
	} else if runtime.GOOS == "darwin" {
		if homeDir != "" {
			systemAppData = filepath.Join(homeDir, "Library", "Application Support")
		}
	} else {
		if homeDir != "" {
			systemAppData = filepath.Join(homeDir, ".config")
		}
	}

	_ = UpdateSettings(enable, systemAppData, homeDir)
	_ = UpdateAgentapiBat(enable, systemAppData, homeDir, caPath)
	HijackCli(enable, systemAppData, homeDir, caPath, logCallback)

	tempDir := filepath.Join(os.TempDir(), "antigravity-agent-asar-temp")
	_ = PatchAgentAsar(enable, homeDir, tempDir, caPath, logCallback)
	return nil
}
