package account

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestManager_WorkBuddyCliVersionRaw(t *testing.T) {
	m := NewManager()
	// 未配置时返回空串(不回退默认值),便于中继按账号区域选择官方默认版本
	if got := m.GetWorkBuddyCliVersionRaw(); got != "" {
		t.Errorf("未配置时应返回空串, 实际 %q", got)
	}

	var nilMgr *Manager
	if got := nilMgr.GetWorkBuddyCliVersionRaw(); got != "" {
		t.Errorf("nil manager 应返回空串, 实际 %q", got)
	}

	m.SetWorkBuddyCliVersion("  5.8.2  ")
	if got := m.GetWorkBuddyCliVersionRaw(); got != "5.8.2" {
		t.Errorf("设置后应返回 TrimSpace 后的原文, 实际 %q", got)
	}

	// 空白配置等同未配置
	m.SetWorkBuddyCliVersion("   ")
	if got := m.GetWorkBuddyCliVersionRaw(); got != "" {
		t.Errorf("空白配置应返回空串, 实际 %q", got)
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

func TestParseUpstreamVersion(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"5.8.3.41334564", "5.8.3"},
		{"v5.8.3.41334564", "5.8.3"},
		{"5.8.3", "5.8.3"},
		{"v5.8.3", "5.8.3"},
		{"5.9.0-rc1.123", "5.9.0-rc1"},
		{"", ""},
		{"   ", ""},
	}

	for _, c := range cases {
		got := parseUpstreamVersion(c.input)
		if got != c.want {
			t.Errorf("parseUpstreamVersion(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFetchVersionFromUpstream_Mock(t *testing.T) {
	// 保存原配置并在测试结束时还原 (Teardown 环境恢复)
	origURL := workbuddyUpstreamUpdateURL
	origClient := workbuddyUpstreamHTTPClient
	defer func() {
		workbuddyUpstreamUpdateURL = origURL
		workbuddyUpstreamHTTPClient = origClient
	}()

	workbuddyUpstreamHTTPClient = &http.Client{Timeout: 1 * time.Second}

	// 1. 模拟正常 200 返回四位版本
	serverOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"productVersion":"5.8.4.12345678","version":"5.8.4.12345678"}`)
	}))
	defer serverOK.Close()

	workbuddyUpstreamUpdateURL = serverOK.URL
	gotOK := fetchVersionFromUpstream()
	if gotOK != "5.8.4" {
		t.Errorf("fetchVersionFromUpstream (OK) = %q, want 5.8.4", gotOK)
	}

	// 2. 模拟 204 No Content
	serverNoContent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer serverNoContent.Close()

	workbuddyUpstreamUpdateURL = serverNoContent.URL
	gotNoContent := fetchVersionFromUpstream()
	if gotNoContent != "" {
		t.Errorf("fetchVersionFromUpstream (204) = %q, want empty", gotNoContent)
	}

	// 3. 模拟 500 异常
	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer serverErr.Close()

	workbuddyUpstreamUpdateURL = serverErr.URL
	gotErr := fetchVersionFromUpstream()
	if gotErr != "" {
		t.Errorf("fetchVersionFromUpstream (500) = %q, want empty", gotErr)
	}
}
