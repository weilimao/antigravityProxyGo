package relay

import (
	"encoding/json"
	"fmt"
	"strings"

	"antigravity-proxy/internal/settings"
)

// nvidia_translate_request.go: Anthropic Messages -> OpenAI Chat 请求方向转换。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// AnthropicToOpenAIChat 把 Anthropic Messages 请求翻译成 OpenAI Chat Completions 请求。
// 字段映射要点：
//   - system(字符串/已由 AnthropicRequest.UnmarshalJSON 规整) → messages[0]{role:system}
//   - 消息 content blocks: text→content 字符串；tool_use→tool_calls；tool_result→role=tool
//   - tools: AnthropicTool{name,input_schema} → OpenAI tools{type:function,function:{name,description,parameters}}
//   - max_tokens / temperature / stream 透传
//
// 思考注入决策(两层,修复 commit 88cf8c8 引入的 redact-thinking 头误判):
//   第一层 thinkingRequested(req):客户端是否显式开思考(body thinking.type∈{enabled,adaptive})。
//     Claude Code 2.1.220 实测:开思考带 thinking.type=adaptive/enabled;关思考直接省略 thinking 字段。
//   第二层 IsEnableThinkingMode():代理全局总开关(UI 可关,strip 所有 CoT)。
//   effort(output_config.effort / thinking.budget_tokens)在第一层判为 ON 时再取值定档,
//   绝不单独驱动 on/off —— 修复 CLI 关思考态 body 仍带 output_config:{effort:max} 被误开思考的次生坑。
//   推理模型(glm-5.2)在客户端未显式请求时不 fallback 强开,尊重 opt-in 语义。
//   Anthropic-Beta 头里的 redact-thinking-* 不再参与本决策(该头在 claude-cli 2.1.220 开/关两态均常驻,
//   与思考 on/off 无关;官方未文档化该头,关思考的正路是 body thinking.type=disabled 或省略 thinking 字段)。
func AnthropicToOpenAIChat(req *AnthropicRequest, mappings ...[]settings.ModelMappingEntry) (*OpenAIChatRequest, error) {
	// 默认不保留 image 块(字符串 content)。多模态上游由调用方显式走 AnthropicToOpenAIChatPreservingImages。
	return anthropicToOpenAIChat(req, false, mappings...)
}

// AnthropicToOpenAIChatPreservingImages 构造 Anthropic→OpenAI 转换,并在 preserveImages=true 时
// 把 image 块转译为 OpenAI Chat Vision 数组形态 content(image_url 块),使多模态上游真正收图。
// 由降级闸判定上游多模态的调用方(nvidia.go / passthrough_forwarder.go)显式传入 true;
// false 时与 AnthropicToOpenAIChat 完全等价(旧行为,字符串 content,image 块走 text 兜底)。
func AnthropicToOpenAIChatPreservingImages(req *AnthropicRequest, preserveImages bool, mappings ...[]settings.ModelMappingEntry) (*OpenAIChatRequest, error) {
	return anthropicToOpenAIChat(req, preserveImages, mappings...)
}

func anthropicToOpenAIChat(req *AnthropicRequest, preserveImages bool, mappings ...[]settings.ModelMappingEntry) (*OpenAIChatRequest, error) {
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
			out.Messages = append(out.Messages, anthropicUserToChat(msg, preserveImages)...)
		default:
			// 其它角色按 user 处理
			out.Messages = append(out.Messages, anthropicUserToChat(msg, preserveImages)...)
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

	// 思考注入决策(三层):
	//   第一层 thinkingRequested(req):客户端是否显式开思考(body thinking.type∈{enabled,adaptive})。
	//   第二层 IsEnableThinkingMode():代理全局总开关(UI 可关,strip 所有 CoT)。
	//   第三层按目标号池分流:
	//     a) Other 号池(isOtherProviderTarget):走官方 OpenAI 顶层 reasoning_effort 字段,
	//        绝不碰 NIM 专属 chat_template_kwargs —— 各第三方上游思考参数格式各异(阿里云 DeepSeek v4 要 bool、
	//        NIM 要字符串、有的根本不认),强塞会触发上游 400 "Input should be a valid boolean" 等类型不匹配。
	//        官方 reasoning_effort 是 OpenAI 原生约定,认 OpenAI 兼容字段的上游(OpenRouter/部分中继)即认它。
	//     b) isNvidiaModelNoKwargs:NIM 模型 chat_template_kwargs 被禁用(InjectChatTemplateKwargs=false)→ 抑制。
	//     c) 其余(NIM 池 kwargs 开启):注入 chat_template_kwargs(原有逻辑,维持 NVIDIA 链路零回归)。
	// effort 在第一层判为 ON 时再取值定档(mapToOfficialOpenAIEffort / mapReasoningEffort),
	// 绝不单独驱动 on/off —— 修复 CLI 关思考态 body 仍带 output_config:{effort:max} 被误开思考的次生坑。
	// 推理模型(glm-5.2)在客户端未显式请求时不 fallback 强开,尊重 opt-in 语义。
	// Anthropic-Beta 头里的 redact-thinking-* 不再参与本决策(该头在 claude-cli 2.1.220 开/关两态均常驻,
	// 与思考 on/off 无关;官方未文档化该头,关思考的正路是 body thinking.type=disabled 或省略 thinking 字段)。
	if !IsEnableThinkingMode() || !thinkingRequested(req) {
		// 无思考信号(opt-in OFF 或全局关)→ 两种参数都不注入,上游行为不变。
		out.ChatTemplateKwargs = nil
		out.ReasoningEffort = ""
	} else if isOtherProviderTarget(req.Model, mappings...) {
		// Other 号池 → 官方 OpenAI reasoning_effort 顶层字段。
		effort := resolveReasoningEffort(req)
		out.ChatTemplateKwargs = nil // Other 池绝不注入 NIM 专属 kwargs
		if mapped := mapToOfficialOpenAIEffort(effort); mapped != "" {
			out.ReasoningEffort = mapped
		}
	} else if isNvidiaModelNoKwargs(req.Model, mappings...) {
		// NIM 模型 kwargs 被禁用 → 抑制(原有语义,NVIDIA 链路零回归)。
		out.ChatTemplateKwargs = nil
		out.ReasoningEffort = ""
	} else {
		// NIM 池 kwargs 开启 → 注入 chat_template_kwargs(原有逻辑)。
		effort := resolveReasoningEffort(req)
		if mapped := mapReasoningEffort(effort, nvidiaThinkingEffortMode(req.Model)); mapped != "" {
			out.ChatTemplateKwargs = map[string]interface{}{
				"thinking":         true,
				"reasoning_effort": mapped,
			}
		} else {
			// thinking 已 ON 但档解析为空(理论兜底):只开思考不定档,让上游按默认档出。
			out.ChatTemplateKwargs = map[string]interface{}{"thinking": true}
		}
	}

	return out, nil
}

// thinkingRequested 判定客户端是否显式请求思考(ON)。
// Claude Code 2.1.220 实测:开思考 body 带 thinking.type=enabled|adaptive;
// 关思考直接省略 thinking 字段(不发 disabled)。effort(output_config.effort)仅定档,不定 on/off,
// 故本函数不看 effort。disabled、缺省、未识别 type → 一律 OFF(opt-in:不显式开即不强开)。
func thinkingRequested(req *AnthropicRequest) bool {
	if req == nil || req.Thinking == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
	case "enabled", "adaptive":
		return true
	default: // disabled / omitted / 未识别
		return false
	}
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

// injectNvidiaChatTemplateKwargs 给 OpenAI Chat 入站(直连或 Codex chat-completions)的请求,
// 把客户端发的思考等级透传成 NIM 认的 chat_template_kwargs。
// 从原始入站 bodyBytes 提取 reasoning_effort(顶层,Codex 形态)或 reasoning.effort(OpenRouter 形态),
// 按 NIM 上游取值模式映射后注入 chat_template_kwargs:{thinking:true, reasoning_effort:<mapped>}。
//
// 注入决策(两层,与 AnthropicToOpenAIChat 对齐,修复 commit 88cf8c8 引入的 redact-thinking 头误判):
//   第一层 IsEnableThinkingMode()(全局总闸)。
//   OpenAI 协议无 body `thinking` 字段,其思考显式信号即 reasoning_effort —— 有即 ON,
//   无或缺省即 OFF(opt-in:客户端不发 reasoning_effort 不强开思考)。
//   none/off/disabled 形态由 normalizeEffort 归一为空串 → 落入"无"分支 → 不注入(关思考语义)。
//   不再对推理模型做"无 effort 也 fallback 注入 thinking:true" —— 与 Anthropic 链 opt-in 语义一致。
//   redact-thinking-* 头不参与(OpenAI 入站本无 Anthropic 头部协议,直连 Codex 恒无该头)。
//
// 设计要点:reasoning_effort 不是 OpenAIChatRequest 字段(顶层该字段 NIM 不认,会 400),
// 既不在结构体里接、也不往上游顶层发,只在原始 body 里提后转进 chat_template_kwargs。
func injectNvidiaChatTemplateKwargs(chatReq *OpenAIChatRequest, bodyBytes []byte, upstreamModel string, mappings ...[]settings.ModelMappingEntry) {
	if chatReq == nil {
		return
	}
	// NVIDIA NIM 上游只认 chat_template_kwargs,顶层 reasoning_effort 会被 NIM 当未知字段拒收(400)。
	// 故 NIM 链路必须把客户端原发在顶层的 reasoning_effort 清空,绝不透传给 NIM 上游。
	// (顶层 reasoning_effort 是 Other 号池 OpenAI 格式组的官方注入字段,NIM 链路禁用。)
	chatReq.ReasoningEffort = ""
	if !IsEnableThinkingMode() || isNvidiaModelNoKwargs(upstreamModel, mappings...) {
		chatReq.ChatTemplateKwargs = nil
		return
	}
	effort := extractOpenAIReasoningEffort(bodyBytes)
	if effort == "" {
		// 无 reasoning_effort(opt-in OFF)或显式 none/off/disabled(normalizeEffort 归一为空)→ 不注入。
		chatReq.ChatTemplateKwargs = nil
		return
	}
	if mapped := mapReasoningEffort(effort, nvidiaThinkingEffortMode(upstreamModel)); mapped != "" {
		chatReq.ChatTemplateKwargs = map[string]interface{}{
			"thinking":         true,
			"reasoning_effort": mapped,
		}
		return
	}
	// effort 非空但映射后为空(如未识别档):回落不注入,避免往上游塞空 reasoning_effort 触发 400。
	chatReq.ChatTemplateKwargs = nil
}

// isNvidiaModelNoKwargs 判定模型映射配置是否显式禁用 chat_template_kwargs 思考参数。
// 匹配规则:
//   - 在 mappings 中匹配 ClientModel 或 TargetModel,命中后:
//     · TargetProvider=="other" 一律禁注入(chat_template_kwargs 是 NVIDIA NIM 专属约定,
//       各第三方上游思考参数格式各异——阿里云 DeepSeek v4 要 bool、NIM 要字符串、有的根本不认;
//       默认强塞会触发上游 400 "Input should be a valid boolean" 等类型不匹配报错)。
//     · InjectChatTemplateKwargs 显式配置为 false 时禁注入(其余号池沿用既有口径)。
func isNvidiaModelNoKwargs(model string, mappings ...[]settings.ModelMappingEntry) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if len(mappings) > 0 {
		for _, entry := range mappings[0] {
			if strings.EqualFold(entry.ClientModel, m) || strings.EqualFold(entry.TargetModel, m) {
				if strings.EqualFold(strings.TrimSpace(entry.TargetProvider), "other") {
					return true
				}
				if !entry.ShouldInjectChatTemplateKwargs() {
					return true
				}
			}
		}
	}
	return false
}

// isOtherProviderTarget 判定模型映射目标是否为 Other 号池(TargetProvider=="other")。
// 与 isNvidiaModelNoKwargs 同源的 mapping 查找(匹配 ClientModel 或 TargetModel),
// 但语义独立:仅返回「目标是否为 Other 号池」,不关心 InjectChatTemplateKwargs 配置。
// 供 AnthropicToOpenAIChat 的思考注入三分支决策使用——Other 号池走官方 OpenAI reasoning_effort,
// 与 NVIDIA NIM 专属 chat_template_kwargs 区分。无 mapping 命中时返回 false(非 Other,走原 NIM 链路)。
func isOtherProviderTarget(model string, mappings ...[]settings.ModelMappingEntry) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if len(mappings) == 0 {
		return false
	}
	for _, entry := range mappings[0] {
		if strings.EqualFold(entry.ClientModel, m) || strings.EqualFold(entry.TargetModel, m) {
			if strings.EqualFold(strings.TrimSpace(entry.TargetProvider), "other") {
				return true
			}
		}
	}
	return false
}

// mapToOfficialOpenAIEffort 把内部规范化思考等级(low/medium/high/max)映射为
// OpenAI 官方 reasoning_effort 取值。返回空串表示不注入(opt-in OFF 或未识别)。
//
// OpenAI 官方 reasoning_effort 取值集为 {"low","medium","high"}(部分新一代推理模型支持 "minimal")。
// 本函数统一映射为官方全集里最稳的三档:max→high(官方无 max,顶级即 high),其余 1:1。
// 不产 "minimal" —— 仅部分 o-series 支持,通用 OpenAI 兼容中继未必认,保守起见落 low。
//
// 输入空串(opt-in OFF / 显式 none|off|disabled 经 normalizeEffort 归一为空)→ 返回空串:
// 思考等级的 on/off 由上层 thinkingRequested 决定,本函数只定档,故空入即空出,绝不替客户端开思考。
func mapToOfficialOpenAIEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "max", "xhigh":
		return "high" // 官方无 max,顶级克制档映射为 high
	default:
		return "" // 空串/未识别 → 不注入
	}
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
//
// preserveImages=true 时(多模态上游,调用方已确认):
//   - image 块转译为 OpenAI Chat Vision 数组形态 content 的 image_url 块(data: URL / http(s) URL);
//   - text 与 image 块按原始 content 顺序交错收集进单一 parts 切片,合并为一条 ContentParts
//     数组消息(非字符串 content),确保 text-first / image-first / 多图多文交错均不丢字段、不乱序;
//   - 使多模态上游真正收到图片,而非走旧默认分支(b.Text 恒空 → 图片被静默丢弃)。
// preserveImages=false 时行为与旧版完全一致:text 合并为字符串 content,image 块走 text 兜底(b.Text 恒空静默丢弃)。
func anthropicUserToChat(msg AnthropicMessage, preserveImages bool) []ChatMessage {
	var toolResults []ChatMessage
	var sb strings.Builder
	// parts 在 preserveImages=true 时承载 user content 块的交错数组形态(OpenAI Chat Vision):
	// text 与 image 块按原始 content 顺序入栈,确保 text-first / image-first / 多图多文交错
	// 全部不丢字段、不乱序。无图时(parts 仅含 text 或为空)走字符串 Content 兜底,数组形态被丢弃。
	var parts []ChatMessageContentPart
	hasText := false
	hasImage := false
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
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
			if preserveImages {
				// 交错收集:text 块按原始 content 顺序入栈 parts,与 image 块同批保序。
				parts = append(parts, ChatMessageTextPart{Type: "text", Text: b.Text})
			}
			hasText = true
		case "image":
			if preserveImages {
				parts = append(parts, ChatMessageImageURLPart{
					Type: "image_url",
					ImageURL: ChatMessageImageURLPartObject{
						URL: imageBlockToDataURL(b),
					},
				})
				hasImage = true
			} else {
				// 非多模态保真:旧默认行为,image 块 b.Text 恒空,静默丢弃走 text 兜底。
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
				hasText = true
			}
		default:
			// 其它类型(think 残留等)按 text 提取，避免丢字段
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
			if preserveImages {
				parts = append(parts, ChatMessageTextPart{Type: "text", Text: b.Text})
			}
			hasText = true
		}
	}
	// 先放 tool 结果，再放普通文本；顺序与 OpenAI 期待一致
	res := toolResults
	if hasImage {
		// 数组形态:parts 已按原始 content 块交错顺序收集 text+image(无重排),
		// 多模态上游原样消化。Content 同写 sb.String() 作字符串兜底(有 ContentParts 时
		// MarshalJSON 输出数组、Text() 取 parts 文本,Content 仅兜底不参与序列化)。
		res = append(res, ChatMessage{
			Role:         "user",
			Content:      sb.String(),
			ContentParts: parts,
		})
	} else if hasText {
		res = append(res, ChatMessage{Role: "user", Content: sb.String()})
	}
	if len(res) == 0 {
		// 整条 user 消息无可见内容，补一个空 user 占位以免上游 400
		res = append(res, ChatMessage{Role: "user", Content: ""})
	}
	return res
}

// imageBlockToDataURL 把 Anthropic image content block 转成 OpenAI Chat Vision image_url 认的 URL。
// Anthropic image 块两种 source.type:
//   - "base64":{media_type:"image/png", data:"<b64>"} → 转 "data:<media_type>;base64,<data>" 内联 URL;
//   - "url":{url:"https://..."} → 直接透传该 http(s) URL(OpenAI image_url.url 接受网络地址,免 base64 下载)。
// 其它/缺省 source(type 非空且非 base64/url、media_type 空)按 base64 分支兜底,避免误传空 data URL。
func imageBlockToDataURL(b AnthropicContent) string {
	if b.Source == nil {
		return ""
	}
	// url 源:透传原始 http(s) 地址,OpenAI Chat Vision 认 image_url.url 为网络链接。
	if b.Source.Type == "url" {
		return strings.TrimSpace(b.Source.Url)
	}
	mediaType := b.Source.MediaType
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "image/png"
	}
	return "data:" + mediaType + ";base64," + b.Source.Data
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

