package relay

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// opencode_sse_aggregate.go: OpenCode 上游 SSE 流 → 非流式 OpenAI Chat 响应聚合器。
//
// 设计动机:
//   OpenCode Zen 对免费模型(big-pickle / *-free 系列)实施策略限制 —— 仅接受
//   stream=true 的请求。实测单变量对照:
//     stream=true  → 200
//     stream=false → 403 {"type":"error","error":{"type":"FreeTierError",...}}
//   因此当客户端发起非流式请求(stream=false)而目标模型为免费模型时, 中继需强制
//   向上游发 stream=true, 拿到 SSE 后在本层聚合成完整的 OpenAI Chat 非流式响应,
//   再交回既有非流式回写路径(OpenAIChatToAnthropic / OpenAIChatToResponses),
//   使下游协议转换逻辑完全复用, 无需为每种协议各写一份聚合。
//
// 聚合语义:
//   - 逐帧解析 `data: {...}` 行, 累积 choices[].delta.content 与 delta.tool_calls;
//   - 保留首个非空 id / created / model, 用于填充聚合响应的元信息;
//   - 取最后一帧的 finish_reason;
//   - usage 以最后一帧携带的值为准(上游 stream_options.include_usage 保证末帧带 usage);
//   - 忽略 [DONE] 哨兵与空帧。
//
// 容错:
//   - 单帧 JSON 解析失败不中断整体聚合(跳过该帧), 避免个别畸形帧导致整请求失败;
//   - 上游在流中回传错误对象(type=error)时, 立即返回该错误, 由调用方转成对应协议错误。

// aggregatedToolCall 是聚合过程中的工具调用累积态。
type aggregatedToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// aggregateOpenAIChatSSE 读取 OpenAI Chat SSE 流, 聚合成单个 OpenAIChatResponse。
//
// 返回的 error 非 nil 时, 调用方应回写 502(上游流异常)或按 err 内容判定具体语义。
// 流中出现 {"type":"error",...} 或 {"error":{...}} 时, 以该错误为准返回。
func aggregateOpenAIChatSSE(reader io.Reader, fallbackModel string) (*OpenAIChatResponse, error) {
	scanner := bufio.NewScanner(reader)
	// 单帧可能很大(工具参数分片), 放宽缓冲上限至 1MB。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		respID      string
		createdAt   int64
		model       string
		finishRsn   string
		content     strings.Builder
		reasoning   strings.Builder
		toolCalls   = make(map[int]*aggregatedToolCall)
		toolOrder   []int
		usage       OpenAIChatUsage
		hasUsage    bool
		sawAnyFrame bool
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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

		// 上游错误帧: 形如 {"type":"error","error":{"type":"...","message":"..."}}
		if errObj, ok := raw["error"].(map[string]interface{}); ok {
			msg, _ := errObj["message"].(string)
			typ, _ := errObj["type"].(string)
			if msg == "" {
				msg = "upstream stream returned an error"
			}
			return nil, &openCodeUpstreamError{Type: typ, Message: msg}
		}
		if typ, ok := raw["type"].(string); ok && typ == "error" && raw["error"] == nil {
			return nil, &openCodeUpstreamError{Type: "error", Message: "upstream stream returned an error"}
		}

		sawAnyFrame = true

		if v, ok := raw["id"].(string); ok && v != "" && respID == "" {
			respID = v
		}
		if v, ok := raw["model"].(string); ok && v != "" {
			model = v
		}
		if v, ok := raw["created"].(float64); ok && createdAt == 0 {
			createdAt = int64(v)
		}
		if u, ok := raw["usage"].(map[string]interface{}); ok && len(u) > 0 {
			usage = parseOpenAIChatUsage(u)
			hasUsage = true
		}

		choices, ok := raw["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishRsn = fr
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			// 部分上游把内容放在 message 字段(非标准流式帧), 兼容处理。
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				delta = msg
			} else {
				continue
			}
		}
		if s, ok := delta["content"].(string); ok {
			content.WriteString(s)
		}
		if s, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(s)
		}
		accumulateToolCallDeltas(delta["tool_calls"], toolCalls, &toolOrder)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if !sawAnyFrame {
		return nil, &openCodeUpstreamError{Type: "empty_stream", Message: "upstream returned no usable SSE frame"}
	}

	if respID == "" {
		respID = "chatcmpl-opencode-aggregated"
	}
	if model == "" {
		model = fallbackModel
	}
	if finishRsn == "" {
		finishRsn = "stop"
	}

	msg := ChatMessage{Role: "assistant", Content: content.String()}
	if reasoning.Len() > 0 {
		msg.ReasoningContent = reasoning.String()
	}
	if len(toolOrder) > 0 {
		tools := make([]ChatToolCall, 0, len(toolOrder))
		for _, idx := range toolOrder {
			tc := toolCalls[idx]
			if tc == nil {
				continue
			}
			tools = append(tools, ChatToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: ChatToolCallFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments.String(),
				},
			})
		}
		if len(tools) > 0 {
			msg.ToolCalls = tools
		}
	}

	out := &OpenAIChatResponse{
		ID:      respID,
		Object:  "chat.completion",
		Created: createdAt,
		Model:   model,
		Choices: []OpenAIChatChoice{
			{Index: 0, Message: msg, FinishReason: finishRsn},
		},
	}
	if hasUsage {
		out.Usage = usage
	}
	return out, nil
}

// accumulateToolCallDeltas 把流式 tool_calls 分片累积到 map, 并记录首次出现顺序。
//
// OpenAI 流式工具调用的分片语义:
//   - 首帧携带 index + id + function.name, arguments 可能为空或首段;
//   - 后续帧仅携带 index + function.arguments 增量;
//   - 因此按 index 归并, arguments 顺序拼接。
func accumulateToolCallDeltas(raw interface{}, acc map[int]*aggregatedToolCall, order *[]int) {
	list, ok := raw.([]interface{})
	if !ok {
		return
	}
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		idxF, ok := m["index"].(float64)
		if !ok {
			continue
		}
		idx := int(idxF)

		tc, exists := acc[idx]
		if !exists {
			tc = &aggregatedToolCall{}
			acc[idx] = tc
			*order = append(*order, idx)
		}
		if id, ok := m["id"].(string); ok && id != "" {
			tc.ID = id
		}
		fn, ok := m["function"].(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok && name != "" {
			tc.Name = name
		}
		if args, ok := fn["arguments"].(string); ok {
			tc.Arguments.WriteString(args)
		}
	}
}

// parseOpenAIChatUsage 从原始 map 解析 usage 字段, 兼容 prompt_tokens_details 嵌套结构。
func parseOpenAIChatUsage(u map[string]interface{}) OpenAIChatUsage {
	var out OpenAIChatUsage
	if v, ok := u["prompt_tokens"].(float64); ok {
		out.PromptTokens = int(v)
	}
	if v, ok := u["completion_tokens"].(float64); ok {
		out.CompletionTokens = int(v)
	}
	if v, ok := u["total_tokens"].(float64); ok {
		out.TotalTokens = int(v)
	}
	if det, ok := u["prompt_tokens_details"].(map[string]interface{}); ok {
		if v, ok := det["cached_tokens"].(float64); ok {
			out.PromptTokensDetails = OpenAIChatUsageTokensDetails{CachedTokens: int(v)}
		}
	}
	return out
}

// openCodeUpstreamError 承载上游流中回传的错误对象, 供调用方按 type 判定语义
// (如 FreeTierError 走专门的日志分支)。
type openCodeUpstreamError struct {
	Type    string
	Message string
}

func (e *openCodeUpstreamError) Error() string {
	if e.Type == "" {
		return e.Message
	}
	return e.Type + ": " + e.Message
}
