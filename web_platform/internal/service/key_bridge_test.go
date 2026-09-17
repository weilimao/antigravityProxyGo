package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
)

func TestKeyService_BridgeAndStrictModels(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	db := database.DB

	// 1. 构造 Mock 18444 Relay 网关服务
	var lastCreatedAllowedModels []string
	var userSyncCalled bool
	var keyCreateCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/admin/users/sync":
			userSyncCalled = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true,"user":{"id":"u1","key":"tester_bob"}}`))
		case "/api/admin/keys/create":
			keyCreateCalled = true
			var req struct {
				Username      string   `json:"username"`
				Name          string   `json:"name"`
				AllowedModels []string `json:"allowedModels"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			lastCreatedAllowedModels = req.AllowedModels
			keyStr := fmt.Sprintf("sk-ant-mocked-%d", time.Now().UnixNano())
			w.WriteHeader(http.StatusOK)
			respBytes, _ := json.Marshal(map[string]interface{}{
				"success": true,
				"key": map[string]interface{}{
					"id":            "k1",
					"key":           keyStr,
					"allowedModels": req.AllowedModels,
				},
			})
			w.Write(respBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	// 2. 配置网关地址指向 Mock Server
	origCfg := config.GlobalConfig
	config.GlobalConfig.Gateway = config.GoRelayGatewayConfig{
		GatewayURL: ts.URL,
		AdminKey:   "sk-ant-admin",
	}
	t.Cleanup(func() {
		config.GlobalConfig = origCfg
	})

	// 3. 创建测试套餐: 管理员仅允许 gemini-2.5-flash 与 deepseek-chat
	plan := model.Plan{
		Name:          "标准极客套餐",
		Description:   "专享测试",
		PriceCents:    9900,
		DurationDays:  30,
		AllowedModels: []string{"gemini-2.5-flash", "deepseek-chat"},
		RateLimit:     60,
		Status:        "active",
	}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("create plan failed: %v", err)
	}

	// 4. 注册用户 (验证自动向网关同步用户)
	authSvc := NewAuthService()
	user, _, err := authSvc.Register("tester_bob", "bob@example.com", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if !userSyncCalled {
		t.Errorf("expected user sync to be called during registration")
	}

	// 关联套餐
	user.PlanID = &plan.ID
	db.Save(user)

	// 5. 创建 API Key，尝试传入包含 auto、套餐允许模型 gemini-2.5-flash 与未经授权的模型 claude-3-7-sonnet
	keySvc := NewKeyService()
	customAttempt := []string{"auto", "gemini-2.5-flash", "claude-3-7-sonnet"}
	createdKey, err := keySvc.CreateKey(user.ID, "Bob Key", customAttempt)
	if err != nil {
		t.Fatalf("CreateKey failed: %v", err)
	}
	if !keyCreateCalled {
		t.Errorf("expected key create on gateway to be called")
	}

	// 6. 严格断言模型白名单:
	// a. 必须包含 auto (显式勾选)
	// b. 必须包含 gemini-2.5-flash (套餐允许模型)
	// c. 坚决不能包含 claude-3-7-sonnet (非管理员后台配置模型被剔除)
	hasAuto := false
	hasFlash := false
	hasClaude := false
	for _, m := range createdKey.AllowedModels {
		if m == "auto" {
			hasAuto = true
		}
		if m == "gemini-2.5-flash" {
			hasFlash = true
		}
		if m == "claude-3-7-sonnet" {
			hasClaude = true
		}
	}

	if !hasAuto {
		t.Errorf("allowedModels must contain 'auto', got %v", createdKey.AllowedModels)
	}
	if !hasFlash {
		t.Errorf("allowedModels must contain 'gemini-2.5-flash', got %v", createdKey.AllowedModels)
	}
	if hasClaude {
		t.Errorf("unauthorized model 'claude-3-7-sonnet' must be filtered out, got %v", createdKey.AllowedModels)
	}

	// 验证向网关提交的入参也严格受限
	for _, m := range lastCreatedAllowedModels {
		if m == "claude-3-7-sonnet" {
			t.Errorf("gateway payload contained unauthorized model 'claude-3-7-sonnet'")
		}
	}

	// 7. 测试未显式勾选 auto 时，Key 中绝不包含 auto
	keyWithoutAuto, err := keySvc.CreateKey(user.ID, "Bob Key Without Auto", []string{"gemini-2.5-flash"})
	if err != nil {
		t.Fatalf("CreateKey without auto failed: %v", err)
	}
	for _, m := range keyWithoutAuto.AllowedModels {
		if m == "auto" {
			t.Errorf("key created without auto should not contain 'auto', got %v", keyWithoutAuto.AllowedModels)
		}
	}
}

func TestKeyService_SyncUsageFromRelay(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	db := database.DB

	mockKeyStr := "sk-ant-mock-usage-sync-test-key"
	var keysUsageCalled bool

	// 1. Mock 18444 Relay 网关服务
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/admin/users/keys-usage" {
			keysUsageCalled = true
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{
				"success":  true,
				"username": "tester_usage_bob",
				"usages": map[string]int64{
					mockKeyStr: 88888,
				},
				"totalUsed": 88888,
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	origCfg := config.GlobalConfig
	config.GlobalConfig.Gateway = config.GoRelayGatewayConfig{
		GatewayURL: ts.URL,
		AdminKey:   "sk-ant-admin",
	}
	t.Cleanup(func() {
		config.GlobalConfig = origCfg
	})

	// 2. 创建本地测试用户与 API Key
	user := model.User{
		Username: "tester_usage_bob",
		Email:    "usage_bob@example.com",
		Role:     "user",
		Status:   "active",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Unscoped().Delete(&model.User{}, user.ID).Error
	})

	key := model.APIKey{
		Key:           mockKeyStr,
		UserID:        user.ID,
		Name:          "用量同步测试Key",
		AllowedModels: []string{"auto"},
		UsedTokens:    0,
		Status:        "active",
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("create api key failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Unscoped().Delete(&model.APIKey{}, key.ID).Error
	})

	// 3. 执行 ListKeys 查询，验证自动触发从 Relay 拉取最新用量并同步本地 DB
	keySvc := NewKeyService()
	keys, err := keySvc.ListKeys(user.ID)
	if err != nil {
		t.Fatalf("ListKeys failed: %v", err)
	}
	if !keysUsageCalled {
		t.Fatalf("expected keys-usage API on relay to be called")
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key returned, got %d", len(keys))
	}
	if keys[0].UsedTokens != 88888 {
		t.Fatalf("expected key UsedTokens to be updated to 88888, got %d", keys[0].UsedTokens)
	}

	// 4. 再次验证本地数据库中记录是否已被持久化更新
	var refreshed model.APIKey
	if err := db.First(&refreshed, key.ID).Error; err != nil {
		t.Fatalf("fetch key from db failed: %v", err)
	}
	if refreshed.UsedTokens != 88888 {
		t.Fatalf("expected db UsedTokens 88888, got %d", refreshed.UsedTokens)
	}
}
