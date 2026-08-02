package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"antigravity-proxy/internal/relay"
)

// TestCustomThinkingOverride_ClaudeBudgetGuard 通过复刻 handler.go:720-785 CustomThinkingOverride 的
// 注入逻辑(含新增的 claude budget/maxOutput 不变式守护),验证当目标为 claude-sonnet/opus 且注入了
// 固定 thinkingBudget 时,maxOutputTokens 必被抬升到 > thinkingBudget,避免 Vertex 400。
//
// 不直接构造 ProxyHandler(依赖太多注入桩),而是用同一份注入代码逻辑对 bodyMap 做等价操作,
// 断言最终 generationConfig 字段——这与线上 handler.go 行为同构,且纯函数守护由
// relay.CalcClaudeGuaranteedMaxOutput 单独覆盖(见 internal/relay/claude_budget_test.go)。
func TestCustomThinkingOverride_ClaudeBudgetGuard(t *testing.T) {
	cases := []struct {
		name            string
		model           string
		supportsThinking bool
		budget           int
		minBudget        int
		maxOutputTokens  int
		// 期望
		wantThinkingBudget any // nil 表示应被省略
		wantMaxOutput       int
		wantIncludeThoughts bool
		desc                string
	}{
		{
			name: "claude_sonnet_budget_gt_user_max", model: "claude-sonnet-4-5",
			supportsThinking: true, budget: 8192, minBudget: 0, maxOutputTokens: 2048,
			wantThinkingBudget: 8192, wantMaxOutput: 8192 + 128, wantIncludeThoughts: true,
			desc: "Codex 走 antigravity 池 claude-sonnet:用户 max(2048)<budget(8192),守护抬升 max 到 8320",
		},
		{
			name: "claude_opus_user_unset_max", model: "claude-opus-4-6",
			supportsThinking: true, budget: 10000, minBudget: 0, maxOutputTokens: 0,
			wantThinkingBudget: 10000, wantMaxOutput: 10000 + 128, wantIncludeThoughts: true,
			desc: "claude-opus 用户未设 max:守护补 budget+128,避免 max_tokens<=budget 触发 400",
		},
		{
			name: "claude_sonnet_max_above_margin_preserved", model: "claude-sonnet-4-5",
			supportsThinking: true, budget: 8192, minBudget: 0, maxOutputTokens: 20000,
			wantThinkingBudget: 8192, wantMaxOutput: 20000, wantIncludeThoughts: true,
			desc: "max(20000) 已 >= budget+128,守护不动,保留用户上限",
		},
		{
			name: "claude_min_budget_clamp_then_guard", model: "claude-sonnet-4-5",
			supportsThinking: true, budget: 100, minBudget: 4096, maxOutputTokens: 0,
			wantThinkingBudget: 4096, wantMaxOutput: 4096 + 128, wantIncludeThoughts: true,
			desc: "budget(100) 经 minBudget(4096)抬升到 4096,再以此 4096 为 committedBudget 守护 max",
		},
		{
			name: "claude_adaptive_budget_no_guard", model: "claude-sonnet-4-5",
			supportsThinking: true, budget: -1, minBudget: 0, maxOutputTokens: 4096,
			wantThinkingBudget: nil, wantMaxOutput: 4096, wantIncludeThoughts: true,
			desc: "-1 自适应不写 thinkingBudget,committedBudget<=0,守护不动作,max 保持 4096",
		},
		{
			name: "claude_thinking_disabled_no_guard", model: "claude-sonnet-4-5",
			supportsThinking: false, budget: 8192, minBudget: 0, maxOutputTokens: 4096,
			wantThinkingBudget: nil, wantMaxOutput: 4096, wantIncludeThoughts: false,
			desc: "supportsThinking=false 走 includeThoughts:false 分支,不写 budget,守护不动作",
		},
		{
			name: "gemini_flash_budget_not_guarded", model: "gemini-3.5-flash",
			supportsThinking: true, budget: 8192, minBudget: 0, maxOutputTokens: 2048,
			wantThinkingBudget: 8192, wantMaxOutput: 2048, wantIncludeThoughts: true,
			desc: "gemini-3.5-flash 注入 budget:8192 但非 claude,守护不抬升 max(2048 原样,走 Gemini 协议无该约束)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inputJSON := `{"model":"` + c.model + `","project":"test-project","request":{"contents":[{"role":"user","parts":[{"text":"Hi"}]}],"generationConfig":{"maxOutputTokens":1024}}}`
			var bodyMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputJSON), &bodyMap); err != nil {
				t.Fatalf("unmarshal input: %v", err)
			}

			// ====== 复刻 handler.go:720-785 CustomThinkingOverride 注入逻辑 ======
			checkModel := c.model
			isTabModel := strings.Contains(strings.ToLower(checkModel), "tab")
			if isTabModel {
				t.Fatalf("fixture model %q should not be tab", c.model)
			}

			supportsThinking := c.supportsThinking
			budget := c.budget
			minBudget := c.minBudget
			maxOutputTokens := c.maxOutputTokens

			var thinkingCfg map[string]interface{}
			if !supportsThinking || budget == 0 {
				thinkingCfg = map[string]interface{}{"includeThoughts": false}
			} else if budget == -1 {
				thinkingCfg = map[string]interface{}{"includeThoughts": true}
			} else {
				clampedBudget := budget
				if minBudget > 0 && clampedBudget < minBudget {
					clampedBudget = minBudget
				}
				thinkingCfg = map[string]interface{}{
					"includeThoughts": true,
					"thinkingBudget":  clampedBudget,
				}
				// 新增的 claude budget/maxOutput 不变式守护(与 handler.go 落地代码一致)
				maxOutputTokens = relay.CalcClaudeGuaranteedMaxOutput(
					clampedBudget, maxOutputTokens, relay.IsClaudeModelForBudget(checkModel),
				)
			}

			if reqMap, ok := bodyMap["request"].(map[string]interface{}); ok {
				genConfig, ok := reqMap["generationConfig"].(map[string]interface{})
				if !ok {
					genConfig = make(map[string]interface{})
					reqMap["generationConfig"] = genConfig
				}
				genConfig["thinkingConfig"] = thinkingCfg
				if maxOutputTokens > 0 {
					genConfig["maxOutputTokens"] = maxOutputTokens
				}
			}

			// ====== 断言最终 generationConfig ======
			outBytes, _ := json.Marshal(bodyMap)
			var resMap map[string]interface{}
			if err := json.Unmarshal(outBytes, &resMap); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			resReq := resMap["request"].(map[string]interface{})
			resGen := resReq["generationConfig"].(map[string]interface{})
			resThinking := resGen["thinkingConfig"].(map[string]interface{})

			if resThinking["includeThoughts"] != c.wantIncludeThoughts {
				t.Errorf("includeThoughts = %v, want %v | %s", resThinking["includeThoughts"], c.wantIncludeThoughts, c.desc)
			}
			if c.wantThinkingBudget == nil {
				if _, exists := resThinking["thinkingBudget"]; exists {
					t.Errorf("thinkingBudget should be omitted | %s, got %v", c.desc, resThinking["thinkingBudget"])
				}
			} else {
				gotBudget, ok := resThinking["thinkingBudget"]
				if !ok {
					t.Errorf("thinkingBudget missing | %s", c.desc)
				} else if int(gotBudget.(float64)) != c.wantThinkingBudget.(int) {
					t.Errorf("thinkingBudget = %v, want %v | %s", gotBudget, c.wantThinkingBudget, c.desc)
				}
			}
			gotMax := int(resGen["maxOutputTokens"].(float64))
			if gotMax != c.wantMaxOutput {
				t.Errorf("maxOutputTokens = %d, want %d | %s", gotMax, c.wantMaxOutput, c.desc)
			}
		})
	}
}
