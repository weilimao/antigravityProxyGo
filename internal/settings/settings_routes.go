package settings

import "strings"

// settings_routes.go:「按模型路由到号池」规则表 (RelayModelRoutes) 的访问器与默认值。
// 与 RelayModelMapping 并列,但职责不同:Mapping 决定模型名翻译与是否暴露给 /v1/models,
// Routes 决定入站 model 路由到哪个 Provider 号池(/route/* 入口)。

// GetDefaultModelRoutes 返回开箱即用的默认路由规则。
// 设计原则:只对「该仓库已落地的号池」给默认规则,避免空指针/无路由命中时退化为不可用。
// nvidia 号池已有完整选号/转发链路,故给一条 nvidia 兜底规则;其余 Provider 留待前端按需添加。
// 命中优先级:Pattern 越具体 Priority 越高;兜底通配 "*" 最低,放最后。
func GetDefaultModelRoutes() []ModelRouteRule {
	return []ModelRouteRule{
		// NVIDIA 号池兜底:所有 nvidia/* 命名空间上游模型走 nvidia 号池。
		{Pattern: "nvidia/*", TargetProvider: "nvidia", Priority: 50, Enabled: true},
		// 顶层兜底:未命中其它规则的模型统一丢给 nvidia 号池(向后兼容,与原 /nvidia 行为对齐)。
		{Pattern: "*", TargetProvider: "nvidia", Priority: 0, Enabled: true},
	}
}

// GetRelayModelRoutes 返回路由规则表;空时回退默认规则(非 nil 切片)。
// 返回副本,避免外部误改内存态。
func (m *Manager) GetRelayModelRoutes() []ModelRouteRule {
	return getSetting(m, func(c *Config) []ModelRouteRule {
		if len(c.RelayModelRoutes) == 0 {
			return GetDefaultModelRoutes()
		}
		out := make([]ModelRouteRule, len(c.RelayModelRoutes))
		copy(out, c.RelayModelRoutes)
		return out
	})
}

// SetRelayModelRoutes 落盘路由规则表。去空 Pattern 与空 Provider 的残项后整体替换。
func (m *Manager) SetRelayModelRoutes(val []ModelRouteRule) error {
	return setSetting(m, func(c *Config, v []ModelRouteRule) {
		cleaned := make([]ModelRouteRule, 0, len(v))
		for _, r := range v {
			r.Pattern = strings.TrimSpace(r.Pattern)
			r.TargetProvider = strings.TrimSpace(r.TargetProvider)
			if r.Pattern == "" || r.TargetProvider == "" {
				continue
			}
			if r.TargetModel != "" {
				r.TargetModel = strings.TrimSpace(r.TargetModel)
			}
			cleaned = append(cleaned, r)
		}
		c.RelayModelRoutes = cleaned
	}, val)
}
