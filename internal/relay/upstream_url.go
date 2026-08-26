package relay

import (
	"regexp"
	"strings"
)

// apiVersionSuffixRegex 匹配以 /v1, /v2, /v3, /v4 等版本号结尾的路径段 (例如 /v4, /v1alpha, /v2beta 等)
var apiVersionSuffixRegex = regexp.MustCompile(`(?i)/v\d+(?:[a-z]+)?$`)

// hasAPIVersionSuffix 检查 baseURL 末尾是否已经包含版本号路径段 (如 /v1, /v2, /v3, /v4 等)
func hasAPIVersionSuffix(baseURL string) bool {
	clean := strings.TrimRight(baseURL, "/")
	return apiVersionSuffixRegex.MatchString(clean)
}

// BuildOpenAIChatURL 构造上游 OpenAI Chat Completions 完整端点 URL。
// 规则：
// 1. 若 baseURL 已以 /chat/completions 结尾，原样返回；
// 2. 若 baseURL 已自带 /v1, /v2, /v3, /v4... 等版本号（如智谱 /api/paas/v4），拼接 /chat/completions；
// 3. 否则默认拼接 /v1/chat/completions。
func BuildOpenAIChatURL(baseURL string) string {
	clean := strings.TrimRight(baseURL, "/")
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, "/chat/completions") {
		return clean
	}
	if hasAPIVersionSuffix(clean) {
		return clean + "/chat/completions"
	}
	return clean + "/v1/chat/completions"
}

// BuildAnthropicMessagesURL 构造上游 Anthropic Messages 完整端点 URL。
// 规则：
// 1. 若 baseURL 已以 /messages 结尾，原样返回；
// 2. 若 baseURL 已自带 /v1, /v2, /v3, /v4... 等版本号，拼接 /messages；
// 3. 否则默认拼接 /v1/messages。
func BuildAnthropicMessagesURL(baseURL string) string {
	clean := strings.TrimRight(baseURL, "/")
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, "/messages") {
		return clean
	}
	if hasAPIVersionSuffix(clean) {
		return clean + "/messages"
	}
	return clean + "/v1/messages"
}

// BuildOpenAIModelsURL 构造上游 OpenAI /models 完整端点 URL。
// 规则：
// 1. 若 baseURL 已以 /models 结尾，原样返回；
// 2. 若 baseURL 已自带 /v1, /v2, /v3, /v4... 等版本号，拼接 /models；
// 3. 否则默认拼接 /v1/models。
func BuildOpenAIModelsURL(baseURL string) string {
	clean := strings.TrimRight(baseURL, "/")
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, "/models") {
		return clean
	}
	if hasAPIVersionSuffix(clean) {
		return clean + "/models"
	}
	return clean + "/v1/models"
}
