package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

func TestMatchModelPattern(t *testing.T) {
	cases := []struct {
		pattern, model string
		want           bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"deepseek-*", "deepseek-chat", true},
		{"deepseek-*", "deepseek-ai/deepseek-r1", true},
		{"deepseek-*", "gpt-4o", false},
		{"nvidia/*", "nvidia/llama-3.1-nemotron-70b", true},
		{"nvidia/*", "deepseek-chat", false},
		{"deepseek-chat", "deepseek-chat", true},
		{"DeepSeek-Chat", "deepseek-chat", true},   // 大小写不敏感精确
		{"deepseek-chat", "deepseek-chat-v2", false},
		{"regexp:^ds-.*", "ds-reasoner", true},
		{"regexp:^ds-.*", "deepseek-chat", false},
		{"", "any", false}, // 空 pattern 不命中
	}
	for _, c := range cases {
		got := matchModelPattern(c.pattern, c.model)
		if got != c.want {
			t.Errorf("matchModelPattern(%q,%q) = %v, want %v", c.pattern, c.model, got, c.want)
		}
	}
}

func TestRouteMatch_PriorityOrder(t *testing.T) {
	rules := []settings.ModelRouteRule{
		{Pattern: "*", TargetProvider: "nvidia", Priority: 0, Enabled: true},
		{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true},
		{Pattern: "deepseek-chat", TargetProvider: "deepseek-official", Priority: 200, Enabled: true},
		{Pattern: "disabled-*", TargetProvider: "deepseek", Priority: 999, Enabled: false},
	}

	// 精确高优先级规则先命中。
	r := routeMatch(rules, "deepseek-chat")
	if r == nil || r.TargetProvider != "deepseek-official" {
		t.Fatalf("expected deepseek-official for deepseek-chat, got %+v", r)
	}
	// 前缀规则次优先命中。
	r = routeMatch(rules, "deepseek-reasoner")
	if r == nil || r.TargetProvider != "deepseek" || r.Pattern != "deepseek-*" {
		t.Fatalf("expected deepseek-* for deepseek-reasoner, got %+v", r)
	}
	// 兜底 "*" 命中。
	r = routeMatch(rules, "gpt-4o")
	if r == nil || r.TargetProvider != "nvidia" {
		t.Fatalf("expected nvidia fallback for gpt-4o, got %+v", r)
	}
	// disabled 规则即便优先级最高也不命中。
	r = routeMatch(rules, "disabled-x")
	if r == nil || r.TargetProvider == "deepseek" {
		t.Fatalf("disabled rule must not match, got %+v", r)
	}
}

func TestRouteMatch_EmptyRules(t *testing.T) {
	if r := routeMatch(nil, "deepseek-chat"); r != nil {
		t.Errorf("empty rules should return nil, got %+v", r)
	}
}

func TestResolveRoutedTarget_TargetModelPassthrough(t *testing.T) {
	// 用一个固定 rules 的 handler(不依赖 settingsMgr)验证 TargetModel 透传语义。
	h := &APICompatHandler{}
	// settingsMgr 为 nil → resolveRoutedTarget 走默认规则表(GetDefaultModelRoutes)。
	// 默认规则: nvidia/* -> nvidia; * -> nvidia。无 deepseek 规则 → 全部命中 nvidia。
	// 这里只验证「命中即返回规则表指定 provider、TargetModel 空则原样透传」的契约。

	// nvidia/* 命中:TargetModel 为空 → 透传入站 model。
	provider, tm, matched := h.resolveRoutedTarget("nvidia/llama-3.1-nemotron-70b")
	if !matched || provider != "nvidia" || tm != "nvidia/llama-3.1-nemotron-70b" {
		t.Errorf("nvidia/* passthrough mismatch: provider=%q tm=%q matched=%v", provider, tm, matched)
	}

	// 兜底 "*" 命中。
	provider, tm, matched = h.resolveRoutedTarget("gpt-4o")
	if !matched || provider != "nvidia" || tm != "gpt-4o" {
		t.Errorf("fallback mismatch: provider=%q tm=%q matched=%v", provider, tm, matched)
	}
}
