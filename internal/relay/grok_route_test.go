package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

// grok_route_test.go: 锁定 Grok 号池在默认路由表(GetDefaultModelRoutes)下的入站模型分发契约。
//
// 默认路由表(settings_routes.go:GetDefaultModelRoutes):
//   - nvidia/*  → provider=nvidia,  Priority=50
//   - grok/*    → provider=grok,    Priority=50
//   - xai/*     → provider=grok,    Priority=50   (/xai 是 /grok 的别名命名空间)
//   - *         → provider=nvidia,  Priority=0    (顶层兜底,向后兼容)
//
// resolveRoutedTarget 返回 4 值:(targetProvider, targetGroupID, targetModel, matched)。
// 命中规则且 rule.TargetModel 为空时,targetModel 原样透传入站 model(与 NVIDIA 兜底语义一致)。
// 本测试用 settingsMgr==nil 的裸 handler(zero APICompatHandler),使 resolveRoutedTarget
// 走 GetDefaultModelRoutes 默认规则表,精确锁定 grok/xai 别名两条路径与 nvidia 兜底路径的分发。

// newBareHandler 构造一个 settingsMgr==nil 的 handler,让 resolveRoutedTarget 回退默认规则表。
// (与 router_dispatch_test.go 的 TestResolveRoutedTarget_TargetModelPassthrough 同款装配。)
func newBareHandler() *APICompatHandler {
	return &APICompatHandler{}
}

// TestResolveRoutedTarget_GrokNamespaceHitsGrokProvider 锁定 grok/* 命名空间上游模型
// 走 grok 号池(provider=grok),TargetModel 空则原样透传入站 model。
func TestResolveRoutedTarget_GrokNamespaceHitsGrokProvider(t *testing.T) {
	h := newBareHandler()
	cases := []struct {
		model string
		want  string
	}{
		{"grok/grok-4.3", "grok/grok-4.3"},
		{"grok/grok-4-fast", "grok/grok-4-fast"},
		{"grok/grok-2", "grok/grok-2"},
		{"grok/GROK-4.3", "grok/GROK-4.3"}, // 大小写不敏感(pattern match 用 EqualFold/regex 大小写不敏感)
	}
	for _, c := range cases {
		provider, _, tm, matched := h.resolveRoutedTarget(c.model)
		if !matched {
			t.Errorf("model %q 应命中(默认 grok/* 规则), got matched=false", c.model)
		}
		if provider != "grok" {
			t.Errorf("model %q: provider=%q want grok", c.model, provider)
		}
		if tm != c.want {
			t.Errorf("model %q: targetModel=%q want %q(空 TargetModel 透传)", c.model, tm, c.want)
		}
	}
}

// TestResolveRoutedTarget_XAIAliasNamespaceHitsGrokProvider 锁定 /xai 别名命名空间
// 同样走 grok 号池(provider=grok),与 grok/* 等精度(对等 settings_routes.go 的别名设计)。
func TestResolveRoutedTarget_XAIAliasNamespaceHitsGrokProvider(t *testing.T) {
	h := newBareHandler()
	cases := []string{"xai/grok-4", "xai/grok-4-fast", "xai/grok-3"}
	for _, m := range cases {
		provider, _, tm, matched := h.resolveRoutedTarget(m)
		if !matched || provider != "grok" {
			t.Errorf("xai 别名 %q: provider=%q matched=%v, want grok", m, provider, matched)
		}
		if tm != m {
			t.Errorf("xai 别名 %q: targetModel=%q want %q(透传)", m, tm, m)
		}
	}
}

// TestResolveRoutedTarget_NvidiaNamespaceHitsNvidiaProvider 锁定 nvidia/* 命名空间
// 仍走 nvidia 号池(provider=nvidia),不被 grok/xai 规则误吞(号池物理隔离回归保证)。
func TestResolveRoutedTarget_NvidiaNamespaceHitsNvidiaProvider(t *testing.T) {
	h := newBareHandler()
	provider, _, tm, matched := h.resolveRoutedTarget("nvidia/llama-3.1-nemotron-70b")
	if !matched || provider != "nvidia" {
		t.Fatalf("nvidia/llama: provider=%q matched=%v, want nvidia", provider, matched)
	}
	if tm != "nvidia/llama-3.1-nemotron-70b" {
		t.Errorf("nvidia/llama: targetModel=%q want 透传", tm)
	}
}

// TestResolveRoutedTarget_FallbackWildcardHitsNvidia 锁定顶层兜底通配 "*"
// 把未命中具名命名空间(grok/* / xai/* / nvidia/*)的模型丢给 nvidia 号池(向后兼容,与原 /nvidia 行为对齐)。
func TestResolveRoutedTarget_FallbackWildcardHitsNvidia(t *testing.T) {
	h := newBareHandler()
	cases := []string{"deepseek-chat", "gpt-4o", "claude-3-5-sonnet", "anything-else"}
	for _, m := range cases {
		provider, _, tm, matched := h.resolveRoutedTarget(m)
		if !matched || provider != "nvidia" {
			t.Errorf("兜底 %q: provider=%q matched=%v, want nvidia", m, provider, matched)
		}
		if tm != m {
			t.Errorf("兜底 %q: targetModel=%q want 透传", m, tm)
		}
	}
}

// TestResolveRoutedTarget_PriorityOrder_GrokBeatsFallback 锁定优先级:
// grok/* (Priority=50) 与 xai/* (Priority=50) 命中优于顶层 "*" (Priority=0)。
// 即 grok/anything 不会因兜底通配被误路由到 nvidia。
func TestResolveRoutedTarget_PriorityOrder_GrokBeatsFallback(t *testing.T) {
	h := newBareHandler()
	// grok/ 开头应命中 grok/* (50) 而非 "*" (0)
	provider, _, _, matched := h.resolveRoutedTarget("grok/anything")
	if !matched || provider != "grok" {
		t.Fatalf("grok/anything 应命中 grok/* (P50) 而非兜底 * (P0), got provider=%q matched=%v", provider, matched)
	}
}

// TestGetDefaultModelRoutes_ContainsGrokAndXAIRules 锁定默认路由表显式包含
// grok/* 与 xai/* 两条 grok 指向规则(防止后续误删导致 Grok 路由回归)。
func TestGetDefaultModelRoutes_ContainsGrokAndXAIRules(t *testing.T) {
	rules := settings.GetDefaultModelRoutes()
	var hasGrok, hasXAI, hasNvidia, hasFallback bool
	for _, r := range rules {
		switch r.Pattern {
		case "grok/*":
			hasGrok = true
			if r.TargetProvider != "grok" || !r.Enabled {
				t.Errorf("grok/* 规则: provider=%q enabled=%v, want grok/enabled", r.TargetProvider, r.Enabled)
			}
		case "xai/*":
			hasXAI = true
			if r.TargetProvider != "grok" || !r.Enabled {
				t.Errorf("xai/* 规则: provider=%q enabled=%v, want grok/enabled", r.TargetProvider, r.Enabled)
			}
		case "nvidia/*":
			hasNvidia = true
		case "*":
			hasFallback = true
		}
	}
	if !hasGrok {
		t.Error("默认路由表缺 grok/* → grok 规则")
	}
	if !hasXAI {
		t.Error("默认路由表缺 xai/* → grok 别名规则")
	}
	if !hasNvidia {
		t.Error("默认路由表缺 nvidia/* → nvidia 规则")
	}
	if !hasFallback {
		t.Error("默认路由表缺 * → nvidia 兜底规则")
	}
}

// TestRouteMatch_GrokPatternsInIsolation 锁定 routeMatch 对 grok/* 与 xai/* 的命中口径,
// 与 matchModelPattern 的通配锚定语义对齐(^grok/.*$),排除 /grokfoo 等误吞。
func TestRouteMatch_GrokPatternsInIsolation(t *testing.T) {
	rules := settings.GetDefaultModelRoutes()
	var grokRule, xaiRule *settings.ModelRouteRule
	for i := range rules {
		switch rules[i].Pattern {
		case "grok/*":
			grokRule = &rules[i]
		case "xai/*":
			xaiRule = &rules[i]
		}
	}
	if grokRule == nil || xaiRule == nil {
		t.Fatalf("默认路由表未取到 grok/*/xai* 规则")
	}
	// grok/* 命中
	if r := routeMatch(rules, "grok/grok-4.3"); r == nil || r.Pattern != "grok/*" {
		t.Errorf("grok/grok-4.3 应命中 grok/*, got %+v", r)
	}
	if r := routeMatch(rules, "xai/grok-4"); r == nil || r.Pattern != "xai/*" {
		t.Errorf("xai/grok-4 应命中 xai/*, got %+v", r)
	}
	// 兜底 * 命中非 grok/xai 命名空间
	if r := routeMatch(rules, "deepseek-chat"); r == nil || r.Pattern != "*" {
		t.Errorf("deepseek-chat 应命中兜底 *, got %+v", r)
	}
}
