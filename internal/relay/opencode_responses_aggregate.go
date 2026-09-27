package relay

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// opencode_responses_aggregate.go: OpenAI Responses API SSE 流 → 非流式聚合器。
//
// 设计动机:
//   OpenCode Zen 上部分模型(如 muse-spark-*)仅支持 Responses API, 打
//   /chat/completions 恒返回 400 ModelProtocolUnsupported。中继对这类模型改用
//   /responses 端点并透传 Responses 协议(见 opencode.go 的 responsesOnly 分支)。
//   而 Responses API 恒为 SSE 驱动, 客户端若要非流式响应, 需在本层聚合。
//
// 与 opencode_sse_aggregate.go 的差异:
//   后者聚合 OpenAI **Chat** SSE(delta.content / delta.tool_calls),
//   本文件聚合 Responses SSE(response.output_text.delta / response.output_item.*),
//   两者事件模型不同, 故独立实现。
//
// Responses SSE 关键事件(实测 Zen 返回序列):
//   response.created              → 含 response.id / model, 作为元信息
//   response.in_progress          → 忽略
//   response.output_item.added    → 输出项开始(message / function_call)
//   response.content_part.added   → 内容块开始
//   response.output_text.delta    → 文本增量(delta 字段)
//   response.content_part.done    → 内容块结束
//   response.output_item.done     → 输出项结束(function_call 在此携带完整 arguments)
//   response.completed            → 流结束, 含 response.usage
//
// 聚合产物:
//   复用既有 OpenAIChatResponse 结构(与 Chat 聚合器一致), 使下游回写路径
//   (OpenAIChatToAnthropic / OpenAIChatToResponses / 直出) 完全复用。
//   Responses 的 function_call 输出项映射为 Chat 的 tool_calls。

// aggregateOpenAIResponsesSSE 读取 Responses SSE 流, 聚合成单个 OpenAIChatResponse。
//
// fallbackModel 用于上游未回传 model 时兜底。
func aggregateOpenAIResponsesSSE(reader io.Reader, fallbackModel string) (*OpenAIChatResponse, error) {
	scanner := bufio.NewScanner(reader)
	// 单帧可能很大(response.created 携带完整 tools 定义), 放宽至 4MB。
	scanner.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	var (
		respID       string
		createdAt    int64
		model        string
		content      strings.Builder
		reasoning    strings.Builder
		toolCalls    []ChatToolCall
		usage        OpenAIChatUsage
		hasUsage     bool
		sawAnyFrame  bool
		sawCompleted bool
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Responses SSE 同时有 `event: xxx` 行与 `data: {...}` 行; 只解析 data。
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			// 畸形帧跳过, 不中断聚合。
			continue
		}

		evType, _ := raw["type"].(string)
		if evType == "" {
			continue
		}

		// 上游错误帧: {"type":"error","error":{...}} 或 response.failed
		if evType == "error" {
			msg, typ := extractResponsesError(raw)
			return nil, &openCodeUpstreamError{Type: typ, Message: msg}
		}
		if evType == "response.failed" {
			msg, typ := extractResponsesErrorFromResponse(raw)
			return nil, &openCodeUpstreamError{Type: typ, Message: msg}
		}

		sawAnyFrame = true

		switch evType {
		case "response.created", "response.in_progress":
			if resp, ok := raw["response"].(map[string]interface{}); ok {
				if v, ok := resp["id"].(string); ok && v != "" && respID == "" {
					respID = v
				}
				if v, ok := resp["model"].(string); ok && v != "" {
					model = v
				}
				if v, ok := resp["created_at"].(float64); ok && createdAt == 0 {
					createdAt = int64(v)
				}
			}

		case "response.output_text.delta":
			if s, ok := raw["delta"].(string); ok {
				content.WriteString(s)
			}

		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if s, ok := raw["delta"].(string); ok {
				reasoning.WriteString(s)
			}

		case "response.output_item.done":
			// function_call 项在 done 事件携带完整 arguments。
			if item, ok := raw["item"].(map[string]interface{}); ok {
				if t, _ := item["type"].(string); t == "function_call" {
					name, _ := item["name"].(string)
					args, _ := item["arguments"].(string)
					callID, _ := item["call_id"].(string)
					if callID == "" {
						callID, _ = item["id"].(string)
					}
					if name != "" {
						toolCalls = append(toolCalls, ChatToolCall{
							ID:   callID,
							Type: "function",
							Function: ChatToolCallFunction{
								Name:      name,
								Arguments: args,
							},
						})
					}
				}
			}

		case "response.completed":
			sawCompleted = true
			if resp, ok := raw["response"].(map[string]interface{}); ok {
				if v, ok := resp["model"].(string); ok && v != "" {
					model = v
				}
				if u, ok := resp["usage"].(map[string]interface{}); ok && len(u) > 0 {
					usage = parseResponsesUsage(u)
					hasUsage = true
				}
				// 部分上游在 completed 的 output 里回传最终文本, 若 delta 未收到则兜底提取。
				if content.Len() == 0 {
					if out, ok := resp["output"].([]interface{}); ok {
						content.WriteString(extractTextFromResponsesOutput(out))
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !sawAnyFrame {
		return nil, &openCodeUpstreamError{Type: "empty_stream", Message: "upstream returned no usable Responses SSE frame"}
	}
	// 未收到 completed 事件时仍返回已聚合内容, 但标注为不完整流由调用方决定。
	_ = sawCompleted

	if respID == "" {
		respID = "chatcmpl-opencode-resp-aggregated"
	}
	if model == "" {
		model = fallbackModel
	}

	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}

	msg := ChatMessage{Role: "assistant", Content: content.String()}
	if reasoning.Len() > 0 {
		msg.ReasoningContent = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	out := &OpenAIChatResponse{
		ID:      respID,
		Object:  "chat.completion",
		Created: createdAt,
		Model:   model,
		Choices: []OpenAIChatChoice{
			{Index: 0, Message: msg, FinishReason: finish},
		},
	}
	if hasUsage {
		out.Usage = usage
	}
	return out, nil
}

// extractResponsesError 从顶层 error 帧提取错误信息。
func extractResponsesError(raw map[string]interface{}) (msg, typ string) {
	if e, ok := raw["error"].(map[string]interface{}); ok {
		msg, _ = e["message"].(string)
		typ, _ = e["type"].(string)
	}
	if msg == "" {
		msg = "upstream returned an error"
	}
	return msg, typ
}

// extractResponsesErrorFromResponse 从 response.failed 事件的 response.error 提取错误。
func extractResponsesErrorFromResponse(raw map[string]interface{}) (msg, typ string) {
	if resp, ok := raw["response"].(map[string]interface{}); ok {
		if e, ok := resp["error"].(map[string]interface{}); ok {
			msg, _ = e["message"].(string)
			typ, _ = e["type"].(string)
		}
	}
	if msg == "" {
		msg = "upstream response failed"
	}
	return msg, typ
}

// parseResponsesUsage 把 Responses 的 usage 映射为 Chat usage 口径。
// Responses: {input_tokens, output_tokens, total_tokens, input_tokens_details:{cached_tokens}}
func parseResponsesUsage(u map[string]interface{}) OpenAIChatUsage {
	var out OpenAIChatUsage
	if v, ok := u["input_tokens"].(float64); ok {
		out.PromptTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok {
		out.CompletionTokens = int(v)
	}
	if v, ok := u["total_tokens"].(float64); ok {
		out.TotalTokens = int(v)
	}
	if det, ok := u["input_tokens_details"].(map[string]interface{}); ok {
		if v, ok := det["cached_tokens"].(float64); ok {
			out.PromptTokensDetails = OpenAIChatUsageTokensDetails{CachedTokens: int(v)}
		}
	}
	return out
}

// extractTextFromResponsesOutput 从 Responses 的 output 数组提取纯文本。
// output 元素形如 {type:"message", content:[{type:"output_text", text:"..."}]}
func extractTextFromResponsesOutput(out []interface{}) string {
	var sb strings.Builder
	for _, item := range out {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "message" {
			continue
		}
		parts, ok := m["content"].([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if txt, ok := pm["text"].(string); ok {
				sb.WriteString(txt)
			}
		}
	}
	return sb.String()
}
