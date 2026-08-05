package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"antigravity-proxy/internal/account"
)

// nvidia_translate_response.go: OpenAI Chat -> Anthropic 非流式响应方向转换。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// OpenAIChatToAnthropic 把 OpenAI Chat Completions 非流式响应回译成 Anthropic Messages 响应。
func OpenAIChatToAnthropic(resp *OpenAIChatResponse) *AnthropicResponse {
	out := &AnthropicResponse{
		ID:           resp.ID,
		Type:         "message",
		Role:         "assistant",
		Model:        resp.Model,
		StopReason:   openAIFinishToAnthropicStop(resp.FinishReason()),
		StopSequence: nil,
		Usage: AnthropicResponseUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
	if out.ID == "" {
		out.ID = "msg_nvidia"
	}
	if out.Model == "" {
		out.Model = "nvidia"
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		content, toolUses := openAIChoiceMessageToAnthropic(choice.Message)
		out.Content = append(out.Content, content...)
		out.Content = append(out.Content, toolUses...)
	} else {
		out.Content = []AnthropicContent{{Type: "text", Text: ""}}
	}
	return out
}

// openAIChoiceMessageToAnthropic 把一个 OpenAI message(content + tool_calls)拆成 Anthropic content blocks。
// 返回值：先行 thinking block(若 reasoning 非空),再行 text block(s),其后跟 tool_use block 列表。
// 思考块顺序严格对齐流式路径 emitThinkingDelta:thinking 先于 text、先于 tool_use,signature 空串占位
// (无签名上游,Gemini 已剥真签名 / 第三方上游无思考签名概念,与流式 signature_delta 空串策略一致)。
//
// 既往缺口:本函数原只处理 m.Content 与 m.ToolCalls,忽略 m.ReasoningContent / m.Reasoning,
// 导致 Other 号池(openai 格式组)非流式响应时的推理内容被静默丢弃。流式路径已有 emitThinkingDelta 覆盖,
// 此处补齐非流式侧,使两路径对偶 —— 客户端经 Anthropic 入站、Other openai 上游非流式返
// reasoning_content 时,能正确拿到 Anthropic thinking content block。
func openAIChoiceMessageToAnthropic(m ChatMessage) ([]AnthropicContent, []AnthropicContent) {
	var texts []AnthropicContent
	// 思考块:优先 reasoning_content,reasoning 字段兜底(部分 NIM 上游模型思考走 reasoning 字段名)。
	// 对齐流式 nvidia_translate_sse.go:123-138 的双字段兜底逻辑,非流式与流式行为一致。
	reasoning := strings.TrimSpace(m.ReasoningContent)
	if reasoning == "" {
		reasoning = strings.TrimSpace(m.Reasoning)
	}
	if reasoning != "" {
		texts = append(texts, AnthropicContent{Type: "thinking", Thinking: reasoning, Signature: ""})
	}
	if strings.TrimSpace(m.Content) != "" {
		texts = append(texts, AnthropicContent{Type: "text", Text: m.Content})
	}
	var tools []AnthropicContent
	for i, tc := range m.ToolCalls {
		var input map[string]interface{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		if input == nil {
			input = make(map[string]interface{})
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("toolu_nvidia_%d", i)
		}
		tools = append(tools, AnthropicContent{
			Type:  "tool_use",
			ID:    id,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return texts, tools
}

// openAIFinishToAnthropicStop 把 OpenAI finish_reason 映射成 Anthropic stop_reason。
func openAIFinishToAnthropicStop(finish string) string {
	switch finish {
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func OpenAIFinishToAnthropicStop(finishReason string) string {
	return openAIFinishToAnthropicStop(finishReason)
}

func MapNvidiaModel(inModel string, acc *account.Account) string {
	return mapNvidiaModel(inModel, acc)
}

