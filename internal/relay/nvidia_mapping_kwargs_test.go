package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

// TestInjectNvidiaChatTemplateKwargs_CustomMappingDisabled 锁定:
// 当用户在 UI「自定义中继模型映射」中对特定模型取消勾选「注入 Template Kwargs」(injectChatTemplateKwargs: false) 时,
// 代理层必须尊重用户的配置,不再注入 chat_template_kwargs,防止触发上游 404。
func TestInjectNvidiaChatTemplateKwargs_CustomMappingDisabled(t *testing.T) {
	noPtr := false
	yesPtr := true

	mappings := []settings.ModelMappingEntry{
		{
			ClientModel:              "custom-deepseek-v4",
			TargetModel:              "moonshotai/kimi-k2.6",
			InjectChatTemplateKwargs: &noPtr,
		},
		{
			ClientModel:              "normal-model",
			TargetModel:              "z-ai/glm-5.2",
			InjectChatTemplateKwargs: &yesPtr,
		},
	}

	// 场景 1: 取消勾选的自定义模型 -> 抑制注入
	reqDisabled := &OpenAIChatRequest{
		Model: "custom-deepseek-v4",
	}
	bodyDisabled := `{"model":"custom-deepseek-v4","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`
	injectNvidiaChatTemplateKwargs(reqDisabled, []byte(bodyDisabled), "custom-deepseek-v4", mappings)

	if reqDisabled.ChatTemplateKwargs != nil {
		t.Fatalf("对于已取消勾选 injectChatTemplateKwargs 的模型, 不应注入 chat_template_kwargs, 实际=%v", reqDisabled.ChatTemplateKwargs)
	}

	// 场景 2: 保持勾选的自定义模型 -> 正常注入
	reqEnabled := &OpenAIChatRequest{
		Model: "normal-model",
	}
	bodyEnabled := `{"model":"normal-model","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`
	injectNvidiaChatTemplateKwargs(reqEnabled, []byte(bodyEnabled), "z-ai/glm-5.2", mappings)

	if reqEnabled.ChatTemplateKwargs == nil {
		t.Fatalf("对于保持勾选 injectChatTemplateKwargs 的模型, 应该正常注入 chat_template_kwargs")
	}

	// 场景 3: Anthropic 转换侧校验
	anthReq := &AnthropicRequest{
		Model: "custom-deepseek-v4",
		Thinking: &AnthropicThinking{
			Type: "enabled",
		},
	}
	outAnth, err := AnthropicToOpenAIChat(anthReq, mappings)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}
	if outAnth.ChatTemplateKwargs != nil {
		t.Fatalf("Anthropic 入站已取消勾选的模型不应注入 chat_template_kwargs, 实际=%v", outAnth.ChatTemplateKwargs)
	}
}
