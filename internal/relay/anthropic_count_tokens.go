package relay

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ===== Anthropic /v1/messages/count_tokens 端点:本地零上游估算 =====
//
// 背景:Claude Code CLI 在每次真实调用 /v1/messages 之前,会先发一个 POST /v1/messages/count_tokens
// 请求预估算本次输入会消耗多少 token,用于本地上下文窗口管理与自动 compact 时机判断。
// Anthropic 官方 LLM Gateway Protocol 明确把该端点标为 (optional):缺失/失败时不致命,
// Claude Code 自动回退到本地估算。此前 relay 不实现该端点、回 404,CLI 虽能降级但日志噪声明显,
// 且让 CLI 拿不到一个 relay 侧的估算值。
//
// 本文件在入站判定处提前识别 /count_tokens 后缀,纯本地按字符级粗估 input_tokens 后直接回 200,
// 不请求任何上游、不消耗号池账号、不产生费用、不进 recordNvidiaUsage。算法故意偏保守(高估),
// 宁可让 CLI 提前触发 compact,也不低估导致 CLI 误以为还有余量继续往窗口里塞 —— 高估最坏只是更早
// compact,无功能性风险;低估可能撞上窗口硬上限。官方文档亦明确 count_tokens 本就是估算值,
// 与真实 /v1/messages 计费 input_tokens 可能略有差异,且该端点独立 RPM 限额、免费。
//
// 响应契约(对齐 Anthropic 官方 MessageTokensCount):
//   HTTP 200
//   {"input_tokens": <非零正整数>}

// countTokens 估算常量(禁止硬编码,集中声明)。比率故意偏保守(高估 token 数):
//   - 中日韩(CJK)及全角字符按 cjkCharsPerToken 计:中文约 1.5 字符/token,取保守上界。
//   - 其余(英文/代码/JSON 骨架)按 otherCharsPerToken 计:英文约 4 字符/token。
// estimateInputTokens 按 ceil(cn/1.5 + other/4) 给值,保底 1(避免 0 让 CLI 误判上下文为空),
// 上限 countTokensHardCap 钳制恶意超大输入,防单次估算循环被撑爆。
const (
	cjkCharsPerToken   = 1.5
	otherCharsPerToken = 4.0
	countTokensHardCap = 1_000_000
)

// handleNvidiaCountTokens 处理 POST /nvidia/v1/messages/count_tokens(及 /nvidia/v1/messages/count_tokens/
// 这种尾带斜杠的归一形态):纯本地估算 input_tokens 并回 200,不触达任何上游。
//
// 入站 body 解析复用 AnthropicRequest.UnmarshalJSON —— 它已兼容 system/content 的字符串与数组两种
// 格式(见 compat_translate.go 的 UnmarshalJSON),messages/tools 也已结构化,这里不重写解析逻辑,
// 严格遵循"精准覆盖而非粗暴替换"与接口驱动复用准则。
//
// body 解析失败回 400(与既有入站链路一致,如 handleNvidia 内 Anthropic 请求体解析失败回 400);
// 解析成功则一定回 200 + 非零 input_tokens(即便空请求也保底 1)。
func (h *APICompatHandler) handleNvidiaCountTokens(w http.ResponseWriter, bodyBytes []byte) {
	var req AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		h.log("🚫 [NVIDIA 中继] count_tokens 入站请求体解析失败: %v, 回写 400", err)
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"type":  "error",
			"error": map[string]interface{}{"type": "invalid_request_error", "message": "invalid anthropic request: " + err.Error()},
		})
		return
	}
	tokens := estimateInputTokens(&req)
	h.log("ℹ️ [NVIDIA 中继] count_tokens 本地估算 input_tokens=%d (零上游, 不计费)", tokens)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"input_tokens": tokens,
	})
}

// estimateInputTokensFromBody 从入站 Anthropic Messages 原始请求体字节估算 input_tokens。
// 供仅持有原始 body bytes(未解析为 AnthropicRequest)的调用方复用,例如 Other 号池「入站 Anthropic +
// 上游 OpenAI」响应回译链路首帧 message_start 的 input_tokens 估值填充。解析失败回退保底 1(与
// estimateInputTokens 的 nil/空语义一致),绝不返回 0,避免让 CLI 误判上下文为空。
func estimateInputTokensFromBody(bodyBytes []byte) int {
	if len(bodyBytes) == 0 {
		return 1
	}
	var req AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return 1
	}
	return estimateInputTokens(&req)
}

// estimateInputTokens 纯函数估算 Anthropic 请求的 input_tokens:
// 把 system + messages(各类 content block 文本) + tools(name/desc/input_schema) 拼成大字符串,
// 按字符类别(CJK vs 其余)分别加权求和,保底 1、上限 countTokensHardCap。
//
// 纯函数化便于单测覆盖(anthropic_count_tokens_test.go),且无副作用,可被任意路径安全复用。
func estimateInputTokens(req *AnthropicRequest) int {
	if req == nil {
		return 1
	}
	var sb strings.Builder
	// 1) system(UnmarshalJSON 已把数组形态归一为单字符串)
	if req.System != "" {
		sb.WriteString(req.System)
		sb.WriteByte(' ')
	}
	// 2) messages.content:遍历每个 content block,按类型取出文本载荷
	//    - text: Text
	//    - thinking: Thinking(前一轮 assistant 思考块;官方 count_tokens 说前一轮 thinking 不计,
	//      但 CLI 入站历史里更常见的是当前轮次的引用,保守计入不影响正确性,只是偏高)
	//    - tool_result: ToolResultContent(可能是 string 或 []block,flattenToolResultContent 已统一展开)
	//    - tool_use: Name + 序列化后的 Input(工具调用入参也算输入侧 token)
	//    - image: Source.Data(base64,占大体积)计字符 —— image block 的 base64 会被 NVIDIA 入站
	//      自愈降级抹成 text,这里保守计入 base64 字符数,贴近真实"输入侧体积"。
	for _, msg := range req.Messages {
		for _, c := range msg.Content {
			if c.Text != "" {
				sb.WriteString(c.Text)
				sb.WriteByte(' ')
			}
			if c.Thinking != "" {
				sb.WriteString(c.Thinking)
				sb.WriteByte(' ')
			}
			if c.Type == "tool_result" {
				if flat := flattenToolResultContent(c.ToolResultContent); flat != "" {
					sb.WriteString(flat)
					sb.WriteByte(' ')
				}
			}
			if c.Type == "tool_use" {
				if c.Name != "" {
					sb.WriteString(c.Name)
					sb.WriteByte(' ')
				}
				if len(c.Input) > 0 {
					if b, err := json.Marshal(c.Input); err == nil {
						sb.Write(b)
						sb.WriteByte(' ')
					}
				}
			}
			if c.Type == "image" && c.Source != nil && c.Source.Data != "" {
				sb.WriteString(c.Source.Data)
				sb.WriteByte(' ')
			}
		}
	}
	// 3) tools:name + description + 序列化后的 input_schema(工具声明也算输入侧 token)
	for _, tool := range req.Tools {
		if tool.Name != "" {
			sb.WriteString(tool.Name)
			sb.WriteByte(' ')
		}
		if tool.Description != "" {
			sb.WriteString(tool.Description)
			sb.WriteByte(' ')
		}
		if len(tool.InputSchema) > 0 {
			if b, err := json.Marshal(tool.InputSchema); err == nil {
				sb.Write(b)
				sb.WriteByte(' ')
			}
		}
	}
	// 4) tool_choice / thinking / output_config 等小体积配置字段不计入:
	//    它们体积极小且与 token 计费语义弱相关,计入反而引入噪声,跟随官方"估算"宽松语义省略。
	tokens := weightedTokenEstimate(sb.String())
	if tokens < 1 {
		tokens = 1
	}
	if tokens > countTokensHardCap {
		tokens = countTokensHardCap
	}
	return tokens
}

// weightedTokenEstimate 按字符类别加权估算 token 数:
// CJK/全角字符按 cjkCharsPerToken(保守 1.5),其余按 otherCharsPerToken(4)。
// ceil 上取整,避免 0.4 token 这种尾数被抹零低估。
// 暴露为包内可见便于单测直接断言算法本身(不依赖 AnthropicRequest 构造)。
func weightedTokenEstimate(s string) int {
	if s == "" {
		return 0
	}
	var cjkCount, otherCount float64
	for _, r := range s {
		if isCJKRune(r) {
			cjkCount++
		} else {
			otherCount++
		}
	}
	raw := cjkCount/cjkCharsPerToken + otherCount/otherCharsPerToken
	tokens := int(raw)
	if raw > float64(tokens) {
		tokens++ // ceil:上取整,保保守高估
	}
	return tokens
}

// isCJKRune 判定一个 rune 是否按 CJK 高密度(1.5 字符/token)计费。
// 覆盖:CJK 统一表意文字、CJK 扩展 A/B、日文假名、谚文、全角标点/符号。
// 全角数字/字母也走 CJK 比率(全角字符密度与中文一致)。
func isCJKRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一表意文字
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK 扩展 A
		return true
	case r >= 0x20000 && r <= 0x2A6DF: // CJK 扩展 B
		return true
	case r >= 0x3040 && r <= 0x30FF: // 平假名 + 片假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 谚文音节
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角 ASCII/标点/符号
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK 标点符号
		return true
	}
	return false
}
