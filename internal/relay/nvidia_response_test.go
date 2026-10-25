package relay

import (
	"strings"
	"testing"
)

// TestSanitizeOpenAIToolCallsSSE 针对 OpenAI 兼容流式输出中非标空数组 "tool_calls":[] 进行清洗测试。
func TestSanitizeOpenAIToolCallsSSE(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "含有非标空数组 tool_calls:[] 的思考帧应被规整为 tool_calls:null",
			input:    `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"content":"","reasoning_content":"We","tool_calls":[]}}]}`,
			expected: `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"content":"","reasoning_content":"We","tool_calls":null}}]}`,
		},
		{
			name:     "无 tool_calls 字段的正常文本帧保持原样",
			input:    `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			expected: `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		},
		{
			name:     "真实工具调用帧保持原样不被误篡改",
			input:    `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search"}}]}}]}`,
			expected: `data: {"id":"c8bb8482","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search"}}]}}]}`,
		},
		{
			name:     "流终止帧 [DONE] 保持原样",
			input:    `data: [DONE]`,
			expected: `data: [DONE]`,
		},
		{
			name:     "心跳与注释行保持原样",
			input:    `: heartbeat`,
			expected: `: heartbeat`,
		},
		{
			name:     "多处 tool_calls:[] 均能被完全清洗",
			input:    `data: {"choices":[{"delta":{"tool_calls":[]}},{"delta":{"tool_calls":[]}}]}`,
			expected: `data: {"choices":[{"delta":{"tool_calls":null}},{"delta":{"tool_calls":null}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeOpenAIToolCallsSSE(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeOpenAIToolCallsSSE() mismatch\ngot:  %s\nwant: %s", result, tt.expected)
			}
			// 确保结果中绝不残留 "tool_calls":[]
			if strings.Contains(result, `"tool_calls":[]`) {
				t.Errorf("result still contains \"tool_calls\":[]: %s", result)
			}
		})
	}
}
