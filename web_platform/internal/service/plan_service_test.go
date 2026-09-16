package service

import (
	"reflect"
	"testing"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
)

func TestPlanAutoModelsPersistence(t *testing.T) {
	teardownEnv := setupTestEnvironment(t)
	defer teardownEnv()

	planSvc := NewPlanService()

	// 1. 创建包含 AutoModels 的测试套餐
	testPlan := &model.Plan{
		Name:          "自动化测试-Auto说明套餐",
		Description:   "验证 auto 模型包含说明的持久化",
		PriceCents:    5900,
		DurationDays:  30,
		AllowedModels: []string{"auto", "claude-3-7-sonnet"},
		AutoModels:    []string{"claude-3-7-sonnet", "gemini-2.5-flash", "deepseek-chat"},
		RateLimit:     60,
		Status:        "active",
	}

	if err := planSvc.CreatePlan(testPlan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// 确保测试退出时数据彻底清理 (Teardown)
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.Plan{}, testPlan.ID).Error
	})

	if testPlan.ID == 0 {
		t.Fatalf("expected non-zero testPlan.ID after creation")
	}

	// 2. 从数据库重新读取并验证 AutoModels
	fetched, err := planSvc.GetPlanByID(testPlan.ID)
	if err != nil {
		t.Fatalf("GetPlanByID failed: %v", err)
	}
	expectedModels := []string{"claude-3-7-sonnet", "gemini-2.5-flash", "deepseek-chat"}
	if !reflect.DeepEqual(fetched.AutoModels, expectedModels) {
		t.Fatalf("expected AutoModels %v, got %v", expectedModels, fetched.AutoModels)
	}

	// 3. 测试通过 UpdatePlan 更新 AutoModels
	updatedAutoModels := []string{"gpt-4o", "claude-3-7-sonnet", "gemini-2.5-pro"}
	updateReq := *fetched
	updateReq.AutoModels = updatedAutoModels
	if err := planSvc.UpdatePlan(testPlan.ID, &updateReq); err != nil {
		t.Fatalf("UpdatePlan failed: %v", err)
	}

	// 4. 再次验证持久化后的新 AutoModels
	reFetched, err := planSvc.GetPlanByID(testPlan.ID)
	if err != nil {
		t.Fatalf("GetPlanByID after update failed: %v", err)
	}
	if !reflect.DeepEqual(reFetched.AutoModels, updatedAutoModels) {
		t.Fatalf("expected updated AutoModels %v, got %v", updatedAutoModels, reFetched.AutoModels)
	}

	// 5. 验证删除与彻底清理
	if err := planSvc.DeletePlan(testPlan.ID); err != nil {
		t.Fatalf("DeletePlan failed: %v", err)
	}
	afterDelete, err := planSvc.GetPlanByID(testPlan.ID)
	if err == nil || afterDelete != nil {
		t.Fatalf("expected plan to be deleted, but still found")
	}
}

func TestUpdatePlanCascadeSyncAPIKeys(t *testing.T) {
	teardownEnv := setupTestEnvironment(t)
	defer teardownEnv()

	planSvc := NewPlanService()

	// 1. 创建测试套餐，包含初始模型 AllowedModels: ["auto", "claude-3-7-sonnet"]
	initialModels := []string{"auto", "claude-3-7-sonnet"}
	testPlan := &model.Plan{
		Name:          "自动化测试-模型同步套餐",
		Description:   "验证修改套餐模型级联同步更新名下已有 API Key",
		PriceCents:    9900,
		DurationDays:  30,
		AllowedModels: initialModels,
		RateLimit:     30,
		Status:        "active",
	}
	if err := planSvc.CreatePlan(testPlan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.Plan{}, testPlan.ID).Error
	})

	// 2. 创建订阅该套餐的测试用户
	testUser := &model.User{
		Username:     "cascade_sync_user",
		Email:        "cascade@example.com",
		Role:         "user",
		Status:       "active",
		PlanID:       &testPlan.ID,
		PlanExpireAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	if err := database.DB.Create(testUser).Error; err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.User{}, testUser.ID).Error
	})

	// 3. 为该用户创建初始 API Key，模型白名单继承套餐初始配置
	initialKey := &model.APIKey{
		Key:           "sk-ant-test-cascade-sync-12345",
		UserID:        testUser.ID,
		Name:          "同步测试密钥",
		AllowedModels: initialModels,
		RateLimit:     30,
		Status:        "active",
	}
	if err := database.DB.Create(initialKey).Error; err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.APIKey{}, initialKey.ID).Error
	})

	// 4. 管理员修改套餐模型，变更为新的模型白名单 ["auto", "gemini-2.5-pro", "deepseek-chat"]
	updatedModels := []string{"auto", "gemini-2.5-pro", "deepseek-chat"}
	fetchedPlan, err := planSvc.GetPlanByID(testPlan.ID)
	if err != nil {
		t.Fatalf("GetPlanByID failed: %v", err)
	}
	updateReq := *fetchedPlan
	updateReq.AllowedModels = updatedModels
	updateReq.RateLimit = 60

	if err := planSvc.UpdatePlan(testPlan.ID, &updateReq); err != nil {
		t.Fatalf("UpdatePlan failed: %v", err)
	}

	// 5. 验证已建 API Key 的 AllowedModels 与 RateLimit 是否被自动级联同步更新
	var refreshedKey model.APIKey
	if err := database.DB.First(&refreshedKey, initialKey.ID).Error; err != nil {
		t.Fatalf("Failed to fetch refreshed API Key: %v", err)
	}

	if !reflect.DeepEqual(refreshedKey.AllowedModels, updatedModels) {
		t.Fatalf("expected API Key AllowedModels %v, got %v", updatedModels, refreshedKey.AllowedModels)
	}
	if refreshedKey.RateLimit != 60 {
		t.Fatalf("expected API Key RateLimit 60, got %d", refreshedKey.RateLimit)
	}
}

func TestPlanTokenLimitPersistence(t *testing.T) {
	teardownEnv := setupTestEnvironment(t)
	defer teardownEnv()

	planSvc := NewPlanService()

	// 1. 创建包含 TokenLimit 的测试套餐
	testPlan := &model.Plan{
		Name:          "自动化测试-Token限制套餐",
		Description:   "验证 Token 额度上限持久化与 API Key 联动继承",
		PriceCents:    2900,
		DurationDays:  30,
		AllowedModels: []string{"auto", "claude-3-7-sonnet"},
		TokenLimit:    5000000,
		RateLimit:     30,
		Status:        "active",
	}
	if err := planSvc.CreatePlan(testPlan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.Plan{}, testPlan.ID).Error
	})

	// 2. 查询并验证 TokenLimit
	fetched, err := planSvc.GetPlanByID(testPlan.ID)
	if err != nil {
		t.Fatalf("GetPlanByID failed: %v", err)
	}
	if fetched.TokenLimit != 5000000 {
		t.Fatalf("expected TokenLimit 5000000, got %d", fetched.TokenLimit)
	}

	// 3. 更新 TokenLimit
	updateReq := *fetched
	updateReq.TokenLimit = 10000000
	if err := planSvc.UpdatePlan(testPlan.ID, &updateReq); err != nil {
		t.Fatalf("UpdatePlan failed: %v", err)
	}
	reFetched, err := planSvc.GetPlanByID(testPlan.ID)
	if err != nil {
		t.Fatalf("GetPlanByID after update failed: %v", err)
	}
	if reFetched.TokenLimit != 10000000 {
		t.Fatalf("expected updated TokenLimit 10000000, got %d", reFetched.TokenLimit)
	}

	// 4. 创建订阅该套餐的用户并创建 API Key，验证 CreateKey 自动继承 TokenLimit
	testUser := &model.User{
		Username:     "token_limit_user",
		Email:        "token_limit@example.com",
		Role:         "user",
		Status:       "active",
		PlanID:       &testPlan.ID,
		PlanExpireAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	if err := database.DB.Create(testUser).Error; err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.User{}, testUser.ID).Error
	})

	keySvc := NewKeyService()
	createdKey, err := keySvc.CreateKey(testUser.ID, "Token测试Key", nil)
	if err != nil {
		t.Fatalf("CreateKey failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.DB.Unscoped().Delete(&model.APIKey{}, createdKey.ID).Error
	})

	if createdKey.LimitTokens != 10000000 {
		t.Fatalf("expected created API Key to inherit LimitTokens 10000000, got %d", createdKey.LimitTokens)
	}

	// 5. 调整套餐 TokenLimit 并调用 ActivatePlan，验证名下已有 Key 的 LimitTokens 自动同步
	reFetched.TokenLimit = 20000000
	if err := planSvc.UpdatePlan(testPlan.ID, reFetched); err != nil {
		t.Fatalf("UpdatePlan failed: %v", err)
	}
	if err := planSvc.ActivatePlan(testUser.ID, testPlan.ID); err != nil {
		t.Fatalf("ActivatePlan failed: %v", err)
	}

	var syncedKey model.APIKey
	if err := database.DB.First(&syncedKey, createdKey.ID).Error; err != nil {
		t.Fatalf("fetch synced API Key failed: %v", err)
	}
	if syncedKey.LimitTokens != 20000000 {
		t.Fatalf("expected synced API Key LimitTokens 20000000, got %d", syncedKey.LimitTokens)
	}
}
