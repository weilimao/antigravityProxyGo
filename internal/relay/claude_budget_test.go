package relay

import "testing"

// TestCalcClaudeGuaranteedMaxOutput 覆盖 Vertex Anthropic "max_tokens > thinking.budget_tokens" 不变式守护纯函数:
// 仅 claude-sonnet/opus + committedBudget>0 时才可能抬升 maxOutputTokens,其余路径原样返回。
func TestCalcClaudeGuaranteedMaxOutput(t *testing.T) {
	cases := []struct {
		name            string
		committedBudget int
		maxOutputTokens int
		isClaudeModel   bool
		want            int
		desc            string
	}{
		// ===== claude 路径 + 固定 budget(本次会向上游写 thinkingBudget)=====
		{
			name: "claude_user_unset_max", committedBudget: 8192, maxOutputTokens: 0, isClaudeModel: true,
			want: 8192 + ClaudeBudgetMargin,
			desc:  "Codex/Claude Code 未设最大输出上限,守护补 budget+128 满足 max_tokens>budget",
		},
		{
			name: "claude_max_below_budget", committedBudget: 10000, maxOutputTokens: 3000, isClaudeModel: true,
			want: 10000 + ClaudeBudgetMargin,
			desc:  "客户端 max_tokens(3000) < budget(10000),必触发 Vertex 400;守护抬升到 10128",
		},
		{
			name: "claude_max_equal_budget", committedBudget: 8192, maxOutputTokens: 8192, isClaudeModel: true,
			want: 8192 + ClaudeBudgetMargin,
			desc:  "max_tokens == budget 仍违反严格大于;守护抬升",
		},
		{
			name: "claude_max_just_above_budget_no_margin", committedBudget: 8192, maxOutputTokens: 8193, isClaudeModel: true,
			want: 8192 + ClaudeBudgetMargin,
			desc:  "max_tokens 仅比 budget 大 1,小于 budget+128 安全余量,守护仍抬升避免重译舍入踩线",
		},
		{
			name: "claude_max_above_margin_preserved", committedBudget: 8192, maxOutputTokens: 20000, isClaudeModel: true,
			want: 20000,
			desc:  "max_tokens 已 >= budget+128,不动,保留用户显式上限",
		},
		{
			name: "claude_max_equals_required_boundary", committedBudget: 8192, maxOutputTokens: 8192 + ClaudeBudgetMargin, isClaudeModel: true,
			want: 8192 + ClaudeBudgetMargin,
			desc:  "max_tokens 恰 == budget+128,边界值已满足不变式,原样保留",
		},

		// ===== 非 claude 路径:守护不动作(committedBudget>0 但 isClaudeModel=false)=====
		{
			name: "gemini_flash_budget_ignored", committedBudget: 8192, maxOutputTokens: 2048, isClaudeModel: false,
			want: 2048,
			desc:  "gemini-3.5-flash 路径 TranslateOpenAIToGemini 的 budget:8192 注入不走 Vertex Anthropic,守护不抬升",
		},
		{
			name: "gemini_pro_user_unset_ignored", committedBudget: 8192, maxOutputTokens: 0, isClaudeModel: false,
			want: 0,
			desc:  "gemini pro + 用户未设 max,非 claude 不触发守护,原样 0(由下游 gemini 默认处理)",
		},

		// ===== claude 路径但 committedBudget<=0(includeThoughts-only / disabled / -1 自适应)=====
		{
			name: "claude_no_budget_ignored", committedBudget: 0, maxOutputTokens: 4096, isClaudeModel: true,
			want: 4096,
			desc:  "claude 但 thinking 仅 includeThoughts 无 budget 字段,不写 thinkingBudget,守护不动作",
		},
		{
			name: "claude_budget_negative_ignored", committedBudget: -1, maxOutputTokens: 4096, isClaudeModel: true,
			want: 4096,
			desc:  "-1 自适应预算不写 thinkingBudget 字段,守护对负值视为无 budget 不动作",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CalcClaudeGuaranteedMaxOutput(c.committedBudget, c.maxOutputTokens, c.isClaudeModel)
			if got != c.want {
				t.Errorf("CalcClaudeGuaranteedMaxOutput(budget=%d, max=%d, isClaude=%v) = %d, want %d | %s",
					c.committedBudget, c.maxOutputTokens, c.isClaudeModel, got, c.want, c.desc)
			}
		})
	}
}

// TestIsClaudeModelForBudget 校验 claude 模型判定与 MapClientModelToGemini 保留原样的 claude 精确对齐:
// 仅 claude-sonnet / claude-opus 命中(上游走 Vertex Anthropic);旧 claude-3-* / claude-haiku-* 已被
// MapClientModelToGemini 降级为 gemini-1.5-*,不再受 Vertex "max_tokens>budget" 约束,故不命中。
func TestIsClaudeModelForBudget(t *testing.T) {
	cases := []struct {
		model string
		want  bool
		desc  string
	}{
		{"claude-sonnet-4-5", true, "Codex/Claude Code 主用,MapClientModelToGemini 原样保留,命中"},
		{"Claude-Sonnet-4-5", true, "大小写不敏感命中"},
		{"claude-opus-4-6-thinking", true, "opus 系命中"},
		{"claude-opus-4-1", true, "opus 旧版命中"},
		{"claude-3-5-sonnet", false, "MapClientModelToGemini 降级为 gemini-1.5-pro(非连续 'claude-sonnet' 子串:claude- 之后是 3-5- 而非 sonnet),不再走 Vertex 协议,不命中"},
		{"claude-3-opus", false, "MapClientModelToGemini 降级为 gemini-1.5-pro,不含连续 'claude-opus' 子串,不命中"},
		{"claude-3-haiku", false, "降级为 gemini-1.5-flash,不命中"},
		{"gemini-3.5-flash", false, "纯 gemini,不命中"},
		{"gemini-2.5-pro", false, "纯 gemini,不命中"},
		{"", false, "空模型名不命中"},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			got := IsClaudeModelForBudget(c.model)
			if got != c.want {
				t.Errorf("IsClaudeModelForBudget(%q) = %v, want %v | %s", c.model, got, c.want, c.desc)
			}
		})
	}
}

// TestTranslateOpenAIToGemini_ClaudeThinkingBudgetGuard 集成覆盖 TranslateOpenAIToGemini 入口的
// claude-*-thinking 守护:claude-opus/sonnet-*-thinking 命中 "thinking" 关键字注入 ThinkingBudget:8192,
// Codex 常带小 max_tokens(如 2048)→ 守护必须抬升 MaxOutputTokens 到 8192+128 避免 Vertex 400。
// gemini flash/pro 同样命中本注入分支但非 claude,守护不动作(走 Gemini 协议无该约束)。
func TestTranslateOpenAIToGemini_ClaudeThinkingBudgetGuard(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	mt := func(v int) *int { return &v }

	cases := []struct {
		name          string
		model         string
		maxTokens     *int
		wantBudget    int
		wantMaxOutput int
		desc          string
	}{
		{
			name: "claude_opus_thinking_user_max_below_8192", model: "claude-opus-4-6-thinking",
			maxTokens: mt(2048), wantBudget: 8192, wantMaxOutput: 8192 + ClaudeBudgetMargin,
			desc: "Codex claude-opus-4-6-thinking max(2048)<注入的 8192,守护抬升避免 Vertex 400",
		},
		{
			name: "claude_sonnet_thinking_user_unset_max", model: "claude-sonnet-4-5-thinking",
			maxTokens: nil, wantBudget: 8192, wantMaxOutput: 8192 + ClaudeBudgetMargin,
			desc: "未设 max,守护补 8192+128",
		},
		{
			name: "claude_opus_thinking_user_max_above_margin", model: "claude-opus-4-6-thinking",
			maxTokens: mt(20000), wantBudget: 8192, wantMaxOutput: 20000,
			desc: "max(20000)>=8192+128,守护保留用户上限",
		},
		{
			name: "gemini_flash_thinking_not_guarded", model: "gemini-3.5-flash",
			maxTokens: mt(2048), wantBudget: 8192, wantMaxOutput: 2048,
			desc: "gemini-3.5-flash 命中注入分支 budget:8192 但非 claude,守护不抬升 max",
		},
		{
			name: "claude_non_thinking_no_budget_injected", model: "claude-sonnet-4-5",
			maxTokens: mt(4096), wantBudget: 0, wantMaxOutput: 4096,
			desc: "claude-sonnet-4-5(不含 thinking)不命中注入分支,无 budget 注入,max 原样(走 ProxyEngine 守护)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			openReq := &OpenAIRequest{
				Model:    c.model,
				MaxTokens: c.maxTokens,
				Messages: []OpenAIMessage{{Role: "user", Content: "hi"}},
			}
			gem := TranslateOpenAIToGemini(openReq)

			if gem.GenerationConfig == nil {
				if c.wantMaxOutput > 0 || c.wantBudget > 0 {
					t.Fatalf("GenerationConfig nil | %s", c.desc)
				}
				return
			}
			if c.wantBudget > 0 {
				if gem.GenerationConfig.ThinkingConfig == nil ||
					gem.GenerationConfig.ThinkingConfig.ThinkingBudget != c.wantBudget {
					got := 0
					if gem.GenerationConfig.ThinkingConfig != nil {
						got = gem.GenerationConfig.ThinkingConfig.ThinkingBudget
					}
					t.Errorf("ThinkingBudget = %d, want %d | %s", got, c.wantBudget, c.desc)
				}
			} else {
				if gem.GenerationConfig.ThinkingConfig != nil && gem.GenerationConfig.ThinkingConfig.ThinkingBudget != 0 {
					got := gem.GenerationConfig.ThinkingConfig.ThinkingBudget
					if got != c.wantBudget {
						t.Errorf("ThinkingBudget = %d, want %d | %s", got, c.wantBudget, c.desc)
					}
				}
			}
			if c.wantMaxOutput > 0 {
				if gem.GenerationConfig.MaxOutputTokens == nil {
					t.Errorf("MaxOutputTokens nil, want %d | %s", c.wantMaxOutput, c.desc)
				} else if *gem.GenerationConfig.MaxOutputTokens != c.wantMaxOutput {
					t.Errorf("MaxOutputTokens = %d, want %d | %s", *gem.GenerationConfig.MaxOutputTokens, c.wantMaxOutput, c.desc)
				}
			} else if c.maxTokens != nil {
				if gem.GenerationConfig.MaxOutputTokens == nil || *gem.GenerationConfig.MaxOutputTokens != *c.maxTokens {
					got := -1
					if gem.GenerationConfig.MaxOutputTokens != nil {
						got = *gem.GenerationConfig.MaxOutputTokens
					}
					t.Errorf("MaxOutputTokens = %d, want %d(原样透传) | %s", got, *c.maxTokens, c.desc)
				}
			}
		})
	}
}

// TestTranslateAnthropicToGemini_ClaudeBudgetGuard 集成覆盖 TranslateAnthropicToGemini 入口的 claude
// budget/maxOutput 不变式守护:claude-sonnet/opus + thinking.type=enabled + BudgetTokens>0 时,
// 最终 GeminiRequest.GenerationConfig.MaxOutputTokens 必须 > ThinkingConfig.ThinkingBudget(ClaudeBudgetMargin).
func TestTranslateAnthropicToGemini_ClaudeBudgetGuard(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	mt := func(v int) *int { return &v }

	cases := []struct {
		name          string
		model         string
		thinkingType  string
		budgetTokens  int
		maxTokens     *int
		wantBudget    int // 期望 ThinkingConfig.ThinkingBudget;0 表示应无该字段(指针为 nil)
		wantMaxOutput int // 期望 MaxOutputTokens;0 表示 nil
		desc          string
	}{
		{
			name: "claude_sonnet_max_below_budget", model: "claude-sonnet-4-5",
			thinkingType: "enabled", budgetTokens: 8192, maxTokens: mt(2048),
			wantBudget: 8192, wantMaxOutput: 8192 + ClaudeBudgetMargin,
			desc: "Claude Code 走 antigravity 池 claude-sonnet:max_tokens(2048)<budget(8192),守护抬升避免 Vertex 400",
		},
		{
			name: "claude_opus_user_unset_max", model: "claude-opus-4-6",
			thinkingType: "enabled", budgetTokens: 10000, maxTokens: nil,
			wantBudget: 10000, wantMaxOutput: 10000 + ClaudeBudgetMargin,
			desc: "未传 max_tokens:守护补 budget+128,避免 max_tokens<=budget 触发 400",
		},
		{
			name: "claude_sonnet_max_above_margin", model: "claude-sonnet-4-5",
			thinkingType: "enabled", budgetTokens: 8192, maxTokens: mt(20000),
			wantBudget: 8192, wantMaxOutput: 20000,
			desc: "max_tokens(20000) 已 >= budget+128,守护保留用户上限不动",
		},
		{
			name: "claude_thinking_disabled_no_guard", model: "claude-sonnet-4-5",
			thinkingType: "disabled", budgetTokens: 8192, maxTokens: mt(4096),
			wantBudget: 0, wantMaxOutput: 4096,
			desc: "thinking.type=disabled 时不注入 ThinkingBudget,守护无 committedBudget 不动作,max=4096 原样",
		},
		{
			name: "gemini_flash_budget_not_guarded", model: "gemini-3.5-flash",
			thinkingType: "enabled", budgetTokens: 8192, maxTokens: mt(2048),
			wantBudget: 8192, wantMaxOutput: 2048,
			desc: "gemini-3.5-flash 注入 budget:8192 但非 claude,守护不抬升 max(走 Gemini 协议无该约束)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			anth := &AnthropicRequest{
				Model:    c.model,
				Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
				MaxTokens: c.maxTokens,
			}
			if c.thinkingType != "" {
				anth.Thinking = &AnthropicThinking{Type: c.thinkingType, BudgetTokens: c.budgetTokens}
			}
			gem := TranslateAnthropicToGemini(anth)

			if gem.GenerationConfig == nil {
				t.Fatalf("GenerationConfig nil | %s", c.desc)
			}
			if c.wantBudget > 0 {
				if gem.GenerationConfig.ThinkingConfig == nil ||
					gem.GenerationConfig.ThinkingConfig.ThinkingBudget != c.wantBudget {
					got := 0
					if gem.GenerationConfig.ThinkingConfig != nil {
						got = gem.GenerationConfig.ThinkingConfig.ThinkingBudget
					}
					t.Errorf("ThinkingBudget = %d, want %d | %s", got, c.wantBudget, c.desc)
				}
			}
			if c.wantMaxOutput > 0 {
				if gem.GenerationConfig.MaxOutputTokens == nil {
					t.Errorf("MaxOutputTokens nil, want %d | %s", c.wantMaxOutput, c.desc)
				} else if *gem.GenerationConfig.MaxOutputTokens != c.wantMaxOutput {
					t.Errorf("MaxOutputTokens = %d, want %d | %s", *gem.GenerationConfig.MaxOutputTokens, c.wantMaxOutput, c.desc)
				}
			} else if c.wantMaxOutput == 0 && c.maxTokens == nil {
				// 未传 max_tokens 且守护未动作(非 claude / disabled)时,MaxOutputTokens 可能为 nil,允许
			}
		})
	}
}
