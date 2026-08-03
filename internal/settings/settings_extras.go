package settings

import "path/filepath"

// settings_extras.go 收纳 debugger 相关特化访问器。
//
// 留此处的原因:
//  1. debugger Setter 原实现「Lock→改→Unlock→SaveConfig」(落盘前释放锁),与泛型
//     setSetting 的「持锁落盘」语义不同,保留手写避免改变锁竞争时序;
//  2. GetResolvedDebuggerLogPath 跨 config + activeDataDirectory/defaultUserDataPath 多源
//     解析绝对路径,逻辑含 filepath.IsAbs/Join,不适合纯字段泛型。
//
// OCR / SessionOptimization / NVIDIA 模型清单等含 trim/去重/聚合多字段的特化访问器,
// 见 settings_nvidia.go(需 strings)。

// ============ Debugger(原 Lock→改→Unlock→SaveConfig 语义,手写保留) ============

func (m *Manager) GetEnableDebuggerMode() bool {
	return getSetting(m, func(c *Config) bool { return c.EnableDebuggerMode })
}

func (m *Manager) SetEnableDebuggerMode(enable bool) error {
	m.Lock()
	m.config.EnableDebuggerMode = enable
	m.Unlock()
	return m.SaveConfig()
}

func (m *Manager) GetDebuggerLogPath() string {
	return getSetting(m, func(c *Config) string { return c.DebuggerLogPath })
}

func (m *Manager) SetDebuggerLogPath(val string) error {
	m.Lock()
	m.config.DebuggerLogPath = val
	m.Unlock()
	return m.SaveConfig()
}

// GetResolvedDebuggerLogPath 解析 debugger 日志绝对路径:空白走默认,相对路径拼数据目录。
func (m *Manager) GetResolvedDebuggerLogPath() string {
	m.RLock()
	defer m.RUnlock()
	p := m.config.DebuggerLogPath
	if p == "" {
		p = "logs/debugger"
	}
	if filepath.IsAbs(p) {
		return p
	}
	baseDir := m.activeDataDirectory
	if baseDir == "" {
		baseDir = m.defaultUserDataPath
	}
	if baseDir == "" {
		baseDir = "."
	}
	return filepath.Join(baseDir, p)
}
