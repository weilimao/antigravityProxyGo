package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
)

// ===== Anthropic Tool Types =====

// AnthropicTool 定义 Anthropic 请求中的工具声明
type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ===== Gemini Tool Types =====

// GeminiFunctionCall 表示 Gemini 响应中的函数调用
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
	ID   string                 `json:"id,omitempty"`
}

// GeminiFunctionResponse 表示发送给 Gemini 的函数执行结果
type GeminiFunctionResponse struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
	ID       string      `json:"id,omitempty"`
}

// GeminiToolDeclaration 表示 Gemini 请求中的工具声明容器
type GeminiToolDeclaration struct {
	FunctionDeclarations []GeminiFunctionDecl `json:"functionDeclarations"`
}

// GeminiFunctionDecl 表示单个 Gemini 函数声明
type GeminiFunctionDecl struct {
	Name                 string                 `json:"name"`
	Description          string                 `json:"description,omitempty"`
	ParametersJsonSchema map[string]interface{} `json:"parametersJsonSchema,omitempty"`
}

// GeminiToolConfig 控制 Gemini 函数调用行为
type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFCConfig `json:"functionCallingConfig,omitempty"`
}

// GeminiFCConfig 函数调用模式配置
type GeminiFCConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// ===== Tool Translation Helpers =====

// generateToolUseID 生成 Anthropic 风格的 tool_use 唯一标识符
func generateToolUseID() string {
	return fmt.Sprintf("toolu_%016x", rand.Int63())
}

// geminiUnsupportedSchemaFields 列出 Gemini API 不支持的 JSON Schema 字段
// 这些字段会在转换时被递归剥离
var geminiUnsupportedSchemaFields = map[string]bool{
	"$schema":              true,
	"$id":                  true,
	"$ref":                 true,
	"$comment":             true,
	"$defs":                true,
	"definitions":          true,
	"default":              true,
	"const":                true,
	"propertyNames":        true,
	"patternProperties":    true,
	"additionalProperties": true,
	"exclusiveMinimum":     true,
	"exclusiveMaximum":     true,
	"minimum":              true,
	"maximum":              true,
	"minLength":            true,
	"maxLength":            true,
	"minItems":             true,
	"maxItems":             true,
	"uniqueItems":          true,
	"oneOf":                true,
	"allOf":                true,
	"anyOf":                true,
	"not":                  true,
	"if":                   true,
	"then":                 true,
	"else":                 true,
	"pattern":              true,
	"title":                true,
	"examples":             true,
	"readOnly":             true,
	"writeOnly":            true,
	"deprecated":           true,
}

// convertSchemaTypesToUpper 递归地将 JSON Schema 转换为 Gemini 兼容格式：
// 1. type 值从小写转为大写 ("string" → "STRING")
// 2. 剥离 Gemini 不支持的 JSON Schema 扩展字段 ($schema, propertyNames, const 等)
func convertSchemaTypesToUpper(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range schema {
		// 跳过 Gemini 不支持的字段
		if geminiUnsupportedSchemaFields[k] {
			continue
		}

		switch k {
		case "type":
			if s, ok := v.(string); ok {
				result[k] = strings.ToUpper(s)
			} else {
				result[k] = v
			}
		case "properties":
			if props, ok := v.(map[string]interface{}); ok {
				converted := make(map[string]interface{})
				for propName, propVal := range props {
					if propMap, ok := propVal.(map[string]interface{}); ok {
						converted[propName] = convertSchemaTypesToUpper(propMap)
					} else {
						converted[propName] = propVal
					}
				}
				result[k] = converted
			} else {
				result[k] = v
			}
		case "items":
			if items, ok := v.(map[string]interface{}); ok {
				result[k] = convertSchemaTypesToUpper(items)
			} else {
				result[k] = v
			}
		default:
			result[k] = v
		}
	}
	return result
}

// extractToolResultText 从 tool_result content block 中提取文本内容
// content 字段可以是字符串或 []AnthropicContent 数组
func extractToolResultText(block AnthropicContent) string {
	if len(block.ToolResultContent) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(block.ToolResultContent)
	if len(trimmed) == 0 {
		return ""
	}

	// 字符串格式: "result text"
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(block.ToolResultContent, &s) == nil {
			return s
		}
		return string(block.ToolResultContent)
	}

	// 数组格式: [{"type":"text","text":"..."}]
	if trimmed[0] == '[' {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block.ToolResultContent, &blocks) == nil {
			var sb strings.Builder
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					sb.WriteString(b.Text)
				}
			}
			return sb.String()
		}
	}

	return string(block.ToolResultContent)
}

// translateToolsToGemini 将 Anthropic 工具定义列表转换为 Gemini 格式
func translateToolsToGemini(tools []AnthropicTool) []GeminiToolDeclaration {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]GeminiFunctionDecl, 0, len(tools))
	for _, tool := range tools {
		decls = append(decls, GeminiFunctionDecl{
			Name:                 tool.Name,
			Description:          tool.Description,
			ParametersJsonSchema: convertSchemaTypesToUpper(tool.InputSchema),
		})
	}
	return []GeminiToolDeclaration{{FunctionDeclarations: decls}}
}

// translateToolChoiceToGemini 将 Anthropic tool_choice 转换为 Gemini toolConfig
func translateToolChoiceToGemini(toolChoice json.RawMessage) *GeminiToolConfig {
	if len(toolChoice) == 0 {
		return nil
	}

	var tc struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	}
	if json.Unmarshal(toolChoice, &tc) != nil {
		return nil
	}

	config := &GeminiToolConfig{
		FunctionCallingConfig: &GeminiFCConfig{},
	}

	switch tc.Type {
	case "auto", "":
		config.FunctionCallingConfig.Mode = "VALIDATED"
	case "any":
		config.FunctionCallingConfig.Mode = "VALIDATED"
	case "tool":
		config.FunctionCallingConfig.Mode = "VALIDATED"
		if tc.Name != "" {
			config.FunctionCallingConfig.AllowedFunctionNames = []string{tc.Name}
		}
	case "none":
		config.FunctionCallingConfig.Mode = "NONE"
	default:
		config.FunctionCallingConfig.Mode = "AUTO"
	}

	return config
}

// mergeConsecutiveRoles 合并连续相同角色的消息。
// Gemini API 要求 user/model 严格交替，但 Claude Code harness 会注入额外的
// user 消息（如 skills 列表、system-reminder），导致出现连续同角色消息。
// 此函数将连续同角色的 Parts 合入一条消息，并在 function→user 中间
// 插入占位 model 消息以满足 Gemini 的角色交替约束。
func mergeConsecutiveRoles(contents []GeminiContent) []GeminiContent {
	if len(contents) <= 1 {
		return contents
	}

	// 第一步：合并连续同角色消息
	merged := make([]GeminiContent, 0, len(contents))
	merged = append(merged, contents[0])
	for i := 1; i < len(contents); i++ {
		last := &merged[len(merged)-1]
		if contents[i].Role == last.Role {
			last.Parts = append(last.Parts, contents[i].Parts...)
		} else {
			merged = append(merged, contents[i])
		}
	}

	// 第二步：在 function→user 之间插入占位 model 消息
	// Gemini 要求 function 消息后必须跟 model 消息
	fixed := make([]GeminiContent, 0, len(merged)+2)
	for i, c := range merged {
		fixed = append(fixed, c)
		if c.Role == "function" && i+1 < len(merged) && merged[i+1].Role != "model" {
			fixed = append(fixed, GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{{Text: "OK."}},
			})
		}
	}

	return fixed
}
