package relay

import (
	"encoding/json"
	"strings"
)

// grok_thinking.go: Grok(x.ai) 号池专属思考(量化推理)注入器。
//
// 关键差异(为何 Grok 不能复用 nvidia/other 池的思考注入分支):
//   - NVIDIA NIM 上游只认 chat_template_kwargs{thinking,reasoning_effort},拒收顶层 reasoning_effort(400);
//   - Other 池/OpenAI 官方链路走顶层 reasoning_effort,但 normalizeEffort 把 none/off/disabled 归一为
//     空串 → 省略不发(opt-in OFF 语义,Grok 上游会回落到官方默认 low 档思考);
//   - Grok(xAI)的 chat/completions 端点同样走顶层 reasoning_effort,取值集 {none,low,medium,high},
//     其中 "none" 表示「完全关闭推理」。用户需求是「显式传 none」表达关闭,故 Grok 路径必须把
//     客户端的关闭信号(thinking.type=disabled / reasoning_effort=none|off|disabled / 全局思考开关关闭)
//     显式翻回上游字段值 "none" 发出去,绝不像 other/nvidia 那样归一空串省略 —— 这正是本注入器的核心职责。
//
// 上游字段:Grok 走 chat/completions,顶层 reasoning_effort(OpenAIChatRequest.ReasoningEffort 字段,
// json:"reasoning_effort,omitempty");非空即透传,空串即省略(omit)。
//
// 三态语义(mode):
//   - off:显式关闭思考 → 注入 reasoning_effort="none"(绝不可省略,否则 Grok 回落 default low 仍思考)。
//   - on:客户端显式开思考 → 注入 reasoning_effort=<档值>(low/medium/high;max→high 因 xAI 官方无 max)。
//   - unspecified:客户端未表达任何 thinking 字段(opt-in 默认) → 注入空串(省略),让上游用官方默认 low。
//
// 全局总闸 IsEnableThinkingMode()==false 时强制 off(注入 "none"):与 nvidia/other「全局关→省略」
// 不同 —— Grok 的「全局关闭」语义需真正关掉上游推理,故显式发 none。

// grokThinkingMode 是 Grok 路径对客户端思考意图的三态分类。
type grokThinkingMode int

const (
	// grokThinkUnspecified 表示客户端未表达任何 thinking 信号(opt-in 默认)。
	grokThinkUnspecified grokThinkingMode = iota
	// grokThinkOff 表示客户端(或全局总闸)显式关闭思考。
	grokThinkOff
	// grokThinkOn 表示客户端显式开启思考。
	grokThinkOn
)

// grokResolveAnthropicThinking 从 Anthropic Messages 入站请求识别客户端思考意图,返回 (mode, effort)。
//
// 判定链(与 nvidia 链路 thinkingRequested + resolveReasoningEffort 对齐,但三态分类用于 Grok):
//   1. 全局总闸关(IsEnableThinkingMode()==false)→ off;
//   2. thinking.type=="disabled" → off(显式关,与 Anthropic 官方 disabled 语义一致);
//   3. thinking.type∈{"enabled","adaptive"} → on,effort 取 resolveReasoningEffort 的规范化值
//      (low/medium/high/max,经 grokMapEffort 映射为上游认的 low/medium/high,max→high);
//   4. 无 thinking 字段或缺省类型 → unspecified(让上游用官方默认 low,不强关不强开)。
//
// 注意:Anthropic-Beta 头里的 redact-thinking-* 不参与本决策(与 nvidia 链路同口径,
// claude-cli 2.1.220 开/关两态均常驻该头,关思考的正路是 body thinking.type=disabled 或省略 thinking 字段)。
func grokResolveAnthropicThinking(req *AnthropicRequest, globalOn bool) (mode grokThinkingMode, effort string) {
	if !globalOn {
		// 全局总闸关:强制 off,所有上下游请求都发 "none" 关闭推理。
		return grokThinkOff, ""
	}
	if req == nil || req.Thinking == nil {
		// 无 thinking 字段 → opt-in 默认,不强开不强关。
		return grokThinkUnspecified, ""
	}
	switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
	case "disabled":
		// 客户端显式关思考 → off(本注入器会把 "none" 发给上游,而非省略)。
		return grokThinkOff, ""
	case "enabled", "adaptive":
		// 客户端显式开思考 → on,effort 取档。
		effort = resolveReasoningEffort(req) // 返回 low/medium/high/max(内部规范化)
		return grokThinkOn, effort
	default:
		// 缺省/未识别 type → unspecified。
		return grokThinkUnspecified, ""
	}
}

// grokResolveOpenAIThinking 从 OpenAI Chat / Responses 入站请求体识别客户端思考意图,返回 (mode, effort)。
//
// 入站 OpenAI 协议的「思考显式信号」即 reasoning_effort 字段(顶层 Codex 形态或 OpenRouter reasoning.effort
// 嵌套形态,均由 extractOpenAIReasoningEffort 统一提取)。判定链:
//   1. 全局总闸关(IsEnableThinkingMode()==false)→ off;
//   2. 提取到 reasoning_effort ∈ {none, off, disabled} → off(显式关);
//   3. 提取到其它有效档(low/medium/high/max)→ on,effort 经 grokMapEffort 映射;
//   4. 未提取到任何 reasoning_effort 字段 → unspecified(让上游用官方默认 low)。
//
// 关键差异:extractOpenAIReasoningEffort 内部对 none/off/disabled 走 normalizeEffort 归一为 ""(空串),
// 这里无法仅靠其返回值区分「显式关(off)」与「未指定(unspecified)」。故本函数在调用它之前先做一次
// 原始提取 grokExtractRawEffort,区分三态:有且为关闭词 → off;有且为有效档 → on;无 → unspecified。
func grokResolveOpenAIThinking(bodyBytes []byte, globalOn bool) (mode grokThinkingMode, effort string) {
	if !globalOn {
		return grokThinkOff, ""
	}
	raw := grokExtractRawEffort(bodyBytes)
	if raw == "" {
		// 未发 reasoning_effort 字段 → opt-in 默认。
		return grokThinkUnspecified, ""
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none", "off", "disabled":
		// 客户端显式关思考 → off。
		return grokThinkOff, ""
	default:
		// 提取到有效档(经 normalizeEffort 归一为 low/medium/high/max)→ on。
		effort = extractOpenAIReasoningEffort(bodyBytes) // 返回规范化值或空串
		return grokThinkOn, effort
	}
}

// grokExtractRawEffort 直接从入站 body 抽取 reasoning_effort 原始字符串(不经归一),
// 供 grokResolveOpenAIThinking 区分「显式关」「未指定」三态。
// 支持 Codex 顶层 reasoning_effort 与 OpenRouter reasoning.effort 两种形态。
// 返回 lowercase 原值(未归一)或空串。
func grokExtractRawEffort(bodyBytes []byte) string {
	if len(bodyBytes) == 0 {
		return ""
	}
	var raw struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if json.Unmarshal(bodyBytes, &raw) == nil {
		if e := strings.ToLower(strings.TrimSpace(raw.ReasoningEffort)); e != "" {
			return e
		}
		if e := strings.ToLower(strings.TrimSpace(raw.Reasoning.Effort)); e != "" {
			return e
		}
	}
	return ""
}

// grokMapEffort 把内部规范化思考等级(low/medium/high/max)映射为 xAI Grok 上游认的 reasoning_effort 取值。
//
// xAI 官方 chat/completions 端点 reasoning_effort 取值集为 {none, low, medium, high}(官方无 max)。
// 映射:max/xhigh → high(顶级克制档映射为官方最高档 high);low/medium/high 原样 1:1;其余/空 → 空(不注入)。
func grokMapEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "max", "xhigh":
		return "high" // xAI 官方无 max,顶级映射为 high
	default:
		return "" // 空串/未识别 → 不注入(末档兜底交由上游默认)
	}
}

// grokApplyThinkingToChat 按三态把思考意图注入到发往 Grok 上游的 OpenAIChatRequest。
//
// 注入规则(真值表落地):
//   - grokThinkOff → chatReq.ReasoningEffort = "none"(显式关,绝不可省略,否则 Grok 回落 default low);
//   - grokThinkOn  → chatReq.ReasoningEffort = grokMapEffort(effort);
//     effort 经映射为空(未识别档)时【按用户基调注入 "none"】—— 即「开思考但定档失败」也保守地关闭,
//     避免不明档位被上游当成 default low 悄悄烧 token。这是「开思考未定档」的边界处理(见方案 flag)。
//   - grokThinkUnspecified → chatReq.ReasoningEffort = ""(省略,让上游用官方默认 low);
//     符合 opt-in 语义:客户端没表达,不强开不强关,尊重 xAI 官方默认行为。
//
// 同时强制清空 ChatTemplateKwargs(Grok 绝不注入 NIM 专属 kwargs,与 other/nvidia 链路隔离)。
func grokApplyThinkingToChat(chatReq *OpenAIChatRequest, mode grokThinkingMode, effort string) {
	if chatReq == nil {
		return
	}
	// Grok 上游只认顶层 reasoning_effort,绝不注入 NIM 专属 chat_template_kwargs。
	chatReq.ChatTemplateKwargs = nil
	switch mode {
	case grokThinkOff:
		chatReq.ReasoningEffort = "none"
	case grokThinkOn:
		mapped := grokMapEffort(effort)
		if mapped != "" {
			chatReq.ReasoningEffort = mapped
		} else {
			// 开思考但定档失败(未识别档) → 保守注入 "none",避免 default low 悄悄思考。
			chatReq.ReasoningEffort = "none"
		}
	default: // grokThinkUnspecified
		chatReq.ReasoningEffort = "" // 省略,让上游用官方默认 low(opt-in 语义)
	}
}
