package relay

import (
	"bytes"
	"strings"
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

// TestInjectNvidiaChatTemplateKwargs_OtherPoolNeverInjects 锁定:
// chat_template_kwargs 是 NVIDIA NIM 专属约定, Other 号池(TargetProvider="other")的第三方上游
// 思考参数格式各异(阿里云 DeepSeek v4 要 bool、NIM 要字符串、有的根本不认), 默认强塞会触发上游
// 400 "Input should be a valid boolean: parameters.chat_template_kwargs.reasoning_effort"。
// 故 Other 号池条目无论 InjectChatTemplateKwargs 取何值, 一律不注入 chat_template_kwargs。
// 本修复后 Other 号池改走官方 OpenAI reasoning_effort 顶层字段(见 mapToOfficialOpenAIEffort),
// 本用例补断言:Anthropic 入站显式开思考时,产出的 OpenAIChatRequest.ReasoningEffort 为官方值。
// 该用例复现用户报障: other/aliyun/deepseek-v4-flash-0731 在开思考态被强塞字符串档位导致 400。
func TestInjectNvidiaChatTemplateKwargs_OtherPoolNeverInjects(t *testing.T) {
	yesPtr := true

	// Other 号池条目即便显式 injectChatTemplateKwargs=true, 也必须抑制 chat_template_kwargs 注入。
	mappings := []settings.ModelMappingEntry{
		{
			ClientModel:              "other/aliyun/deepseek-v4-flash-0731",
			TargetModel:              "deepseek-v4-flash-0731",
			TargetProvider:           "other",
			TargetGroupID:            "aliyun",
			InjectChatTemplateKwargs: &yesPtr,
		},
	}

	// OpenAI Chat 入站侧(injectNvidiaChatTemplateKwargs)
	reqOther := &OpenAIChatRequest{
		Model: "other/aliyun/deepseek-v4-flash-0731",
	}
	bodyOther := `{"model":"other/aliyun/deepseek-v4-flash-0731","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max"}`
	injectNvidiaChatTemplateKwargs(reqOther, []byte(bodyOther), "deepseek-v4-flash-0731", mappings)
	if reqOther.ChatTemplateKwargs != nil {
		t.Fatalf("Other 号池条目不应注入 chat_template_kwargs(会触发阿里云上游 400 类型不匹配), 实际=%v", reqOther.ChatTemplateKwargs)
	}
	// NIM 链路兜底:reasoning_effort 顶层字段被清空(NIM 不认,会 400),injectNvidiaChatTemplateKwargs 内已屏蔽。
	if reqOther.ReasoningEffort != "" {
		t.Fatalf("NVIDIA 链路 injectNvidiaChatTemplateKwargs 应清空顶层 reasoning_effort(NIM 不认), 实际=%q", reqOther.ReasoningEffort)
	}

	// Anthropic 入站侧(AnthropicToOpenAIChat): 客户端显式开思考(adaptive→max)→ 官方 reasoning_effort 注入。
	anthReq := &AnthropicRequest{
		Model: "other/aliyun/deepseek-v4-flash-0731",
		Thinking: &AnthropicThinking{
			Type: "adaptive",
		},
	}
	outAnth, err := AnthropicToOpenAIChat(anthReq, mappings)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}
	if outAnth.ChatTemplateKwargs != nil {
		t.Fatalf("Other 号池 Anthropic 入站应抑制 chat_template_kwargs 注入, 实际=%v", outAnth.ChatTemplateKwargs)
	}
	// 修复后:Other 号池改用官方 OpenAI reasoning_effort 顶层字段。adaptive→max→官方映射为 high(无 max)。
	if outAnth.ReasoningEffort != "high" {
		t.Fatalf("Other 号池 Anthropic 入站开思考(adaptive→max)应注入官方 reasoning_effort=high, 实际=%q", outAnth.ReasoningEffort)
	}

	// 对照组: 非 Other 号池(NVIDIA)同款条目保持注入, 确认抑制只针对 chat_template_kwargs 语义,
	// 不改 NVIDIA 池仍走 chat_template_kwargs 的零回归契约。
	mappingsNvidia := []settings.ModelMappingEntry{
		{
			ClientModel:              "nvidia/deepseek-ai/deepseek-v4-pro",
			TargetModel:              "deepseek-ai/deepseek-v4-pro",
			TargetProvider:           "nvidia",
			InjectChatTemplateKwargs: &yesPtr,
		},
	}
	reqNvidia := &OpenAIChatRequest{
		Model: "nvidia/deepseek-ai/deepseek-v4-pro",
	}
	bodyNvidia := `{"model":"nvidia/deepseek-ai/deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`
	injectNvidiaChatTemplateKwargs(reqNvidia, []byte(bodyNvidia), "deepseek-ai/deepseek-v4-pro", mappingsNvidia)
	if reqNvidia.ChatTemplateKwargs == nil {
		t.Fatalf("对照组: NVIDIA 号池应正常注入 chat_template_kwargs, 实际被误抑制")
	}
	// NVIDIA 池 reasoning_effort 顶层字段必须被清空(NIM 不认,会 400),保持既有零回归。
	if reqNvidia.ReasoningEffort != "" {
		t.Fatalf("NVIDIA 号池顶层 reasoning_effort 必须清空(NIM 不认,会 400), 实际=%q", reqNvidia.ReasoningEffort)
	}
}

// TestAnthropicToOpenAIChat_OtherPool_ReasoningEffortGrades 锁定 Other 号池 Anthropic→OpenAI
// 转译时思考等级按官方 OpenAI reasoning_effort 取值集{low,medium,high}注入,max→high。
// 各 Anthropic 思考信号(thinking.type+budget_tokens / output_config.effort)经 resolveReasoningEffort
// 归一为内部档位后再映射为官方值,确认档位语义保留且不产 NIM 专有的 max/minimal。
func TestAnthropicToOpenAIChat_OtherPool_ReasoningEffortGrades(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	yesPtr := true
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "other/openai/gpt-4o", TargetModel: "gpt-4o", TargetProvider: "other", TargetGroupID: "openai", InjectChatTemplateKwargs: &yesPtr},
	}

	cases := map[string]string{
		// thinking.type=enabled + budget_tokens 分档(resolveReasoningEffort 内部档→官方映射)
		`{"type":"enabled","budget_tokens":1024}`:  "low",  // <4000 → low
		`{"type":"enabled","budget_tokens":8000}`:  "medium",
		`{"type":"enabled","budget_tokens":32000}`: "high",
		`{"type":"adaptive"}`:                       "high", // adaptive→max→官方 high(无 max)
		`{"type":"disabled"}`:                       "",     // 显式关闭 → 不注入
	}
	for tk, want := range cases {
		req := makeAnthReq(t, "other/openai/gpt-4o", tk, "")
		out, err := AnthropicToOpenAIChat(req, mappings)
		if err != nil {
			t.Fatalf("AnthropicToOpenAIChat(%s) 失败: %v", tk, err)
		}
		if out.ChatTemplateKwargs != nil {
			t.Errorf("Other 号池不应注入 chat_template_kwargs(thinking=%s), 实际=%v", tk, out.ChatTemplateKwargs)
		}
		if out.ReasoningEffort != want {
			t.Errorf("Other 号池 thinking %s → reasoning_effort: want %q, got %q", tk, want, out.ReasoningEffort)
		}
	}
}

// TestOpenAIToAnthropicMessages_ReasoningEffortToThinking 锁定 Other 号池 OpenAI→Anthropic
// 转译时把入站 reasoning_effort 注入 Anthropic 官方 thinking 字段,并守护「max_tokens > budget_tokens」不变式。
func TestOpenAIToAnthropicMessages_ReasoningEffortToThinking(t *testing.T) {
	cases := map[string]int{
		"low":    4000,
		"medium": 12000,
		"high":   32000,
		"max":    64000,
	}
	for effort, wantBudget := range cases {
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"` + effort + `"}`
		req, err := OpenAIToAnthropicMessages([]byte(body), "claude-sonnet-4-5", false)
		if err != nil {
			t.Fatalf("effort=%s: %v", effort, err)
		}
		if req.Thinking == nil || req.Thinking.Type != "enabled" {
			t.Fatalf("effort=%s: 应注入 thinking.type=enabled, 实际=%v", effort, req.Thinking)
		}
		if req.Thinking.BudgetTokens != wantBudget {
			t.Errorf("effort=%s: budget_tokens want=%d got=%d", effort, wantBudget, req.Thinking.BudgetTokens)
		}
		// 守护「max_tokens > budget_tokens」:无客户端 max_tokens 时兜底 8192 若 < budget 会被抬升。
		if req.MaxTokens == nil || *req.MaxTokens <= req.Thinking.BudgetTokens {
			t.Errorf("effort=%s: max_tokens(%v) 必须 > budget_tokens(%d)", effort, req.MaxTokens, req.Thinking.BudgetTokens)
		}
	}

	// opt-in OFF:无 reasoning_effort → 不注入 thinking,保持既有零回归(回归红线对齐 TestOpenAIToAnthropicMessages_Basic)。
	reqOff, err := OpenAIToAnthropicMessages([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), "claude", false)
	if err != nil {
		t.Fatalf("opt-in OFF: %v", err)
	}
	if reqOff.Thinking != nil {
		t.Fatalf("无 reasoning_effort 应不注入 thinking, 实际=%v", reqOff.Thinking)
	}
}

// TestOpenAIChatToAnthropic_NonStreamThinkingRoundTrip 锁定非流式 OpenAI→Anthropic 回译
// 把上游 reasoning_content 转成 Anthropic thinking content block,signal 顺序:thinking 先于 text。
func TestOpenAIChatToAnthropic_NonStreamThinkingRoundTrip(t *testing.T) {
	resp := &OpenAIChatResponse{
		ID: "chatcmpl-1", Object: "chat.completion", Model: "gpt-4o",
		Choices: []OpenAIChatChoice{{
			Index: 0, FinishReason: "stop",
			Message: ChatMessage{Role: "assistant", Content: "answer", ReasoningContent: "let me think"},
		}},
		Usage: OpenAIChatUsage{PromptTokens: 5, CompletionTokens: 3},
	}
	anth := OpenAIChatToAnthropic(resp)
	if len(anth.Content) < 2 {
		t.Fatalf("应含 thinking + text 两块, 实际 %d 块: %+v", len(anth.Content), anth.Content)
	}
	if anth.Content[0].Type != "thinking" || anth.Content[0].Thinking != "let me think" {
		t.Errorf("首块应为 thinking 块, 实际=%+v", anth.Content[0])
	}
	if anth.Content[0].Signature != "" {
		t.Errorf("thinking 块 signature 应空串占位, 实际=%q", anth.Content[0].Signature)
	}
	if anth.Content[1].Type != "text" || anth.Content[1].Text != "answer" {
		t.Errorf("次块应为 text 块 answer, 实际=%+v", anth.Content[1])
	}
}

// TestAnthropicResponseToOpenAIChat_NonStreamThinkingRoundTrip 锁定非流式 Anthropic→OpenAI 回译
// 把上游 thinking content block 转成 OpenAI reasoning_content(修复既往 b.Text 误读导致丢弃)。
func TestAnthropicResponseToOpenAIChat_NonStreamThinkingRoundTrip(t *testing.T) {
	resp := &AnthropicResponse{
		ID: "msg_1", Model: "claude", StopReason: "end_turn",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "internal reasoning"},
			{Type: "text", Text: "final answer"},
		},
		Usage: AnthropicResponseUsage{InputTokens: 1, OutputTokens: 2},
	}
	out := AnthropicResponseToOpenAIChat(resp)
	if len(out.Choices) != 1 {
		t.Fatalf("Choices: want 1, got %d", len(out.Choices))
	}
	msg := out.Choices[0].Message
	if msg.ReasoningContent != "internal reasoning" {
		t.Errorf("ReasoningContent: want 'internal reasoning', got %q", msg.ReasoningContent)
	}
	if msg.Reasoning != "internal reasoning" {
		t.Errorf("Reasoning 兜底字段: want 'internal reasoning', got %q", msg.Reasoning)
	}
	if msg.Content != "final answer" {
		t.Errorf("Content: want 'final answer', got %q", msg.Content)
	}
}

// TestAnthropicSSEToOpenAIChatSSE_ThinkingStream 锁定流式 Anthropic→OpenAI 回译:
// 上游 thinking_delta → 下游 delta.reasoning_content 增量,signature_delta 跳过。
func TestAnthropicSSEToOpenAIChatSSE_ThinkingStream(t *testing.T) {
	events := []string{
		`data: {"type":"message_start","message":{"id":"msg_t"}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"partial"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":""}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}
	reader := bytes.NewReader([]byte(strings.Join(events, "\n")))
	var out bytes.Buffer
	_, _, err := AnthropicSSEToOpenAIChatSSE(reader, &out, "claude")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	chunks := parseOpenAIChunks(t, out.Bytes())
	var reasoningAcc string
	var hasReasoningChunk bool
	for _, c := range chunks {
		choices, ok := c["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		delta, ok := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := delta["reasoning_content"].(string); ok && v != "" {
			hasReasoningChunk = true
			reasoningAcc += v
		}
	}
	if !hasReasoningChunk {
		t.Fatalf("应含 reasoning_content delta chunk, 实际无; chunks=%v", chunks)
	}
	if reasoningAcc != "partial" {
		t.Errorf("reasoning_content 累积: want 'partial', got %q", reasoningAcc)
	}
}
