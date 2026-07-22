package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"antigravity-proxy/internal/settings"
)

// ===== OpenAI Types =====
// ...（保持原样，以下由于替换边界包含故完整列出）

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

// ===== Anthropic Types =====

type AnthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// tool_use 字段（响应构建 + 请求历史解析）
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
	// tool_result 字段（请求历史解析）
	ToolUseID        string          `json:"tool_use_id,omitempty"`
	ToolResultContent json.RawMessage `json:"content,omitempty"` // string 或 []block
	IsError          *bool           `json:"is_error,omitempty"`
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
}

// UnmarshalJSON 允许 AnthropicRequest.System 兼容字符串及数组两种格式 of JSON 解析
func (r *AnthropicRequest) UnmarshalJSON(data []byte) error {
	var temp struct {
		Model       string             `json:"model"`
		Messages    []AnthropicMessage `json:"messages"`
		System      json.RawMessage    `json:"system,omitempty"`
		MaxTokens   *int               `json:"max_tokens,omitempty"`
		Temperature *float64           `json:"temperature,omitempty"`
		Stream      bool               `json:"stream,omitempty"`
		Tools       []AnthropicTool    `json:"tools,omitempty"`
		ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"`
		Thinking    *AnthropicThinking `json:"thinking,omitempty"`
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
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Content      []AnthropicContent `json:"content"`
	Model        string             `json:"model"`
	StopReason   string             `json:"stop_reason"`
	StopSequence interface{}        `json:"stop_sequence"`
	Usage        AnthropicResponseUsage `json:"usage"`
}

// ===== Gemini Types =====

type GeminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *GeminiBlob             `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts,omitempty"`
}

type GeminiInstruction struct {
	Parts []GeminiPart `json:"parts"`
}

type GeminiCandidateContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role"`
}

type GeminiCandidate struct {
	Content      GeminiCandidateContent `json:"content"`
	FinishReason string                 `json:"finishReason"`
	Index        int                    `json:"index"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate   `json:"candidates"`
	UsageMetadata GeminiUsageMetadata `json:"usageMetadata"`
}

// UnmarshalJSON 实现了自适应套娃解包：兼容官方的扁平结构与云助手的 "response": {} 嵌套结构
func (g *GeminiResponse) UnmarshalJSON(data []byte) error {
	type Alias GeminiResponse
	var aux struct {
		*Alias
		Response *Alias `json:"response"`
	}
	aux.Alias = (*Alias)(g)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Response != nil {
		if len(aux.Response.Candidates) > 0 {
			g.Candidates = aux.Response.Candidates
		}
		if aux.Response.UsageMetadata.PromptTokenCount > 0 || aux.Response.UsageMetadata.CandidatesTokenCount > 0 {
			g.UsageMetadata = aux.Response.UsageMetadata
		}
	}
	return nil
}

// ===== Model Mappings =====

func MapClientModelToGemini(clientModel string, customMapping []settings.ModelMappingEntry) string {
	// 1. Check custom mapping first (case-insensitive lookup)
	if len(customMapping) > 0 {
		// Exact match
		for _, entry := range customMapping {
			if entry.ClientModel == clientModel {
				return entry.TargetModel
			}
		}
		// Case-insensitive match
		for _, entry := range customMapping {
			if strings.EqualFold(entry.ClientModel, clientModel) {
				return entry.TargetModel
			}
		}
	}

	m := strings.ToLower(clientModel)
	
	// 如果本身已经是含有 gemini / gpt-cos / tab_ / claude-sonnet 等原生或中继映射的模型名字，直接保留原样返回，不进行 fallback 转换
	if strings.Contains(m, "gemini") || strings.Contains(m, "gpt-cos") || strings.Contains(m, "tab_") || strings.Contains(m, "claude-sonnet") || strings.Contains(m, "claude-opus") {
		return clientModel
	}
	
	// Anthropic Maps
	if strings.Contains(m, "claude-3-5-sonnet") {
		return "gemini-1.5-pro"
	}
	if strings.Contains(m, "claude-3-opus") {
		return "gemini-1.5-pro"
	}
	if strings.Contains(m, "claude-3-haiku") || strings.Contains(m, "claude-3-5-haiku") {
		return "gemini-1.5-flash"
	}
	if strings.Contains(m, "claude") {
		return "gemini-1.5-pro"
	}

	// OpenAI Maps
	if strings.Contains(m, "gpt-4o") || strings.Contains(m, "gpt-4-turbo") || strings.Contains(m, "gpt-4") {
		return "gemini-1.5-pro"
	}
	if strings.Contains(m, "gpt-3.5") || strings.Contains(m, "o1-mini") {
		return "gemini-1.5-flash"
	}
	if strings.Contains(m, "o1-pro") || strings.Contains(m, "o1-preview") {
		return "gemini-2.0-flash"
	}
	if strings.Contains(m, "gpt-5") {
		return "gemini-3-flash-agent"
	}

	// Default Fallback
	return "gemini-1.5-pro"
}

// ===== Convert Requests =====

// ParseUnifiedOpenAIRequest 统一解析 Chat Completions 与 Responses 报文格式
func ParseUnifiedOpenAIRequest(bodyBytes []byte) (*OpenAIRequest, error) {
	type TempReq struct {
		Model        string               `json:"model"`
		Messages     []OpenAIMessage      `json:"messages"`
		Input        []ResponsesInputItem `json:"input"`
		Tools        []ResponsesToolDef   `json:"tools,omitempty"`
		Instructions string               `json:"instructions"`
		Temperature  *float64             `json:"temperature,omitempty"`
		MaxTokens    *int                 `json:"max_tokens,omitempty"`
		Stream       bool                 `json:"stream,omitempty"`
		Request      *TempReq             `json:"request,omitempty"`
	}

	var temp TempReq
	if err := json.Unmarshal(bodyBytes, &temp); err != nil {
		return nil, err
	}

	if temp.Request != nil {
		if temp.Model == "" && temp.Request.Model != "" {
			temp.Model = temp.Request.Model
		}
		if len(temp.Messages) == 0 && len(temp.Request.Messages) > 0 {
			temp.Messages = temp.Request.Messages
		}
		if len(temp.Input) == 0 && len(temp.Request.Input) > 0 {
			temp.Input = temp.Request.Input
		}
		if len(temp.Tools) == 0 && len(temp.Request.Tools) > 0 {
			temp.Tools = temp.Request.Tools
		}
		if temp.Instructions == "" && temp.Request.Instructions != "" {
			temp.Instructions = temp.Request.Instructions
		}
		if temp.Temperature == nil && temp.Request.Temperature != nil {
			temp.Temperature = temp.Request.Temperature
		}
		if temp.MaxTokens == nil && temp.Request.MaxTokens != nil {
			temp.MaxTokens = temp.Request.MaxTokens
		}
		if !temp.Stream && temp.Request.Stream {
			temp.Stream = temp.Request.Stream
		}
	}

	req := &OpenAIRequest{
		Model:       temp.Model,
		Temperature: temp.Temperature,
		MaxTokens:   temp.MaxTokens,
		Stream:      temp.Stream,
	}

	// 1. 如果是标准的 Chat Completions，直接返回
	if len(temp.Messages) > 0 {
		req.Messages = temp.Messages
		return req, nil
	}

	// 2. 如果是 Responses 格式，将其翻译为 Messages 数组
	var messages []OpenAIMessage
	if temp.Instructions != "" {
		messages = append(messages, OpenAIMessage{
			Role:    "system",
			Content: temp.Instructions,
		})
	}

	if len(temp.Input) > 0 {
		parsedMessages := parseResponsesInput(temp.Input)
		messages = append(messages, parsedMessages...)
	}
	
	req.Tools = parseResponsesTools(temp.Tools)
	req.Messages = messages
	return req, nil
}

// parseOpenAIContentString 尝试将可能包含数组格式或纯文本的 OpenAI Content 转换为 GeminiPart 切片，支持图片（Base64）。
func parseOpenAIContentString(contentStr string) []GeminiPart {
	if contentStr == "" {
		return nil
	}
	trimmed := strings.TrimSpace(contentStr)
	if strings.HasPrefix(trimmed, "[") {
		var arr []map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			var parts []GeminiPart
			for _, item := range arr {
				t, _ := item["type"].(string)
				if t == "text" || t == "output_text" || t == "input_text" {
					if text, ok := item["text"].(string); ok && text != "" {
						clean := SanitizeAllThoughtSignatures(text)
						if clean != "" {
							parts = append(parts, GeminiPart{Text: clean})
						}
					}
				} else if t == "image_url" {
					if imgObj, ok := item["image_url"].(map[string]interface{}); ok {
						if urlStr, ok := imgObj["url"].(string); ok && strings.HasPrefix(urlStr, "data:") {
							idx := strings.Index(urlStr, ";base64,")
							if idx > 0 {
								parts = append(parts, GeminiPart{
									InlineData: &GeminiBlob{
										MimeType: urlStr[5:idx],
										Data:     urlStr[idx+8:],
									},
								})
							}
						}
					}
				} else if t == "image" { // Anthropic / Claude Code style tool result
					if source, ok := item["source"].(map[string]interface{}); ok {
						if data, ok := source["data"].(string); ok {
							mime := "image/jpeg"
							if m, ok := source["media_type"].(string); ok {
								mime = m
							}
							parts = append(parts, GeminiPart{
								InlineData: &GeminiBlob{
									MimeType: mime,
									Data:     data,
								},
							})
						}
					}
				}
			}
			if len(parts) > 0 {
				return parts
			}
		}
	}
	return []GeminiPart{{Text: SanitizeAllThoughtSignatures(contentStr)}}
}

func TranslateOpenAIToGemini(openReq *OpenAIRequest) *GeminiRequest {
	gemReq := &GeminiRequest{
		Contents: make([]GeminiContent, 0),
	}

	// 翻译工具定义
	gemReq.Tools = translateToolsToGemini(openReq.Tools)
	if len(gemReq.Tools) > 0 {
		gemReq.ToolConfig = &GeminiToolConfig{
			FunctionCallingConfig: &GeminiFCConfig{Mode: "VALIDATED"},
		}
	}

	var systemInstructionParts []GeminiPart

	for _, msg := range openReq.Messages {
		role := strings.ToLower(msg.Role)
		if role == "system" {
			systemInstructionParts = append(systemInstructionParts, parseOpenAIContentString(msg.Content)...)
			continue
		}

		// 处理 assistant 消息（可能包含工具调用）
		if role == "assistant" {
			parts := parseOpenAIContentString(msg.Content)
			for _, tc := range msg.ToolCalls {
				args := parseToolCallArgs(tc.Arguments)
				parts = append(parts, GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: tc.Name,
						Args: args,
						ID:   tc.ID,
					},
					ThoughtSignature: "skip_thought_signature_validator",
				})
			}
			if len(parts) > 0 {
				gemReq.Contents = append(gemReq.Contents, GeminiContent{
					Role:  "model",
					Parts: parts,
				})
			}
		} else if role == "tool" {
			// 工具结果 → Gemini functionResponse
			toolName := msg.ToolName
			if toolName == "" {
				toolName = findOpenAIToolNameByID(openReq.Messages, msg.ToolCallID)
			}
			
			// 提取 text 部分作为 functionResponse 的 result，将图片部分（如果有）作为独立的 user 内容块（Gemini 不支持 FunctionResponse 中带图片，但可通过 user 消息附加图片上下文）
			contentParts := parseOpenAIContentString(msg.Content)
			var textResult strings.Builder
			var imageParts []GeminiPart
			
			for _, p := range contentParts {
				if p.InlineData != nil {
					imageParts = append(imageParts, p)
				} else if p.Text != "" {
					if textResult.Len() > 0 {
						textResult.WriteString("\n")
					}
					textResult.WriteString(p.Text)
				}
			}
			
			if textResult.Len() == 0 && len(imageParts) > 0 {
				textResult.WriteString("[Image Result attached]")
			}
			
			funcPart := GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					Name:     toolName,
					Response: map[string]interface{}{"result": truncateToolResultText(textResult.String(), 30000)},
					ID:       msg.ToolCallID,
				},
			}

			n := len(gemReq.Contents)
			if n > 0 && gemReq.Contents[n-1].Role == "user" {
				gemReq.Contents[n-1].Parts = append(gemReq.Contents[n-1].Parts, funcPart)
			} else {
				gemReq.Contents = append(gemReq.Contents, GeminiContent{
					Role:  "user",
					Parts: []GeminiPart{funcPart},
				})
			}

			if len(imageParts) > 0 {
				n = len(gemReq.Contents)
				if n > 0 && gemReq.Contents[n-1].Role == "user" {
					gemReq.Contents[n-1].Parts = append(gemReq.Contents[n-1].Parts, imageParts...)
				} else {
					gemReq.Contents = append(gemReq.Contents, GeminiContent{
						Role:  "user",
						Parts: imageParts,
					})
				}
			}

		} else {
			// user 消息
			userParts := parseOpenAIContentString(msg.Content)
			if len(userParts) > 0 {
				n := len(gemReq.Contents)
				if n > 0 && gemReq.Contents[n-1].Role == "user" {
					gemReq.Contents[n-1].Parts = append(gemReq.Contents[n-1].Parts, userParts...)
				} else {
					gemReq.Contents = append(gemReq.Contents, GeminiContent{
						Role:  "user",
						Parts: userParts,
					})
				}
			}
		}
	}

	if len(systemInstructionParts) > 0 {
		gemReq.SystemInstruction = &GeminiInstruction{
			Parts: systemInstructionParts,
		}
	}

	if openReq.Temperature != nil || openReq.MaxTokens != nil {
		gemReq.GenerationConfig = &GeminiConfig{
			Temperature:     openReq.Temperature,
			MaxOutputTokens: openReq.MaxTokens,
		}
	}

	// 自动为 flash / pro / thinking 等思考型推理模型注入 thinkingConfig 预算，防止谷歌上游截断返回 0 OutTokens
	lowerModel := strings.ToLower(openReq.Model)
	if strings.Contains(lowerModel, "flash") || strings.Contains(lowerModel, "pro") || strings.Contains(lowerModel, "thinking") {
		if gemReq.GenerationConfig == nil {
			gemReq.GenerationConfig = &GeminiConfig{}
		}
		if gemReq.GenerationConfig.ThinkingConfig == nil {
			gemReq.GenerationConfig.ThinkingConfig = &GeminiThinkingConfig{
				ThinkingBudget: 8192,
			}
		}
	}

	// 强制满足 Gemini 的严格 user/model 角色交替约束
	gemReq.Contents = mergeConsecutiveRoles(gemReq.Contents)

	return gemReq
}

func TranslateAnthropicToGemini(anthReq *AnthropicRequest) *GeminiRequest {
	gemReq := &GeminiRequest{
		Contents: make([]GeminiContent, 0),
	}

	if anthReq.System != "" {
		gemReq.SystemInstruction = &GeminiInstruction{
			Parts: []GeminiPart{{Text: SanitizeAllThoughtSignatures(anthReq.System)}},
		}
	}

	// 翻译工具定义: Anthropic tools 转换 Gemini functionDeclarations
	gemReq.Tools = translateToolsToGemini(anthReq.Tools)
	gemReq.ToolConfig = translateToolChoiceToGemini(anthReq.ToolChoice)
	
	// 如果提供了工具，则强制设置为 VALIDATED 模式，防止 Gemini-3-flash-agent 偷懒不调用
	if len(gemReq.Tools) > 0 {
		if gemReq.ToolConfig == nil {
			gemReq.ToolConfig = &GeminiToolConfig{
				FunctionCallingConfig: &GeminiFCConfig{
					Mode: "VALIDATED",
				},
			}
		} else if gemReq.ToolConfig.FunctionCallingConfig == nil {
			gemReq.ToolConfig.FunctionCallingConfig = &GeminiFCConfig{
				Mode: "VALIDATED",
			}
		} else if gemReq.ToolConfig.FunctionCallingConfig.Mode == "" || gemReq.ToolConfig.FunctionCallingConfig.Mode == "AUTO" || gemReq.ToolConfig.FunctionCallingConfig.Mode == "ANY" {
			gemReq.ToolConfig.FunctionCallingConfig.Mode = "VALIDATED"
		}
	}

	// 翻译消息历史，支持 text / tool_use / tool_result 三种 content block 类型
	for _, msg := range anthReq.Messages {
		role := strings.ToLower(msg.Role)

		// 分离普通 Part 和 functionResponse Part，因为 Gemini 要求 functionResponse 使用 role:"function"
		var normalParts []GeminiPart
		var funcRespParts []GeminiPart

		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					cleanText := SanitizeAllThoughtSignatures(block.Text)
					if cleanText != "" {
						normalParts = append(normalParts, GeminiPart{Text: cleanText})
					}
				}
			case "tool_use":
				// assistant 消息中的工具调用 → Gemini functionCall Part
				normalParts = append(normalParts, GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: block.Name,
						Args: block.Input,
						ID:   block.ID,
					},
					ThoughtSignature: "skip_thought_signature_validator",
				})
			case "tool_result":
				// user 消息中的工具结果 → Gemini functionResponse Part
				toolName := findToolNameByID(anthReq.Messages, block.ToolUseID)
				resultText := truncateToolResultText(SanitizeAllThoughtSignatures(extractToolResultText(block)), 30000)
				funcRespParts = append(funcRespParts, GeminiPart{
					FunctionResponse: &GeminiFunctionResponse{
						Name:     toolName,
						Response: map[string]interface{}{"result": resultText},
						ID:       block.ToolUseID,
					},
				})
			}
		}

		// 添加 functionResponse 消息（Gemini 要求 role:"function"但在 Antigravity backend 中必须是 "user"）
		if len(funcRespParts) > 0 {
			gemReq.Contents = append(gemReq.Contents, GeminiContent{
				Role:  "user",
				Parts: funcRespParts,
			})
		}

		// 添加普通消息
		if len(normalParts) > 0 {
			gemRole := "user"
			if role == "assistant" {
				gemRole = "model"
			}
			gemReq.Contents = append(gemReq.Contents, GeminiContent{
				Role:  gemRole,
				Parts: normalParts,
			})
		}
	}

	if anthReq.Temperature != nil || anthReq.MaxTokens != nil || anthReq.Thinking != nil {
		gemReq.GenerationConfig = &GeminiConfig{
			Temperature:     anthReq.Temperature,
			MaxOutputTokens: anthReq.MaxTokens,
		}
		if anthReq.Thinking != nil && anthReq.Thinking.BudgetTokens > 0 {
			gemReq.GenerationConfig.ThinkingConfig = &GeminiThinkingConfig{
				ThinkingBudget: anthReq.Thinking.BudgetTokens,
			}
		}
	}

	// 强制满足 Gemini 的严格 user/model 角色交替约束
	gemReq.Contents = mergeConsecutiveRoles(gemReq.Contents)

	return gemReq
}

func findToolNameByID(messages []AnthropicMessage, toolUseID string) string {
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ID == toolUseID {
				return block.Name
			}
		}
	}
	return "unknown"
}

// findOpenAIToolNameByID 在消息历史中查找 tool_call_id 对应的工具名称
func findOpenAIToolNameByID(messages []OpenAIMessage, toolCallID string) string {
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID == toolCallID {
				return tc.Name
			}
		}
	}
	return "unknown"
}

// ===== Helper Extract Token =====

func extractToken(r *http.Request) string {
	// 优先兼容某些分发客户端自定义传输的头部 (如 ANTHROPIC_API_KEY / API_KEY)
	if tok := r.Header.Get("ANTHROPIC_API_KEY"); tok != "" {
		return strings.TrimSpace(tok)
	}
	if tok := r.Header.Get("API_KEY"); tok != "" {
		return strings.TrimSpace(tok)
	}
	if tok := r.Header.Get("x-goog-api-key"); tok != "" {
		return strings.TrimSpace(tok)
	}
	if tok := r.Header.Get("X-Goog-Api-Key"); tok != "" {
		return strings.TrimSpace(tok)
	}

	header := r.Header.Get("Authorization")
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	token := r.Header.Get("X-API-Key")
	if token != "" {
		return strings.TrimSpace(token)
	}
	// 支持从 URL 参数 key 提取 (作为兜底)
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

// parseToolCallArgs 将 JSON 字符串格式的参数解析为 map，并兜底处理空参数防止 Gemini 报 MALFORMED_FUNCTION_CALL
func parseToolCallArgs(argsStr string) map[string]interface{} {
	trimmed := strings.TrimSpace(argsStr)
	if trimmed == "" || trimmed == "{}" {
		return map[string]interface{}{"_": true}
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		return map[string]interface{}{"raw": argsStr}
	}
	if len(args) == 0 {
		return map[string]interface{}{"_": true}
	}
	return args
}

// truncateToolResultText 智能截断过长的工具输出，保留头部与尾部关键信息，防止提示词上下文爆炸（>10万Token）
func truncateToolResultText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	half := maxChars / 2
	head := text[:half]
	tail := text[len(text)-half:]
	truncatedCount := len(text) - maxChars
	return fmt.Sprintf("%s\n\n...[Tool Output Truncated %d Characters to save context]...\n\n%s", head, truncatedCount, tail)
}
