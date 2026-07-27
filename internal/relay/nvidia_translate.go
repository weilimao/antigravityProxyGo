package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
)

// nvidia_translate.go 实现 Anthropic Messages ↔ OpenAI Chat Completions 的双向协议转换。
// 这是给 NVIDIA NIM(及任意 OpenAI Chat 兼容上游)专用的转换层，
// 与现有 compat_translate.go 的「客户端协议 → Gemini」单向转换并存但不耦合。
//
// 参考实现：cc-switch 的 transform.rs::anthropic_to_openai_with_reasoning_content /
// openai_to_anthropic 及 streaming.rs::create_anthropic_sse_stream。

// ===== 请求方向：Anthropic Messages → OpenAI Chat Completions =====

// AnthropicToOpenAIChat 把 Anthropic Messages 请求翻译成 OpenAI Chat Completions 请求。
// 字段映射要点：
//   - system(字符串/已由 AnthropicRequest.UnmarshalJSON 规整) → messages[0]{role:system}
//   - 消息 content blocks: text→content 字符串；tool_use→tool_calls；tool_result→role=tool
//   - tools: AnthropicTool{name,input_schema} → OpenAI tools{type:function,function:{name,description,parameters}}
//   - max_tokens / temperature / stream 透传
func AnthropicToOpenAIChat(req *AnthropicRequest) (*OpenAIChatRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("nvidia: nil anthropic request")
	}

	out := &OpenAIChatRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	// system → messages[0]
	if strings.TrimSpace(req.System) != "" {
		out.Messages = append(out.Messages, ChatMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	// 逐条消息转换
	for _, msg := range req.Messages {
		switch msg.Role {
		case "assistant":
			out.Messages = append(out.Messages, anthropicAssistantToChat(msg))
		case "user":
			out.Messages = append(out.Messages, anthropicUserToChat(msg)...)
		default:
			// 其它角色按 user 处理
			out.Messages = append(out.Messages, anthropicUserToChat(msg)...)
		}
	}

	// tools
	if len(req.Tools) > 0 {
		out.Tools = make([]ChatTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			tool := ChatTool{Type: "function"}
			tool.Function.Description = t.Description
			tool.Function.Name = t.Name
			if t.InputSchema != nil {
				tool.Function.Parameters = t.InputSchema
			} else {
				tool.Function.Parameters = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
			out.Tools = append(out.Tools, tool)
		}
	}

	// tool_choice：Anthropic 的 tool_choice 转换为 OpenAI 规范
	// 在 OpenAI / NVIDIA 上游中，tool_choice 必须是字符串 "auto"/"none"/"required" 或 {"type":"function","function":{"name":"..."}}
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		var choice map[string]interface{}
		if err := json.Unmarshal(req.ToolChoice, &choice); err == nil {
			if t, ok := choice["type"].(string); ok {
				switch t {
				case "auto", "none":
					out.ToolChoice = t
				case "any":
					out.ToolChoice = "required"
				case "tool":
					if name, ok := choice["name"].(string); ok {
						out.ToolChoice = map[string]interface{}{
							"type":     "function",
							"function": map[string]interface{}{"name": name},
						}
					}
				}
			}
		}
	}

	// 流式必须注入 stream_options.include_usage，否则上游不在 SSE 末尾吐 usage。
	if out.Stream {
		out.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}

	return out, nil
}

// anthropicAssistantToChat 把 Anthropic assistant 消息转成 OpenAI assistant 消息。
// assistant 的 content 中可能混合 text 与 tool_use 块：text→content 字符串，tool_use→tool_calls。
func anthropicAssistantToChat(msg AnthropicMessage) ChatMessage {
	var sb strings.Builder
	var toolCalls []ChatToolCall
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   b.ID,
				Type: "function",
				Function: ChatToolCallFunction{
					Name:      b.Name,
					Arguments: string(args),
				},
			})
		}
	}
	return ChatMessage{
		Role:      "assistant",
		Content:   sb.String(),
		ToolCalls: toolCalls,
	}
}

// anthropicUserToChat 把 Anthropic user 消息转成 OpenAI messages。
// user content 中若包含 tool_result 块，需要单独拆成 role=tool 的消息（OpenAI 规定 tool 结果只能单独成条）。
// 其余 text 块合并进一条 user 消息。
func anthropicUserToChat(msg AnthropicMessage) []ChatMessage {
	var toolResults []ChatMessage
	var sb strings.Builder
	hasText := false
	for _, b := range msg.Content {
		switch b.Type {
		case "tool_result":
			content := flattenToolResultContent(b.ToolResultContent)
			toolResults = append(toolResults, ChatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: b.ToolUseID,
				ToolName:   b.Name,
			})
		case "text":
			sb.WriteString(b.Text)
			hasText = true
		default:
			// 其它类型(如 image)暂按 text 提取，避免丢字段
			sb.WriteString(b.Text)
			hasText = true
		}
	}
	// 先放 tool 结果，再放普通文本；顺序与 OpenAI 期待一致
	res := toolResults
	if hasText {
		res = append(res, ChatMessage{Role: "user", Content: sb.String()})
	}
	if len(res) == 0 {
		// 整条 user 消息无可见内容，补一个空 user 占位以免上游 400
		res = append(res, ChatMessage{Role: "user", Content: ""})
	}
	return res
}

// flattenToolResultContent 把 Anthropic tool_result 的 content(string 或 []block)拍平成纯字符串。
func flattenToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var blocks []AnthropicContent
		if err := json.Unmarshal(raw, &blocks); err == nil {
			var sb strings.Builder
			for _, b := range blocks {
				if b.Type == "text" {
					sb.WriteString(b.Text)
				}
			}
			return sb.String()
		}
	}
	return string(raw)
}

// ===== 响应方向：OpenAI Chat → Anthropic Messages =====

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
// 返回值：先行 text block（若有），其后跟 tool_use block 列表。
func openAIChoiceMessageToAnthropic(m ChatMessage) ([]AnthropicContent, []AnthropicContent) {
	var texts []AnthropicContent
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

// ===== 流式：OpenAI Chat SSE → Anthropic SSE =====

// OpenAIChatSSEToAnthropicSSE 实时把 NVIDIA(OpenAI Chat 兼容)的 SSE 流重写成 Anthropic Messages SSE 事件流。
// reader 读上游 SSE，writer 写 Anthropic SSE。返回累计 input/output tokens。
// 协议事件序列：message_start → (content_block_start/delta/stop × N) → message_delta(usage) → message_stop。
//
// ctx 为入站请求的 r.Context()：客户端主动取消时 ctx 被撤销，watchCancel 立即 Close 上游 body,
// 使阻塞的 scanner.Scan() 退出读循环，随后在循环外统一补发 message_delta + message_stop 尾帧。
// body 为上游响应体(用于 ctx 取消时主动 Close 触发 scanner 退出);body 为 nil 时仅退化兼容旧行为,
// 不接入"取消即断"(留给调用方保证非空)。
//
// 本函数为薄委托:将 sink 具体化为 flushWriter(写往 *bufio.Writer + 可选 http.Flusher),
// 真正的转译逻辑在 openAIChatSSEToAnthropicSSEInto(接收 sseEventSink)。蓄流回放重试链路
// 直接调 openAIChatSSEToAnthropicSSEInto 传 replayWriter,本函数签名保持不变,所有旧调用零改动。
func OpenAIChatSSEToAnthropicSSE(ctx context.Context, reader io.Reader, body io.ReadCloser, writer *bufio.Writer, model string, flusher ...http.Flusher) (input, output int, err error) {
	streamID := fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	fw := newFlushWriter(streamID, writer, flusher...)
	input, output, _, _, err = openAIChatSSEToAnthropicSSEInto(ctx, reader, body, fw, streamID, model)
	return input, output, err
}

// openAIChatSSEToAnthropicSSEInto 把上游 OpenAI Chat SSE 翻译成 Anthropic SSE,写到 sink(flushWriter 或 replayWriter)。
// 返回 (input, output, finishEmitted, streamTerminated, err):
//   - finishEmitted:是否收到上游合法 finish_reason 帧并已 close 所有 block(确定性"本轮正文收尾"信号);
//   - streamTerminated:上游流是否以正常协议终止符结束 —— 收到 [DONE] 或读到正常 EOF(无扫描错误)。
//     这一路径的完整性兜底:NIM 等上游存在"不发 finish_reason 帧,仅发 usage 帧后跟 [DONE]"的合法收尾形态,
//     仅靠 finishEmitted 会把它误判为不完整断流。streamTerminated==true 表示上游流是"协议级结束"而非断流,
//     重试主体据此判定可回放。
//   - err:上游 SSE 内嵌 error chunk 或 scanner.Err()(非 ctx 取消)的错误,供上层日志/重试判定。
//
// 完整性判定(重试主体使用):finishEmitted || (streamTerminated && err==nil) 视为完整可回放。
// 真·断流(unexpected EOF 在 [DONE] 之前打断)会使 streamTerminated=false 且 err!=nil,两条件均不满足 → 重试。
//
// 转译逻辑与原 OpenAIChatSSEToAnthropicSSE 逐行等价,仅参数化 sink 与 streamID,并新增 streamTerminated 信号。
func openAIChatSSEToAnthropicSSEInto(ctx context.Context, reader io.Reader, body io.ReadCloser, sink sseEventSink, streamID, model string) (input, output int, finishEmitted, streamTerminated bool, err error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// 客户端取消即断：ctx.Done() → Close 上游 body → scanner.Scan() 立即返回
	if ctx != nil && body != nil {
		stop := watchCancel(ctx, body)
		defer stop()
	}

	// message_start
	sink.writeEvent("message_start", messageStartPayload(streamID, model))

	blockStates := &sseBlockStates{blocks: map[int]*sseBlock{}}
	stopReason := ""

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
			streamTerminated = true // OpenAI 协议权威终止符:流正常结束,非断流。
			break
		}

		type sseErrorChunk struct {
			Error *struct {
				Message string      `json:"message"`
				Type    string      `json:"type"`
				Code    interface{} `json:"code"`
			} `json:"error"`
		}
		var errChunk sseErrorChunk
		if json.Unmarshal([]byte(data), &errChunk) == nil && errChunk.Error != nil && errChunk.Error.Message != "" {
			errMsg := fmt.Sprintf("upstream sse error: %s (code: %v)", errChunk.Error.Message, errChunk.Error.Code)
			// 保住 err 供上层(writeNvidiaAnthropicStream 忽略返回值,但 watchCancel/日志可取)
			// 仅在尚未被 ctx 取消语义占据时记录上游 error,避免覆盖既有 ctx 取消路径的语义。
			if err == nil {
				err = fmt.Errorf("%s", errMsg)
			}
			if !blockStates.hasEmittedAnyBlock() {
				// 历史缺口:此处曾直接 return,跳过循环外统一尾帧补发,
				// 导致 CLI 仅收到 message_start 而无 message_stop → 卡等尾帧、
				// 表现为"断了不干活"。现改为保底发一个文本块并 break,让控制流落到循环外
				// 统一补 message_delta + message_stop,产出完整闭合的 SSE 流(空本轮)。
				// 取舍:CLI 视为本轮正常结束(end_turn),不卡等、不触发重试风暴;
				// 上游 error 原文已保存在 err 并由代理日志记录,便于事后排查。
				blockStates.ensureAtLeastOneBlock(sink)
			}
			break
		}

		var chunk OpenAIChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 跳过无法解析的行，但不中断流
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
		if ch.Delta.Content != "" {
			blockStates.emitTextDelta(ch.Delta.Content, sink)
		} else if ch.Delta.ReasoningContent != "" {
			blockStates.emitTextDelta(ch.Delta.ReasoningContent, sink)
		}
		for _, tc := range ch.Delta.ToolCalls {
			blockStates.emitToolCallDelta(tc, sink)
		}
		if ch.FinishReason != nil && ch.FinishReason != "" {
			blockStates.ensureAtLeastOneBlock(sink)
			blockStates.closeAll(sink)
			stopReason = blockStates.determineStopReason(fmt.Sprintf("%v", ch.FinishReason))
			finishEmitted = true
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && scanErr != io.EOF {
		// ctx 取消触发的 body.Close() 会让 Scan() 返回 "read on closed *" 类错误,
		// 这属于"客户端主动取消"的正常收尾路径,不应作为 err 上抛(避免调用方误判为上游故障),
		// 走尾帧补发即可。
		if ctx == nil || ctx.Err() == nil {
			err = scanErr
		}
	} else if err == nil {
		// 循环无扫描错误且 err 仍 nil(未被上游 SSE error chunk 等主动 break 路径污染):
		// 上游流以正常 EOF 关闭,协议级结束,非断流。[DONE] 分支已提前置位;此处覆盖"上游未发 [DONE]
		// 即 EOF 关闭"的合法收尾形态。err!=nil(如 error chunk break)则保持 streamTerminated=false,
		// 让重试主体据此判定为不完整 → 重试,符合上游报错应重试的语义。
		streamTerminated = true
	}
	// message_delta 必须在循环结束后发出：Anthropic 官方要求 message_delta.usage 的 token
	// 计数为累计值 (cumulative)，而上游 NIM 的 usage 帧 ({"choices":[],"usage":{...}}) 在
	// finish_reason 帧之后、[DONE] 之前才送达。若在 finish_reason 帧时立即发 message_delta，
	// input/output 仍为 0，会导致 Claude Code SDK 的 MessageAccumulator 误判流未正常结束，
	// 触发"等连接关闭/下次请求才整条渲染"的退化路径。此处统一在循环外、usage 帧已落地后发出。
	if !finishEmitted {
		blockStates.ensureAtLeastOneBlock(sink)
		blockStates.closeAll(sink)
		stopReason = blockStates.determineStopReason("")
	}
	// 客户端主动取消(ctx 被撤销)且上游未给出 finish_reason:补 end_turn 语义尾帧,
	// 让 Claude Code SDK 的 MessageAccumulator 视为"本轮正常结束",不触发失败重试、不卡等尾帧。
	if ctx != nil && ctx.Err() != nil && stopReason == "" {
		stopReason = "end_turn"
	}
	sink.writeEvent("message_delta", messageDeltaPayload(stopReason, input, output))
	sink.writeEvent("message_stop", `{"type":"message_stop"}`)
	sink.flush()
	return input, output, finishEmitted, streamTerminated, err
}

// messageStartPayload 生成 message_start 事件的 data 负载。
// 严格对齐 Anthropic 官方流式协议：顶层 type 必须是 "message_start"，且包含嵌套的 message 对象，
// 否则 VS Code Claude 扩展 SDK 的 MessageAccumulator 解析不到 message.id / message.content，
// 会报 "Message not found" 并降级为非流式模式——这正是 NVIDIA 中继流式持久失败而 antigravity(Gemini)
// 链正常工作的根因差异所在（antigravity 链 compat.go:870-882 已按正确嵌套实现）。
func messageStartPayload(streamID, model string) string {
	if model == "" {
		model = "nvidia"
	}
	if streamID == "" {
		streamID = fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            streamID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 1},
		},
	})
	return string(payload)
}

// messageDeltaPayload 生成 message_delta 事件的 data 负载。
// 对齐 Anthropic 官方流式协议：usage 字段的 token 计数为累计值(cumulative)，
// 官方明确标注 "The token counts shown in the `usage` field of the `message_delta`
// event are *cumulative*"，故 output_tokens 必须填本次流的真实累计输出 token 数，
// input_tokens 填真实累计输入 token 数。早期的硬编码 {"output_tokens":0} 会让部分
// Claude Code SDK 的 MessageAccumulator 误判流未正常结束，触发"等连接关闭/下次请求
// 才整条渲染"的退化路径。
func messageDeltaPayload(stopReason string, inputTokens, outputTokens int) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason": stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	})
	return string(payload)
}

// ===== SSE 流式辅助状态机 =====

// sseBlock 记录当前打开的内容块(文本或工具调用)在 Anthropic 流中的索引与身份。
type sseBlock struct {
	index       int
	kind        string // "text" | "tool_use"
	toolID      string
	toolName    string
	textStarted bool
	toolStarted bool
}

type sseBlockStates struct {
	mu          sync.Mutex
	blocks      map[int]*sseBlock
	next        int
	textEmitted bool
	hasToolCall bool
}

// emitTextDelta 把一条 OpenAI 文本增量转成 Anthropic content_block_delta(text_delta)。
func (s *sseBlockStates) emitTextDelta(text string, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 文本块在 Anthropic 里通常用 index 0；此处维护单一文本块
	b, ok := s.blocks[0]
	if !ok {
		b = &sseBlock{index: 0, kind: "text"}
		s.blocks[0] = b
	}
	if !b.textStarted {
		b.textStarted = true
		s.textEmitted = true
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "text", "", ""))
	}
	fw.writeEvent("content_block_delta", contentBlockTextDeltaPayload(b.index, text))
}

// emitToolCallDelta 处理 OpenAI tool_calls 增量(index 指向上游分块的工具调用编号)，
// 映射成 Anthropic 的 content_block_start(tool_use) + content_block_delta(input_json_delta)。
func (s *sseBlockStates) emitToolCallDelta(tc ChatToolCall, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasToolCall = true
	idx := tc.Index
	base := 0
	if s.textEmitted {
		base = 1
	}
	key := base + idx
	b, ok := s.blocks[key]
	if !ok {
		b = &sseBlock{index: key, kind: "tool_use", toolID: tc.ID, toolName: tc.Function.Name}
		s.blocks[key] = b
	}
	if !b.toolStarted {
		b.toolStarted = true
		if b.toolID == "" {
			b.toolID = fmt.Sprintf("toolu_nvidia_%d", idx)
		}
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "tool_use", b.toolID, b.toolName))
	}
	// OpenAI 流式 tool_calls 的 arguments 是增量字符串，Anthropic 用 input_json_delta 直传
	if tc.Function.Arguments != "" {
		fw.writeEvent("content_block_delta", contentBlockInputJSONDeltaPayload(b.index, tc.Function.Arguments))
	}
}

// closeAll 关闭所有已打开的文本/工具块，发出 content_block_stop。
func (s *sseBlockStates) closeAll(fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]int, 0, len(s.blocks))
	for k := range s.blocks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		b := s.blocks[k]
		if b.textStarted || b.toolStarted {
			fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
		}
		b.textStarted = false
		b.toolStarted = false
	}
}

// hasEmittedAnyBlock 检查本轮会话是否已发出过至少一个 content_block
func (s *sseBlockStates) hasEmittedAnyBlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocks) > 0
}

// ensureAtLeastOneBlock 确保流结束前至少发出一个 content_block(若零 Block 则保底发空文本块)
func (s *sseBlockStates) ensureAtLeastOneBlock(fw sseEventSink) {
	if !s.hasEmittedAnyBlock() {
		s.emitTextDelta("", fw)
	}
}

// determineStopReason 根据本轮是否发出过工具块及上游 finishReason 精准计算 Anthropic stop_reason。
// 若包含工具调用，必定返回 "tool_use"，确保 Claude Code 等 Agent 客户端能自动驱动后续工具执行。
func (s *sseBlockStates) determineStopReason(rawFinishReason string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasToolCall {
		return "tool_use"
	}
	switch rawFinishReason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// watchCancel 监听 ctx 取消：一旦 ctx 被撤销(客户端主动断开 / 请求超时),
// 立即 Close 上游 resp.Body,使阻塞在 bufio.Scanner.Scan() 上的读循环以
// "read on closed body" 错误立即返回,从而跳出逐帧回写的主循环。
//
// 这是 NVIDIA 流式链路"取消即断"的唯一可靠触发点 —— 不依赖下游写错检测
// (存在竞态:客户端断开时若 scanner 正好在两帧之间阻塞读,写错不会触发)。
// ctx.Done() 由 net/http 在客户端 TCP 半关闭时确定性触发,无竞态。
//
// 对齐谷歌链路 handler.go:1131-1140 的 cancelChan 监听协程,但抽成可复用 helper,
// 供 NVIDIA 三条流式回写路径(Anthropic / Responses / OpenAI 透传)统一接入。
//
// 返回 stop 函数:defer 调用以释放监听 goroutine,避免泄漏。
//   body 为上游响应体;调用方负责保证只在 ctx 取消时由本 helper 触发 Close,
//   正常流式读完时 scanner 先返回 EOF,主循环退出后 defer stop() 释放 goroutine,
//   body 的最终 Close 仍由各路径既有 defer resp.Body.Close() 负责。
func watchCancel(ctx context.Context, body io.ReadCloser) (stop func()) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// 客户端已断开:主动切断上游连接,让阻塞的 Scan() 立即返回
			_ = body.Close()
		case <-stopped:
			// 正常收尾:主循环已退出,无需 Close(由既有的 defer resp.Body.Close() 兜底)
		}
		close(done)
	}()
	return func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
		<-done
	}
}

// ===== sseEventSink / flushwriter / replaywriter =====

// sseEventSink 抽象 SSE 事件写入目标。两类实现:
//   - flushWriter:边读上游边把 Anthropic SSE 事件逐帧 flush 到客户端 TCP socket(实时流式);
//   - replayWriter:把整条转译结果蓄流进内存 bytes.Buffer,供上游断流重试场景攒全量再回放。
//
// 抽象该接口使 OpenAIChatSSEToAnthropicSSE 的转译逻辑与"写往哪里"解耦:
// 蓄流回放链路(writeNvidiaAnthropicStream)先用 replayWriter 在内存攒出完整 Anthropic SSE,
// 断流可丢弃本次 buffer 原账号重拉上游(≤5×5s),整条 ready 后再把 buffer 逐帧 flush 给客户端,
// 客户端在重试期间未收到任何字节,不会出现"半截内容冲突"。
type sseEventSink interface {
	writeEvent(event, data string)
	writeRaw(s string)
	flush()
}

type flushWriter struct {
	w       *bufio.Writer
	flusher http.Flusher
	reqID   string
	mu      sync.Mutex
}

// newFlushWriter 创建 flushWriter。若 flusher 非 nil, writeEvent/writeRaw/flush 会在 bufio.Flush
// 之后调 http.Flusher.Flush(), 把字节真正推到 TCP socket, 实现逐帧实时递送给客户端。
func newFlushWriter(reqID string, w *bufio.Writer, flusher ...http.Flusher) *flushWriter {
	fw := &flushWriter{w: w, reqID: reqID}
	if len(flusher) > 0 {
		fw.flusher = flusher[0]
	}
	return fw
}

func (f *flushWriter) writeEvent(event, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w.WriteString("event: " + event + "\n")
	f.w.WriteString("data: " + data + "\n\n")
	f.w.Flush() // 出 bufio 内部缓冲 → http.ResponseWriter
	if f.flusher != nil {
		f.flusher.Flush() // 出 http.ResponseWriter → socket
	}
}

func (f *flushWriter) writeRaw(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w.WriteString(s)
	f.w.Flush()
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

func (f *flushWriter) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w.Flush()
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

// replayWriter 是 sseEventSink 的蓄流实现:把 Anthropic SSE 事件按帧原样写进内存 bytes.Buffer,
// 不接触任何 socket。供上游断流重试链路(writeNvidiaAnthropicStream)在整条上游 SSE
// 攒齐之前先把转译结果持留在 buffer,断流可丢弃本次 buffer 重拉上游,ready 后再回放给客户端。
//
// 写入格式与 flushWriter.writeEvent 完全一致(event:/data:/空行),保证回放时客户端拿到的
// SSE 字节流与"边读边写"链路逐字节等价,行为零差异。
//
// 帧与帧之间不复用 bufio,直接写 bytes.Buffer;所有写操作加锁,防止并发乱序
// (虽然当前转译链路单协程顺序写,加锁为防御性,与 flushWriter 对齐)。
type replayWriter struct {
	mu sync.Mutex
	buf bytes.Buffer
}

func newReplayWriter() *replayWriter {
	return &replayWriter{}
}

// writeEvent 写一帧 Anthropic SSE: event: <name>\n data: <data>\n\n。
func (r *replayWriter) writeEvent(event, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.WriteString("event: " + event + "\n")
	r.buf.WriteString("data: " + data + "\n\n")
}

// writeRaw 写原始 SSE 字节(如末尾 data: [DONE]\n\n 兼容 OpenAI 透传语义),原样直灌 buffer。
func (r *replayWriter) writeRaw(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.WriteString(s)
}

// flush 在蓄流语义下为空操作:真正的 flush 发生在回放给 flushWriter 那一刻,
// 这里保留方法以满足 sseEventSink 接口契约。
func (r *replayWriter) flush() {}

// bytes 返回已蓄流的完整 SSE 字节切片(只读视图),供回放层逐帧 flush 给客户端。
func (r *replayWriter) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Bytes()
}

// len 返回已蓄流字节数,供上层做超大流保护判定(超过阈值则退回边读边写,避免无界内存)。
func (r *replayWriter) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Len()
}

// ===== Anthropic SSE payload 构造 =====

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
	b, _ := json.Marshal(m)
	return string(b)
}

func contentBlockTextDeltaPayload(index int, text string) string {
	m := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func contentBlockInputJSONDeltaPayload(index int, partialJSON string) string {
	m := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": partialJSON},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func contentBlockStopPayload(index int) string {
	m := map[string]interface{}{"type": "content_block_stop", "index": index}
	b, _ := json.Marshal(m)
	return string(b)
}

// ===== OpenAI Chat 兼容请求/响应类型(独立于 compat_translate.go 的 OpenAIRequest)=====
// 现有 OpenAIRequest 是给 Gemini 链路用的(无 messages 内 tool_calls 序列化细节)，这里为了
// 保证 NVIDIA 上游收到的 OpenAI Chat JSON 严格符合规范，单独定义一套 chat 类型。

// ChatMessage 是发给 NVIDIA 上游的 OpenAI Chat messages 元素。
//
// 注意:Content 字段刻意不使用 omitempty。
// 原因:NVIDIA(及多数 OpenAI 兼容)上游用 serde 反序列化,要求每条 message 显式带 content 字段;
// 若空串 "" 被 omitempty 省略,上游会回 400 "Failed to deserialize the JSON body into
// the target type: missing field `content`"。因此空内容必须序列化为 "content":"" 落盘,
// 这对 assistant(纯 tool_use 无文本)与 tool(空 tool_result)角色尤其关键。
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
}

type ChatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatToolCall struct {
	Index    int                  `json:"index,omitempty"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type"`
	Function ChatToolCallFunction `json:"function"`
}

type ChatTool struct {
	Type      string         `json:"type"`
	Function  ChatToolFunc   `json:"function"`
}

type ChatToolFunc struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ChatToolChoice struct {
	Type     string              `json:"type,omitempty"`
	Function ChatToolChoiceFunc  `json:"function,omitempty"`
}

type ChatToolChoiceFunc struct {
	Name string `json:"name"`
}

type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// OpenAIChatRequest 是发给 NVIDIA 上游的 OpenAI Chat Completions 请求体。
type OpenAIChatRequest struct {
	Model        string          `json:"model"`
	Messages     []ChatMessage   `json:"messages"`
	Temperature  *float64        `json:"temperature,omitempty"`
	MaxTokens    *int            `json:"max_tokens,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Tools         []ChatTool         `json:"tools,omitempty"`
	ToolChoice    interface{}        `json:"tool_choice,omitempty"`
	StreamOptions *ChatStreamOptions `json:"stream_options,omitempty"`
}

// OpenAIChatResponse 是 NVIDIA 上游返回的 OpenAI Chat Completions 非流式响应。
type OpenAIChatResponse struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []OpenAIChatChoice      `json:"choices"`
	Usage   OpenAIChatUsage         `json:"usage"`
}

type OpenAIChatChoice struct {
	Index        int        `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type OpenAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (r *OpenAIChatResponse) FinishReason() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
}

// OpenAIChatStreamChunk 是 NVIDIA 上游的流式 chunk。
type OpenAIChatStreamChunk struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []OpenAIChatStreamChoice `json:"choices"`
	Usage   *OpenAIChatUsage        `json:"usage,omitempty"`
}

type OpenAIChatStreamChoice struct {
	Index        int                     `json:"index"`
	Delta        OpenAIChatDelta         `json:"delta"`
	FinishReason interface{}             `json:"finish_reason"`
}

type OpenAIChatDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
}

// mapNvidiaModel 按账号配置把入站模型名映射成上游模型 id。
func mapNvidiaModel(inModel string, acc *account.Account) string {
	return account.ResolveNvidiaModel(inModel, acc)
}
