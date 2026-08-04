package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

// TestInjectNvidiaChatTemplateKwargs_DeepSeekV4Pro_DefaultInjects 锁定:
// 探针实测证明 deepseek-ai/deepseek-v4-pro 上游支持 chat_template_kwargs 且吐出 25 帧 reasoning_content。
// 映射配置默认 (nil/true) 时必须正常注入 chat_template_kwargs, 触发思考输出。
func TestInjectNvidiaChatTemplateKwargs_DeepSeekV4Pro_DefaultInjects(t *testing.T) {
	chatReq := &OpenAIChatRequest{
		Model: "deepseek-ai/deepseek-v4-pro",
	}

	body := `{"model":"deepseek-ai/deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`

	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "deepseek-ai/deepseek-v4-pro")

	if chatReq.ChatTemplateKwargs == nil {
		t.Fatalf("deepseek-ai/deepseek-v4-pro 默认应注入 chat_template_kwargs 以触发思考内容")
	}
}

// TestInjectNvidiaChatTemplateKwargs_DeepSeekV4Pro_DisabledByMapping 锁定:
// 当用户在 UI「自定义中继模型映射」显式取消勾选 (InjectChatTemplateKwargs: false) 时, 代理层切切实实抑制注入。
func TestInjectNvidiaChatTemplateKwargs_DeepSeekV4Pro_DisabledByMapping(t *testing.T) {
	noPtr := false
	mappings := []settings.ModelMappingEntry{
		{
			ClientModel:              "deepseek-ai/deepseek-v4-pro",
			TargetModel:              "deepseek-ai/deepseek-v4-pro",
			InjectChatTemplateKwargs: &noPtr,
		},
	}

	chatReq := &OpenAIChatRequest{
		Model: "deepseek-ai/deepseek-v4-pro",
	}

	body := `{"model":"deepseek-ai/deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`

	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "deepseek-ai/deepseek-v4-pro", mappings)

	if chatReq.ChatTemplateKwargs != nil {
		t.Fatalf("当映射中显式配置 InjectChatTemplateKwargs=false 时, 不应注入 chat_template_kwargs, 实际=%v", chatReq.ChatTemplateKwargs)
	}
}
