package relay

import (
	"testing"
)

func TestInferOwnedBy(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"gemini-2.5-pro", "google"},
		{"gemini-2.5-flash-thinking", "google"},
		{"text-embedding-004", "google"},
		{"learnlm-1.5-pro-experimental", "google"},
		{"aqa", "google"},
		{"tab_flash_lite_preview", "google"},
		{"claude-3-5-sonnet", "anthropic"},
		{"claude-opus-4-6", "anthropic"},
		{"deepseek-chat", "deepseek"},
		{"deepseek-reasoner", "deepseek"},
		{"deepseek-ai/deepseek-r1", "deepseek"},
		{"gpt-4o", "openai"},
		{"gpt-5", "openai"},
		{"o1-mini", "openai"},
		{"o3-pro", "openai"},
		{"o4-mini", "openai"},
		{"meta/llama-3.3-70b-instruct", "meta"},
		{"llama-3.1-nemotron-70b", "meta"},
		{"qwen-2.5-72b", "qwen"},
		{"moonshotai/kimi-k2.5", "moonshot"},
		{"kimi-k2.5", "moonshot"},
		{"glm-4.6", "zhipu"},
		{"nvidia/llama-3.1-nemotron-70b-instruct", "nvidia"},
		{"something-nemotron-70b", "nvidia"},
		{"", "openai"},        // 空模型兜底 openai
		{"unknown-random", "openai"}, // 兜底 openai(不再是 google)
	}
	for _, c := range cases {
		got := inferOwnedBy(c.model)
		if got != c.want {
			t.Errorf("inferOwnedBy(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}
