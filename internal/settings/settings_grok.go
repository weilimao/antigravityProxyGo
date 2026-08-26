package settings

import "strings"

// settings_grok.go 收纳 Grok 号池专用的 Cloudflare Worker 出口代理配置。
// 拆分自 settings_nvidia.go 同款范式(GetXxxURL/SetXxxURL/IsXxxEnabled/SetXxxEnabled),
// 与 NVIDIA 池互为对仗;Grok 池无专属 SOCKS5/HTTP 代理概念,故无互斥保护(逻辑上更简单)。

// ============ Grok Cloudflare Worker 出口代理配置 ============

// GetGrokWorkerProxyURL 返回 Grok 号池专用的 Cloudflare Worker 出口代理 URL
// (如 https://my-grok.workers.dev)。空串=未配置。
func (m *Manager) GetGrokWorkerProxyURL() string {
	return getSetting(m, func(c *Config) string {
		return strings.TrimSpace(c.GrokWorkerProxyURL)
	})
}

// SetGrokWorkerProxyURL 持久化 Grok Cloudflare Worker 出口代理 URL。
// 写入前 TrimSpace 规整;空串表示清空恢复直连。
func (m *Manager) SetGrokWorkerProxyURL(val string) error {
	return setSetting(m, func(c *Config, v string) { c.GrokWorkerProxyURL = strings.TrimSpace(v) }, val)
}

// IsGrokWorkerProxyEnabled 返回是否启用 Grok Worker 出口代理。
// 启用判定:开关为 true 且 URL 非空 —— 两者同时满足时 relay 才会改写上流 baseURL,
// 防止「开关已开但 URL 忘填导致上游请求落空」的误配置。
func (m *Manager) IsGrokWorkerProxyEnabled() bool {
	return getSetting(m, func(c *Config) bool {
		return c.GrokWorkerProxyEnabled && strings.TrimSpace(c.GrokWorkerProxyURL) != ""
	})
}

// SetGrokWorkerProxyEnabled 持久化是否启用 Grok Worker 出口代理。
func (m *Manager) SetGrokWorkerProxyEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.GrokWorkerProxyEnabled = v }, val)
}
