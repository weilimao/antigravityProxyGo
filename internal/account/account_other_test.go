package account

import (
	"strings"
	"testing"
)

// account_other_test.go: Other 号池(自定义多上游组)的构造、校验、组级管理与选号单元测试。
// 覆盖:ValidateOtherAccountInput 边界、NewOtherAccount 字段映射、normalizeOtherFormats 排序、
// AddOtherAccount 组内同 Key 去重、GetOtherGroups 聚合、GetAvailableAccountsForChannelAndGroup 组维度选号。

// validInput 是一个最小可用的 Other 账号录入样例,各 case 在其上微调以覆盖分支。
func validOtherInput() OtherAccountInput {
	return OtherAccountInput{
		GroupID:   "openai",
		GroupName: "OpenAI 上游组",
		BaseURL:   "https://api.openai.com/v1",
		APIKey:    "sk-test-key-001",
		Formats:   []string{"openai"},
	}
}

func TestValidateOtherAccountInput_OK(t *testing.T) {
	if err := ValidateOtherAccountInput(validOtherInput()); err != nil {
		t.Fatalf("expected nil error for valid input, got: %v", err)
	}
	// 多选格式 also OK。
	in := validOtherInput()
	in.Formats = []string{"openai", "anthropic"}
	if err := ValidateOtherAccountInput(in); err != nil {
		t.Fatalf("expected nil error for multi-format input, got: %v", err)
	}
	// GroupName 留空合法(回退 GroupID)。
	in2 := validOtherInput()
	in2.GroupName = ""
	if err := ValidateOtherAccountInput(in2); err != nil {
		t.Fatalf("expected nil error when groupName empty, got: %v", err)
	}
	// BaseURL 带尾斜杠也合法(构造时会 trim)。
	in3 := validOtherInput()
	in3.BaseURL = "https://api.openai.com/v1/"
	if err := ValidateOtherAccountInput(in3); err != nil {
		t.Fatalf("expected nil error for baseURL with trailing slash, got: %v", err)
	}
	// BaseURL 允许内网 http(非 SSL 明文上游端点)。
	in4 := validOtherInput()
	in4.BaseURL = "http://112.124.3.174:55555/"
	if err := ValidateOtherAccountInput(in4); err != nil {
		t.Fatalf("expected nil error for http baseURL, got: %v", err)
	}
}

func TestValidateOtherAccountInput_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(in OtherAccountInput) OtherAccountInput
		want string
	}{
		{"空 groupId", func(in OtherAccountInput) OtherAccountInput { in.GroupID = ""; return in }, "groupId 不能为空"},
		{"保留 groupId", func(in OtherAccountInput) OtherAccountInput { in.GroupID = "nvidia"; return in }, "冲突"},
		{"保留 groupId other", func(in OtherAccountInput) OtherAccountInput { in.GroupID = "other"; return in }, "冲突"},
		{"groupId 含非法字符", func(in OtherAccountInput) OtherAccountInput { in.GroupID = "OpenAI!"; return in }, "仅允许小写字母"},
		{"groupId 含空格", func(in OtherAccountInput) OtherAccountInput { in.GroupID = "open ai"; return in }, "仅允许小写字母"},
		{"空 baseURL", func(in OtherAccountInput) OtherAccountInput { in.BaseURL = ""; return in }, "baseUrl 不能为空"},
		{"非法 scheme baseURL", func(in OtherAccountInput) OtherAccountInput { in.BaseURL = "ftp://api.openai.com/v1"; return in }, "http"},
		{"非法 baseURL", func(in OtherAccountInput) OtherAccountInput { in.BaseURL = "://missing-scheme"; return in }, "http"},
		{"空 apiKey", func(in OtherAccountInput) OtherAccountInput { in.APIKey = ""; return in }, "apiKey 不能为空"},
		{"空 formats", func(in OtherAccountInput) OtherAccountInput { in.Formats = nil; return in }, "formats 至少勾选"},
		{"非法 format", func(in OtherAccountInput) OtherAccountInput { in.Formats = []string{"gemini"}; return in }, "仅支持 openai/anthropic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateOtherAccountInput(c.mut(validOtherInput()))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain expected substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestValidateOtherAccountInput_GroupIDNormalization(t *testing.T) {
	// 大写 + 首尾空格 → 规范化后应判保留冲突(说明规范化生效)。
	in := validOtherInput()
	in.GroupID = "  NVIDIA  "
	err := ValidateOtherAccountInput(in)
	if err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("expected reserve conflict after normalization, got: %v", err)
	}
	// 大写非保留 → 规范化后合法。
	in2 := validOtherInput()
	in2.GroupID = "MyRelay"
	if err := ValidateOtherAccountInput(in2); err != nil {
		t.Fatalf("expected nil error for uppercase non-reserve groupId, got: %v", err)
	}
}

func TestNormalizeOtherFormats(t *testing.T) {
	// 去重 + 大小写规范化 + 稳定排序(openai 在前)。
	got := normalizeOtherFormats([]string{"Anthropic", "openai", "OPENAI", " anthropic "})
	if len(got) != 2 {
		t.Fatalf("expected 2 formats after dedup, got %v", got)
	}
	if got[0] != "openai" || got[1] != "anthropic" {
		t.Fatalf("expected [openai, anthropic] stable order, got %v", got)
	}
	// 单格式不额外排序。
	got2 := normalizeOtherFormats([]string{"anthropic"})
	if len(got2) != 1 || got2[0] != "anthropic" {
		t.Fatalf("expected [anthropic], got %v", got2)
	}
	// 空入参返回空切片。
	got3 := normalizeOtherFormats(nil)
	if len(got3) != 0 {
		t.Fatalf("expected empty slice for nil input, got %v", got3)
	}
}

func TestNewOtherAccount_Fields(t *testing.T) {
	in := validOtherInput()
	in.Label = ""
	in.DefaultModel = "gpt-4o-mini"
	acc := NewOtherAccount(in)

	if acc.Provider != "other" {
		t.Errorf("Provider: want other, got %q", acc.Provider)
	}
	if acc.ScopeType != "other" {
		t.Errorf("ScopeType: want other, got %q", acc.ScopeType)
	}
	if acc.GroupID != "openai" {
		t.Errorf("GroupID: want openai, got %q", acc.GroupID)
	}
	if acc.GroupName != "OpenAI 上游组" {
		t.Errorf("GroupName: want OpenAI 上游组, got %q", acc.GroupName)
	}
	if acc.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL: want trimmed https://api.openai.com/v1, got %q", acc.BaseURL)
	}
	if acc.AccessToken != "sk-test-key-001" {
		t.Errorf("AccessToken: want sk-test-key-001, got %q", acc.AccessToken)
	}
	if !acc.Enabled {
		t.Errorf("Enabled: want true")
	}
	if len(acc.Formats) != 1 || acc.Formats[0] != "openai" {
		t.Errorf("Formats: want [openai], got %v", acc.Formats)
	}
	if acc.Cooldowns == nil {
		t.Errorf("Cooldowns: want non-nil map")
	}
	// Label 留空 → 回退 "{GroupName}-{defaultModel}-{Key前6位}"。
	if !strings.Contains(acc.Email, "OpenAI 上游组") || !strings.Contains(acc.Email, "gpt-4o-mini") {
		t.Errorf("Email(展示名)回退: want contains GroupName+defaultModel, got %q", acc.Email)
	}
}

func TestNewOtherAccount_GroupNameFallback(t *testing.T) {
	// GroupName 留空 → 回退 GroupID;Label 留空且无 DefaultModel → 回退 "{GroupID}-{Key前6位}"。
	in := validOtherInput()
	in.GroupID = "deepseek"
	in.GroupName = ""
	in.Label = ""
	in.DefaultModel = ""
	acc := NewOtherAccount(in)
	if acc.GroupName != "deepseek" {
		t.Errorf("GroupName fallback: want deepseek, got %q", acc.GroupName)
	}
	if !strings.HasPrefix(acc.Email, "deepseek-") {
		t.Errorf("Email fallback: want prefix 'deepseek-', got %q", acc.Email)
	}
}

func TestManager_AddOtherAccount_Dedup(t *testing.T) {
	m := NewManager()
	in := validOtherInput()

	_, err := m.AddOtherAccount(in)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	// 同 GroupID + 同 APIKey → 拒绝(组内同 Key 去重)。
	_, err = m.AddOtherAccount(in)
	if err == nil || !strings.Contains(err.Error(), "已存在相同 APIKey") {
		t.Fatalf("expected dup-key rejection, got: %v", err)
	}

	// 同 GroupID + 不同 APIKey → 允许(组内多账号)。
	// 注意:NewOtherAccount 的 label 回退取 Key 前 6 位,AddAccount 又按 Email 去重,
	// 故两个 Key 的前 6 位必须不同,否则会被 AddAccount 视为同号覆盖。这里用 sk-open/sk-deep 区分。
	in2 := in
	in2.APIKey = "sk-deepseek-key"
	if _, err := m.AddOtherAccount(in2); err != nil {
		t.Fatalf("second add with different key: %v", err)
	}

	// 落库语义校验:该组应有 2 个账号,且 Key 互不相同(不依赖时间戳 ID 的唯一性,
	// 避开 generateAccountID 在 Windows 同纳秒打码撞 ID 的既有缺陷干扰)。
	accs := m.GetEnabledOtherAccounts(in.GroupID)
	if len(accs) != 2 {
		t.Fatalf("expected 2 enabled accounts in group, got %d", len(accs))
	}
	keys := map[string]bool{}
	for _, a := range accs {
		keys[a.GetAccessToken()] = true
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 distinct APIKeys in group, got %d: %v", len(keys), keys)
	}

	// 组级汇总应反映 AccountCount==2 / EnabledCount==2。
	groups := m.GetOtherGroups()
	if len(groups) != 1 || groups[0].AccountCount != 2 || groups[0].EnabledCount != 2 {
		t.Fatalf("expected 1 group {count:2, enabled:2}, got %+v", groups)
	}
}

// TestManager_AddOtherAccount_LabelCollision 揭示 label 回退取 Key 前 6 位的边界:
// 两个不同 Key 若前 6 位相同(如 sk-test-key-001 / sk-test-key-002),AddAccount 的 Email 去重
// 会把第二个视作同号覆盖。该测试锁定该既有行为,提示前端录入时尽量填 Label 或用前缀区分的 Key。
func TestManager_AddOtherAccount_LabelCollision(t *testing.T) {
	m := NewManager()
	in := validOtherInput()
	in.APIKey = "sk-test-key-001"
	if _, err := m.AddOtherAccount(in); err != nil {
		t.Fatalf("first add: %v", err)
	}
	in2 := in
	in2.APIKey = "sk-test-key-002" // 前 6 位与第一个相同 → label 撞 → 被 AddAccount 覆盖
	if _, err := m.AddOtherAccount(in2); err != nil {
		t.Fatalf("second add (label collision): %v", err)
	}
	accs := m.GetEnabledOtherAccounts(in.GroupID)
	if len(accs) != 1 {
		t.Fatalf("expected 1 account after label-collision overwrite, got %d", len(accs))
	}
}

func TestManager_GetOtherGroups(t *testing.T) {
	m := NewManager()
	// openai 组:2 账号(1 启用 1 停用)
	_, _ = m.AddOtherAccount(OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 组", BaseURL: "https://api.openai.com/v1",
		APIKey: "k1", Formats: []string{"openai"},
	})
	id2, _ := m.AddOtherAccount(OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 组", BaseURL: "https://api.openai.com/v1",
		APIKey: "k2", Formats: []string{"openai"},
	})
	m.UpdateAccountEnabled(id2, false)
	// deepseek 组:1 账号
	_, _ = m.AddOtherAccount(OtherAccountInput{
		GroupID: "deepseek", GroupName: "DeepSeek 组", BaseURL: "https://api.deepseek.com/v1",
		APIKey: "d1", Formats: []string{"openai", "anthropic"},
	})

	groups := m.GetOtherGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(groups), groups)
	}
	// 顺序按首次出现:openai 在前。
	if groups[0].GroupID != "openai" {
		t.Errorf("first group: want openai, got %q", groups[0].GroupID)
	}
	if groups[0].AccountCount != 2 {
		t.Errorf("openai AccountCount: want 2, got %d", groups[0].AccountCount)
	}
	if groups[0].EnabledCount != 1 {
		t.Errorf("openai EnabledCount: want 1, got %d", groups[0].EnabledCount)
	}
	if groups[1].GroupID != "deepseek" {
		t.Errorf("second group: want deepseek, got %q", groups[1].GroupID)
	}
	// 组 Formats(openai 优先 anthropic)。
	if len(groups[1].Formats) != 2 || groups[1].Formats[0] != "openai" || groups[1].Formats[1] != "anthropic" {
		t.Errorf("deepseek Formats: want [openai anthropic], got %v", groups[1].Formats)
	}
}

func TestManager_GetAvailableAccountsForChannelAndGroup_Other(t *testing.T) {
	m := NewManager()
	_, _ = m.AddOtherAccount(OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 组", BaseURL: "https://api.openai.com/v1",
		APIKey: "k1", Formats: []string{"openai"},
	})
	_, _ = m.AddOtherAccount(OtherAccountInput{
		GroupID: "deepseek", GroupName: "DeepSeek 组", BaseURL: "https://api.deepseek.com/v1",
		APIKey: "d1", Formats: []string{"openai"},
	})

	// 只选 openai 组。
	got := m.GetAvailableAccountsForChannelAndGroup("other", "openai", "other/openai/gpt-4o")
	if len(got) != 1 {
		t.Fatalf("expected 1 account in openai group, got %d", len(got))
	}
	if got[0].GroupID != "openai" {
		t.Errorf("selected acc group: want openai, got %q", got[0].GroupID)
	}

	// groupID 为空 → 不过滤,选所有 other 账号(向后兼容)。
	gotAll := m.GetAvailableAccountsForChannelAndGroup("other", "", "")
	if len(gotAll) != 2 {
		t.Fatalf("expected 2 accounts with empty groupID, got %d", len(gotAll))
	}

	// 不存在的组 → 空。
	gotNone := m.GetAvailableAccountsForChannelAndGroup("other", "nope", "")
	if len(gotNone) != 0 {
		t.Fatalf("expected 0 accounts for unknown group, got %d", len(gotNone))
	}

	// 非 other channel → 不该返回 other 账号。
	gotNv := m.GetAvailableAccountsForChannelAndGroup("nvidia", "openai", "")
	if len(gotNv) != 0 {
		t.Fatalf("expected 0 other accounts under nvidia channel, got %d", len(gotNv))
	}
}

func TestManager_OtherLBMode(t *testing.T) {
	m := NewManager()
	// 默认 round-robin。
	if got := m.GetOtherLBMode("openai"); got != "round-robin" {
		t.Errorf("default LB mode: want round-robin, got %q", got)
	}
	// 设置 sticky。
	m.SetOtherLBMode("openai", "sticky")
	if got := m.GetOtherLBMode("openai"); got != "sticky" {
		t.Errorf("after set: want sticky, got %q", got)
	}
	// 空模式回退默认。
	m.SetOtherLBMode("openai", "   ")
	if got := m.GetOtherLBMode("openai"); got != "round-robin" {
		t.Errorf("after empty set: want round-robin, got %q", got)
	}
	// 组 ID 大小写归一化。
	m.SetOtherLBMode("OpenAI", "sticky")
	if got := m.GetOtherLBMode("openai"); got != "sticky" {
		t.Errorf("case-insensitive groupID: want sticky, got %q", got)
	}
}

func TestManager_GetOtherGroupFormats(t *testing.T) {
	m := NewManager()
	_, _ = m.AddOtherAccount(OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 组", BaseURL: "https://api.openai.com/v1",
		APIKey: "k1", Formats: []string{"openai", "anthropic"},
	})
	got := m.GetOtherGroupFormats("openai")
	if len(got) != 2 || got[0] != "openai" || got[1] != "anthropic" {
		t.Errorf("GetOtherGroupFormats: want [openai anthropic], got %v", got)
	}
	// 未知组返回 nil。
	if got := m.GetOtherGroupFormats("nope"); got != nil {
		t.Errorf("GetOtherGroupFormats unknown: want nil, got %v", got)
	}
}
