package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeWorkBuddyMessages_BillingHeaderVariations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		excludes []string
	}{
		{
			name:     "standard billing header with newline",
			input:    "x-anthropic-billing-header: cc_version=2.1.261.533; cc_entrypoint=cli;\nYou are an assistant.",
			contains: "You are an assistant.",
			excludes: []string{"x-anthropic-billing-header", "cc_version"},
		},
		{
			name:     "billing header without colon",
			input:    "x-anthropic-billing-header cc_version=2.1.261\nYou are an assistant.",
			contains: "You are an assistant.",
			excludes: []string{"x-anthropic-billing-header"},
		},
		{
			name:     "billing header alone",
			input:    "x-anthropic-billing-header",
			contains: "",
			excludes: []string{"x-anthropic-billing-header"},
		},
		{
			name:     "Claude Code CLI introduction",
			input:    "You are Claude Code, Anthropic's official CLI for Claude.\nHelp user code.",
			contains: "You are an interactive AI assistant helping with coding.\nHelp user code.",
			excludes: []string{"You are Claude Code, Anthropic's official CLI for Claude."},
		},
		{
			name:     "claude-cli and claude-code token replacement",
			input:    "Running with claude-cli and claude-code context.",
			contains: "Running with ai-cli and ai-assistant context.",
			excludes: []string{"claude-cli", "claude-code"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &OpenAIChatRequest{
				Messages: []ChatMessage{
					{Role: "system", Content: tt.input},
				},
			}
			sanitizeWorkBuddyMessages(req)
			got := req.Messages[0].Content
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected %q to contain %q", got, tt.contains)
			}
			for _, ex := range tt.excludes {
				if strings.Contains(strings.ToLower(got), strings.ToLower(ex)) {
					t.Errorf("expected %q to NOT contain %q", got, ex)
				}
			}
		})
	}
}

func TestSanitizeWorkBuddyMessages_ContentPartsAndTools(t *testing.T) {
	req := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{
				Role:    "user",
				Content: "fallback content with x-anthropic-billing-header: test",
				ContentParts: []ChatMessageContentPart{
					ChatMessageTextPart{
						Type: "text",
						Text: "x-anthropic-billing-header: cc_version=2.1.0\nHello from Claude Code!",
					},
				},
			},
		},
		Tools: []ChatTool{
			{
				Type: "function",
				Function: ChatToolFunc{
					Name:        "submit_pr",
					Description: "Create PR. Generated with [Claude Code](https://claude-code.com)",
					Parameters:  map[string]interface{}{"type": "object"},
				},
			},
		},
	}

	sanitizeWorkBuddyMessages(req)

	// Check Content
	if strings.Contains(req.Messages[0].Content, "x-anthropic-billing-header") {
		t.Errorf("req.Messages[0].Content still has billing header: %s", req.Messages[0].Content)
	}

	// Check ContentParts
	if len(req.Messages[0].ContentParts) > 0 {
		part, ok := req.Messages[0].ContentParts[0].(ChatMessageTextPart)
		if !ok {
			t.Fatalf("expected ChatMessageTextPart, got %T", req.Messages[0].ContentParts[0])
		}
		if strings.Contains(part.Text, "x-anthropic-billing-header") {
			t.Errorf("ContentParts text still has billing header: %s", part.Text)
		}
		if strings.Contains(part.Text, "Claude Code") {
			t.Errorf("ContentParts text still has Claude Code: %s", part.Text)
		}
		if !strings.Contains(part.Text, "AI Assistant") {
			t.Errorf("ContentParts text missing replaced text: %s", part.Text)
		}
	}

	// Check Tools Description
	toolDesc := req.Tools[0].Function.Description
	if strings.Contains(toolDesc, "Claude Code") {
		t.Errorf("Tool description still has Claude Code: %s", toolDesc)
	}
	if !strings.Contains(toolDesc, "AI Assistant") {
		t.Errorf("Tool description should have AI Assistant: %s", toolDesc)
	}
}

func TestAnthropicSystemBlocks_JoiningWithNewlines(t *testing.T) {
	rawJSON := `{
		"model": "auto",
		"system": [
			{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.261.533; cc_entrypoint=cli;"},
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."}
		],
		"messages": [{"role": "user", "content": "hello"}]
	}`

	var req AnthropicRequest
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// Blocks must be joined with \n\n, not sticking together
	if !strings.Contains(req.System, "\n\n") {
		t.Errorf("expected System blocks to be joined by double newline, got: %q", req.System)
	}

	// Now sanitize as OpenAIChatRequest
	chatReq := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: req.System},
		},
	}
	sanitizeWorkBuddyMessages(chatReq)

	res := chatReq.Messages[0].Content
	if strings.Contains(res, "x-anthropic-billing-header") {
		t.Errorf("sanitized system still has billing header: %s", res)
	}
	if strings.Contains(res, "Claude Code") {
		t.Errorf("sanitized system still has Claude Code: %s", res)
	}
	if !strings.Contains(res, "You are an interactive AI assistant helping with coding.") {
		t.Errorf("expected replacement CLI text, got: %s", res)
	}
}

func TestSanitizeWorkBuddy_DeepParametersSchema(t *testing.T) {
	req := &OpenAIChatRequest{
		Tools: []ChatTool{
			{
				Type: "function",
				Function: ChatToolFunc{
					Name:        "run_command",
					Description: "Execute a shell command with Claude Code CLI.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"cmd": map[string]interface{}{
								"type":        "string",
								"description": "Command managed by claude-code or claude-cli runtime.",
								"title":       "Claude Code Command",
							},
							"options": map[string]interface{}{
								"type": "array",
								"items": []interface{}{
									map[string]interface{}{
										"description": "Config for Claude Agent SDK.",
									},
									"default_for_claude-code",
								},
							},
						},
					},
				},
			},
		},
	}

	sanitizeWorkBuddyMessages(req)

	tool := req.Tools[0]
	if strings.Contains(tool.Function.Description, "Claude Code") {
		t.Errorf("tool description not sanitized: %s", tool.Function.Description)
	}

	props := tool.Function.Parameters["properties"].(map[string]interface{})
	cmdProp := props["cmd"].(map[string]interface{})
	if strings.Contains(cmdProp["description"].(string), "claude-code") || strings.Contains(cmdProp["description"].(string), "claude-cli") {
		t.Errorf("nested param description not sanitized: %s", cmdProp["description"])
	}
	if strings.Contains(cmdProp["title"].(string), "Claude Code") {
		t.Errorf("nested param title not sanitized: %s", cmdProp["title"])
	}

	optProp := props["options"].(map[string]interface{})
	items := optProp["items"].([]interface{})
	itemMap := items[0].(map[string]interface{})
	if strings.Contains(itemMap["description"].(string), "Claude Agent SDK") {
		t.Errorf("nested array item map not sanitized: %s", itemMap["description"])
	}
	if strings.Contains(items[1].(string), "claude-code") {
		t.Errorf("nested array string item not sanitized: %s", items[1])
	}
}

func TestSanitizeWorkBuddy_ToolCallsAndArguments(t *testing.T) {
	req := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{
				Role:       "tool",
				ToolCallID: "call_claude_code_123",
				ToolName:   "claude_code_executor",
				Content:    "Executed via Claude Code successfully.",
			},
			{
				Role: "assistant",
				ToolCalls: []ChatToolCall{
					{
						ID:   "call_claude_code_123",
						Type: "function",
						Function: ChatToolCallFunction{
							Name:      "claude_code_tool",
							Arguments: `{"client":"Claude Code","flag":"claude-cli"}`,
						},
					},
				},
			},
		},
	}

	sanitizeWorkBuddyMessages(req)

	if strings.Contains(req.Messages[0].ToolCallID, "claude_code") {
		t.Errorf("tool_call_id not sanitized: %s", req.Messages[0].ToolCallID)
	}
	if strings.Contains(req.Messages[0].ToolName, "claude_code") {
		t.Errorf("tool_name not sanitized: %s", req.Messages[0].ToolName)
	}
	if strings.Contains(req.Messages[0].Content, "Claude Code") {
		t.Errorf("tool response content not sanitized: %s", req.Messages[0].Content)
	}

	call := req.Messages[1].ToolCalls[0]
	if strings.Contains(call.Function.Name, "claude_code") {
		t.Errorf("tool call function name not sanitized: %s", call.Function.Name)
	}
	if strings.Contains(call.Function.Arguments, "Claude Code") || strings.Contains(call.Function.Arguments, "claude-cli") {
		t.Errorf("tool call arguments not sanitized: %s", call.Function.Arguments)
	}
}

func TestSanitizeWorkBuddyFinalPayload(t *testing.T) {
	raw := []byte(`{"stream":true,"prompt":"x-anthropic-billing-header: cc_version=2.1.261; cc_entrypoint=cli;\nClaude Code with claude-cli"}`)
	sanitized := sanitizeWorkBuddyFinalPayload(raw)
	str := string(sanitized)

	if strings.Contains(str, "x-anthropic-billing-header") {
		t.Errorf("final payload still contains billing header: %s", str)
	}
	if strings.Contains(str, "cc_version=") || strings.Contains(str, "cc_entrypoint=") {
		t.Errorf("final payload still contains cc_ tokens: %s", str)
	}
	if strings.Contains(str, "Claude Code") {
		t.Errorf("final payload still contains Claude Code: %s", str)
	}
	if strings.Contains(str, "claude-cli") {
		t.Errorf("final payload still contains claude-cli: %s", str)
	}
}

