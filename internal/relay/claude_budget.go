package relay

import "strings"

// ClaudeBudgetMargin 是 Vertex AI Claude 对 "max_tokens 必须 > thinking.budget_tokens" 约束预留的安全余量。
//
// 背景:daily-cloudcode-pa(antigravity 号池上游)会把 Gemini 请求体的 generationConfig.thinkingBudget /
// maxOutputTokens 重译为 Vertex Anthropic messages 的 thinking.budget_tokens / max_tokens;Vertex Claude
// 严格校验 max_tokens > thinking.budget_tokens,违反即返回 400 INVALID_ARGUMENT
// ("max_tokens must be greater than thinking.budget_tokens",request_id 形如 req_vrtx_...)。
//
// 当代理注入固定 thinkingBudget(>0)却未同步抬高或保留足够的 maxOutputTokens 时,就会触发该 400。
// 128 是经验余量:足够覆盖重译舍入与不变式差值,又不至于浪费输出上限。
const ClaudeBudgetMargin = 128

// CalcClaudeGuaranteedMaxOutput 在目标为 claude-* 模型(经 antigravity 号池/daily-cloudcode-pa 重译为
// Vertex Anthropic messages)且本次会向上游注入固定 thinkingBudget(committedBudget>0)时,守住
// "maxOutputTokens 必须 > thinkingBudget" 的 Vertex 不变式,避免上游 400。
//
// 不变式不适用时(isClaudeModel=false,或 committedBudget<=0 即只 includeThoughts 无 budget 字段,
// 如 gemini flash/pro 系、"includeThoughts:false"、"-1 自适应" 路径)原样返回 maxOutputTokens,
// 确保既有注入行为零回归。
//
// 返回值即应写入 generationConfig.maxOutputTokens 的最终值:
//   - 用户未设(maxOutputTokens<=0)→ committedBudget + ClaudeBudgetMargin
//   - 用户设了但 < committedBudget + ClaudeBudgetMargin → 抬升到 committedBudget + ClaudeBudgetMargin
//   - 用户设了且 >= committedBudget + ClaudeBudgetMargin → 原样保留
func CalcClaudeGuaranteedMaxOutput(committedBudget, maxOutputTokens int, isClaudeModel bool) int {
	if !isClaudeModel || committedBudget <= 0 {
		return maxOutputTokens
	}
	required := committedBudget + ClaudeBudgetMargin
	if maxOutputTokens <= 0 || maxOutputTokens < required {
		return required
	}
	return maxOutputTokens
}

// IsClaudeModelForBudget 判定入站模型是否为经 MapClientModelToGemini 保留为 claude-* 原(
// 即上游会以 Vertex Anthropic messages 协议处理、受 "max_tokens > thinking.budget_tokens" 约束的模型)。
//
// 依据 MapClientModelToGemini(compat_translate.go:432):仅当模型名含 "claude-sonnet" 或 "claude-opus"
// 时原样保留为 claude;其余 claude-3-* / claude-haiku-* 等会被降级映射为 gemini-1.5-*,不再受该约束。
// 故此处精确匹配 sonnet/opus,避免对降级为 gemini 的旧 claude-3-* 误抬 maxOutputTokens。
func IsClaudeModelForBudget(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "claude-sonnet") || strings.Contains(m, "claude-opus")
}
