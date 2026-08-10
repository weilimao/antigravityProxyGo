package relay

// compat_translate_types_openai.go defines OpenAI Chat Completions protocol types
// (Message / ToolCall / Request / Response / Stream / Delta),
// shared by compat.go inbound paths and translate functions
// (TranslateOpenAIToGemini / ParseUnifiedOpenAIRequest).
// Split from compat_translate.go, physical move only, line-for-line equivalent.

import (
	"encoding/json"
)

// OpenAIMessage 兼容 Chat Completions 的 message 体。content 字段在响应用途下需要支持
// "纯工具调用时输出 null" 的官方语义(OpenAI 规范:assistant 调用工具时 content 为 null),
// 故 OpenAIMessage 实现自定义 MarshalJSON:仅当存在正文文本或思考内容时输出字符串 content,
// 纯 tool_calls 场景显式输出 "content":null,严格客户端不再因空串误判异常。
type OpenAIMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ToolName         string           `json:"tool_name,omitempty"`
}

// MarshalJSON 响应侧序列化:实现 OpenAI 官方"纯工具调用时 content=null"语义。
// 判定:
//   - 存在正文(Content !="") 或 存在思考(ReasoningContent !="") → content 按字符串输出;
//   - 仅 tool_calls / tool 相关字段、无正文无思考 → 输出 "content":null;
//   - 其余(纯文本 assistant 回复) → content 字符串(omitempty 不生效因有 MarshalJSON,故显式判空)。
//
// 入站解析路径已有 OpenAIMessage.UnmarshalJSON,二者互不影响。
func (m OpenAIMessage) MarshalJSON() ([]byte, error) {
	type alias OpenAIMessage
	aux := alias(m)
	if aux.Content == "" && aux.ReasoningContent == "" {
		// 纯工具调用场景:content 显式 null(与 OpenAI 官方对齐)。
		// 用 map 显式写 null,避免 struct tag omitempty 把空串省略成字段缺失
		// (字段缺失与 null 在多数客户端等价,但严格 SDK 按字段存在性区分 empty/null)。
		obj := map[string]interface{}{
			"role":    aux.Role,
			"content": nil,
		}
		if aux.ReasoningContent != "" {
			obj["reasoning_content"] = aux.ReasoningContent
		}
		if len(aux.ToolCalls) > 0 {
			obj["tool_calls"] = aux.ToolCalls
		}
		if aux.ToolCallID != "" {
			obj["tool_call_id"] = aux.ToolCallID
		}
		if aux.ToolName != "" {
			obj["tool_name"] = aux.ToolName
		}
		return json.Marshal(obj)
	}
	return json.Marshal(aux)
}

type OpenAIToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type OpenAIToolCall struct {
	Index     int                    `json:"index,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Function  OpenAIToolCallFunction `json:"function"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
}

func (tc *OpenAIToolCall) UnmarshalJSON(data []byte) error {
	type Alias OpenAIToolCall
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*tc = OpenAIToolCall(aux)
	if tc.Name == "" && tc.Function.Name != "" {
		tc.Name = tc.Function.Name
	} else if tc.Function.Name == "" && tc.Name != "" {
		tc.Function.Name = tc.Name
	}
	if tc.Arguments == "" && tc.Function.Arguments != "" {
		tc.Arguments = tc.Function.Arguments
	} else if tc.Function.Arguments == "" && tc.Arguments != "" {
		tc.Function.Arguments = tc.Arguments
	}
	if tc.ID != "" && tc.Type == "" {
		tc.Type = "function"
	}
	return nil
}

func (tc OpenAIToolCall) MarshalJSON() ([]byte, error) {
	type Alias OpenAIToolCall
	aux := Alias(tc)
	if aux.ID != "" && aux.Type == "" {
		aux.Type = "function"
	}
	if aux.Function.Name == "" && aux.Name != "" {
		aux.Function.Name = aux.Name
	}
	if aux.Function.Arguments == "" && aux.Arguments != "" {
		aux.Function.Arguments = aux.Arguments
	}
	aux.Name = ""
	aux.Arguments = ""
	return json.Marshal(aux)
}

// UnmarshalJSON 使 OpenAIMessage.Content 兼容字符串及数组（用于 Vision API 等场景）
func (m *OpenAIMessage) UnmarshalJSON(data []byte) error {
	type Alias OpenAIMessage
	var aux struct {
		*Alias
		Content json.RawMessage `json:"content"`
	}
	aux.Alias = (*Alias)(m)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.Content) == 0 || string(aux.Content) == "null" {
		return nil
	}

	if aux.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(aux.Content, &s); err != nil {
			return err
		}
		m.Content = s
	} else {
		m.Content = string(aux.Content)
	}

	return nil
}

type OpenAIRequest struct {
	Model               string            `json:"model"`
	Messages            []OpenAIMessage   `json:"messages"`
	Temperature         *float64          `json:"temperature,omitempty"`
	MaxTokens           *int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	Stream              bool              `json:"stream,omitempty"`
	Tools               []AnthropicTool   `json:"-"`
	ToolChoice          interface{}       `json:"tool_choice,omitempty"`
	StreamOptions       *OpenAIStreamOpts `json:"stream_options,omitempty"`
}

// OpenAIStreamOpts 对齐 OpenAI 官方 stream_options.include_usage。客户端流式请求若传
// {"stream_options":{"include_usage":true}},需在流末尾发一个 choices 为空 + 带 usage 的独立 chunk。
type OpenAIStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type OpenAIResponseChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type OpenAIResponseUsage struct {
	PromptTokens            int                  `json:"prompt_tokens"`
	CompletionTokens        int                  `json:"completion_tokens"`
	TotalTokens             int                  `json:"total_tokens"`
	PromptTokensDetails     *OpenAITokensDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *OpenAITokensDetails `json:"completion_tokens_details,omitempty"`
}

// OpenAITokensDetails 对齐 OpenAI usage 明细嵌套对象。reasoning_tokens 承载思考模型
// 的推理 token 消耗(对齐 Gemini usageMetadata.thoughtsTokenCount),cached_tokens 为
// 提示侧缓存命中口径(本链路目前不产出,保留字段供热更新)。
type OpenAITokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
}

type OpenAIResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []OpenAIResponseChoice `json:"choices"`
	Usage   OpenAIResponseUsage    `json:"usage"`
}

type OpenAIDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type OpenAIStreamChoice struct {
	Index        int         `json:"index"`
	Delta        OpenAIDelta `json:"delta"`
	FinishReason interface{} `json:"finish_reason"`
}

type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
	// Usage 仅在 stream_options.include_usage=true 时的末尾 usage chunk 携带(choices 为空)。
	// 指针类型区分"未设"(nil,字段省略,常规 chunk 不含 usage)与"显式携带"。
	Usage *OpenAIResponseUsage `json:"usage,omitempty"`
}
