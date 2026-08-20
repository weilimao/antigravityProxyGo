package account

import (
	"fmt"
	"strings"
)

// account_antigravity.go: Antigravity 官方号池的配置管理、版本号与 User-Agent 构造。

const (
	// DefaultAntigravityCliVersion 是 Antigravity 号池全局 Hub/客户端版本号的默认值,
	// 用于发往 Google 上游的 User-Agent 身份头 (如 antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64))。
	// 用户可在 Antigravity 官方账号控制区配置覆盖。
	DefaultAntigravityCliVersion = "2.3.1"
)

// FormatAntigravityUserAgent 格式化 Antigravity Hub User-Agent 字符串。
func FormatAntigravityUserAgent(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = DefaultAntigravityCliVersion
	}
	return fmt.Sprintf("antigravity/hub/%s (aidev_client; os_type=windows; arch=amd64)", v)
}

// GetAntigravityCliVersion 返回 Antigravity 号池全局 Hub 客户端版本号(单池单值,对仗 AntigravityMaxConcurrency)。
// 空串=未配置, 回退默认 DefaultAntigravityCliVersion("2.3.1")。
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
// 入参做 TrimSpace 空白规整; 传入空串等同「未配置」, GetAntigravityCliVersion 会回退默认 2.3.1。
func (m *Manager) SetAntigravityCliVersion(v string) {
	if m == nil {
		return
	}
	m.Lock()
	m.antigravityCliVersion = strings.TrimSpace(v)
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// GetAntigravityUserAgent 获取组装后的完整 User-Agent 请求头。
func (m *Manager) GetAntigravityUserAgent() string {
	if m == nil {
		return FormatAntigravityUserAgent(DefaultAntigravityCliVersion)
	}
	return FormatAntigravityUserAgent(m.GetAntigravityCliVersion())
}
