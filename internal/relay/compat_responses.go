package relay

import (
	"encoding/json"
	"strings"
)

// parseRawMessageToString 将 json.RawMessage 转换为 string，如果原本是字符串则剥离引号，否则保留原样（如数组JSON）。
func parseRawMessageToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	if raw[0] == '[' {
		var arr []map[string]interface{}
		if err := json.Unmarshal(raw, &arr); err == nil {
			var sb strings.Builder
			for _, item := range arr {
				if t, ok := item["text"].(string); ok {
					sb.WriteString(t)
				}
			}
			if sb.Len() > 0 {
				return sb.String()
			}
		}
	}
	return string(raw)
}

// ResponsesInputItem 表示 Responses API input 数组中的一个条目
type ResponsesInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Status    string          `json:"status,omitempty"`
}

// ResponsesToolDef 表示 Responses API 中的工具定义
type ResponsesToolDef struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Function    *struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description,omitempty"`
		Parameters  map[string]interface{} `json:"parameters,omitempty"`
	} `json:"function,omitempty"`
}

type responsesContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parseResponsesInput 将 Responses API 的异构 input 数组解析为统一的 OpenAIMessage 列表
func parseResponsesInput(items []ResponsesInputItem) []OpenAIMessage {
	var messages []OpenAIMessage
	var pendingToolCalls []OpenAIToolCall

	flushPendingToolCalls := func() {
		if len(pendingToolCalls) > 0 {
			messages = append(messages, OpenAIMessage{
				Role:      "assistant",
				ToolCalls: pendingToolCalls,
			})
			pendingToolCalls = nil
		}
	}

	for _, item := range items {
		switch item.Type {
		case "function_call":
			pendingToolCalls = append(pendingToolCalls, OpenAIToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})

		case "function_call_output":
			flushPendingToolCalls()
			messages = append(messages, OpenAIMessage{
				Role:       "tool",
				Content:    parseRawMessageToString(item.Output),
				ToolCallID: item.CallID,
			})

		case "message", "":
			flushPendingToolCalls()
			if item.Role == "" || len(item.Content) == 0 {
				continue
			}

			messages = append(messages, OpenAIMessage{
				Role:    item.Role,
				Content: parseRawMessageToString(item.Content),
			})
		}
	}
	
	flushPendingToolCalls()
	return messages
}

// parseResponsesTools 将 Responses API 的 tools 定义转换为 AnthropicTool 格式，以复用工具翻译链路
func parseResponsesTools(tools []ResponsesToolDef) []AnthropicTool {
	var anthropicTools []AnthropicTool
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		
		name := t.Name
		desc := t.Description
		params := t.Parameters
		
		if t.Function != nil && t.Function.Name != "" {
			name = t.Function.Name
			desc = t.Function.Description
			params = t.Function.Parameters
		}
		
		if name == "" {
			continue
		}
		
		anthropicTools = append(anthropicTools, AnthropicTool{
			Name:        name,
			Description: desc,
			InputSchema: params,
		})
	}
	return anthropicTools
}
