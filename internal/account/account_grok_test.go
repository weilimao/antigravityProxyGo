package account

import (
	"strings"
	"testing"
)

// account_grok_test.go: Grok(x.ai) 号池账号域的构造、校验、录入、更新与档位解析单元测试。
// 与 account_other_test.go / account_nvidia_test.go 同构:覆盖 ValidateGrokAccountInput 边界、
// NewGrokAccount 字段映射、AddGrokAccount 默认档位、UpdateGrokAccount(key 留空保持)、IsGrokAvailable、
// ResolveGrokModel 档位解析、池模式 / LB / 并发上限 访问器。

// validGrokInput 是一个最小可用的 Grok 账号录入样例,各 case 在其上微调覆盖分支。
func validGrokInput() GrokAccountInput {
	return GrokAccountInput{
		BaseURL: "https://api.x.ai/v1",
		APIKey:  "xai-1234567890abcdef",
		Label:   "我的 Grok 账号",
	}
}

func TestValidateGrokAccountInput_OK(t *testing.T) {
	if err := ValidateGrokAccountInput(validGrokInput()); err != nil {
		t.Fatalf("expected nil error for valid input, got: %v", err)
	}
	// BaseURL 留空 → 回退默认(合法)。
	in := validGrokInput()
	in.BaseURL = ""
	if err := ValidateGrokAccountInput(in); err != nil {
		t.Fatalf("expected nil error when baseURL empty(回退默认), got: %v", err)
	}
	// BaseURL 带尾斜杠也合法。
	in2 := validGrokInput()
	in2.BaseURL = "https://api.x.ai/v1/"
	if err := ValidateGrokAccountInput(in2); err != nil {
		t.Fatalf("expected nil error for baseURL with trailing slash, got: %v", err)
	}
	// http(非 SSL 内网上游)合法。
	in3 := validGrokInput()
	in3.BaseURL = "http://localhost:8080/v1"
	if err := ValidateGrokAccountInput(in3); err != nil {
		t.Fatalf("expected nil error for http baseURL, got: %v", err)
	}
}

func TestValidateGrokAccountInput_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(in GrokAccountInput) GrokAccountInput
		want string
	}{
		{"非法 scheme", func(in GrokAccountInput) GrokAccountInput { in.BaseURL = "ftp://api.x.ai/v1"; return in }, "http"},
		{"非法 baseURL", func(in GrokAccountInput) GrokAccountInput { in.BaseURL = "://missing-scheme"; return in }, "http"},
		{"空 apiKey(新增态)", func(in GrokAccountInput) GrokAccountInput { in.APIKey = ""; return in }, "api_key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateGrokAccountInput(c.mut(validGrokInput()))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain expected substring %q", err.Error(), c.want)
			}
		})
	}
}

// TestValidateGrokAccountInput_EditAllowsEmptyKey 锁定编辑态 validateGrokFields(requireKey=false)
// 允许 APIKey 留空(前端编辑时若不改 Key 则传空)。通过直接调 AddGrokAccount 无法触发 requireKey=false,
// 故经 UpdateGrokAccount 间接覆盖(Update调 validateGrokFields(in,false))。
func TestValidateGrokAccountInput_EditAllowsEmptyKey(t *testing.T) {
	m := NewManager()
	id, err := m.AddGrokAccount(validGrokInput())
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// 编辑态:key 留空 → 仅更新其它字段,不报 key 必填错。
	in := validGrokInput()
	in.APIKey = "" // 留空表示保持原 Key
	in.DefaultModel = "grok-4-fast"
	acc, err := m.UpdateGrokAccount(id, in)
	if err != nil {
		t.Fatalf("edit with empty key should succeed(保持原 Key), got: %v", err)
	}
	// 原 Key 应保持不变(key 留空不覆盖)。
	if acc.GetAccessToken() != "xai-1234567890abcdef" {
		t.Errorf("empty key edit should keep original key, got %q", acc.GetAccessToken())
	}
	if acc.DefaultModel != "grok-4-fast" {
		t.Errorf("defaultModel should update, got %q", acc.DefaultModel)
	}
}

func TestNewGrokAccount_Fields(t *testing.T) {
	in := validGrokInput()
	in.Label = ""
	in.DefaultModel = "grok-4-fast"
	in.ModelSonnet = "grok-4"
	in.ModelOpus = "grok-4.3"
	in.ModelHaiku = "grok-4-fast"
	in.ModelFable = "grok-3"
	acc := NewGrokAccount(in)

	if acc.Provider != grokProvider {
		t.Errorf("Provider: want %q, got %q", grokProvider, acc.Provider)
	}
	if acc.ScopeType != grokScope {
		t.Errorf("ScopeType: want %q, got %q", grokScope, acc.ScopeType)
	}
	if acc.BaseURL != "https://api.x.ai/v1" {
		t.Errorf("BaseURL: want https://api.x.ai/v1, got %q", acc.BaseURL)
	}
	if acc.AccessToken != "xai-1234567890abcdef" {
		t.Errorf("AccessToken: want xai-..., got %q", acc.AccessToken)
	}
	if !acc.Enabled {
		t.Errorf("Enabled: want true")
	}
	if acc.DefaultModel != "grok-4-fast" {
		t.Errorf("DefaultModel: want grok-4-fast, got %q", acc.DefaultModel)
	}
	if acc.ModelSonnet != "grok-4" || acc.ModelOpus != "grok-4.3" || acc.ModelHaiku != "grok-4-fast" || acc.ModelFable != "grok-3" {
		t.Errorf("model tiers mismatch: sonnet=%q opus=%q haiku=%q fable=%q", acc.ModelSonnet, acc.ModelOpus, acc.ModelHaiku, acc.ModelFable)
	}
	if acc.Cooldowns == nil {
		t.Errorf("Cooldowns: want non-nil map")
	}
}

func TestNewGrokAccount_LabelFallbackToHost(t *testing.T) {
	// Label 留空 → 回退 base_url 的 host(api.x.ai),便于在号池中辨认。
	in := validGrokInput()
	in.Label = ""
	acc := NewGrokAccount(in)
	if acc.Email != "api.x.ai" {
		t.Errorf("Email(展示名)回退: want api.x.ai(host), got %q", acc.Email)
	}
}

func TestNewGrokAccount_BaseURLEmptyFallsBackDefault(t *testing.T) {
	// BaseURL 留空 → 回退 DefaultGrokBaseURL。
	in := validGrokInput()
	in.BaseURL = ""
	in.Label = ""
	acc := NewGrokAccount(in)
	if acc.BaseURL != DefaultGrokBaseURL {
		t.Errorf("BaseURL empty should fallback to %q, got %q", DefaultGrokBaseURL, acc.BaseURL)
	}
}

// TestManager_AddGrokAccount_DefaultModel 锁定:全部模型字段留空时,AddGrokAccount
// 给一个开箱即用的默认档位(DefaultGrokModel="grok-4.3")。
func TestManager_AddGrokAccount_DefaultModel(t *testing.T) {
	m := NewManager()
	in := validGrokInput()
	// 全部模型字段留空
	in.DefaultModel = ""
	in.ModelSonnet = ""
	in.ModelOpus = ""
	in.ModelHaiku = ""
	in.ModelFable = ""
	id, err := m.AddGrokAccount(in)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	acc := m.GetRawAccountsByProvider("grok")[0]
	if acc.ID != id {
		t.Errorf("returned id mismatch")
	}
	if acc.DefaultModel != DefaultGrokModel {
		t.Errorf("全部模型留空应回退默认 %q, got %q", DefaultGrokModel, acc.DefaultModel)
	}
}

func TestManager_AddGrokAccount_DedupByLabel(t *testing.T) {
	m := NewManager()
	in := validGrokInput() // Label="我的 Grok 账号"
	if _, err := m.AddGrokAccount(in); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// AddAccount 按 Email+Provider 去重:同 Label + 同 Provider 视为同号。
	// 返回不报错(AddGrokAccount 不查重),但落库后 GetRawAccountsByProvider 应只 1 条。
	_, _ = m.AddGrokAccount(in)
	accs := m.GetRawAccountsByProvider("grok")
	if len(accs) != 1 {
		t.Fatalf("同 Label+Provider 应被 AddAccount 去重为 1 条, got %d", len(accs))
	}
}

// TestManager_AddGrokAccount_TwoDistinctLabels 锁定组内多账号:两个不同 Label(或不同 Key)
// 的 Grok 账号可共存(号池轮换前提)。
func TestManager_AddGrokAccount_TwoDistinctLabels(t *testing.T) {
	m := NewManager()
	in1 := validGrokInput()
	in1.Label = "Grok-A"
	in1.APIKey = "xai-aaaaaaaaaaaaaaaa"
	in2 := validGrokInput()
	in2.Label = "Grok-B"
	in2.APIKey = "xai-bbbbbbbbbbbbbbbb"
	if _, err := m.AddGrokAccount(in1); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if _, err := m.AddGrokAccount(in2); err != nil {
		t.Fatalf("add B: %v", err)
	}
	accs := m.GetEnabledGrokAccounts()
	if len(accs) != 2 {
		t.Fatalf("expected 2 enabled grok accounts, got %d", len(accs))
	}
}

func TestManager_UpdateGrokAccount_NotFoundOrWrongType(t *testing.T) {
	m := NewManager()
	// 不存在的 id。
	_, err := m.UpdateGrokAccount("nonexistent", validGrokInput())
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("update nonexistent should error 含\"不存在\", got: %v", err)
	}
	// 非 grok 类型的账号(NVIDIA)不被 UpdateGrokAccount 修改。
	m.AddNvidiaAccount(NvidiaAccountInput{BaseURL: "https://integrate.api.nvidia.com/v1", APIKey: "nv-key", Label: "nv"})
	nvAccs := m.GetRawAccountsByProvider("nvidia")
	if len(nvAccs) == 0 {
		t.Fatalf("seed nvidia account failed")
	}
	_, err = m.UpdateGrokAccount(nvAccs[0].ID, validGrokInput())
	if err == nil || !strings.Contains(err.Error(), "非 Grok") {
		t.Fatalf("update nvidia acc via UpdateGrokAccount should error 含\"非 Grok\", got: %v", err)
	}
}

func TestManager_UpdateGrokAccount_OverwriteKey(t *testing.T) {
	m := NewManager()
	id, err := m.AddGrokAccount(validGrokInput())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 编辑态提供新 key → 覆盖 AccessToken。
	in := validGrokInput()
	in.APIKey = "xai-newkey1234567890"
	acc, err := m.UpdateGrokAccount(id, in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if acc.GetAccessToken() != "xai-newkey1234567890" {
		t.Errorf("new key should overwrite, got %q", acc.GetAccessToken())
	}
}

func TestIsGrokAvailable(t *testing.T) {
	// 完整可用账号。
	acc := NewGrokAccount(validGrokInput())
	if !IsGrokAvailable(acc) {
		t.Error("完整 grok 账号应可用")
	}
	// 停用。
	disabled := NewGrokAccount(validGrokInput())
	disabled.Enabled = false
	if IsGrokAvailable(disabled) {
		t.Error("停用账号不应可用")
	}
	// 空 Key。
	noKey := NewGrokAccount(validGrokInput())
	noKey.AccessToken = ""
	if IsGrokAvailable(noKey) {
		t.Error("空 Key 账号不应可用")
	}
	// 空 BaseURL。
	noURL := NewGrokAccount(validGrokInput())
	noURL.BaseURL = ""
	if IsGrokAvailable(noURL) {
		t.Error("空 BaseURL 账号不应可用")
	}
	// 非 grok Provider。
	nv := NewGrokAccount(validGrokInput())
	nv.Provider = "nvidia"
	if IsGrokAvailable(nv) {
		t.Error("非 grok Provider 不应被 IsGrokAvailable 判可用")
	}
	// nil 账号。
	if IsGrokAvailable(nil) {
		t.Error("nil 账号不应可用")
	}
}

// TestResolveGrokModel 锁定档位解析:[1M] 后缀剥离 / 命中档位取账号字段 / 缺省回退 DefaultModel /
// 再缺省回退默认 / 客户端显式具名上游模型(含 /)优先透传。
func TestResolveGrokModel(t *testing.T) {
	acc := &Account{
		Provider:     grokProvider,
		DefaultModel: "grok-4.3",
		ModelSonnet:  "grok-4",
		ModelOpus:    "grok-4.3",
		ModelHaiku:   "grok-4-fast",
		ModelFable:   "grok-3",
	}
	cases := []struct {
		name  string
		in    string
		want  string
	}{
		{"命中 sonnet 档", "claude-sonnet-4", "grok-4"},
		{"命中 opus 档(大小写不敏感)", "Claude-OPUS-4.1", "grok-4.3"},
		{"命中 haiku 档", "claude-3-5-haiku", "grok-4-fast"},
		{"命中 fable 档", "claude-fable-5", "grok-3"},
		{"未命中档位 → DefaultModel", "deepseek-chat", "grok-4.3"},
		{"[1M] 后缀剥离后未命中档 → DefaultModel", "gpt-4o[1M]", "grok-4.3"},
		{"含 / 具名上游模型优先透传", "xai/grok-2", "xai/grok-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveGrokModel(c.in, acc)
			if got != c.want {
				t.Errorf("ResolveGrokModel(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// nil acc → DefaultGrokModel。
	if got := ResolveGrokModel("claude-sonnet-4", nil); got != DefaultGrokModel {
		t.Errorf("nil acc should return DefaultGrokModel %q, got %q", DefaultGrokModel, got)
	}
	// acc 无 DefaultModel 且 input 含 / → 透传。
	bareAcc := &Account{Provider: grokProvider, ModelSonnet: "grok-4"}
	if got := ResolveGrokModel("xai/grok-2", bareAcc); got != "xai/grok-2" {
		t.Errorf("无 DefaultModel 且含 / 应透传, got %q", got)
	}
	// acc 无 DefaultModel 且 input 不含 / 但有值 → 透传原始名(未配档位)。
	if got := ResolveGrokModel("deepseek-chat", bareAcc); got != "deepseek-chat" {
		t.Errorf("无 DefaultModel 且无档命中应透传原名, got %q", got)
	}
	// acc 无 DefaultModel 且 input 为空 → DefaultGrokModel。
	emptyAcc := &Account{Provider: grokProvider}
	if got := ResolveGrokModel("", emptyAcc); got != DefaultGrokModel {
		t.Errorf("空 input + 无 DefaultModel 应回退默认, got %q", got)
	}
}

func TestManager_GrokLBMode(t *testing.T) {
	m := NewManager()
	// 默认 round-robin。
	if got := m.GetGrokLBMode(); got != "round-robin" {
		t.Errorf("default grok LB mode: want round-robin, got %q", got)
	}
	// 设置 sticky。
	m.SetGrokLBMode("sticky")
	if got := m.GetGrokLBMode(); got != "sticky" {
		t.Errorf("after set: want sticky, got %q", got)
	}
}

func TestManager_GrokMaxConcurrency(t *testing.T) {
	m := NewManager()
	// 默认值(<=0 回退 defaultMaxConcurrency)。
	def := m.GetGrokMaxConcurrency()
	if def <= 0 {
		t.Errorf("default max concurrency should be > 0, got %d", def)
	}
	// 设置具体值。
	m.SetGrokMaxConcurrency(20)
	if got := m.GetGrokMaxConcurrency(); got != 20 {
		t.Errorf("after set: want 20, got %d", got)
	}
	// 负值钳为 0 → 回退默认。
	m.SetGrokMaxConcurrency(-1)
	if got := m.GetGrokMaxConcurrency(); got != def {
		t.Errorf("negative should clamp to 0 → default %d, got %d", def, got)
	}
}

// TestManager_GrokCliVersion 锁定 Grok 号池全局 CLI 版本号访问器(对仗 TestManager_GrokMaxConcurrency)。
// 空串=未配置 → 回退默认 DefaultGrokCliVersion("1.0.0")(与 MaxConcurrency <=0 回退 default 同范式,
// 确保用户清空配置不会让身份头丢失触发 426)。
func TestManager_GrokCliVersion(t *testing.T) {
	m := NewManager()
	// 默认值(空串回退 DefaultGrokCliVersion "1.0.0")。
	if got := m.GetGrokCliVersion(); got != DefaultGrokCliVersion {
		t.Errorf("default grok cli version: want %q, got %q", DefaultGrokCliVersion, got)
	}
	// 设置具体值。
	m.SetGrokCliVersion("0.2.93")
	if got := m.GetGrokCliVersion(); got != "0.2.93" {
		t.Errorf("after set: want 0.2.93, got %q", got)
	}
	// 空串 + 纯空白 → 回退默认(SetGrokCliVersion TrimSpace 后空 → GetGrokCliVersion 回退)。
	m.SetGrokCliVersion("   ")
	if got := m.GetGrokCliVersion(); got != DefaultGrokCliVersion {
		t.Errorf("blank should fall back to default %q, got %q", DefaultGrokCliVersion, got)
	}
	// 入参 TrimSpace:首尾空白应被规整,Get 返回去空白后的真值。
	m.SetGrokCliVersion("  1.2.3  ")
	if got := m.GetGrokCliVersion(); got != "1.2.3" {
		t.Errorf("TrimSpace: want 1.2.3, got %q", got)
	}
}

// TestManager_SetGrokPoolMode_MutexAndActiveChannel 锁定:开 Grok 池模式时,
// 其余号池模式互斥置 false 并 activeChannel 切到 "grok"。
func TestManager_SetGrokPoolMode_MutexAndActiveChannel(t *testing.T) {
	m := NewManager()
	// 先开 nvidia 池,验证互斥。
	m.SetNvidiaPoolMode(true)
	if !m.GetNvidiaPoolMode() {
		t.Fatal("seed nvidia pool should be on")
	}
	// 开 grok 池 → nvidia 应被关,activeChannel=grok。
	m.SetGrokPoolMode(true)
	if !m.GetGrokPoolMode() {
		t.Error("grok pool mode should be on after SetGrokPoolMode(true)")
	}
	if m.GetNvidiaPoolMode() {
		t.Error("nvidia pool mode should be off (互斥) after enabling grok")
	}
	if m.GetActiveChannel() != "grok" {
		t.Errorf("activeChannel should be grok, got %q", m.GetActiveChannel())
	}
	// 关 grok 池 → activeChannel 不被强制改(关时不置互斥分支),但 poolMode 应 false。
	m.SetGrokPoolMode(false)
	if m.GetGrokPoolMode() {
		t.Error("grok pool mode should be off after SetGrokPoolMode(false)")
	}
}

func TestManager_GetEnabledGrokAccounts(t *testing.T) {
	m := NewManager()
	// 用显式唯一 ID 直接构造 + AddAccount,绕开 generateAccountID 在 Windows 同纳秒
	// 打码撞 ID 的既有缺陷(两个 AddGrokAccount 连调可能生成相同 ID,导致
	// UpdateAccountEnabled 按首个匹配禁用错号)。这与 grok_usage_test 的 mkGrokAccount 同款。
	accA := &Account{
		ID: "grok-enabled-A", Email: "Grok-A", Provider: grokProvider, ScopeType: grokScope,
		AccessToken: "xai-aaaaaaaaaaaaaaaa", BaseURL: DefaultGrokBaseURL, Enabled: true,
		Cooldowns: map[string]int64{},
	}
	accB := &Account{
		ID: "grok-enabled-B", Email: "Grok-B", Provider: grokProvider, ScopeType: grokScope,
		AccessToken: "xai-bbbbbbbbbbbbbbbb", BaseURL: DefaultGrokBaseURL, Enabled: true,
		Cooldowns: map[string]int64{},
	}
	m.AddAccount(accA)
	m.AddAccount(accB)
	// 停用第二个。
	m.UpdateAccountEnabled("grok-enabled-B", false)

	got := m.GetEnabledGrokAccounts()
	if len(got) != 1 {
		t.Fatalf("expected 1 enabled grok account, got %d", len(got))
	}
	if got[0].Email != "Grok-A" {
		t.Errorf("expected Grok-A(未停用), got %q", got[0].Email)
	}
}

// TestGrokModelField_String 锁定档位字段常量的字符串值(供日志/调试,不参与序列化)。
func TestGrokModelField_String(t *testing.T) {
	cases := map[GrokModelField]string{
		GrokModelSonnet:  "sonnet",
		GrokModelOpus:    "opus",
		GrokModelHaiku:   "haiku",
		GrokModelFable:   "fable",
		GrokModelDefault: "default",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", f, got, want)
		}
	}
}

// TestGrokDefaultConstants 锁定默认常量值(防止后续误改导致开箱即用回归)。
func TestGrokDefaultConstants(t *testing.T) {
	if DefaultGrokModel != "grok-4.3" {
		t.Errorf("DefaultGrokModel = %q, want grok-4.3", DefaultGrokModel)
	}
	if DefaultGrokBaseURL != "https://api.x.ai/v1" {
		t.Errorf("DefaultGrokBaseURL = %q, want https://api.x.ai/v1", DefaultGrokBaseURL)
	}
	if grokProvider != "grok" {
		t.Errorf("grokProvider = %q, want grok", grokProvider)
	}
	if grokScope != "grok" {
		t.Errorf("grokScope = %q, want grok", grokScope)
	}
}
