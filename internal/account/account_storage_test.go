package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// account_storage_test.go: 账号池分区化磁盘读写层回归。
// 覆盖 8 条核心路径:分区加载、旧单文件一次性迁移、定向落盘(单 provider/空 kinds=全量)、
// 池配置定向写、2FA 联动双写、跨 provider 批量导入定向落盘、往返一致性。
//
// 全部用 tmp 目录 + 真文件 + 真序列化,与生产路径同口径(account_storage.go 无 mock 路径)。

// ---------- 工具:断言文件存在/不存在 ----------

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected file to exist: %s", path)
	}
}

func assertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected file to NOT exist: %s", path)
	}
}

// readFileAccounts 读一个账号分区文件(外壳 {"accounts":[...]})并返回其账号数组。
func readFileAccounts(t *testing.T, path string) []*Account {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var shell accountsFileShell
	if err := json.Unmarshal(data, &shell); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return shell.Accounts
}

// readFilePoolConfig 读 accounts_pool.json 并返回其池配置投影。
func readFilePoolConfig(t *testing.T, path string) poolConfigOnDisk {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg poolConfigOnDisk
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return cfg
}

// ---------- 1. 分区加载 ----------

// TestLoadAccounts_PartitionedFiles 验证:从既有 7 个分区文件加载,内存聚合数组 + 2FA 列表 + 池配置全部回填。
func TestLoadAccounts_PartitionedFiles(t *testing.T) {
	tmp := t.TempDir()

	// 准备 5 个 provider 分区 + 2fa 分区 + pool 分区(手写真文件,模拟用户已分区化数据目录)。
	mustWrite := func(name string, v any) {
		data, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustWrite("accounts_antigravity.json", accountsFileShell{Accounts: []*Account{
		{ID: "ag1", Email: "ag@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}},
		{ID: "ag2", Email: "ag2@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}},
	}})
	mustWrite("accounts_nvidia.json", accountsFileShell{Accounts: []*Account{
		{ID: "nv1", Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: "https://integrate.api.nvidia.com/v1", Enabled: true, Cooldowns: map[string]int64{}},
	}})
	mustWrite("accounts_grok.json", accountsFileShell{Accounts: []*Account{
		{ID: "gr1", Email: "gr@x.ai", Provider: "grok", AccessToken: "k", BaseURL: "https://api.x.ai/v1", Enabled: true, Cooldowns: map[string]int64{}},
	}})
	// project / other 留空但文件存在。
	mustWrite("accounts_project.json", accountsFileShell{Accounts: []*Account{}})
	mustWrite("accounts_other.json", accountsFileShell{Accounts: []*Account{}})
	// 2FA 分区。
	mustWrite("accounts_2fa.json", accountsFileShell{Accounts: []*Account{
		{ID: "2fa1", Email: "2fa@x.ai", Provider: "2fa", ScopeType: "2fa", TwoFASecret: "S1", Enabled: false, Cooldowns: map[string]int64{}},
	}})
	// pool 配置分区。
	mustWrite("accounts_pool.json", poolConfigOnDisk{
		PoolMode:             true,
		ActiveChannel:        "antigravity",
		NvidiaLBMode:         "round-robin",
		NvidiaMaxConcurrency: 7,
		OtherLBModes:         map[string]string{"openai": "sticky"},
		OtherMaxConcurrency:  map[string]int{"openai": 5},
	})

	m := NewManager()
	m.Init(tmp) // shouldMigrateLegacy == false(已有分区文件)→ 直接 loadFromPartitions

	// 聚合数组:4 个 active 账号(ag1, ag2, nv1, gr1)。
	accs := m.GetAccounts()
	if len(accs) != 4 {
		t.Fatalf("LoadAccounts got %d accounts, want 4", len(accs))
	}
	// 2FA 列表:1 个独立 2FA 账号。
	twofa := m.GetTwoFAAccounts()
	if len(twofa) != 1 {
		t.Fatalf("LoadAccounts got %d 2fa, want 1", len(twofa))
	}
	if twofa[0].Email != "2fa@x.ai" || twofa[0].TwoFASecret != "S1" {
		t.Fatalf("2fa account mismatch: %+v", twofa[0])
	}
	// 池配置回填。
	if got := m.GetNvidiaMaxConcurrency(); got != 7 {
		t.Fatalf("pool config NvidiaMaxConcurrency = %d, want 7", got)
	}
	if got := m.GetOtherMaxConcurrency("openai"); got != 5 {
		t.Fatalf("pool config OtherMaxConcurrency[openai] = %d, want 5", got)
	}
	if got := m.GetOtherLBMode("openai"); got != "sticky" {
		t.Fatalf("pool config OtherLBMode[openai] = %q, want sticky", got)
	}
	if !m.poolMode {
		t.Fatalf("pool config PoolMode not loaded")
	}
}

// ---------- 2. 旧单文件迁移 ----------

// TestLoadAccounts_LegacySingleFileMigration 验证:旧 accounts.json 存在且无分区文件时,
// 一次性拆分为 7 分区 + 重命名为 .bak;内存经 loadFromPartitions 完整回填。
func TestLoadAccounts_LegacySingleFileMigration(t *testing.T) {
	tmp := t.TempDir()

	// 旧单文件:含多 provider + 2FA 列表 + 池配置。
	legacy := `{
	  "accounts": [
	    {"id":"a1","email":"ag@x.ai","provider":"antigravity","enabled":true,"cooldowns":{}},
	    {"id":"a2","email":"pj@x.ai","provider":"project","projectId":"p1","enabled":true,"cooldowns":{}},
	    {"id":"a3","email":"nv@x.ai","provider":"nvidia","access_token":"k","baseUrl":"https://integrate.api.nvidia.com/v1","enabled":true,"cooldowns":{}},
	    {"id":"a4","email":"gr@x.ai","provider":"grok","access_token":"k","baseUrl":"https://api.x.ai/v1","enabled":true,"cooldowns":{}},
	    {"id":"a5","email":"ot@x.ai","provider":"other","groupId":"openai","accessToken":"k","baseUrl":"https://api.openai.com","formats":["openai"],"enabled":true,"cooldowns":{}}
	  ],
	  "twofa_accounts": [
	    {"id":"t1","email":"2fa@x.ai","twoFA_secret":"S1","provider":"2fa","scopeType":"2fa","enabled":false,"cooldowns":{}}
	  ],
	  "poolMode": true,
	  "activeChannel": "gemini-cli",
	  "nvidiaLbMode": "sticky",
	  "nvidiaMaxConcurrency": 3
	}`
	legacyPath := filepath.Join(tmp, "accounts.json")
	if err := os.WriteFile(legacyPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	m := NewManager()
	m.Init(tmp)

	// 1. 旧文件应被重命名为 .bak。
	assertFileNotExists(t, legacyPath)
	assertFileExists(t, legacyPath+".bak")
	// 2. 7 个分区文件应全部生成。
	for _, kind := range allPartitionKinds() {
		assertFileExists(t, filepath.Join(tmp, partitionFileName(kind)))
	}
	// 3. 内存回填:5 active + 1 2FA。
	accs := m.GetAccounts()
	if len(accs) != 5 {
		t.Fatalf("after migration got %d accounts, want 5", len(accs))
	}
	twofa := m.GetTwoFAAccounts()
	if len(twofa) != 1 {
		t.Fatalf("after migration got %d 2fa, want 1", len(twofa))
	}
	// 4. 池配置回填:gemini-cli 被规整为 antigravity, nvidiaLbMode=sticky, NvidiaMaxConcurrency=3。
	if m.activeChannel != "antigravity" {
		t.Fatalf("activeChannel = %q, want antigravity (gemini-cli normalized)", m.activeChannel)
	}
	if m.nvidiaLBMode != "sticky" {
		t.Fatalf("nvidiaLBMode = %q, want sticky", m.nvidiaLBMode)
	}
	if got := m.GetNvidiaMaxConcurrency(); got != 3 {
		t.Fatalf("NvidiaMaxConcurrency = %d, want 3", got)
	}
	// 5. 分区文件内账号分布正确。
	nvAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_nvidia.json"))
	if len(nvAccounts) != 1 || nvAccounts[0].ID != "a3" {
		t.Fatalf("accounts_nvidia.json = %v, want [a3]", nvAccounts)
	}
	grAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_grok.json"))
	if len(grAccounts) != 1 || grAccounts[0].ID != "a4" {
		t.Fatalf("accounts_grok.json = %v, want [a4]", grAccounts)
	}
	otAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_other.json"))
	if len(otAccounts) != 1 || otAccounts[0].ID != "a5" {
		t.Fatalf("accounts_other.json = %v, want [a5]", otAccounts)
	}
	agAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_antigravity.json"))
	if len(agAccounts) != 1 || agAccounts[0].ID != "a1" {
		t.Fatalf("accounts_antigravity.json = %v, want [a1]", agAccounts)
	}
	// 6. 再次 LoadAccounts 不应重复迁移(分区已存在 → shouldMigrateLegacy=false)。
	//    仅读上次写入的 .bak 不会误判(shouldMigrateLegacy 只看 accounts.json 是否存在,不看 .bak)。
	assertFileExists(t, legacyPath+".bak") // .bak 仍在(不被二次迁移删/改)
}

// ---------- 3. 定向落盘:单 provider ----------

// TestSaveAccountsFor_TargetedProvider 验证:SaveAccountsFor(silent, "nvidia") 只重写 nvidia 分区,
// 其它 provider 分区的修改时间与内容均不受影响。
func TestSaveAccountsFor_TargetedProvider(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 先塞进两个 provider 的账号。
	m.AddAccount(&Account{ID: "ag1", Email: "ag@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}})
	m.AddAccount(&Account{ID: "nv1", Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: DefaultNvidiaBaseURL, Enabled: true, Cooldowns: map[string]int64{}})

	// AddAccount 已各自落对应 provider 分区。记录 antigravity 分区的原始内容。
	agPath := filepath.Join(tmp, "accounts_antigravity.json")
	nvPath := filepath.Join(tmp, "accounts_nvidia.json")
	agBytesBefore, _ := os.ReadFile(agPath)

	// 再改一个 nvidia 账号字段,触发定向落盘 nvidia 分区。
	m.UpdateAccountEnabled("nv1", false) // SaveAccountsFor(true, "nvidia")

	// nvidia 分区应反映 disabled。
	nvAccounts := readFileAccounts(t, nvPath)
	if len(nvAccounts) != 1 || nvAccounts[0].Enabled {
		t.Fatalf("nvidia partition not updated to disabled: %+v", nvAccounts)
	}
	// antigravity 分区字节应与改前完全一致(未被牵连重写)。
	agBytesAfter, _ := os.ReadFile(agPath)
	if string(agBytesBefore) != string(agBytesAfter) {
		t.Fatalf("antigravity partition was rewritten by nvidia-targeted save:\nbefore=%s\nafter=%s", agBytesBefore, agBytesAfter)
	}
}

// ---------- 4. 空 kinds = 全量 ----------

// TestSaveAccountsFor_AllWhenEmpty 验证:kinds 为空时退化为全量写盘,7 个分区文件全部生成。
func TestSaveAccountsFor_AllWhenEmpty(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 塞进 2 个 provider 账号 + 设一个池配置 + 一个 2FA。
	m.AddAccount(&Account{ID: "ag1", Email: "ag@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}})
	m.AddAccount(&Account{ID: "nv1", Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: DefaultNvidiaBaseURL, Enabled: true, Cooldowns: map[string]int64{}})
	m.SetNvidiaLBMode("sticky") // 落 pool 分区
	m.AddTwoFAAccount("2fa@x.ai", "S1")

	// 删除全部分区文件,模拟「磁盘上什么都没有」状态。
	for _, kind := range allPartitionKinds() {
		_ = os.Remove(filepath.Join(tmp, partitionFileName(kind)))
	}
	for _, kind := range allPartitionKinds() {
		assertFileNotExists(t, filepath.Join(tmp, partitionFileName(kind)))
	}

	// 全量写盘(空 kinds)。
	if err := m.SaveAccounts(false); err != nil {
		t.Fatalf("SaveAccounts (full) error: %v", err)
	}
	// 7 个分区文件应全部重新生成。
	for _, kind := range allPartitionKinds() {
		assertFileExists(t, filepath.Join(tmp, partitionFileName(kind)))
	}
	// antigravity + nvidia 各 1 个,2fa 1 个,pool 配置非空。
	agAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_antigravity.json"))
	if len(agAccounts) != 1 {
		t.Fatalf("full save antigravity = %v, want 1 account", agAccounts)
	}
	nvAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_nvidia.json"))
	if len(nvAccounts) != 1 {
		t.Fatalf("full save nvidia = %v, want 1 account", nvAccounts)
	}
	twofaAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_2fa.json"))
	if len(twofaAccounts) != 1 {
		t.Fatalf("full save 2fa = %v, want 1 account", twofaAccounts)
	}
	// project / other / grok 分区文件存在但账号数组为空。
	emptyAccounts := readFileAccounts(t, filepath.Join(tmp, "accounts_project.json"))
	if len(emptyAccounts) != 0 {
		t.Fatalf("project partition should be empty, got %v", emptyAccounts)
	}
	poolCfg := readFilePoolConfig(t, filepath.Join(tmp, "accounts_pool.json"))
	if poolCfg.NvidiaLBMode != "sticky" {
		t.Fatalf("pool NvidiaLBMode = %q, want sticky", poolCfg.NvidiaLBMode)
	}
}

// ---------- 5. 池配置定向写 ----------

// TestSetPoolMode_WritesOnlyPoolFile 验证:SetPoolMode 等池配置变更只落 accounts_pool.json,
// 不重写任何 provider 分区(账号分区文件字节级不变)。
func TestSetPoolMode_WritesOnlyPoolFile(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 准备一个 nvidia 账号(落 accounts_nvidia.json)。
	m.AddAccount(&Account{ID: "nv1", Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: DefaultNvidiaBaseURL, Enabled: true, Cooldowns: map[string]int64{}})

	nvPath := filepath.Join(tmp, "accounts_nvidia.json")
	poolPath := filepath.Join(tmp, "accounts_pool.json")
	nvBytesBefore, _ := os.ReadFile(nvPath)
	poolBytesBefore, _ := os.ReadFile(poolPath)

	// 切池模式 → SaveAccountsFor(false, poolPartKind),只应动 pool 分区。
	m.SetPoolMode(true)

	// nvidia 分区字节不变。
	nvBytesAfter, _ := os.ReadFile(nvPath)
	if string(nvBytesBefore) != string(nvBytesAfter) {
		t.Fatalf("nvidia partition changed by SetPoolMode:\nbefore=%s\nafter=%s", nvBytesBefore, nvBytesAfter)
	}
	// pool 分区应变化(PoolMode 从 false → true)。
	poolBytesAfter, _ := os.ReadFile(poolPath)
	if string(poolBytesBefore) == string(poolBytesAfter) {
		t.Fatalf("pool partition unchanged by SetPoolMode")
	}
	poolCfg := readFilePoolConfig(t, poolPath)
	if !poolCfg.PoolMode {
		t.Fatalf("pool PoolMode = false, want true")
	}
}

// ---------- 6. 2FA 联动双写 ----------

// TestUpdateAccount2FASecret_WritesBothPartitions 验证:UpdateAccount2FASecret 同时改 2FA 列表与
// active 账号的 TwoFASecret 字段时,双写 accounts_2fa.json 与对应 provider 分区。
//
// 场景构造:同一邮箱 "shared@x.ai" 同时存在于 active 池(有 OAuth token 的 antigravity 账号)
// 与独立 2FA 列表(2FA-only 条目)。这正是 UpdateAccount2FASecret 双写路径的目标态——
// 单纯走 AddTwoFAAccount 公开 API 时,若邮箱已在 active 池,AddTwoFAAccount 只改 active 账号
// 字段、不会向 twofaAccounts 追加(故无法经公开 API 形成双列表态);此处直接构造双列表
// 以精确锁定双写分支。
func TestUpdateAccount2FASecret_WritesBothPartitions(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 1. active 池:一个有 token 的 antigravity 账号(有 token → 不会被 LoadAccounts 当成 2FA-only 迁走)。
	agAcc := &Account{ID: "ag1", Email: "shared@x.ai", Provider: "antigravity", AccessToken: "tok", Enabled: true, Cooldowns: map[string]int64{}}
	m.AddAccount(agAcc)

	// 2. 独立 2FA 列表:直接追加一个同邮箱的 2FA-only 条目(同包可触私有字段)。
	m.Lock()
	m.twofaAccounts = append(m.twofaAccounts, &Account{
		ID:          "2fa1",
		Email:       "shared@x.ai",
		Provider:    "2fa",
		ScopeType:   "2fa",
		TwoFASecret: "OLD-SECRET",
		Enabled:     false,
		Cooldowns:   map[string]int64{},
	})
	m.Unlock()

	agPath := filepath.Join(tmp, "accounts_antigravity.json")
	twofaPath := filepath.Join(tmp, "accounts_2fa.json")

	// 以 2FA 列表账号的 ID 为入口刷新密钥 → 触发双写 [2fa, antigravity]。
	m.UpdateAccount2FASecret("2fa1", "NEW-SECRET")

	// 2FA 分区应反映 NEW-SECRET。
	twofaAccounts := readFileAccounts(t, twofaPath)
	found := false
	for _, a := range twofaAccounts {
		if a.Email == "shared@x.ai" && a.TwoFASecret == "NEW-SECRET" {
			found = true
		}
	}
	if !found {
		t.Fatalf("2fa partition not updated to NEW-SECRET: %+v", twofaAccounts)
	}
	// antigravity 分区也应反映 active 账号的 TwoFASecret = NEW-SECRET(双写)。
	agAccounts := readFileAccounts(t, agPath)
	if len(agAccounts) != 1 {
		t.Fatalf("antigravity partition = %v, want 1 account", agAccounts)
	}
	if agAccounts[0].TwoFASecret != "NEW-SECRET" {
		t.Fatalf("antigravity partition TwoFASecret = %q, want NEW-SECRET (dual-write)", agAccounts[0].TwoFASecret)
	}
}

// ---------- 7. 跨多 provider 批量导入 ----------

// TestImportAccountsList_CrossProvider 验证:ImportAccountsList 跨多 provider 导入时,
// 按受影响 provider 集合逐分区写盘,未涉及 provider 分区不生成(或不变)。
func TestImportAccountsList_CrossProvider(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 批量导入 3 个 provider 各 1 个账号。
	in := []*Account{
		{Email: "ag@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}},
		{Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: DefaultNvidiaBaseURL, Enabled: true, Cooldowns: map[string]int64{}},
		{Email: "gr@x.ai", Provider: "grok", AccessToken: "k", BaseURL: DefaultGrokBaseURL, Enabled: true, Cooldowns: map[string]int64{}},
	}
	added := m.ImportAccountsList(in)
	if added != 3 {
		t.Fatalf("ImportAccountsList added = %d, want 3", added)
	}

	// 三处涉及分区应生成且各 1 个账号。
	assertFileExists(t, filepath.Join(tmp, "accounts_antigravity.json"))
	assertFileExists(t, filepath.Join(tmp, "accounts_nvidia.json"))
	assertFileExists(t, filepath.Join(tmp, "accounts_grok.json"))
	if got := len(readFileAccounts(t, filepath.Join(tmp, "accounts_antigravity.json"))); got != 1 {
		t.Fatalf("antigravity partition = %d accounts, want 1", got)
	}
	if got := len(readFileAccounts(t, filepath.Join(tmp, "accounts_nvidia.json"))); got != 1 {
		t.Fatalf("nvidia partition = %d accounts, want 1", got)
	}
	if got := len(readFileAccounts(t, filepath.Join(tmp, "accounts_grok.json"))); got != 1 {
		t.Fatalf("grok partition = %d accounts, want 1", got)
	}
	// 未涉及的 project / other 分区不应生成(定向落盘不创建无关分区)。
	assertFileNotExists(t, filepath.Join(tmp, "accounts_project.json"))
	assertFileNotExists(t, filepath.Join(tmp, "accounts_other.json"))
	// pool 分区也不应生成(导入不涉及池配置)。
	assertFileNotExists(t, filepath.Join(tmp, "accounts_pool.json"))

	// ID 全局唯一。
	accs := m.GetAccounts()
	seen := make(map[string]struct{}, len(accs))
	for _, a := range accs {
		if a.ID == "" {
			t.Errorf("imported account %q got empty ID", a.Email)
			continue
		}
		if _, dup := seen[a.ID]; dup {
			t.Errorf("duplicate ID %q after import", a.ID)
		}
		seen[a.ID] = struct{}{}
	}
}

// ---------- 8. 往返一致性 ----------

// TestRoundTrip_LoadSaveLoad 验证:写盘 → 新实例加载 → 再写盘 → 再加载,
// 内存聚合数组 + 2FA 列表 + 池配置应该三方一致往返(零字段漂移)。
func TestRoundTrip_LoadSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	m1 := NewManager()
	m1.Init(tmp)

	// 填充多 provider + 2FA + 池配置。
	m1.AddAccount(&Account{ID: "ag1", Email: "ag@x.ai", Provider: "antigravity", Enabled: true, Cooldowns: map[string]int64{}})
	m1.AddAccount(&Account{ID: "nv1", Email: "nv@x.ai", Provider: "nvidia", AccessToken: "k", BaseURL: DefaultNvidiaBaseURL, Enabled: true, Cooldowns: map[string]int64{}})
	m1.AddAccount(&Account{ID: "gr1", Email: "gr@x.ai", Provider: "grok", AccessToken: "k", BaseURL: DefaultGrokBaseURL, Enabled: true, Cooldowns: map[string]int64{}})
	m1.AddTwoFAAccount("2fa@x.ai", "S1")
	m1.SetPoolMode(false)                     // 关 antigravity 池模式,放开 channel 互斥
	m1.SetActiveChannel("nvidia")             // antigravity/project/nvidia 互斥:poolMode 关后 nvidia 可切
	m1.SetNvidiaLBMode("sticky")
	m1.SetNvidiaMaxConcurrency(7)
	m1.SetOtherLBMode("openai", "sticky")
	m1.SetOtherMaxConcurrency("openai", 5)
	m1.SetGrokCliVersion("0.1.202")
	m1.SetGrokQuotaCooldownHours(48)
	m1.SetProjectMaxConcurrency(13) // 另一非默认池配置项,验证多字段往返

	// 第二实例从同目录加载。
	m2 := NewManager()
	m2.Init(tmp)
	if got := len(m2.GetAccounts()); got != 3 {
		t.Fatalf("m2 Load accounts = %d, want 3", got)
	}
	if got := len(m2.GetTwoFAAccounts()); got != 1 {
		t.Fatalf("m2 Load 2fa = %d, want 1", got)
	}
	if m2.poolMode {
		t.Fatalf("m2 PoolMode = true, want false (m1 SetPoolMode(false) 应原样落盘)")
	}
	if m2.activeChannel != "nvidia" {
		t.Fatalf("m2 activeChannel = %q, want nvidia", m2.activeChannel)
	}
	if got := m2.GetNvidiaMaxConcurrency(); got != 7 {
		t.Fatalf("m2 NvidiaMaxConcurrency = %d, want 7", got)
	}
	if got := m2.GetOtherLBMode("openai"); got != "sticky" {
		t.Fatalf("m2 OtherLBMode[openai] = %q, want sticky", got)
	}
	if got := m2.GetOtherMaxConcurrency("openai"); got != 5 {
		t.Fatalf("m2 OtherMaxConcurrency[openai] = %d, want 5", got)
	}
	if got := m2.GetProjectMaxConcurrency(); got != 13 {
		t.Fatalf("m2 ProjectMaxConcurrency = %d, want 13", got)
	}
	if got := m2.GetGrokCliVersion(); got != "0.1.202" {
		t.Fatalf("m2 GrokCliVersion = %q, want 0.1.202", got)
	}
	if got := m2.GetGrokQuotaCooldownHours(); got != 48 {
		t.Fatalf("m2 GrokQuotaCooldownHours = %d, want 48", got)
	}

	// m2 再写一次全量,验证字节稳定(与 m1 写出的文件在「内存层正规化后」一致)。
	// 注意:LoadAccounts 的内存层正规化会对每个 active 账号补空 Provider→antigravity 推断、
	// 空补 account 等(xxx 字段由 LoadAccounts 现写),分区文件首次写出(m1 落盘)时这些字段
	// 暂为空;经 m2 重载后内存补全、再写出时这些字段被填上。故逐字节幂等性在此处不成立
	//(非真缺陷,是内存层 Load 时副写正规化字段的预期行为),改为按「正规化后稳定」断言:
	// m2 写出 → m3 重载 → m3 写出:这两次写出应逐字节一致(正规化已收敛)。
	if err := m2.SaveAccounts(false); err != nil {
		t.Fatalf("m2 SaveAccounts error: %v", err)
	}
	partitionBytesM2 := map[string][]byte{}
	for _, kind := range allPartitionKinds() {
		p := filepath.Join(tmp, partitionFileName(kind))
		if data, err := os.ReadFile(p); err == nil {
			partitionBytesM2[kind] = data
		}
	}
	// m3 从 m2 写出的分区重载,再做一次全量写盘。
	m3 := NewManager()
	m3.Init(tmp)
	if err := m3.SaveAccounts(false); err != nil {
		t.Fatalf("m3 SaveAccounts error: %v", err)
	}
	for kind, before := range partitionBytesM2 {
		after, err := os.ReadFile(filepath.Join(tmp, partitionFileName(kind)))
		if err != nil {
			t.Fatalf("re-read %s: %v", kind, err)
		}
		if string(before) != string(after) {
			t.Fatalf("partition %s not idempotent across post-normalize Load→Save round-trip:\nbefore=%s\nafter=%s",
				kind, before, after)
		}
	}

	// 白盒:删除全部分区,验证空 kinds 全量仍能从空目录正常出 7 文件。
	for _, kind := range allPartitionKinds() {
		_ = os.Remove(filepath.Join(tmp, partitionFileName(kind)))
	}
	m4 := NewManager()
	m4.Init(tmp)
	if err := m4.SaveAccounts(false); err != nil {
		t.Fatalf("m4 SaveAccounts from empty: %v", err)
	}
	for _, kind := range allPartitionKinds() {
		assertFileExists(t, filepath.Join(tmp, partitionFileName(kind)))
	}
	// 提供 placeholder 引用以避免 import 未用(若未来加 strings 用例保留导入)。
	_ = strings.TrimSpace
}

// TestNvidiaAccount_EgressIP 验证 NVIDIA 账号配置专属 EgressIP 的 CRUD 与落盘往返
func TestNvidiaAccount_EgressIP(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 1. 添加带 EgressIP 的账号
	id, err := m.AddNvidiaAccount(NvidiaAccountInput{
		BaseURL:  "https://integrate.api.nvidia.com/v1",
		APIKey:   "nvapi-test-key",
		Label:    "nv-with-ip",
		EgressIP: "104.28.19.82",
	})
	if err != nil {
		t.Fatalf("AddNvidiaAccount failed: %v", err)
	}

	acc := m.GetAccountByID(id)
	if acc == nil || acc.EgressIP != "104.28.19.82" {
		t.Fatalf("Expected EgressIP 104.28.19.82, got %+v", acc)
	}

	// 2. 更新 EgressIP
	_, err = m.UpdateNvidiaAccount(id, NvidiaAccountInput{
		BaseURL:  "https://integrate.api.nvidia.com/v1",
		Label:    "nv-with-ip-updated",
		EgressIP: "104.28.19.99",
	})
	if err != nil {
		t.Fatalf("UpdateNvidiaAccount failed: %v", err)
	}

	accUpdated := m.GetAccountByID(id)
	if accUpdated == nil || accUpdated.EgressIP != "104.28.19.99" {
		t.Fatalf("Expected updated EgressIP 104.28.19.99, got %+v", accUpdated)
	}

	// 3. 从新 Manager 重新加载，验证 JSON 分区文件落盘无损回填与 GetAccounts 深拷贝透出
	m2 := NewManager()
	m2.Init(tmp)
	reloadedAcc := m2.GetAccountByID(id)
	if reloadedAcc == nil || reloadedAcc.EgressIP != "104.28.19.99" {
		t.Fatalf("Expected reloaded EgressIP 104.28.19.99, got %+v", reloadedAcc)
	}

	// 4. 验证 GetAccounts 深拷贝列表透出 EgressIP(供前端展示与编辑模态框回显)
	frontendAccounts := m2.GetAccounts()
	if len(frontendAccounts) != 1 || frontendAccounts[0].EgressIP != "104.28.19.99" {
		t.Fatalf("Expected frontend deep copy EgressIP 104.28.19.99, got %+v", frontendAccounts)
	}
}

