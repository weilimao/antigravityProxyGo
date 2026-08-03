package relay

// compat_translate_helpers.go holds the small helpers for the translate pipeline:
// calcAnthropicCommittedBudget / findToolNameByID / findOpenAIToolNameByID /
// extractToken / parseToolCallArgs / truncateToolResultText.
// Split from compat_translate.go, physical move only, line-for-line equivalent.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func calcAnthropicCommittedBudget(anthReq *AnthropicRequest) int {
	if anthReq.Thinking == nil || !strings.EqualFold(anthReq.Thinking.Type, "enabled") {
		return 0
	}
	if anthReq.Thinking.BudgetTokens <= 0 {
		return 0
	}
	return anthReq.Thinking.BudgetTokens
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
