package settings

import "strings"

// settings_nvidia.go 收纳 NVIDIA 专属模型清单与接口断言。
// 拆分自原 settings.go 尾部(NvidiaPreferredModels 与 ManagerInterface 断言)。

// GetNvidiaPreferredModels 返回全局级 NVIDIA 专属模型清单;nil 时返回空切片。
func (m *Manager) GetNvidiaPreferredModels() []string {
	return getSetting(m, func(c *Config) []string {
		if c.NvidiaPreferredModels == nil {
			return []string{}
		}
		// 返回副本,避免外部误改内存态
		out := make([]string, len(c.NvidiaPreferredModels))
		copy(out, c.NvidiaPreferredModels)
		return out
	})
}

// SetNvidiaPreferredModels 去空去重后存入并持久化。
func (m *Manager) SetNvidiaPreferredModels(val []string) error {
	return setSetting(m, func(c *Config, v []string) {
		seen := make(map[string]bool)
		cleaned := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			cleaned = append(cleaned, item)
		}
		c.NvidiaPreferredModels = cleaned
	}, val)
}

// ============ OCR 模型(含 trim 兜底,泛型 + trim 回调) ============

// GetOcrModel 读取入站 image 自愈降级使用的本地 Gemini OCR 模型名。
// 空值(旧配置或未设置)走 DefaultOcrModel,保持与历史行为一致。
func (m *Manager) GetOcrModel() string {
	return getSetting(m, func(c *Config) string {
		if strings.TrimSpace(c.OcrModel) == "" {
			return DefaultOcrModel
		}
		return c.OcrModel
	})
}

// SetOcrModel 持久化 OCR 模型名。前端下拉切换后经 IPC 调用此方法落盘。
// 空字符串写入会被 GetOcrModel 兜底为默认值,不阻断主请求;trim 在写锁内完成。
func (m *Manager) SetOcrModel(val string) error {
	return setSetting(m, func(c *Config, v string) { c.OcrModel = strings.TrimSpace(v) }, val)
}

// ============ 会话压缩 / NVIDIA 号池就地压缩结构体读写 ============

// GetSessionOptimization 把散落在 Config 里的会话压缩/号池压缩字段聚合成结构体返回。
// 此接口聚合多字段,无法走单字段泛型;沿用原手写 RLock 快照读。
func (m *Manager) GetSessionOptimization() SessionOptimizationConfig {
	m.RLock()
	defer m.RUnlock()
	return SessionOptimizationConfig{
		EnableCustomCompression:        m.config.EnableCustomCompression,
		MaxTokensThreshold:             m.config.MaxTokensThreshold,
		CompressionStrategy:            m.config.CompressionStrategy,
		SummaryModel:                   m.config.SummaryModel,
		KeepRecentTurns:                m.config.KeepRecentTurns,
		NvidiaCompressEnabled:          m.config.NvidiaCompressEnabled,
		NvidiaCompressThresholdTokens:  m.config.NvidiaCompressThresholdTokens,
		NvidiaCompressKeepToolResults:  m.config.NvidiaCompressKeepToolResults,
	}
}

// SetSessionOptimization 反向回写聚合结构体到散落字段,持写锁落盘。
func (m *Manager) SetSessionOptimization(cfg SessionOptimizationConfig) error {
	return setSetting(m, func(c *Config, v SessionOptimizationConfig) {
		c.EnableCustomCompression = v.EnableCustomCompression
		c.MaxTokensThreshold = v.MaxTokensThreshold
		c.CompressionStrategy = v.CompressionStrategy
		c.SummaryModel = v.SummaryModel
		c.KeepRecentTurns = v.KeepRecentTurns
		c.NvidiaCompressEnabled = v.NvidiaCompressEnabled
		c.NvidiaCompressThresholdTokens = v.NvidiaCompressThresholdTokens
		c.NvidiaCompressKeepToolResults = v.NvidiaCompressKeepToolResults
	}, cfg)
}

// 接口断言:保证 *Manager 实现 ManagerInterface(定义于 settings.go)。
// 放在文件末尾保持与原 settings.go 一致。
var _ ManagerInterface = (*Manager)(nil)
