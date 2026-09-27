package account

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkBuddyAccount_Validation(t *testing.T) {
	// 缺少 token
	err := ValidateWorkBuddyAccountInput(WorkBuddyAccountInput{
		BaseURL: "https://www.codebuddy.ai",
	})
	if err == nil {
		t.Fatal("期望缺少 token 时校验报错，但返回了 nil")
	}

	// 非法 URL
	err = ValidateWorkBuddyAccountInput(WorkBuddyAccountInput{
		BaseURL:     "ftp://invalid-url",
		AccessToken: "test-token",
	})
	if err == nil {
		t.Fatal("期望非法 BaseURL 报错，但返回了 nil")
	}

	// 正常参数
	err = ValidateWorkBuddyAccountInput(WorkBuddyAccountInput{
		BaseURL:     "https://www.codebuddy.ai",
		AccessToken: "test-token-123",
	})
	if err != nil {
		t.Fatalf("正常参数不应报错: %v", err)
	}
}

func TestWorkBuddyAccount_ParseAuthInfo(t *testing.T) {
	sampleJSON := []byte(`{
		"account": {
			"uid": "155c43e7-3588-4f7e-b2ae-ba3c74df0ee1",
			"nickname": "testuser@example.com"
		},
		"auth": {
			"accessToken": "eyJhbGciOi...",
			"refreshToken": "ref-12345",
			"expiresIn": 31184633
		}
	}`)

	input, err := ParseWorkBuddyAuthInfo(sampleJSON)
	if err != nil {
		t.Fatalf("ParseWorkBuddyAuthInfo 报错: %v", err)
	}

	if input.UID != "155c43e7-3588-4f7e-b2ae-ba3c74df0ee1" {
		t.Errorf("UID 期望 155c43e7-3588-4f7e-b2ae-ba3c74df0ee1, 得到 %s", input.UID)
	}
	if input.Nickname != "testuser@example.com" {
		t.Errorf("Nickname 期望 testuser@example.com, 得到 %s", input.Nickname)
	}
	if input.AccessToken != "eyJhbGciOi..." {
		t.Errorf("AccessToken 期望 eyJhbGciOi..., 得到 %s", input.AccessToken)
	}
	if input.RefreshToken != "ref-12345" {
		t.Errorf("RefreshToken 期望 ref-12345, 得到 %s", input.RefreshToken)
	}
}

func TestWorkBuddyAccount_CRUDAndImport(t *testing.T) {
	// 独立的临时工作区，严格遵循 Teardown 规范
	tempDir, err := os.MkdirTemp("", "wb_test_*")
	if err != nil {
		t.Fatalf("创建临时测试目录失败: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	mgr := NewManager()
	mgr.Init(tempDir)

	// 测试新增
	id, err := mgr.AddWorkBuddyAccount(WorkBuddyAccountInput{
		AccessToken: "test-token-initial",
		Nickname:    "my-wb-account",
		UID:         "wb-uid-001",
	})
	if err != nil {
		t.Fatalf("AddWorkBuddyAccount 失败: %v", err)
	}

	acc := mgr.GetAccountByID(id)
	if acc == nil {
		t.Fatalf("未找到新增的账号 id: %s", id)
	}
	if acc.Provider != "workbuddy" {
		t.Errorf("Provider 期望 workbuddy, 得到 %s", acc.Provider)
	}
	if !IsWorkBuddyAvailable(acc) {
		t.Error("IsWorkBuddyAvailable 期望为 true")
	}

	// 测试更新
	updated, err := mgr.UpdateWorkBuddyAccount(id, WorkBuddyAccountInput{
		AccessToken:  "test-token-updated",
		Nickname:     "my-wb-account-renamed",
		DefaultModel: "hy4-preview-f",
	})
	if err != nil {
		t.Fatalf("UpdateWorkBuddyAccount 失败: %v", err)
	}
	if updated.AccessToken != "test-token-updated" {
		t.Errorf("AccessToken 未成功更新")
	}
	if updated.Email != "my-wb-account-renamed" {
		t.Errorf("Email/Nickname 未成功更新")
	}

	// 测试从临时文件导入
	mockFilePath := filepath.Join(tempDir, "mock_wb.info")
	mockContent := []byte(`{
		"account": {
			"uid": "wb-uid-002",
			"nickname": "imported@example.com"
		},
		"auth": {
			"accessToken": "imported-token-xyz",
			"refreshToken": "imported-ref",
			"expiresIn": 3600
		}
	}`)
	if err := os.WriteFile(mockFilePath, mockContent, 0644); err != nil {
		t.Fatalf("写 mock 文件失败: %v", err)
	}

	importedAcc, err := mgr.ImportWorkBuddyLocalAccount(mockFilePath)
	if err != nil {
		t.Fatalf("ImportWorkBuddyLocalAccount 失败: %v", err)
	}
	if importedAcc.ProjectID != "wb-uid-002" {
		t.Errorf("导入账号 UID 期望 wb-uid-002, 得到 %s", importedAcc.ProjectID)
	}
	if importedAcc.Email != "imported@example.com" {
		t.Errorf("导入账号 Email 期望 imported@example.com, 得到 %s", importedAcc.Email)
	}

	// 再次导入同 UID 账号，应更新而非重复新增
	mockContentUpdated := []byte(`{
		"account": {
			"uid": "wb-uid-002",
			"nickname": "imported@example.com"
		},
		"auth": {
			"accessToken": "imported-token-refreshed",
			"refreshToken": "imported-ref-2",
			"expiresIn": 7200
		}
	}`)
	_ = os.WriteFile(mockFilePath, mockContentUpdated, 0644)

	reImported, err := mgr.ImportWorkBuddyLocalAccount(mockFilePath)
	if err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}
	if reImported.ID != importedAcc.ID {
		t.Errorf("二次导入应该更新同一账号 ID (%s), 却生成了新 ID (%s)", importedAcc.ID, reImported.ID)
	}
	if reImported.AccessToken != "imported-token-refreshed" {
		t.Errorf("二次导入后 AccessToken 未更新")
	}
}

func TestWorkBuddyAccount_ResolveModel(t *testing.T) {
	acc := &Account{
		DefaultModel: "deepseek-v4.1-flash",
	}

	if m := ResolveWorkBuddyModel("deepseek-v4.1-flash", acc); m != "deepseek-v4.1-flash" {
		t.Errorf("期望 deepseek-v4.1-flash, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("workbuddy/hy4-preview-f", acc); m != "hy4-preview-f" {
		t.Errorf("期望剥离前缀后得到 hy4-preview-f, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("workbuddy/domestic/hunyuan-2.0-instruct", acc); m != "hunyuan-2.0-instruct" {
		t.Errorf("期望剥离 domestic 前缀后得到 hunyuan-2.0-instruct, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("workbuddy/intl/gpt-5.5", acc); m != "gpt-5.5" {
		t.Errorf("期望剥离 intl 前缀后得到 gpt-5.5, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("workbuddy/cn/minimax-m2.5", acc); m != "minimax-m2.5" {
		t.Errorf("期望剥离 cn 前缀后得到 minimax-m2.5, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("workbuddy/overseas/kimi-k3", acc); m != "kimi-k3" {
		t.Errorf("期望剥离 overseas 前缀后得到 kimi-k3, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("hy3[1M]", acc); m != "hy3" {
		t.Errorf("期望剥离上下文后缀后得到 hy3, 得到 %s", m)
	}
	if m := ResolveWorkBuddyModel("", acc); m != "deepseek-v4.1-flash" {
		t.Errorf("空入参期望回退账号默认模型, 得到 %s", m)
	}
}

func TestWorkBuddy_ModelAffinity(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		// 显式前缀
		{"workbuddy/domestic/hunyuan-chat", "domestic"},
		{"workbuddy/cn/deepseek-v4.1-flash", "domestic"},
		{"workbuddy/intl/gpt-5.5", "international"},
		{"workbuddy/overseas/kimi-k3", "international"},
		// 国内特征
		{"hunyuan-2.0-instruct", "domestic"},
		{"workbuddy/hunyuan-chat", "domestic"},
		{"default-1.1", "domestic"},
		{"default-1.2", "domestic"},
		{"codewise-7b", "domestic"},
		{"minimax-m2.5", "domestic"},
		{"glm-5.1", "domestic"},
		{"glm-5.0", "domestic"},
		{"glm-4-flash", "domestic"},
		{"deepseek-v3", "domestic"},
		{"deepseek-r1", "domestic"},
		// 国际特征
		{"gpt-5.5", "international"},
		{"gpt-5.4", "international"},
		{"gemini-3.5-flash", "international"},
		{"kimi-k3", "international"},
		{"kimi-k2.8", "international"},
		{"hy4-preview-f", "international"},
		{"fast-model", "international"},
		{"balanced-model", "international"},
		{"deep-model", "international"},
		{"primary-model", "international"},
		{"gpt-5.5[128k]", "international"},
		// 通用双端支持
		{"deepseek-v4.1-flash", "any"},
		{"deepseek-v4-flash", "any"},
		{"hy3", "any"},
		{"auto", "any"},
		{"default-model", "any"},
		{"unknown-custom-model", "any"},
	}

	for _, tc := range tests {
		got := GetWorkBuddyModelAffinity(tc.model)
		if got != tc.expected {
			t.Errorf("model %q: expected affinity %q, got %q", tc.model, tc.expected, got)
		}
	}
}

func TestWorkBuddyAccount_DynamicLocalScan(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "wb-scan-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir) // 环境清理 (Teardown)

	origEnv := os.Getenv("LOCALAPPDATA")
	os.Setenv("LOCALAPPDATA", tempDir)
	defer os.Setenv("LOCALAPPDATA", origEnv) // 恢复环境

	authDir := filepath.Join(tempDir, "CodeBuddyExtension", "Data", "Public", "auth")
	if err := os.MkdirAll(authDir, 0755); err != nil {
		t.Fatalf("创建 authDir 失败: %v", err)
	}

	// 1. 写入 .logged-out 状态文件（应被忽略）
	loggedOutFile := filepath.Join(authDir, "workbuddy-desktop-ai.info.logged-out")
	_ = os.WriteFile(loggedOutFile, []byte("logged-out-state"), 0644)

	// 2. 写入旧的凭证文件
	olderFile := filepath.Join(authDir, "workbuddy-desktop-ai.2026-09-17T08-00-00-000Z.111.info")
	_ = os.WriteFile(olderFile, []byte(`{"account":{"uid":"old-uid","nickname":"old@test.com"},"auth":{"accessToken":"old-token"}}`), 0644)
	_ = os.Chtimes(olderFile, time.Now().Add(-10*time.Minute), time.Now().Add(-10*time.Minute))

	// 3. 写入较新的凭证文件
	newerFile := filepath.Join(authDir, "workbuddy-desktop-ai.2026-09-17T09-00-00-000Z.222.info")
	_ = os.WriteFile(newerFile, []byte(`{"account":{"uid":"new-uid","nickname":"new@test.com"},"auth":{"accessToken":"new-token"}}`), 0644)
	_ = os.Chtimes(newerFile, time.Now(), time.Now())

	// 动态扫描应命中最新的凭证文件
	detectedPath := GetDefaultWorkBuddyLocalInfoPath()
	if detectedPath != newerFile {
		t.Errorf("动态凭证探测期望命中最新文件 %s, 实际返回 %s", newerFile, detectedPath)
	}
}

func TestUpdateAccountCooldownFromQuota_WorkBuddyIgnored(t *testing.T) {
	mgr := NewManager()
	mgr.accounts = []*Account{
		{
			ID:            "wb-1",
			Email:         "wb@test.com",
			Provider:      "workbuddy",
			Enabled:       true,
			Cooldowns:     map[string]int64{},
			CooldownUntil: 0,
		},
	}

	// 传入包含 0 剩余（耗尽）的配额桶
	exhaustedBuckets := []QuotaBucket{
		{
			Group:             "WorkBuddy 官方账号",
			ModelID:           "积分余额: 0",
			RemainingFraction: 0,
			RemainPercent:     0,
		},
	}

	changed := mgr.UpdateAccountCooldownFromQuota("wb-1", exhaustedBuckets)
	if changed {
		t.Errorf("期望 WorkBuddy 账号不被配额桶改变冷却，但返回了 changed=true")
	}

	acc := mgr.GetAccountByID("wb-1")
	if acc.CooldownUntil != 0 {
		t.Errorf("期望 CooldownUntil 为 0，实际为 %d", acc.CooldownUntil)
	}
	if len(acc.Cooldowns) != 0 {
		t.Errorf("期望 Cooldowns 为空 map，实际含有: %v", acc.Cooldowns)
	}
}

func TestLoadAccounts_WorkBuddyAutoCleanCooldown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "wb-clean-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir) // 环境恢复 (Teardown)

	// 构造一份带有 gemini 冷却脏数据的 accounts_workbuddy.json
	wbJSON := `{
		"accounts": [
			{
				"id": "wb-dirty-1",
				"email": "dirty@wb.com",
				"provider": "workbuddy",
				"enabled": true,
				"access_token": "valid-token",
				"cooldowns": {
					"gemini": 1789649552185
				},
				"cooldownUntil": 1789649552185
			}
		]
	}`
	wbFile := filepath.Join(tempDir, "accounts_workbuddy.json")
	if err := os.WriteFile(wbFile, []byte(wbJSON), 0644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}

	mgr := NewManager()
	mgr.Init(tempDir)

	acc := mgr.GetAccountByID("wb-dirty-1")
	if acc == nil {
		t.Fatalf("未成功载入测试账号 wb-dirty-1")
	}
	if acc.CooldownUntil != 0 {
		t.Errorf("期望自动清洗后 CooldownUntil 为 0，实际仍为 %d", acc.CooldownUntil)
	}
	if _, hasGemini := acc.Cooldowns["gemini"]; hasGemini {
		t.Errorf("期望自动清洗后 gemini 冷却被剔除，但仍存在")
	}
}

func TestWorkBuddy_DomesticEmailDetection(t *testing.T) {
	domesticList := []string{
		"test@qq.com", "vip@vip.qq.com", "user@foxmail.com",
		"work@163.com", "dev@126.com", "test@yeah.net",
		"admin@sina.com", "news@sina.cn", "contact@sohu.com",
		"corp@aliyun.com", "phone@139.com", "test@189.com",
		"user@wo.cn", "corp@company.cn", "company@edu.cn",
	}
	for _, em := range domesticList {
		if !IsDomesticEmail(em) {
			t.Errorf("IsDomesticEmail(%q) 期望为 true, 实际为 false", em)
		}
	}

	foreignList := []string{
		"test@gmail.com", "corp@google.com", "user@outlook.com",
		"dev@hotmail.com", "admin@yahoo.com", "support@icloud.com",
		"sec@proton.me", "dev@company.org", "test@nexus.io",
	}
	for _, em := range foreignList {
		if IsDomesticEmail(em) {
			t.Errorf("IsDomesticEmail(%q) 期望为 false, 实际为 true", em)
		}
	}

	// 测试账号仅有昵称，但 AccessToken (JWT) 包含国内邮箱
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"preferred_username":"weilimao","email":"2729547911@qq.com"}`))
	dummyJWT := "eyJhbGciOiJub25lIn0." + payload + ".dummySig"

	accDomesticJWT := &Account{
		Provider:    "workbuddy",
		Email:       "weilimao",
		AccessToken: dummyJWT,
	}
	if !IsWorkBuddyDomesticAccount(accDomesticJWT) {
		t.Errorf("从 JWT 解析 QQ 邮箱期望判定为国内账号，实际判定为 false")
	}

	// 测试国外 Gmail 账号
	accForeign := &Account{
		Provider: "workbuddy",
		Email:    "weilimao0714@gmail.com",
	}
	if IsWorkBuddyDomesticAccount(accForeign) {
		t.Errorf("Gmail 账号期望判定为国外账号(false)，实际判定为 true")
	}
}

func TestWorkBuddy_DomesticDualVersion(t *testing.T) {
	// 1. 测试解析国内版凭证
	domesticJSON := []byte(`{
		"account": {
			"uid": "87582519-8c36-47f2-b7d7-9865e721225a",
			"nickname": "会游的鱼",
			"phoneNumber": "18776859497"
		},
		"auth": {
			"accessToken": "test-domestic-token",
			"refreshToken": "test-domestic-ref",
			"expiresIn": 4751998,
			"domain": "www.workbuddy.cn"
		}
	}`)

	input, err := ParseWorkBuddyAuthInfo(domesticJSON)
	if err != nil {
		t.Fatalf("ParseWorkBuddyAuthInfo 国内版报错: %v", err)
	}

	if input.BaseURL != DefaultWorkBuddyDomesticBaseURL {
		t.Errorf("国内版 BaseURL 期望 %s, 得到 %s", DefaultWorkBuddyDomesticBaseURL, input.BaseURL)
	}
	if input.Nickname != "会游的鱼" {
		t.Errorf("国内版 Nickname 期望 会游的鱼, 得到 %s", input.Nickname)
	}
	if input.Label != "会游的鱼 (国内版)" {
		t.Errorf("国内版 Label 期望 会游的鱼 (国内版), 得到 %s", input.Label)
	}

	// 2. 测试账号 IsWorkBuddyDomestic 与 BuildWorkBuddyHeaders
	accDomestic := NewWorkBuddyAccount(*input)
	if !accDomestic.IsWorkBuddyDomestic() {
		t.Fatalf("国内版账号 IsWorkBuddyDomestic 期望 true, 实际得到 false")
	}

	domHeaders := BuildWorkBuddyHeaders(accDomestic)
	if domHeaders["X-IDE-Type"] != "WorkBuddy" {
		t.Errorf("国内版 X-IDE-Type 期望 WorkBuddy, 得到 %s", domHeaders["X-IDE-Type"])
	}
	if domHeaders["X-Domain"] != "www.workbuddy.cn" {
		t.Errorf("国内版 X-Domain 期望 www.workbuddy.cn, 得到 %s", domHeaders["X-Domain"])
	}
	if domHeaders["X-User-Id"] != "87582519-8c36-47f2-b7d7-9865e721225a" {
		t.Errorf("国内版 X-User-Id 期望 87582519-8c36-47f2-b7d7-9865e721225a, 得到 %s", domHeaders["X-User-Id"])
	}
	if domHeaders["Authorization"] != "Bearer test-domestic-token" {
		t.Errorf("国内版 Authorization 期望 Bearer test-domestic-token, 得到 %s", domHeaders["Authorization"])
	}

	// 3. 国际版账号 Headers
	accIntl := &Account{
		Provider:    "workbuddy",
		BaseURL:     DefaultWorkBuddyBaseURL,
		AccessToken: "test-intl-token",
		ProjectID:   "intl-uid-123",
	}
	if accIntl.IsWorkBuddyDomestic() {
		t.Fatalf("国际版账号 IsWorkBuddyDomestic 期望 false, 实际得到 true")
	}
	intlHeaders := BuildWorkBuddyHeaders(accIntl)
	if intlHeaders["X-IDE-Type"] != "CodeBuddy" {
		t.Errorf("国际版 X-IDE-Type 期望 CodeBuddy, 得到 %s", intlHeaders["X-IDE-Type"])
	}
	if _, hasDomain := intlHeaders["X-Domain"]; hasDomain {
		t.Errorf("国际版不应包含 X-Domain")
	}

	// 4. 扫描功能测试 (带隔离 Teardown)
	tempDir := t.TempDir()
	authDir := filepath.Join(tempDir, "CodeBuddyExtension", "Data", "Public", "auth")
	if err := os.MkdirAll(authDir, 0755); err != nil {
		t.Fatalf("创建临时 authDir 失败: %v", err)
	}

	f1 := filepath.Join(authDir, "workbuddy-desktop-ai.info")
	f2 := filepath.Join(authDir, "workbuddy-desktop.info")
	_ = os.WriteFile(f1, []byte("{}"), 0644)
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(f2, []byte("{}"), 0644)

	t.Setenv("LOCALAPPDATA", tempDir)

	paths := ScanAllWorkBuddyLocalInfoPaths()
	if len(paths) < 2 {
		t.Fatalf("期望扫描出至少 2 个凭证文件，实际得到 %d", len(paths))
	}
	if paths[0] != f2 {
		t.Errorf("期望最新的文件排第一 (workbuddy-desktop.info), 实际得到 %s", paths[0])
	}
}

func TestWorkBuddyAccount_SanitizeCodeBuddyURL(t *testing.T) {
	// 1. NewWorkBuddyAccount 自动清洗 codebuddy.ai 为 workbuddy.ai
	acc := NewWorkBuddyAccount(WorkBuddyAccountInput{
		BaseURL:     "https://www.codebuddy.ai",
		AccessToken: "test-token",
		Nickname:    "intl-user",
	})
	if acc.BaseURL != DefaultWorkBuddyBaseURL {
		t.Errorf("NewWorkBuddyAccount 期望自动纠正 BaseURL 为 %s, 实际为 %s", DefaultWorkBuddyBaseURL, acc.BaseURL)
	}

	// 2. ClearAccountCooldown 解除 WorkBuddy 账号冷却测试
	mgr := NewManager()
	testAcc := &Account{
		ID:            "wb-test-1",
		Email:         "test-overseas@example.com",
		Provider:      "workbuddy",
		BaseURL:       "https://www.codebuddy.ai",
		Cooldowns:     map[string]int64{"workbuddy": 9999999999999},
		CooldownUntil: 9999999999999,
	}
	mgr.AddAccount(testAcc)
	if !mgr.ClearAccountCooldown("wb-test-1") {
		t.Fatalf("ClearAccountCooldown 期望成功清除冷却并返回 true")
	}
	gotAcc := mgr.GetAccountByID("wb-test-1")
	if gotAcc == nil {
		t.Fatalf("未能找到账号 wb-test-1")
	}
	if gotAcc.CooldownUntil != 0 || len(gotAcc.Cooldowns) != 0 {
		t.Errorf("期望 CooldownUntil 为 0 且 Cooldowns 清空，实际 CooldownUntil=%d, Cooldowns=%v", gotAcc.CooldownUntil, gotAcc.Cooldowns)
	}
}

