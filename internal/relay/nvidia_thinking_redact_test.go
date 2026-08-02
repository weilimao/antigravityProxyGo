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

// nvidia_thinking_redact_test.go 锁定 NVIDIA 中继对 Claude Code "关闭思考"信号(Anthropic-Beta
// 头部 redact-thinking-*)的识别与翻译层短路。Claude Code 关闭思考开关时:
//   - body 不带 thinking 字段,也不带 thinking:{type:disabled};
//   - 仅在 Anthropic-Beta 头里追加 redact-thinking-2026-02-12 标志位。
//
// 改动前:代理翻译层只看 body(无 disabled 信号),fallback 路径对推理模型(glm-5.2 等)强行注入
// chat_template_kwargs:{thinking:true} → 上游仍出思考 → CLI 体感"关闭思考没用"。
// 改动后:头部 redact-thinking-* 命中即绝对跳过 thinking 注入,与客户端关闭意图严格对齐。
//
// 本文件同时覆盖三层:头部解析闸函数、AnthropicToOpenAIChat/injectNvidiaChatTemplateKwargs
// 翻译注入、handleNvidia 端到端业务流(真实请求经选号→翻译→上游收到的 OpenAIChatRequest)。

// ===== 第一层:anthropicBetaThinkingRedacted 头部闸函数 =====

func TestAnthropicBetaThinkingRedacted_DirectHit(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Beta", "redact-thinking-2026-02-12")
	if !anthropicBetaThinkingRedacted(h) {
		t.Fatalf("仅含 redact-thinking-2026-02-12 应判定为关思考")
	}
}

func TestAnthropicBetaThinkingRedacted_MixedComma(t *testing.T) {
	// 真实 Claude Code 形态:多条 beta 用逗号拼接成单条 header,redact-thinking 混在其中
	h := http.Header{}
	h.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,effort-2025-11-24")
	if !anthropicBetaThinkingRedacted(h) {
		t.Fatalf("逗号拼接的多 beta 中含 redact-thinking- 应判定为关思考")
	}
}

func TestAnthropicBetaThinkingRedacted_MultipleHeaders(t *testing.T) {
	// http.Header 允许同名 header 多条;SDK 也可能拆两条发
	h := http.Header{}
	h.Add("Anthropic-Beta", "claude-code-20250219")
	h.Add("Anthropic-Beta", "redact-thinking-2026-02-12")
	if !anthropicBetaThinkingRedacted(h) {
		t.Fatalf("多条 Anthropic-Beta 中含 redact-thinking- 应判定为关思考")
	}
}

func TestAnthropicBetaThinkingRedacted_NoRedact(t *testing.T) {
	// 开思考态:无 redact-thinking 前缀
	h := http.Header{}
	h.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,effort-2025-11-24")
	if anthropicBetaThinkingRedacted(h) {
		t.Fatalf("无 redact-thinking- 前缀应判定为非关思考(开思考态)")
	}
}

func TestAnthropicBetaThinkingRedacted_NoHeader(t *testing.T) {
	if anthropicBetaThinkingRedacted(http.Header{}) {
		t.Fatalf("无 Anthropic-Beta 头应判定为非关思考")
	}
	if anthropicBetaThinkingRedacted(nil) {
		t.Fatalf("nil 头应判定为非关思考(回归安全)")
	}
}

func TestAnthropicBetaThinkingRedacted_PrefixVersionTolerant(t *testing.T) {
	// beta 末尾日期版本号会变,前缀匹配应容忍未来 redact-thinking-9999-99-99
	h := http.Header{}
	h.Set("Anthropic-Beta", "claude-code-20250219,redact-thinking-9999-12-31")
	if !anthropicBetaThinkingRedacted(h) {
		t.Fatalf("前缀匹配应容忍 redact-thinking 末尾版本号变化")
	}
	// 仅前缀子串不算命中(如 "interleaved-thinking-..." 不含 redact-thinking-)
	h2 := http.Header{}
	h2.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13")
	if anthropicBetaThinkingRedacted(h2) {
		t.Fatalf("不含 redact-thinking- 前缀的 beta 不应误判为关思考")
	}
}

// ===== 第二层:AnthropicToOpenAIChat 翻译注入短路 =====

// TestAnthropicToOpenAIChat_RedactSkipsEffortInjection 锁定:output_config:{effort:max}
// 在思考态下本应注入 {thinking:true,reasoning_effort:max},但 redact-thinking 命中后绝对短路为不注入。
// 这是用户报障的核心场景:CLI 切强档仍发 effort:max,WiredEdge redact-thinking 应让代理尊重关闭意图。
func TestAnthropicToOpenAIChat_RedactSkipsEffortInjection(t *testing.T) {
	// 对照组(redact=false):effort:max → 注入 thinking:true + reasoning_effort:max
	reqOpen := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"adaptive"}`, `{"effort":"max"}`)
	outOpen, _ := AnthropicToOpenAIChat(reqOpen, false)
	kwOpen := ctKwargs(t, outOpen)
	if kwOpen["thinking"] != true || kwOpen["reasoning_effort"] != "max" {
		t.Fatalf("开思考态:adaptive+effort:max 应注入 thinking+reasoning_effort:max,实际=%v", kwOpen)
	}

	// 实验组(redact=true):同样 adaptive+effort:max,但头部 redact-thinking 命中 → 绝对不注入
	reqRed := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"adaptive"}`, `{"effort":"max"}`)
	outRed, _ := AnthropicToOpenAIChat(reqRed, true)
	if outRed.ChatTemplateKwargs != nil {
		t.Fatalf("redact-thinking 命中应跳过 effort 注入,实际注入了=%v", outRed.ChatTemplateKwargs)
	}
}

// TestAnthropicToOpenAIChat_RedactSkipsReasoningModelFallback 锁定:推理模型(glm-5.2)
// 客户端未发思考时本应 fallback 注入 {thinking:true},redact-thinking 命中后绝对短路。
// 这是 Claude Code 关思考的真实 body 形态:无 thinking 字段、无 output_config disabled,
// 仅靠头部 redact-thinking 表达关闭。
func TestAnthropicToOpenAIChat_RedactSkipsReasoningModelFallback(t *testing.T) {
	// 对照组(redact=false):推理模型无思考配置 → fallback 注入 thinking:true
	reqOpen := makeAnthReq(t, "z-ai/glm-5.2", "", "")
	outOpen, _ := AnthropicToOpenAIChat(reqOpen, false)
	if outOpen.ChatTemplateKwargs == nil || outOpen.ChatTemplateKwargs["thinking"] != true {
		t.Fatalf("开思考态:推理模型未发思考应 fallback 注入 thinking:true,实际=%v", outOpen.ChatTemplateKwargs)
	}

	// 实验组(redact=true):同样无思考配置,但 redact-thinking 命中 → 不 fallback
	reqRed := makeAnthReq(t, "z-ai/glm-5.2", "", "")
	outRed, _ := AnthropicToOpenAIChat(reqRed, true)
	if outRed.ChatTemplateKwargs != nil {
		t.Fatalf("redact-thinking 命中应跳过推理模型 fallback,实际注入了=%v", outRed.ChatTemplateKwargs)
	}
}

// TestAnthropicToOpenAIChat_RedactOutrulesExplicitDisabled 锁定:redact-thinking 与 body
// 显式 disabled 同时存在时仍不注入(双重关思考信号,redact 优先级与 disabled 等强,结果一致为 nil)。
func TestAnthropicToOpenAIChat_RedactOutrulesExplicitDisabled(t *testing.T) {
	req := makeAnthReq(t, "z-ai/glm-5.2", `{"type":"disabled"}`, "")
	out, _ := AnthropicToOpenAIChat(req, true)
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("disabled + redact-thinking 应双重不注入,实际=%v", out.ChatTemplateKwargs)
	}
}

// ===== 第三层:injectNvidiaChatTemplateKwargs 翻译注入短路 =====

// TestInjectNvidiaChatTemplateKwargs_RedactSkipsEffort 锁定:OpenAI Chat 入站
// 顶层 reasoning_effort:high 在思考态本应注入,redact-thinking 命中后绝对短路。
func TestInjectNvidiaChatTemplateKwargs_RedactSkipsEffort(t *testing.T) {
	body := mustJSONString(map[string]interface{}{
		"model": "x", "reasoning_effort": "high", "messages": []interface{}{},
	})

	// 对照组:无 redact → 注入 thinking:true + reasoning_effort:high
	chatOpen := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatOpen, []byte(body), "z-ai/glm-5.2", false)
	if chatOpen.ChatTemplateKwargs == nil || chatOpen.ChatTemplateKwargs["thinking"] != true {
		t.Fatalf("开思考态:reasoning_effort:high 应注入 thinking:true,实际=%v", chatOpen.ChatTemplateKwargs)
	}

	// 实验组:redact=true → 不注入
	chatRed := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatRed, []byte(body), "z-ai/glm-5.2", true)
	if chatRed.ChatTemplateKwargs != nil {
		t.Fatalf("redact-thinking 命中应跳过 reasoning_effort 注入,实际=%v", chatRed.ChatTemplateKwargs)
	}
}

// TestInjectNvidiaChatTemplateKwargs_RedactSkipsFallback 锁定:推理模型无 effort 时
// 本应 fallback 注入 thinking:true,redact-thinking 命中后绝对短路。
func TestInjectNvidiaChatTemplateKwargs_RedactSkipsFallback(t *testing.T) {
	body := mustJSONString(map[string]interface{}{
		"model": "x", "messages": []interface{}{},
	})

	// 对照组:推理模型无 effort → fallback thinking:true
	chatOpen := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatOpen, []byte(body), "z-ai/glm-5.2", false)
	if chatOpen.ChatTemplateKwargs == nil || chatOpen.ChatTemplateKwargs["thinking"] != true {
		t.Fatalf("开思考态:推理模型无 effort 应 fallback thinking:true,实际=%v", chatOpen.ChatTemplateKwargs)
	}

	// 实验组:redact=true → 不 fallback
	chatRed := &OpenAIChatRequest{Model: "z-ai/glm-5.2"}
	injectNvidiaChatTemplateKwargs(chatRed, []byte(body), "z-ai/glm-5.2", true)
	if chatRed.ChatTemplateKwargs != nil {
		t.Fatalf("redact-thinking 命中应跳过推理模型 fallback,实际=%v", chatRed.ChatTemplateKwargs)
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

// TestHandleNvidia_AnthropicRedactThinking_NoInjection 锁定端到端:
// 客户端带 Anthropic-Beta: redact-thinking-2026-02-12 头 + 推理模型(glm-5.2)
// → 经 handleNvidia 选号翻译后,上游收到的 OpenAIChatRequest.ChatTemplateKwargs 必须为 nil。
// 这是业务闭环:从 HTTP 头部到上游请求体的关思考信号无损贯穿。
func TestHandleNvidia_AnthropicRedactThinking_NoInjection(t *testing.T) {
	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-r", "nvidia-r", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 真实 Claude Code 关思考 body 形态:无 thinking 字段、无 output_config disabled,只 max_tokens+messages
	anthReq := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens":  32000,
		"stream":      false,
		"messages":    []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	// 关思考信号:Anthropic-Beta 头携带 redact-thinking-2026-02-12(混在正常 beta 列表里)
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24")
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-redact", UserKey: "k-r"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured != nil {
		t.Fatalf("redact-thinking 头应让上游收到 ChatTemplateKwargs==nil,实际上游收到=%v", captured)
	}
}

// TestHandleNvidia_AnthropicNoRedact_ReasoningModelDefaultThinking 锁定端到端对照:
// 同样推理模型(glm-5.2)、同样无 body 思考配置,但不带 redact-thinking 头
// → 上游收到 ChatTemplateKwargs={thinking:true}(fallback 路径正常注入,开思考态)。
// 与上一用例互为红绿对照,确保 redact 修复未误伤正常的推理模型默认思考行为。
func TestHandleNvidia_AnthropicNoRedact_ReasoningModelDefaultThinking(t *testing.T) {
	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-o", "nvidia-o", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens":  32000,
		"stream":      false,
		"messages":    []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	// 开思考态:Anthropic-Beta 头无 redact-thinking- 前缀
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24")
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-open", UserKey: "k-o"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured == nil || captured["thinking"] != true {
		t.Fatalf("开思考态:推理模型未发思考应 fallback 注入 thinking:true,上游收到=%v", captured)
	}
}

// TestHandleNvidia_AnthropicRedact_EffortMaxStillNoInjection 锁定端到端:
// 客户端带 effort:max(Claude Code 强档)且同时带 redact-thinking 头(Claude Code 关思考开关)
// → 上游收到 ChatTemplateKwargs==nil。覆盖用户真实日志场景:
// body 同时含 output_config:{effort:max} 与 redact-thinking-2026-02-12 头,修复前会注入 max。
func TestHandleNvidia_AnthropicRedact_EffortMaxStillNoInjection(t *testing.T) {
	var captured map[string]interface{}
	upstream := captureUpstreamChatTemplateKwargs(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-em", "nvidia-em", "test-key", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := map[string]interface{}{
		"model":         "claude-sonnet-4-5",
		"max_tokens":     32000,
		"stream":         false,
		"output_config":  map[string]interface{}{"effort": "max"},
		"messages":       []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,effort-2025-11-24")
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-em", UserKey: "k-em"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if captured != nil {
		t.Fatalf("effort:max + redact-thinking 头应让上游收到 ChatTemplateKwargs==nil,实际上游收到=%v", captured)
	}
}
