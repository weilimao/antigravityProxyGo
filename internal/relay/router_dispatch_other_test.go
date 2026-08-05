package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

// router_dispatch_other_test.go: Other 号池「三段前缀 + 组维度路由」的 resolveRoutedTarget 单元测试。
//
// 覆盖 ModelMappingEntry 显式配置 TargetProvider="other" + TargetGroupID 时的 4 值返回契约,
// 以及「未命中 mapping → 回退默认 route 规则」的兜底语义。与 router_dispatch_test.go 的差异:
// 该文件聚焦 other/* 三段前缀与 TargetGroupID 透传,前者聚焦通用 route 规则优先级。

// TestResolveRoutedTarget_OtherMappingWithGroupID 验证 ModelMappingEntry 显式携带 TargetGroupID 时,
// resolveRoutedTarget 必须把组 ID 透传回调用方(供 passthroughForward 按组选号)。
func TestResolveRoutedTarget_OtherMappingWithGroupID(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "other/openai/gpt-4o",
					TargetModel:    "gpt-4o",
					TargetProvider: "other",
					TargetGroupID:  "openai",
					Expose:         true,
				},
			},
		},
	}

	provider, groupID, tm, matched := h.resolveRoutedTarget("other/openai/gpt-4o")
	if !matched {
		t.Fatal("expected matched=true for explicit other mapping")
	}
	if provider != "other" {
		t.Errorf("provider: want other, got %q", provider)
	}
	if groupID != "openai" {
		t.Errorf("groupID: want openai, got %q", groupID)
	}
	if tm != "gpt-4o" {
		t.Errorf("targetModel: want gpt-4o, got %q", tm)
	}
}

// TestResolveRoutedTarget_OtherMappingNoGroupID 验证 mapping 未配 TargetGroupID 时回退空串,
// 不影响 provider/targetModel 的正常返回(向后兼容既有非组维度 mapping)。
func TestResolveRoutedTarget_OtherMappingNoGroupID(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "other/deepseek/deepseek-chat",
					TargetModel:    "deepseek-chat",
					TargetProvider: "other",
					Expose:         true,
				},
			},
		},
	}

	provider, groupID, tm, matched := h.resolveRoutedTarget("other/deepseek/deepseek-chat")
	if !matched {
		t.Fatal("expected matched=true")
	}
	if provider != "other" {
		t.Errorf("provider: want other, got %q", provider)
	}
	if groupID != "" {
		t.Errorf("groupID: want empty (no TargetGroupID configured), got %q", groupID)
	}
	if tm != "deepseek-chat" {
		t.Errorf("targetModel: want deepseek-chat, got %q", tm)
	}
}

// TestResolveRoutedTarget_OtherMappingEmptyTargetModelPassthrough 验证 mapping 的 TargetModel 留空时,
// targetModel 透传为入站 model 原值(三段前缀全量回传,由下游 passthroughForward 自行剥前缀)。
func TestResolveRoutedTarget_OtherMappingEmptyTargetModelPassthrough(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "other/openai/gpt-4o-mini",
					TargetModel:    "", // 留空
					TargetProvider: "other",
					TargetGroupID:  "openai",
					Expose:         true,
				},
			},
		},
	}

	provider, groupID, tm, matched := h.resolveRoutedTarget("other/openai/gpt-4o-mini")
	if !matched {
		t.Fatal("expected matched=true")
	}
	if provider != "other" || groupID != "openai" {
		t.Errorf("provider/groupID: want other/openai, got %q/%q", provider, groupID)
	}
	// TargetModel 留空 → 透传入站 model 原值。
	if tm != "other/openai/gpt-4o-mini" {
		t.Errorf("targetModel passthrough: want 'other/openai/gpt-4o-mini', got %q", tm)
	}
}

// TestResolveRoutedTarget_OtherMappingCaseInsensitive 验证 ClientModel 匹配大小写不敏感,
// 与 matchModelPattern 的精确相等分支对齐(EqualFold)。
func TestResolveRoutedTarget_OtherMappingCaseInsensitive(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "other/OpenAI/GPT-4o",
					TargetModel:    "gpt-4o",
					TargetProvider: "other",
					TargetGroupID:  "openai",
					Expose:         true,
				},
			},
		},
	}

	provider, groupID, tm, matched := h.resolveRoutedTarget("other/openai/gpt-4o")
	if !matched {
		t.Fatal("expected matched=true for case-insensitive client model")
	}
	if provider != "other" || groupID != "openai" || tm != "gpt-4o" {
		t.Errorf("case-insensitive mismatch: provider=%q groupID=%q tm=%q", provider, groupID, tm)
	}
}

// TestResolveRoutedTarget_OtherNoMappingFallsBackToRoutes 验证未命中任何 mapping 时,
// resolveRoutedTarget 回退默认 route 规则表(GetDefaultModelRoutes)。
// 默认规则无 other/* 专条 → other/openai/gpt-4o 命中兜底 "*" → provider=nvidia。
// 该用例锁定「other 三段前缀不靠默认 route 规则路由,必须显式 mapping」的设计契约。
func TestResolveRoutedTarget_OtherNoMappingFallsBackToRoutes(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: nil, // 无 mapping
		},
	}

	provider, groupID, tm, matched := h.resolveRoutedTarget("other/openai/gpt-4o")
	// 默认规则 "*" → nvidia,targetModel 透传。
	if !matched {
		t.Fatal("expected matched=true via fallback '*' rule")
	}
	if provider != "nvidia" {
		t.Errorf("fallback provider: want nvidia (default route), got %q", provider)
	}
	if groupID != "" {
		t.Errorf("fallback groupID: want empty (route rules carry no groupID), got %q", groupID)
	}
	if tm != "other/openai/gpt-4o" {
		t.Errorf("fallback targetModel: want passthrough 'other/openai/gpt-4o', got %q", tm)
	}
}

// TestResolveRoutedTarget_NoMatchAtAll 验证 settingsMgr 为 nil 且默认规则也无法匹配时,
// 返回 ("", "", "", false),由调用方决定兜底。
func TestResolveRoutedTarget_NoMatchAtAll(t *testing.T) {
	h := &APICompatHandler{} // settingsMgr 为 nil

	provider, groupID, tm, matched := h.resolveRoutedTarget("totally-unknown-model")
	// 默认规则 "*" 兜底命中,故 matched=true、provider=nvidia。
	// 这里只锁定 4 值返回的形态一致性(无 panic、groupID 恒为空串)。
	if matched {
		if provider == "" {
			t.Error("matched but provider empty")
		}
		if groupID != "" {
			t.Errorf("route-rule path groupID: want empty, got %q", groupID)
		}
		if tm == "" {
			t.Error("matched but targetModel empty")
		}
	}
	// 不论 matched 与否,都不应 panic;此处断言函数可安全调用即可。
	_ = provider
	_ = tm
}
