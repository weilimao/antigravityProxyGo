package relay

import (
	"encoding/json"
	"net/http"
	"testing"

	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
)

// opencode_thinking_test.go: 验证 OpenCode 客户端 (User-Agent: opencode/*) 在四大号池
// (NVIDIA, Other OpenAI, Other Anthropic, Grok, Google Gemini) 下基于 output_config.effort
// 触发思考模式的端到端适配。

const testOpenCodeUA = "opencode/1.18.18 ai-sdk/provider-utils/4.0.27 runtime/bun/1.3.14"
const testClaudeCodeUA = "ClaudeCode/2.1.220 darwin/arm64"

// makeOpenCodeAnthReq 构造一个模拟 OpenCode 发出的 AnthropicRequest
func makeOpenCodeAnthReq(model, outputConfigEffort string) *AnthropicRequest {
	req := &AnthropicRequest{
		Model:     model,
		UserAgent: testOpenCodeUA,
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{
					{Type: "text", Text: "Hello"},
				},
			},
		},
	}
	if outputConfigEffort != "" {
		req.OutputConfig = json.RawMessage(`{"effort":"` + outputConfigEffort + `"}`)
	}
	return req
}

// ===== 1. NVIDIA 号池入口测试 =====

func TestOpenCodeThinking_Nvidia_Injected(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// 模拟 OpenCode 客户端发送 output_config: { effort: "max" }
	req := makeOpenCodeAnthReq("z-ai/glm-5.2", "max")

	if !thinkingRequested(req) {
		t.Fatalf("OpenCode + output_config:max 应被 thinkingRequested 判定为 ON")
	}

	out, err := AnthropicToOpenAIChat(req)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}

	if out.ChatTemplateKwargs == nil {
		t.Fatalf("NVIDIA 路径应当注入 ChatTemplateKwargs")
	}
	if thinking, ok := out.ChatTemplateKwargs["thinking"].(bool); !ok || !thinking {
		t.Errorf("ChatTemplateKwargs.thinking 应为 true, 实际: %v", out.ChatTemplateKwargs["thinking"])
	}
	if effort, ok := out.ChatTemplateKwargs["reasoning_effort"].(string); !ok || effort != "max" {
		t.Errorf("ChatTemplateKwargs.reasoning_effort 应为 max, 实际: %v", out.ChatTemplateKwargs["reasoning_effort"])
	}
}

func TestOpenCodeThinking_Nvidia_DifferentEfforts(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	cases := []struct {
		effort     string
		wantEffort string
	}{
		{"low", "high"},    // NIM deepseek mode: low 映射为 high 档
		{"medium", "high"}, // NIM deepseek mode: medium 映射为 high 档
		{"high", "high"},   // high 映射为 high
		{"max", "max"},     // max 映射为 max
	}

	for _, tc := range cases {
		t.Run("effort_"+tc.effort, func(t *testing.T) {
			req := makeOpenCodeAnthReq("z-ai/glm-5.2", tc.effort)
			out, err := AnthropicToOpenAIChat(req)
			if err != nil {
				t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
			}
			if out.ChatTemplateKwargs == nil {
				t.Fatalf("effort=%s 应当注入 ChatTemplateKwargs", tc.effort)
			}
			if effort := out.ChatTemplateKwargs["reasoning_effort"]; effort != tc.wantEffort {
				t.Errorf("reasoning_effort want=%s, got=%v", tc.wantEffort, effort)
			}
		})
	}
}

func TestOpenCodeThinking_Nvidia_NoEffort_NoInjection(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// OpenCode 未开启思考 (default 变体, 无 output_config)
	req := makeOpenCodeAnthReq("z-ai/glm-5.2", "")
	if thinkingRequested(req) {
		t.Fatalf("OpenCode 无 output_config 时应判 OFF")
	}

	out, err := AnthropicToOpenAIChat(req)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("OpenCode 无思考档位时绝不应注入 ChatTemplateKwargs, 实际: %v", out.ChatTemplateKwargs)
	}
}

// ===== 2. Other 号池 OpenAI 兼容上游测试 =====

func TestOpenCodeThinking_OtherPool_OpenAI(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	mappings := []settings.ModelMappingEntry{
		{ClientModel: "other/sensenova/glm-5.2", TargetModel: "glm-5.2", TargetProvider: "other"},
	}

	req := makeOpenCodeAnthReq("other/sensenova/glm-5.2", "high")
	out, err := AnthropicToOpenAIChat(req, mappings)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}

	// Other 号池 OpenAI 上游应走顶层 ReasoningEffort
	if out.ChatTemplateKwargs != nil {
		t.Errorf("Other 号池绝不应注入 NIM chat_template_kwargs")
	}
	if out.ReasoningEffort != "high" {
		t.Errorf("Other 号池 ReasoningEffort 应为 high, 实际: %q", out.ReasoningEffort)
	}
}

// ===== 3. Other 号池 Anthropic 原生端点测试 =====

func TestOpenCodeThinking_OtherPool_Anthropic_BuildUpstreamBody(t *testing.T) {
	pf := &passthroughForward{h: &APICompatHandler{}}

	inBody := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 8192,
		"output_config": {"effort": "max"},
		"messages": [{"role": "user", "content": "hello"}]
	}`)

	upstreamBody, _, err := pf.buildUpstreamBody(
		inBody, "claude-sonnet-4-5", false,
		false, false, true, "anthropic", nil, false, testOpenCodeUA,
	)
	if err != nil {
		t.Fatalf("buildUpstreamBody failed: %v", err)
	}

	var parsed struct {
		Thinking *struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"thinking"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal(upstreamBody, &parsed); err != nil {
		t.Fatalf("unmarshal upstreamBody failed: %v", err)
	}

	if parsed.Thinking == nil {
		t.Fatalf("OpenCode 打 Anthropic 上游时应自动补齐 thinking 字段")
	}
	if parsed.Thinking.Type != "enabled" || parsed.Thinking.BudgetTokens != 64000 {
		t.Errorf("thinking 字段异常: type=%s, budget=%d", parsed.Thinking.Type, parsed.Thinking.BudgetTokens)
	}
	if parsed.MaxTokens <= parsed.Thinking.BudgetTokens {
		t.Errorf("max_tokens (%d) 必须大于 budget_tokens (%d)", parsed.MaxTokens, parsed.Thinking.BudgetTokens)
	}
}

// ===== 4. Grok 号池测试 =====

func TestOpenCodeThinking_Grok(t *testing.T) {
	req := makeOpenCodeAnthReq("grok/grok-4.6", "max")

	mode, effort := grokResolveAnthropicThinking(req, true)
	if mode != grokThinkOn {
		t.Fatalf("OpenCode + output_config:max 应返回 grokThinkOn, 实际: %v", mode)
	}
	if effort != "max" {
		t.Errorf("effort want=max, got=%q", effort)
	}

	// 验证未开思考态
	reqNoEffort := makeOpenCodeAnthReq("grok/grok-4.6", "")
	mode2, _ := grokResolveAnthropicThinking(reqNoEffort, true)
	if mode2 != grokThinkUnspecified {
		t.Fatalf("OpenCode 无 effort 应返回 grokThinkUnspecified, 实际: %v", mode2)
	}
}

// ===== 5. Google / Gemini 池测试 =====

func TestOpenCodeThinking_Gemini(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	req := makeOpenCodeAnthReq("gemini-3.7-flash-high", "high")
	gemReq := TranslateAnthropicToGemini(req)

	if gemReq.GenerationConfig == nil || gemReq.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("Gemini 请求应注入 ThinkingConfig")
	}
	tc := gemReq.GenerationConfig.ThinkingConfig
	if tc.IncludeThoughts == nil || !*tc.IncludeThoughts {
		t.Errorf("IncludeThoughts 应为 true")
	}
	if tc.ThinkingBudget != 32000 {
		t.Errorf("ThinkingBudget want=32000 (high 档), got=%d", tc.ThinkingBudget)
	}
}

// ===== 6. Claude Code 关思考防误开回归测试 =====

func TestClaudeCode_NoThinkingField_RemainsOff(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// Claude Code 关思考态: 无 thinking 字段, 残留 output_config.effort: "max"
	claudeReq := &AnthropicRequest{
		Model:        "z-ai/glm-5.2",
		UserAgent:    testClaudeCodeUA,
		OutputConfig: json.RawMessage(`{"effort":"max"}`),
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Hello"}}},
		},
	}

	if thinkingRequested(claudeReq) {
		t.Fatalf("Claude Code 关思考残留 output_config 严禁被误判为 ON")
	}

	out, _ := AnthropicToOpenAIChat(claudeReq)
	if out.ChatTemplateKwargs != nil {
		t.Fatalf("Claude Code 关思考态绝不应注入 ChatTemplateKwargs, 实际: %v", out.ChatTemplateKwargs)
	}
}

// ===== 7. OpenCode X-Session-Id 会话键提取与 Sticky 选号测试 =====

func TestOpenCode_SessionKey_Extraction(t *testing.T) {
	h := &APICompatHandler{
		sessionRouter: session.NewRouter(),
	}

	const testSessionID = "ses_ffad672d8ffenm0wrlAwIM85IH"

	// 场景 1: 携带 X-Session-Id
	httpReq1, _ := http.NewRequest("POST", "/nvidia/v1/messages", nil)
	httpReq1.Header.Set("User-Agent", testOpenCodeUA)
	httpReq1.Header.Set("X-Session-Id", testSessionID)

	sess1 := &RelaySession{UserID: "user_123"}
	h.ensureSessionKey(sess1, httpReq1, nil)

	if sess1.SessionKey != "opencode:"+testSessionID {
		t.Errorf("SessionKey want=opencode:%s, got=%q", testSessionID, sess1.SessionKey)
	}
	if stickyKey := h.stickyKeyOf(sess1); stickyKey != "opencode:"+testSessionID {
		t.Errorf("stickyKey want=opencode:%s, got=%q", testSessionID, stickyKey)
	}

	// 场景 2: 仅携带 X-Session-Affinity
	httpReq2, _ := http.NewRequest("POST", "/nvidia/v1/messages", nil)
	httpReq2.Header.Set("User-Agent", testOpenCodeUA)
	httpReq2.Header.Set("X-Session-Affinity", testSessionID)

	sess2 := &RelaySession{UserID: "user_123"}
	h.ensureSessionKey(sess2, httpReq2, nil)

	if sess2.SessionKey != "opencode:"+testSessionID {
		t.Errorf("SessionKey want=opencode:%s, got=%q", testSessionID, sess2.SessionKey)
	}
}

