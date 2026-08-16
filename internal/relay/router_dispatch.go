package relay

import (
	"encoding/json"
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

// resolveVariantEffort: 解析入站 model 名是否携带 "-{effort}" 形式的变体后缀。
//
// 设计:
//   - 遍历所有 ModelMappingEntry, 对每条 mapping 的 VariantEfforts:
//     若 inModel 末段 "-{effort}" 的 effort 命中, 且剥后的 baseModel == 该 mapping ClientModel,
//     视为该 mapping 配置出的变体虚项请求, 返回 baseModel 与 effort。
//   - 强约束 effort 与 baseModel 必须来自同一条 mapping(避免跨条目 effort/base 误命中)。
//   - 未命中/不含映射 → 返回 ("", "", false), 调用方按原 inModel 走 resolveRoutedTarget。
//
// 例: VariantEfforts={"high","max"} 且 ClientModel="glm-5.2" 的某条 mapping →
//
//	inModel="glm-5.2-max" → 命中该条 mapping 的 "max", 剥离后返回 ("glm-5.2","max",true)。
//	inModel="glm-5.2-high" → 命中 "high", 返回 ("glm-5.2","high",true)。
//	inModel="plain-model-high" 且存在 plain-model 但其无 VariantEfforts → 不剥(跨条目不拼)。
//	inModel="unknown-model-max" → 无 ClientModel="unknown-model" 条目 → 不剥。
func (h *APICompatHandler) resolveVariantEffort(inModel string) (baseModel, effort string, matched bool) {
	inModel = strings.TrimSpace(inModel)
	// 只看最后一段 "-" 之后的内容作为 candidate effort。
	dashIdx := strings.LastIndex(inModel, "-")
	if dashIdx <= 0 {
		return "", "", false
	}
	candidateEffort := inModel[dashIdx+1:]
	candidateBase := inModel[:dashIdx]
	if candidateEffort == "" || candidateBase == "" {
		return "", "", false
	}
	if h.settingsMgr == nil {
		return "", "", false
	}
	mappings := h.settingsMgr.GetRelayModelMapping()
	// 大小写不敏感比较: candidateEffort 与 candidateBase 同源于同一条 mapping。
	for _, m := range mappings {
		cm := strings.TrimSpace(m.ClientModel)
		if strings.EqualFold(cm, candidateBase) {
			for _, ef := range m.VariantEfforts {
				ef = strings.TrimSpace(ef)
				if strings.EqualFold(ef, candidateEffort) {
					return candidateBase, candidateEffort, true
				}
			}
		}
	}
	return "", "", false
}

// maybeInjectEffortFallback: 兜底注入剥离出的 effort 到请求体, 仅在客户端未显式携带思考信号时生效。
// 入站协议（anthropic / openai chat / openai responses）三形态各自判定:
//   - anthropic messages: 原有 output_config.effort 或 thinking.type∈{enabled,adaptive} 为"显式开思考";
//   - openai chat: 顶层 reasoning_effort 非空 为"显式开思考";
//   - openai responses: 顶层 reasoning.effort 非空 为"显式开思考".
//
// 兜底语义: 客户端未显式带时, 把剥离出的 effort 写入与协议匹配的字段,
// 让后续 thinkingRequested / 收思考参数的链路把它当显式信号处理, 无需 router_entry 各 handler 改造。
// 客户端已显式带则尊重不动, 不修改 body。
//
// 返回最终要在转发链路消费的请求体（若未变更, 返回与入参等价的切片）。
func maybeInjectEffortFallback(bodyBytes []byte, strippedEffort string) []byte {
	if strippedEffort == "" {
		return bodyBytes
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil || parsed == nil {
		return bodyBytes
	}
	changed := false

	// 1) Anthropic: 兜底写 thinking.type=adaptive + 兜底写 output_config.effort。
	// 现有 thinkingRequested 判定看 thinking.type∈{enabled,adaptive} 为 ON, adaptive 是常见
	// "客户端按模型名选档"场景下 OpenCode 默认发送的语义类型, 故兜底用 adaptive。
	if _, hasThinking := parsed["thinking"]; !hasThinking {
		parsed["thinking"] = map[string]interface{}{
			"type": "adaptive",
		}
		changed = true
	}
	if oc, ok := parsed["output_config"].(map[string]interface{}); ok && oc != nil {
		if _, hasEffort := oc["effort"]; !hasEffort {
			oc["effort"] = strippedEffort
			changed = true
		}
	} else if _, hasOC := parsed["output_config"]; !hasOC {
		parsed["output_config"] = map[string]interface{}{"effort": strippedEffort}
		changed = true
	}

	// 2) OpenAI Chat 顶层 reasoning_effort: 未显式带则兜底写入。
	if _, hasRE := parsed["reasoning_effort"]; !hasRE {
		parsed["reasoning_effort"] = strippedEffort
		changed = true
	}

	// 3) OpenAI Responses: reasoning.effort 未显式带则兜底写入。
	if reasoning, ok := parsed["reasoning"].(map[string]interface{}); ok && reasoning != nil {
		if _, hasEffort := reasoning["effort"]; !hasEffort {
			reasoning["effort"] = strippedEffort
			changed = true
		}
	} else if _, hasReasoning := parsed["reasoning"]; !hasReasoning {
		parsed["reasoning"] = map[string]interface{}{"effort": strippedEffort}
		changed = true
	}

	if !changed {
		return bodyBytes
	}
	newBody, err := json.Marshal(parsed)
	if err != nil {
		return bodyBytes
	}
	return newBody
}
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
