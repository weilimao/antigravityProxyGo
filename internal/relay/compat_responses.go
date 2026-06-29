package relay

import (
	"encoding/json"
	"strings"
)

// ResponsesInputItem 表示 Responses API input 数组中的一个条目
type ResponsesInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
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
				Content:    item.Output,
				ToolCallID: item.CallID,
			})

		case "message", "":
			flushPendingToolCalls()
			if item.Role == "" {
				continue
			}
			
			if len(item.Content) == 0 {
				continue
			}

			// 尝试解析 content
			var contentStr string
			if err := json.Unmarshal(item.Content, &contentStr); err == nil {
				messages = append(messages, OpenAIMessage{
					Role:    item.Role,
					Content: contentStr,
				})
				continue
			}

			var blocks []responsesContentBlock
			if err := json.Unmarshal(item.Content, &blocks); err == nil {
				var textParts []string
				for _, b := range blocks {
					if b.Text != "" && (b.Type == "text" || b.Type == "output_text" || b.Type == "input_text") {
						textParts = append(textParts, b.Text)
					}
				}
				messages = append(messages, OpenAIMessage{
					Role:    item.Role,
					Content: strings.Join(textParts, "\n"),
				})
				continue
			}

			var singleBlock responsesContentBlock
			if err := json.Unmarshal(item.Content, &singleBlock); err == nil {
				if singleBlock.Text != "" && (singleBlock.Type == "text" || singleBlock.Type == "output_text" || singleBlock.Type == "input_text") {
					messages = append(messages, OpenAIMessage{
						Role:    item.Role,
						Content: singleBlock.Text,
					})
				}
				continue
			}
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
