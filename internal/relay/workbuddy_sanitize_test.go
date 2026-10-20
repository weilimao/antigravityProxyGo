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


// TestSanitizeWorkBuddySystemText_RemovesResidualIdentity 覆盖黑名单漏网后的残留身份兜底:
// system 消息里只要还剩 claude / anthropic 字样,上游就有理由判定非官方客户端(11128)。
func TestSanitizeWorkBuddySystemText_RemovesResidualIdentity(t *testing.T) {
	raw := "You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Read https://claude.ai/code/docs and install @anthropic-ai/sdk.\n" +
		"Anthropic models are supported."

	got := sanitizeWorkBuddySystemText(raw)
	lower := strings.ToLower(got)

	if strings.Contains(lower, "claude") {
		t.Errorf("system 清洗后仍残留 claude: %q", got)
	}
	if strings.Contains(lower, "anthropic") {
		t.Errorf("system 清洗后仍残留 anthropic: %q", got)
	}
	if !strings.Contains(got, "install") {
		t.Errorf("清洗不应丢弃正文内容: %q", got)
	}
}

// TestSanitizeWorkBuddyMessages_SystemOnlyResidualScrub 确认残留兜底只作用于 system,
// 不篡改用户/助手正文(避免误伤正常对话内容)。
func TestSanitizeWorkBuddyMessages_SystemOnlyResidualScrub(t *testing.T) {
	req := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are Claude Code, Anthropic's official CLI for Claude."},
			{Role: "user", Content: "What is Anthropic's headquarters?"},
			{Role: "assistant", Content: "Anthropic is based in San Francisco."},
		},
	}

	sanitizeWorkBuddyMessages(req)

	if strings.Contains(strings.ToLower(req.Messages[0].Content), "claude") ||
		strings.Contains(strings.ToLower(req.Messages[0].Content), "anthropic") {
		t.Errorf("system 消息残留身份特征: %q", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[1].Content, "Anthropic") {
		t.Errorf("用户正文不应被泛化替换: %q", req.Messages[1].Content)
	}
	if !strings.Contains(req.Messages[2].Content, "Anthropic") {
		t.Errorf("助手正文不应被泛化替换: %q", req.Messages[2].Content)
	}
}

// TestSanitizeWorkBuddy_GitStatusPRPhrase 覆盖实测到的上游提示词指纹黑名单:
// `Main branch (you will usually use this for PRs)` 整句出现即被判 11128
// (直连上游验证: 该句 + "hi" → 11128;去掉括号注释 → 200)。清洗后必须只保留
// "Main branch: <分支>" 语义,不能把分支名一起丢掉。
func TestSanitizeWorkBuddy_GitStatusPRPhrase(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "标准句式",
			in:   "gitStatus: ...\n\nCurrent branch: main\n\nMain branch (you will usually use this for PRs): main\n\nGit user: weilimao",
			want: "Main branch: main",
		},
		{
			name: "大小写变体",
			in:   "main BRANCH (You will usually use this for prs): dev",
			want: "Main branch: dev",
		},
		{
			name: "括号内多余空格",
			in:   "Main branch ( you will usually use this for PRs ) : main",
			want: "Main branch : main",
		},
	}

	for _, tc := range tests {
		got := sanitizeWorkBuddyText(tc.in)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: 清洗结果缺少 %q: %q", tc.name, tc.want, got)
		}
		if reWorkBuddyGitStatusPRPhrase.MatchString(got) {
			t.Errorf("%s: 指纹句式仍残留: %q", tc.name, got)
		}
	}

	// 不相关的 "Main branch" 写法不应被改动
	keep := "Main branch: main"
	if got := sanitizeWorkBuddyText(keep); got != keep {
		t.Errorf("普通 Main branch 行不应被改写: %q", got)
	}
}

// TestSanitizeWorkBuddyMessages_GitStatusPhraseAnyRole 确认指纹清洗对 system 与 user
// 都生效(不同客户端把 gitStatus 段放在 system 或首条 user 里,两处都要兜住),
// 且最终字节级载荷里也不再残留。
func TestSanitizeWorkBuddyMessages_GitStatusPhraseAnyRole(t *testing.T) {
	phrase := "Main branch (you will usually use this for PRs): main"
	req := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are an assistant.\n\n" + phrase},
			{Role: "user", Content: "<system-reminder>\nCurrent branch: main\n\n" + phrase + "\n</system-reminder>"},
		},
	}

	sanitizeWorkBuddyMessages(req)

	for i, m := range req.Messages {
		if reWorkBuddyGitStatusPRPhrase.MatchString(m.Content) {
			t.Errorf("messages[%d] 仍残留指纹句式: %q", i, m.Content)
		}
		if !strings.Contains(m.Content, "Main branch: main") {
			t.Errorf("messages[%d] 丢失分支语义: %q", i, m.Content)
		}
	}

	raw, _ := json.Marshal(req)
	payload := string(sanitizeWorkBuddyFinalPayload(raw))
	if reWorkBuddyGitStatusPRPhrase.MatchString(payload) {
		t.Errorf("最终载荷仍残留指纹句式: %s", payload)
	}
}

// TestSanitizeWorkBuddyFinalPayload_GitStatusPhrase 字节级兜底:即使结构体层漏网
// (例如指纹藏在工具参数或 ContentParts 里),序列化后也必须被抹掉。
func TestSanitizeWorkBuddyFinalPayload_GitStatusPhrase(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"system","content":"Main branch (you will usually use this for PRs): main"}]}`)
	got := string(sanitizeWorkBuddyFinalPayload(raw))
	if strings.Contains(got, "you will usually use this for PRs") {
		t.Errorf("最终载荷仍残留指纹句式: %s", got)
	}
	if !strings.Contains(got, "Main branch: main") {
		t.Errorf("最终载荷丢失分支语义: %s", got)
	}
}
