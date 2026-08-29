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

// ============ NVIDIA Cloudflare Worker 出口代理配置 ============

// GetNvidiaWorkerProxyURL 返回 NVIDIA 号池专用的 Cloudflare Worker 出口代理 URL (如 https://my-nvidia.workers.dev)
func (m *Manager) GetNvidiaWorkerProxyURL() string {
	return getSetting(m, func(c *Config) string {
		return strings.TrimSpace(c.NvidiaWorkerProxyURL)
	})
}

// SetNvidiaWorkerProxyURL 持久化 NVIDIA Cloudflare Worker 出口代理 URL
func (m *Manager) SetNvidiaWorkerProxyURL(val string) error {
	return setSetting(m, func(c *Config, v string) { c.NvidiaWorkerProxyURL = strings.TrimSpace(v) }, val)
}

// IsNvidiaWorkerProxyEnabled 返回是否启用 NVIDIA Worker 出口代理
func (m *Manager) IsNvidiaWorkerProxyEnabled() bool {
	return getSetting(m, func(c *Config) bool {
		return c.NvidiaWorkerProxyEnabled && strings.TrimSpace(c.NvidiaWorkerProxyURL) != ""
	})
}

// SetNvidiaWorkerProxyEnabled 持久化是否启用 NVIDIA Worker 出口代理
func (m *Manager) SetNvidiaWorkerProxyEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.NvidiaWorkerProxyEnabled = v }, val)
}

// ============ NVIDIA 专属 SOCKS5/HTTP 出口代理配置 ============

// GetNvidiaDedicatedProxyAddress 返回 NVIDIA 专属出站代理地址
func (m *Manager) GetNvidiaDedicatedProxyAddress() string {
	return getSetting(m, func(c *Config) string {
		return strings.TrimSpace(c.NvidiaDedicatedProxyAddress)
	})
}

// SetNvidiaDedicatedProxyAddress 持久化 NVIDIA 专属出站代理地址
func (m *Manager) SetNvidiaDedicatedProxyAddress(val string) error {
	return setSetting(m, func(c *Config, v string) { c.NvidiaDedicatedProxyAddress = strings.TrimSpace(v) }, val)
}

// GetNvidiaDedicatedProxyEnabled 返回是否启用 NVIDIA 专属出站代理
func (m *Manager) GetNvidiaDedicatedProxyEnabled() bool {
	return getSetting(m, func(c *Config) bool {
		return c.NvidiaDedicatedProxyEnabled && strings.TrimSpace(c.NvidiaDedicatedProxyAddress) != ""
	})
}

// SetNvidiaDedicatedProxyEnabled 持久化是否启用 NVIDIA 专属出站代理
func (m *Manager) SetNvidiaDedicatedProxyEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.NvidiaDedicatedProxyEnabled = v }, val)
}

// GetNvidiaDedicatedProxyUsername 返回 NVIDIA 专属出站代理用户名
func (m *Manager) GetNvidiaDedicatedProxyUsername() string {
	return getSetting(m, func(c *Config) string {
		return strings.TrimSpace(c.NvidiaDedicatedProxyUsername)
	})
}

// SetNvidiaDedicatedProxyUsername 持久化 NVIDIA 专属出站代理用户名
func (m *Manager) SetNvidiaDedicatedProxyUsername(val string) error {
	return setSetting(m, func(c *Config, v string) { c.NvidiaDedicatedProxyUsername = strings.TrimSpace(v) }, val)
}

// GetNvidiaDedicatedProxyPassword 返回 NVIDIA 专属出站代理密码
func (m *Manager) GetNvidiaDedicatedProxyPassword() string {
	return getSetting(m, func(c *Config) string {
		return c.NvidiaDedicatedProxyPassword
	})
}

// SetNvidiaDedicatedProxyPassword 持久化 NVIDIA 专属出站代理密码
func (m *Manager) SetNvidiaDedicatedProxyPassword(val string) error {
	return setSetting(m, func(c *Config, v string) { c.NvidiaDedicatedProxyPassword = v }, val)
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
		EnableCustomCompression:       m.config.EnableCustomCompression,
		MaxTokensThreshold:            m.config.MaxTokensThreshold,
		CompressionStrategy:           m.config.CompressionStrategy,
		SummaryModel:                  m.config.SummaryModel,
		KeepRecentTurns:               m.config.KeepRecentTurns,
		NvidiaCompressEnabled:         m.config.NvidiaCompressEnabled,
		NvidiaCompressThresholdTokens: m.config.NvidiaCompressThresholdTokens,
		NvidiaCompressKeepToolResults: m.config.NvidiaCompressKeepToolResults,
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

// GetMaxInputTokensByModel 返回「上游模型 id → 声明的上下文窗口(window in tokens)」的查询函数。
// 供 NVIDIA 链路 /v1/models 的 Anthropic 形态回写时按上游 id 附加 max_input_tokens 字段。
//
// 解析规则(指针 + 持久层 "已有值才改" 语义,与 Multimodal 同款):
//   - 映射条目显式配置 MaxInputTokens(>0):取该值;
//   - 其余条目:回退 fallback(NVIDIA 模型列表的上下文窗口默认)逐条传入,
//     因此 fallback != 0 时所有可见条目都覆盖声明,受 allowlist 收窄;
//   - fallback == 0(从未配置默认):仅显式配置的条目声明,其余省略字段。
//
// allowlist 为的专属清单(NvidiaPreferredModels):非空时仅清单内 id 声明窗口,
// 清单外 id 返回 0(不附加字段)。nil/空 allowlist 表示不过滤。
//
// 返回 nil 时调用方不得使用(等价零值,不附加字段):settingsMgr 为 nil(测试构造或未注入)时早退。
func (m *Manager) GetMaxInputTokensByModel(allowlist []string, fallback int64) func(string) int64 {
	if m == nil {
		return nil
	}
	explicit := map[string]int64{}
	m.RLock()
	for _, entry := range m.config.RelayModelMapping {
		if entry.MaxInputTokens == nil || *entry.MaxInputTokens <= 0 {
			continue
		}
		// key 用 TargetModel: /v1/models 上游 id 属目标模型命名空间(如 nvidia/deepseek-ai/gemma-3-27b-it)。
		explicit[strings.TrimSpace(entry.TargetModel)] = *entry.MaxInputTokens
	}
	fb := fallback
	m.RUnlock()
	return func(modelID string) int64 {
		id := strings.TrimSpace(modelID)
		if v, ok := explicit[id]; ok {
			return v
		}
		// allowlist 收窄:清单非空时仅有清单内 id 才声明(兜底也需落在清单内)。
		if allowlist != nil && len(allowlist) > 0 && !containsStr(allowlist, id) {
			return 0
		}
		return fb
	}
}

// containsStr 判定字符串切片是否包含目标(精确匹配,与清单过滤同口径)。
func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ============ NVIDIA 对冲请求 (hedged request) ============

// 对冲触发延迟的默认/上下界(毫秒)。上风语义:NIM 首帧尾部延迟常态 10-40s,
// 默认 10s 使对冲只打尾部;2s 下限防误配置成 0/负导致每请求无差别双发(上游计费直接翻倍)。
const (
	defaultNvidiaHedgeDelayMs = 10000
	minNvidiaHedgeDelayMs     = 2000
	maxNvidiaHedgeDelayMs     = 60000
)

// normalizeNvidiaHedgeDelayMs 读写两侧共用归一化:0/负(未配置/旧配置)→ 默认,越界钳位。
// 保证 GetXxx 返回值恒可直接用于 time.Duration 换算,SetXxx 落盘值与生效值一致。
func normalizeNvidiaHedgeDelayMs(v int) int {
	if v <= 0 {
		return defaultNvidiaHedgeDelayMs
	}
	if v < minNvidiaHedgeDelayMs {
		return minNvidiaHedgeDelayMs
	}
	if v > maxNvidiaHedgeDelayMs {
		return maxNvidiaHedgeDelayMs
	}
	return v
}

// IsNvidiaHedgeEnabled 返回是否启用 NVIDIA 对冲请求(默认 false,显式 opt-in)。
func (m *Manager) IsNvidiaHedgeEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.NvidiaHedgeEnabled })
}

// SetNvidiaHedgeEnabled 持久化 NVIDIA 对冲请求开关。
func (m *Manager) SetNvidiaHedgeEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.NvidiaHedgeEnabled = v }, val)
}

// GetNvidiaHedgeDelayMs 返回对冲触发延迟(毫秒),经归一化兜底。
func (m *Manager) GetNvidiaHedgeDelayMs() int {
	return getSetting(m, func(c *Config) int { return normalizeNvidiaHedgeDelayMs(c.NvidiaHedgeDelayMs) })
}

// SetNvidiaHedgeDelayMs 持久化对冲触发延迟(毫秒),写前归一化钳位。
func (m *Manager) SetNvidiaHedgeDelayMs(val int) error {
	return setSetting(m, func(c *Config, v int) { c.NvidiaHedgeDelayMs = normalizeNvidiaHedgeDelayMs(v) }, val)
}

// 对冲总并发(含主请求)的默认与钳位区间。默认值 2 保守起步;上限 512 只是防脏值的
// 硬保险丝,产品级上限在 IPC 写入处动态钳为「当前启用中的 NVIDIA 账号数」——
// 运行时另有候选耗尽自动降级兜底,任何超限配置都不会造成超发。
// 计费提醒:同时轰出形态下最坏上游计费 = 并发数 × 单请求预填成本。
const (
	defaultNvidiaHedgeMaxParallel = 2
	minNvidiaHedgeMaxParallel     = 2
	maxNvidiaHedgeMaxParallel     = 512
)

// normalizeNvidiaHedgeMaxParallel 读写共用归一化:<=0(未配置/旧配置)→ 默认 2,越界钳位。
func normalizeNvidiaHedgeMaxParallel(v int) int {
	if v <= 0 {
		return defaultNvidiaHedgeMaxParallel
	}
	if v < minNvidiaHedgeMaxParallel {
		return minNvidiaHedgeMaxParallel
	}
	if v > maxNvidiaHedgeMaxParallel {
		return maxNvidiaHedgeMaxParallel
	}
	return v
}

// GetNvidiaHedgeMaxParallel 返回对冲总参赛请求数(含主请求),经归一化兜底。
func (m *Manager) GetNvidiaHedgeMaxParallel() int {
	return getSetting(m, func(c *Config) int { return normalizeNvidiaHedgeMaxParallel(c.NvidiaHedgeMaxParallel) })
}

// SetNvidiaHedgeMaxParallel 持久化对冲总并发,写前归一化钳位。
func (m *Manager) SetNvidiaHedgeMaxParallel(val int) error {
	return setSetting(m, func(c *Config, v int) { c.NvidiaHedgeMaxParallel = normalizeNvidiaHedgeMaxParallel(v) }, val)
}

// IsNvidiaHedgeImmediate 返回即刻竞赛开关(默认 false = 延迟对冲模式)。
// 开启后主请求与全部对冲在 t=0 同刻发出,跳过触发延迟。
func (m *Manager) IsNvidiaHedgeImmediate() bool {
	return getSetting(m, func(c *Config) bool { return c.NvidiaHedgeImmediate })
}

// SetNvidiaHedgeImmediate 持久化即刻竞赛开关。
func (m *Manager) SetNvidiaHedgeImmediate(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.NvidiaHedgeImmediate = v }, val)
}

// 接口断言:保证 *Manager 实现 ManagerInterface(定义于 settings.go)。
// 放在文件末尾保持与原 settings.go 一致。
var _ ManagerInterface = (*Manager)(nil)
