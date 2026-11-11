package settings

import (
	"sort"
)

// DefaultSwitchCooldownSecs 切号默认冷静期（秒）
const DefaultSwitchCooldownSecs = 60

// MaxSwitchCooldownSecs 最大冷静期（7天）
const MaxSwitchCooldownSecs = 7 * 24 * 3600

// DefaultPoolMaxRetries 默认最大重试次数
const DefaultPoolMaxRetries = 3

// PoolErrorRetryConfig 定义号池在请求遇到上游报错时的自由配置策略
type PoolErrorRetryConfig struct {
	// Enabled 是否启用自定义重试与切号规则 (默认 false，关闭时走原生默认逻辑)
	Enabled bool `json:"enabled"`
	// SwitchCodes 遇到这些报错状态码时，直接切换到下一个账号请求 (如 429, 403, 503)
	SwitchCodes []int `json:"switchCodes"`
	// RetryCodes 遇到这些报错状态码时，直接在当前账号上重试 (如 500, 502, 504)
	RetryCodes []int `json:"retryCodes"`
	// SwitchCooldownSecs 切换下一个号时，将当前账号标记临时冷静期的秒数 (默认 60s；0 表示不写长期冷静期，仅本次请求内跳过)
	SwitchCooldownSecs int `json:"switchCooldownSecs"`
	// MaxRetries 遇到错误时的最大重试次数 (默认 3 次，范围 1~10)
	MaxRetries int `json:"maxRetries"`
}

// NormalizePoolErrorRetry 规整配置参数：合法状态码过滤（100-599）、排序去重、时间与次数钳位
func NormalizePoolErrorRetry(cfg PoolErrorRetryConfig) PoolErrorRetryConfig {
	out := PoolErrorRetryConfig{
		Enabled:            cfg.Enabled,
		SwitchCooldownSecs: cfg.SwitchCooldownSecs,
		MaxRetries:         cfg.MaxRetries,
	}

	if out.SwitchCooldownSecs < 0 {
		out.SwitchCooldownSecs = 0
	} else if out.SwitchCooldownSecs > MaxSwitchCooldownSecs {
		out.SwitchCooldownSecs = MaxSwitchCooldownSecs
	}

	if out.MaxRetries <= 0 {
		out.MaxRetries = DefaultPoolMaxRetries
	} else if out.MaxRetries > 10 {
		out.MaxRetries = 10
	}

	switchMap := make(map[int]bool, len(cfg.SwitchCodes))
	for _, c := range cfg.SwitchCodes {
		if c >= 100 && c <= 599 && !switchMap[c] {
			switchMap[c] = true
			out.SwitchCodes = append(out.SwitchCodes, c)
		}
	}
	sort.Ints(out.SwitchCodes)

	retryMap := make(map[int]bool, len(cfg.RetryCodes))
	for _, c := range cfg.RetryCodes {
		// 若同时配置在 switchCodes 中，以 switchCodes 优先（切号覆盖同号重试）
		if c >= 100 && c <= 599 && !switchMap[c] && !retryMap[c] {
			retryMap[c] = true
			out.RetryCodes = append(out.RetryCodes, c)
		}
	}
	sort.Ints(out.RetryCodes)

	return out
}

// MatchAction 判断给定状态码应该采取什么动作:
// 返回 "switch" (切换下一个账号), "retry" (当前账号重试), 或 "" (不处理，走默认逻辑)
func (c *PoolErrorRetryConfig) MatchAction(statusCode int) string {
	if c == nil || !c.Enabled {
		return ""
	}
	for _, code := range c.SwitchCodes {
		if code == statusCode {
			return "switch"
		}
	}
	for _, code := range c.RetryCodes {
		if code == statusCode {
			return "retry"
		}
	}
	return ""
}

// GetPoolErrorRetry 获取当前的号池错误重试配置
func (m *Manager) GetPoolErrorRetry() PoolErrorRetryConfig {
	return getSetting(m, func(c *Config) PoolErrorRetryConfig {
		return c.PoolErrorRetry
	})
}

// SetPoolErrorRetry 保存号池错误重试配置
func (m *Manager) SetPoolErrorRetry(cfg PoolErrorRetryConfig) error {
	normalized := NormalizePoolErrorRetry(cfg)
	return setSetting(m, func(c *Config, v PoolErrorRetryConfig) {
		c.PoolErrorRetry = v
	}, normalized)
}
