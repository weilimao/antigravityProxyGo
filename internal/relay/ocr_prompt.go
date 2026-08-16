package relay

import (
	"fmt"
	"strings"
)

// ocr_prompt.go —— OCR 保真与视觉理解条款的单一信息源(单图 + 批量共用)。
//
// 核心设计:
//   - ocrFidelityCore      : 截图逐字可见、读到什么写什么的铁律 + 典型字形混淆示例;
//   - ocrVisualCuesClause  : 微观视觉线索与状态(波浪红线/告警、断点、未保存圆点、Diff 行、控件禁用/选中等);
//   - ocrTopologyClause    : 空间与逻辑拓扑(UI 嵌套从属、流程图/架构图流向或 Mermaid、Markdown 表格);
//   - ocrUncertaintyClause : 模糊/遮挡无法逐字确认时如实标注、严禁编造的条款。
//
// 单图(buildSingleOcrPrompt)与批量(buildBatchOcrPrompt/batchMarkerRule)消费同一组常量
// 拼装各自 prompt,后续调整任何条款只需改一处即可同步生效。
//
// 注:不确定标注「[此段因不清晰未完全确认,原文如下]」用单方括号,与批量拆分标记
// 「[[图k]]」(双方括号)不碰撞,不会被 splitBatchOcrText 误判为段边界而提前截断段尾。

// ocrFidelityCore 是 OCR 转写铁律(主语无关的核心句):截图逐字符可见,读到什么写什么,
// 给出典型字形混淆对(l/I、0/O、1/l)抑制改写冲动,并禁止按经验重写命令/报错。单图与
// 批量共用此核心句,由各自模板在其前补主语("本张图是…" / "每张图都是…")。
const ocrFidelityCore = "图中的代码、报错、标识符都是逐字符可见的原始数据。你读到什么词就原样写下什么词，严禁改写字形（如把 `l` 写成 `I`、把 `0` 写成 `O`、把 `1` 写成 `l`），严禁按经验重写命令或报错信息。"

// ocrVisualCuesClause 是微观视觉与状态线索条款:重点捕捉代码波浪线、断点、高亮、状态指示等。
const ocrVisualCuesClause = "必须敏锐捕捉微观视觉与状态线索：代码中的波浪红线/黄色告警位置、断点标记、光标焦点、文件未保存小圆点、Diff 变更行（+/-）、终端状态码颜色（红/绿/黄）、UI 按钮/输入框的禁用（disabled）或选中/加载中状态。"

// ocrTopologyClause 是空间结构与逻辑拓扑条款:规范 UI 层次与图表流向表达。
const ocrTopologyClause = "若含 UI 或 IDE 界面，注明高亮项、报错弹窗、按钮状态及其在画面中的相对位置与父子层级关系；若含流程图/架构图，使用清晰的节点流向（如 `节点A --> 节点B`）或 Mermaid 语法还原所有连线与数据流；若含表格，使用 Markdown 表格完整还原。"

// ocrUncertaintyClause 是不确定标注条款:文字轮廓被放大/模糊/遮挡无法逐字确认时,先
// 如实标注固定前缀再输出确信部分,严禁编造剩余字符。单图与批量共用。
// 用单方括号「[ … ]」包裹标注,与批量标记「[[图k]]」(双方括号)不碰撞。
const ocrUncertaintyClause = "若文字轮廓被放大/模糊/遮挡而无法逐字确认，先如实标注“[此段因不清晰未完全确认，原文如下]”，再输出你确信的部分，严禁编造剩余字符。"

// ocrMarkerUncertaintyPrefix 是不确定标注在输出里固定使用的段前缀,供测试断言锚定。
const ocrMarkerUncertaintyPrefix = "[此段因不清晰未完全确认，原文如下]"

// buildSingleOcrPrompt 构造单图 OCR 的 prompt,消费 ocrFidelityCore / ocrVisualCuesClause /
// ocrTopologyClause / ocrUncertaintyClause 共享常量,与批量 prompt 共享保真条款单一信息源。
// promptCtx 非空走靶向模板、空走通用提取模板。
func buildSingleOcrPrompt(promptCtx string) string {
	const role = "你是一个顶级的多模态视觉分析助手。"
	const noPreamble = "直接输出结构化结果，严禁包含任何前言、引言或客套话。"
	ctx := strings.TrimSpace(promptCtx)
	if ctx != "" {
		// 靶向模板:注入用户提问上下文,首条做靶向分析。
		return fmt.Sprintf(
			"%s请深入分析图片内容并准确提取关键信息。\n\n【用户提问上下文】：用户在发送此图片时附带的提问/说明文本为：\n\"%s\"\n\n请按以下要求分析：\n"+
				"1. [重点靶向分析]：结合上述用户的提问与关注点，重点分析图片中与问题相关的代码行、报错提示、界面元素或逻辑关系。\n"+
				"2. [微观视觉与状态线索]：%s\n"+
				"3. [文字与代码精准提取 (OCR)]：\n"+
				"   - 本张图是用户直接截取的屏幕画面，%s\n"+
				"   - 原样逐字提取图中涉及的代码、控制台报错或文本，保持原始缩进与排版，不要自动修正错别字，用 Markdown 代码块包裹。\n"+
				"   - %s\n"+
				"4. [空间与结构]：\n"+
				"   - %s\n"+
				"5. [输出要求]：%s",
			role, ctx, ocrVisualCuesClause, ocrFidelityCore, ocrUncertaintyClause, ocrTopologyClause, noPreamble,
		)
	}
	// 通用模板:无提问上下文,首条做图像总体概览。
	return fmt.Sprintf(
		"%s请深入分析图片内容并准确提取关键信息。要求如下：\n"+
			"1. [图像总体概览]：用一句话概括图片类型（如：IDE代码截图、控制台报错、UI界面、架构流程图等）及核心意图。\n"+
			"2. [微观视觉与状态线索]：%s\n"+
			"3. [文字与代码精准提取 (OCR)]：\n"+
			"   - 本张图是用户直接截取的屏幕画面，%s\n"+
			"   - 提取图中所有的文本、代码、终端命令与报错堆栈。\n"+
			"   - 代码与报错日志必须【原样逐字提取】，严格保留缩进、换行与标点符号，严禁自动修改拼写错误。使用 Markdown 代码块包裹。\n"+
			"   - %s\n"+
			"4. [视觉布局与逻辑关系]：\n"+
			"   - %s\n"+
			"5. [输出要求]：%s",
		role, ocrVisualCuesClause, ocrFidelityCore, ocrUncertaintyClause, ocrTopologyClause, noPreamble,
	)
}
