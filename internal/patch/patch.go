package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// UpdateSettings updates jetski.cloudCodeUrl in IDE and Agent settings files
func UpdateSettings(enable bool, appData, homeDir string) error {
	idePath := filepath.Join(appData, "Antigravity IDE", "User", "settings.json")

	agentPaths := []string{
		filepath.Join(appData, "Antigravity", "User", "settings.json"),
		filepath.Join(appData, "Antigravity-Agent", "User", "settings.json"),
		filepath.Join(homeDir, ".antigravity", "settings.json"),
		filepath.Join(homeDir, ".antigravity-agent", "settings.json"),
		filepath.Join(homeDir, ".gemini", "antigravity-cli", "settings.json"),
		filepath.Join(homeDir, ".gemini", "settings.json"),
	}

	updateFile := func(path string) bool {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		var settings map[string]interface{}
		if err := json.Unmarshal(data, &settings); err != nil {
			return false
		}

		if enable {
			settings["jetski.cloudCodeUrl"] = "http://127.0.0.1:18443"
		} else {
			delete(settings, "jetski.cloudCodeUrl")
		}

		bytesData, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return false
		}

		_ = os.WriteFile(path, bytesData, 0644)
		return true
	}

	_ = updateFile(idePath)
	for _, p := range agentPaths {
		if updateFile(p) {
			break // Update the first one that exists
		}
	}

	return nil
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

	// appData is passed as our app's specific defaultUserData (.../Roaming/antigravity-proxy-desktop)
	// We get its parent directory to obtain the system global AppData/Application Support directory.
	systemAppData := filepath.Dir(appData)
	_ = UpdateSettings(enable, systemAppData, homeDir)
	_ = UpdateAgentapiBat(enable, systemAppData, homeDir, caPath)
	HijackCli(enable, systemAppData, homeDir, caPath, logCallback)

	tempDir := filepath.Join(os.TempDir(), "antigravity-agent-asar-temp")
	_ = PatchAgentAsar(enable, homeDir, tempDir, caPath, logCallback)
	return nil
}
