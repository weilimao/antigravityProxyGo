package settings

import "strings"

// settings_benchmark.go: 模型测速(首帧/耗时)配置的聚合读写。
//
// BenchmarkConfig 聚合 Config 里散落的 5 个测速字段(Enabled/Models/IntervalMinutes/
// Prompt/TimeoutMs), 经泛型 getSetting/setSetting 落盘, 与 SessionOptimization 同构。
// 读写两侧共用同一归一化(interval 钳 [1,1440]→5, timeout 钳下限 5000→30000、无上限,
// prompt 空→"Hi", models 去空去重), 保证 Get 返回值恒可直接使用、Set 落盘值与生效值一致。
//
// 测速调度器(internal/benchmark)只读本配置; IPC(app_benchmark_ipc.go)负责写。
// 测速请求走中继回环并经 RelaySession.IsBenchmark 跳过 stats 落库, 不污染仪表盘统计。

// BenchmarkConfig 是模型测速配置的聚合形态(经归一化兜底)。
type BenchmarkConfig struct {
	Enabled         bool     `json:"enabled"`
	Models          []string `json:"models"`
	IntervalMinutes int      `json:"intervalMinutes"`
	Prompt          string   `json:"prompt"`
	TimeoutMs       int      `json:"timeoutMs"`
}

const (
	defaultBenchmarkIntervalMinutes = 5
	defaultBenchmarkTimeoutMs       = 30000
	defaultBenchmarkPrompt         = "Hi"
	minBenchmarkIntervalMinutes     = 1
	maxBenchmarkIntervalMinutes     = 1440
	minBenchmarkTimeoutMs           = 5000
)

func normalizeBenchmarkInterval(v int) int {
	if v <= 0 {
		return defaultBenchmarkIntervalMinutes
	}
	if v < minBenchmarkIntervalMinutes {
		return minBenchmarkIntervalMinutes
	}
	if v > maxBenchmarkIntervalMinutes {
		return maxBenchmarkIntervalMinutes
	}
	return v
}

func normalizeBenchmarkTimeout(v int) int {
	if v <= 0 {
		return defaultBenchmarkTimeoutMs
	}
	if v < minBenchmarkTimeoutMs {
		return minBenchmarkTimeoutMs
	}
	// 无上限: 允许自定义任意大的超时(如慢思考模型), 由调用方自行承担时长。
	return v
}

// dedupModels 去空去重(保留顺序), 供读写两侧共用, 保证配置清单洁净。
func dedupModels(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// GetBenchmarkConfig 读测速配置聚合(经归一化兜底); nil models 返回空切片。
func (m *Manager) GetBenchmarkConfig() BenchmarkConfig {
	return getSetting(m, func(c *Config) BenchmarkConfig {
		prompt := strings.TrimSpace(c.BenchmarkPrompt)
		if prompt == "" {
			prompt = defaultBenchmarkPrompt
		}
		return BenchmarkConfig{
			Enabled:         c.BenchmarkEnabled,
			Models:          dedupModels(c.BenchmarkModels),
			IntervalMinutes: normalizeBenchmarkInterval(c.BenchmarkIntervalMinutes),
			Prompt:           prompt,
			TimeoutMs:        normalizeBenchmarkTimeout(c.BenchmarkTimeoutMs),
		}
	})
}

// SetBenchmarkConfig 写测速配置聚合(写前归一化) + 持久化。
func (m *Manager) SetBenchmarkConfig(cfg BenchmarkConfig) error {
	return setSetting(m, func(c *Config, v BenchmarkConfig) {
		prompt := strings.TrimSpace(v.Prompt)
		if prompt == "" {
			prompt = defaultBenchmarkPrompt
		}
		c.BenchmarkEnabled = v.Enabled
		c.BenchmarkModels = dedupModels(v.Models)
		c.BenchmarkIntervalMinutes = normalizeBenchmarkInterval(v.IntervalMinutes)
		c.BenchmarkPrompt = prompt
		c.BenchmarkTimeoutMs = normalizeBenchmarkTimeout(v.TimeoutMs)
	}, cfg)
}
