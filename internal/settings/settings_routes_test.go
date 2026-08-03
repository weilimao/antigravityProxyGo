package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRelayModelRoutes_Retention 覆盖路由规则表的默认值、Set/Get 往返、去残项、落盘与重载一致性。
func TestRelayModelRoutes_Retention(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-routes-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认应回退 GetDefaultModelRoutes(非空)。
	got := mgr.GetRelayModelRoutes()
	if len(got) == 0 {
		t.Fatalf("expected default routes fallback, got empty")
	}

	// 2. Set 自定义规则 → Get 一致;残项(空 Pattern / 空 Provider)被剔除。
	want := []ModelRouteRule{
		{Pattern: "deepseek-*", TargetProvider: "deepseek", TargetModel: "", Priority: 100, Enabled: true},
		{Pattern: "", TargetProvider: "deepseek", Enabled: true},           // 空 Pattern → 被剔
		{Pattern: "gpt-*", TargetProvider: "", Enabled: true},              // 空 Provider → 被剔
		{Pattern: "nvidia/*", TargetProvider: "nvidia", Priority: 50, Enabled: true},
	}
	if err := mgr.SetRelayModelRoutes(want); err != nil {
		t.Fatalf("SetRelayModelRoutes failed: %v", err)
	}
	got = mgr.GetRelayModelRoutes()
	expected := []ModelRouteRule{
		{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true},
		{Pattern: "nvidia/*", TargetProvider: "nvidia", Priority: 50, Enabled: true},
	}
	if len(got) != len(expected) {
		t.Fatalf("expected %d rules after cleanup, got %d: %+v", len(expected), len(got), got)
	}
	for i, e := range expected {
		if got[i].Pattern != e.Pattern || got[i].TargetProvider != e.TargetProvider || got[i].Priority != e.Priority {
			t.Errorf("rule[%d] mismatch: got %+v, want %+v", i, got[i], e)
		}
	}

	// 3. 返回副本,外部改写不影响内存态。
	got[0].Pattern = "tampered"
	again := mgr.GetRelayModelRoutes()
	if again[0].Pattern == "tampered" {
		t.Errorf("GetRelayModelRoutes should return a defensive copy")
	}

	// 4. 落盘 + 重载一致。
	configPath := filepath.Join(tempDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config.json persisted: %v", err)
	}
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	reloaded := mgr2.GetRelayModelRoutes()
	if len(reloaded) != len(expected) {
		t.Fatalf("reloaded routes count mismatch: got %d, want %d", len(reloaded), len(expected))
	}
	for i, e := range expected {
		if reloaded[i].Pattern != e.Pattern || reloaded[i].TargetProvider != e.TargetProvider {
			t.Errorf("reloaded rule[%d] mismatch: got %+v, want %+v", i, reloaded[i], e)
		}
	}

	// 5. 清空规则表 → Get 应回退默认规则(非空)。
	if err := mgr2.SetRelayModelRoutes([]ModelRouteRule{}); err != nil {
		t.Fatalf("clear routes failed: %v", err)
	}
	mgr3 := NewManager()
	mgr3.Init(tempDir)
	empty := mgr3.GetRelayModelRoutes()
	if len(empty) == 0 {
		t.Fatalf("expected default routes fallback after clear, got empty")
	}
}

// TestGetDefaultModelRoutes_Shape 锁定默认规则表的形状(nvidia 兜底两条)。
func TestGetDefaultModelRoutes_Shape(t *testing.T) {
	r := GetDefaultModelRoutes()
	if len(r) < 2 {
		t.Fatalf("expected at least 2 default routes, got %d", len(r))
	}
	// 应包含 nvidia/* 前缀规则与顶层 "*" 兜底。
	hasNvidiaPrefix := false
	hasStarFallback := false
	for _, rule := range r {
		if rule.Pattern == "nvidia/*" && rule.TargetProvider == "nvidia" && !rule.Enabled == false {
			hasNvidiaPrefix = true
		}
		if rule.Pattern == "*" && rule.TargetProvider == "nvidia" {
			hasStarFallback = true
		}
	}
	if !hasNvidiaPrefix {
		t.Errorf("default routes should include nvidia/* prefix rule")
	}
	if !hasStarFallback {
		t.Errorf("default routes should include * fallback rule")
	}
}
