package relay

import (
	"encoding/json"
	"testing"
)

func TestTranslateAnthropicThinking(t *testing.T) {
	rawJSON := `{
		"model": "claude-3-5-sonnet",
		"messages": [{"role": "user", "content": "Hello"}],
		"thinking": {
			"type": "enabled",
			"budget_tokens": 2048
		}
	}`

	var req AnthropicRequest
	err := json.Unmarshal([]byte(rawJSON), &req)
	if err != nil {
		t.Fatalf("Failed to unmarshal Anthropic request with thinking: %v", err)
	}

	if req.Thinking == nil {
		t.Fatalf("Expected Thinking struct to be non-nil")
	}
	if req.Thinking.BudgetTokens != 2048 {
		t.Errorf("Expected BudgetTokens=2048, got %d", req.Thinking.BudgetTokens)
	}

	gemReq := TranslateAnthropicToGemini(&req)
	if gemReq.GenerationConfig == nil || gemReq.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("Expected GenerationConfig.ThinkingConfig to be populated")
	}
	if gemReq.GenerationConfig.ThinkingConfig.ThinkingBudget != 2048 {
		t.Errorf("Expected ThinkingBudget=2048, got %d", gemReq.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
}

func TestTranslateToolsSorting(t *testing.T) {
	tools := []AnthropicTool{
		{Name: "zeta_tool", Description: "Zeta"},
		{Name: "alpha_tool", Description: "Alpha"},
		{Name: "beta_tool", Description: "Beta"},
	}

	decls := translateToolsToGemini(tools)
	if len(decls) == 0 || len(decls[0].FunctionDeclarations) != 3 {
		t.Fatalf("Expected 3 function declarations")
	}

	funcs := decls[0].FunctionDeclarations
	if funcs[0].Name != "alpha_tool" || funcs[1].Name != "beta_tool" || funcs[2].Name != "zeta_tool" {
		t.Errorf("Expected tools sorted as [alpha_tool, beta_tool, zeta_tool], got [%s, %s, %s]",
			funcs[0].Name, funcs[1].Name, funcs[2].Name)
	}
}

func TestTruncateToolResultText(t *testing.T) {
	shortText := "Hello World"
	if got := truncateToolResultText(shortText, 100); got != shortText {
		t.Errorf("Expected shortText un-truncated, got: %s", got)
	}

	longText := ""
	for i := 0; i < 1000; i++ {
		longText += "0123456789"
	} // 10000 chars

	truncated := truncateToolResultText(longText, 1000)
	if len(truncated) >= len(longText) {
		t.Errorf("Expected truncated length < %d, got %d", len(longText), len(truncated))
	}
	if !testing.Verbose() {
		// Verify head and tail preservation
		if truncated[:500] != longText[:500] {
			t.Errorf("Head preservation failed")
		}
		if truncated[len(truncated)-500:] != longText[len(longText)-500:] {
			t.Errorf("Tail preservation failed")
		}
	}
}
