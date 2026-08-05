package relay

import (
	"encoding/json"
	"fmt"
	"strings"

	"antigravity-proxy/internal/settings"
)

// passthrough_anthropic.go: Other 号池「上游 Anthropic 原生端点」的协议双向转译。
//
// 与 NVIDIA 链路(nvidia_translate_*.go)的关系:NVIDIA 链路把入站 Anthropic 转成 OpenAI 发往 NVIDIA 上游,
// 响应再 OpenAI→Anthropic 回译;本文件承接的是「上游本身就是 Anthropic 原生端点(/v1/messages)」的场景:
//   - 入站 OpenAI Chat / Responses → 上游 Anthropic Messages(OpenAIToAnthropicMessages);
//   - 入站 Anthropic → 上游 Anthropic(原样透传,仅 model 改写,见 passthroughForward.buildUpstreamBody);
// 响应回译方向同理:上游 Anthropic 响应 → 入站 OpenAI Chat 客户端(AnthropicResponseToOpenAIChat)。
//
// 复用现有 OpenAI↔Anthropic 转译函数(AnthropicToOpenAIChat / OpenAIChatToAnthropic / OpenAIChatSSEToAnthropicSSE)
// 的字段映射成果,本文件只补「OpenAI Chat 请求 → Anthropic Messages 请求」与「Anthropic 响应 → OpenAI Chat 响应」
// 这两个 NVIDIA 链路未曾需要的方向,保证 Other 号池 Anthropic 格式组的完整闭环。

// OpenAIToAnthropicMessages 把入站 OpenAI Chat(或 Responses)请求体转译为 Anthropic Messages 请求体。
// isResponses=true 时 body 先经 ResponsesToOpenAIChat 归一化为 OpenAIChatRequest,再走同一转译路径。
//
// 字段映射要点(与 AnthropicToOpenAIChat 严格对偶):
//   - system 消息(role=system)抽出合并为顶层 system 字符串(多条以 \n\n 拼接);
//   - assistant/user/tool 消息 → Anthropic messages,文本走 text 块,tool_calls → tool_use 块,
//     tool 角色消息 → user 角色含 tool_result 块(对偶 AnthropicToOpenAIChat 的 tool_result→tool 映射);
//   - tools(OpenAI function tools)→ Anthropic tools{name,input_schema};
//   - max_tokens / temperature / stream 透传(Anthropic MaxTokens 必填,OpenAI 留空时给一个兜底大值)。
//
// 思考注入(Other 号池 Anthropic 上游组专用,官方格式):
//   入站 OpenAI 顶层 reasoning_effort(或 reasoning.effort)→ Anthropic 官方 thinking 字段
//   {type:"enabled", budget_tokens:N}。分档预算:low→4000 / medium→12000 / high→32000 / max→64000。
//   守护 Anthropic「max_tokens > budget_tokens」不变式:若 budget>=max_tokens,把 max_tokens 抬到
//   budget+ClaudeBudgetMargin。opt-in OFF(reasoning_effort 空/none/off/disabled)→ 不注入 thinking。
func OpenAIToAnthropicMessages(bodyBytes []byte, upstreamModel string, isResponses bool) (*AnthropicRequest, error) {
	var chatReq OpenAIChatRequest
	if isResponses {
		u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("responses->openai pre-transform failed: %w", err)
		}
		chatReq = *u
	} else {
		if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
			return nil, fmt.Errorf("invalid openai chat request: %w", err)
		}
	}

	out := &AnthropicRequest{
		Model:       upstreamModel,
		Stream:      chatReq.Stream,
		MaxTokens:   chatReq.MaxTokens,
		Temperature: chatReq.Temperature,
	}

	// max_tokens 兜底:Anthropic 必填,OpenAI 留空时给一个安全大值(避免上游 400 invalid_request_error)。
	if out.MaxTokens == nil || *out.MaxTokens == 0 {
		fallback := 8192
		out.MaxTokens = &fallback
	}

	// 思考注入:入站 reasoning_effort → Anthropic thinking.budget_tokens(官方格式,对偶
	// AnthropicToOpenAIChat 的 reasoning_effort 注入)。仅 Other anthropic 组走本函数,故无 NIM 顾虑。
	effort := extractOpenAIReasoningEffort(bodyBytes)
	if budget := mapReasoningEffortToAnthropicBudget(effort); budget > 0 {
		out.Thinking = &AnthropicThinking{Type: "enabled", BudgetTokens: budget}
		// 守护「max_tokens > budget_tokens」:Anthropic 严格校验,违反返回 400
		// ("max_tokens must be greater than thinking.budget_tokens")。budget>=max_tokens 时抬升 max_tokens。
		cur := 0
		if out.MaxTokens != nil {
			cur = *out.MaxTokens
		}
		if cur <= budget {
			raised := budget + ClaudeBudgetMargin
			out.MaxTokens = &raised
		}
	}

	// system 消息抽出合并为顶层 system 字符串;非 system 消息按角色转译。
	var systemParts []string
	for _, msg := range chatReq.Messages {
		switch msg.Role {
		case "system":
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "assistant":
			out.Messages = append(out.Messages, openAIAssistantToAnthropic(msg))
		case "tool":
			// OpenAI tool 角色消息(对应 Anthropic 的 tool_result)→ 转成 user 角色含 tool_result 块。
			out.Messages = append(out.Messages, openAIToolToAnthropic(msg))
		default: // user / 其它
			out.Messages = append(out.Messages, openAIUserToAnthropic(msg))
		}
	}
	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n\n")
	}

	// tools 转换:OpenAI function tools → Anthropic tools{name,input_schema}。
	for _, t := range chatReq.Tools {
		out.Tools = append(out.Tools, AnthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	// tool_choice 透传:OpenAI tool_choice 形态与 Anthropic 不同,二者都用 RawMessage 原样保留,
	// 由 passthroughForward.buildUpstreamBody 在透传分支原样写入;此处不强行改写避免破坏语义。

	return out, nil
}

// openAIAssistantToAnthropic 把 OpenAI assistant 消息转成 Anthropic assistant 消息(text + tool_use 块)。
// 与 nvidia_translate_response.go 的 openAIChoiceMessageToAnthropic 对偶,但作用于请求侧(入站)。
func openAIAssistantToAnthropic(m ChatMessage) AnthropicMessage {
	var blocks []AnthropicContent
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, AnthropicContent{Type: "text", Text: m.Content})
	}
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
			id = fmt.Sprintf("toolu_other_%d", i)
		}
		blocks = append(blocks, AnthropicContent{
			Type:  "tool_use",
			ID:    id,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	if len(blocks) == 0 {
		// Anthropic 要求 assistant 消息至少有一个 content 块;空内容兜底一个空 text 块。
		blocks = []AnthropicContent{{Type: "text", Text: ""}}
	}
	return AnthropicMessage{Role: "assistant", Content: blocks}
}

// openAIUserToAnthropic 把 OpenAI user 消息转成 Anthropic user 消息(文本块)。
// 入站 OpenAI Chat 的 content 是 string(ChatMessage.Content),直接转 text 块;
// 数组形态 content 已在 buildPassthroughUpstreamReq 的 Unmarshal 阶段规整为 string,
// 若保留 RawMessage 数组(如 image_url)此处暂不转(Other 号池 Anthropic 上游若支持多模态需另写)。
func openAIUserToAnthropic(m ChatMessage) AnthropicMessage {
	return AnthropicMessage{
		Role: "user",
		Content: []AnthropicContent{
			{Type: "text", Text: m.Content},
		},
	}
}

// openAIToolToAnthropic 把 OpenAI tool 角色消息(函数执行结果)转成 Anthropic user 角色含 tool_result 块。
// OpenAI: {role:"tool", content:"<结果文本>", tool_call_id:"xxx", tool_name:"yyy"}
// Anthropic: {role:"user", content:[{type:"tool_result", tool_use_id:"xxx", content:"<结果文本>"}]}
func openAIToolToAnthropic(m ChatMessage) AnthropicMessage {
	isErr := false
	// OpenAI 无明确的 is_error 语义,按 content 是否含 "error" 关键字粗判(保守:仅明显错误才标 true)。
	if strings.Contains(strings.ToLower(m.Content), "error:") || strings.Contains(strings.ToLower(m.Content), "execution failed") {
		isErr = true
	}
	block := AnthropicContent{
		Type:      "tool_result",
		ToolUseID: m.ToolCallID,
		IsError:   &isErr,
	}
	if m.Content != "" {
		// content 字段兼容 string 或 []block;序列化为 JSON 字符串最简单(Anthropic 允许 tool_result.content 为 string)。
		raw, _ := json.Marshal(m.Content)
		block.ToolResultContent = raw
	}
	return AnthropicMessage{
		Role:    "user",
		Content: []AnthropicContent{block},
	}
}

// AnthropicResponseToOpenAIChat 把上游 Anthropic 非流式响应转译为 OpenAI Chat 非流式响应。
// 用于「入站 OpenAI Chat + 上游 Anthropic 格式组」场景的响应回译。
func AnthropicResponseToOpenAIChat(resp *AnthropicResponse) *OpenAIChatResponse {
	out := &OpenAIChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []OpenAIChatChoice{},
		Usage: OpenAIChatUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	if out.ID == "" {
		out.ID = "chatcmpl-other"
	}
	if out.Model == "" {
		out.Model = "other"
	}
	if len(resp.Content) > 0 {
		choice := OpenAIChatChoice{
			Index:        0,
			FinishReason: anthropicStopToOpenAIFinish(resp.StopReason),
			Message:      anthropicContentToChatMessage(resp.Content),
		}
		out.Choices = append(out.Choices, choice)
	} else {
		out.Choices = append(out.Choices, OpenAIChatChoice{
			Index:        0,
			FinishReason: "stop",
			Message:      ChatMessage{Role: "assistant", Content: ""},
		})
	}
	return out
}

// anthropicStopToOpenAIFinish 把 Anthropic stop_reason 映射为 OpenAI finish_reason(对偶 openAIFinishToAnthropicStop)。
func anthropicStopToOpenAIFinish(stop string) string {
	switch strings.ToLower(strings.TrimSpace(stop)) {
	case "end_turn", "":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}

// anthropicContentToChatMessage 把 Anthropic content 块数组聚合为一个 OpenAI ChatMessage。
// text 块拼到 Content;thinking 块写入 ReasoningContent(对齐 OpenAI 官方 reasoning_content 字段);
// tool_use 块转 ToolCalls。
//
// 既往缺陷:原实现 `case "text","thinking": sb.WriteString(b.Text)` 把 thinking 块也走 b.Text,
// 而 Anthropic thinking 块的内容存在 b.Thinking 字段(兼容 Anthropic 官方协议:thinking_delta.payload.thinking),
// b.Text 恒空 → 思考内容被静默丢弃。此处修正为 thinking 块读 b.Thinking、写 ReasoningContent,
// 与流式路径 anthropicSSEToOpenAIChatSSEInto 的 thinking_delta → delta.reasoning_content 翻译对齐。
// 同时回填 Reasoning 兜底字段,对偶 nvidia_translate_sse.go:131-138 的 reasoning 字段兜底语义。
func anthropicContentToChatMessage(blocks []AnthropicContent) ChatMessage {
	var sb strings.Builder
	var tools []ChatToolCall
	var reasoningSb strings.Builder
	for i, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "thinking":
			// 思考内容存在 b.Thinking(非 b.Text);空串/纯空白 thinking 不计入避免污染 ReasoningContent。
			if strings.TrimSpace(b.Thinking) != "" {
				reasoningSb.WriteString(b.Thinking)
			}
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			id := b.ID
			if id == "" {
				id = fmt.Sprintf("toolu_other_%d", i)
			}
			tools = append(tools, ChatToolCall{
				Index: i,
				ID:    id,
				Type:  "function",
				Function: ChatToolCallFunction{
					Name:      b.Name,
					Arguments: string(args),
				},
			})
		}
	}
	msg := ChatMessage{
		Role:      "assistant",
		Content:   sb.String(),
		ToolCalls: tools,
	}
	if reasoningSb.Len() > 0 {
		msg.ReasoningContent = reasoningSb.String()
		msg.Reasoning = reasoningSb.String()
	}
	return msg
}

// AnthropicSSEToOpenAIChatSSE 把上游 Anthropic Messages SSE 流实时重写为 OpenAI Chat Completions SSE 流。
// reader 读上游 Anthropic SSE,writer 写 OpenAI Chat SSE。返回累计 input/output tokens。
//
// 协议事件序列对偶(Anthropic SSE → OpenAI SSE):
//
//	message_start → (首帧,发 role:assistant + 可选空 content)
//	content_block_start(text) → delta.content 增量
//	content_block_delta(text_delta) → delta.content 增量
//	content_block_start(tool_use) + input_json_delta → delta.tool_calls 增量
//	content_block_stop → (块结束,OpenAI 无对应事件,跳过)
//	message_delta(usage) → 末尾 usage chunk
//	message_stop → finish_reason=stop + [DONE]
//
// 设计为薄函数,错误时仍补一个 finish + [DONE] 尾帧,避免客户端卡等。
func AnthropicSSEToOpenAIChatSSE(reader interface{ Read(p []byte) (int, error) }, writer interface {
	Write(p []byte) (int, error)
}, model string) (input, output int, err error) {
	// 复用现有 sseBlockStates / flushWriter 的事件驱动基础设施风险较大(它们面向 Anthropic 输出侧),
	// 这里采用自包含的逐行扫描重写,逻辑直观可控,与 OpenAIChatSSEToAnthropicSSE 的对称结构对偶。
	return anthropicSSEToOpenAIChatSSEInto(reader, writer, model)
}

// 向后兼容占位:fetchChannelAvailableModels 等不直接依赖本文件的 mapping 透传,
// 但保持 settings 包可见(防止未来访问器挪动时本文件 import 被自动裁剪)。
var _ = settings.ModelMappingEntry{}

// mapReasoningEffortToAnthropicBudget 把 OpenAI 官方 reasoning_effort 思考档位映射成
// Anthropic 官方 thinking.budget_tokens 预算(单位 token)。返回 0 表示不注入思考
// (opt-in OFF / effort 空 / none|off|disabled 经 normalizeEffort 归一为空)。
//
// 分档预算(均 > Anthropic 最小 1024 且对齐主流推理档),对偶 AnthropicToOpenAIChat 的
// resolveReasoningEffort 把 thinking.budget_tokens 反向分档的同类语义:
//   low → 4000   medium → 12000   high → 32000   max → 64000
// 预算语义:Anthropic thinking.budget_tokens 即「思考 token 上限」,与 OpenAI reasoning_effort
// 的「思考强度档」语义对齐 —— low 偏轻量、high/max 偏重度。max 同样映射到固定 64000(避免无限大预算
// 触发上游「max_tokens > budget_tokens」不变式时被迫把 max_tokens 抬到不可接受量级)。
func mapReasoningEffortToAnthropicBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "minimal":
		return 4000
	case "medium":
		return 12000
	case "high":
		return 32000
	case "max", "xhigh":
		return 64000
	default:
		return 0
	}
}
