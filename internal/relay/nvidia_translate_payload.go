package relay

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"antigravity-proxy/internal/account"
)

// nvidia_translate_payload.go: Anthropic SSE event payload 构造器 + 全局思考开关 + reasoning-as-text 开关 + mapNvidiaModel。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// messageStartPayload 生成 message_start 事件的 data 负载。
// 严格对齐 Anthropic 官方流式协议：顶层 type 必须是 "message_start"，且包含嵌套的 message 对象，
// 否则 VS Code Claude 扩展 SDK 的 MessageAccumulator 解析不到 message.id / message.content，
// 会报 "Message not found" 并降级为非流式模式——这正是 NVIDIA 中继流式持久失败而 antigravity(Gemini)
// 链正常工作的根因差异所在（antigravity 链 compat.go:870-882 已按正确嵌套实现）。
//
// inputTokens 为入站请求本地估算的输入 token 数(保底 1):官方真实 API 在 message_start 即给出
// 真实 input_tokens(它本身即服务端,请求一进来就知道输入 token);代理经 NVIDIA/Gemini 上游时,
// 流首物理上拿不到真实输入 token(上游首帧不带),此前置 0 会导致 Claude Code spinner 进行中
// 只显示 ↓(累计 output)而无 ↑(input),因为客户端读 message_start.usage.input_tokens 拿到 0。
// 现改为首帧回填本地估算值,让流首即有非零 ↑;真实累计值仍由末帧 message_delta.usage 覆盖,
// 结算精度(/cost、Stats 面板)不受影响。inputTokens<1 时保底 1,与官方非零语义一致。
func messageStartPayload(streamID, model string, inputTokens int) string {
	if model == "" {
		model = "nvidia"
	}
	if streamID == "" {
		streamID = fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	}
	if inputTokens < 1 {
		inputTokens = 1 // 保底 1,避免 0 让 CLI 误判上下文为空(与 estimateInputTokens 口径一致)
	}
	return jsonString(map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            streamID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			// usage:首帧 input_tokens 用入站请求本地估算值(保底 1),让客户端(Claude Code spinner
			// 进行中)在流首即可显示 ↑;output_tokens 起始占位为 1(官方惯例预扣占位,与 NVIDIA 路径
			// 历史一致)。真实累计值由末帧 message_delta.usage 覆盖(input/output 双填上游真实值)。
			"usage": map[string]interface{}{
				"input_tokens":                inputTokens,
				"output_tokens":               1,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})
}

// messageDeltaPayload 生成 message_delta 事件的 data 负载。
// 对齐 Anthropic 官方流式协议：usage 字段的 token 计数为累计值(cumulative)，
// 官方明确标注 "The token counts shown in the `usage` field of the `message_delta`
// event are *cumulative*"，故 output_tokens 必须填本次流的真实累计输出 token 数，
// input_tokens 填真实累计输入 token 数。早期的硬编码 {"output_tokens":0} 会让部分
// Claude Code SDK 的 MessageAccumulator 误判流未正常结束，触发"等连接关闭/下次请求
// 才整条渲染"的退化路径。
func messageDeltaPayload(stopReason string, outputTokens int) string {
	return jsonString(map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": outputTokens,
		},
	})
}

// contentBlockStartPayload 构造 content_block_start 事件 data 负载。
// 严格对齐 Anthropic 官方流式协议：文本块 content_block 必须带 "text":"" 字段，
// 否则新版 Claude Code / Cursor 插件的 MessageAccumulator 解析时无法建立 current text block，
// 紧接着的 content_block_delta(text_delta) 会报 "Received content_block_delta without a current message"。
// 工具块则需带 id/name/input 三字段。
func contentBlockStartPayload(index int, kind, id, name string) string {
	cb := map[string]interface{}{"type": kind}
	if kind == "text" {
		cb["text"] = ""
	} else if kind == "tool_use" {
		cb["id"] = id
		cb["name"] = name
		cb["input"] = map[string]interface{}{}
	}
	m := map[string]interface{}{
		"type":          "content_block_start",
		"index":         index,
		"content_block": cb,
	}
	return jsonString(m)
}

func contentBlockTextDeltaPayload(index int, text string) string {
	return jsonString(map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	})
}

func contentBlockInputJSONDeltaPayload(index int, partialJSON string) string {
	return jsonString(map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": partialJSON},
	})
}

func contentBlockStopPayload(index int) string {
	return jsonString(map[string]interface{}{"type": "content_block_stop", "index": index})
}

// contentBlockThinkingStartPayload 构造 thinking 块的 content_block_start 负载。
// 严格对齐 Anthropic 官方流式协议:thinking 块开块时 thinking 与 signature 均为空串,
// 后续由 thinking_delta 累积思考文本、由 signature_delta 在关块前补签名。
// 对无签名上游(NIM/GLM reasoning_content、Gemini 已剥签名的 thought)关块前发空串占位,
// 等同官方 display:"omitted" 形态 —— 满足事件序列形状,让 Claude Code SDK 的
// MessageAccumulator 能正常识别并渲染思考块。
func contentBlockThinkingStartPayload(index int) string {
	return jsonString(map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":      "thinking",
			"thinking":  "",
			"signature": "",
		},
	})
}

// contentBlockThinkingDeltaPayload 构造 thinking_delta 增量负载,承载上游推理过程的分片文本。
func contentBlockThinkingDeltaPayload(index int, thinking string) string {
	return jsonString(map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": thinking},
	})
}

// contentBlockSignatureDeltaPayload 构造 signature_delta 负载:关 thinking 块前发一次。
// 对无签名上游传空串占位,保证协议形态完整,避免客户端把缺 signature_delta 的 thinking 块判为不完整而丢弃。
func contentBlockSignatureDeltaPayload(index int, signature string) string {
	return jsonString(map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "signature_delta", "signature": signature},
	})
}

// mapNvidiaModel 按账号配置把入站模型名映射成上游模型 id。
func mapNvidiaModel(inModel string, acc *account.Account) string {
	return account.ResolveNvidiaModel(inModel, acc)
}

var globalReasoningAsText atomic.Bool

var globalEnableThinkingMode atomic.Bool

func init() {
	globalEnableThinkingMode.Store(true) // 思考模式默认开启
}

func SetGlobalReasoningAsText(v bool) {
	globalReasoningAsText.Store(v)
}

func IsReasoningAsText() bool {
	if globalReasoningAsText.Load() {
		return true
	}
	env := strings.ToLower(os.Getenv("REASONING_AS_TEXT"))
	return env == "true" || env == "1" || env == "yes"
}

func SetGlobalEnableThinkingMode(v bool) {
	globalEnableThinkingMode.Store(v)
}

func IsEnableThinkingMode() bool {
	if env := strings.ToLower(os.Getenv("ENABLE_THINKING_MODE")); env != "" {
		return env == "true" || env == "1" || env == "yes"
	}
	return globalEnableThinkingMode.Load()
}

