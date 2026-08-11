// Package pricing: pricing_anchor.go —— AI 计费生成专用的真实厂商牌价锚点表。
//
// 用途与定位(与 pricing.go 的 defaultPricing 严格隔离):
//   - defaultPricing(pricing.go:26)是「计费匹配引擎」的兜底价表,键名与 GetPricingForModel
//     的 exactMappings / 模糊分支硬绑,改动会连锁破坏计费匹配——故本表不动它。
//   - realPricingAnchor 是「AI 生成质检锚点」,只读、只供 AIPriceGenerator.Generate 在拿到
//     AI 返回结果后做偏差比对用,不进 pricing.json,不影响 CalculateCostBreakdown 等计费消费方。
//
// 数据来源:本表收录截至编写时各厂商官网公开的真实型号 USD/每百万 Tokens 标价。各厂牌价
// 随时间变动快,本表仅作为「AI 搜到的与锚点偏差 > 2x 时前端打红色警告」的比对基准,不是
// 最终计费来源——最终计费落盘仍走前端确认后的 update-pricing-batch → ModelRate。
//
// 比对规则(见 AnchorConflict):键为模型基名小写(如 "gemini-2.5-flash");若某 AI 生成结果
// 在锚点表中命中,且其 Input 单价相对锚点偏差超过 anchorConflictRatio 倍(默认 2.0),
// 判定为锚点冲突,前端在该行渲染红色警示,提示用户人工核验该价格是否真确。
package pricing

// anchorConflictRatio 是判定 AI 生成价与真实锚点价偏差过大的倍数阈值。
// 当 |AI.Input - anchor.Input| / anchor.Input > anchorConflictRatio 时标记冲突。
// 默认 2.0:即 AI 给的输入价超过锚点价 2 倍或低于 1/2 时打警告。
const anchorConflictRatio = 2.0

// realPricingAnchor 是 AI 计费生成质检用的真实厂商公开牌价锚点表(USD/每百万 Tokens)。
// 键为模型基名小写(与 aiPricingCandidates 的基名清洗口径一致,可直接按下标查)。
// 仅被 AIPriceGenerator.Generate 用于 AnchorConflict 比对,不被任何计费消费路径读取。
//
// 注意:本表为参考起点值,各厂真实牌价随时间变动。落地后若需更新,直接改本表即可,
// 不影响任何已落盘的 pricing.json 与计费匹配引擎。
var realPricingAnchor = map[string]ModelRate{
	// Google Gemini 2.5 系列(官方公开价,ai.google.dev/pricing)
	"gemini-2.5-pro":         {Input: 1.25, Output: 10.00, Cached: 0.3125},
	"gemini-2.5-flash":       {Input: 0.30, Output: 2.50, Cached: 0.075},
	"gemini-2.5-flash-lite": {Input: 0.10, Output: 0.40, Cached: 0.025},

	// Anthropic Claude 4.5 系列(官方公开价,anthropic.com/pricing)
	// Claude 写入缓存(prompt caching)命中读价为输入价的 0.1 倍。
	"claude-sonnet-4-5": {Input: 3.00, Output: 15.00, Cached: 0.30},
	"claude-opus-4-5":   {Input: 5.00, Output: 25.00, Cached: 0.50},
	"claude-haiku-4-5":  {Input: 1.00, Output: 5.00, Cached: 0.10},

	// OpenAI GPT 系列(官方公开价,openai.com/api/pricing)
	"gpt-5":        {Input: 1.25, Output: 10.00, Cached: 0.125},
	"gpt-5-mini":   {Input: 0.25, Output: 2.00, Cached: 0.025},
	"gpt-5-nano":   {Input: 0.05, Output: 0.40, Cached: 0.005},
	"gpt-oss-120b": {Input: 0.15, Output: 0.60, Cached: 0.0375},

	// DeepSeek(官方公开价,deepseek.com/pricing)
	// deepseek-chat(V3)缓存命中价为输入价的 ~5.2%(0.014/0.27);reasoner 无缓存定价时按输入价估算。
	"deepseek-chat":     {Input: 0.27, Output: 1.10, Cached: 0.014},
	"deepseek-reasoner": {Input: 0.55, Output: 2.19, Cached: 0.014},
}

// anchorConflictForModel 判定一条 AI 生成的单价结果是否与真实锚点价偏差过大。
// 命中锚点表且 Input 偏差 > anchorConflictRatio 倍时返回 true(前端打红色警示);
// 不在锚点表中的模型(如虚构型号)返回 false——它们由 Estimated/Grounded 标记负责提示。
//
// rateOffset 防御性兜底:锚点 Input 为 0 时退化为不判冲突,避免除零。
func anchorConflictForModel(modelBaseLower string, rate ModelRate) bool {
	anchor, ok := realPricingAnchor[modelBaseLower]
	if !ok {
		return false
	}
	if anchor.Input <= 0 {
		return false
	}
	diff := rate.Input - anchor.Input
	if diff < 0 {
		diff = -diff
	}
	return diff/anchor.Input > anchorConflictRatio
}
