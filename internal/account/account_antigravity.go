package account

import (
	"fmt"
	"strconv"
	"strings"
)

// account_antigravity.go: Antigravity 官方号池的配置管理、版本号与 User-Agent 构造。

const (
	// DefaultAntigravityCliVersion 是 Antigravity 号池全局 Hub/客户端版本号的默认值,
	// 用于发往 Google 上游的 User-Agent 身份头 (如 antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558))。
	// 用户可在 Antigravity 官方账号控制区配置覆盖，或由抓包请求动态自动学习。
	DefaultAntigravityCliVersion = "2.29.1"

	// DefaultAntigravityBuildCL 是官方 Antigravity Desktop (language_server.exe --stamp) 内置的构建 Changelist 编号。
	// 官方 codeassistclient.UserAgent 恒在括号末尾附加 "; cl=<BuildChangelist>"。
	DefaultAntigravityBuildCL = "1015941558"
)

// CompareVersion 比较两个形如 "x.y.z" 的语义化版本字符串。
// 若 v1 > v2 返回 1; 若 v1 < v2 返回 -1; 若相等返回 0。
func CompareVersion(v1, v2 string) int {
	parts1 := strings.Split(strings.TrimSpace(v1), ".")
	parts2 := strings.Split(strings.TrimSpace(v2), ".")
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

// FormatAntigravityUserAgentWithCL 格式化 Antigravity Hub User-Agent 字符串，严格对齐官方 language_server.exe (codeassistclient.UserAgent) 格式。
func FormatAntigravityUserAgentWithCL(version, buildCL string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = DefaultAntigravityCliVersion
	}
	if strings.HasPrefix(strings.ToLower(v), "antigravity/") {
		return v
	}
	cl := strings.TrimSpace(buildCL)
	if cl == "" {
		cl = DefaultAntigravityBuildCL
	}
	return fmt.Sprintf("antigravity/hub/%s (aidev_client; os_type=windows; arch=amd64; cl=%s)", v, cl)
}

// FormatAntigravityUserAgent 保留单参兼容接口，默认使用当前最新构建号或默认构建号。
func FormatAntigravityUserAgent(version string) string {
	return FormatAntigravityUserAgentWithCL(version, DefaultAntigravityBuildCL)
}

// GetAntigravityCliVersion 返回 Antigravity 号池全局 Hub 客户端版本号(单池单值,对仗 AntigravityMaxConcurrency)。
// 空串=未配置, 回退默认 DefaultAntigravityCliVersion("2.29.1")。
func (m *Manager) GetAntigravityCliVersion() string {
	if m == nil {
		return DefaultAntigravityCliVersion
	}
	m.RLock()
	defer m.RUnlock()
	if strings.TrimSpace(m.antigravityCliVersion) == "" {
		return DefaultAntigravityCliVersion
	}
	return m.antigravityCliVersion
}

// SetAntigravityCliVersion 设置 Antigravity 号池全局 Hub 客户端版本号并持久化(对仗 SetAntigravityMaxConcurrency)。
// 入参做 TrimSpace 空白规整; 传入空串等同「未配置」, GetAntigravityCliVersion 会回退默认。
func (m *Manager) SetAntigravityCliVersion(v string) {
	if m == nil {
		return
	}
	m.Lock()
	m.antigravityCliVersion = strings.TrimSpace(v)
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// GetAntigravityBuildCL 返回 Antigravity 官方 Language Server 的构建 Changelist 编号。
func (m *Manager) GetAntigravityBuildCL() string {
	if m == nil {
		return DefaultAntigravityBuildCL
	}
	m.RLock()
	defer m.RUnlock()
	if strings.TrimSpace(m.antigravityBuildCL) == "" {
		return DefaultAntigravityBuildCL
	}
	return m.antigravityBuildCL
}

// SetAntigravityBuildCL 设置 Antigravity 构建 Changelist 编号并持久化。
func (m *Manager) SetAntigravityBuildCL(cl string) {
	if m == nil {
		return
	}
	m.Lock()
	m.antigravityBuildCL = strings.TrimSpace(cl)
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// SetOnAntigravityVersionUpdated 注册版本与 CL 动态嗅探更新后的回调钩子。
func (m *Manager) SetOnAntigravityVersionUpdated(fn func(version, buildCL string)) {
	if m == nil {
		return
	}
	m.Lock()
	defer m.Unlock()
	m.onAntigravityVersionUpdated = fn
}

// UpdateAntigravityVersionAndCL 动态学习并更新官方客户端版本与构建号。
// 仅在嗅探到的版本更高（CompareVersion > 0），或版本相同且 CL 构建号更新时才执行更新；严禁版本降级。
// 若版本或 CL 发生实质性升级，则更新内存、持久化配置并触发回调通知，返回 true；若无变化或为旧版本则直接返回 false。
func (m *Manager) UpdateAntigravityVersionAndCL(version, cl string) bool {
	if m == nil {
		return false
	}
	newVer := strings.TrimSpace(version)
	newCL := strings.TrimSpace(cl)
	if newVer == "" {
		return false
	}

	m.Lock()
	curVer := m.antigravityCliVersion
	if curVer == "" {
		curVer = DefaultAntigravityCliVersion
	}
	curCL := m.antigravityBuildCL
	if curCL == "" {
		curCL = DefaultAntigravityBuildCL
	}

	cmp := CompareVersion(newVer, curVer)
	// 严禁版本降级：仅允许升级或相同版本下更新 CL
	if cmp < 0 {
		m.Unlock()
		return false
	}
	if cmp == 0 && (newCL == "" || curCL == newCL) {
		m.Unlock()
		return false
	}

	m.antigravityCliVersion = newVer
	if newCL != "" {
		m.antigravityBuildCL = newCL
	}
	notifyVer := m.antigravityCliVersion
	notifyCL := m.antigravityBuildCL
	hook := m.onAntigravityVersionUpdated
	m.Unlock()

	_ = m.SaveAccountsFor(false, poolPartKind)

	if hook != nil {
		hook(notifyVer, notifyCL)
	}
	return true
}

// GetAntigravityUserAgent 获取组装后的完整 User-Agent 请求头。
func (m *Manager) GetAntigravityUserAgent() string {
	if m == nil {
		return FormatAntigravityUserAgentWithCL(DefaultAntigravityCliVersion, DefaultAntigravityBuildCL)
	}
	return FormatAntigravityUserAgentWithCL(m.GetAntigravityCliVersion(), m.GetAntigravityBuildCL())
}
