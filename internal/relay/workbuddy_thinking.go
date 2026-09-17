package relay

import (
	"strings"
)

// workbuddy_thinking.go: 腾讯 WorkBuddy 号池专属思考(量化推理)注入器。
//
// 关键特性:
//   - 腾讯 WorkBuddy 上游 /v2/chat/completions 走官方顶层 reasoning_effort 字段,
//     取值集为 {low, medium, high, max}。其中 max 档位经真实抓包证实已被上游原生支持。
//   - 上游严禁注入 NIM 专属的 chat_template_kwargs, 强塞会被忽略或导致不可预期行为。
//   - WorkBuddy 上游未带 reasoning_effort 时默认不开启思考(reasoning_tokens: 0)。
//   - 三态语义(mode):
//     * off: 显式关闭思考 -> reasoning_effort="" (省略不发, 上游默认不推理);
//     * on: 客户端显式开启思考 -> reasoning_effort=<档值> (low/medium/high/max);
//     * unspecified: 客户端未表达任何 thinking 字段(opt-in 默认) -> reasoning_effort="" (省略);
//   - 全局总闸 IsEnableThinkingMode()==false 时强制 off。

type workbuddyThinkingMode int

const (
	// workbuddyThinkUnspecified 表示客户端未表达任何 thinking 信号(opt-in 默认)。
	workbuddyThinkUnspecified workbuddyThinkingMode = iota
	// workbuddyThinkOff 表示客户端(或全局总闸)显式关闭思考。
	workbuddyThinkOff
	// workbuddyThinkOn 表示客户端显式开启思考。
	workbuddyThinkOn
)

// workbuddyResolveAnthropicThinking 从 Anthropic Messages 入站请求识别客户端思考意图, 返回 (mode, effort)。
func workbuddyResolveAnthropicThinking(req *AnthropicRequest, globalOn bool) (mode workbuddyThinkingMode, effort string) {
	if !globalOn {
		return workbuddyThinkOff, ""
	}
	if req == nil {
		return workbuddyThinkUnspecified, ""
	}
	if req.Thinking != nil {
		switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
		case "disabled":
			return workbuddyThinkOff, ""
		case "enabled", "adaptive":
			effort = resolveReasoningEffort(req)
			return workbuddyThinkOn, effort
		default:
			return workbuddyThinkUnspecified, ""
		}
	}
	// 无 thinking 字段场景: OpenCode/ZCode 客户端由 output_config.effort 驱动思考
	if isOpenCodeUA(req.UserAgent) {
		effort = resolveReasoningEffort(req)
		if effort != "" {
			return workbuddyThinkOn, effort
		}
	}
	return workbuddyThinkUnspecified, ""
}

// workbuddyResolveOpenAIThinking 从 OpenAI Chat / Responses 入站请求体识别客户端思考意图, 返回 (mode, effort)。
func workbuddyResolveOpenAIThinking(bodyBytes []byte, globalOn bool) (mode workbuddyThinkingMode, effort string) {
	if !globalOn {
		return workbuddyThinkOff, ""
	}
	raw := grokExtractRawEffort(bodyBytes)
	if raw == "" {
		return workbuddyThinkUnspecified, ""
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none", "off", "disabled":
		return workbuddyThinkOff, ""
	default:
		effort = extractOpenAIReasoningEffort(bodyBytes)
		return workbuddyThinkOn, effort
	}
}

// workbuddyMapEffort 把内部规范化思考等级(low/medium/high/max)映射为 WorkBuddy 上游认的 reasoning_effort 取值。
// WorkBuddy 上游原生支持 {low, medium, high, max}。
func workbuddyMapEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "max", "xhigh":
		return "max"
	default:
		return "high" // 开思考但未识别具体档位时, 默认使用 high 档位
	}
}

// workbuddyApplyThinkingToChat 按三态把思考意图注入到发往 WorkBuddy 上游的 OpenAIChatRequest。
func workbuddyApplyThinkingToChat(chatReq *OpenAIChatRequest, mode workbuddyThinkingMode, effort string) {
	if chatReq == nil {
		return
	}
	// WorkBuddy 严禁注入 NIM 专属 chat_template_kwargs
	chatReq.ChatTemplateKwargs = nil

	switch mode {
	case workbuddyThinkOff:
		chatReq.ReasoningEffort = ""
	case workbuddyThinkOn:
		mapped := workbuddyMapEffort(effort)
		chatReq.ReasoningEffort = mapped
	default: // workbuddyThinkUnspecified
		chatReq.ReasoningEffort = ""
	}
}
