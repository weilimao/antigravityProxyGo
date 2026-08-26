package settings

import "strings"

// settings_antigravity.go 收纳 Antigravity 官方号池专用的 Cloudflare Worker 出口代理配置。
// 与 settings_grok.go / settings_nvidia.go 同范式(GetXxxURL/SetXxxURL/IsXxxEnabled/SetXxxEnabled)。
// Antigravity 池无专属 SOCKS5 代理概念,故无互斥保护;且其上游 URL 形如
// https://cloudcode-pa.googleapis.com/v1beta/...(完整 URL 含 path),改写逻辑需经
// net/url 精确重组 scheme/host,由 relay.compat.finalRequester 落地,本文件只管持久化与开关断言。

// ============ Antigravity Cloudflare Worker 出口代理配置 ============

// GetAntigravityWorkerProxyURL 返回 Antigravity 号池专用的 Cloudflare Worker 出口代理 URL
// (如 https://my-antigravity.workers.dev)。空串=未配置。
func (m *Manager) GetAntigravityWorkerProxyURL() string {
	return getSetting(m, func(c *Config) string {
		return strings.TrimSpace(c.AntigravityWorkerProxyURL)
	})
}

// SetAntigravityWorkerProxyURL 持久化 Antigravity Cloudflare Worker 出口代理 URL。
// 写入前 TrimSpace 规整;空串表示清空恢复直连 Google 上游。
func (m *Manager) SetAntigravityWorkerProxyURL(val string) error {
	return setSetting(m, func(c *Config, v string) { c.AntigravityWorkerProxyURL = strings.TrimSpace(v) }, val)
}

// IsAntigravityWorkerProxyEnabled 返回是否启用 Antigravity Worker 出口代理。
// 启用判定与 NVIDIA/Grok 同款:开关 true 且 URL 非空,防误配置。
func (m *Manager) IsAntigravityWorkerProxyEnabled() bool {
	return getSetting(m, func(c *Config) bool {
		return c.AntigravityWorkerProxyEnabled && strings.TrimSpace(c.AntigravityWorkerProxyURL) != ""
	})
}

// SetAntigravityWorkerProxyEnabled 持久化是否启用 Antigravity Worker 出口代理。
func (m *Manager) SetAntigravityWorkerProxyEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.AntigravityWorkerProxyEnabled = v }, val)
}
