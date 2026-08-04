package relay

// compat_translate_types_anthropic.go defines Anthropic Messages protocol types
// (Content / ImageSource / Message / Thinking / Request / Response) plus the Gemini
// carrier types (ThinkingConfig / Config / Request), shared by TranslateAnthropicToGemini
// and inbound paths.
// Split from compat_translate.go, physical move only, line-for-line equivalent.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type AnthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// thinking 块字段(响应构建:Gemini thought:true 回译为 Anthropic thinking 块)
	// signature 恒为空串占位(Gemini 已剥真签名,对齐流式路径 signature_delta 空串策略)
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// tool_use 字段（响应构建 + 请求历史解析）
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// tool_result 字段（请求历史解析）
	ToolUseID         string          `json:"tool_use_id,omitempty"`
	ToolResultContent json.RawMessage `json:"content,omitempty"` // string 或 []block
	IsError           *bool           `json:"is_error,omitempty"`
	// image 块字段(请求历史解析 + NVIDIA 入站自愈降级):承载 Anthropic image content block 的
	// base64 数据,仅当 Type=="image" 时有意义。NVIDIA 上游不支持多模态,见 nvidia.go 注入的降级
	// 会先用本地 Gemini 把 Source.Data OCR 成纯文本并改写为 text 块,再走 AnthropicToOpenAIChat,
	// 故上游段永远只见 text、永远零负担。omitted 时不影响现有序列化形态。
	Source *AnthropicImageSource `json:"source,omitempty"`
}

// AnthropicImageSource 对齐 Anthropic image content block 的 source 字段:
// {"type":"base64","media_type":"image/png","data":"<base64>"}  或
// {"type":"url","url":"https://..."}。
// type=base64 时 Data 承载纯 base64(本地直取 OCR);type=url 时 Url 承载网络图片地址,
// 由 OCRService.fetchImageAsBase64 在 SSRF 防护下下载转 base64 后再 OCR(P2)。
type AnthropicImageSource struct {
	Type      string `json:"type"`         // "base64" | "url"
	MediaType string `json:"media_type"`   // "image/png" 等
	Data      string `json:"data"`        // base64 数据(base64 类型时非空)
	Url       string `json:"url,omitempty"` // url 类型时的网络图片地址(http/https)
}

type AnthropicMessage struct {
	Role    string             `json:"role"`
	Content []AnthropicContent `json:"content"`
}

// UnmarshalJSON 允许 AnthropicMessage.Content 兼容字符串及数组两种格式的 JSON 解析
func (m *AnthropicMessage) UnmarshalJSON(data []byte) error {
	var temp struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	m.Role = temp.Role

	if len(temp.Content) == 0 {
		return nil
	}

	trimmed := bytes.TrimSpace(temp.Content)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// 数组格式：如 [{"type": "text", "text": "..."}]
		var blocks []AnthropicContent
		if err := json.Unmarshal(temp.Content, &blocks); err != nil {
			return err
		}
		m.Content = blocks
	} else if len(trimmed) > 0 && trimmed[0] == '"' {
		// 纯字符串格式：如 "你是什么模型"
		var str string
		if err := json.Unmarshal(temp.Content, &str); err != nil {
			return err
		}
		m.Content = []AnthropicContent{{Type: "text", Text: str}}
	} else {
		return fmt.Errorf("invalid content field format inside Anthropic message")
	}

	return nil
}

type GeminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
	// IncludeThoughts 为 true 时,要求上游 Gemini 返回带 thought:true 标记的明文思考内容。
	// 用指针类型区分"未设"(nil,字段省略)与"显式 false"(不输出思考)。
	// 这是 Claude Code 走 antigravity 号池能看到思考过程的根因字段:
	// 缺它则上游永远不返 thought:true,回译侧 part.Thought 分支(compat.go)永不命中。
	IncludeThoughts *bool `json:"includeThoughts,omitempty"`
}

type GeminiConfig struct {
	Temperature     *float64              `json:"temperature,omitempty"`
	MaxOutputTokens *int                  `json:"maxOutputTokens,omitempty"`
	CandidateCount  int                   `json:"candidateCount,omitempty"`
	ThinkingConfig  *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiInstruction      `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiConfig           `json:"generationConfig,omitempty"`
	Tools             []GeminiToolDeclaration `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig       `json:"toolConfig,omitempty"`
}

type AnthropicThinking struct {
	Type         string `json:"type,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type AnthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   *int               `json:"max_tokens,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"`
	Thinking    *AnthropicThinking `json:"thinking,omitempty"`
	// OutputConfig 承载 Anthropic 新协议的 output_config.effort 字段,用 RawMessage 原样保留,
	// 由 nvidia 链路 resolveReasoningEffort 优先解析 effort,高于 thinking.budget_tokens。
	OutputConfig json.RawMessage `json:"output_config,omitempty"`
}

// UnmarshalJSON 允许 AnthropicRequest.System 兼容字符串及数组两种格式 of JSON 解析
func (r *AnthropicRequest) UnmarshalJSON(data []byte) error {
	var temp struct {
		Model        string             `json:"model"`
		Messages     []AnthropicMessage `json:"messages"`
		System       json.RawMessage    `json:"system,omitempty"`
		MaxTokens    *int               `json:"max_tokens,omitempty"`
		Temperature  *float64           `json:"temperature,omitempty"`
		Stream       bool               `json:"stream,omitempty"`
		Tools        []AnthropicTool    `json:"tools,omitempty"`
		ToolChoice   json.RawMessage    `json:"tool_choice,omitempty"`
		Thinking     *AnthropicThinking `json:"thinking,omitempty"`
		OutputConfig json.RawMessage    `json:"output_config,omitempty"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	r.Model = temp.Model
	r.Messages = temp.Messages
	r.MaxTokens = temp.MaxTokens
	r.Temperature = temp.Temperature
	r.Stream = temp.Stream
	r.Tools = temp.Tools
	r.ToolChoice = temp.ToolChoice
	r.Thinking = temp.Thinking
	r.OutputConfig = temp.OutputConfig

	if len(temp.System) == 0 {
		return nil
	}

	trimmed := bytes.TrimSpace(temp.System)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// 数组格式：如 [{"type": "text", "text": "..."}]
		var blocks []AnthropicContent
		if err := json.Unmarshal(temp.System, &blocks); err != nil {
			return err
		}
		var sb strings.Builder
		for _, b := range blocks {
			if b.Text != "" {
				sb.WriteString(b.Text)
			}
		}
		r.System = sb.String()
	} else if len(trimmed) > 0 && trimmed[0] == '"' {
		// 纯字符串格式：如 "You are a helpful assistant."
		var str string
		if err := json.Unmarshal(temp.System, &str); err != nil {
			return err
		}
		r.System = str
	} else {
		return fmt.Errorf("invalid system field format inside AnthropicRequest")
	}

	return nil
}

type AnthropicResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type AnthropicResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Content      []AnthropicContent     `json:"content"`
	Model        string                 `json:"model"`
	StopReason   string                 `json:"stop_reason"`
	StopSequence interface{}            `json:"stop_sequence"`
	Usage        AnthropicResponseUsage `json:"usage"`
}
