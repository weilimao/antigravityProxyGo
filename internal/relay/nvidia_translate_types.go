package relay

import (
	"bytes"
	"encoding/json"
	"strings"
)

// nvidia_translate_types.go: OpenAI Chat 兼容请求/响应/流式 chunk 类型(独立于 compat_translate.go 的 OpenAIRequest)。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// ChatMessage 是发给 NVIDIA 上游的 OpenAI Chat messages 元素。
//
// 注意:Content 字段刻意不使用 omitempty。
// 原因:NVIDIA(及多数 OpenAI 兼容)上游用 serde 反序列化,要求每条 message 显式带 content 字段;
// 若空串 "" 被 omitempty 省略,上游会回 400 "Failed to deserialize the JSON body into
// the target type: missing field `content`"。因此空内容必须序列化为 "content":"" 落盘,
// 这对 assistant(纯 tool_use 无文本)与 tool(空 tool_result)角色尤其关键。
//
// content 双形态(多模态扩展):
//   - 字符串(默认):纯文本 content,序列化为 "content":"<text>"。绝大多数 NVIDIA/OpenAI
//     兼容上游(glm-5.2 / deepseek-chat 等非多模态)只认此形态,保持零回归。
//   - 数组(多模态):当上游模型原生支持视觉(qwen-vl / gpt-4o / kimi-k2 等)时,Loading
//     anthropicUserToChat 会把 image 块转译为 OpenAI image_url 数组形态 content,序列化为
//     "content":[{"type":"image_url","image_url":{"url":"data:..."}},{"type":"text","text":"..."}]。
//     由自定义 MarshalJSON 按 ContentParts 是否非空决定输出数组还是字符串:
//       - ContentParts 为空 → 输出字符串(Content 字段,保持既有 string 语义);
//       - ContentParts 非空 → 输出数组(忽略 Content,ChatMessage.Content 仅作字符串形态的
//         读取/兜底,不参与数组形态的序列化)。
//   - UnmarshalJSON 兼容读取两种形态:遇数组时把纯文本块聚合进 Content,图片块存入 ContentParts
//     (供响应侧 Text() 兜底与必要处原样透传),保证下游 json.Unmarshal(req, &OpenAIChatRequest)
//     的既有 string 消费点(chatcompress 估算 / Objects 响应回译)不因数组形态而崩溃或丢字段。
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
	// ContentParts 承载多模态数组形态 content 的块(OpenAI Chat Vision 协议)。
	// 仅当非空时才启用数组序列化;为空时保持既有 string 形态(零回归)。用于:
	//   - 请求侧:anthropicUserToChat 把 image 块转 image_url 块存入,使多模态上游真正收图;
	//   - 响应侧:json.Unmarshal 遇数组形态时把纯文本块聚合进 Content、图片块存入 ContentParts,
	//     供 Text() 兜底回译(见 nvidia_translate_response.go)。
	ContentParts []ChatMessageContentPart `json:"-"`
}

// ChatMessageContentPart 是 OpenAI Chat Vision content 数组里单个块的抽象。
// 仅承载 text 与 image_url 两类块(text 块走 ChatMessageTextPart,image_url 块走
// ChatMessageImageURLPart);MarshalJSON 按实际类型分发。其它类型块(如 input_audio)本链路
// 不产生也不消费,保持集合内仅有序 text/image_url 的语义。
type ChatMessageContentPart interface {
	ChatContentPart()
}

// ChatMessageTextPart 是 OpenAI Chat Vision content 数组里的文本块。
type ChatMessageTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ChatContentPart 标记 ChatMessageTextPart 为 content 块。
func (ChatMessageTextPart) ChatContentPart() {}

// ChatMessageImageURLPart 是 OpenAI Chat Vision content 数组里的 image_url 块。
type ChatMessageImageURLPart struct {
	Type     string                        `json:"type"`
	ImageURL ChatMessageImageURLPartObject `json:"image_url"`
}

// ChatContentPart 标记 ChatMessageImageURLPart 为 content 块。
func (ChatMessageImageURLPart) ChatContentPart() {}

// ChatMessageImageURLPartObject 是 image_url 块内 {"url": "..."} 对象。
type ChatMessageImageURLPartObject struct {
	URL string `json:"url"`
}

// MarshalJSON 是 ChatMessage 的数组/字符串双形态序列化。
// 返回的 JSON 结构:
//   - ContentParts 非空 → {"role","content":[text/image_url 块,...],"tool_calls":...}
//     (content 为数组,多模态上游可消化;Content 字段忽略);
//   - ContentParts 为空 → 走标准 struct 序列化(Content 为字符串,维持既有 omitempty 语义)。
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	if len(m.ContentParts) == 0 {
		return marshalChatMessageString(m)
	}
	aux := map[string]interface{}{
		"role":    m.Role,
		"content": m.ContentParts,
	}
	if len(m.ToolCalls) > 0 {
		aux["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		aux["tool_call_id"] = m.ToolCallID
	}
	if m.ToolName != "" {
		aux["tool_name"] = m.ToolName
	}
	if m.ReasoningContent != "" {
		aux["reasoning_content"] = m.ReasoningContent
	}
	if m.Reasoning != "" {
		aux["reasoning"] = m.Reasoning
	}
	return json.Marshal(aux)
}

// marshalChatMessageString 走标准 struct 序列化(Content 为字符串)。
// 独立成函数避免与 MarshalJSON 递归(标准 struct 序列化若直接 json.Marshal(m) 会因
// MarshalJSON 已定义而无限递归,故用 type alias 屏蔽)。
func marshalChatMessageString(m ChatMessage) ([]byte, error) {
	type chatMessageString ChatMessage
	return json.Marshal(chatMessageString(m))
}

// UnmarshalJSON 兼容读取 ChatMessage 的字符串/数组两种 content 形态。
//   - 字符串:标准解析,Content 为原文;
//   - 数组:纯文本块(type=text)聚合进 Content(保持既有 string 消费点),图片块
//     (type=image_url)存入 ContentParts(供 Text() 兜底与响应侧原样透传)。
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type chatMessageAlias ChatMessage
	var alias struct {
		*chatMessageAlias
		Content json.RawMessage `json:"content"`
	}
	alias.chatMessageAlias = (*chatMessageAlias)(m)
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	if len(alias.Content) == 0 || string(alias.Content) == "null" {
		return nil
	}
	trimmed := bytes.TrimSpace(alias.Content)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		// 字符串形态:外层 alias.Content(RawMessage)遮藏了嵌入的 ChatMessage.Content(string),
		// 标准解析只填充外层 RawMessage、不会回填嵌入的 Content 字段,故须手动把原文字符串
		// 反序列化进 m.Content(否则字符串形态下 Content 恒空,Text() 一并取空)。
		var s string
		if err := json.Unmarshal(alias.Content, &s); err == nil {
			m.Content = s
		}
		return nil
	}
	// 数组形态:分离 text 与 image_url 块。
	var parts []json.RawMessage
	if err := json.Unmarshal(alias.Content, &parts); err != nil {
		return err
	}
	var sb strings.Builder
	var contentParts []ChatMessageContentPart
	for _, raw := range parts {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil || probe.Type == "" {
			continue
		}
		switch probe.Type {
		case "text":
			var tp ChatMessageTextPart
			if err := json.Unmarshal(raw, &tp); err == nil {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(tp.Text)
			}
		case "image_url":
			var ip ChatMessageImageURLPart
			if err := json.Unmarshal(raw, &ip); err == nil {
				contentParts = append(contentParts, ip)
			}
		}
	}
	m.Content = sb.String()
	m.ContentParts = contentParts
	return nil
}

// Text 返回 ChatMessage 的纯文本内容。
// 数组形态(ContentParts 非空)时,把纯文本块按序拼接返回(与 Content 字段的 string 语义对齐,
// 供既有 string 消费点——chatcompress 估算 / 响应回译 / Objects 转换——在数组形态下仍拿到正文,
// 绝不因 content 变数组而把正文当空串丢弃);字符串形态时直接返回 Content。
func (m *ChatMessage) Text() string {
	if len(m.ContentParts) == 0 {
		return m.Content
	}
	var sb strings.Builder
	for _, part := range m.ContentParts {
		switch p := part.(type) {
		case ChatMessageTextPart:
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(sb.String())
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
	// ReasoningEffort 承载 OpenAI 官方顶层 reasoning_effort 字段(low/medium/high)。
	// 仅 Other 号池 OpenAI 格式组使用:NVIDIA NIM 走上面的 ChatTemplateKwargs,
	// 第三方 OpenAI 兼容上游认官方顶层字段(如 OpenAI o-series、OpenRouter、部分 DeepSeek 中继)。
	// 非空时直接透传到上游请求体顶层;空串视作未设置(omitempty 不发)。
	// buildPassthroughUpstreamReq 的 Unmarshal→Marshal 路径靠此字段保留客户端原发的 reasoning_effort,
	// 否则 OpenAIChatRequest 无该字段会导致 Other openai 组透传时静默丢弃思考等级。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
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

// OpenAIChatUsageTokensDetails 是 usage.prompt_tokens_details 的嵌套明细(OpenAI 标准缓存口径)。
// 缓存命中 token 取 cached_tokens;部分兼容上游(如 DeepSeek)同时返回 prompt_cache_hit_tokens。
type OpenAIChatUsageTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type OpenAIChatUsage struct {
	PromptTokens     int                          `json:"prompt_tokens"`
	CompletionTokens int                          `json:"completion_tokens"`
	TotalTokens      int                          `json:"total_tokens"`
	PromptTokensDetails OpenAIChatUsageTokensDetails `json:"prompt_tokens_details"`
	// PromptCacheHitTokens 是 DeepSeek 等上游在 usage 顶层返回的缓存命中 token 数(OpenAI 标准无此字段)。
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
}

// CachedTokens 返回该 usage 的缓存命中 token 数(取 prompt_cache_hit_tokens 或 prompt_tokens_details.cached_tokens 较大者, 兼容两种口径)。
func (u *OpenAIChatUsage) CachedTokens() int {
	if u == nil {
		return 0
	}
	if u.PromptCacheHitTokens > 0 {
		return u.PromptCacheHitTokens
	}
	if u.PromptTokensDetails.CachedTokens > 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	return 0
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

