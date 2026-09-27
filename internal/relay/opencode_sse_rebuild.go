package relay

import (
	"bytes"
	"encoding/json"
	"strings"
)

// opencode_sse_rebuild.go: 把聚合后的 OpenAI Chat 响应重新序列化为 Chat SSE 流。
//
// 设计动机:
//   Responses-only 模型(如 muse-spark-*)的上游是 Responses SSE, 中继需先聚合
//   (见 opencode_responses_aggregate.go)。但客户端若要求流式(Anthropic SSE /
//   Responses SSE / OpenAI SSE), 直接把聚合结果当 JSON 返回会导致客户端认为
//   "流断了"而反复重试 —— 实测真实 opencode CLI 走 Anthropic 入站时, 收到 JSON
//   会持续重发请求直至超时。
//
//   为复用既有成熟的 Chat-SSE→各协议转换器(OpenAIChatSSEToAnthropicSSE /
//   OpenAIChatSSEToResponsesSSE), 此处把聚合结果"倒带"成 Chat SSE 帧序列,
//   再交由那些转换器输出 —— 避免为每种下游协议各写一份 Responses SSE 转换器。
//
// 帧序列(标准 OpenAI Chat SSE):
//   role 帧 → reasoning 帧(若有) → content 帧(若有) → tool_calls 帧(若有)
//   → 末帧(含 finish_reason 与 usage) → [DONE]
//
// 注意: 本函数产出的是**一次性完整流**(非实时), 因为输入已聚合完毕。
// 对下游客户端而言仍是合法 SSE 流可正常解析; 代价是失去逐字实时性
// —— 这是 Responses-only 模型在当前架构下的固有取舍(上游事件模型与下游不同)。

// rebuildChatSSEFromAggregated 把聚合响应转为 Chat SSE 字节流。
func rebuildChatSSEFromAggregated(resp *OpenAIChatResponse) []byte {
	var buf bytes.Buffer
	if resp == nil || len(resp.Choices) == 0 {
		buf.WriteString("data: [DONE]\n\n")
		return buf.Bytes()
	}

	choice := resp.Choices[0]
	msg := choice.Message

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage interface{}) {
		frame := map[string]interface{}{
			"id":      resp.ID,
			"object":  "chat.completion.chunk",
			"created": resp.Created,
			"model":   resp.Model,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         delta,
					"finish_reason": finish,
				},
			},
		}
		if usage != nil {
			frame["usage"] = usage
		}
		b, err := json.Marshal(frame)
		if err != nil {
			return
		}
		buf.WriteString("data: ")
		buf.Write(b)
		buf.WriteString("\n\n")
	}

	// 1. role 帧
	writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)

	// 2. 思考内容(部分上游用 reasoning_content 承载)
	if msg.ReasoningContent != "" {
		writeChunk(map[string]interface{}{"reasoning_content": msg.ReasoningContent}, nil, nil)
	}

	// 3. 正文内容
	if msg.Content != "" {
		writeChunk(map[string]interface{}{"content": msg.Content}, nil, nil)
	}

	// 4. 工具调用(整块给出, 非分片)
	for i, tc := range msg.ToolCalls {
		writeChunk(map[string]interface{}{
			"tool_calls": []map[string]interface{}{
				{
					"index": i,
					"id":    tc.ID,
					"type":  "function",
					"function": map[string]interface{}{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				},
			},
		}, nil, nil)
	}

	// 5. 末帧: finish_reason + usage
	writeChunk(map[string]interface{}{}, choice.FinishReason, map[string]interface{}{
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"total_tokens":      resp.Usage.TotalTokens,
	})

	buf.WriteString("data: [DONE]\n\n")
	return buf.Bytes()
}

// rebuildChatSSEString 便于测试断言。
func rebuildChatSSEString(resp *OpenAIChatResponse) string {
	return string(rebuildChatSSEFromAggregated(resp))
}

// countSSEFrames 统计 SSE data 帧数, 供测试使用。
func countSSEFrames(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "data: ") {
			n++
		}
	}
	return n
}
