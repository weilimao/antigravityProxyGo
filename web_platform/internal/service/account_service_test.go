package service

import (
	"testing"

	"antigravity-web-platform/internal/model"
)

func TestAccountService_PoolConfig(t *testing.T) {
	// 使用 t.TempDir() 构建全隔离沙箱，测试完毕后自动 Teardown 清理
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	// 1. 获取初始默认配置
	data, err := svc.GetAccountsData()
	if err != nil {
		t.Fatalf("GetAccountsData 失败: %v", err)
	}
	if data.Config == nil {
		t.Fatal("期望 Config 不为 nil")
	}
	if data.Config.NvidiaMaxConcurrency != 40 {
		t.Errorf("期望默认并发为 40，得到 %d", data.Config.NvidiaMaxConcurrency)
	}

	// 2. 修改号池配置并保存
	data.Config.NvidiaLBMode = "sticky"
	data.Config.NvidiaMaxConcurrency = 60
	data.Config.OtherLBModes["aliyun"] = "round-robin"
	if err := svc.SavePoolConfig(data.Config); err != nil {
		t.Fatalf("SavePoolConfig 失败: %v", err)
	}

	// 3. 重新创建实例读取以验证磁盘持久化
	svc2 := NewAccountServiceWithDir(tempDir)
	data2, err := svc2.GetAccountsData()
	if err != nil {
		t.Fatalf("svc2 GetAccountsData 失败: %v", err)
	}
	if data2.Config.NvidiaLBMode != "sticky" {
		t.Errorf("期望 NvidiaLBMode 为 sticky，得到 %s", data2.Config.NvidiaLBMode)
	}
	if data2.Config.NvidiaMaxConcurrency != 60 {
		t.Errorf("期望 NvidiaMaxConcurrency 为 60，得到 %d", data2.Config.NvidiaMaxConcurrency)
	}
	if data2.Config.OtherLBModes["aliyun"] != "round-robin" {
		t.Errorf("期望 aliyun LBMode 为 round-robin，得到 %s", data2.Config.OtherLBModes["aliyun"])
	}
}

func TestAccountService_AccountCRUD(t *testing.T) {
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	// 1. 添加一个 NVIDIA 账号
	acc1, err := svc.AddAccount(&model.AddAccountRequest{
		Provider:    "nvidia",
		Email:       "test-nv@example.com",
		AccessToken: "nvapi-12345678abcdef",
	})
	if err != nil {
		t.Fatalf("AddAccount 失败: %v", err)
	}
	if acc1.ID == "" {
		t.Fatal("期望生成唯一 ID")
	}
	if acc1.BaseURL != "https://integrate.api.nvidia.com/v1" {
		t.Errorf("期望默认 BaseURL，得到 %s", acc1.BaseURL)
	}
	if acc1.MaskedKey != "nvap****cdef" {
		t.Errorf("期望脱敏 Key 为 nvap****cdef，得到 %s", acc1.MaskedKey)
	}

	// 2. 添加一个 Other 账号
	acc2, err := svc.AddAccount(&model.AddAccountRequest{
		Provider:     "other",
		Email:        "test-other@example.com",
		AccessToken:  "sk-testotherkey123456",
		GroupID:      "aliyun",
		GroupName:    "阿里云",
		Formats:      []string{"openai"},
		DefaultModel: "deepseek-v3",
	})
	if err != nil {
		t.Fatalf("AddAccount Other 失败: %v", err)
	}

	// 3. 读取全量数据验证账号与 Other 分组提取
	data, err := svc.GetAccountsData()
	if err != nil {
		t.Fatalf("GetAccountsData 失败: %v", err)
	}
	if len(data.Accounts) != 2 {
		t.Fatalf("期望共有 2 个账号，实际得到 %d", len(data.Accounts))
	}
	if len(data.Groups) != 1 || data.Groups[0].GroupID != "aliyun" {
		t.Fatalf("期望正确提取 aliyun 分组，得到: %+v", data.Groups)
	}

	// 4. 更新账号
	newBaseURL := "https://custom.nvidia.com/v1"
	updated, err := svc.UpdateAccount(acc1.ID, &model.UpdateAccountRequest{
		BaseURL: newBaseURL,
	})
	if err != nil {
		t.Fatalf("UpdateAccount 失败: %v", err)
	}
	if updated.BaseURL != newBaseURL {
		t.Errorf("期望 BaseURL 为 %s，得到 %s", newBaseURL, updated.BaseURL)
	}

	// 5. 启停 Toggle
	if err := svc.ToggleAccount(acc1.ID, false); err != nil {
		t.Fatalf("ToggleAccount 失败: %v", err)
	}

	// 6. 删除账号
	if err := svc.DeleteAccount(acc1.ID); err != nil {
		t.Fatalf("DeleteAccount 失败: %v", err)
	}
	dataAfterDel, _ := svc.GetAccountsData()
	if len(dataAfterDel.Accounts) != 1 || dataAfterDel.Accounts[0].ID != acc2.ID {
		t.Fatalf("期望仅剩 acc2，实际得到 %d 个账号", len(dataAfterDel.Accounts))
	}
}

func TestAccountService_BatchDelete(t *testing.T) {
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	a1, _ := svc.AddAccount(&model.AddAccountRequest{Provider: "nvidia", Email: "b1@test.com"})
	a2, _ := svc.AddAccount(&model.AddAccountRequest{Provider: "nvidia", Email: "b2@test.com"})
	a3, _ := svc.AddAccount(&model.AddAccountRequest{Provider: "nvidia", Email: "b3@test.com"})

	deleted, err := svc.BatchDeleteAccounts([]string{a1.ID, a2.ID})
	if err != nil {
		t.Fatalf("BatchDeleteAccounts 失败: %v", err)
	}
	if deleted != 2 {
		t.Errorf("期望删除 2 条，得到 %d", deleted)
	}

	data, _ := svc.GetAccountsData()
	if len(data.Accounts) != 1 || data.Accounts[0].ID != a3.ID {
		t.Fatalf("期望仅剩 a3，实际得到: %d", len(data.Accounts))
	}
}

func TestAccountService_ImportExport(t *testing.T) {
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	// 批量导入
	incoming := []*model.AccountDTO{
		{Provider: "nvidia", Email: "imp1@test.com", AccessToken: "key-111111111"},
		{Provider: "nvidia", Email: "imp2@test.com", AccessToken: "key-222222222"},
		{Provider: "other", Email: "imp3@test.com", AccessToken: "key-333333333", GroupID: "bitdeer"},
	}

	imported, err := svc.ImportAccounts(incoming)
	if err != nil {
		t.Fatalf("ImportAccounts 失败: %v", err)
	}
	if imported != 3 {
		t.Errorf("期望成功导入 3 条，得到 %d", imported)
	}

	// 重复导入（验证去重）
	dupImported, err := svc.ImportAccounts(incoming)
	if err != nil {
		t.Fatalf("重复 ImportAccounts 失败: %v", err)
	}
	if dupImported != 0 {
		t.Errorf("期望去重后导入 0 条，得到 %d", dupImported)
	}

	// 导出测试
	nvList, err := svc.ExportAccounts("nvidia")
	if err != nil {
		t.Fatalf("ExportAccounts nvidia 失败: %v", err)
	}
	if len(nvList) != 2 {
		t.Errorf("期望导出 2 个 nvidia 账号，得到 %d", len(nvList))
	}

	allList, err := svc.ExportAccounts("all")
	if err != nil {
		t.Fatalf("ExportAccounts all 失败: %v", err)
	}
	if len(allList) != 3 {
		t.Errorf("期望导出 3 个全部账号，得到 %d", len(allList))
	}
}

func TestAccountService_WorkBuddyPoolConfig(t *testing.T) {
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	// 1. 读取默认配置
	data, err := svc.GetAccountsData()
	if err != nil {
		t.Fatalf("GetAccountsData 失败: %v", err)
	}
	if data.Config.WorkbuddyLBMode != "round-robin" {
		t.Errorf("期望默认 WorkbuddyLBMode 为 round-robin，实际得到 %s", data.Config.WorkbuddyLBMode)
	}
	if data.Config.WorkbuddyMaxConcurrency != 10 {
		t.Errorf("期望默认 WorkbuddyMaxConcurrency 为 10，实际得到 %d", data.Config.WorkbuddyMaxConcurrency)
	}

	// 2. 修改并持久化
	data.Config.WorkbuddyLBMode = "sticky"
	data.Config.WorkbuddyMaxConcurrency = 30
	if err := svc.SavePoolConfig(data.Config); err != nil {
		t.Fatalf("SavePoolConfig 失败: %v", err)
	}

	// 3. 验证重新加载
	svc2 := NewAccountServiceWithDir(tempDir)
	data2, err := svc2.GetAccountsData()
	if err != nil {
		t.Fatalf("svc2 GetAccountsData 失败: %v", err)
	}
	if data2.Config.WorkbuddyLBMode != "sticky" {
		t.Errorf("期望 WorkbuddyLBMode 为 sticky，实际得到 %s", data2.Config.WorkbuddyLBMode)
	}
	if data2.Config.WorkbuddyMaxConcurrency != 30 {
		t.Errorf("期望 WorkbuddyMaxConcurrency 为 30，实际得到 %d", data2.Config.WorkbuddyMaxConcurrency)
	}
}

func TestAccountService_WorkBuddyAccountCRUD(t *testing.T) {
	tempDir := t.TempDir()
	svc := NewAccountServiceWithDir(tempDir)

	// 1. 添加 WorkBuddy 账号
	acc, err := svc.AddAccount(&model.AddAccountRequest{
		Provider:    "workbuddy",
		Email:       "test-wb@workbuddy.ai",
		AccessToken: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
	})
	if err != nil {
		t.Fatalf("AddAccount workbuddy 失败: %v", err)
	}
	if acc.BaseURL != "https://www.codebuddy.ai" {
		t.Errorf("期望默认 BaseURL 为 https://www.codebuddy.ai，实际为 %s", acc.BaseURL)
	}
	if acc.Tier != "Free" {
		t.Errorf("期望默认 Tier 为 Free，实际为 %s", acc.Tier)
	}

	// 2. 读取全量账号
	data, err := svc.GetAccountsData()
	if err != nil {
		t.Fatalf("GetAccountsData 失败: %v", err)
	}
	if len(data.Accounts) != 1 {
		t.Fatalf("期望共有 1 个账号，实际为 %d", len(data.Accounts))
	}
	if data.Accounts[0].Provider != "workbuddy" {
		t.Errorf("期望 provider 为 workbuddy，实际为 %s", data.Accounts[0].Provider)
	}

	// 3. 更新账号
	updated, err := svc.UpdateAccount(acc.ID, &model.UpdateAccountRequest{
		Email: "updated-wb@workbuddy.ai",
		Tier:  "Pro",
	})
	if err != nil {
		t.Fatalf("UpdateAccount 失败: %v", err)
	}
	if updated.Email != "updated-wb@workbuddy.ai" || updated.Tier != "Pro" {
		t.Errorf("更新账号数据不符合期望: %+v", updated)
	}

	// 4. 启停账号
	if err := svc.ToggleAccount(acc.ID, false); err != nil {
		t.Fatalf("ToggleAccount 失败: %v", err)
	}
	dataAfterToggle, _ := svc.GetAccountsData()
	if dataAfterToggle.Accounts[0].Enabled {
		t.Errorf("期望账号已停用，实际仍为启用")
	}

	// 5. 导出与批量导入
	wbList, err := svc.ExportAccounts("workbuddy")
	if err != nil || len(wbList) != 1 {
		t.Fatalf("ExportAccounts workbuddy 失败: len=%d, err=%v", len(wbList), err)
	}

	// 6. 删除账号
	if err := svc.DeleteAccount(acc.ID); err != nil {
		t.Fatalf("DeleteAccount 失败: %v", err)
	}
	dataAfterDel, _ := svc.GetAccountsData()
	if len(dataAfterDel.Accounts) != 0 {
		t.Errorf("期望删除后账号数为 0，实际为 %d", len(dataAfterDel.Accounts))
	}
}
