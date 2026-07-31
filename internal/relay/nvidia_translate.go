package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

	// 思考等级透传:把客户端(Claude Code Anthropic 协议)的思考配置 resolve 为等级,
	// 再按 NIM 上游取值模式映射,注入 chat_template_kwargs:{thinking:true, reasoning_effort:<mapped>}。
	// 客户端不发思考配置时 effort 为空 → 不注入 → 上游行为与改动前一致(回归安全)。
	// 仅对支持思考的 NIM 推理模型注入,避免往不支持 chat_template_kwargs 的上游误塞引发 400。
	if !IsEnableThinkingMode() {
		out.ChatTemplateKwargs = nil
	} else if effort := resolveReasoningEffort(req); effort != "" {
		mode := nvidiaThinkingEffortMode(req.Model)
		if mapped := mapReasoningEffort(effort, mode); mapped != "" {
			out.ChatTemplateKwargs = map[string]interface{}{
				"thinking":         true,
				"reasoning_effort": mapped,
			}
		}
	} else if !isThinkingExplicitlyDisabled(req) && nvidiaModelSupportsThinking(req.Model) {
		// 客户端未表达思考强度(且未显式关闭),但模型本身是推理型:仅开 thinking 不设等级,
		// 让上游按默认档出思考(NIM 推理模型默认行为),避免思考被误关。
		// 注意:客户端显式 disabled 时绝不 fallback,尊重关闭意图。
		out.ChatTemplateKwargs = map[string]interface{}{"thinking": true}
	}

	return out, nil
}

// resolveReasoningEffort 从 Anthropic 请求体识别客户端想要的思考等级,
// 返回规范化内部值 "low"/"medium"/"high"/"max"(max 即 cc-switch 的 xhigh,NIM 直用 max)。
// 空串表示客户端未表达思考或显式关闭 → 不注入。
// 移植自 cc-switch transform.rs:94-124,优先级:output_config.effort > thinking.type+budget_tokens。
//
// 解析入口:output_config.effort(low/medium/high/max 1:1,未知丢)优先;
// 兜底 thinking.type:adaptive→max,enabled 按 budget_tokens 分档(<4000→low,4000-15999→medium,
// ≥16000→high,无 budget→high),disabled/缺省→""。
func resolveReasoningEffort(req *AnthropicRequest) string {
	if req == nil {
		return ""
	}
	// Priority 1: output_config.effort
	if len(req.OutputConfig) > 0 {
		var oc struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(req.OutputConfig, &oc) == nil {
			switch strings.ToLower(strings.TrimSpace(oc.Effort)) {
			case "low":
				return "low"
			case "medium":
				return "medium"
			case "high":
				return "high"
			case "max":
				return "max"
			}
		}
	}
	// Priority 2: thinking.type + budget_tokens
	if req.Thinking == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
	case "adaptive":
		return "max" // adaptive = 最强推理,对应 NIM max 档
	case "enabled":
		b := req.Thinking.BudgetTokens
		switch {
		case b <= 0:
			return "high" // enabled 但无 budget → 假定强推理
		case b < 4000:
			return "low"
		case b < 16000:
			return "medium"
		default:
			return "high"
		}
	default:
		return "" // disabled / 缺省
	}
}

// mapReasoningEffort 将内部规范化等级按上游取值模式映射成目标上游认的字符串。
// 移植自 cc-switch transform_codex_chat.rs:458-491,NIM 链用 "deepseek" mode:
// max/xhigh→max,其余(low/medium/high/adaptive)→high —— 只产 NIM v4-flash 认的 high/max,
// 不产 low/medium,避免触发上游 400 "Invalid reasoning_effort"。
// 返回空串表示不注入(上游不认的值)。
func mapReasoningEffort(effort, mode string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "none", "off", "disabled":
		return "" // 显式关闭:不注入 effort,由 thinking:false 路径处理(此处不处理关闭)
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "deepseek":
		// NIM DeepSeek v4-flash 等取值仅 high/max:max/xhigh→max,其余→high。
		switch effort {
		case "max", "xhigh":
			return "max"
		default:
			return "high"
		}
	case "passthrough":
		switch effort {
		case "minimal", "low", "medium", "high", "max":
			return effort
		default:
			return ""
		}
	default:
		// 未显式配置模式:降级为 deepseek(NIM 默认上游取值最稳)
		switch effort {
		case "max", "xhigh":
			return "max"
		default:
			return "high"
		}
	}
}

// nvidiaThinkingEffortMode 按上游模型名判定不同上游取值模式。
// 当前 NIM 池上游统一为 deepseek 取值(只有 high/max 两档最稳);新增上游若取值不同再按模型分支扩展。
func nvidiaThinkingEffortMode(model string) string {
	return "deepseek"
}

// nvidiaModelSupportsThinking 判别上游模型是否为推理型(默认开思考)。
// 推理型模型即使客户端未显式请求思考,也应发 thinking:true 让上游按默认档出思考,
// 避免被默认关掉。判别基于模型名关键字:deepseek-v4/glm-4.6/glm-5/qwen3/r1 等推理模型。
func nvidiaModelSupportsThinking(model string) bool {
	low := strings.ToLower(model)
	keywords := []string{"deepseek-v4", "deepseek-r", "glm-4.5", "glm-4.6", "glm-5", "qwen3", "/r1", "reasoning"}
	for _, k := range keywords {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// injectNvidiaChatTemplateKwargs 给 OpenAI Chat 入站(直连或 Codex chat-completions)的请求,
// 把客户端发的思考等级透传成 NIM 认的 chat_template_kwargs。
// 从原始入站 bodyBytes 提取 reasoning_effort(顶层,Codex 形态)或 reasoning.effort(OpenRouter 形态),
// 按 NIM 上游取值模式映射后注入 chat_template_kwargs:{thinking:true, reasoning_effort:<mapped>}。
// 客户端未发思考强度时:若模型本身推理型 → 仅 thinking:true 默认档;否则不注入(回归安全)。
//
// 设计要点:reasoning_effort 不是 OpenAIChatRequest 字段(顶层该字段 NIM 不认,会 400),
// 既不在结构体里接、也不往上游顶层发,只在原始 body 里提后转进 chat_template_kwargs。
func injectNvidiaChatTemplateKwargs(chatReq *OpenAIChatRequest, bodyBytes []byte, upstreamModel string) {
	if chatReq == nil {
		return
	}
	if !IsEnableThinkingMode() {
		chatReq.ChatTemplateKwargs = nil
		return
	}
	mode := nvidiaThinkingEffortMode(upstreamModel)
	if effort := extractOpenAIReasoningEffort(bodyBytes); effort != "" {
		if mapped := mapReasoningEffort(effort, mode); mapped != "" {
			chatReq.ChatTemplateKwargs = map[string]interface{}{
				"thinking":         true,
				"reasoning_effort": mapped,
			}
			return
		}
		return
	}
	if nvidiaModelSupportsThinking(upstreamModel) && !openAIBodyExplicitlyDisabled(bodyBytes) {
		chatReq.ChatTemplateKwargs = map[string]interface{}{"thinking": true}
	}
}

// isThinkingExplicitlyDisabled 判定 Anthropic 客户端是否显式关闭思考。
// 仅当 thinking.type=="disabled" 时为真(output_config 协议无关闭概念,其 effort 字段均为开档)。
// 用于在"模型默认开思考"的 fallback 路径中排除客户端显式关闭,尊重关闭意图。
func isThinkingExplicitlyDisabled(req *AnthropicRequest) bool {
	if req == nil || req.Thinking == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(req.Thinking.Type)) == "disabled"
}

// openAIBodyExplicitlyDisabled 判定 OpenAI 入站 body 是否显式关闭思考。
// OpenAI 协议无显式 disabled 字段;若 reasoning_effort 为 "none"/"off"/"disabled" 视为显式关闭。
func openAIBodyExplicitlyDisabled(bodyBytes []byte) bool {
	effort := extractOpenAIReasoningEffort(bodyBytes)
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	}
	return false
}

// extractOpenAIReasoningEffort 从原始入站 body 提取思考等级字符串。
// 支持两种形态:Codex 顶层 "reasoning_effort":"high" 与 OpenRouter "reasoning":{"effort":"max"}。
// 返回 lowercase 规范化值或空串。
func extractOpenAIReasoningEffort(bodyBytes []byte) string {
	if len(bodyBytes) == 0 {
		return ""
	}
	var raw struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if json.Unmarshal(bodyBytes, &raw) == nil {
		if e := strings.ToLower(strings.TrimSpace(raw.ReasoningEffort)); e != "" {
			return normalizeEffort(e)
		}
		if e := strings.ToLower(strings.TrimSpace(raw.Reasoning.Effort)); e != "" {
			return normalizeEffort(e)
		}
	}
	return ""
}

// normalizeEffort 把 OpenAI 各档措辞归一为内部值 low/medium/high/max。
// xhigh(OpenAI 最强档)→ max(对应 NIM max);其余常见项直接映射;未知返回空串。
func normalizeEffort(e string) string {
	switch e {
	case "minimal":
		return "low" // NIM 无 minimal,后续 mapReasoningEffort 会再落到 high
	case "low", "medium", "high", "max", "xhigh":
		if e == "xhigh" {
			return "max"
		}
		return e
	case "none", "off", "disabled":
		return ""
	default:
		return ""
	}
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
			// 仅在非空时进 thinking 分支:无推理模型(reasoning_content 恒空)永不开 thinking 块,
			// 若开启 IsReasoningAsText() 伪装模式,则作为普通 text_delta 直接逐字推打屏幕,避免 CLI 界面自动折叠。
			if IsReasoningAsText() {
				blockStates.emitTextDelta(ch.Delta.ReasoningContent, sink)
			} else {
				blockStates.emitThinkingDelta(ch.Delta.ReasoningContent, sink)
			}
		} else if ch.Delta.Reasoning != "" {
			// 兜底:部分 NIM 上游模型思考文本走 reasoning 字段(而非 reasoning_content),
			// 同样在伪装模式下转为普通 text_delta。
			if IsReasoningAsText() {
				blockStates.emitTextDelta(ch.Delta.Reasoning, sink)
			} else {
				blockStates.emitThinkingDelta(ch.Delta.Reasoning, sink)
			}
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
// kind 取值:"text" | "tool_use" | "thinking"。thinking 块固定占 index 0,先于 text/tool 块,
// 一旦开过即永久占位(不从 map 删除),保证后续 text/tool 块按官方"index 单调递增不复用"分配。
type sseBlock struct {
	index           int
	kind            string // "text" | "tool_use" | "thinking"
	toolID          string
	toolName        string
	textStarted     bool
	toolStarted     bool
	thinkingStarted bool // thinking 块已开块且至少发过一条 thinking_delta 的标志
	closed          bool // 该块是否已发过 content_block_stop,避免 closeAll 重复关块
}

type sseBlockStates struct {
	mu          sync.Mutex
	blocks      map[int]*sseBlock
	next        int
	textEmitted bool
	hasToolCall bool
}

// nextFreeIndex 返回当前 blocks 中未占用的最小 index,供 text/tool 块分配使用。
// 引入 thinking 块(固定占 index 0)后,text 与 tool 块需据此整体后移一位,避免与 thinking 块抢同 index。
func (s *sseBlockStates) nextFreeIndex() int {
	used := map[int]bool{}
	for k := range s.blocks {
		used[k] = true
	}
	for i := 0; ; i++ {
		if !used[i] {
			return i
		}
	}
}

// emitTextDelta 把一条 OpenAI 文本增量转成 Anthropic content_block_delta(text_delta)。
// 若 thinking 块当前已开(thinkingStarted),先按官方序列完整闭合它
// (signature_delta → content_block_stop) 再开 text 块,保证"思考先于正文、思考块完全闭合后才开 text"。
// text 块 index 分配:若 index 0 尚未被任何块占用(无 thinking/无 tool)→ 0;
// 否则用 nextFreeIndex()(thinking 开过永久占 0 → text 落在 1)。
// 已开过的同 index text 块(连续 text_delta)直接复用、不重复开块。
func (s *sseBlockStates) emitTextDelta(text string, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 若 thinking 块当前已开:先发空串 signature_delta + content_block_stop 闭合它
	s.closeThinkingIfOpen(fw)
	// 优先检索是否有已创建且未关闭的 text 块，有则复用（避免多帧 text_delta 误触发 nextFreeIndex 开新块）
	var b *sseBlock
	for _, blk := range s.blocks {
		if blk != nil && blk.kind == "text" && !blk.closed {
			b = blk
			break
		}
	}
	if b == nil {
		// 未找到已有 text 块：分配 index（若 0 位被 thinking 或 tool 占领则用 nextFreeIndex）
		idx := 0
		if b0, ok := s.blocks[0]; ok && b0 != nil && (b0.kind == "thinking" || b0.kind == "tool_use") {
			idx = s.nextFreeIndex()
		}
		b = &sseBlock{index: idx, kind: "text"}
		s.blocks[idx] = b
	}
	if !b.textStarted {
		b.textStarted = true
		s.textEmitted = true
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "text", "", ""))
	}
	fw.writeEvent("content_block_delta", contentBlockTextDeltaPayload(b.index, text))
}

// closeThinkingIfOpen 在锁内调用:若 blocks[0] 是已开块(thinkingStarted)且尚未关闭的 thinking 块,
// 按 official 序列发 signature_delta(空)+content_block_stop 闭合它,并标记 closed,
// 但不从 map 删除——以保证后续 text/tool 块按官方"index 单调递增不复用 thinking 的 0 位"分配。
// 仅可开块却从未下发 thinking_delta 的异常 thinking 块(thinkingStarted==false)静默丢弃且不占位。
func (s *sseBlockStates) closeThinkingIfOpen(fw sseEventSink) {
	b, ok := s.blocks[0]
	if !ok || b == nil || b.kind != "thinking" || b.closed {
		return
	}
	if b.thinkingStarted {
		fw.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(b.index, ""))
		fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
		b.closed = true
	}
	// 未发过 thinking_delta 的空块:丢弃,不占位(无推理模型守卫),从 map 删除
	if !b.thinkingStarted {
		delete(s.blocks, 0)
	}
}

// emitThinkingDelta 把上游 reasoning_content 增量转成 Anthropic thinking 块事件序列:
// 首次开 content_block_start(thinking),后续 content_block_delta(thinking_delta)。
// thinking 块固定占 index 0,严格对齐官方"思考先于正文"顺序。
// 仅在 reasoning_content != "" 时由主循环调用,无推理模型路径永不开 thinking 块。
func (s *sseBlockStates) emitThinkingDelta(text string, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 若 text 块已开却回收到 reasoning(异常顺序),先关 text 块再开 thinking(防御,理论上不触发)
	if b, ok := s.blocks[0]; ok && b != nil && b.kind == "text" && b.textStarted {
		fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
		delete(s.blocks, 0)
	}
	b, ok := s.blocks[0]
	if !ok || b == nil {
		b = &sseBlock{index: 0, kind: "thinking"}
		s.blocks[0] = b
	} else if b.kind != "thinking" {
		// 防御:index 0 被非 thinking 占据时,用下一可用 index 开 thinking
		idx := s.nextFreeIndex()
		b = &sseBlock{index: idx, kind: "thinking"}
		s.blocks[idx] = b
	}
	if !b.thinkingStarted {
		b.thinkingStarted = true
		fw.writeEvent("content_block_start", contentBlockThinkingStartPayload(b.index))
	}
	fw.writeEvent("content_block_delta", contentBlockThinkingDeltaPayload(b.index, text))
}

// emitToolCallDelta 处理 OpenAI tool_calls 增量(index 指向上游分块的工具调用编号)，
// 映射成 Anthropic 的 content_block_start(tool_use) + content_block_delta(input_json_delta)。
func (s *sseBlockStates) emitToolCallDelta(tc ChatToolCall, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasToolCall = true
	// 若 thinking 块当前已开:先按官方序列完整闭合它(signature_delta → stop)再开 tool_use,
	// 保证"思考先于正文/工具"且 thinking 块在 tool_use 块之前完全闭合。
	s.closeThinkingIfOpen(fw)
	// tool_use 块 index 分配:base = 已开(含已关)块数量 —— thinking 开过占 1 位 + text 开过占 1 位。
	// 上游 tc.Index 是该工具调用在上游工具列表里的位次,key = base + tc.Index 保证多工具不抢 index,
	// 且工具块严格排在 thinking/text 之后,符合官方"思考→正文→工具"或"思考→工具"顺序。
	base := 0
	if b0, ok := s.blocks[0]; ok && b0 != nil && b0.kind == "thinking" {
		base = 1
	}
	if s.textEmitted {
		base = 1
		if b0, ok := s.blocks[0]; ok && b0 != nil && b0.kind == "thinking" {
			base = 2
		}
	}
	key := base + tc.Index
	b, ok := s.blocks[key]
	if !ok {
		b = &sseBlock{index: key, kind: "tool_use", toolID: tc.ID, toolName: tc.Function.Name}
		s.blocks[key] = b
	}
	if !b.toolStarted {
		b.toolStarted = true
		if b.toolID == "" {
			b.toolID = fmt.Sprintf("toolu_nvidia_%d", tc.Index)
		}
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "tool_use", b.toolID, b.toolName))
	}
	// OpenAI 流式 tool_calls 的 arguments 是增量字符串，Anthropic 用 input_json_delta 直传
	if tc.Function.Arguments != "" {
		fw.writeEvent("content_block_delta", contentBlockInputJSONDeltaPayload(b.index, tc.Function.Arguments))
	}
}

// closeAll 关闭所有已打开但尚未 closed 的文本/工具/思考块,发出 content_block_stop。
// 对 thinking 块:关块前先发一条空串 signature_delta(无签名上游占位),严格对齐官方序列。
// 已经被 closeThinkingIfOpen/emitThinkingDelta 切换逻辑提前闭合(closed==true)的块跳过,避免重复关门。
// 对只开块却从未下发 thinking_delta 的异常 thinking 块(无推理模型误触发 / 上游异常握手帧):
// 直接丢弃,不发 signature_delta、不发 stop,避免客户端 SDK 收到空 thinking 块报错或卡等。
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
		if b.closed {
			continue // 已被切换逻辑提前闭合,不重复关块
		}
		if b.kind == "thinking" && !b.thinkingStarted {
			// 空块丢弃:从未实际下发 thinking_delta 的 thinking 块,当作没开过。
			delete(s.blocks, k)
			continue
		}
		if b.thinkingStarted {
			fw.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(b.index, ""))
			fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
			b.closed = true
		} else if b.textStarted || b.toolStarted {
			fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
			b.closed = true
		}
	}
}

// hasEmittedAnyBlock 检查本轮会话是否已发出过至少一个 content_block。
// 注意:从未下发 thinking_delta 的异常 thinking 块(只开块无内容)不计入,避免它错误地
// 抑制空块兜底逻辑,保证无推理模型路径与改动前行为一致。
func (s *sseBlockStates) hasEmittedAnyBlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.blocks {
		if b.thinkingStarted || b.textStarted || b.toolStarted {
			return true
		}
	}
	return false
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
	// firstByteHook 在首次向 w 写入前一次性调用(混合模式延迟 WriteHeader 场景)。
	// 用途:past-WriteHeader 的首字节到达时由 flushWriter 触发回调,保证 WriteHeader(200)
	// 先于任何响应体字节落盘。nil 时跳过。触发后置空避免重复调用。
	firstByteHook func()
	// deferred 延迟缓冲:混合模式延迟 WriteHeader 场景下,在"实质内容首字"到达前,
	// 框架帧(message_start + thinking 块 content_block_start)先进 deferred 暂存,不落盘不 flusher、
	// 也不触发 WriteHeader。首个实质内容(thinking_delta 或回放正文首帧)到达时调 flushDeferred 触发
	// firstByteHook(WriteHeader 200 + 刷头)+ 把 deferred 字节顺序写 w + flusher,转入直写模式。
	// 若上游在实质内容前就断流重试耗尽,dropDeferred 丢弃暂存帧,回写 503,不污染客户端流。
	// deferredActive=false(默认)时 writeEvent/writeRaw 直接走直写路径,行为与改动前一致(零回归)。
	deferred       bytes.Buffer
	deferredActive bool
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

// writeEvent 写一帧 Anthropic SSE。deferredActive 期间该帧进 deferred 暂存,不落盘;
// 否则触发 firstByteHook(首次)后直写 w + flusher。
func (f *flushWriter) writeEvent(event, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferredActive {
		f.deferred.WriteString("event: " + event + "\n")
		f.deferred.WriteString("data: " + data + "\n\n")
		return
	}
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil // 一次性触发,避免重复 WriteHeader
		hook()
	}
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
	if f.deferredActive {
		f.deferred.WriteString(s)
		return
	}
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil
		hook()
	}
	f.w.WriteString(s)
	f.w.Flush()
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

// flushDeferred 把暂存的 deferred 字节一次性落盘:先触发 firstByteHook(WriteHeader 200 + 刷头),
// 再把 deferred 写 w + flusher,然后关闭延迟模式转入直写。幂等:多次调用只首次落盘 deferred。
// 用于混合模式首个实质内容(thinking_delta 或回放正文首帧)到达时确认 200 流,把其前的框架帧一并送出。
func (f *flushWriter) flushDeferred() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.deferredActive {
		return // 未进入延迟模式,无需处理
	}
	f.deferredActive = false
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil
		hook() // WriteHeader(200) + flusher.Flush 刷头
	}
	if f.deferred.Len() > 0 {
		f.w.Write(f.deferred.Bytes())
		f.w.Flush()
		if f.flusher != nil {
			f.flusher.Flush()
		}
		f.deferred.Reset()
	}
}

// dropDeferred 丢弃暂存的框架帧并关闭延迟模式,供上游在实质内容前断流重试耗尽时回 503 使用:
// 客户端从未收到任何字节(message_start 等框架帧未落盘),故可干净回写 503 overloaded_error。
func (f *flushWriter) dropDeferred() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deferredActive = false
	f.deferred.Reset()
	f.firstByteHook = nil
}

func (f *flushWriter) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferredActive {
		return // 延迟模式:不刷盘,等 flushDeferred 转实写
	}
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

// reset 清空已蓄流内容(保留 buffer 容量)。混合模式下重试轮开始前丢弃首轮未完整蓄流,
// 换用纯 replay 重新蓄整条上游内容时调用。
func (r *replayWriter) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Reset()
}

// replayBodyInto 把本 replay buffer 中的"正文/工具段 + 尾帧"回放到 live 客户端 sink,
// 跳过开头的 message_start 与整个思考段(它们在首轮已通过 teeSink 实时推给 live)。
// 供混合模式(writeNvidiaAnthropicStream)在整条 ready 后把正文回放给客户端使用。
//
// 帧序约定(由 openAIChatSSEToAnthropicSSEInto 产出):
//
//	message_start
//	[content_block_start(thinking) → thinking_delta* → signature_delta → content_block_stop]  // 思考段,可缺省
//	content_block_start(text|tool_use) → ... → content_block_stop                              // 正文/工具段
//	message_delta
//	message_stop
//
// 回放策略:逐帧扫描,message_start 永不回放(首轮已发);遇思考段从其 content_block_start(thinking)
// 起整段跳到配对 content_block_stop 之后;首个正文/工具 content_block_start 起的所有帧(含 text/tool
// delta、停块、message_delta、message_stop)原样回放给 live。
//
// liveSink 期望为 flushWriter;replayBodyInto 不锁 liveSink,调用方需保证此时无其它写者并发。
func (r *replayWriter) replayBodyInto(liveSink sseEventSink) {
	r.mu.Lock()
	raw := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	frames := scanAnthropicSSEFrames(raw)
	skippingThinking := false
	bodyStarted := false
	for _, f := range frames {
		switch f.event {
		case "message_start":
			// 首帧已在首轮实时发过,不重发
			continue
		case "content_block_start":
			if !bodyStarted {
				kind := contentBlockKind(f.data)
				if kind == "thinking" {
					// 思考段开头:跳过直到它的 content_block_stop
					skippingThinking = true
					continue
				}
				if kind == "text" || kind == "tool_use" {
					bodyStarted = true
				}
			}
		case "content_block_stop":
			if skippingThinking {
				// 配对到思考块的 stop,跳过本帧后结束跳过状态
				skippingThinking = false
				continue
			}
		}
		if skippingThinking {
			continue
		}
		liveSink.writeRaw(f.raw)
	}
	liveSink.flush()
}

// anthropicSSEFrame 表示 replay buffer 内的一帧原始 SSE 字节片段,附解析出的事件名与 data。
type anthropicSSEFrame struct {
	event string
	data  string
	raw   string // 原始 SSE 文本(event: X\ndata: Y\n\n),回放时原样写出
}

// scanAnthropicSSEFrames 把 Anthropic SSE 字节流按帧切成 []anthropicSSEFrame。
// 仅用于 replayBodyInto 回放内部 buffer(格式由 replayWriter.writeEvent 固定为
// "event: <name>\ndata: <data>\n\n"),改造极轻,不依赖外部 SSE 包。
func scanAnthropicSSEFrames(raw []byte) []anthropicSSEFrame {
	frames := make([]anthropicSSEFrame, 0, 16)
	var event, data string
	rawStart := 0
	n := len(raw)
	i := 0
	for i < n {
		// 找一行结尾(\n)
		nl := n
		for j := i; j < n; j++ {
			if raw[j] == '\n' {
				nl = j
				break
			}
		}
		line := bytes.TrimRight(raw[i:nl], "\r")
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			event = string(bytes.TrimPrefix(line, []byte("event: ")))
		case bytes.HasPrefix(line, []byte("data: ")):
			data = string(bytes.TrimPrefix(line, []byte("data: ")))
		case len(line) == 0:
			// 空行 = 帧分隔,只有同时具备 event+data 才成帧
			if event != "" && data != "" {
				frames = append(frames, anthropicSSEFrame{
					event: event,
					data:  data,
					raw:   string(raw[rawStart:nl+1]) + "\n",
				})
			}
			event, data = "", ""
			rawStart = nl + 1
		}
		i = nl + 1
	}
	return frames
}

// contentBlockKind 从 content_block_start 事件的 data 中提取 content_block.type(thinking|text|tool_use)。
// 解析失败返回空串,调用方按"非思考即正文"处理。
func contentBlockKind(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	cb, _ := m["content_block"].(map[string]interface{})
	if cb == nil {
		return ""
	}
	kind, _ := cb["type"].(string)
	return kind
}

// ===== teeSink: 混合模式双写 sink(思考实时透传 + 正文/工具蓄流) =====

// teeSink 是 sseEventSink 的混合模式实现:把同一份 Anthropic SSE 事件同时写给
// replay(蓄流,供断流重试与正文回放)与 live(实时推客户端 TCP socket)。
//
// 分流目标——精准解决"思考块等整条 ready 才出"的首字节延迟痛点:
//
//	message_start          → 双写(让客户端立即进入 SSE 等待态)
//	思考段(thinking 块
//	content_block_start →
//	thinking_delta* →
//	signature_delta →
//	content_block_stop)    → 双写(思考逐字实时显示在客户端)
//	正文/工具段起          → 只写 replay(整条 ready 后由 replayBodyInto 回放,
//	                         保留断流重试能力——正文不丢半截)
//	message_delta/stop     → 只写 replay(尾帧随正文回放补发)
//
// 切换时机:转译主循环保证"思考块在正文块之前完全闭合"(closeThinkingIfOpen 在开 text/tool 前必调,
// 见 nvidia_translate.go:782-784)。故 tee 只需在见到首个正文块的 content_block_start(text/tool_use)
// 时置 bodyStarted=true,此前所有帧双写 live,此后所有帧只写 replay。message_start 永远双写。
//
// replayOnly=true 时一律只写 replay:用于上游断流后的重试轮——首轮思考已实时发到客户端(草稿,
// 断流不回滚),重试轮重新蓄流整条,整条 ready 后只回放正文(replayBodyInto 跳过思考头),
// 避免向客户端二次推送 thinking/content_block_start 导致的 index 冲突与重复 message_start。
type teeSink struct {
	replay      *replayWriter
	live        *flushWriter
	bodyStarted bool // 已见首个正文/工具块 content_block_start,此后只蓄流不实时推
	replayOnly  bool // 重试轮:全部只写 replay(压住思考重复外发)
	// liveThinkingOpen 跟踪 live 上思考块是否仍处开块未闭合状态,供回放前判断是否补闭合。
	liveThinkingOpen bool
	// liveDeferredFlushed 标记首条实质思考内容是否已 flushDeferred(确认 200 流)。
	liveDeferredFlushed bool
}

func newTeeSink(replay *replayWriter, live *flushWriter) *teeSink {
	return &teeSink{replay: replay, live: live}
}

// writeEvent 按 bodyStarted/replayOnly 分流写 live+replay。
func (t *teeSink) writeEvent(event, data string) {
	// replay 始终写(蓄流供重试判定与正文回放)
	t.replay.writeEvent(event, data)
	if t.replayOnly || t.live == nil {
		// 重试轮:liveThinkingOpen 保持首轮残留值不动,仅蓄流
		return
	}
	// 双写分流:思考段(+message_start)实时推 live,正文段只 replay
	pushLive := false
	switch event {
	case "message_start":
		pushLive = true
	case "content_block_start":
		if !t.bodyStarted {
			kind := contentBlockKind(data)
			switch kind {
			case "thinking":
				pushLive = true
				t.liveThinkingOpen = true
			case "text", "tool_use":
				// 首个正文块:不推 live(蓄流),标记正文段开始
				t.bodyStarted = true
			}
		}
	case "content_block_delta":
		// 仍在思考段(bodyStarted 未置)才推 live;正文 delta 蓄流不推
		if !t.bodyStarted {
			pushLive = true
			// 首条实质内容(thinking_delta):此前 message_start + thinking 块 start 是框架帧,
			// 已暂存在 live.deferred。此刻 flushDeferred 触发 WriteHeader(200)+把框架帧一并送出,
			// 确认 200 流。若上游在首条思考内容前就断流,deferred 未 flush,可干净回 503。
			if !t.liveDeferredFlushed {
				t.live.flushDeferred()
				t.liveDeferredFlushed = true
			}
		}
	case "content_block_stop":
		// 仍在思考段的 stop 推 live 并清 liveThinkingOpen;正文 stop 蓄流不推
		if !t.bodyStarted {
			pushLive = true
			t.liveThinkingOpen = false
		}
	}
	if pushLive {
		t.live.writeEvent(event, data)
	}
}

// writeRaw 原始 SSE 字节同步双写(转译主循环未用到 writeRaw,保留接口对称)。
func (t *teeSink) writeRaw(s string) {
	t.replay.writeRaw(s)
	if !t.replayOnly && t.live != nil && !t.bodyStarted {
		t.live.writeRaw(s)
	}
}

func (t *teeSink) flush() {
	t.replay.flush()
	if !t.replayOnly && t.live != nil {
		t.live.flush()
	}
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

// contentBlockThinkingStartPayload 构造 thinking 块的 content_block_start 负载。
// 严格对齐 Anthropic 官方流式协议:thinking 块开块时 thinking 与 signature 均为空串,
// 后续由 thinking_delta 累积思考文本、由 signature_delta 在关块前补签名。
// 对无签名上游(NIM/GLM reasoning_content、Gemini 已剥签名的 thought)关块前发空串占位,
// 等同官方 display:"omitted" 形态 —— 满足事件序列形状,让 Claude Code SDK 的
// MessageAccumulator 能正常识别并渲染思考块。
func contentBlockThinkingStartPayload(index int) string {
	m := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":      "thinking",
			"thinking":  "",
			"signature": "",
		},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// contentBlockThinkingDeltaPayload 构造 thinking_delta 增量负载,承载上游推理过程的分片文本。
func contentBlockThinkingDeltaPayload(index int, thinking string) string {
	m := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": thinking},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// contentBlockSignatureDeltaPayload 构造 signature_delta 负载:关 thinking 块前发一次。
// 对无签名上游传空串占位,保证协议形态完整,避免客户端把缺 signature_delta 的 thinking 块判为不完整而丢弃。
func contentBlockSignatureDeltaPayload(index int, signature string) string {
	m := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{"type": "signature_delta", "signature": signature},
	}
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
	// ReasoningContent 承载 NVIDIA 上游非流式响应里推理模型的思考文本。
	// 旧实现非流式回译忽略该字段,思考被丢弃(D-nvidia 侧)。部分模型用 reasoning 字段名兜底。
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
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
	// ChatTemplateKwargs 透传 NIM 推理模型的思考开关与等级。NIM 官方 DeepSeek v4-flash 示例
	// 证实上游认 {"thinking":true,"reasoning_effort":"high"|"max"},经 OpenAI SDK 走 extra_body,
	// 原生 HTTP 请求体里即顶层 chat_template_kwargs 对象,由 vLLM 模板注入。
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
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
	// Reasoning 是部分 NIM 上游模型(D/S 派系官方示例用 getattr 兜底)返回思考文本的字段名兜底。
	// 当 reasoning_content 缺失、reasoning 有值时也走 thinking_delta 回译。
	Reasoning string         `json:"reasoning,omitempty"`
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
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
