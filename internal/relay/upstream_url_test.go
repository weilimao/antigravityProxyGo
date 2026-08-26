package relay

import (
	"testing"
)

func TestBuildOpenAIChatURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "智谱开放平台(带 /v4 版本号)",
			baseURL:  "https://open.bigmodel.cn/api/paas/v4",
			expected: "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		},
		{
			name:     "智谱开放平台(带末尾斜杠)",
			baseURL:  "https://open.bigmodel.cn/api/paas/v4/",
			expected: "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		},
		{
			name:     "火山方舟(带 /v3 版本号)",
			baseURL:  "https://ark.cn-beijing.volces.com/api/v3",
			expected: "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
		},
		{
			name:     "标准 OpenAI(带 /v1)",
			baseURL:  "https://api.openai.com/v1",
			expected: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:     "标准 OpenAI(带 /v1 和斜杠)",
			baseURL:  "https://api.openai.com/v1/",
			expected: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:     "裸域名(不带版本号，自动补 /v1)",
			baseURL:  "https://api.openai.com",
			expected: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:     "裸域名带斜杠",
			baseURL:  "https://api.openai.com/",
			expected: "https://api.openai.com/v1/chat/completions",
		},
		{
			name:     "带 /v2 版本号",
			baseURL:  "https://api.example.com/v2",
			expected: "https://api.example.com/v2/chat/completions",
		},
		{
			name:     "用户误填完整 /chat/completions",
			baseURL:  "https://open.bigmodel.cn/api/paas/v4/chat/completions",
			expected: "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		},
		{
			name:     "带 /v1beta 扩展版本号",
			baseURL:  "https://api.example.com/v1beta",
			expected: "https://api.example.com/v1beta/chat/completions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildOpenAIChatURL(tc.baseURL)
			if got != tc.expected {
				t.Errorf("BuildOpenAIChatURL(%q) = %q, want %q", tc.baseURL, got, tc.expected)
			}
		})
	}
}

func TestBuildAnthropicMessagesURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "Anthropic 官方(带 /v1)",
			baseURL:  "https://api.anthropic.com/v1",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "Anthropic 裸域名",
			baseURL:  "https://api.anthropic.com",
			expected: "https://api.anthropic.com/v1/messages",
		},
		{
			name:     "第三方自定义 /v2",
			baseURL:  "https://custom-anthropic.com/v2",
			expected: "https://custom-anthropic.com/v2/messages",
		},
		{
			name:     "已含 /messages 完整路径",
			baseURL:  "https://api.anthropic.com/v1/messages",
			expected: "https://api.anthropic.com/v1/messages",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildAnthropicMessagesURL(tc.baseURL)
			if got != tc.expected {
				t.Errorf("BuildAnthropicMessagesURL(%q) = %q, want %q", tc.baseURL, got, tc.expected)
			}
		})
	}
}

func TestBuildOpenAIModelsURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "带 /v4 版本号",
			baseURL:  "https://open.bigmodel.cn/api/paas/v4",
			expected: "https://open.bigmodel.cn/api/paas/v4/models",
		},
		{
			name:     "带 /v1 版本号",
			baseURL:  "https://api.openai.com/v1",
			expected: "https://api.openai.com/v1/models",
		},
		{
			name:     "裸域名",
			baseURL:  "https://api.openai.com",
			expected: "https://api.openai.com/v1/models",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildOpenAIModelsURL(tc.baseURL)
			if got != tc.expected {
				t.Errorf("BuildOpenAIModelsURL(%q) = %q, want %q", tc.baseURL, got, tc.expected)
			}
		})
	}
}
