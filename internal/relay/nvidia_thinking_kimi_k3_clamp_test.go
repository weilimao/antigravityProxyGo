package relay

// nvidia_thinking_kimi_k3_clamp_test.go 是针对「kimi-k3 在 reasoning_effort=max +
// tool_choice=required 组合下陷入长思考链不收敛」的专用回归测试。
//
// 实测证据(scripts/nvidia_tool_call_probe, 2026-08-25 多角度):
//   - tool_probe_20260825_120241_moonshotai_kimi-k3.log: thinking 长链深度 600s 跑到
//     context deadline exceeded,tool_calls 首帧从未发出。
//   - tool_probe_20260825_140651_summary.log: kimi-k3 reasoning 31646 B,llama-3.3 直连 EOF,
//     与 Claude Code「工具调用中断」现象一致。
//
// 修复策略(见 nvidia_translate_request.go 的 clampNvidiaReasoningEffortForKimiK3):
// 在 Anthropic→OpenAI 转换层把发往 kimi-k3 的 reasoning_effort 从 max 钳到 high,保留
// thinking=true 让上游仍走思考路径,但思考预算受限到 8k-16k,能正常收敛并产生 tool_calls。
//
// 本文件覆盖三个维度:
//   A) 单元 — clampNvidiaReasoningEffortForKimiK3 的边界(命中/未命中/其他档维持);
//   B) 函数注入 — injectNvidiaChatTemplateKwargs 在 kimi-k3 模型上,无论客户端发什么档,
//      落到上游的 reasoning_effort 必为 high;
//   C) 函数注入 — anthropicToOpenAIChat 的同源防护(Claude Code 走 Anthropic 入站的场景)。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-proxy/internal/account"
)

// ============================================================================
// A) clampNvidiaReasoningEffortForKimiK3 函数语义单元测试
// ============================================================================

func TestClampNvidiaReasoningEffortForKimiK3_Hit(t *testing.T) {
	cases := []struct {
		effort, model, want string
	}{
		// max/xhigh → high(核心修复)
		{"max", "moonshotai/kimi-k3", "high"},
		{"xhigh", "moonshotai/kimi-k3", "high"},
		{"max", "moonshotai/KIMI-K3-ultra", "high"}, // 大小写不敏感

		// 其他档不受钳位影响
		{"high", "moonshotai/kimi-k3", "high"},
		{"medium", "moonshotai/kimi-k3", "medium"},
		{"low", "moonshotai/kimi-k3", "low"},
		{"none", "moonshotai/kimi-k3", "none"},

		// 非 kimi-k3 不命中(关键:不能让其他模型也降档)
		{"max", "z-ai/glm-5.2", "max"},
		{"max", "deepseek-ai/deepseek-v4-flash", "max"},
		{"max", "deepseek-ai/deepseek-v4-pro", "max"},
		{"max", "moonshotai/kimi-k2.5", "max"}, // k2.5 不命中 k3 家族
		{"max", "meta/llama-3.3-70b-instruct", "max"},
		{"xhigh", "z-ai/glm-5.2", "xhigh"},
	}
	for _, c := range cases {
		got := clampNvidiaReasoningEffortForKimiK3(c.effort, c.model)
		if got != c.want {
			t.Errorf("clampNvidiaReasoningEffortForKimiK3(%q, %q) = %q, want %q",
				c.effort, c.model, got, c.want)
		}
	}
}

// ============================================================================
// B) injectNvidiaChatTemplateKwargs 在 kimi-k3 上游接收到的最大档永远为 high
//    (覆盖 OpenCode/Codex 等 OpenAI 入站 + NVIDIA 上游 kimi-k3 场景)
// ============================================================================

// TestInjectNvidiaChatTemplateKwargs_KimiK3ClampedToHigh 是「kimi-k3 钳位」的核心防线:
// 即便客户端显式发 reasoning_effort=max, kimi-k3 也必须把上游收到的档压到 high,
// 避免 deepseek 模式 max 档在 kimi-k3 模型上陷入 思考链不收敛 → Claude Code 工具中断。
func TestInjectNvidiaChatTemplateKwargs_KimiK3ClampedToHigh(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	cases := []struct {
		name      string
		clientEff string // 客户端(Codex/OpenCode body)等级
		wantUpEff string // 实际发给上游 kimi-k3 的档(应统一钳位)
	}{
		{"客户端 max 应被压到 high", "max", "high"},
		{"客户端 xhigh 应被压到 high", "xhigh", "high"},
		{"客户端 high 直接注到 high(不受影响)", "high", "high"},
		{"客户端 low 在 NIM 兜底层走 high(不受影响)", "low", "high"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chatReq := &OpenAIChatRequest{Model: "moonshotai/kimi-k3"}
			body := mustJSONString(map[string]interface{}{
				"model":            "moonshotai/kimi-k3",
				"reasoning_effort": tc.clientEff,
				"messages":         []interface{}{},
			})
			// 上游模型与 body.model 同 k3,走 deepseek 模式;clamp 应在注入前拦截 max。
			injectNvidiaChatTemplateKwargs(chatReq, []byte(body), "moonshotai/kimi-k3")

			if chatReq.ChatTemplateKwargs == nil {
				t.Fatalf("应注入 chat_template_kwargs,实际为 nil(模型: kimi-k3 | 客户端档: %s)", tc.clientEff)
			}
			got := chatReq.ChatTemplateKwargs["reasoning_effort"]
			if got != tc.wantUpEff {
				t.Errorf("kimi-k3 上游 reasoning_effort 应为 %q,实际=%q (客户端发了 %q)",
					tc.wantUpEff, got, tc.clientEff)
			}
			if chatReq.ChatTemplateKwargs["thinking"] != true {
				t.Errorf("kimi-k3 上游 thinking 应为 true,实际=%v", chatReq.ChatTemplateKwargs["thinking"])
			}
		})
	}
}

// TestInjectNvidiaChatTemplateKwargs_KimiK3OthersUntouched 反向防护:
// 同样输入下,非 kimi-k3 模型(如 deepseek/llama)必须保持现有 max 档映射,不被误压。
// 否则会把 max-capable 模型也强行降到 high,导致思考强度被低估。
func TestInjectNvidiaChatTemplateKwargs_KimiK3OthersUntouched(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	others := []string{
		"deepseek-ai/deepseek-v4-flash",
		"deepseek-ai/deepseek-v4-pro",
		"z-ai/glm-5.2",
		"meta/llama-3.3-70b-instruct",
	}
	for _, m := range others {
		t.Run(m, func(t *testing.T) {
			chatReq := &OpenAIChatRequest{Model: m}
			body := mustJSONString(map[string]interface{}{
				"model":            m,
				"reasoning_effort": "max",
				"messages":         []interface{}{},
			})
			injectNvidiaChatTemplateKwargs(chatReq, []byte(body), m)
			if chatReq.ChatTemplateKwargs == nil {
				t.Fatalf("非 k3 模型应仍能注入 chat_template_kwargs,实际为 nil")
			}
			got := chatReq.ChatTemplateKwargs["reasoning_effort"]
			if got != "max" {
				t.Errorf("非 k3 模型 %s 应保持 max 档,实际=%q", m, got)
			}
		})
	}
}

// ============================================================================
// C) anthropicToOpenAIChat 同源防护(Claude Code 走 Anthropic 入站场景)
// ============================================================================

// TestAnthropicToOpenAIChat_KimiK3ClampedToHigh 是 Claude Code 走 NVIDIA+Anthropic 链路
// 的端到端防线:thinking.type=enabled 且最大预算(budget_tokens>=16000 → max)也必须在
// 发往 kimi-k3 上游前压到 high。修复前在 max 档下上游陷入长思考并产生 Claude Code
// 「工具调用中断」;修复后保证 kimi-k3 实际收到的是 high 档。
func TestAnthropicToOpenAIChat_KimiK3ClampedToHigh(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	cases := []struct {
		name         string
		budgetTokens int
		wantUpEff    string
	}{
		// thinking.budget_tokens >= 16000 会走 max → clamp 到 high
		{"budget=64000 (旧 max) → upstream high", 64000, "high"},
		{"budget=16000 (边界 max) → upstream high", 16000, "high"},
		{"budget=8192 (正好 high 档)→ upstream high", 8192, "high"},
		{"budget=4096 (medium) → upstream high(mapper 归一)", 4096, "high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			anth := &AnthropicRequest{
				Model: "moonshotai/kimi-k3",
				Messages: []AnthropicMessage{{
					Role: "user",
					Content: []AnthropicContent{{Type: "text", Text: "hi"}},
				}},
				Thinking: &AnthropicThinking{Type: "enabled", BudgetTokens: tc.budgetTokens},
			}
			out, err := AnthropicToOpenAIChat(anth)
			if err != nil {
				t.Fatalf("AnthropicToOpenAIChat failed: %v", err)
			}
			if out.ChatTemplateKwargs == nil {
				t.Fatalf("kimi-k3 上游应注入 chat_template_kwargs,实际为 nil")
			}
			got := out.ChatTemplateKwargs["reasoning_effort"]
			if got != tc.wantUpEff {
				t.Errorf("kimi-k3 上游 reasoning_effort 应为 %q,实际=%q (budget_tokens=%d)",
					tc.wantUpEff, got, tc.budgetTokens)
			}
		})
	}
}

// TestAnthropicToOpenAIChat_KimiK3MaxSimilarToClaudeCode 端到端复现用户报障场景:
// Claude Code UA + thinking.enabled + budget=64000 (最大档) + NIM 上游 kimi-k3
// → 修复前 upstream 收 max 导致 600s 不收敛 / Claude Code 工具中断;
// → 修复后 upstream 收 high,思考能在合理预算内收敛产出 tool_calls。
// 该用例同时覆盖主流程「reasoning_effort 重映射」与「chat_template_kwargs.thinking=true」。
func TestAnthropicToOpenAIChat_KimiK3MaxSimilarToClaudeCode(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req OpenAIChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("upstream unmarshal failed: %v body=%s", err, string(body))
		}
		if req.Model != "moonshotai/kimi-k3" {
			t.Errorf("upstream 收到 model 应为 moonshotai/kimi-k3, 实际=%q", req.Model)
		}
		if req.ChatTemplateKwargs == nil {
			t.Errorf("claude-code + thinking.enabled 上游应收 chat_template_kwargs,可惜为 nil")
		}
		got := req.ChatTemplateKwargs["reasoning_effort"]
		if got != "high" {
			t.Errorf("claude-code 客户端 max 档发往 kimi-k3 上游必须被压到 high,实际=%q", got)
		}
		if req.ChatTemplateKwargs["thinking"] != true {
			t.Errorf("kimi-k3 上游 thinking 应为 true,实际=%v", req.ChatTemplateKwargs["thinking"])
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-x", Model: "moonshotai/kimi-k3",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-kimi-k3-clamp", "nv-kimi-k3-clamp", "test-key", upstream.URL, "moonshotai/kimi-k3")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 仿真 Claude Code 走 NVIDIA+Anthropic 入站,带 thinking.type=enabled 与最大预算。
	anthReq := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 32000,
		"stream":     false,
		"thinking": map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 64000, // 最大档 → 修复前 max;修复后必须被压到 high
		},
		"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	req.Header.Set("User-Agent", "claude-cli/2.1.221 (external, cli)")
	req.Header.Set("Anthropic-Beta",
		"claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12")
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-claude-code-k3-clamp", UserKey: "k-claude-code"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}
