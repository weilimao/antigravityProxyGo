package account

import (
	"os"
	"path/filepath"
	"testing"
)

// account_import_id_test.go: 针对「多账号共用同一 ID 导致前端只渲染一张卡片」缺陷的修复回归。
// 覆盖三条路径:
//  1. ImportAccountsList 批量导入多个不同邮箱账号 → 每个账号 ID 唯一,互不碰撞。
//  2. LoadAccounts 读取历史遗留含重复 ID 的 accounts.json → 自动为重复 ID 重新分配唯一值。
//  3. AddAccount 显式传入与池内已有账号相同 ID 的账号 → 自动重新分配,不顶掉既有账号。
//
// 与 account_grok_test / account_other_test 的「显式唯一 ID 绕开 old generateAccountID 撞号」
// 注释形成呼应:此处锁定的就是该既有缺陷已被修复。

// writeAccountsFile 在 dir 下写一个 accounts.json(内容为 raw JSON 字符串),供 LoadAccounts 回读。
func writeAccountsFile(t *testing.T, dir, raw string) {
	t.Helper()
	path := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatalf("write accounts.json: %v", err)
	}
}

// TestImportAccountsList_UniqueIDs 验证批量导入多个不同邮箱账号后,池内每个账号 ID 两两不同。
// 修复前:旧 generateAccountID 在同纳秒连调时生成相同 ID,导致前端 querySelector 只命中首张卡片。
func TestImportAccountsList_UniqueIDs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_import_id_test_*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// 构造 4 个不同邮箱的 Grok 账号(模拟用户 grok_xai_import.json 导入场景)。
	// 全部带空 ID,完全依赖 ImportAccountsList 内部分配。
	emails := []string{
		"tmp8ictzgjoum@nexusquantum.cloud",
		"tmpgogic1qa9k@nexusquantum.cloud",
		"tmphjbdo0ud3h@nexusquantum.cloud",
		"tmpuiw15vmnzt@nexusquantum.cloud",
	}
	var in []*Account
	for _, e := range emails {
		in = append(in, &Account{
			ID:          "", // 空 ID,触发内部分配
			Email:       e,
			Provider:    "grok",
			ScopeType:   "grok",
			AccessToken: "xai-token-" + e,
			BaseURL:     DefaultGrokBaseURL,
			Enabled:     true,
			Cooldowns:   map[string]int64{},
		})
	}

	added := m.ImportAccountsList(in)
	if added != len(emails) {
		t.Fatalf("ImportAccountsList added = %d, want %d", added, len(emails))
	}

	accs := m.GetAccounts()
	// 校验 ID 全局唯一。
	seen := make(map[string]struct{}, len(accs))
	for _, a := range accs {
		if a.ID == "" {
			t.Errorf("account %q got empty ID after import", a.Email)
			continue
		}
		if _, dup := seen[a.ID]; dup {
			t.Errorf("duplicate ID %q (account %q) —— 修复后每个账号 ID 必须唯一", a.ID, a.Email)
		}
		seen[a.ID] = struct{}{}
	}
	// 校验 4 个邮箱全部在池。
	gotEmails := make(map[string]struct{}, len(accs))
	for _, a := range accs {
		gotEmails[a.Email] = struct{}{}
	}
	for _, want := range emails {
		if _, ok := gotEmails[want]; !ok {
			t.Errorf("imported account %q missing from pool after import", want)
		}
	}
}

// TestImportAccountsList_PreassignedDuplicateIDs 验证:即便导入 JSON 自带的 ID 非唯一
// (历史脏数据 / 外部工具生成),ImportAccountsList 也必须重写为唯一 ID,避免渲染丢号。
func TestImportAccountsList_PreassignedDuplicateIDs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_import_predup_*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	// 两个账号显式带相同 ID(模拟历史脏数据)。
	const sharedID = "1786327931810559000-59000"
	in := []*Account{
		{ID: sharedID, Email: "dup-a@x.ai", Provider: "grok", ScopeType: "grok", AccessToken: "t-a", BaseURL: DefaultGrokBaseURL, Enabled: true, Cooldowns: map[string]int64{}},
		{ID: sharedID, Email: "dup-b@x.ai", Provider: "grok", ScopeType: "grok", AccessToken: "t-b", BaseURL: DefaultGrokBaseURL, Enabled: true, Cooldowns: map[string]int64{}},
	}

	added := m.ImportAccountsList(in)
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	accs := m.GetAccounts()
	var ids []string
	for _, a := range accs {
		if a.Email == "dup-a@x.ai" || a.Email == "dup-b@x.ai" {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 imported accounts, got %d", len(ids))
	}
	if ids[0] == ids[1] {
		t.Fatalf("两个导入账号仍共用同一 ID %q —— ImportAccountsList 必须重写重复 ID", ids[0])
	}
}

// TestLoadAccounts_RepairsHistoricalDuplicateIDs 验证 LoadAccounts 读取含历史重复 ID 的
// accounts.json 时,自动为「除首遇外」的重复 ID 账号重新分配唯一 ID。
func TestLoadAccounts_RepairsHistoricalDuplicateIDs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_load_dup_*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 4 个不同邮箱的 Grok 账号,但全部共用同一历史脏 ID(复刻用户现场 accounts.json 中的实际状况)。
	const dirtyID = "1786327931810559000-59000"
	raw := `{
	  "accounts": [
	    {"id":"` + dirtyID + `","email":"tmp8ictzgjoum@nexusquantum.cloud","provider":"grok","scopeType":"grok","access_token":"ta","baseUrl":"https://api.x.ai/v1","enabled":true,"cooldowns":{}},
	    {"id":"` + dirtyID + `","email":"tmpgogic1qa9k@nexusquantum.cloud","provider":"grok","scopeType":"grok","access_token":"tb","baseUrl":"https://api.x.ai/v1","enabled":true,"cooldowns":{}},
	    {"id":"` + dirtyID + `","email":"tmphjbdo0ud3h@nexusquantum.cloud","provider":"grok","scopeType":"grok","access_token":"tc","baseUrl":"https://api.x.ai/v1","enabled":true,"cooldowns":{}},
	    {"id":"` + dirtyID + `","email":"tmpuiw15vmnzt@nexusquantum.cloud","provider":"grok","scopeType":"grok","access_token":"td","baseUrl":"https://api.x.ai/v1","enabled":true,"cooldowns":{}}
	  ]
	}`
	writeAccountsFile(t, tempDir, raw)

	m := NewManager()
	m.Init(tempDir) // LoadAccounts 在 Init 内触发

	accs := m.GetAccounts()
	if len(accs) != 4 {
		t.Fatalf("expected 4 accounts after LoadAccounts repair, got %d", len(accs))
	}
	seen := make(map[string]struct{}, len(accs))
	for _, a := range accs {
		if a.ID == "" {
			t.Errorf("account %q still has empty ID after LoadAccounts", a.Email)
			continue
		}
		if a.ID == dirtyID {
			// 允许首遇保留 dirtyID,但必须只有一个账号持有它。
			if _, dup := seen[dirtyID]; dup {
				t.Errorf("dirty ID %q 仍被多个账号共用 —— LoadAccounts 必须去重", dirtyID)
			}
		}
		if _, dup := seen[a.ID]; dup {
			t.Errorf("duplicate ID %q (account %q) survived LoadAccounts —— 必须重新分配唯一 ID", a.ID, a.Email)
		}
		seen[a.ID] = struct{}{}
	}

	// 4 个邮箱全部保留(去重 ID 不应丢账号)。
	wantEmails := map[string]bool{
		"tmp8ictzgjoum@nexusquantum.cloud": false,
		"tmpgogic1qa9k@nexusquantum.cloud": false,
		"tmphjbdo0ud3h@nexusquantum.cloud": false,
		"tmpuiw15vmnzt@nexusquantum.cloud": false,
	}
	for _, a := range accs {
		if _, ok := wantEmails[a.Email]; ok {
			wantEmails[a.Email] = true
		}
	}
	for e, found := range wantEmails {
		if !found {
			t.Errorf("account %q dropped during LoadAccounts repair", e)
		}
	}
}

// TestLoadAccounts_RepairsDuplicateIDs_Across2FA 验证 2FA 列表内同样会被去重 ID。
func TestLoadAccounts_RepairsDuplicateIDs_Across2FA(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_2fa_dup_*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	const dirtyID = "two-shared-999"
	raw := `{
	  "accounts": [
	    {"id":"a1","email":"pool@x.ai","provider":"antigravity","enabled":true,"cooldowns":{}}
	  ],
	  "twofa_accounts": [
	    {"id":"` + dirtyID + `","email":"2fa-a@x.ai","twoFA_secret":"S1","provider":"2fa","scopeType":"2fa","enabled":false,"cooldowns":{}},
	    {"id":"` + dirtyID + `","email":"2fa-b@x.ai","twoFA_secret":"S2","provider":"2fa","scopeType":"2fa","enabled":false,"cooldowns":{}}
	  ]
	}`
	writeAccountsFile(t, tempDir, raw)

	m := NewManager()
	m.Init(tempDir)

	twofa := m.GetTwoFAAccounts()
	if len(twofa) != 2 {
		t.Fatalf("expected 2 twofa accounts, got %d", len(twofa))
	}
	seen := make(map[string]struct{}, len(twofa))
	for _, a := range twofa {
		if a.ID == "" {
			t.Errorf("2fa account %q empty ID", a.Email)
			continue
		}
		if _, dup := seen[a.ID]; dup {
			t.Errorf("2fa duplicate ID %q survived LoadAccounts (%q)", a.ID, a.Email)
		}
		seen[a.ID] = struct{}{}
	}
}

// TestAddAccount_RegeneratesDuplicateID 验证 AddAccount 显式传入与池内已有账号相同 ID 时,
// 会重新分配唯一 ID,而不是让两个账号共用同一 ID。
func TestAddAccount_RegeneratesDuplicateID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "account_add_dup_*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager()
	m.Init(tempDir)

	first := &Account{ID: "shared-add-id", Email: "a@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}}
	m.AddAccount(first)

	second := &Account{ID: "shared-add-id", Email: "b@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}}
	m.AddAccount(second)

	accs := m.GetAccounts()
	var ids []string
	for _, a := range accs {
		if a.Email == "a@x.ai" || a.Email == "b@x.ai" {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(ids))
	}
	if ids[0] == ids[1] {
		t.Fatalf("AddAccount allowed duplicate ID %q to persist across two accounts", ids[0])
	}
	// 第一个账号的 ID 应保持不变(首遇保留语义)。
	var firstID string
	for _, a := range accs {
		if a.Email == "a@x.ai" {
			firstID = a.ID
		}
	}
	if firstID != "shared-add-id" {
		t.Errorf("first account ID changed to %q, want preserved 'shared-add-id'", firstID)
	}
}

// TestGenerateAccountID_StronglyUnique 连续生成大量 ID,验证进程内不碰撞。
// 旧实现因纳秒同值 + %100000 在连调下极易碰撞;新实现纳秒⊕序号⊕epoch 应全程唯一。
func TestGenerateAccountID_StronglyUnique(t *testing.T) {
	m := NewManager()
	const n = 5000
	seen := make(map[string]struct{}, n)
	// generateAccountID 期望在持锁临界区内调用;这里单独测试其唯一性,持锁后调用符合契约。
	m.Lock()
	for i := 0; i < n; i++ {
		id := m.generateAccountID()
		if _, dup := seen[id]; dup {
			m.Unlock()
			t.Fatalf("generateAccountID collision at i=%d on id=%q", i, id)
		}
		seen[id] = struct{}{}
	}
	m.Unlock()
}
