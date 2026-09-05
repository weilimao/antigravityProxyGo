package account

import (
	"testing"
	"time"
)

// account_other_cooldown_test.go: Other 号池组级自定义冷却策略(OtherCooldownRule)的单元测试。
// 覆盖:规则规整(状态码去重/钳域、时长回退、模型去重)、Set/Get 往返、冷却裁决
// GetOtherCooldownDurationMs(状态码命中/模型白名单/前缀通配/多候选模型)、规则清除、
// 冷却写入后选号过滤联动(GetAvailableAccountsForChannelAndGroup)与 GetOtherGroups 回显。

func TestNormalizeOtherCooldownRule(t *testing.T) {
	nr := normalizeOtherCooldownRule(&OtherCooldownRule{
		Enabled:      true,
		StatusCodes:  []int{500, 429, 700, 99, 429, 402},
		CooldownSecs: 0,
		Models:       []string{" DeepSeek-V4 ", "deepseek-v4", "DeepSeek-R1"},
	})
	if nr == nil {
		t.Fatalf("expected non-nil rule")
	}
	// 状态码:去重 + 排序 + 非法域(99/700)剔除。
	if len(nr.StatusCodes) != 3 || nr.StatusCodes[0] != 402 || nr.StatusCodes[1] != 429 || nr.StatusCodes[2] != 500 {
		t.Fatalf("unexpected statusCodes: %v", nr.StatusCodes)
	}
	// 时长:0 回退默认 60。
	if nr.CooldownSecs != DefaultOtherCooldownSecs {
		t.Fatalf("expected default cooldownSecs %d, got %d", DefaultOtherCooldownSecs, nr.CooldownSecs)
	}
	// 模型:大小写不敏感去重,保留首个原始大小写。
	if len(nr.Models) != 2 || nr.Models[0] != "DeepSeek-V4" || nr.Models[1] != "DeepSeek-R1" {
		t.Fatalf("unexpected models: %v", nr.Models)
	}

	// 越界时长钳上限。
	nr2 := normalizeOtherCooldownRule(&OtherCooldownRule{Enabled: true, StatusCodes: []int{429}, CooldownSecs: 99999999})
	if nr2.CooldownSecs != maxOtherCooldownSecs {
		t.Fatalf("expected clamp to %d, got %d", maxOtherCooldownSecs, nr2.CooldownSecs)
	}

	// 无状态码:规则无触发源,模型清空。
	nr3 := normalizeOtherCooldownRule(&OtherCooldownRule{Enabled: true, Models: []string{"m1"}})
	if len(nr3.StatusCodes) != 0 || nr3.Models != nil {
		t.Fatalf("expected empty rule without status codes, got %+v", nr3)
	}

	// nil 入参 → nil。
	if normalizeOtherCooldownRule(nil) != nil {
		t.Fatalf("expected nil for nil input")
	}
}

func TestSetOtherCooldownRule_Match(t *testing.T) {
	m := NewManager()
	saved := m.SetOtherCooldownRule("Bitdeer", OtherCooldownRule{
		Enabled:      true,
		StatusCodes:  []int{429, 402},
		CooldownSecs: 30,
	})
	if !saved.Enabled || saved.CooldownSecs != 30 {
		t.Fatalf("unexpected saved rule: %+v", saved)
	}
	// GroupID 大小写规范化:大写入参 → 小写键,Get 用任意大小写都命中。
	if got := m.GetOtherCooldownDurationMs("bitdeer", 429, "other/deepseek/DeepSeek-V4"); got != 30*1000 {
		t.Fatalf("expected 30000ms for 429, got %d", got)
	}
	if got := m.GetOtherCooldownDurationMs("bitdeer", 402, "whatever"); got != 30*1000 {
		t.Fatalf("expected 30000ms for 402, got %d", got)
	}
	// 未配置的状态码 → 不冷却。
	if got := m.GetOtherCooldownDurationMs("bitdeer", 500, "m"); got != 0 {
		t.Fatalf("expected 0 for unconfigured 500, got %d", got)
	}
	// 未配置的组 → 不冷却。
	if got := m.GetOtherCooldownDurationMs("other-group", 429, "m"); got != 0 {
		t.Fatalf("expected 0 for unconfigured group, got %d", got)
	}

	// 模型白名单:精确匹配(大小写不敏感)+ trailing-* 前缀通配 + 多候选任一命中。
	m.SetOtherCooldownRule("g2", OtherCooldownRule{
		Enabled:      true,
		StatusCodes:  []int{429},
		CooldownSecs: 15,
		Models:       []string{"deepseek-v4", "kimi*"},
	})
	if got := m.GetOtherCooldownDurationMs("g2", 429, "DeepSeek-V4"); got != 15*1000 {
		t.Fatalf("expected match exact model, got %d", got)
	}
	// 候选二(上游模型名)命中前缀通配也算命中。
	if got := m.GetOtherCooldownDurationMs("g2", 429, "other/x/other-name", "kimi-k2-coding"); got != 15*1000 {
		t.Fatalf("expected prefix wildcard match, got %d", got)
	}
	if got := m.GetOtherCooldownDurationMs("g2", 429, "gpt-4o"); got != 0 {
		t.Fatalf("expected 0 for non-whitelisted model, got %d", got)
	}
	// 网络错误无状态码(0)永不命中。
	if got := m.GetOtherCooldownDurationMs("g2", 0, "deepseek-v4"); got != 0 {
		t.Fatalf("expected 0 for network error, got %d", got)
	}

	// 未启用规则 → 不冷却(但配置保留,供回显)。
	m.SetOtherCooldownRule("g3", OtherCooldownRule{Enabled: false, StatusCodes: []int{429}, CooldownSecs: 30})
	if got := m.GetOtherCooldownDurationMs("g3", 429, "m"); got != 0 {
		t.Fatalf("expected 0 for disabled rule, got %d", got)
	}
	if gr := m.GetOtherCooldownRule("g3"); gr.Enabled || len(gr.StatusCodes) != 1 {
		t.Fatalf("expected disabled rule echoed back with config preserved, got %+v", gr)
	}
}

func TestSetOtherCooldownRule_Clear(t *testing.T) {
	m := NewManager()
	m.SetOtherCooldownRule("g1", OtherCooldownRule{Enabled: true, StatusCodes: []int{429}, CooldownSecs: 30})
	if m.GetOtherCooldownDurationMs("g1", 429, "m") == 0 {
		t.Fatalf("expected rule active before clear")
	}
	// 空规则(全零值)= 清除。
	m.SetOtherCooldownRule("g1", OtherCooldownRule{})
	if gr := m.GetOtherCooldownRule("g1"); gr.Enabled || len(gr.StatusCodes) != 0 {
		t.Fatalf("expected rule cleared, got %+v", gr)
	}
	if got := m.GetOtherCooldownDurationMs("g1", 429, "m"); got != 0 {
		t.Fatalf("expected 0 after clear, got %d", got)
	}
}

// TestOtherCooldown_GatesSelection 验证规则写入的冷却与既有选号过滤联动:
// 命中规则的账号被冻结后,组内选号只剩未冻结账号;冷却到期后恢复。
func TestOtherCooldown_GatesSelection(t *testing.T) {
	m := NewManager()
	idA := "other-cool-a"
	idB := "other-cool-b"
	m.accounts = []*Account{
		{ID: idA, Provider: "other", ScopeType: "other", GroupID: "g1", AccessToken: "key-a", BaseURL: "https://a.example.com", Enabled: true, Cooldowns: map[string]int64{}},
		{ID: idB, Provider: "other", ScopeType: "other", GroupID: "g1", AccessToken: "key-b", BaseURL: "https://b.example.com", Enabled: true, Cooldowns: map[string]int64{}},
	}
	m.SetOtherCooldownRule("g1", OtherCooldownRule{Enabled: true, StatusCodes: []int{429}, CooldownSecs: 120})

	// 模拟账号 A 上游 429:转发层调用口径(SetAccountCooldownForChannel)写冷却。
	d := m.GetOtherCooldownDurationMs("g1", 429, "other/g1/model-x")
	if d == 0 {
		t.Fatalf("expected rule to trigger for 429")
	}
	m.SetAccountCooldownForChannel(idA, time.Now().UnixNano()/1e6+d, "other", "other/g1/model-x")

	avail := m.GetAvailableAccountsForChannelAndGroup("other", "g1", "other/g1/model-x")
	if len(avail) != 1 || avail[0].ID != idB {
		t.Fatalf("expected only account B available during cooldown, got %d accounts", len(avail))
	}

	// 未命中规则的错误码(500)不写冷却:账号保持可用。
	if got := m.GetOtherCooldownDurationMs("g1", 500, "other/g1/model-x"); got != 0 {
		t.Fatalf("expected 0 for 500, got %d", got)
	}

	// 冷却到期:直接把 until 拨回过去,选号恢复。
	m.Lock()
	m.accounts[0].Cooldowns["other"] = time.Now().UnixNano()/1e6 - 1
	m.Unlock()
	avail = m.GetAvailableAccountsForChannelAndGroup("other", "g1", "other/g1/model-x")
	if len(avail) != 2 {
		t.Fatalf("expected both accounts available after cooldown expiry, got %d", len(avail))
	}
}

// TestGetOtherGroups_CooldownEcho 验证组聚合信息回显冷却规则(GetOtherGroups → 前端 otherGroups payload)。
func TestGetOtherGroups_CooldownEcho(t *testing.T) {
	m := NewManager()
	m.accounts = []*Account{
		{ID: "echo-a", Provider: "other", ScopeType: "other", GroupID: "bitdeer", GroupName: "Bitdeer", AccessToken: "k1", BaseURL: "https://x.example.com", Enabled: true, Cooldowns: map[string]int64{}},
	}
	m.SetOtherCooldownRule("bitdeer", OtherCooldownRule{Enabled: true, StatusCodes: []int{402, 429}, CooldownSecs: 45, Models: []string{"deepseek*"}})
	groups := m.GetOtherGroups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	cd := groups[0].Cooldown
	if cd == nil || !cd.Enabled || len(cd.StatusCodes) != 2 || cd.StatusCodes[0] != 402 || cd.StatusCodes[1] != 429 || cd.CooldownSecs != 45 || len(cd.Models) != 1 || cd.Models[0] != "deepseek*" {
		t.Fatalf("unexpected cooldown echo: %+v", cd)
	}
}
