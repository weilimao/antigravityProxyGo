package relay

import (
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

