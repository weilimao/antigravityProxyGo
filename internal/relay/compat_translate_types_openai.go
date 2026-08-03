package relay

// compat_translate_types_openai.go defines OpenAI Chat Completions protocol types
// (Message / ToolCall / Request / Response / Stream / Delta),
// shared by compat.go inbound paths and translate functions
// (TranslateOpenAIToGemini / ParseUnifiedOpenAIRequest).
// Split from compat_translate.go, physical move only, line-for-line equivalent.

import (
	"encoding/json"
)

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolName   string           `json:"tool_name,omitempty"`
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
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []AnthropicTool `json:"-"`
}

type OpenAIResponseChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type OpenAIResponseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
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
}
