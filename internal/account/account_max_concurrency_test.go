package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMaxConcurrency_DefaultFallback 验证 Get 在 0/负数 = 未配置时回退默认 10。
func TestMaxConcurrency_DefaultFallback(t *testing.T) {
	m := NewManager()
	if got := m.GetNvidiaMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("GetNvidiaMaxConcurrency fresh = %d, want %d", got, defaultMaxConcurrency)
	}
	if got := m.GetAntigravityMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("GetAntigravityMaxConcurrency fresh = %d, want %d", got, defaultMaxConcurrency)
	}
	if got := m.GetProjectMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("GetProjectMaxConcurrency fresh = %d, want %d", got, defaultMaxConcurrency)
	}
	if got := m.GetOtherMaxConcurrency("any-group"); got != defaultMaxConcurrency {
		t.Fatalf("GetOtherMaxConcurrency unknown group = %d, want %d", got, defaultMaxConcurrency)
	}

	// 显式 Set 0 后仍回退默认(0 视作未配置,非「关闭限制」)。
	m.SetNvidiaMaxConcurrency(0)
	if got := m.GetNvidiaMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("GetNvidiaMaxConcurrency after Set(0) = %d, want %d", got, defaultMaxConcurrency)
	}
	// 负数 Set 被规整为 0,Get 仍回退默认。
	m.SetAntigravityMaxConcurrency(-5)
	if got := m.GetAntigravityMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("GetAntigravityMaxConcurrency after Set(-5) = %d, want %d", got, defaultMaxConcurrency)
	}
	m.SetOtherMaxConcurrency("OpenAI", -1) // 键应小写规范化
	if got := m.GetOtherMaxConcurrency("openai"); got != defaultMaxConcurrency {
		t.Fatalf("GetOtherMaxConcurrency after Set(-1) normalized lookup = %d, want %d", got, defaultMaxConcurrency)
	}
}

// TestMaxConcurrency_SetGetPersist 验证正数配置读写 + Other map 小写键规范化 + 多组并存。
func TestMaxConcurrency_SetGetPersist(t *testing.T) {
	tmp := t.TempDir()

	m := NewManager()
	m.nvidiaMaxConcurrency = 7
	m.otherMaxConcurrency = map[string]int{"openai": 5, "deepseek": 3}
	// 直接写文件路径再 LoadAccounts 验证往返(Round-Trip)+ 键规范化。
	m.accountsFilePath = filepath.Join(tmp, "accounts.json")
	m.SetNvidiaMaxConcurrency(7)      // 触发 SaveAccounts(false)
	m.SetOtherMaxConcurrency("OpenAI", 5)
	m.SetOtherMaxConcurrency("deepseek", 3)
	m.SetProjectMaxConcurrency(20)

	// 重新装载同一文件验证持久化往返。
	if _, err := os.Stat(m.accountsFilePath); err != nil {
		t.Fatalf("accounts.json not written: %v", err)
	}
	raw, _ := os.ReadFile(m.accountsFilePath)
	var parsed AccountsData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("accounts.json invalid json: %v", err)
	}
	if parsed.NvidiaMaxConcurrency != 7 {
		t.Fatalf("persisted NvidiaMaxConcurrency = %d, want 7", parsed.NvidiaMaxConcurrency)
	}
	if parsed.ProjectMaxConcurrency != 20 {
		t.Fatalf("persisted ProjectMaxConcurrency = %d, want 20", parsed.ProjectMaxConcurrency)
	}
	if got := parsed.OtherMaxConcurrency["openai"]; got != 5 {
		t.Fatalf("persisted OtherMaxConcurrency[openai] = %d, want 5 (lowercase key)", got)
	}
	if got := parsed.OtherMaxConcurrency["deepseek"]; got != 3 {
		t.Fatalf("persisted OtherMaxConcurrency[deepseek] = %d, want 3", got)
	}

	// 独立实例从磁盘载入验证 LoadAccounts 回填内存字段。
	m2 := NewManager()
	m2.accountsFilePath = m.accountsFilePath
	m2.LoadAccounts()
	if got := m2.GetNvidiaMaxConcurrency(); got != 7 {
		t.Fatalf("LoadAccounts GetNvidiaMaxConcurrency = %d, want 7", got)
	}
	if got := m2.GetProjectMaxConcurrency(); got != 20 {
		t.Fatalf("LoadAccounts GetProjectMaxConcurrency = %d, want 20", got)
	}
	if got := m2.GetOtherMaxConcurrency("OPENAI"); got != 5 {
		t.Fatalf("LoadAccounts GetOtherMaxConcurrency(OPENAI) = %d, want 5 (case-insensitive)", got)
	}
	if got := m2.GetOtherMaxConcurrency("deepseek"); got != 3 {
		t.Fatalf("LoadAccounts GetOtherMaxConcurrency(deepseek) = %d, want 3", got)
	}
}

// TestMaxConcurrency_LegacyUpgrade 验证旧 accounts.json(无新字段)载入后 Get 回退默认。
func TestMaxConcurrency_LegacyUpgrade(t *testing.T) {
	tmp := t.TempDir()
	// 写一份仅含旧字段的 JSON(模拟升级前数据)。
	legacy := []byte(`{"accounts":[{"id":"a1","provider":"nvidia","enabled":true,"access_token":"k"}],"poolMode":true,"nvidiaLbMode":"round-robin"}`)
	path := filepath.Join(tmp, "accounts.json")
	if err := os.WriteFile(path, legacy, 0644); err != nil {
		t.Fatalf("write legacy json: %v", err)
	}
	m := NewManager()
	m.accountsFilePath = path
	m.LoadAccounts()
	if got := m.GetNvidiaMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("legacy GetNvidiaMaxConcurrency = %d, want default %d", got, defaultMaxConcurrency)
	}
	if got := m.GetAntigravityMaxConcurrency(); got != defaultMaxConcurrency {
		t.Fatalf("legacy GetAntigravityMaxConcurrency = %d, want default %d", got, defaultMaxConcurrency)
	}
	if got := m.GetOtherMaxConcurrency("openai"); got != defaultMaxConcurrency {
		t.Fatalf("legacy GetOtherMaxConcurrency = %d, want default %d", got, defaultMaxConcurrency)
	}
}

// TestMaxConcurrency_GetOtherGroupsEcho 验证 GetOtherGroups 回显 MaxConcurrency(供前端组 tab 回填)。
func TestMaxConcurrency_GetOtherGroupsEcho(t *testing.T) {
	m := NewManager()
	m.SetOtherMaxConcurrency("openai", 8)
	m.AddAccount(NewOtherAccount(OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 上游", BaseURL: "https://api.openai.com",
		APIKey: "sk-test", Formats: []string{"openai"},
	}))
	groups := m.GetOtherGroups()
	if len(groups) != 1 {
		t.Fatalf("GetOtherGroups len = %d, want 1", len(groups))
	}
	if groups[0].MaxConcurrency != 8 {
		t.Fatalf("GetOtherGroups[0].MaxConcurrency = %d, want 8", groups[0].MaxConcurrency)
	}
	// 未配置组(Set(0))回显应回退默认 10,与选号热路径 GetOtherMaxConcurrency 同口径、
	// 与 NVIDIA/Antigravity/Project 三池 emitter 回退范式对齐(不再直出 0)。
	m.SetOtherMaxConcurrency("openai", 0)
	for _, g := range m.GetOtherGroups() {
		if g.GroupID == "openai" && g.MaxConcurrency != defaultMaxConcurrency {
			t.Fatalf("GetOtherGroups openai after Set(0) echo = %d, want %d (未配置回退默认)", g.MaxConcurrency, defaultMaxConcurrency)
		}
	}
}

// TestMaxConcurrency_FilterAndLeastLoaded 验证 Manager 转发方法接线正确(端到端用到选号链路的语义)。
func TestMaxConcurrency_FilterAndLeastLoaded(t *testing.T) {
	m := NewManager()
	accs := []*Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	// a 占 2,b 占 1,c 空闲;limit=2 → 仅 c(count 0 < 2)入选,b 被 1<2 误判?
	// b count=1 < 2 仍入选,故结果 [b,c]。a count=2 不 < 2 被排除。
	m.AcquireAccount("a")
	m.AcquireAccount("a")
	m.AcquireAccount("b")
	got := m.FilterByConcurrency(accs, 2)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("FilterByConcurrency = %v, want [b c]", accIDs(got))
	}
	// 全满(limit=1,所有 count>=1 的除外)→ 仅 c(0<1)入选;LeastLoaded 取 c(0,最少)。
	filtLimit1 := m.FilterByConcurrency(accs, 1)
	if len(filtLimit1) != 1 || filtLimit1[0].ID != "c" {
		t.Fatalf("FilterByConcurrency limit=1 = %v, want [c]", accIDs(filtLimit1))
	}
	if got := m.LeastLoadedAccount(accs); got == nil || got.ID != "c" {
		t.Fatalf("LeastLoadedAccount = %v, want c (least in-flight)", accIDPtr(got))
	}
	// 计数查询只读。
	if got := m.AccountInFlightCount("a"); got != 2 {
		t.Fatalf("AccountInFlightCount(a) = %d, want 2", got)
	}
	// 释放后归零即删键。
	m.ReleaseAccount("a")
	m.ReleaseAccount("a")
	if got := m.AccountInFlightCount("a"); got != 0 {
		t.Fatalf("AccountInFlightCount(a) after release = %d, want 0", got)
	}
}

func accIDs(accs []*Account) []string {
	out := make([]string, 0, len(accs))
	for _, a := range accs {
		out = append(out, a.ID)
	}
	return out
}

func accIDPtr(a *Account) string {
	if a == nil {
		return "<nil>"
	}
	return a.ID
}
