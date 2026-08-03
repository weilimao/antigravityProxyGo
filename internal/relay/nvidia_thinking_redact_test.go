package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-proxy/internal/account"
)

// nvidia_thinking_redact_test.go: NVIDIA 中继思考注入"opt-in 语义"锁定。
//
// 背景:Claude Code 2.1.220 实测 —— Anthropic-Beta 头里的 redact-thinking-2026-02-12 在
// 开/关思考两态均常驻,与思考 on/off 无关(官方未文档化该头;关思考的正路是 body 省略 thinking 字段)。
// 改动前(commit 88cf8c8):代理把 redact-thinking 头当成"关思考"信号,命中即绝对短路注入,
// 由于该头两态都在 → 闸恒真 → 即使 body 带 thinking.type=adaptive(开思考)也被误关 → CLI 不显思考。
// 改动后:思考注入只由 body 显式信号(Anthropic 的 thinking.type、OpenAI 的 reasoning_effort)决定。
//
// 本文件锁定的核心真值:
//   - Anthropic 入站无 thinking 字段 / disabled → 不注入(effort 不越权驱动 on/off)。
//   - Anthropic 入站 thinking.type=adaptive/enabled → 注入 {thinking:true, reasoning_effort:*},
//     且 Anthropic-Beta 头带 redact-thinking-2026-02-12 时也正常注入(redact 头不再参与)。
//   - OpenAI 入站无 reasoning_effort → 不注入(opt-in),推理模型 glm-5.2 不 fallback 强开。
//   - OpenAI 入站有 reasoning_effort → 按档注入。
// 端到端用例显式 SetGlobalEnableThinkingMode(true) + defer 复位,避免依赖隐性全局状态。

// 确保 thinkingRequested 的 opt-in 判定与 redact 头隔离:无论 header 是否带 redact-thinking,
// thinkingRequested 只看 body thinking.type;AnthropicToOpenAIChat 不再接受 thinkingRedacted 入参。

// ===== 第一层:thinkingRequested opt-in 单元 =====

func TestThinkingRequested_Adaptive(t *testing.T) {
	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"adaptive"}`, "")
	if !thinkingRequested(req) {
		t.Fatalf("thinking.type=adaptive 应判 ON")
	}
}

func TestThinkingRequested_Enabled(t *testing.T) {
	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"enabled","budget_tokens":1024}`, "")
	if !thinkingRequested(req) {
		t.Fatalf("thinking.type=enabled 应判 ON")
	}
}

func TestThinkingRequested_Disabled(t *testing.T) {
	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"disabled"}`, "")
	if thinkingRequested(req) {
		t.Fatalf("thinking.type=disabled 应判 OFF")
	}
}

// TestThinkingRequested_NoField 锁定核心场景:Claude Code 关思考时省略 thinking 字段 → OFF。
// 注意:body 仍可能带 output_config.effort(max),但 effort 不参与 on/off 判定。
func TestThinkingRequested_NoField(t *testing.T) {
	req := makeAnthReq(t, "z-ai/glm-5.2", "", `{"effort":"max"}`)
	if thinkingRequested(req) {
		t.Fatalf("无 thinking 字段应判 OFF,即使 output_config.effort:max 仍在(effort 只定档不定开关)")
	}
}

func TestThinkingRequested_Nil(t *testing.T) {
	if thinkingRequested(nil) {
		t.Fatalf("nil request 应判 OFF(回归安全)")
	}
	// 非 nil 但无 thinking 字段
	req := makeAnthReq(t, "z-ai/glm-5.2", "", "")
	if thinkingRequested(req) {
		t.Fatalf("无 thinking 字段应判 OFF")
	}
}

// ===== 第二层:AnthropicToOpenAIChat 注入决策 =====

// TestAnthropicToOpenAIChat_NoThinkingField_NoInjection 锁定用户报障核心场景:
// glm-5.2 + 无 thinking 字段 + output_config.effort:max → 不注入 ChatTemplateKwargs。
// 即使 Anthropic-Beta 头带 redact-thinking-2026-02-12(经端到端验证),也仅由 body 决定。
// effort:max 不再越权驱动开思考(opt-in 修复)。
func TestAnthropicToOpenAIChat_NoThinkingField_NoInjection(t *testing.T) {
	// 该用例只验 body→注入映射,与全局开关协同:全局 ON 时无 thinking 字段仍不注入。
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	req := makeAnthReq(t, "z-ai/glm-5.2", "", `{"effort":"max"}`)
	out, _ := AnthropicToOpenAIChat(req)
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("无 thinking 字段(即使 effort:max)应不注入,实际=%v", out.ChatTemplateKwargs)
	}
}

func TestAnthropicToOpenAIChat_Disabled_NoInjection(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"disabled"}`, `{"effort":"max"}`)
	out, _ := AnthropicToOpenAIChat(req)
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("thinking.type=disabled 应不注入,实际=%v", out.ChatTemplateKwargs)
	}
}

// TestAnthropicToOpenAIChat_AdaptiveMax_Injects 锁定开思考态注入:
// glm-5.2 + thinking.type=adaptive + output_config.effort:max → 注入 {thinking:true, reasoning_effort:max}。
// 此用例与旧 redact 短路用例形成对照:adaptive+effort:max 必须正常注入(回归前被 redact 头误杀)。
func TestAnthropicToOpenAIChat_AdaptiveMax_Injects(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"adaptive"}`, `{"effort":"max"}`)
	out, _ := AnthropicToOpenAIChat(req)
	kw := ctKwargs(t, out)
	if kw["thinking"] != true || kw["reasoning_effort"] != "max" {
		t.Fatalf("adaptive+effort:max 应注入 thinking:true + reasoning_effort:max,实际=%v", kw)
	}
}

// TestAnthropicToOpenAIChat_GlobalGateOff_Suppresses 锁定全局总闸:
// IsEnableThinkingMode()==false 时即使 body 显式 thinking.type=adaptive 也不注入。
func TestAnthropicToOpenAIChat_GlobalGateOff_Suppresses(t *testing.T) {
	SetGlobalEnableThinkingMode(false)
	defer SetGlobalEnableThinkingMode(true)

	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"adaptive"}`, `{"effort":"max"}`)
	out, _ := AnthropicToOpenAIChat(req)
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("全局思考开关关闭时应不注入,实际=%v", out.ChatTemplateKwargs)
	}
}

// ===== 第三层:injectNvidiaChatTemplateKwargs(OpenAI Chat 入站)注入决策 =====

func TestInjectNvidiaChatTemplateKwargs_NoEffort_NoFallback(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	body := mustJSONString(map[string]interface{}{
		"model": "z-ai/glm-5.2", "messages": []interface{}{},
	})
	chatReq := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "z-ai/glm-5.2")
	if chatReq.ChatTemplateKwargs != nil {
		t.Fatalf("OpenAI 入站无 reasoning_effort 不应 fallback 注入(opt-in),实际=%v", chatReq.ChatTemplateKwargs)
	}
}

func TestInjectNvidiaChatTemplateKwargs_EffortHigh_Injects(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	body := mustJSONString(map[string]interface{}{
		"model": "z-ai/glm-5.2", "reasoning_effort": "high", "messages": []interface{}{},
	})
	chatReq := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "z-ai/glm-5.2")
	if chatReq.ChatTemplateKwargs == nil || chatReq.ChatTemplateKwargs["thinking"] != true || chatReq.ChatTemplateKwargs["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort:high 应注入 {thinking:true, reasoning_effort:high},实际=%v", chatReq.ChatTemplateKwargs)
	}
}

func TestInjectNvidiaChatTemplateKwargs_EffortDisabled_NoInjection(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// reasoning_effort:none → normalizeEffort 归一为空 → 不注入(关思考语义)
	body := mustJSONString(map[string]interface{}{
		"model": "z-ai/glm-5.2", "reasoning_effort": "none", "messages": []interface{}{},
	})
	chatReq := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "z-ai/glm-5.2")
	if chatReq.ChatTemplateKwargs != nil {
		t.Fatalf("reasoning_effort:none 应不注入(关思考语义),实际=%v", chatReq.ChatTemplateKwargs)
	}
}

func TestInjectNvidiaChatTemplateKwargs_GlobalGateOff_Suppresses(t *testing.T) {
	SetGlobalEnableThinkingMode(false)
	defer SetGlobalEnableThinkingMode(true)

	body := mustJSONString(map[string]interface{}{
		"model": "z-ai/glm-5.2", "reasoning_effort": "high", "messages": []interface{}{},
	})
	chatReq := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "z-ai/glm-5.2")
	if chatReq.ChatTemplateKwargs != nil {
		t.Fatalf("全局思考开关关闭时应不注入,实际=%v", chatReq.ChatTemplateKwargs)
	}
}

// ===== 第四层:handleNvidia 端到端业务流(真实选号→翻译→上游收到的 OpenAIChatRequest) =====

// captureUpstreamChatTemplateKwargs 构造一个 mock NVIDIA 上游,捕获收到的 OpenAIChatRequest
// 并回写非流式 200 响应。返回上游捕获到的 ChatTemplateKwargs 与服务器实例。
func captureUpstreamChatTemplateKwargs(t *testing.T, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req OpenAIChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("upstream unmarshal failed: %v body=%s", err, string(body))
		} else {
			*captured = req.ChatTemplateKwargs
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-1", Model: "z-ai/glm-5.2",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return upstream
}

//	withRedactHeader:是否往请求注入 Anthropic-Beta: ...,redact-thinking-2026-02-12,... 头。
//	两态用例都带这个头,锁定"redact 头不再参与思考注入决策"这一修复核心。
const ccRedactBetaHeader = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24"

// TestHandleNvidia_NoThinkingFieldAndRedactHeader_NoInjection 端到端锁定用户报障场景:
// glm-5.2 + 无 thinking 字段 + output_config.effort:max + 带 redact-thinking 头
// → 上游收到 ChatTemplateKwargs==nil。body 无显式思考信号即关思考,redact 头免役。
func TestHandleNvidia_NoThinkingFieldAndRedactHeader_NoInjection(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-nofield", "nv-nofield", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 真实 Claude Code 关思考 body 形态:无 thinking 字段,但 output_config.effort:max 仍在
	anthReq := map[string]interface{}{
		"model":         "claude-sonnet-4-5",
		"max_tokens":    32000,
		"stream":        false,
		"output_config": map[string]interface{}{"effort": "max"},
		"messages":       []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	req.Header.Set("Anthropic-Beta", ccRedactBetaHeader)
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-nofield", UserKey: "k-nofield"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured != nil {
		t.Fatalf("无 thinking 字段(即使 effort:max + redact 头)上游应收 ChatTemplateKwargs==nil,实际=%v", captured)
	}
}

// TestHandleNvidia_AdaptiveThinking_InjectsDespiteRedactHeader 端到端锁定开思考态:
// glm-5.2 + thinking.type=adaptive + output_config.effort:max + 带 redact-thinking 头
// → 上游收到 ChatTemplateKwargs=={thinking:true, reasoning_effort:max}。
// 与上一用例互为红绿对照:同样带 redact 头,关思考态不注入、开思考态正常注入,完全由 body 决定。
func TestHandleNvidia_AdaptiveThinking_InjectsDespiteRedactHeader(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-adapt", "nv-adapt", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 真实 Claude Code 开思考 body 形态:thinking.type=adaptive + output_config.effort:max
	anthReq := map[string]interface{}{
		"model":         "claude-sonnet-4-5",
		"max_tokens":    32000,
		"stream":        false,
		"thinking":      map[string]interface{}{"type": "adaptive"},
		"output_config": map[string]interface{}{"effort": "max"},
		"messages":       []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	req.Header.Set("Anthropic-Beta", ccRedactBetaHeader)
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-adapt", UserKey: "k-adapt"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured == nil || captured["thinking"] != true || captured["reasoning_effort"] != "max" {
		t.Fatalf("adaptive+effort:max(带 redact 头)上游应收 {thinking:true,reasoning_effort:max},实际=%v", captured)
	}
}

// TestHandleNvidia_Disabled_NoInjection 端到端:显式 disabled → 不注入。
func TestHandleNvidia_Disabled_NoInjection(t *testing.T) {
	// 显式置全局思考开关 ON,并 defer 复位为 true(全局默认值),隔离用例间状态。
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-dis", "nv-dis", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens":  32000,
		"stream":      false,
		"thinking":   map[string]interface{}{"type": "disabled"},
		"messages":   []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	req.Header.Set("Anthropic-Beta", ccRedactBetaHeader)
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-dis", UserKey: "k-dis"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured != nil {
		t.Fatalf("thinking.type=disabled 上游应收 ChatTemplateKwargs==nil,实际=%v", captured)
	}
}
