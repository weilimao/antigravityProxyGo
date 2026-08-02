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
//
// thinkingRedacted 表达客户端是否通过 Anthropic-Beta 头部 redact-thinking-* 显式关闭思考
// (Claude Code 关闭思考开关时,body 常不带 thinking 字段,但头部带 redact-thinking-2026-02-12)。
// 命中即绝对跳过所有 thinking 注入(含 effort 解析与推理模型默认 fallback),
// 优先级高于 IsEnableThinkingMode() 全局开关与 body 显式 disabled,与客户端关闭意图严格对齐。
func AnthropicToOpenAIChat(req *AnthropicRequest, thinkingRedacted bool) (*OpenAIChatRequest, error) {
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
	//
	// thinkingRedacted:客户端通过 Anthropic-Beta 头部 redact-thinking-* 标志位表达"关闭思考"意图
	// (Claude Code 关闭思考开关时,body 不带 thinking 字段但头部带 redact-thinking-2026-02-12)。
	// 命中即绝对跳过所有 thinking 注入(effort 解析与推理模型 fallback 全部短路),
	// 优先级高于 IsEnableThinkingMode() 全局开关、高于 body 显式 disabled,与 body 路径同等强力。
	if thinkingRedacted {
		out.ChatTemplateKwargs = nil
	} else if !IsEnableThinkingMode() {
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
//
// thinkingRedacted:OpenAI Chat 入站本身无 Anthropic 头部协议,该入参由上游调用方根据
// Anthropic-Beta redact-thinking-* 头部解析后传入(直连 OpenAI/Codex 客户端恒为 false)。
// 命中即绝对跳过所有 thinking 注入,与 Anthropic 链路保持强一致的关思考语义。
func injectNvidiaChatTemplateKwargs(chatReq *OpenAIChatRequest, bodyBytes []byte, upstreamModel string, thinkingRedacted bool) {
	if chatReq == nil {
		return
	}
	if thinkingRedacted {
		chatReq.ChatTemplateKwargs = nil
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

// anthropicBetaThinkingRedacted 判定 HTTP 请求头里的 Anthropic-Beta 是否带 redact-thinking-* 标志。
// Claude Code 的思考开关在"关闭"态发送 redact-thinking-2026-02-12 beta 头(此时 body 常不带 thinking 字段),
// 这是客户端表达"关闭思考"的协议信号。本函数从标准 http.Header 多值字段中按逗号切分逐项前缀匹配,
// 命中任意 redact-thinking- 前缀即返回 true。头部缺失或无匹配返回 false(回归安全)。
//
// 设计依据:Anthropic SDK 把 anthropic-beta 多值用逗号分隔写成单条 header
// (如 "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,..."),
// 同时 http.Header 允许多条同名 header,$values 数组再逐条逗号拆分覆盖两种形态。
// 大小写不敏感(http.Header.Get 已规范化键名),前缀匹配容忍 beta 末尾日期版本号变化。
func anthropicBetaThinkingRedacted(header http.Header) bool {
	if header == nil {
		return false
	}
	for _, raw := range header["Anthropic-Beta"] {
		if raw == "" {
			continue
		}
		for _, tok := range strings.Split(raw, ",") {
			t := strings.TrimSpace(strings.ToLower(tok))
			if strings.HasPrefix(t, "redact-thinking-") {
				return true
			}
		}
	}
	return false
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

// writePendingEvent 把一帧 Anthropic SSE(event: <name>\n data: <data>\n\n)追加进给定 buffer,
// 不落 live。resumeSink 用它把本轮待提交的补闭合帧 + 重映射正文帧攒进 pending,
// 仅在 message_stop(整条 ready)提交时一次性刷给 live;断流轮 pending 随 reset 丢弃。
func writePendingEvent(buf *bytes.Buffer, event, data string) {
	buf.WriteString("event: " + event + "\n")
	buf.WriteString("data: " + data + "\n\n")
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

// liveStreamState 封装"截至本次成功判定时,客户端 live 上已经下发的内容块协议态",
// 供 replayFollowingInto 决定哪些块已 live(跳过不重发)、哪些块尚未 live(需 remap 后补发),
// 以及分配新块 index 的起点。它由 teeSink/resumeSink 在 pull 成功时落地的运行期态快照构造。
//
// 字段:
//   - liveIdxMap:上游 block index → 客户端实际 index 的映射,仅含"已实时推给 live 的正文 text 块"。
//     首轮 identity(上游 idx==客户端 idx);续传轮为 resumeSink 重映射后的客户端 idx。
//     replayFollowingInto 据此跳过已 live 的 text 块(整块 start/delta/stop 全跳),避免重复下发。
//   - liveMaxIdx:客户端已用过的最大 index(单调),replayFollowingInto 给"尚未 live 的块"(tool_use/
//     尾随 text)分配 liveMaxIdx+1 起新 client index,保证不与已 live 的 text 块 index 冲突。
//   - thinkingLive:首轮/续传轮 thinking 段是否已实时推 live(决定 replayFollowingInto 是否跳过思考头)。
type liveStreamState struct {
	liveIdxMap   map[int]int
	liveMaxIdx   int
	thinkingLive bool
}

// replayFollowingInto 是混合模式正文实时下发架构的成功回放入口:把成功轮 replay 缓冲里
// "尚未实时下发给 live 的内容"补发给 liveSink,并补发尾帧(message_delta/message_stop),
// 实现整条流的完整闭合。与旧 replayBodyInto 的根本区别:
//   - replayBodyInto 假设正文从未 live,整段正文+尾帧从头回放 → 会重复已 live 的 text 块(草稿段)。
//   - replayFollowingInto 据 prevState.liveIdxMap 跳过"已 live 的 text 块"(start/delta/stop 全跳),
//     只补发"尚未 live 的块"(tool_use、thinking 后的 text、尾随 text 等)并 remap 其 index 到
//     liveMaxIdx+1(避免与已 live 的 text 块 index 冲突),最后补发 message_delta+message_stop 尾帧。
//
// 跳过规则(按 replay 帧序):
//   - message_start:全跳(首轮已发)。
//   - thinking 段(content_block_start thinking / thinking_delta / signature_delta / 配对的 stop):
//     prevState.thinkingLive==true 时全跳;false 时(thinking 未 live,如无推理模型或首轮 thinking 未到)
//     原样回放。
//   - text 块:上游 index 在 prevState.liveIdxMap 中 → 整块(start/delta/stop)全跳(已 live);
//     否则(尾随 text,或无推理模型首轮 text 未推 live 的兜底)remap 到 liveMaxIdx+1 后回放。
//   - tool_use 块:从未 live,remap 到 liveMaxIdx+1 后整块回放(防错误工具调用约束要求 tool 段走 replay)。
//   - message_delta/message_stop:补发一次(取 replay 尾帧数据,stop_reason/usage 与成功轮一致)。
//
// 该函数统一服务纯 text 回复(只补尾帧)、含 tool_use 回复(补 tool 块+尾帧)、续传成功(补未 live 段+尾帧)
// 三种形态,无需调用方分支判断——调用方只需在 pull 整体成功后调用一次。
func (r *replayWriter) replayFollowingInto(liveSink sseEventSink, prevState *liveStreamState) {
	r.mu.Lock()
	raw := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	frames := scanAnthropicSSEFrames(raw)
	// 跳过思考头的状态机:遇到 thinking start 进入跳过,配对到它的 stop 后退出(跳过整段思考头)。
	skippingThinking := false
	// 新块 index 分配:从 prevState.liveMaxIdx+1 起递增,给"尚未 live 的块"(tool/尾随 text)用。
	nextIdx := prevState.liveMaxIdx + 1
	// 本回放轮内"上游 idx → 客户端 idx"重映射(仅给未 live 的块;已 live 的块全跳不进此表)。
	remap := map[int]int{}
	tailEmitted := false
	for _, f := range frames {
		switch f.event {
		case "message_start":
			continue // 首轮已发
		case "content_block_start":
			kind := contentBlockKind(f.data)
			if kind == "thinking" {
				if prevState.thinkingLive {
					skippingThinking = true
					continue
				}
				// thinking 未 live:原样回放(无推理模型或首轮 thinking 未到的兜底)
				liveSink.writeRaw(f.raw)
				continue
			}
			if skippingThinking {
				// 仍在思考头跳过区间内却遇到非 thinking start:不应出现,防御性退出跳过
				skippingThinking = false
			}
			upIdx := contentBlockIndex(f.data)
			if kind == "text" {
				if _, alreadyLive := prevState.liveIdxMap[upIdx]; alreadyLive {
					// 已 live 的 text 块:整块跳过(start/delta/stop 全跳),记录"跳过态"避免 stop 误回放
					remap[upIdx] = -1 // -1 哨兵:后续 delta/stop 见 -1 即跳过
					continue
				}
				// 尾随 text:尚未 live,remap 后回放
				ci := nextIdx
				remap[upIdx] = ci
				nextIdx++
				liveSink.writeEvent("content_block_start", rewriteContentBlockIndex(f.data, ci))
				continue
			}
			// tool_use:从未 live,remap 后整块回放
			if kind == "tool_use" {
				ci := nextIdx
				remap[upIdx] = ci
				nextIdx++
				liveSink.writeEvent("content_block_start", rewriteContentBlockIndex(f.data, ci))
				continue
			}
			// 未知 kind:原样回放(防御,不应出现)
			liveSink.writeRaw(f.raw)
		case "content_block_delta":
			if skippingThinking {
				continue // 思考头区间内 delta 全跳
			}
			upIdx := contentBlockIndex(f.data)
			ci, mapped := remap[upIdx]
			if mapped && ci == -1 {
				continue // 已 live 的 text 块 delta:跳过
			}
			if !mapped {
				// 无映射:可能是存量 thinking_delta/signature_delta(已被 thinking 头逻辑覆盖)或异常 delta。
				// thinking 未 live 路径已原样回放其 start,此处 delta 也需原样跟回放。
				liveSink.writeRaw(f.raw)
				continue
			}
			liveSink.writeEvent("content_block_delta", rewriteContentBlockIndex(f.data, ci))
		case "content_block_stop":
			if skippingThinking {
				// 配对到思考块的 stop:结束跳过区间
				skippingThinking = false
				continue
			}
			upIdx := contentBlockIndex(f.data)
			ci, mapped := remap[upIdx]
			if mapped && ci == -1 {
				continue // 已 live 的 text 块 stop:跳过
			}
			if !mapped {
				liveSink.writeRaw(f.raw)
				continue
			}
			liveSink.writeEvent("content_block_stop", rewriteContentBlockIndex(f.data, ci))
		case "message_delta":
			if tailEmitted {
				continue
			}
			tailEmitted = true
			liveSink.writeRaw(f.raw)
		case "message_stop":
			liveSink.writeRaw(f.raw)
		default:
			liveSink.writeRaw(f.raw)
		}
	}
	liveSink.flush()
}


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

// contentBlockIndex 从 content_block_start / content_block_delta / content_block_stop 事件的 data 中
// 提取顶层 index 字段。这三类事件都带顶层 "index"(见 contentBlockStartPayload/contentBlockXxxPayload 构造),
// 解析失败返回 -1。供 teeSink/resumeSink 追踪 live 上的开块 index 与做上游→客户端 index 映射。
func contentBlockIndex(data string) int {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return -1
	}
	idx, ok := m["index"].(float64)
	if !ok {
		return -1
	}
	return int(idx)
}

// ===== teeSink: 混合模式双写 sink(思考+纯文本正文实时透传,tool 蓄流回放) =====

// teeSink 是 sseEventSink 的混合模式实现:把同一份 Anthropic SSE 事件同时写给
// replay(蓄流,供断流重试完整性判定与含工具回复回放)与 live(实时推客户端 TCP socket)。
//
// 分流目标——把"正文逐块实时下发"的首字节延迟降到 TTFT,同时保留断流重试能力:
//
//	message_start          → 双写(让客户端立即进入 SSE 等待态,经 deferred 暂存)
//	思考段(thinking 块
//	content_block_start →
//	thinking_delta* →
//	signature_delta →
//	content_block_stop)    → 双写(思考逐字实时显示在客户端)
//	正文 text 块(start/delta/stop)→ 双写(正文逐字实时显示 + 蓄流兜底)
//	tool_use 块及之后所有块 → 只写 replay(含工具调用的回复整条 ready 后由 replayBodyInto 回放)
//	message_delta/stop     → 只写 replay(尾帧只在整条 ready 后由调用方决定是否送 live)
//
// tool 段为何不实时推 live:首轮推过部分 tool input_json(id=A)后断流,重试轮新 tool 块 id 可能
// 变 B,客户端持两个不同 id 半截 tool 块会让 Claude Code 按旧 id 拿残缺 JSON 发起错误工具调用
// ——功能错误比正文显示错乱严重得多。故见 tool_use 起锁定只 replay,保持现状蓄流回放。
//
// 切换时机:转译主循环保证"思考块在正文块之前完全闭合"(closeThinkingIfOpen 在开 text/tool 前必调,
// 见 nvidia_translate.go:782-784)。text 块推 live 时需追踪 live 协议态(liveBodyOpenIdx/liveMaxUsedIdx),
// 断流时供调用方拷贝进 resumeSink,在重试轮惰性补闭合 + index 重映射后续推未发正文。
//
// replayOnly=true 时一律只写 replay:用于上游断流后的重试轮(由 resumeSink 接管,tee 在重试轮不用)。
type teeSink struct {
	replay      *replayWriter
	live        *flushWriter
	toolSeen    bool // 已见 tool_use 块:此后所有块只 replay 不推 live(防错误工具调用)
	replayOnly  bool // 重试轮:全部只写 replay(压住思考重复外发)
	// liveThinkingOpen 跟踪 live 上思考块是否仍处开块未闭合状态,供本轮断流判定与 resumeSink 补闭合。
	liveThinkingOpen bool
	// liveBodyOpenIdx 跟踪 live 上是否有未闭合的正文 text 块,记录其 index;-1 表已闭合或未开。
	// 断流时停在"已发 start 未发 stop"的 index,供 resumeSink 在首个正文 start 前惰性补 stop(liveBodyOpenIdx)。
	liveBodyOpenIdx int
	// liveMaxUsedIdx 记录 live 上曾发过的最大 content_block index(已闭合不复用),供 resumeSink 分配
	// 重试轮新正文块 index(liveMaxUsedIdx+1 单调递增)。包含 thinking 块的 0 与正文块 index。
	liveMaxUsedIdx int
	// liveIdxMap 记录已实时推给 live 的正文 text 块上游 index 集合(上游 block index → 客户端 index,首轮/续传均 identity 或重映射后值)。
	// 成功回放时 replayFollowingInto 据此跳过"已 live 的块",避免重复下发 start/delta/stop。
	// 仅 text 块进此表;thinking 段头由 replayFollowingInto 按类型跳过;tool 段不进 live 故不进此表。
	liveIdxMap map[int]int
	// liveThinkingPushed 标记首轮是否曾有 thinking 块实时推给 live(Once true 永不复位)。
	// 成功快照时据此设 liveStreamState.thinkingLive:retry 成功轮的 thinking 是草稿已丢弃不重发,
	// replayFollowingInto 据本字段跳过成功轮 replay 里的 thinking 头(thinking 在已 live 的 text 之前,
	// 重发会违反"思考先于正文"协议顺序,故一律跳过)。
	liveThinkingPushed bool
	// liveDeferredFlushed 标记首条实质思考内容是否已 flushDeferred(确认 200 流)。
	liveDeferredFlushed bool
}

func newTeeSink(replay *replayWriter, live *flushWriter) *teeSink {
	return &teeSink{replay: replay, live: live, liveBodyOpenIdx: -1, liveIdxMap: map[int]int{}}
}

// writeEvent 按 toolSeen/replayOnly 分流写 live+replay。
//
// 分流规则:
//   - replay 始终写(蓄流供重试判定;含工具回复整条 ready 后回放)。
//   - replayOnly(重试轮由 resumeSink 接管,tee 不用于重试轮):只写 replay。
//   - toolSeen(见过 tool_use):此后所有块只 replay,不再推 live(防错误工具调用)。
//   - 否则:thinking 段 + text 块实时推 live,delta 推 live 时刷新 liveBodyOpenIdx/liveMaxUsedIdx;
//     message_delta/message_stop 只 replay(尾帧由调用方在整条 ready 后决定,不在首轮推 live)。
func (t *teeSink) writeEvent(event, data string) {
	// replay 始终写(蓄流供重试判定与正文回放)
	t.replay.writeEvent(event, data)
	if t.replayOnly || t.live == nil {
		// 重试轮:liveThinkingOpen 保持首轮残留值不动,仅蓄流
		return
	}
	// 已锁定只 replay(见 tool_use 后):只把帧蓄流,不再实时推 live
	if t.toolSeen {
		return
	}
	// 双写分流:思考段 + 正文 text 段实时推 live,尾帧只 replay
	pushLive := false
	switch event {
	case "message_start":
		// 框架帧,经 deferred 暂存,首条实质内容到达时 flushDeferred 一并送出
		pushLive = true
	case "content_block_start":
		kind := contentBlockKind(data)
		switch kind {
		case "thinking":
			pushLive = true
			t.liveThinkingOpen = true
			t.liveThinkingPushed = true
		case "text":
			// 纯文本正文块:实时推 live + 蓄流。追踪 live 开块 index 供断流续传。
			pushLive = true
			idx := contentBlockIndex(data)
			if idx > t.liveMaxUsedIdx {
				t.liveMaxUsedIdx = idx
			}
			t.liveBodyOpenIdx = idx
			t.liveIdxMap[idx] = idx // 首轮上游 index 与客户端 index identity;成功回放时据此跳过已 live 块
		case "tool_use":
			// 见 tool_use:锁定此后只 replay(功能正确性约束)。本帧不推 live。
			t.toolSeen = true
		}
	case "content_block_delta":
		// 未锁定只 replay 时:思考段或正文 text 段的 delta 实时推 live。
		// 首条实质内容(thinking_delta 或非空 text_delta):此前 message_start 等框架帧已暂存 live.deferred,
		// 此刻 flushDeferred 触发 WriteHeader(200)+把框架帧一并送出,确认 200 流。
		// 若上游在首条实质内容前就断流,deferred 未 flush,可干净回 503。
		// 关键:保底空块的空 text_delta(ensureAtLeastOneBlock 在断流/no-content 路径补的空块)不触发 flushDeferred,
		// 否则断流轮提前 WriteHeader 200、丢失 503 干净失败能力。thinking_delta 永远非空(translator 保证),直接触发。
		dtype := deltaTypeForContentBlockDelta(data)
		isThinkingDelta := dtype == "thinking_delta" || dtype == "signature_delta"
		hasRealText := dtype == "text_delta" && deltaTextForContentBlockDelta(data) != ""
		if !isThinkingDelta && !hasRealText {
			// 空 text_delta(保底块)/未识别 delta:只蓄流不推 live,也不触发 deferred flush
			break
		}
		pushLive = true
		if !t.liveDeferredFlushed {
			t.live.flushDeferred()
			t.liveDeferredFlushed = true
		}
	case "content_block_stop":
		// 思考段 stop:推 live 并清 liveThinkingOpen;正文 text 段 stop:推 live 并清 liveBodyOpenIdx。
		// (tool 段 stop 因 toolSeen 已锁定走不到这里。)
		pushLive = true
		idx := contentBlockIndex(data)
		if t.liveThinkingOpen && idx == 0 {
			t.liveThinkingOpen = false
		}
		if t.liveBodyOpenIdx == idx {
			t.liveBodyOpenIdx = -1
		}
	}
	if pushLive {
		t.live.writeEvent(event, data)
	}
}

// writeRaw 原始 SSE 字节同步双写(转译主循环未用到 writeRaw,保留接口对称)。
func (t *teeSink) writeRaw(s string) {
	t.replay.writeRaw(s)
	if !t.replayOnly && t.live != nil && !t.toolSeen {
		t.live.writeRaw(s)
	}
}

func (t *teeSink) flush() {
	t.replay.flush()
	if !t.replayOnly && t.live != nil {
		t.live.flush()
	}
}

// ===== resumeSink:重试轮续传(不重发 message_start / 思考 / 已发正文块) =====

// resumeSink 是重试轮的 sseEventSink:把上游(经 openAIChatSSEToAnthropicSSEInto 再次转译)
// 产的 Anthropic SSE 事件,按首轮客户端已收到的协议态(liveThinkingOpen/liveBodyOpenIdx/liveMaxUsedIdx)
// 过滤后实时推给 live,实现"续传不重发":
//
//   - message_start 全跳(首轮已发,不能重复发)。
//   - 思考段(content_block_start thinking / thinking_delta / signature_delta / content_block_stop thinking)
//     全跳——首轮实时推到客户端的思考是草稿,重试轮不重发(避免重复 message_start 外的 index 冲突)。
//   - tool_use 段:与首轮一样不实时推 live(只随 replayWriter 蓄流,功能正确性约束)。重试轮若上游又生成
//     tool_use,本 sink 把 tool_use 帧只写 replay 不推 live。但含工具回复的整条回放由调用方在成功时
//     replayBodyInto 处理(同首轮含工具链路)。纯 text 回复的重试轮不会有 tool_use。
//   - 正文 text 块:首个正文 content_block_start 到达时惰性补闭合客户端残留的未闭合块(先思考→再正文),
//     然后用 liveMaxUsedIdx+1 开新块(index 重映射),后续 text_delta/stop 改写 index 后实时推 live。
//   - message_delta/message_stop:直接推 live(首轮不推两尾帧,故不重复)。
//
// 同步写 replay:供 pull 的完整性判定(finishEmitted||streamTerminated 需看 replay 是否收到 finish_reason)。
// replay 与 live 双写时,先写 replay(原样)再过滤/重映射后写 live。
//
// 设计依据见 plan twinkly-sniffing-lemon.md 核心技术结论:(B) 假闭合旧块+开新块+从头发新正文是 Anthropic
// 协议下"续传不重发"的唯一合法解,客户端可见"草稿段(已闭合)+重启段"两段相邻文字,无 index 冲突、SDK 不报错。
type resumeSink struct {
	live   *flushWriter
	replay *replayWriter

	// 跨轮持久态(只在成功提交时推进;失败轮 reset 后回退到上一轮提交值):
	//   - liveMaxUsedIdx:客户端 live 上已用过的最大 index(已闭合不复用),重试轮新块从此+1 分配。
	//   - liveThinkingOpen:live 上 index 0 thinking 块是否仍开未闭合(首轮断流时由 tee 拷入;
	//     实际首轮 translator closeAll 多已闭合它故常为 false,保留作补闭合兜底)。
	//   - liveBodyOpenIdx:live 上残留未闭合正文块 index(-1 表无);同上多已被 closeAll 闭合故常 -1。
	// 这三个字段在 pending-轮内不被直接改写,而由 pend* 镜像字段在提交时回填(见下)。
	liveMaxUsedIdx   int
	liveThinkingOpen bool
	liveBodyOpenIdx  int

	// 本轮运行期态(reset 每轮清零):
	closedDangling   bool        // 惰性补闭合标志:首个正文 start/tool_use 前补一次;reset 复位
	toolSeen         bool        // 见过 tool_use:此后所有帧只 replay 不推 live
	messageStartSeen bool
	stopSent         bool
	indexMap         map[int]int // 本轮"上游 idx → 客户端 idx"重映射(成功快照回传给 replayFollowingInto)
	pending          bytes.Buffer // 本轮待提交给 live 的字节(补闭合帧 + 重映射正文 start/delta/stop);断流轮 reset 丢弃
	// pend* 是跨轮持久态的本轮镜像:轮内分配/补闭合改写 pend*,提交时回填到 liveMaxUsedIdx 等。
	// 失败轮 reset 后 pend* 重新从持久态初始化,故失败轮的 index 分配/块开闭全被丢弃,客户端态零变更。
	pendMaxIdx     int  // 本轮已分配的最大 index(从 liveMaxUsedIdx 起步)
	pendBodyOpenIdx int // 本轮 pending 中当前未闭合正文块 index(-1 表无)
	pendThinkingOpen bool // 本轮提交后 liveThinkingOpen 的目标值
}

func newResumeSink(live *flushWriter, replay *replayWriter, thinkingOpen bool, bodyOpenIdx, maxUsedIdx int) *resumeSink {
	return &resumeSink{
		live:             live,
		replay:           replay,
		liveMaxUsedIdx:   maxUsedIdx,
		liveThinkingOpen: thinkingOpen,
		liveBodyOpenIdx:  bodyOpenIdx,
		indexMap:         map[int]int{},
	}
}

// reset 跨重试轮复用前的复位:清本轮运行期态(pending/indexMap/closedDangling/toolSeen/尾帧标志 +
// pend* 镜像回退到持久值),保留 liveMaxUsedIdx/liveThinkingOpen/liveBodyOpenIdx——它们反映"截至上一轮
// 成功提交,客户端 live 上的协议态",本轮据此惰性补闭合并分配新块 index。
// 失败轮(未到 message_stop)的 pending/index 分配随 reset 全部丢弃,客户端态零变更。
func (r *resumeSink) reset() {
	r.indexMap = map[int]int{}
	r.closedDangling = false
	r.toolSeen = false
	r.messageStartSeen = false
	r.stopSent = false
	r.pending.Reset()
	r.pendMaxIdx = r.liveMaxUsedIdx
	r.pendBodyOpenIdx = r.liveBodyOpenIdx
	r.pendThinkingOpen = r.liveThinkingOpen
}

// closeDanglingBlocks 惰性补闭合客户端仍未闭合的块:先 thinking(0)→再正文(pendBodyOpenIdx),
// 顺序与 writeNvidiaAnthropicStream 补闭合一致,保证思考块在正文块之前完全闭合。
// 仅在本重试轮首次见到正文 content_block_start / tool_use start 时调一次(closedDangling 守门)。
// 补闭合帧写进 pending(断流轮会随 reset 丢弃,不污染 live)。
func (r *resumeSink) closeDanglingBlocks() {
	if r.closedDangling {
		return
	}
	r.closedDangling = true
	// 先补闭合残留的 thinking 块(index 0):发空串 signature_delta + content_block_stop(0)
	if r.pendThinkingOpen {
		writePendingEvent(&r.pending, "content_block_delta", contentBlockSignatureDeltaPayload(0, ""))
		writePendingEvent(&r.pending, "content_block_stop", contentBlockStopPayload(0))
		r.pendThinkingOpen = false
	}
	// 再补闭合残留的正文块(pendBodyOpenIdx):只发 content_block_stop(正文块无 signature_delta)
	if r.pendBodyOpenIdx >= 0 {
		writePendingEvent(&r.pending, "content_block_stop", contentBlockStopPayload(r.pendBodyOpenIdx))
		r.pendBodyOpenIdx = -1
	}
}

// writeEvent 按帧类型过滤后写 replay + pending(提交时才落 live)。replay 始终原样写。
func (r *resumeSink) writeEvent(event, data string) {
	// replay 始终原样写(供 pull 完整性判定;成功快照时 replayFollowingInto 回放未 live 段)
	r.replay.writeEvent(event, data)

	switch event {
	case "message_start":
		// 首轮已发,重试轮全跳
		r.messageStartSeen = true
		return
	case "content_block_start":
		kind := contentBlockKind(data)
		if kind == "thinking" {
			// 思考段全跳(首轮思考草稿不重发)
			return
		}
		if kind == "tool_use" {
			// 工具块本身不实时推 live(功能正确性约束);锁定此后只 replay。
			// 但仍需惰性补闭合客户端残留的未闭合块(首轮实时推的思考/正文 text 若未闭合断流),
			// closeDanglingBlocks 在首个正文 start 或首个 tool_use start 时都会执行(幂等,只补一次)。
			r.closeDanglingBlocks()
			r.toolSeen = true
			return
		}
		// text 块:惰性补闭合 → 新 index 映射 → 改写 index 后写 pending(提交时落 live)
		if r.toolSeen {
			return
		}
		r.closeDanglingBlocks()
		upIdx := contentBlockIndex(data)
		if upIdx < 0 {
			upIdx = 0
		}
		newIdx := r.pendMaxIdx + 1
		r.indexMap[upIdx] = newIdx
		r.pendMaxIdx = newIdx
		r.pendBodyOpenIdx = newIdx // 记录本轮新开正文块 index
		writePendingEvent(&r.pending, "content_block_start", rewriteContentBlockIndex(data, newIdx))
		return
	case "content_block_delta":
		if r.toolSeen {
			return
		}
		// 思考段的 delta(thinking_delta/signature_delta)全跳;正文 text_delta 改写 index 后写 pending
		if deltaTypeForContentBlockDelta(data) != "text_delta" {
			return
		}
		upIdx := contentBlockIndex(data)
		newIdx, ok := r.indexMap[upIdx]
		if !ok {
			// 无对应开块的 delta:防御丢弃(不应出现——text 块必先有 start 建 map)
			return
		}
		writePendingEvent(&r.pending, "content_block_delta", rewriteContentBlockIndex(data, newIdx))
		return
	case "content_block_stop":
		if r.toolSeen {
			return
		}
		upIdx := contentBlockIndex(data)
		// 思考段 stop(index 0 且 indexMap 无该 idx):全跳
		newIdx, isBody := r.indexMap[upIdx]
		if !isBody {
			return
		}
		if r.pendBodyOpenIdx == newIdx {
			r.pendBodyOpenIdx = -1 // 本轮正文块已闭合,清回 -1
		}
		writePendingEvent(&r.pending, "content_block_stop", rewriteContentBlockIndex(data, newIdx))
		return
	case "message_delta":
		// 尾帧只蓄流不推 live:本轮可能仍会断流(上游未给 finish_reason 即断),若把 message_delta 推 live,
		// 客户端流的语义尾帧就提前落地,后续重试轮再续推正文会违反"流已结束不能再有块"。尾帧统一由
		// 调用方(writeNvidiaAnthropicStream 整体成功后经 replayFollowingInto)一次性补发给 live。
		return
	case "message_stop":
		// 整条 ready 信号之一(但 translator 在上游 error chunk 断流路径也会无条件补 message_stop,
		// 故 message_stop 不能单独作为提交依据——提交由 pull 在确认 finishEmitted||streamTerminated 后
		// 显式调 commitPending() 完成)。幂等(stopSent)防重入。
		if r.stopSent {
			return
		}
		r.stopSent = true
		return
	}
	// 未识别的事件类型:默认不推 live(防御),replay 已写
}

// commitPending 由 pull 在确认本轮整条 ready(sseErr==nil && (finishEmitted||streamTerminated))后显式调用:
// 把 pending 字节一次性刷给 live(滚滚落盘为"重启段"),并把本轮 pend* 镜像回填到持久态(liveMaxUsedIdx 等),
// 供下一轮/调用方快照使用。失败轮(未确认完整)不调此方法,pending 随 reset 丢弃,持久态不变。
func (r *resumeSink) commitPending() {
	if r.pending.Len() > 0 && r.live != nil {
		r.live.writeRaw(r.pending.String())
		r.pending.Reset()
	}
	r.liveMaxUsedIdx = r.pendMaxIdx
	r.liveThinkingOpen = r.pendThinkingOpen
	r.liveBodyOpenIdx = r.pendBodyOpenIdx
}

// writeRaw 原始 SSE 字节:重试轮转译主循环未用到 writeRaw,保留接口对称,只写 replay。
func (r *resumeSink) writeRaw(s string) {
	r.replay.writeRaw(s)
}

func (r *resumeSink) flush() {
	r.replay.flush()
	if r.live != nil {
		r.live.flush()
	}
}

// rewriteContentBlockIndex 把 content_block_start / content_block_delta / content_block_stop 事件 data
// 的顶层 index 字段改写为 newIdx,其余字段原样保留。解析失败则原样返回(防御:不破坏帧)。
func rewriteContentBlockIndex(data string, newIdx int) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return data
	}
	m["index"] = newIdx
	b, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(b)
}

// deltaTypeForContentBlockDelta 从 content_block_delta 事件 data 提取 delta.type。
// 返回 "" 表示解析失败或无 delta 字段。供 resumeSink 区分 text_delta(续推)与 thinking_delta/signature_delta(跳过)。
func deltaTypeForContentBlockDelta(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	delta, _ := m["delta"].(map[string]interface{})
	if delta == nil {
		return ""
	}
	t, _ := delta["type"].(string)
	return t
}

// deltaTextForContentBlockDelta 从 content_block_delta 事件 data 提取 delta.text(text_delta 的正文)。
// 供 teeSink 判断"首个 delta 是否携带实质正文":保底空块(ensureAtLeastOneBlock 发的空 text_delta)
// 的 text 为空,不应触发 flushDeferred(否则断流轮提前 WriteHeader 200、丢失 503 干净失败能力)。
// 解析失败或非 text_delta 返回 ""。
func deltaTextForContentBlockDelta(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	delta, _ := m["delta"].(map[string]interface{})
	if delta == nil {
		return ""
	}
	if t, _ := delta["type"].(string); t != "text_delta" {
		return ""
	}
	s, _ := delta["text"].(string)
	return s
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

// nvidiaImageOcrDescHeader 把 OCR 识别出的纯文本包装成一段带上下文标记的描述,
// 供 downgradeAnthropicImagesToText 把 image 块原地改写为 text 块时使用。
//
// ocrModel 参数化:文案里展示真实使用的 OCR 模型,前端改模型后文案随之变化,
// 与 compat.go Gemini 入站自愈链路的 inline 文案语义完全一致。
func nvidiaImageOcrDescHeader(ocrModel, ocrText string) string {
	if strings.TrimSpace(ocrModel) == "" {
		ocrModel = "gemini-2.5-flash"
	}
	return fmt.Sprintf("\n\n[本地中继服务已自动调用 %s 协助分析了用户发送的截图，内容提取如下：]\n%s\n[图片分析内容结束]\n", ocrModel, ocrText)
}

// imageNotExtractablePlaceholder 用于 image 块无法本地 OCR 时的占位文本(url 类型、
// 空数据、或 OCR 服务不可达)。绝不阻断主请求,确保用户至少能看到"此处有图但未能识别"的信号。
const imageNotExtractablePlaceholder = "[用户发送了一张图片，但本地中继未能识别其内容（OCR 不可用或图源不可直取），请提示用户改用 analyze_clipboard_image 工具或补充文字描述]"

// ocrRecentWindowMessages 是 downwards 降级时"真打上游 OCR"的消息窗口口径。
// 仅 req.Messages 末尾 N 条内的图片在 cache miss 时才真打 gemini 上游;窗口之外的图片
// 只查缓存(命中→复用历史 OCR 文本,未命中→占位文本兜底,绝不重新 OCR)。
//
// 取 10 的依据:Claude Code 客户端无状态,每轮重发完整历史;若窗口全开就会把几十张老图
// 全部重新 OCR 烧爆配额,若窗口过小则用户在长会话里翻回去追问老图时无法复用 OCR 结果。
// 10 条覆盖大多数追问场景下"用户当前关注的消息段",与前端历史面板可视线量级匹配,
// 同时给缓存(成功 TTL 24h)+ LRU 容量上限留出回收空间,兼顾实时性与配额成本。
const ocrRecentWindowMessages = 10

// downgradeAnthropicImagesToText 扫 AnthropicRequest 所有消息的 content 块,
// 把 type=="image" 的块用本地 Gemini OCR 降级:OCR 成功则替换为
// [{"type":"text","text":"<OCR 识别文本>"}];OCR 失败则替换为占位文本。
// 绝不向 NVIDIA 上游直送 image_url,避免非多模态模型解析失败(400)。
//
// 设计要点:
//   - 原地替换 block(blocks[bi]),不动数组顺序、不增删 block,保证 [Image #N] 芯片
//     与 text/tool_result 块的相对位置不变,后续 AnthropicToOpenAIChat 的 text 合并 +
//     tool_result 拆分逻辑零变更。
//   - 降级后 Type=="text"、Source 置空 → AnthropicToOpenAIChat 走 case "text" 正常消化,
//     ChatMessage.Content 永远是 string,上游段零侵入。
//   - 失败不中止:返回 error 供调用方记日志,但仍把 block 改写成占位文本后继续,优先保证可用性。
//
// 返回:成功降级的 image 块数 + 遇到的最后一个错误(若有) + ocrHits/ocrMisses/ocrSkipped。
// ocrHits   = 命中缓存直接返回(窗口内命中,纳秒级不烧配额) + 窗口外缓存复用的总数;
// ocrMisses = 窗口内 cache miss 真打 gemini 上游的图数(含成功与失败);
// ocrSkipped = 窗口外图缓存未命中 → 走占位文本兜底的块数(绝不重新 OCR,省配额)。
//
// 最近 N 条消息窗口:仅对 req.Messages 末尾 ocrRecentWindowMessages 条内的图片走"miss 即真打上游";
// 窗口之外的图片只查缓存(ocrImageCacheOnlyLookup):命中则复用历史 OCR 文本(不烧配额),
// 未命中写占位文本兜底。这样既防 NVIDIA 上游 400(永远只见 text 块),又避免 Claude Code 每轮
// 重发完整历史时把几十张老图全部重新 OCR 烧爆 antigravity 号池配额。
func (h *APICompatHandler) downgradeAnthropicImagesToText(req *AnthropicRequest, userSession *RelaySession) (replaced int, lastErr error, ocrHits, ocrMisses, ocrSkipped int) {
	if req == nil {
		return 0, nil, 0, 0, 0
	}
	// 窗口起点:消息数 <= 窗口口径时全覆盖;> 窗口口径时只覆盖末尾 N 条,前序消息内的图视为"窗外"。
	msgCount := len(req.Messages)
	windowStart := 0
	if msgCount > ocrRecentWindowMessages {
		windowStart = msgCount - ocrRecentWindowMessages
	}
	var ocrModel string
	for mi := range req.Messages {
		inWindow := mi >= windowStart
		// 收集同消息或上下文的用户文案
		var userTextBuilder strings.Builder
		for _, b := range req.Messages[mi].Content {
			if b.Type == "text" && b.Text != "" {
				if userTextBuilder.Len() > 0 {
					userTextBuilder.WriteString("\n")
				}
				userTextBuilder.WriteString(b.Text)
			}
		}
		if userTextBuilder.Len() == 0 && mi > 0 {
			for prev := mi - 1; prev >= 0; prev-- {
				if req.Messages[prev].Role == "user" {
					for _, b := range req.Messages[prev].Content {
						if b.Type == "text" && b.Text != "" {
							if userTextBuilder.Len() > 0 {
								userTextBuilder.WriteString("\n")
							}
							userTextBuilder.WriteString(b.Text)
						}
					}
					if userTextBuilder.Len() > 0 {
						break
					}
				}
			}
		}
		userPromptCtx := userTextBuilder.String()

		blocks := req.Messages[mi].Content
		for bi := range blocks {
			if blocks[bi].Type != "image" || blocks[bi].Source == nil {
				continue
			}
			src := blocks[bi].Source
			mime := src.MediaType
			if mime == "" {
				mime = "image/jpeg"
			}
			// 非 base64(如 url 类型)本机无法直取 → 占位文本兜底,不调 OCR,不计数。
			if src.Type != "base64" || src.Data == "" {
				blocks[bi].Source = nil
				blocks[bi].Type = "text"
				blocks[bi].Text = imageNotExtractablePlaceholder
				continue
			}
			// 窗外历史图:只查缓存复用,绝不重新打上游。命中→复用历史 OCR 文本(replaced+1);
			// 未命中→占位文本兜底,跳过(ocrSkipped+1),省下昂贵的 antigravity 号池 ORC 配额。
			if !inWindow {
				cachedText, hit := h.ocrImageCacheOnlyLookup(userSession, src.Data, userPromptCtx)
				if hit && strings.TrimSpace(cachedText) != "" {
					if ocrModel == "" {
						ocrModel = h.getOcrModel()
					}
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = nvidiaImageOcrDescHeader(ocrModel, cachedText)
					replaced++
					ocrHits++
				} else {
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = imageNotExtractablePlaceholder
					ocrSkipped++
				}
				continue
			}
			// 窗内图:走完整缓存+singleflight+miss 真打上游链路。
			ocrText, ocrErr, cachedHit := h.ocrImageViaLocalGemini(userSession, src.Data, mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil || strings.TrimSpace(ocrText) == "" {
				lastErr = ocrErr
				blocks[bi].Source = nil
				blocks[bi].Type = "text"
				blocks[bi].Text = imageNotExtractablePlaceholder
				continue
			}
			if ocrModel == "" {
				ocrModel = h.getOcrModel()
			}
			blocks[bi].Source = nil
			blocks[bi].Type = "text"
			blocks[bi].Text = nvidiaImageOcrDescHeader(ocrModel, ocrText)
			replaced++
		}
	}
	return replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
}
