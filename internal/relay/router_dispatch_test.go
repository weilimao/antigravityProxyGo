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
		{"DeepSeek-Chat", "deepseek-chat", true}, // 大小写不敏感精确
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
	// resolveRoutedTarget 返回 4 值:(targetProvider, targetGroupID, targetModel, matched)。

	// nvidia/* 命中:TargetModel 为空 → 透传入站 model。
	provider, _, tm, matched := h.resolveRoutedTarget("nvidia/llama-3.1-nemotron-70b")
	if !matched || provider != "nvidia" || tm != "nvidia/llama-3.1-nemotron-70b" {
		t.Errorf("nvidia/* passthrough mismatch: provider=%q tm=%q matched=%v", provider, tm, matched)
	}

	// 兜底 "*" 命中。
	provider, _, tm, matched = h.resolveRoutedTarget("gpt-4o")
	if !matched || provider != "nvidia" || tm != "gpt-4o" {
		t.Errorf("fallback mismatch: provider=%q tm=%q matched=%v", provider, tm, matched)
	}
}

type stubMappingSettings struct {
	settings.ManagerInterface
	mappings []settings.ModelMappingEntry
}

func (s *stubMappingSettings) GetRelayModelMapping() []settings.ModelMappingEntry {
	return s.mappings
}

func (s *stubMappingSettings) GetRelayModelRoutes() []settings.ModelRouteRule {
	return nil
}

func TestResolveRoutedTarget_WithModelMappingTargetProvider(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "my-custom-ds",
					TargetModel:    "deepseek-chat",
					TargetProvider: "deepseek",
					Expose:         true,
				},
			},
		},
	}

	provider, _, tm, matched := h.resolveRoutedTarget("my-custom-ds")
	if !matched || provider != "deepseek" || tm != "deepseek-chat" {
		t.Fatalf("expected deepseek / deepseek-chat for my-custom-ds, got provider=%q tm=%q matched=%v", provider, tm, matched)
	}
}

type benchEntrySettings struct {
	settings.ManagerInterface
	mappings []settings.ModelMappingEntry
	routes   []settings.ModelRouteRule
}

func (s *benchEntrySettings) GetRelayModelMapping() []settings.ModelMappingEntry {
	return s.mappings
}

func (s *benchEntrySettings) GetRelayModelRoutes() []settings.ModelRouteRule {
	return s.routes
}

func TestResolveBenchmarkEntryPath(t *testing.T) {
	stub := &benchEntrySettings{
		mappings: []settings.ModelMappingEntry{
			{ClientModel: "nvidia/moonshotai/kimi-k3", TargetModel: "moonshotai/kimi-k3", Expose: true},
			{ClientModel: "gemini-2.5-flash", TargetModel: "gemini-2.5-flash", Expose: true},
			{ClientModel: "other/openai/gpt-4o", TargetModel: "gpt-4o", Expose: true},
		},
		routes: settings.GetDefaultModelRoutes(),
	}
	h := &APICompatHandler{settingsMgr: stub}

	cases := []struct {
		model string
		want  string
	}{
		// 非 Google 命名空间前缀 → /route(与真实客户端 BaseURL=/route 同口径)
		{"nvidia/moonshotai/kimi-k3", "/route/v1/chat/completions"},
		{"nvidia/deepseek-ai/deepseek-r1", "/route/v1/chat/completions"},
		{"grok/grok-3", "/route/v1/chat/completions"},
		{"xai/grok-3-mini", "/route/v1/chat/completions"},
		{"other/openai/gpt-4o", "/route/v1/chat/completions"},
		{"deepseek/deepseek-chat", "/route/v1/chat/completions"},
		// Google 名即使被默认兜底通配 * → nvidia 误命中, 也不得走 /route
		{"gemini-2.5-flash", "/v1/chat/completions"},
		{"gemini-3-flash", "/v1/chat/completions"},
		{"unknown-model", "/v1/chat/completions"},
		{"", "/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := h.ResolveBenchmarkEntryPath(c.model); got != c.want {
			t.Errorf("ResolveBenchmarkEntryPath(%q) = %q, want %q", c.model, got, c.want)
		}
	}

	// 映射显式声明 Google Provider 时以映射为准(即使模型名带非 Google 前缀)
	prov := &benchEntrySettings{
		mappings: []settings.ModelMappingEntry{
			{ClientModel: "nvidia/gemini-x", TargetModel: "gemini-x", TargetProvider: "antigravity", Expose: true},
		},
		routes: settings.GetDefaultModelRoutes(),
	}
	h2 := &APICompatHandler{settingsMgr: prov}
	if got := h2.ResolveBenchmarkEntryPath("nvidia/gemini-x"); got != "/v1/chat/completions" {
		t.Errorf("explicit google provider: got %q, want /v1/chat/completions", got)
	}

	// 映射显式声明非 Google Provider → /route
	h3 := &APICompatHandler{settingsMgr: &benchEntrySettings{
		mappings: []settings.ModelMappingEntry{
			{ClientModel: "my-custom-ds", TargetModel: "deepseek-chat", TargetProvider: "deepseek", Expose: true},
		},
		routes: settings.GetDefaultModelRoutes(),
	}}
	if got := h3.ResolveBenchmarkEntryPath("my-custom-ds"); got != "/route/v1/chat/completions" {
		t.Errorf("explicit non-google provider: got %q, want /route/v1/chat/completions", got)
	}

	// settingsMgr 为 nil(单测/退化态): 前缀与默认规则仍应正确判向
	h4 := &APICompatHandler{}
	if got := h4.ResolveBenchmarkEntryPath("nvidia/moonshotai/kimi-k3"); got != "/route/v1/chat/completions" {
		t.Errorf("nil settingsMgr nvidia prefix: got %q, want /route/v1/chat/completions", got)
	}
	if got := h4.ResolveBenchmarkEntryPath("gemini-2.5-flash"); got != "/v1/chat/completions" {
		t.Errorf("nil settingsMgr gemini: got %q, want /v1/chat/completions", got)
	}
}
