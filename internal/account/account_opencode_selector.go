package account

import (
	"strings"
)

// account_opencode_selector.go: OpenCode 号池的负载均衡轮询模式与单账号并发限制。
// 独立模块封装,避免向行数接近 1000 行的 account_selector.go 膨胀代码,保持高内聚低耦合。

// GetOpenCodeLBMode 获取 OpenCode 号池当前的负载均衡模式。
// 未配置或为空时回退默认的 "round-robin" (游标轮询)。
func (m *Manager) GetOpenCodeLBMode() string {
	m.RLock()
	defer m.RUnlock()
	mode := strings.TrimSpace(m.opencodeLBMode)
	if mode == "" {
		return "round-robin"
	}
	return mode
}

// SetOpenCodeLBMode 设置 OpenCode 号池的负载均衡模式 (round-robin / sticky)。
// 遇非法输入自动归一为 "round-robin", 并定向落盘 pool 配置。
func (m *Manager) SetOpenCodeLBMode(mode string) {
	mode = strings.TrimSpace(mode)
	if mode != "round-robin" && mode != "sticky" {
		mode = "round-robin"
	}
	m.Lock()
	m.opencodeLBMode = mode
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// GetOpenCodeMaxConcurrency 获取 OpenCode 号池单账号在途并发上限。
// <=0 表示未配置, 回退 defaultMaxConcurrency (10)。
func (m *Manager) GetOpenCodeMaxConcurrency() int {
	m.RLock()
	v := m.opencodeMaxConcurrency
	m.RUnlock()
	if v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

// SetOpenCodeMaxConcurrency 设置 OpenCode 号池单账号在途并发上限。
// 负数置 0 (视为未配置), 并定向落盘 pool 配置。
func (m *Manager) SetOpenCodeMaxConcurrency(v int) {
	if v < 0 {
		v = 0
	}
	m.Lock()
	m.opencodeMaxConcurrency = v
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}
