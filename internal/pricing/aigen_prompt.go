// Package pricing: aigen_prompt.go —— AI 一键生成计费的提示词构造。
//
// 从 aigen.go 抽离的纯函数 buildPricingPrompt,职责单一便于单测与改写。
//
// 去幻觉重写要点(相对旧版):
//   - 旧版开篇"基于你的训练知识"诱导模型走纯知识路径,本版改为显式驱动模型优先调用
//     google_search 工具检索各厂商官网定价页,基于检索到的真实公开标价填写;
//   - 旧版第 6 条"不确定就基于相邻型号估算,不许返回 0/空"是幻觉核心指令源,
//     本版改为"检索后仍无法确信则给最合理估算,并在 estimated 字段标 true",
//     允许 AI 诚实标注不确定性,而非强制编一个数字压回去;
//   - JSON schema 扩展:元素新增可选 estimated(bool)与 source_url(string)字段,
//     供后端切分 Grounded/Estimated 标记与来源 URL 透出给前端。
package pricing

import (
	"fmt"
	"strings"
)

// buildPricingPrompt 构造让 AI 给一批模型生成 USD/每百万 tokens 单价的提示词。
//
// 强约束:
//   - 优先调用 google_search 工具检索厂商官网定价页(openai/anthropic/google/deepseek 等),
//     从检索结果中提取真实公开标价,而非凭记忆编造;
//   - 仅输出 JSON 数组、顺序与输入一致、首字符为 '[';
//   - 不确信时给最合理估算并标 estimated:true,不强制 0/空,允许诚实标注;
//   - cached 未公布按 input×0.25 估算并标 estimated:true。
//
// 模型名作为键保留原始基名(已由前端清洗掉前缀)直接回传。
func buildPricingPrompt(models []string) string {
	var b strings.Builder
	b.WriteString("你是资深的云模型计费定价分析师。请优先调用 google_search 工具,逐一检索下面这批 AI 模型的")
	b.WriteString("官方公开定价页(如 https://openai.com/api/pricing/、https://www.anthropic.com/pricing、")
	b.WriteString("https://ai.google.dev/gemini/pricing、https://api-docs.deepseek.com/quick_start/pricing 等),")
	b.WriteString("基于检索到的真实公开标价填写单价。单位统一为 USD/每百万 Tokens")
	b.WriteString("(United States Dollar per one million tokens),包含输入(input)、输出(output)、")
	b.WriteString("缓存命中(cached)三类单价。\n\n")
	b.WriteString("严格要求:\n")
	b.WriteString("1. 仅输出一个 JSON 数组,绝对不要 markdown 代码块标记(```),不要任何解释性前言或总结,")
	b.WriteString("回答的第一个字符必须是 '['。\n")
	b.WriteString("2. 数组长度与顺序必须与下方输入模型列表完全一致,每个元素形如 ")
	b.WriteString(`{"name":"模型名","input":数值,"output":数值,"cached":数值,"estimated":是否估算,"source_url":"引用URL"}` + "。\n")
	b.WriteString("3. name 字段回传我给你的模型原始名称,不要改名不要加引号外符号。\n")
	b.WriteString("4. 对每个模型,请先用 google_search 工具检索其厂商官方定价页,从检索结果中提取真实公开的")
	b.WriteString("input/output/cached 单价(精确到 6 位小数)。若确信检索到的是官方真实标价,")
	b.WriteString("estimated 字段填 false,source_url 填该官方定价页 URL。\n")
	b.WriteString("5. cached 是缓存命中输入的单价。各厂常用口径:Google/Anthropic 的缓存命中价约为输入价的 0.1~0.25 倍。")
	b.WriteString("若该模型厂家明确公布缓存定价用公布值;若未公布,则按 input 单价乘以 0.25 估算填入 cached 字段,")
	b.WriteString("并把该条的 estimated 字段标为 true。\n")
	b.WriteString("6. 若检索后仍无法确信该模型存在,或该模型尚未公开发布、无官方定价页,")
	b.WriteString("基于同系列相邻型号的定价规律给出你认为最合理的估算值填入 input/output/cached,")
	b.WriteString("并把该条的 estimated 字段标为 true(此类条目前端会醒目标注「估算」,供用户人工核验去留)。\n")
	b.WriteString("7. 不允许把 input/output 写成 0、空或 null;cached 允许按估算值填,但同样不许为空或 null。\n")
	b.WriteString("8. source_url 在你有引用官方定价页 URL 时填入;无明确引用时可留空字符串。\n\n")
	b.WriteString("需定价的模型列表(共 ")
	b.WriteString(fmt.Sprintf("%d 个):", len(models)))
	for i, m := range models {
		if i%6 == 0 {
			b.WriteString("\n")
		}
		b.WriteString(m)
		if i < len(models)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString("\n\n现在请直接输出 JSON 数组,第一个字符必须是 '['。")
	return b.String()
}
