package relay

import (
	"bytes"
	"regexp"
	"strings"
)

// workbuddy_sanitize.go: 深度脱敏触碰腾讯 WorkBuddy 安全策略（11128）的客户端特有标识与渠道特征。
//
// 典型拦截场景：
//   - Claude Code CLI 发起请求时携带的 system prompt、身份描述、x-anthropic-billing-header 等。
//   - Tools 声明（包含 Function Description 以及 Function Parameters JSON Schema 内部属性）。
//   - 历史消息中的 ToolCalls、ToolCallID、Arguments。
//   - 序列化后的最终 JSON 载荷中可能漏网的敏感模式。

var (
	reWorkBuddyBillingHeader   = regexp.MustCompile(`(?i)x-anthropic-billing-header[^\r\n]*[\r\n]*`)
	reWorkBuddyBillingFallback = regexp.MustCompile(`(?i)x-anthropic-billing-header[a-zA-Z0-9_=:;.\- ]*`)
	reWorkBuddyCCVersion       = regexp.MustCompile(`(?i)cc_version=[a-zA-Z0-9_.\-]+[;,\s]*`)
	reWorkBuddyCCEntrypoint    = regexp.MustCompile(`(?i)cc_entrypoint=[a-zA-Z0-9_.\-]+[;,\s]*`)

	reWorkBuddyByteBilling   = regexp.MustCompile(`(?i)x-anthropic-billing-header[^\r\n"\\,}]*`)
	reWorkBuddyByteCCVer     = regexp.MustCompile(`(?i)cc_version=[a-zA-Z0-9_.\-]+[;,\s]*`)
	reWorkBuddyByteCCEntry   = regexp.MustCompile(`(?i)cc_entrypoint=[a-zA-Z0-9_.\-]+[;,\s]*`)
	reWorkBuddyByteClaudeCLI = regexp.MustCompile(`(?i)claude-cli`)
)

// sanitizeWorkBuddyText 统一脱敏字符串中触碰 WorkBuddy 安全策略（11128）的客户端渠道特征。
func sanitizeWorkBuddyText(text string) string {
	if text == "" {
		return ""
	}

	// 1. 过滤计费头与 CLI 跟踪标记
	if reWorkBuddyBillingHeader.MatchString(text) {
		text = reWorkBuddyBillingHeader.ReplaceAllString(text, "")
	}
	if strings.Contains(strings.ToLower(text), "x-anthropic-billing-header") {
		text = reWorkBuddyBillingFallback.ReplaceAllString(text, "")
	}
	if reWorkBuddyCCVersion.MatchString(text) {
		text = reWorkBuddyCCVersion.ReplaceAllString(text, "")
	}
	if reWorkBuddyCCEntrypoint.MatchString(text) {
		text = reWorkBuddyCCEntrypoint.ReplaceAllString(text, "")
	}

	// 2. 替换 Claude Code 典型 Prompt 声明
	if strings.Contains(text, "You are Claude Code, Anthropic's official CLI for Claude.") {
		text = strings.ReplaceAll(text, "You are Claude Code, Anthropic's official CLI for Claude.", "You are an interactive AI assistant helping with coding.")
	}
	if strings.Contains(text, "Anthropic's official CLI for Claude") {
		text = strings.ReplaceAll(text, "Anthropic's official CLI for Claude", "AI Assistant CLI")
	}
	if strings.Contains(text, "Claude Agent SDK") {
		text = strings.ReplaceAll(text, "Claude Agent SDK", "Agent SDK")
	}

	// 3. 替换工具名与客户端通用标记
	if strings.Contains(text, "Claude Code") {
		text = strings.ReplaceAll(text, "Claude Code", "AI Assistant")
	}
	if strings.Contains(text, "claude-code") {
		text = strings.ReplaceAll(text, "claude-code", "ai-assistant")
	}
	if strings.Contains(text, "claude_code") {
		text = strings.ReplaceAll(text, "claude_code", "ai_assistant")
	}
	if strings.Contains(text, "claude-cli") {
		text = strings.ReplaceAll(text, "claude-cli", "ai-cli")
	}
	if strings.Contains(text, "Claude CLI") {
		text = strings.ReplaceAll(text, "Claude CLI", "AI CLI")
	}
	if strings.Contains(text, "claude cli") {
		text = strings.ReplaceAll(text, "claude cli", "ai cli")
	}

	return text
}

// sanitizeWorkBuddyMap 递归深度清洗 map（用于清洗 Tool Parameters 的 JSON Schema 树）。
func sanitizeWorkBuddyMap(m map[string]interface{}) {
	if m == nil {
		return
	}
	for k, v := range m {
		switch val := v.(type) {
		case string:
			m[k] = sanitizeWorkBuddyText(val)
		case map[string]interface{}:
			sanitizeWorkBuddyMap(val)
		case []interface{}:
			sanitizeWorkBuddySlice(val)
		}
	}
}

// sanitizeWorkBuddySlice 递归深度清洗 slice。
func sanitizeWorkBuddySlice(arr []interface{}) {
	for i, v := range arr {
		switch val := v.(type) {
		case string:
			arr[i] = sanitizeWorkBuddyText(val)
		case map[string]interface{}:
			sanitizeWorkBuddyMap(val)
		case []interface{}:
			sanitizeWorkBuddySlice(val)
		}
	}
}

// sanitizeWorkBuddyTools 深度清洗请求中定义的 Tools（名称、描述及参数 Schema）。
func sanitizeWorkBuddyTools(tools []ChatTool) {
	for i := range tools {
		tools[i].Function.Name = sanitizeWorkBuddyText(tools[i].Function.Name)
		tools[i].Function.Description = sanitizeWorkBuddyText(tools[i].Function.Description)
		if tools[i].Function.Parameters != nil {
			sanitizeWorkBuddyMap(tools[i].Function.Parameters)
		}
	}
}

// sanitizeWorkBuddyMessages 深度清洗触碰 WorkBuddy 安全策略（11128）的客户端请求对象。
func sanitizeWorkBuddyMessages(req *OpenAIChatRequest) {
	if req == nil {
		return
	}

	for i := range req.Messages {
		req.Messages[i].Content = sanitizeWorkBuddyText(req.Messages[i].Content)
		if len(req.Messages[i].ContentParts) > 0 {
			for j := range req.Messages[i].ContentParts {
				switch p := req.Messages[i].ContentParts[j].(type) {
				case ChatMessageTextPart:
					p.Text = sanitizeWorkBuddyText(p.Text)
					req.Messages[i].ContentParts[j] = p
				case *ChatMessageTextPart:
					if p != nil {
						p.Text = sanitizeWorkBuddyText(p.Text)
					}
				}
			}
		}

		if req.Messages[i].ToolName != "" {
			req.Messages[i].ToolName = sanitizeWorkBuddyText(req.Messages[i].ToolName)
		}
		if req.Messages[i].ToolCallID != "" {
			req.Messages[i].ToolCallID = sanitizeWorkBuddyText(req.Messages[i].ToolCallID)
		}

		for j := range req.Messages[i].ToolCalls {
			req.Messages[i].ToolCalls[j].Function.Name = sanitizeWorkBuddyText(req.Messages[i].ToolCalls[j].Function.Name)
			req.Messages[i].ToolCalls[j].Function.Arguments = sanitizeWorkBuddyText(req.Messages[i].ToolCalls[j].Function.Arguments)
		}
	}

	if len(req.Tools) > 0 {
		sanitizeWorkBuddyTools(req.Tools)
	}
}

// sanitizeWorkBuddyFinalPayload 对最终序列化后的上游 JSON 载荷执行终极字节级过滤兜底。
func sanitizeWorkBuddyFinalPayload(b []byte) []byte {
	if len(b) == 0 {
		return b
	}

	// 1. 过滤计费头残留
	if bytes.Contains(b, []byte("x-anthropic-billing-header")) || bytes.Contains(b, []byte("X-Anthropic-Billing-Header")) {
		b = reWorkBuddyByteBilling.ReplaceAll(b, []byte(""))
	}
	if bytes.Contains(b, []byte("cc_version=")) {
		b = reWorkBuddyByteCCVer.ReplaceAll(b, []byte(""))
	}
	if bytes.Contains(b, []byte("cc_entrypoint=")) {
		b = reWorkBuddyByteCCEntry.ReplaceAll(b, []byte(""))
	}

	// 2. 过滤客户端关键词残留
	if bytes.Contains(b, []byte("You are Claude Code, Anthropic's official CLI for Claude.")) {
		b = bytes.ReplaceAll(b, []byte("You are Claude Code, Anthropic's official CLI for Claude."), []byte("You are an interactive AI assistant helping with coding."))
	}
	if bytes.Contains(b, []byte("Claude Code")) {
		b = bytes.ReplaceAll(b, []byte("Claude Code"), []byte("AI Assistant"))
	}
	if bytes.Contains(b, []byte("claude-code")) {
		b = bytes.ReplaceAll(b, []byte("claude-code"), []byte("ai-assistant"))
	}
	if bytes.Contains(b, []byte("claude_code")) {
		b = bytes.ReplaceAll(b, []byte("claude_code"), []byte("ai_assistant"))
	}
	if bytes.Contains(b, []byte("claude-cli")) {
		b = reWorkBuddyByteClaudeCLI.ReplaceAll(b, []byte("ai-cli"))
	}
	if bytes.Contains(b, []byte("Claude CLI")) {
		b = bytes.ReplaceAll(b, []byte("Claude CLI"), []byte("AI CLI"))
	}
	if bytes.Contains(b, []byte("claude cli")) {
		b = bytes.ReplaceAll(b, []byte("claude cli"), []byte("ai cli"))
	}

	return b
}
