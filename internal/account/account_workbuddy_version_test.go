package account

import (
	"testing"
)

func TestManager_WorkBuddyCliVersion_Default(t *testing.T) {
	m := NewManager()
	// 默认未配置时应回退 DefaultWorkBuddyDomesticVersion ("5.7.7")
	if got := m.GetWorkBuddyCliVersion(); got != DefaultWorkBuddyDomesticVersion {
		t.Errorf("default WorkBuddy CLI version: want %q, got %q", DefaultWorkBuddyDomesticVersion, got)
	}

	var nilMgr *Manager
	if got := nilMgr.GetWorkBuddyCliVersion(); got != DefaultWorkBuddyDomesticVersion {
		t.Errorf("nil manager: want %q, got %q", DefaultWorkBuddyDomesticVersion, got)
	}
}

func TestManager_WorkBuddyCliVersion_SetAndTrim(t *testing.T) {
	m := NewManager()

	// 1. 设置具体版本
	m.SetWorkBuddyCliVersion("5.7.8")
	if got := m.GetWorkBuddyCliVersion(); got != "5.7.8" {
		t.Errorf("after set: want 5.7.8, got %q", got)
	}

	// 2. 空白字符串回退默认值
	m.SetWorkBuddyCliVersion("   ")
	if got := m.GetWorkBuddyCliVersion(); got != DefaultWorkBuddyDomesticVersion {
		t.Errorf("blank should fall back to default %q, got %q", DefaultWorkBuddyDomesticVersion, got)
	}

	// 3. TrimSpace 规整
	m.SetWorkBuddyCliVersion("  5.7.9-rc1  ")
	if got := m.GetWorkBuddyCliVersion(); got != "5.7.9-rc1" {
		t.Errorf("TrimSpace: want 5.7.9-rc1, got %q", got)
	}
}

func TestManager_WorkBuddyCliVersion_Persistence(t *testing.T) {
	tempDir := t.TempDir()

	m1 := NewManager()
	m1.Init(tempDir)
	m1.SetWorkBuddyCliVersion("5.8.0")

	// 重新载入验证持久化
	m2 := NewManager()
	m2.Init(tempDir)
	if got := m2.GetWorkBuddyCliVersion(); got != "5.8.0" {
		t.Errorf("persisted version: want 5.8.0, got %q", got)
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{"5.7.7", "5.7.6", 1},
		{"5.7.6", "5.7.7", -1},
		{"5.7.7", "5.7.7", 0},
		{"v5.7.7", "5.7.7", 0},
		{"5.7.10", "5.7.9", 1},
		{"5.8.0", "5.7.99", 1},
		{"", "5.7.7", -1},
		{"5.7.7", "", 1},
	}

	for _, tt := range tests {
		got := compareSemver(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestDetectWorkBuddyLatestVersion(t *testing.T) {
	ver := DetectWorkBuddyLatestVersion()
	if ver == "" {
		t.Fatalf("DetectWorkBuddyLatestVersion returned empty version")
	}
	// 探测结果应该至少大于等于 5.7.7 (当前官方国内最新基线)
	if compareSemver(ver, "5.7.7") < 0 {
		t.Errorf("detected version %q is lower than expected baseline 5.7.7", ver)
	}
}

func TestBuildWorkBuddyHeaders_CustomAndDefaultVersions(t *testing.T) {
	accDom := &Account{
		Provider: "workbuddy",
		BaseURL:  "https://copilot.tencent.com",
	}
	accIntl := &Account{
		Provider: "workbuddy",
		BaseURL:  "https://www.workbuddy.ai",
	}

	// 1. 默认版本
	hDom := BuildWorkBuddyHeaders(accDom)
	if hDom["X-IDE-Version"] != "5.7.7" || hDom["User-Agent"] != "WorkBuddy/5.7.7" {
		t.Errorf("domestic default headers mismatch: X-IDE-Version=%s, UA=%s", hDom["X-IDE-Version"], hDom["User-Agent"])
	}

	hIntl := BuildWorkBuddyHeaders(accIntl)
	if hIntl["X-IDE-Version"] != "5.7.6" || hIntl["User-Agent"] != "WorkBuddy/5.7.6" {
		t.Errorf("international default headers mismatch: X-IDE-Version=%s, UA=%s", hIntl["X-IDE-Version"], hIntl["User-Agent"])
	}

	// 2. 自定义版本覆盖
	hDomCustom := BuildWorkBuddyHeaders(accDom, "5.8.1")
	if hDomCustom["X-IDE-Version"] != "5.8.1" || hDomCustom["User-Agent"] != "WorkBuddy/5.8.1" {
		t.Errorf("domestic custom headers mismatch: X-IDE-Version=%s, UA=%s", hDomCustom["X-IDE-Version"], hDomCustom["User-Agent"])
	}

	hIntlCustom := BuildWorkBuddyHeaders(accIntl, "5.8.1")
	if hIntlCustom["X-IDE-Version"] != "5.8.1" || hIntlCustom["User-Agent"] != "WorkBuddy/5.8.1" {
		t.Errorf("international custom headers mismatch: X-IDE-Version=%s, UA=%s", hIntlCustom["X-IDE-Version"], hIntlCustom["User-Agent"])
	}
}
