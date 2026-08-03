package relay

import "strings"

// router_ownedby.go: 模型归属(owned_by/Provider)的兜底推断。
//
// 与 settings.ModelMappingEntry.OwnedBy 解耦:OwnedBy 留空时由此处按模型名前缀
// 猜一个合理的归属,供 /v1/models 展示归类与 /route/* 入口预判 Provider 共用。
// 命中即返回;都不命中返回 "openai"(OpenAI 兼容协议的兜底默认,而非旧硬编码 "google",
// 因为未来多号池场景里多数第三方上游是 OpenAI 兼容,而 Gemini 直连链路已有显式 OwnedBy)。

// inferOwnedBy 按模型 id 推断归属号池/Provider。
// 规则保守:只判明显前缀,判不出一律 "openai"。
func inferOwnedBy(modelID string) string {
	m := strings.ToLower(strings.TrimSpace(modelID))
	if m == "" {
		return "openai"
	}
	switch {
	case strings.HasPrefix(m, "gemini"),
		strings.HasPrefix(m, "text-embedding-0"),
		strings.HasPrefix(m, "learnlm"),
		strings.HasPrefix(m, "aqa"),
		strings.HasPrefix(m, "tab_"):
		return "google"
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.Contains(m, "/deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "nvidia/"):
		return "nvidia"
	case strings.HasPrefix(m, "meta/"),
		strings.HasPrefix(m, "llama"),
		strings.Contains(m, "/llama"):
		return "meta"
	case strings.HasSuffix(m, "-nemotron"),
		strings.Contains(m, "nemotron") && !strings.HasPrefix(m, "llama"):
		return "nvidia"
	case strings.HasPrefix(m, "gpt"),
		strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"),
		strings.HasPrefix(m, "chatgpt"):
		return "openai"
	case strings.HasPrefix(m, "qwen"),
		strings.Contains(m, "/qwen"):
		return "qwen"
	case strings.HasPrefix(m, "moonshot"),
		strings.HasPrefix(m, "kimi"),
		strings.Contains(m, "/kimi"),
		strings.HasPrefix(m, "moonshotai"):
		return "moonshot"
	case strings.HasPrefix(m, "glm"),
		strings.Contains(m, "/glm"):
		return "zhipu"
	}
	return "openai"
}
