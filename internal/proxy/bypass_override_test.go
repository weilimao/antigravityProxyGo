package proxy

import (
	"strings"
	"testing"

	"antigravity-proxy/internal/settings"
)

// TestShouldBypassOverride_DefaultTabPrefixes 锁定默认 ["tab"] 白名单下,
// Tab 补全模型被放行、推理模型仍被覆写的行为。shouldBypassOverride 是
// handler.go 全局覆写的前缀绕过判定,提取到 helpers.go 便于复用与单测。
func TestShouldBypassOverride_DefaultTabPrefixes(t *testing.T) {
	defaults := []string{"tab"}

	cases := []struct {
		model string
		want  bool
		why   string
	}{
		{"tab_flash_lite_preview", true, "Tab 补全模型应被绕过,避免改向推理上游 400"},
		{"tab_jump_flash_lite_preview", true, "jump lite 同属 Tab 系,应被绕过"},
		{"models/tab_flash_lite_preview", true, "带 models/ 前缀也应绕过(去前缀后小写化)"},
		{"TAB_Flash_Lite_Preview", true, "大小写不敏感:客户端可能传大写"},
		{"gemini-3-flash", false, "推理模型不应被绕过"},
		{"gemini-3.6-flash-high", false, "推理模型不应被绕过"},
		{"claude-sonnet-4-6", false, "claude 推理模型不应被绕过"},
		{"antigravity-core", false, "未知/泛模型名不应被绕过"},
		{"gpt-oss-120b-medium", false, "非 tab 前缀不应被绕过"},
	}
	for _, c := range cases {
		got := shouldBypassOverride(c.model, defaults)
		if got != c.want {
			t.Errorf("shouldBypassOverride(%q, defaults) = %v, want %v (%s)", c.model, got, c.want, c.why)
		}
	}
}

// TestShouldBypassOverride_CustomPrefixes 锁定自定义多前缀白名单下的命中行为。
func TestShouldBypassOverride_CustomPrefixes(t *testing.T) {
	prefixes := []string{"tab", "jump", "claude-"}

	cases := []struct {
		model string
		want  bool
	}{
		{"tab_flash_lite_preview", true},
		{"jump_anything", true},
		{"claude-sonnet-4-6", true},
		{"Claude-Opus-4-6", true}, // 大小写不敏感
		{"gemini-3-flash", false},
		{"gpt-oss-120b-medium", false},
	}
	for _, c := range cases {
		got := shouldBypassOverride(c.model, prefixes)
		if got != c.want {
			t.Errorf("shouldBypassOverride(%q, %v) = %v, want %v", c.model, prefixes, got, c.want)
		}
	}
}

// TestShouldBypassOverride_EmptyPrefixes 锁定白名单为空时一律不绕过(全部覆写)。
func TestShouldBypassOverride_EmptyPrefixes(t *testing.T) {
	if shouldBypassOverride("tab_flash_lite_preview", nil) {
		t.Errorf("nil prefix list should not bypass tab model")
	}
	if shouldBypassOverride("tab_flash_lite_preview", []string{}) {
		t.Errorf("empty prefix slice should not bypass tab model")
	}
	// 白名单只含空串/纯空白项,等价于无有效前缀,不应绕过
	if shouldBypassOverride("tab_flash_lite_preview", []string{"", "  "}) {
		t.Errorf("whitespace-only prefixes should not bypass tab model")
	}
	// 前缀本身带空白也应 trim 后比较
	if !shouldBypassOverride("tab_x", []string{"  tab  "}) {
		t.Errorf("trimmed prefix 'tab' should bypass 'tab_x'")
	}
}

// TestShouldBypassOverride_SettingsMgrIntegration 验证与 settings.Manager 的
// Set/Get 往返一致,保证前端配置 → 后端落盘 → handler 判定的端到端闭环。
func TestShouldBypassOverride_SettingsMgrIntegration(t *testing.T) {
	mgr := settings.NewManager()
	// SetBypassOverridePrefixes 内部去空去重(大小写不敏感),与 handler 判定同源。
	prefixes := []string{"tab", "Tab", "", "claude-"}
	if err := mgr.SetBypassOverridePrefixes(prefixes); err != nil {
		t.Fatalf("SetBypassOverridePrefixes failed: %v", err)
	}
	got := mgr.GetBypassOverridePrefixes()
	if len(got) != 2 || got[0] != "tab" || got[1] != "claude-" {
		t.Fatalf("Expected [tab, claude-] after dedup, got %v", got)
	}
	if !shouldBypassOverride("tab_flash_lite_preview", got) {
		t.Errorf("Manager-backed prefixes should bypass tab model")
	}
	if !shouldBypassOverride("claude-sonnet-4-6", got) {
		t.Errorf("Manager-backed prefixes should bypass claude model")
	}
	if shouldBypassOverride("gemini-3-flash", got) {
		t.Errorf("Manager-backed prefixes should NOT bypass gemini model")
	}
}

// 确保 strings 包被使用(避免未使用导入在严格构建下报错)。
var _ = strings.HasPrefix
