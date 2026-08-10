package relay

import (
	"strings"
	"testing"
)

// grok_thinking_test.go 锁定 Grok 号池思考注入三态语义(off/on/unspecified)
// 及其上游 reasoning_effort 落地真值表。
//
// 核心契约(与 nvidia/other 池的根本差异):
//   - 关闭信号必须翻译为上游字段值 "none" 显式发出(xAI 官方默认 low 即常驻思考,省略≠关闭);
//   - 开启但定档失败(未识别档)保守注入 "none"(避免 default low 悄悄烧 token);
//   - 未指定(opt-in 默认)注入空串省略,让上游用官方默认 low;
//   - 始终清空 ChatTemplateKwargs(Grok 绝不注入 NIM 专属 kwargs)。
//
// 锁定函数:grokResolveAnthropicThinking / grokResolveOpenAIThinking /
// grokExtractRawEffort / grokMapEffort / grokApplyThinkingToChat。
// 注:resolve 函数把 globalOn 作为显式入参(非读全局),故测试直接传 bool,无需拨全局总闸。

// grokChatBody 构造一个含 reasoning_effort(顶层 Codex 形态)的 OpenAI Chat 入站 body。
// effort 为空时不写该字段(模拟客户端未发 reasoning_effort)。
func grokChatBody(t *testing.T, effort string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"model":"grok-4","messages":[{"role":"user","content":"hi"}]`)
	if effort != "" {
		b.WriteString(`,"reasoning_effort":` + mustJSONString(effort))
	}
	b.WriteString(`}`)
	return []byte(b.String())
}

// grokNestedEffortBody 构造 OpenRouter 形态 reasoning.effort 入站 body。
func grokNestedEffortBody(t *testing.T, effort string) []byte {
	t.Helper()
	body := `{"model":"grok-4","messages":[{"role":"user","content":"hi"}],"reasoning":{"effort":` + mustJSONString(effort) + `}}`
	return []byte(body)
}

// applyGrok 是测试便利:构造一个预填 ChatTemplateKwargs(非 nil)的 OpenAIChatRequest,
// 应用三态后返回最终 ReasoningEffort 与 ChatTemplateKwargs 是否被清空(nil)。
func applyGrok(mode grokThinkingMode, effort string) (reasoningEffort string, kwargsCleared bool) {
	req := &OpenAIChatRequest{
		Model:              "grok-4",
		ChatTemplateKwargs: map[string]interface{}{"thinking": true, "reasoning_effort": "high"},
	}
	grokApplyThinkingToChat(req, mode, effort)
	return req.ReasoningEffort, req.ChatTemplateKwargs == nil
}

// ===== grokResolveAnthropicThinking =====

// TestGrokResolveAnthropicThinking_GlobalOffForcesOff 锁定全局总闸关:
// 无论客户端 thinking 字段如何表达,强制 off → 上游发 "none"(Grok 的「全局关闭」语义需真正关掉上游推理)。
func TestGrokResolveAnthropicThinking_GlobalOffForcesOff(t *testing.T) {
	cases := []string{
		`{"type":"disabled"}`,
		`{"type":"enabled","budget_tokens":32000}`,
		`{"type":"adaptive"}`,
		``, // 无 thinking 字段
	}
	for _, tk := range cases {
		req := makeAnthReq(t, "grok-4", tk, "")
		mode, effort := grokResolveAnthropicThinking(req, false)
		if mode != grokThinkOff {
			t.Errorf("globalOff + thinking=%q: mode=%v want grokThinkOff", tk, mode)
		}
		if effort != "" {
			t.Errorf("globalOff effort should be empty, got %q", effort)
		}
	}
}

func TestGrokResolveAnthropicThinking_DisabledIsOff(t *testing.T) {
	req := makeAnthReq(t, "grok-4", `{"type":"disabled"}`, "")
	mode, effort := grokResolveAnthropicThinking(req, true)
	if mode != grokThinkOff || effort != "" {
		t.Fatalf("thinking.type=disabled → (off, \"\"), got (%v, %q)", mode, effort)
	}
}

func TestGrokResolveAnthropicThinking_EnabledAdaptiveIsOn(t *testing.T) {
	// enabled + budget 32000 → resolveReasoningEffort=high
	req := makeAnthReq(t, "grok-4", `{"type":"enabled","budget_tokens":32000}`, "")
	mode, effort := grokResolveAnthropicThinking(req, true)
	if mode != grokThinkOn || effort != "high" {
		t.Fatalf("enabled+budget32000 → (on, high), got (%v, %q)", mode, effort)
	}
	// adaptive → resolveReasoningEffort=max
	req2 := makeAnthReq(t, "grok-4", `{"type":"adaptive"}`, "")
	mode2, effort2 := grokResolveAnthropicThinking(req2, true)
	if mode2 != grokThinkOn || effort2 != "max" {
		t.Fatalf("adaptive → (on, max), got (%v, %q)", mode2, effort2)
	}
	// enabled + budget 1024 → low
	req3 := makeAnthReq(t, "grok-4", `{"type":"enabled","budget_tokens":1024}`, "")
	mode3, effort3 := grokResolveAnthropicThinking(req3, true)
	if mode3 != grokThinkOn || effort3 != "low" {
		t.Fatalf("enabled+budget1024 → (on, low), got (%v, %q)", mode3, effort3)
	}
	// enabled + budget 8000 → medium
	req4 := makeAnthReq(t, "grok-4", `{"type":"enabled","budget_tokens":8000}`, "")
	mode4, effort4 := grokResolveAnthropicThinking(req4, true)
	if mode4 != grokThinkOn || effort4 != "medium" {
		t.Fatalf("enabled+budget8000 → (on, medium), got (%v, %q)", mode4, effort4)
	}
}

// TestGrokResolveAnthropicThinking_NoThinkingFieldIsUnspecified 锁定 opt-in 默认:
// 无 thinking 字段 / req nil / Thinking nil → unspecified(不强开不强关,让上游用官方默认 low)。
func TestGrokResolveAnthropicThinking_NoThinkingFieldIsUnspecified(t *testing.T) {
	req := makeAnthReq(t, "grok-4", "", "")
	mode, effort := grokResolveAnthropicThinking(req, true)
	if mode != grokThinkUnspecified || effort != "" {
		t.Fatalf("no thinking → (unspecified, \"\"), got (%v, %q)", mode, effort)
	}
	mode2, effort2 := grokResolveAnthropicThinking(nil, true)
	if mode2 != grokThinkUnspecified || effort2 != "" {
		t.Fatalf("nil req → (unspecified, \"\"), got (%v, %q)", mode2, effort2)
	}
	req3 := &AnthropicRequest{Model: "grok-4"}
	mode3, effort3 := grokResolveAnthropicThinking(req3, true)
	if mode3 != grokThinkUnspecified || effort3 != "" {
		t.Fatalf("nil Thinking → (unspecified, \"\"), got (%v, %q)", mode3, effort3)
	}
}

// TestGrokResolveAnthropicThinking_OutputConfigEffortRespected 锁定 output_config.effort
// 优先于 thinking.type(resolveReasoningEffort 既有语义):thinking=adaptive(max) 但 output_config=low → effort=low。
func TestGrokResolveAnthropicThinking_OutputConfigEffortRespected(t *testing.T) {
	req := makeAnthReq(t, "grok-4", `{"type":"adaptive"}`, `{"effort":"low"}`)
	mode, effort := grokResolveAnthropicThinking(req, true)
	if mode != grokThinkOn || effort != "low" {
		t.Fatalf("output_config=low 应优先, got (%v, %q)", mode, effort)
	}
}

// ===== grokResolveOpenAIThinking / grokExtractRawEffort =====

func TestGrokResolveOpenAIThinking_GlobalOffForcesOff(t *testing.T) {
	for _, body := range [][]byte{
		grokChatBody(t, "high"),
		grokChatBody(t, "none"),
		grokChatBody(t, ""),
		grokNestedEffortBody(t, "max"),
	} {
		mode, effort := grokResolveOpenAIThinking(body, false)
		if mode != grokThinkOff || effort != "" {
			t.Errorf("globalOff → (off, \"\"), got (%v, %q) for body=%s", mode, effort, body)
		}
	}
}

// TestGrokResolveOpenAIThinking_NoneOffDisabledIsOff 锁定显式关闭词
// (none/off/disabled,含大小写与空白)→ off。
func TestGrokResolveOpenAIThinking_NoneOffDisabledIsOff(t *testing.T) {
	for _, e := range []string{"none", "off", "disabled", "NONE", " Off ", "Disabled"} {
		body := grokChatBody(t, e)
		mode, effort := grokResolveOpenAIThinking(body, true)
		if mode != grokThinkOff || effort != "" {
			t.Errorf("reasoning_effort=%q → (off, \"\"), got (%v, %q)", e, mode, effort)
		}
	}
}

func TestGrokResolveOpenAIThinking_ValidEffortIsOn(t *testing.T) {
	cases := map[string]string{
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"max":    "max",
	}
	for in, want := range cases {
		body := grokChatBody(t, in)
		mode, effort := grokResolveOpenAIThinking(body, true)
		if mode != grokThinkOn || effort != want {
			t.Errorf("reasoning_effort=%q → (on, %q), got (%v, %q)", in, want, mode, effort)
		}
	}
}

// TestGrokResolveOpenAIThinking_UnknownEffortIsOnButEmpty 锁定「开思考但定档失败」上游来源:
// 未知档(fast):grokExtractRawEffort 返回非空 "fast"(非关闭词)→ 判 on,
// 但 extractOpenAIReasoningEffort 经 normalizeEffort("fast") 归一为空 → effort 空。
// 这正是 grokApplyThinkingToChat「开思考未定档 → 保守注入 none」的触发输入。
func TestGrokResolveOpenAIThinking_UnknownEffortIsOnButEmpty(t *testing.T) {
	body := grokChatBody(t, "fast")
	mode, effort := grokResolveOpenAIThinking(body, true)
	if mode != grokThinkOn {
		t.Fatalf("未知档 fast 应判 on(定档失败由 apply 兜底 none), got mode=%v", mode)
	}
	if effort != "" {
		t.Fatalf("未知档 effort 应为空(normalizeEffort 归一失败), got %q", effort)
	}
}

func TestGrokResolveOpenAIThinking_NoEffortFieldIsUnspecified(t *testing.T) {
	body := grokChatBody(t, "")
	mode, effort := grokResolveOpenAIThinking(body, true)
	if mode != grokThinkUnspecified || effort != "" {
		t.Fatalf("无 reasoning_effort → (unspecified, \"\"), got (%v, %q)", mode, effort)
	}
	mode2, effort2 := grokResolveOpenAIThinking(nil, true)
	if mode2 != grokThinkUnspecified || effort2 != "" {
		t.Fatalf("nil body → (unspecified, \"\"), got (%v, %q)", mode2, effort2)
	}
}

// TestGrokResolveOpenAIThinking_NestedReasoningEffortForm 锁定 OpenRouter
// 嵌套形态 reasoning.effort=high → (on, high)。
func TestGrokResolveOpenAIThinking_NestedReasoningEffortForm(t *testing.T) {
	body := grokNestedEffortBody(t, "high")
	mode, effort := grokResolveOpenAIThinking(body, true)
	if mode != grokThinkOn || effort != "high" {
		t.Fatalf("reasoning.effort=high → (on, high), got (%v, %q)", mode, effort)
	}
}

// TestGrokExtractRawEffort 锁定原始(未归一)effort 提取:顶层优先于嵌套,
// 返回 lowercase 原值,非 JSON / 无字段 → 空串。
func TestGrokExtractRawEffort(t *testing.T) {
	cases := map[string]string{
		`{"reasoning_effort":"high"}`:                                "high",
		`{"reasoning_effort":"NONE"}`:                                 "none",
		`{"reasoning_effort":" Off "}`:                                "off",
		`{"reasoning":{"effort":"max"}}`:                               "max",
		`{"reasoning_effort":"high","reasoning":{"effort":"low"}}`:    "high", // 顶层优先
		`{"model":"grok-4"}`:                                          "", // 无字段
		``:                                                            "",
		`not-json`:                                                    "", // 非 JSON → ""
	}
	for body, want := range cases {
		got := grokExtractRawEffort([]byte(body))
		if got != want {
			t.Errorf("grokExtractRawEffort(%q) = %q, want %q", body, got, want)
		}
	}
}

// TestGrokMapEffort 锁定 xAI Grok 上游认的 reasoning_effort 取值映射:
// low/medium/high 1:1;max/xhigh→high(官方无 max);关闭词/空/未知→空(不注入)。
func TestGrokMapEffort(t *testing.T) {
	cases := map[string]string{
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"max":    "high",  // xAI 官方无 max → high
		"xhigh":  "high",
		"none":   "",     // 关闭词不在识别集 → 空(由 apply 的 off 分支直接写 "none")
		"off":    "",
		"":       "",     // 空 → 不注入
		"fast":   "",     // 未知 → 空
		" Low ":  "low",  // TrimSpace + ToLower
	}
	for in, want := range cases {
		got := grokMapEffort(in)
		if got != want {
			t.Errorf("grokMapEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

// ===== grokApplyThinkingToChat 真值表 =====

// TestGrokApplyThinkingToChat_OffInjectsNone 锁定 off → reasoning_effort="none"(显式关闭,
// 绝不可省略,否则 Grok 回落 default low 仍思考) + 清空 ChatTemplateKwargs。
func TestGrokApplyThinkingToChat_OffInjectsNone(t *testing.T) {
	re, cleared := applyGrok(grokThinkOff, "")
	if re != "none" {
		t.Fatalf("off → reasoning_effort=none, got %q", re)
	}
	if !cleared {
		t.Fatalf("off 必须清空 ChatTemplateKwargs")
	}
}

func TestGrokApplyThinkingToChat_OnMapsEffort(t *testing.T) {
	// on + max → grokMapEffort(max)=high
	if re, _ := applyGrok(grokThinkOn, "max"); re != "high" {
		t.Fatalf("on+max → high, got %q", re)
	}
	if re, _ := applyGrok(grokThinkOn, "low"); re != "low" {
		t.Fatalf("on+low → low, got %q", re)
	}
	if re, _ := applyGrok(grokThinkOn, "medium"); re != "medium" {
		t.Fatalf("on+medium → medium, got %q", re)
	}
	if re, _ := applyGrok(grokThinkOn, "high"); re != "high" {
		t.Fatalf("on+high → high, got %q", re)
	}
	if re, cleared := applyGrok(grokThinkOn, "max"); re != "high" || !cleared {
		t.Fatalf("on+max → high 且清空 kwargs, got re=%q cleared=%v", re, cleared)
	}
}

// TestGrokApplyThinkingToChat_OnUnknownEffortFallsBackToNone 锁定「开思考但定档失败」边界:
// on + 空/未知 effort → 保守注入 none,避免不明档位被上游当成 default low 悄悄烧 token。
func TestGrokApplyThinkingToChat_OnUnknownEffortFallsBackToNone(t *testing.T) {
	if re, _ := applyGrok(grokThinkOn, ""); re != "none" {
		t.Fatalf("on+空 effort → none(定档失败保守关闭), got %q", re)
	}
	if re, _ := applyGrok(grokThinkOn, "fast"); re != "none" {
		t.Fatalf("on+unknown fast → none, got %q", re)
	}
}

// TestGrokApplyThinkingToChat_UnspecifiedInjectsEmpty 锁定 unspecified → reasoning_effort=""
// (省略,让上游用官方默认 low,opt-in 语义) + 清空 ChatTemplateKwargs。
func TestGrokApplyThinkingToChat_UnspecifiedInjectsEmpty(t *testing.T) {
	re, cleared := applyGrok(grokThinkUnspecified, "")
	if re != "" {
		t.Fatalf("unspecified → reasoning_effort=\"\"(省略,让上游用官方默认 low), got %q", re)
	}
	if !cleared {
		t.Fatalf("unspecified 必须清空 ChatTemplateKwargs")
	}
}

// TestGrokApplyThinkingToChat_NilChatReqNoPanic 锁定 nil chatReq 防御性早退,不 panic。
func TestGrokApplyThinkingToChat_NilChatReqNoPanic(t *testing.T) {
	grokApplyThinkingToChat(nil, grokThinkOff, "")
}

// TestGrokApplyThinkingToChat_AlreadyNilKwargsStaysNil 锁定 ChatTemplateKwargs
// 本就 nil 时不得被误设为非 nil(清空操作是 *设 nil*,不是设空 map)。
func TestGrokApplyThinkingToChat_AlreadyNilKwargsStaysNil(t *testing.T) {
	req := &OpenAIChatRequest{Model: "grok-4"}
	grokApplyThinkingToChat(req, grokThinkUnspecified, "")
	if req.ChatTemplateKwargs != nil {
		t.Fatalf("本来 nil 的 ChatTemplateKwargs 应保持 nil, got %v", req.ChatTemplateKwargs)
	}
	if req.ReasoningEffort != "" {
		t.Fatalf("unspecified → ReasoningEffort=\"\", got %q", req.ReasoningEffort)
	}
}

// ===== 端到端:Anthropic resolve → apply 真值表(覆盖 handleGrok 的真实组合)=====

func TestGrokAnthropicResolveThenApply_EndToEnd(t *testing.T) {
	cases := []struct {
		name         string
		thinkingJSON string
		ocJSON       string
		wantMode     grokThinkingMode
		wantEffort   string // resolve 返回的 effort
		wantApplied  string // apply 后的 ReasoningEffort
	}{
		{"disabled → off → none", `{"type":"disabled"}`, "", grokThinkOff, "", "none"},
		{"enabled+budget32000 → on high → high", `{"type":"enabled","budget_tokens":32000}`, "", grokThinkOn, "high", "high"},
		{"adaptive → on max → high", `{"type":"adaptive"}`, "", grokThinkOn, "max", "high"},
		{"enabled+budget1024 → on low → low", `{"type":"enabled","budget_tokens":1024}`, "", grokThinkOn, "low", "low"},
		{"no thinking → unspecified → empty", "", "", grokThinkUnspecified, "", ""},
		{"output_config=low → on low → low", `{"type":"adaptive"}`, `{"effort":"low"}`, grokThinkOn, "low", "low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := makeAnthReq(t, "grok-4", c.thinkingJSON, c.ocJSON)
			mode, effort := grokResolveAnthropicThinking(req, true)
			if mode != c.wantMode {
				t.Errorf("mode = %v, want %v", mode, c.wantMode)
			}
			if effort != c.wantEffort {
				t.Errorf("effort = %q, want %q", effort, c.wantEffort)
			}
			chatReq := &OpenAIChatRequest{Model: "grok-4", ChatTemplateKwargs: map[string]interface{}{"thinking": true}}
			grokApplyThinkingToChat(chatReq, mode, effort)
			if chatReq.ReasoningEffort != c.wantApplied {
				t.Errorf("applied ReasoningEffort = %q, want %q (kwargs cleared=%v)", chatReq.ReasoningEffort, c.wantApplied, chatReq.ChatTemplateKwargs == nil)
			}
			if chatReq.ChatTemplateKwargs != nil {
				t.Errorf("ChatTemplateKwargs must be cleared, got %v", chatReq.ChatTemplateKwargs)
			}
		})
	}
}

func TestGrokOpenAIResolveThenApply_EndToEnd(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantMode    grokThinkingMode
		wantEffort  string
		wantApplied string
	}{
		{"none → off → none", `{"model":"grok-4","reasoning_effort":"none","messages":[]}`, grokThinkOff, "", "none"},
		{"high → on high → high", `{"model":"grok-4","reasoning_effort":"high","messages":[]}`, grokThinkOn, "high", "high"},
		{"max → on max → high", `{"model":"grok-4","reasoning_effort":"max","messages":[]}`, grokThinkOn, "max", "high"},
		{"nested high → on high → high", `{"model":"grok-4","reasoning":{"effort":"high"},"messages":[]}`, grokThinkOn, "high", "high"},
		{"unknown fast → on empty → none", `{"model":"grok-4","reasoning_effort":"fast","messages":[]}`, grokThinkOn, "", "none"},
		{"no field → unspecified → empty", `{"model":"grok-4","messages":[]}`, grokThinkUnspecified, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mode, effort := grokResolveOpenAIThinking([]byte(c.body), true)
			if mode != c.wantMode {
				t.Errorf("mode = %v, want %v", mode, c.wantMode)
			}
			if effort != c.wantEffort {
				t.Errorf("effort = %q, want %q", effort, c.wantEffort)
			}
			chatReq := &OpenAIChatRequest{Model: "grok-4", ChatTemplateKwargs: map[string]interface{}{"thinking": true}}
			grokApplyThinkingToChat(chatReq, mode, effort)
			if chatReq.ReasoningEffort != c.wantApplied {
				t.Errorf("applied = %q, want %q", chatReq.ReasoningEffort, c.wantApplied)
			}
			if chatReq.ChatTemplateKwargs != nil {
				t.Errorf("ChatTemplateKwargs must be cleared")
			}
		})
	}
}
