package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// account_workbuddy_version.go: WorkBuddy 客户端模拟版本号管理与自适应探测。
//
// 核心职责:
// 1. 提供号池单值 GetWorkBuddyCliVersion / SetWorkBuddyCliVersion，对仗 GrokCliVersion。
// 2. 探测本机安装的最新 WorkBuddy 版本 (注册表探测 + 本地 manifest 扫描 + 官方基线兜底)。
// 3. 规避旧版 5.5.x 导致官方登录页面提示“版本过低”的问题。

// GetWorkBuddyCliVersion 返回 WorkBuddy 号池全局客户端模拟版本号。
// 空串=未配置, 回退默认 DefaultWorkBuddyDomesticVersion ("5.7.7")。
func (m *Manager) GetWorkBuddyCliVersion() string {
	if m == nil {
		return DefaultWorkBuddyDomesticVersion
	}
	m.RLock()
	defer m.RUnlock()
	v := strings.TrimSpace(m.workbuddyCliVersion)
	if v == "" {
		return DefaultWorkBuddyDomesticVersion
	}
	return v
}

// GetWorkBuddyCliVersionRaw 返回已配置的模拟版本号原文; 空串表示未配置。
// 与 GetWorkBuddyCliVersion 的区别是不回退默认值,便于调用方按账号区域选择默认版本
// (国际版 5.7.6 / 国内版 5.7.7),避免国际版被国内默认版本顶替。
func (m *Manager) GetWorkBuddyCliVersionRaw() string {
	if m == nil {
		return ""
	}
	m.RLock()
	defer m.RUnlock()
	return strings.TrimSpace(m.workbuddyCliVersion)
}

// SetWorkBuddyCliVersion 设置 WorkBuddy 号池全局客户端模拟版本号并定向落盘。
// 入参做 TrimSpace 空白规整; 传入空串等同「未配置」, GetWorkBuddyCliVersion 会回退默认 5.7.7。
func (m *Manager) SetWorkBuddyCliVersion(v string) {
	if m == nil {
		return
	}
	m.Lock()
	m.workbuddyCliVersion = strings.TrimSpace(v)
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// DetectWorkBuddyLatestVersion 综合探测当前环境可用的 WorkBuddy 最新版本。
// 探测优先级:
// 1. 本机 Windows 注册表已安装版本 (如 5.7.7)
// 2. 本机安装目录中的 install-manifest.json 中的 appVersion
// 3. 官方稳定基准默认值 (DefaultWorkBuddyDomesticVersion 5.7.7)
func DetectWorkBuddyLatestVersion() string {
	highest := DefaultWorkBuddyDomesticVersion

	// 1. 尝试从操作系统注册表探测
	if regVer := detectVersionFromRegistry(); regVer != "" {
		if compareSemver(regVer, highest) > 0 {
			highest = regVer
		}
	}

	// 2. 尝试从本地 manifest 文件探测
	if localVer := detectVersionFromLocalManifests(); localVer != "" {
		if compareSemver(localVer, highest) > 0 {
			highest = localVer
		}
	}

	return highest
}

// detectVersionFromLocalManifests 扫描本地已知目录中的 install-manifest.json 提取版本。
func detectVersionFromLocalManifests() string {
	highest := ""
	candidateDirs := []string{}

	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		candidateDirs = append(candidateDirs,
			filepath.Join(localAppData, "Programs", "WorkBuddy", "resources"),
			filepath.Join(localAppData, "Programs", "WorkBuddyCN", "resources"),
			filepath.Join(localAppData, "Programs", "WorkBuddy AI", "resources"),
		)
	}
	if progFiles := os.Getenv("ProgramFiles"); progFiles != "" {
		candidateDirs = append(candidateDirs,
			filepath.Join(progFiles, "WorkBuddy", "resources"),
			filepath.Join(progFiles, "WorkBuddyCN", "resources"),
			filepath.Join(progFiles, "WorkBuddy AI", "resources"),
		)
	}

	// 检查可能存在的其他盘符安装路径 (如 E:\WorkBuddyCN\resources, E:\WorkBuddyAI-Intl\resources)
	for _, drive := range []string{"C", "D", "E", "F"} {
		candidateDirs = append(candidateDirs,
			drive+`:\WorkBuddyCN\resources`,
			drive+`:\WorkBuddyAI-Intl\resources`,
			drive+`:\WorkBuddy\resources`,
		)
	}

	for _, dir := range candidateDirs {
		manifestPath := filepath.Join(dir, "install-manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var parsed struct {
			AppVersion string `json:"appVersion"`
		}
		if err := json.Unmarshal(data, &parsed); err == nil && parsed.AppVersion != "" {
			v := strings.TrimSpace(parsed.AppVersion)
			if compareSemver(v, highest) > 0 {
				highest = v
			}
		}
	}

	return highest
}

// compareSemver 比较两个语义版本字符串。
// 返回值: 1 (v1 > v2), -1 (v1 < v2), 0 (v1 == v2)。
func compareSemver(v1, v2 string) int {
	v1 = strings.TrimPrefix(strings.TrimSpace(v1), "v")
	v2 = strings.TrimPrefix(strings.TrimSpace(v2), "v")
	if v1 == v2 {
		return 0
	}
	if v1 == "" {
		return -1
	}
	if v2 == "" {
		return 1
	}

	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}
