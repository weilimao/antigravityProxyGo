package relay

import (
	"regexp"
	"strings"

	"antigravity-proxy/internal/settings"
)

// router_dispatch.go: 按入站模型名匹配路由规则,决定转发到哪个 Provider 号池。
// 与 settings.ModelRouteRule 解耦:settings 只存规则表,这里负责「匹配 + 选出目标 Provider」。

// routeMatch 对入站 model 返回首个命中的 *启用* 规则。
// 匹配优先级:Priority 降序;同优先级按规则表原序(稳定)。未命中返回 nil。
func routeMatch(rules []settings.ModelRouteRule, model string) *settings.ModelRouteRule {
	if len(rules) == 0 {
		return nil
	}
	// 按 Priority 降序拷贝一帧下标,避免改原表顺序;同优先级保原序(稳定排序)。
	type ix struct{ i, p int }
	order := make([]ix, 0, len(rules))
	for i, r := range rules {
		if !r.Enabled {
			continue
		}
		order = append(order, ix{i: i, p: r.Priority})
	}
	// 简单稳定插入排序:规则表规模极小(常见 <20),无需 sort.Slice 带来的依赖与分配。
	for k := 1; k < len(order); k++ {
		for j := k; j > 0 && order[j-1].p < order[j].p; j-- {
			order[j-1], order[j] = order[j], order[j-1]
		}
	}

	for _, o := range order {
		r := rules[o.i]
		if matchModelPattern(r.Pattern, model) {
			return &r
		}
	}
	return nil
}

// matchModelPattern 判定 model 是否命中单条规则的 Pattern。
// 支持三种写法:
//   - 正则: "regexp:..." 前缀 → 正则匹配(编译失败按字面相等处理);
//   - 通配: 含 '*' → 把 * 转成正则锚定匹配(如 "deepseek-*" / "nvidia/*");
//   - 其它: 大小写不敏感精确相等。
func matchModelPattern(pattern, model string) bool {
	p := strings.TrimSpace(pattern)
	m := strings.TrimSpace(model)
	if p == "" {
		return false
	}
	if p == "*" {
		return true
	}
	if strings.HasPrefix(p, "regexp:") {
		expr := strings.TrimPrefix(p, "regexp:")
		re, err := regexp.Compile(expr)
		if err != nil {
			return strings.EqualFold(p, m)
		}
		return re.MatchString(m)
	}
	if strings.Contains(p, "*") {
		// 把字面 * 转成 .* 并锚定;其余元字符先转义避免注入。
		expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, ".*") + "$"
		re, err := regexp.Compile(expr)
		if err != nil {
			return strings.EqualFold(p, m)
		}
		return re.MatchString(m)
	}
	return strings.EqualFold(p, m)
}

// resolveRoutedTarget 按当前 settings 规则表解析入站 model 的目标号池 Provider 与目标上游模型。
// 返回 (targetProvider, targetGroupID, targetModel, matched)。
//   - 命中规则:返回规则的 TargetProvider / TargetModel(TargetModel 为空则原样透传入站 model);
//     targetGroupID 仅在 ModelMappingEntry 显式配置时返回(Other 号池组内细分),否则空串。
//   - 未命中:返回 ("", "", "", false),由调用方决定是否兜底。
func (h *APICompatHandler) resolveRoutedTarget(model string) (targetProvider, targetGroupID, targetModel string, matched bool) {
	if h.settingsMgr != nil {
		mappings := h.settingsMgr.GetRelayModelMapping()
		for _, m := range mappings {
			if strings.EqualFold(strings.TrimSpace(m.ClientModel), strings.TrimSpace(model)) && strings.TrimSpace(m.TargetProvider) != "" {
				tm := strings.TrimSpace(m.TargetModel)
				if tm == "" {
					tm = model
				}
				return strings.TrimSpace(m.TargetProvider), strings.TrimSpace(m.TargetGroupID), tm, true
			}
		}
	}

	var rules []settings.ModelRouteRule
	if h.settingsMgr != nil {
		rules = h.settingsMgr.GetRelayModelRoutes()
	}
	if len(rules) == 0 {
		rules = settings.GetDefaultModelRoutes()
	}
	rule := routeMatch(rules, model)
	if rule == nil {
		return "", "", "", false
	}
	tm := strings.TrimSpace(rule.TargetModel)
	if tm == "" {
		tm = model
	}
	return rule.TargetProvider, "", tm, true
}
