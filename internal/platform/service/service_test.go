package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"antigravity-proxy/internal/platform/config"
	"antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/platform/model"
	"antigravity-proxy/internal/platform/pkg/crypto"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestEnvironment(t *testing.T) func() {
	tempDir, err := os.MkdirTemp("", "antigravity_web_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "test.db")

	testDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_ = testDB.AutoMigrate(
		&model.User{},
		&model.Plan{},
		&model.Order{},
		&model.APIKey{},
		&model.Setting{},
	)

	db.GlobalDB = testDB

	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			JWTSecret:        "test-secret-key-12345",
			TokenExpireHours: 24,
		},
		Payment: config.RelayPaymentConfig{
			RelaySecret: "test-relay-secret-888",
			NotifyURL:   "http://localhost:8080/api/v1/pay/notify/relay",
			ReturnURL:   "http://localhost:8080/#/dashboard",
		},
	}

	teardown := func() {
		if db.GlobalDB != nil {
			sqlDB, _ := db.GlobalDB.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
		_ = os.RemoveAll(tempDir)
	}

	return teardown
}

func TestAuthAndUserLifecycle(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	authSvc := NewAuthService()

	// 1. 注册新用户
	user, token, err := authSvc.Register("test_developer", "dev@example.com", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if user.ID == 0 || token == "" {
		t.Fatalf("expected valid user ID and token, got ID=%d, token empty=%v", user.ID, token == "")
	}

	// 2. 验证密码校验
	if !user.CheckPassword("password123") {
		t.Fatalf("password verification failed")
	}
	if user.CheckPassword("wrong_password") {
		t.Fatalf("expected password verification to fail on wrong password")
	}

	// 3. 登录验证
	loginUser, loginToken, err := authSvc.Login("test_developer", "password123")
	if err != nil || loginUser.ID != user.ID || loginToken == "" {
		t.Fatalf("login failed: %v", err)
	}
}

func TestPlanAndModelAuthorization(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	authSvc := NewAuthService()
	planSvc := NewPlanService()
	keySvc := NewKeyService()

	// 1. 创建用户
	user, _, err := authSvc.Register("sub_tester", "sub@example.com", "password123")
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	// 2. 创建专属套餐，限制仅开放指定 3 个模型
	testModels := []string{"claude-3-7-sonnet", "gemini-2.5-flash", "auto"}
	plan := model.Plan{
		Name:          "AI 极速开发套餐",
		Description:   "专享 Claude 3.7 与 Gemini 2.5",
		PriceCents:    2900,
		DurationDays:  30,
		AllowedModels: testModels,
		RateLimit:     45,
		Status:        "active",
	}
	if err := planSvc.CreatePlan(&plan); err != nil {
		t.Fatalf("create plan failed: %v", err)
	}

	// 3. 用户在未订阅前尝试创建 API Key，必须被拦截拒绝
	_, err = keySvc.CreateKey(user.ID, "VSCode Key", nil)
	if err == nil {
		t.Fatalf("expected create api key to fail for unsubscribed user, but succeeded")
	}

	// 4. 激活套餐订阅
	if err := planSvc.ActivatePlan(user.ID, plan.ID); err != nil {
		t.Fatalf("activate plan failed: %v", err)
	}

	// 4.1 激活后创建 API Key 应当成功
	apiKey, err := keySvc.CreateKey(user.ID, "VSCode Key", nil)
	if err != nil {
		t.Fatalf("create api key failed after subscription: %v", err)
	}

	// 5. 校验用户订阅状态与到期时间
	updatedUser, err := authSvc.GetProfile(user.ID)
	if err != nil {
		t.Fatalf("get profile failed: %v", err)
	}
	if !updatedUser.IsSubscriptionActive() {
		t.Fatalf("expected subscription to be active")
	}
	if updatedUser.PlanExpireAt <= time.Now().Unix() {
		t.Fatalf("expected PlanExpireAt to be in the future, got %d", updatedUser.PlanExpireAt)
	}

	// 6. 校验用户的 API Key 是否自动继承并刷新了套餐允许的模型白名单
	keys, err := keySvc.ListKeys(user.ID)
	if err != nil || len(keys) == 0 {
		t.Fatalf("list keys failed: %v", err)
	}

	foundKey := keys[0]
	for _, m := range testModels {
		found := false
		for _, am := range foundKey.AllowedModels {
			if am == m {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected model %s in allowed models, got %v", m, foundKey.AllowedModels)
		}
	}
	if foundKey.RateLimit != 45 {
		t.Fatalf("expected rate limit 45, got %d", foundKey.RateLimit)
	}
	_ = apiKey
}

func TestRelayWebhookFulfillment(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	// 1. 准备用户与套餐
	user, _, _ := authSvc.Register("pay_buyer", "buyer@example.com", "password123")
	plan := model.Plan{
		Name:          "单月尊享套餐",
		PriceCents:    4900,
		DurationDays:  30,
		AllowedModels: []string{"gemini-2.5-flash", "deepseek-chat"},
		Status:        "active",
	}
	_ = planSvc.CreatePlan(&plan)

	// 2. 创建未支付订单
	orderNo := fmt.Sprintf("ORD_TEST_%d", time.Now().Unix())
	order := model.Order{
		OrderNo:     orderNo,
		UserID:      user.ID,
		PlanID:      plan.ID,
		AmountCents: plan.PriceCents,
		Status:      "pending",
	}
	if err := db.GlobalDB.Create(&order).Error; err != nil {
		t.Fatalf("create order failed: %v", err)
	}

	// 3. 构造极客工坊切单支付成功 Webhook 回调参数
	secret := config.GlobalConfig.Payment.RelaySecret
	notifyParams := map[string]interface{}{
		"out_trade_no": orderNo,
		"trade_no":     "GEEK_PAY_TXN_998877",
		"trade_status": "TRADE_SUCCESS",
		"type":         "alipay",
		"money":        "49.00",
	}
	notifyParams["sign"] = crypto.GenerateRelaySign(notifyParams, secret)

	// 4. 执行回调处理
	if err := paymentSvc.HandleRelayWebhook(notifyParams); err != nil {
		t.Fatalf("handle webhook failed: %v", err)
	}

	// 5. 断言订单状态扭转为 paid
	updatedOrder, err := paymentSvc.GetOrderByNo(orderNo)
	if err != nil {
		t.Fatalf("get order failed: %v", err)
	}
	if updatedOrder.Status != "paid" {
		t.Fatalf("expected order status 'paid', got '%s'", updatedOrder.Status)
	}
	if updatedOrder.PaidAt == nil {
		t.Fatalf("expected PaidAt to be set")
	}

	// 6. 断言用户套餐已激活生效
	buyer, _ := authSvc.GetProfile(user.ID)
	if !buyer.IsSubscriptionActive() {
		t.Fatalf("expected buyer subscription to be active after webhook fulfillment")
	}
	if buyer.PlanID == nil || *buyer.PlanID != plan.ID {
		t.Fatalf("expected buyer PlanID to be %d, got %v", plan.ID, buyer.PlanID)
	}
}

func TestAutoConfigAndGatewaySync(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	// 1. 启动一个模拟的远程 Go Relay 网关 Server (模拟 18444 端口服务)
	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{"id": "claude-3-7-sonnet"},
					{"id": "gemini-2.5-pro"},
					{"id": "qwen-max"},
				},
			})
		case "/api/models/auto-config", "/api/admin/models/auto-config":
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"config": model.AutoRacingConfig{
						Enabled:          true,
						CandidateModels:  []string{"claude-3-7-sonnet", "gemini-2.5-pro"},
						UseBenchmarkPool: true,
					},
				})
			} else if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGateway.Close()

	// 配置网关地址为 mock server
	config.GlobalConfig.Gateway.SyncEnabled = true
	config.GlobalConfig.Gateway.GatewayURL = mockGateway.URL
	config.GlobalConfig.Gateway.AdminKey = "mock-admin-key"

	gatewaySync := NewGatewaySyncService()
	settingSvc := NewSettingService()

	// 2. 测试从网关拉取真实可用大模型列表
	gatewayModels, err := gatewaySync.FetchGatewayModels()
	if err != nil {
		t.Fatalf("FetchGatewayModels failed: %v", err)
	}
	if len(gatewayModels) < 3 {
		t.Fatalf("expected at least 3 models from gateway, got %d", len(gatewayModels))
	}
	foundMockModel := false
	for _, m := range gatewayModels {
		if m == "qwen-max" {
			foundMockModel = true
			break
		}
	}
	if !foundMockModel {
		t.Fatalf("expected gateway models to include 'qwen-max'")
	}

	// 3. 测试通过 SettingService 获取聚合可用模型列表（包含 auto 虚拟模型与候选池）
	available, err := settingSvc.GetAvailableModels()
	if err != nil {
		t.Fatalf("GetAvailableModels failed: %v", err)
	}
	foundAuto := false
	for _, m := range available {
		if m == "auto" {
			foundAuto = true
			break
		}
	}
	if !foundAuto {
		t.Fatalf("expected available models to include 'auto'")
	}

	// 4. 测试拉取网关当前生效的 Auto 竞速配置并落库
	pulledCfg, err := settingSvc.PullAutoConfigFromGateway()
	if err != nil {
		t.Fatalf("PullAutoConfigFromGateway failed: %v", err)
	}
	if len(pulledCfg.CandidateModels) != 2 || pulledCfg.CandidateModels[0] != "claude-3-7-sonnet" {
		t.Fatalf("unexpected pulled candidate models: %v", pulledCfg.CandidateModels)
	}

	// 5. 测试修改并保存 Auto 配置（同时下发到模拟网关）
	newCfg := model.AutoRacingConfig{
		Enabled:          true,
		CandidateModels:  []string{"claude-3-7-sonnet", "gemini-2.5-pro", "qwen-max"},
		UseBenchmarkPool: false,
	}
	if err := settingSvc.SetAutoRacingConfig(&newCfg); err != nil {
		t.Fatalf("SetAutoRacingConfig failed: %v", err)
	}

	// 6. 验证读取本地保存的配置
	savedCfg, err := settingSvc.GetAutoRacingConfig()
	if err != nil {
		t.Fatalf("GetAutoRacingConfig failed: %v", err)
	}
	if len(savedCfg.CandidateModels) != 3 || savedCfg.UseBenchmarkPool != false {
		t.Fatalf("saved auto config does not match expected: %+v", savedCfg)
	}
}

func TestSettingService_OCRAndModelMappings(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	var receivedOcrPost bool
	var receivedMappingPost bool

	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/models/mapping" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"mappings": []map[string]interface{}{
					{
						"clientModel":    "claude-3-7-sonnet",
						"targetModel":    "claude-3-7-sonnet-20250219",
						"targetProvider": "claude",
						"expose":         true,
					},
					{
						"clientModel":    "gemini-2.5-flash",
						"targetModel":    "gemini-2.5-flash",
						"targetProvider": "google",
						"expose":         true,
					},
				},
			})
		case r.URL.Path == "/api/models/mapping" && r.Method == http.MethodPost:
			receivedMappingPost = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case r.URL.Path == "/api/admin/settings/ocr" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":   true,
				"ocrModel":  "gemini-2.5-pro",
				"ocrModels": []string{"gemini-2.5-pro", "gemini-2.0-flash"},
			})
		case r.URL.Path == "/api/admin/settings/ocr" && r.Method == http.MethodPost:
			receivedOcrPost = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGateway.Close()

	config.GlobalConfig.Gateway = config.GoRelayGatewayConfig{
		GatewayURL:  mockGateway.URL,
		SyncEnabled: true,
		AdminKey:    "sk-ant-admin",
	}

	settingSvc := NewSettingService()

	// 1. 从网关拉取模型映射并验证落库
	mappings, err := settingSvc.PullModelMappingsFromGateway()
	if err != nil {
		t.Fatalf("PullModelMappingsFromGateway failed: %v", err)
	}
	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	// 验证下发映射到网关
	if err := settingSvc.SetModelMappings(mappings); err != nil {
		t.Fatalf("SetModelMappings failed: %v", err)
	}
	if err := settingSvc.gatewaySync.SyncModelMappingsToGateway(mappings); err != nil || !receivedMappingPost {
		t.Fatalf("SyncModelMappingsToGateway failed or mapping post not received")
	}

	// 2. 验证 GetMappingClientModels 输出纯净的套餐可用模型 (带 auto，消除混乱底层 ID)
	clientModels, err := settingSvc.GetMappingClientModels()
	if err != nil {
		t.Fatalf("GetMappingClientModels failed: %v", err)
	}
	expectedModels := []string{"auto", "claude-3-7-sonnet", "gemini-2.5-flash"}
	if len(clientModels) != len(expectedModels) {
		t.Fatalf("expected %d client models, got %d (%v)", len(expectedModels), len(clientModels), clientModels)
	}
	for i, m := range expectedModels {
		if clientModels[i] != m {
			t.Errorf("expected clientModels[%d] == %s, got %s", i, m, clientModels[i])
		}
	}

	// 3. 从网关拉取 OCR 降级多模型候选池
	primaryOcr, ocrList, err := settingSvc.PullOCRModelFromGateway()
	if err != nil {
		t.Fatalf("PullOCRModelFromGateway failed: %v", err)
	}
	if primaryOcr != "gemini-2.5-pro" {
		t.Errorf("expected primary OCR model 'gemini-2.5-pro', got %q", primaryOcr)
	}
	if len(ocrList) != 2 || ocrList[0] != "gemini-2.5-pro" || ocrList[1] != "gemini-2.0-flash" {
		t.Errorf("expected ocrList ['gemini-2.5-pro', 'gemini-2.0-flash'], got %v", ocrList)
	}

	// 4. 设置并更新多模型 OCR 竞速池，验证联动网关推送
	testCandidates := []string{"gemini-2.0-flash", "gemini-2.5-pro", "deepseek-ai/deepseek-vl2"}
	if err := settingSvc.SetOCRModels(testCandidates); err != nil {
		t.Fatalf("SetOCRModels failed: %v", err)
	}
	if !receivedOcrPost {
		t.Errorf("expected SetOCRModels to send post request to mock gateway")
	}
	dbPrimary, err := settingSvc.GetOCRModel()
	if err != nil || dbPrimary != "gemini-2.0-flash" {
		t.Errorf("expected DB primary OCR model 'gemini-2.0-flash', got %q", dbPrimary)
	}
	dbModels, err := settingSvc.GetOCRModels()
	if err != nil || len(dbModels) != 3 || dbModels[0] != "gemini-2.0-flash" {
		t.Errorf("expected DB OCR models %v, got %v", testCandidates, dbModels)
	}

	// 5. 验证清空候选池
	if err := settingSvc.SetOCRModels([]string{}); err != nil {
		t.Fatalf("clear OCR models failed: %v", err)
	}
	clearedModels, err := settingSvc.GetOCRModels()
	if err != nil || len(clearedModels) != 0 {
		t.Errorf("expected empty OCR models, got %v", clearedModels)
	}
}


