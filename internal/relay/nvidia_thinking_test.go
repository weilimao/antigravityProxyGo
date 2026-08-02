package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// nvidia_thinking_test.go 锁定 NVIDIA 池路径(/nvidia/v1/messages)对上游 reasoning_content 的
// Anthropic thinking 流式协议翻译,严格对齐官方序列:
//   content_block_start(thinking, thinking:"", signature:"")
//   → content_block_delta(thinking_delta)*
//   → content_block_delta(signature_delta, signature:"")   // 关块前空串占位
//   → content_block_stop
//   → content_block_start(text, text:"")
//   → content_block_delta(text_delta)*
//   → content_block_stop
//
// 并锁定无推理模型适配:reasoning_content 恒空时绝不误开 thinking 块(回归红线)。

// reasoningChunkLine 构造一个带 reasoning_content 增量的上游 stream chunk SSE 行。
func reasoningChunkLine(delta string) string {
	return mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"reasoning_content": delta},
			},
		},
	})
}

// usageChunkLine 构造一个仅含 usage 的上游 fanout 行(NIM 常在 finish 帧后发)。
func usageChunkLine() string {
	return mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{},
		"usage": map[string]interface{}{
			"prompt_tokens":     10,
			"completion_tokens": 20,
		},
	})
}

// TestReasoningEmitsThinkingBlock 锁定核心修复:上游 reasoning_content 多帧 → 下游依次
// content_block_start(thinking) → thinking_delta* → signature_delta(空) → content_block_stop。
func TestReasoningEmitsThinkingBlock(t *testing.T) {
	upstream := writeUpstream(
		reasoningChunkLine("I need to compute the GCD.\n"),
		reasoningChunkLine("1071 = 2 × 462 + 147"),
		finishChunkLine("stop"),
		usageChunkLine(),
		"[DONE]",
	)
	events := runAnthropicSSE(t, upstream)

	requireEvent(t, events, "message_start")
	requireEvent(t, events, "content_block_start")
	requireEvent(t, events, "content_block_delta")
	requireEvent(t, events, "content_block_stop")
	requireEvent(t, events, "message_delta")
	requireEvent(t, events, "message_stop")

	// 找出 thinking 块开块事件,断言 content_block.type=="thinking" 且含空 thinking/signature
	var startEv sseEvent
	for _, ev := range events {
		if ev.event == "content_block_start" {
			startEv = ev
			break
		}
	}
	if startEv.event == "" {
		t.Fatalf("未找到 content_block_start 事件")
	}
	m := dataMap(t, startEv)
	cb, _ := m["content_block"].(map[string]interface{})
	if cb == nil || cb["type"] != "thinking" {
		t.Fatalf("首块应为 thinking 块,实际 content_block=%v", cb)
	}
	if cb["thinking"] != "" || cb["signature"] != "" {
		t.Fatalf("thinking 块开块时 thinking/signature 必须为空串,实际 cb=%v", cb)
	}
	if m["index"].(float64) != 0 {
		t.Fatalf("thinking 块应占 index 0,实际 index=%v", m["index"])
	}

	// 断言存在 thinking_delta 与 signature_delta 两类 delta
	var hasThinkingDelta, hasSignatureDelta bool
	var thinkingText string
	for _, ev := range events {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta == nil {
			continue
		}
		switch delta["type"] {
		case "thinking_delta":
			hasThinkingDelta = true
			if s, ok := delta["thinking"].(string); ok {
				thinkingText += s
			}
		case "signature_delta":
			hasSignatureDelta = true
			if delta["signature"] != "" {
				t.Fatalf("无签名上游 signature_delta 必须为空串占位,实际=%v", delta["signature"])
			}
		case "text_delta":
			t.Fatalf("纯推理流不应出现 text_delta,events=%v", eventNames(events))
		}
	}
	if !hasThinkingDelta {
		t.Fatalf("缺少 thinking_delta 事件")
	}
	if !hasSignatureDelta {
		t.Fatalf("关 thinking 块前必须发一条空串 signature_delta")
	}
	if !contains(thinkingText, "GCD") || !contains(thinkingText, "1071") {
		t.Fatalf("thinking_delta 文本累积不完整,实际=%q", thinkingText)
	}
}

// TestReasoningThenTextCorrectIndexOrder 锁定 B:reasoning 在前、正文在后,
// thinking 块 index 0,text 块 index 1,顺序与官方一致。
func TestReasoningThenTextCorrectIndexOrder(t *testing.T) {
	upstream := writeUpstream(
		reasoningChunkLine("Let me think."),
		textChunkLine("The answer is 42."),
		finishChunkLine("stop"),
	)
	events := runAnthropicSSE(t, upstream)

	// 顺序断言:message_start → cbs(thinking,0) → thinking_delta → signature_delta → cbs_stop(0)
	//          → cbs(text,1) → text_delta → cbs_stop(1) → message_delta → message_stop
	want := []string{
		"message_start",
		"content_block_start",   // thinking, index 0
		"content_block_delta",  // thinking_delta
		"content_block_delta",  // signature_delta (空)
		"content_block_stop",   // index 0
		"content_block_start",  // text, index 1
		"content_block_delta",  // text_delta
		"content_block_stop",   // index 1
		"message_delta",
		"message_stop",
	}
	got := eventNames(events)
	if len(got) != len(want) {
		t.Fatalf("事件数不符 want=%v got=%v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("第 %d 个事件不符 want=%s got=%s (全序列 got=%v)", i, w, got[i], got)
		}
	}

	// 细查 index:thinking 块 index 0、text 块 index 1
	var startIdxs []int
	var stopIdxs []int
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			startIdxs = append(startIdxs, int(m["index"].(float64)))
		}
		if ev.event == "content_block_stop" {
			m := dataMap(t, ev)
			stopIdxs = append(stopIdxs, int(m["index"].(float64)))
		}
	}
	if len(startIdxs) != 2 || startIdxs[0] != 0 || startIdxs[1] != 1 {
		t.Fatalf("两块 start index 应为 [0,1],实际=%v", startIdxs)
	}
	if len(stopIdxs) != 2 || stopIdxs[0] != 0 || stopIdxs[1] != 1 {
		t.Fatalf("两块 stop index 应为 [0,1],实际=%v", stopIdxs)
	}

	// 第二个 content_block_start 应是 text 块
	var secondStart sseEvent
	var count int
	for _, ev := range events {
		if ev.event == "content_block_start" {
			count++
			if count == 2 {
				secondStart = ev
				break
			}
		}
	}
	m2 := dataMap(t, secondStart)
	cb2, _ := m2["content_block"].(map[string]interface{})
	if cb2 == nil || cb2["type"] != "text" {
		t.Fatalf("第二块应为 text,实际=%v", cb2)
	}
}

// TestNoReasoningKeepsNoThinkingBlock 锁定 C(无推理模型回归红线):全程只发 content、
// reasoning_content 恒空 → 下游零 thinking 块、零 signature_delta,仅 text_delta。
func TestNoReasoningKeepsNoThinkingBlock(t *testing.T) {
	upstream := writeUpstream(
		textChunkLine("Hello "),
		textChunkLine("world."),
		finishChunkLine("stop"),
	)
	events := runAnthropicSSE(t, upstream)

	for _, ev := range events {
		if ev.event != "content_block_start" && ev.event != "content_block_delta" {
			continue
		}
		m := dataMap(t, ev)
		if ev.event == "content_block_start" {
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Fatalf("无推理模型不应开 thinking 块,events=%v", eventNames(events))
			}
		}
		if ev.event == "content_block_delta" {
			delta, _ := m["delta"].(map[string]interface{})
			if delta != nil && (delta["type"] == "thinking_delta" || delta["type"] == "signature_delta") {
				t.Fatalf("无推理模型不应发 thinking_delta/signature_delta")
			}
		}
	}
	requireEvent(t, events, "content_block_start")
	requireEvent(t, events, "content_block_delta")
}

// TestEmptyReasoningHandshakeFrameSkipped 锁定 D:上游先发空串 reasoning_content 握手帧,
// 再发正文 → 空串被跳过,无空 thinking 块,仅 text 块。
func TestEmptyReasoningHandshakeFrameSkipped(t *testing.T) {
	empty := mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"role": "assistant", "reasoning_content": ""},
			},
		},
	})
	upstream := writeUpstream(
		empty,
		textChunkLine("answer"),
		finishChunkLine("stop"),
	)
	events := runAnthropicSSE(t, upstream)

	// 无任何 thinking 块事件
	for _, ev := range events {
		if ev.event != "content_block_start" {
			continue
		}
		m := dataMap(t, ev)
		cb, _ := m["content_block"].(map[string]interface{})
		if cb != nil && cb["type"] == "thinking" {
			t.Fatalf("空 reasoning 握手帧不应开 thinking 块")
		}
	}
	requireEvent(t, events, "message_stop")
}

// TestReasoningFollowedByToolCalls 锁定 E:reasoning + tool_calls 共存时,
// thinking 块先发、tool_use 块 index 正确后移、不抢 index。
func TestReasoningFollowedByToolCalls(t *testing.T) {
	upstream := writeUpstream(
		reasoningChunkLine("I should call a tool."),
		toolChunkLine(0, "call_1", "get_weather", `{"loc":"NYC"}`),
		finishChunkLine("tool_calls"),
	)
	events := runAnthropicSSE(t, upstream)

	// 存在 thinking 块
	var hasThinkingStart, hasThinkingDelta, hasToolStart, hasInputJSONDelta bool
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil {
				switch cb["type"] {
				case "thinking":
					hasThinkingStart = true
				case "tool_use":
					hasToolStart = true
				}
			}
		}
		if ev.event == "content_block_delta" {
			dm := dataMap(t, ev)
			delta, _ := dm["delta"].(map[string]interface{})
			if delta != nil {
				if delta["type"] == "thinking_delta" {
					hasThinkingDelta = true
				}
				if delta["type"] == "input_json_delta" {
					hasInputJSONDelta = true
				}
			}
		}
	}
	if !hasThinkingStart || !hasThinkingDelta {
		t.Fatalf("reasoning+tool 流缺少 thinking 块,events=%v", eventNames(events))
	}
	if !hasToolStart || !hasInputJSONDelta {
		t.Fatalf("reasoning+tool 流缺少 tool_use 块,events=%v", eventNames(events))
	}
	// thinking 块 index 0, tool_use 块 index 不为 0(thinking 已占 0 → tool 至少 index 1)
	var toolIdx int = -1
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				toolIdx = int(m["index"].(float64))
			}
		}
	}
	if toolIdx <= 0 {
		t.Fatalf("tool_use 块 index 应在 thinking(0) 之后,实际=%d", toolIdx)
	}
	// stop_reason 应为 tool_use
	var msgDelta sseEvent
	var found bool
	for _, ev := range events {
		if ev.event == "message_delta" {
			msgDelta = ev
			found = true
		}
	}
	if found {
		dm := dataMap(t, msgDelta)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta == nil || delta["stop_reason"] != "tool_use" {
			t.Fatalf("reasoning+tool 的 stop_reason 应为 tool_use,实际 delta=%v", delta)
		}
	}
}

// contains 是本地字符串包含测试的简写,避免引入额外包。
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ===== 思考等级透传测试(resolveReasoningEffort / mapReasoningEffort / 注入) =====
//
// 锁定 NVIDIA NIM 思考等级透传:把 Claude Code/Codex 的思考配置 resolve+map 后注入 chat_template_kwargs,
// 复用 cc-switch 的 resolve_reasoning_effort / map_reasoning_effort 行为契约。

// makeAnthReq 构造一个带指定 thinking / output_config 的 AnthropicRequest(用于 AnthropicToOpenAIChat 测试)。
func makeAnthReq(t *testing.T, model string, thinkingJSON, outputConfigJSON string) *AnthropicRequest {
	t.Helper()
	parts := make([]string, 0, 4)
	parts = append(parts, `"model":`+mustJSONString(model))
	parts = append(parts, `"messages":[{"role":"user","content":"hi"}]`)
	if thinkingJSON != "" {
		parts = append(parts, `"thinking":`+thinkingJSON)
	}
	if outputConfigJSON != "" {
		parts = append(parts, `"output_config":`+outputConfigJSON)
	}
	body := "{" + strings.Join(parts, ",") + "}"
	var req AnthropicRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal anthropic req failed: %v body=%s", err, body)
	}
	return &req
}

// ctKwargs 从 AnthropicToOpenAIChat 产出的 OpenAIChatRequest 提取 chat_template_kwargs。
func ctKwargs(t *testing.T, req *OpenAIChatRequest) map[string]interface{} {
	t.Helper()
	if req.ChatTemplateKwargs == nil {
		return nil
	}
	m := map[string]interface{}{"thinking": req.ChatTemplateKwargs["thinking"]}
	if v, ok := req.ChatTemplateKwargs["reasoning_effort"]; ok {
		m["reasoning_effort"] = v
	}
	return m
}

// TestResolveReasoningEffort_OutputConfigEffort 锁定 output_config.effort 优先且 1:1 映射(max→max 不转 xhigh)。
func TestResolveReasoningEffort_OutputConfigEffort(t *testing.T) {
	cases := map[string]string{
		`{"effort":"low"}`:    "low",
		`{"effort":"medium"}`: "medium",
		`{"effort":"high"}`:   "high",
		`{"effort":"max"}`:    "max",
		`{"effort":"turbo"}`:  "", // 未知值 → fallback 到 thinking
	}
	for oc, want := range cases {
		req := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", "", oc)
		// turbo 未知应回落 thinking,此处 req 无 thinking → 空,所以 want==""
		got := resolveReasoningEffort(req)
		if got != want {
			t.Errorf("output_config %s → resolve=%q want=%q", oc, got, want)
		}
	}
}

// TestResolveReasoningEffort_ThinkingFallback 锁定 thinking 兜底:
// adaptive→max,enabled+budget 分档,enabled 无 budget→high,disabled→空。
func TestResolveReasoningEffort_ThinkingFallback(t *testing.T) {
	cases := map[string]string{
		`{"type":"adaptive"}`:              "max",
		`{"type":"enabled","budget_tokens":1024}`:  "low",
		`{"type":"enabled","budget_tokens":8000}`: "medium",
		`{"type":"enabled","budget_tokens":32000}`:"high",
		`{"type":"enabled"}`:                "high",
		`{"type":"disabled"}`:               "",
	}
	for tk, want := range cases {
		req := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", tk, "")
		got := resolveReasoningEffort(req)
		if got != want {
			t.Errorf("thinking %s → resolve=%q want=%q", tk, got, want)
		}
	}
}

// TestResolveReasoningEffort_OutputConfigBeatsThinking 锁定 output_config 优先于 thinking。
func TestResolveReasoningEffort_OutputConfigBeatsThinking(t *testing.T) {
	// thinking=adaptive(max), 但 output_config=low → 应取 low
	req := makeAnthReq(t, "m", `{"type":"adaptive"}`, `{"effort":"low"}`)
	if got := resolveReasoningEffort(req); got != "low" {
		t.Fatalf("output_config 应优先于 thinking,得到=%q 想要=low", got)
	}
}

// TestMapReasoningEffort_DeepSeekMode 锁定 NIM deepseek mode:
// max/xhigh→max,其余(low/medium/high)→high —— 不产 low/medium,避免 NIM v4-flash 400。
func TestMapReasoningEffort_DeepSeekMode(t *testing.T) {
	cases := map[string]string{
		"low":  "high",
		"medium": "high",
		"high": "high",
		"max":  "max",
		"xhigh": "max",
		"none": "", // 关闭不注入
	}
	for in, want := range cases {
		got := mapReasoningEffort(in, "deepseek")
		if got != want {
			t.Errorf("mapReasoningEffort(%q,deepseek)=%q want=%q", in, got, want)
		}
	}
}

// TestAnthropicToOpenAIChat_InjectsChatTemplateKwargs 锁定注入:Anthropic→chat_template_kwargs 形态。
func TestAnthropicToOpenAIChat_InjectsChatTemplateKwargs(t *testing.T) {
	// max 等级 → chat_template_kwargs:{thinking:true, reasoning_effort:max}
	req := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", `{"type":"adaptive"}`, "")
	out, err := AnthropicToOpenAIChat(req, false)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat failed: %v", err)
	}
	kw := ctKwargs(t, out)
	if kw["thinking"] != true {
		t.Fatalf("thinking 应为 true,实际 kw=%v", kw)
	}
	if kw["reasoning_effort"] != "max" {
		t.Fatalf("adaptive→max,实际 reasoning_effort=%v want=max", kw["reasoning_effort"])
	}

	// low 等级(deepseek mode 落回 high)→ reasoning_effort:high
	req2 := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", `{"type":"enabled","budget_tokens":1024}`, "")
	out2, _ := AnthropicToOpenAIChat(req2, false)
	kw2 := ctKwargs(t, out2)
	if kw2["reasoning_effort"] != "high" {
		t.Fatalf("low 经 deepseek mode 映射应→high,实际=%v", kw2["reasoning_effort"])
	}

	// 客户端明示 disabled → 不注入 chat_template_kwargs(回归安全)
	req3 := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", `{"type":"disabled"}`, "")
	out3, _ := AnthropicToOpenAIChat(req3, false)
	if out3.ChatTemplateKwargs != nil {
		t.Fatalf("disabled 不应注入 chat_template_kwargs,实际=%v", out3.ChatTemplateKwargs)
	}
}

// TestAnthropicToOpenAIChat_ReasoningModelDefaultThinking 锁定:推理型模型客户端未发思考时仍发 thinking:true。
func TestAnthropicToOpenAIChat_ReasoningModelDefaultThinking(t *testing.T) {
	req := makeAnthReq(t, "deepseek-ai/deepseek-v4-flash", "", "")
	out, _ := AnthropicToOpenAIChat(req, false)
	if out.ChatTemplateKwargs == nil || out.ChatTemplateKwargs["thinking"] != true {
		t.Fatalf("推理模型未发思考时应注入 thinking:true,实际=%v", out.ChatTemplateKwargs)
	}
	if _, ok := out.ChatTemplateKwargs["reasoning_effort"]; ok {
		t.Fatalf("未发思考等级时不应注入 reasoning_effort,实际=%v", out.ChatTemplateKwargs)
	}
}

// TestInjectNvidiaChatTemplateKwargs_OpenAIEffort 锁定 OpenAI 入站透传:
// 从原始 body 的顶层 reasoning_effort 提取并映射进 chat_template_kwargs。
func TestInjectNvidiaChatTemplateKwargs_OpenAIEffort(t *testing.T) {
	chatReq := &OpenAIChatRequest{Model: "deepseek-ai/deepseek-v4-flash"}
	body := mustJSONString(map[string]interface{}{
		"model": "x", "reasoning_effort": "high", "messages": []interface{}{},
	})
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "deepseek-ai/deepseek-v4-flash", false)
	if chatReq.ChatTemplateKwargs == nil {
		t.Fatalf("应注入 chat_template_kwargs")
	}
	if chatReq.ChatTemplateKwargs["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort:high 应映射→high,实际=%v", chatReq.ChatTemplateKwargs)
	}
	if chatReq.ChatTemplateKwargs["thinking"] != true {
		t.Fatalf("thinking 应为 true,实际=%v", chatReq.ChatTemplateKwargs)
	}
}

// TestInjectNvidiaChatTemplateKwargs_ReasoningObjectForm 锁定 OpenRouter reasoning.effort 形态也能提取。
func TestInjectNvidiaChatTemplateKwargs_ReasoningObjectForm(t *testing.T) {
	chatReq := &OpenAIChatRequest{Model: "deepseek-ai/deepseek-v4-flash"}
	body := mustJSONString(map[string]interface{}{
		"model": "x", "reasoning": map[string]interface{}{"effort": "max"}, "messages": []interface{}{},
	})
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "deepseek-ai/deepseek-v4-flash", false)
	if chatReq.ChatTemplateKwargs["reasoning_effort"] != "max" {
		t.Fatalf("reasoning.effort:max 应映射→max,实际=%v", chatReq.ChatTemplateKwargs)
	}
}

// TestReasoningFieldFallbackStream 锁定回译兜底:上游发 reasoning 字段(非 reasoning_content)也能成 thinking_delta。
func TestReasoningFieldFallbackStream(t *testing.T) {
	reasoningChunk := mustJSONString(map[string]interface{}{
		"id": "chatcmpl-x", "object": "chat.completion.chunk", "created": 1700000000, "model": "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"reasoning": "inner reasoning via reasoning field"},
			},
		},
	})
	upstream := writeUpstream(reasoningChunk, finishChunkLine("stop"))
	events := runAnthropicSSE(t, upstream)

	var sawThinkingText string
	var hasThinkingDelta bool
	for _, ev := range events {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta == nil {
			continue
		}
		if delta["type"] == "thinking_delta" {
			hasThinkingDelta = true
			if s, ok := delta["thinking"].(string); ok {
				sawThinkingText += s
			}
		}
	}
	if !hasThinkingDelta || !contains(sawThinkingText, "reasoning field") {
		t.Fatalf("上游 reasoning 字段应兜底成 thinking_delta,实际 sawThinkingText=%q hasDelta=%v", sawThinkingText, hasThinkingDelta)
	}
}

// TestReasoningFollowedByMultipleTextChunksSingleBlock 锁定:思考块之后跟多帧文本增量时,
// 全流仅下发 1 个 content_block_start(thinking) 和 1 个 content_block_start(text),
// 绝不为后续每一帧文本 Chunk 错误创建新 content_block_start(避免前端渲染成多行/列表项)。
func TestReasoningFollowedByMultipleTextChunksSingleBlock(t *testing.T) {
	upstream := writeUpstream(
		reasoningChunkLine("Let me think first."),
		textChunkLine("Chunk 1: Calling error."),
		textChunkLine("Chunk 2: , "),
		textChunkLine("Chunk 3: Let me retry."),
		finishChunkLine("stop"),
	)
	events := runAnthropicSSE(t, upstream)

	var textStartCount int
	var textBlockIndices []int
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "text" {
				textStartCount++
				textBlockIndices = append(textBlockIndices, int(m["index"].(float64)))
			}
		}
	}

	if textStartCount != 1 {
		t.Fatalf("思考之后的 3 帧文本 Chunk 期望只触发 1 次 content_block_start(text),实际触发了 %d 次", textStartCount)
	}
	if len(textBlockIndices) > 0 && textBlockIndices[0] != 1 {
		t.Fatalf("思考块(index 0)之后的文本块应使用 index 1,实际=%d", textBlockIndices[0])
	}
}
