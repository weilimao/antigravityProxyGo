package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
)

// nvidia_responses.go 实现 NVIDIA 号池对 OpenAI Responses API("/v1/responses")的协议兼容。
//
// 入站方向：Responses 请求(input[] + instructions + tools) → OpenAI Chat Completions 请求(messages[])。
//   解析层直接复用主链路的 ParseUnifiedOpenAIRequest / parseResponsesInput / parseResponsesTools，
//   它们已把 Responses 异构 input 拍平成 OpenAIMessage 列表，这里只负责字段对齐到本链路的 ChatMessage/ChatTool。
//
// 出站方向：NVIDIA 上游返回的是原生 OpenAI Chat 响应(非 Gemini)，不能复用主链路与 GeminiResponse 耦合的
//   Responses 输出，因此重写一份 OpenAIChat → Responses 的回译：
//   - 非流式：OpenAIChatResponse 聚合后产出 Responses 顶层对象，output[] 由 message 条目与 function_call 条目组成。
//   - 流式：逐行读上游 SSE data: chunk，重写成 Responses 事件序列
//     (response.created → in_progress → output_item.added → content_part.added → output_text.delta →
//      ...done 系列 → response.completed)，工具调用映射为 function_call 条目的事件流。
//
// 事件序列严格对齐 codex 客户端期望(参照主链路 compat.go 中 responses 分支的事件形态)。
// 配额/统计沿用 recordNvidiaUsage，usage 口径与 Chat 透传链路一致。

// ===== 入站：Responses → OpenAIChatRequest =====

// ResponsesToOpenAIChat 把 Responses API 请求体翻译成发往 NVIDIA 上游的 OpenAI Chat Completions 请求。
// upstreamModel 是经号池档位映射后的目标模型 id。
func ResponsesToOpenAIChat(bodyBytes []byte, upstreamModel string) (*OpenAIChatRequest, error) {
	uni, err := ParseUnifiedOpenAIRequest(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse responses body: %w", err)
	}

	out := &OpenAIChatRequest{
		Model:       upstreamModel,
		Stream:      uni.Stream,
		Temperature: uni.Temperature,
		MaxTokens:   uni.MaxTokens,
	}

	// messages：ParseUnifiedOpenAIRequest 已把 instructions→system、input[]→messages 拍平成 OpenAIMessage 列表。
	for _, m := range uni.Messages {
		cm := ChatMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			// function_call_output 在统一解析里落成 role=tool 消息
			cm.ToolCallID = m.ToolCallID
		}
		// assistant 的 tool_calls 转成 ChatToolCall（OpenAI Chat 规范）。
		// OpenAIToolCall.UnmarshalJSON 会把 function.name/name 互相同步，但保险起见优先取 Function.Name，
		// 为空回退 Name（兼容只有顶层 name 的非标准上游报文）。
		if len(m.ToolCalls) > 0 {
			cm.ToolCalls = make([]ChatToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				name := tc.Function.Name
				if name == "" {
					name = tc.Name
				}
				args := tc.Function.Arguments
				if args == "" {
					args = tc.Arguments
				}
				cm.ToolCalls = append(cm.ToolCalls, ChatToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: ChatToolCallFunction{
						Name:      name,
						Arguments: args,
					},
				})
			}
		}
		out.Messages = append(out.Messages, cm)
	}

	// tools：uni.Tools 是 AnthropicTool 结构（parseResponsesTools 已转好），统一映射成 ChatTool。
	if len(uni.Tools) > 0 {
		out.Tools = make([]ChatTool, 0, len(uni.Tools))
		for _, t := range uni.Tools {
			tool := ChatTool{Type: "function"}
			tool.Function.Name = t.Name
			tool.Function.Description = t.Description
			if t.InputSchema != nil {
				tool.Function.Parameters = t.InputSchema
			} else {
				tool.Function.Parameters = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
			out.Tools = append(out.Tools, tool)
		}
	}

	// 流式必须注入 stream_options.include_usage，否则上游不在 SSE 末帧吐 usage，统计拿不到 token。
	if out.Stream {
		out.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}

	return out, nil
}

// ===== 出站：OpenAIChat → Responses =====

// ResponsesResponse 是回译给 codex 的 Responses 顶层对象。
type ResponsesResponse struct {
	ID        string                 `json:"id"`
	Object    string                 `json:"object"` // 固定 "response"
	CreatedAt int64                  `json:"created_at"`
	Status    string                 `json:"status"` // 固定 "completed"
	Model     string                 `json:"model,omitempty"`
	Output    []ResponsesOutputItem  `json:"output"`
	Usage     ResponsesUsage         `json:"usage"`
}

// ResponsesOutputItem 是 Responses output[] 的统一条目。
// 文本条目用 Type="message" + Content；工具调用条目用 Type="function_call" + Name/CallID/Arguments。
type ResponsesOutputItem struct {
	Type      string                   `json:"type"`
	ID        string                   `json:"id,omitempty"`
	Role      string                   `json:"role,omitempty"`
	Status    string                   `json:"status,omitempty"`
	Content   []ResponsesContentPart   `json:"content,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

// ResponsesContentPart 是 message 条目里的内容块（类型 output_text）。
type ResponsesContentPart struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

// ResponsesUsage 是 Responses 用量字段（字段名与 Chat 不同：input/output/total_tokens）。
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// OpenAIChatToResponses 把 NVIDIA 上游非流式 OpenAI Chat 响应回译成 Responses 顶层对象。
func OpenAIChatToResponses(resp *OpenAIChatResponse, displayModel string) *ResponsesResponse {
	out := &ResponsesResponse{
		ID:        resp.ID,
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Model:     displayModel,
		Usage: ResponsesUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
		},
	}
	if out.ID == "" {
		out.ID = "resp_nvidia"
	}

	if len(resp.Choices) == 0 {
		// 上游无 choice：给一个空 message 兜底，避免 codex 收到空 output 报错
		out.Output = []ResponsesOutputItem{{
			Type:   "message",
			ID:     "msg_nvidia_0",
			Role:   "assistant",
			Status: "completed",
			Content: []ResponsesContentPart{{Type: "output_text", Text: ""}},
		}}
		return out
	}

	choice := resp.Choices[0]
	out.Output = openAIChoiceToResponsesItems(choice.Message, out.ID)

	return out
}

// openAIChoiceToResponsesItems 把一个 OpenAI Chat choice message 拆成 Responses output[] 条目。
// 文本→message 条目；tool_calls→每个一个 function_call 条目（顺序与上游一致）。
func openAIChoiceToResponsesItems(m ChatMessage, respID string) []ResponsesOutputItem {
	var items []ResponsesOutputItem

	// 文本条目（即使为空也保留，保持 message 结构完整）
	if strings.TrimSpace(m.Content) != "" || len(m.ToolCalls) == 0 {
		items = append(items, ResponsesOutputItem{
			Type:   "message",
			ID:     "msg_" + respID + "_0",
			Role:   "assistant",
			Status: "completed",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: m.Content,
			}},
		})
	}

	// 工具调用条目
	for i, tc := range m.ToolCalls {
		callID := tc.ID
		if callID == "" {
			callID = fmt.Sprintf("call_%s_%d", respID, i)
		}
		name := tc.Function.Name
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		items = append(items, ResponsesOutputItem{
			Type:      "function_call",
			ID:        fmt.Sprintf("fc_%s_%d", respID, i),
			CallID:    callID,
			Name:      name,
			Arguments: args,
			Status:    "completed",
		})
	}

	return items
}

// writeNvidiaResponsesNormal 处理非流式 Responses 入站：读全量上游 OpenAI Chat 响应 → 回译 → 写出。
func (h *APICompatHandler) writeNvidiaResponsesNormal(w http.ResponseWriter, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx nvidiaLogCtx) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
		return
	}
	if resp.StatusCode != http.StatusOK {
		// 上游非 200：包成 Responses 风格错误体透传
		h.log("⚠️ [NVIDIA Responses] 上游状态码 %d 非透传 | body: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeResponsesError(w, resp.StatusCode, bodyBytes)
		return
	}
	var chatResp OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "invalid openai response json: " + err.Error()})
		return
	}
	rr := OpenAIChatToResponses(&chatResp, model)
	payload, _ := json.Marshal(rr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)

	// 配额/统计回调，与 Chat 透传链路口径一致
	h.recordNvidiaUsage(userSession, model, rr.Usage.InputTokens, rr.Usage.OutputTokens, poolAccount, logCtx)
}

// writeNvidiaResponsesStream 处理流式 Responses 入站：上游 OpenAI Chat SSE → Responses SSE 事件序列。
func (h *APICompatHandler) writeNvidiaResponsesStream(w http.ResponseWriter, r *http.Request, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx nvidiaLogCtx) {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		h.log("⚠️ [NVIDIA Responses 流式] 上游状态码 %d 非透传 | body: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeResponsesError(w, resp.StatusCode, bodyBytes)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.log("⚠️ [NVIDIA Responses 流式] http.ResponseWriter 不支持 Flusher, 降级为仅 bufio flush")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush()
	}

	reqID := fmt.Sprintf("nv_resp_%d", time.Now().UnixNano())
	fw := newFlushWriter(reqID, bufio.NewWriter(w), flusher)
	// 透传 r.Context() 与 resp.Body：客户端取消时 watchCancel 立即 Close 上游,
	// scanner 退出后循环外既有 response.completed 尾帧自动补发(stop 语义)。
	in, out := OpenAIChatSSEToResponsesSSE(r.Context(), resp.Body, resp.Body, fw, model)
	fw.flush()

	h.recordNvidiaUsage(userSession, model, in, out, poolAccount, logCtx)
}

// OpenAIChatSSEToResponsesSSE 实时把 NVIDIA(OpenAI Chat 兼容)的 SSE 流重写成 Responses API 事件流。
// reader 读上游 SSE，fw 写 Responses SSE。返回累计 input/output tokens。
// 事件序列：response.created → response.in_progress →
//   (response.output_item.added → response.content_part.added → response.output_text.delta×N →
//    response.output_text.done → response.content_part.done → response.output_item.done)  // 文本
//   (response.output_item.added → response.function_call_arguments.delta×N →
//    response.function_call_arguments.done → response.output_item.done)  // 工具调用，按上游 tool_calls index
//   → response.completed(带 usage)。
//
// ctx 为入站请求的 r.Context()：客户端取消时 watchCancel Close 上游 body 让 scanner 退出,
// 循环外既有 response.completed 尾帧自动补发,body 为 nil 时退化兼容旧行为(不接入取消即断)。
func OpenAIChatSSEToResponsesSSE(ctx context.Context, reader io.Reader, body io.ReadCloser, fw *flushWriter, model string) (input, output int) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	// 客户端取消即断：ctx.Done() → Close 上游 body → scanner.Scan() 立即返回
	if ctx != nil && body != nil {
		stop := watchCancel(ctx, body)
		defer stop()
	}

	// streamID 与 createdAt 用于 created/in_progress/completed 的 id/created_at 字段
	streamID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	createdAt := time.Now().Unix()

	fw.writeEvent("response.created", responsesCreatedPayload(streamID, createdAt, model))
	fw.writeEvent("response.in_progress", responsesInProgressPayload(streamID, createdAt))

	// 文本块与工具块状态机
	textItem := &responsesStreamItem{kind: "text", id: "msg_" + streamID + "_0"}
	toolItems := map[int]*responsesStreamItem{}
	var fullText strings.Builder

	finishReason := ""

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk OpenAIChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 跳过无法解析的坏帧，不中断流
			continue
		}
		if chunk.Usage != nil {
			input = chunk.Usage.PromptTokens
			output = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]

		// 文本增量
		if strings.TrimSpace(ch.Delta.Content) != "" || ch.Delta.Content != "" {
			textItem.ensureOpened(fw, "output_text")
			fullText.WriteString(ch.Delta.Content)
			fw.writeEvent("response.output_text.delta", responsesOutputTextDeltaPayload(textItem.id, ch.Delta.Content))
		}

		// 工具调用增量：按上游 tool_calls[index] 维护独立条目
		for _, tc := range ch.Delta.ToolCalls {
			idx := tc.Index
			it, ok := toolItems[idx]
			if !ok {
				it = &responsesStreamItem{kind: "tool", index: idx}
				toolItems[idx] = it
			}
			// 首次出现该工具调用：补齐 function_call 元信息并发 output_item.added
			if !it.opened {
				if tc.ID != "" {
					it.callID = tc.ID
				} else {
					it.callID = fmt.Sprintf("call_%s_%d", streamID, idx)
				}
				it.id = fmt.Sprintf("fc_%s_%d", streamID, idx)
				if tc.Function.Name != "" {
					it.name = tc.Function.Name
				}
				it.opened = true
				fw.writeEvent("response.output_item.added", responsesFunctionCallItemAddedPayload(it))
			}
			// 名称若上游分多次给（极少），首帧后补
			if tc.Function.Name != "" && it.name == "" {
				it.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				it.argsBuf.WriteString(tc.Function.Arguments)
				fw.writeEvent("response.function_call_arguments.delta", responsesFunctionCallArgsDeltaPayload(it.id, tc.Function.Arguments))
			}
		}

		if ch.FinishReason != nil {
			if s := fmt.Sprintf("%v", ch.FinishReason); s != "" && s != "<nil>" {
				finishReason = s
			}
		}
	}

	// 收尾：文本块
	if textItem.opened {
		ft := fullText.String()
		fw.writeEvent("response.output_text.done", responsesOutputTextDonePayload(textItem.id, ft))
		fw.writeEvent("response.content_part.done", responsesContentPartDonePayload(textItem.id, ft))
		fw.writeEvent("response.output_item.done", responsesOutputItemDoneMessagePayload(textItem.id, ft))
	}

	// 收尾：工具调用块（按 index 升序）
	toolIndexes := make([]int, 0, len(toolItems))
	for k := range toolItems {
		toolIndexes = append(toolIndexes, k)
	}
	// 简单冒泡排序，index 不多且为整数
	for i := 0; i < len(toolIndexes); i++ {
		for j := i + 1; j < len(toolIndexes); j++ {
			if toolIndexes[i] > toolIndexes[j] {
				toolIndexes[i], toolIndexes[j] = toolIndexes[j], toolIndexes[i]
			}
		}
	}
	for _, idx := range toolIndexes {
		it := toolItems[idx]
		args := it.argsBuf.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		fw.writeEvent("response.function_call_arguments.done", responsesFunctionCallArgsDonePayload(it.id, args))
		fw.writeEvent("response.output_item.done", responsesFunctionCallItemDonePayload(it, args))
	}

	// 末帧 response.completed
	stopReason := "max_output_tokens"
	switch finishReason {
	case "stop", "":
		stopReason = "stop"
	case "tool_calls", "function_call":
		stopReason = "tool_calls"
	case "length":
		stopReason = "max_output_tokens"
	}
	fw.writeEvent("response.completed", responsesCompletedPayload(streamID, createdAt, model, stopReason, input, output))

	return input, output
}

// responsesStreamItem 记录流中一个 output 条目的打开状态与累积内容。
type responsesStreamItem struct {
	kind     string // "text" | "tool"
	index    int
	id       string
	callID   string
	name     string
	opened   bool
	argsBuf  strings.Builder
}

// ensureOpened 在首个文本增量前补发 output_item.added + content_part.added（part 类型 output_text）。
func (it *responsesStreamItem) ensureOpened(fw *flushWriter, partType string) {
	if it.opened {
		return
	}
	it.opened = true
	fw.writeEvent("response.output_item.added", responsesMessageItemAddedPayload(it.id))
	fw.writeEvent("response.content_part.added", responsesContentPartAddedPayload(it.id, partType))
}

// ===== Responses SSE payload 构造器 =====

func responsesCreatedPayload(id string, createdAt int64, model string) string {
	resp := map[string]interface{}{
		"id":         id,
		"object":     "response",
		"created_at": createdAt,
		"status":     "in_progress",
		"model":      model,
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.created",
		"sequence_number": 0,
		"response":        resp,
	})
	return string(payload)
}

func responsesInProgressPayload(id string, createdAt int64) string {
	resp := map[string]interface{}{
		"id":         id,
		"object":     "response",
		"created_at": createdAt,
		"status":     "in_progress",
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.in_progress",
		"sequence_number": 1,
		"response":        resp,
	})
	return string(payload)
}

// responsesMessageItemAddedPayload 发文本 message 条目的 output_item.added。
func responsesMessageItemAddedPayload(itemID string) string {
	item := map[string]interface{}{
		"id":      itemID,
		"type":    "message",
		"status":  "in_progress",
		"role":    "assistant",
		"content": []interface{}{},
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_item.added",
		"sequence_number": 0,
		"output_index":    0,
		"item":            item,
	})
	return string(payload)
}

// responsesContentPartAddedPayload 发 content_part.added，part.type 通常为 output_text。
func responsesContentPartAddedPayload(itemID, partType string) string {
	part := map[string]interface{}{"type": partType, "text": ""}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.content_part.added",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"part":            part,
	})
	return string(payload)
}

func responsesOutputTextDeltaPayload(itemID, delta string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_text.delta",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"delta":           delta,
	})
	return string(payload)
}

func responsesOutputTextDonePayload(itemID, text string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_text.done",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"text":            text,
	})
	return string(payload)
}

func responsesContentPartDonePayload(itemID, text string) string {
	part := map[string]interface{}{"type": "output_text", "text": text}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.content_part.done",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"part":            part,
	})
	return string(payload)
}

func responsesOutputItemDoneMessagePayload(itemID, text string) string {
	item := map[string]interface{}{
		"id":      itemID,
		"type":    "message",
		"status":  "completed",
		"role":    "assistant",
		"content": []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_item.done",
		"sequence_number": 0,
		"output_index":    0,
		"item":            item,
	})
	return string(payload)
}

// responsesFunctionCallItemAddedPayload 发 function_call 条目的 output_item.added。
func responsesFunctionCallItemAddedPayload(it *responsesStreamItem) string {
	item := map[string]interface{}{
		"id":        it.id,
		"type":      "function_call",
		"status":    "in_progress",
		"call_id":   it.callID,
		"name":      it.name,
		"arguments": "",
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_item.added",
		"sequence_number": 0,
		"output_index":    it.index,
		"item":            item,
	})
	return string(payload)
}

func responsesFunctionCallArgsDeltaPayload(itemID, delta string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.function_call_arguments.delta",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"delta":           delta,
	})
	return string(payload)
}

func responsesFunctionCallArgsDonePayload(itemID, args string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.function_call_arguments.done",
		"sequence_number": 0,
		"item_id":         itemID,
		"output_index":    0,
		"arguments":       args,
	})
	return string(payload)
}

func responsesFunctionCallItemDonePayload(it *responsesStreamItem, args string) string {
	item := map[string]interface{}{
		"id":        it.id,
		"type":      "function_call",
		"status":    "completed",
		"call_id":   it.callID,
		"name":      it.name,
		"arguments": args,
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.output_item.done",
		"sequence_number": 0,
		"output_index":    it.index,
		"item":            item,
	})
	return string(payload)
}

func responsesCompletedPayload(id string, createdAt int64, model, stopReason string, inTokens, outTokens int) string {
	resp := map[string]interface{}{
		"id":         id,
		"object":     "response",
		"created_at": createdAt,
		"status":     "completed",
		"model":      model,
		"output":     []interface{}{},
		"usage": map[string]interface{}{
			"input_tokens":  inTokens,
			"output_tokens": outTokens,
			"total_tokens":  inTokens + outTokens,
		},
	}
	if stopReason != "" {
		resp["stop_reason"] = stopReason
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":            "response.completed",
		"sequence_number": 0,
		"response":        resp,
	})
	return string(payload)
}

// writeResponsesError 把上游错误体包成 Responses 风格的 JSON 错误并写回。
// 上游错误体若是合法 JSON（如 {"error":{...}}）则原样透传；否则包成 message 字符串。
func writeResponsesError(w http.ResponseWriter, statusCode int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	// 若已是合法 JSON 对象，原样透传，保留上游结构
	trimmed := strings.TrimSpace(string(body))
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid(body) {
		_, _ = w.Write(body)
		return
	}
	// 兜底包成 Responses 错误体
	errObj := map[string]interface{}{
		"error": map[string]interface{}{
			"message": string(body),
		},
	}
	payload, _ := json.Marshal(errObj)
	_, _ = w.Write(payload)
}
