package account

import (
	"strings"
)

// account_workbuddy_selector.go: WorkBuddy 号池的负载均衡轮询模式与单账号并发限制。
// 独立模块封装,避免向行数接近 1000 行的 account_selector.go 膨胀代码,保持高内聚低耦合。

// GetWorkBuddyLBMode 获取 WorkBuddy 号池当前的负载均衡模式。
// 未配置或为空时回退默认的 "round-robin" (游标轮询)。
func (m *Manager) GetWorkBuddyLBMode() string {
	m.RLock()
	defer m.RUnlock()
	mode := strings.TrimSpace(m.workbuddyLBMode)
	if mode == "" {
		return "round-robin"
	}
	return mode
}

// SetWorkBuddyLBMode 设置 WorkBuddy 号池的负载均衡模式 (round-robin / sticky)。
// 遇非法输入自动归一为 "round-robin", 并定向落盘 pool 配置。
func (m *Manager) SetWorkBuddyLBMode(mode string) {
	mode = strings.TrimSpace(mode)
	if mode != "round-robin" && mode != "sticky" {
		mode = "round-robin"
	}
	m.Lock()
	m.workbuddyLBMode = mode
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}

// GetWorkBuddyMaxConcurrency 获取 WorkBuddy 号池单账号在途并发上限。
// <=0 表示未配置, 回退 defaultMaxConcurrency (10)。
func (m *Manager) GetWorkBuddyMaxConcurrency() int {
	m.RLock()
	v := m.workbuddyMaxConcurrency
	m.RUnlock()
	if v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

// SetWorkBuddyMaxConcurrency 设置 WorkBuddy 号池单账号在途并发上限。
// 负数置 0 (视为未配置), 并定向落盘 pool 配置。
func (m *Manager) SetWorkBuddyMaxConcurrency(v int) {
	if v < 0 {
		v = 0
	}
	m.Lock()
	m.workbuddyMaxConcurrency = v
	m.Unlock()
	_ = m.SaveAccountsFor(false, poolPartKind)
}
