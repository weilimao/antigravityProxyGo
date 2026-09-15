package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"antigravity-proxy/internal/platform/config"
	"antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/platform/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupAPITestEnvironment(t *testing.T) func() {
	tempDir, err := os.MkdirTemp("", "antigravity_api_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "api_test.db")

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_ = db.AutoMigrate(
		&model.User{},
		&model.Plan{},
		&model.Order{},
		&model.APIKey{},
		&model.Setting{},
	)

	db.GlobalDB = db

	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			JWTSecret:        "test-api-jwt-secret-999",
			TokenExpireHours: 24,
		},
		Payment: config.RelayPaymentConfig{
			RelaySecret: "test-relay-secret-888",
		},
	}

	teardown := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = os.RemoveAll(tempDir)
	}

	return teardown
}

func TestHTTPAPIRoutesAndRBAC(t *testing.T) {
	teardown := setupAPITestEnvironment(t)
	defer teardown()

	router := SetupRouter()

	// 1. 测试 Health 端点
	reqHealth, _ := http.NewRequest(http.MethodGet, "/api/health", nil)
	wHealth := httptest.NewRecorder()
	router.ServeHTTP(wHealth, reqHealth)
	if wHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/health, got %d", wHealth.Code)
	}

	// 2. 注册普通用户
	regPayload := map[string]string{
		"username": "api_user",
		"password": "password123",
		"email":    "user@api.com",
	}
	regBytes, _ := json.Marshal(regPayload)
	reqReg, _ := http.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBytes))
	reqReg.Header.Set("Content-Type", "application/json")
	wReg := httptest.NewRecorder()
	router.ServeHTTP(wReg, reqReg)
	if wReg.Code != http.StatusOK {
		t.Fatalf("expected 200 on register, got %d: %s", wReg.Code, wReg.Body.String())
	}

	var regResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wReg.Body.Bytes(), &regResp)
	userToken := regResp.Data.Token
	if userToken == "" {
		t.Fatalf("expected token in register response")
	}

	// 3. 普通用户尝试访问 Admin 接口，断言 403 权限拦截
	reqAdminForbidden, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/plans", nil)
	reqAdminForbidden.Header.Set("Authorization", "Bearer "+userToken)
	wAdminForbidden := httptest.NewRecorder()
	router.ServeHTTP(wAdminForbidden, reqAdminForbidden)
	if wAdminForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin on /api/v1/admin/plans, got %d", wAdminForbidden.Code)
	}

	// 4. 创建管理员用户并登录
	adminUser := model.User{
		Username: "super_admin",
		Role:     "admin",
		Status:   "active",
	}
	_ = adminUser.SetPassword("admin_pass_888")
	db.GlobalDB.Create(&adminUser)

	loginAdminPayload := map[string]string{
		"username": "super_admin",
		"password": "admin_pass_888",
	}
	loginBytes, _ := json.Marshal(loginAdminPayload)
	reqAdminLogin, _ := http.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBytes))
	reqAdminLogin.Header.Set("Content-Type", "application/json")
	wAdminLogin := httptest.NewRecorder()
	router.ServeHTTP(wAdminLogin, reqAdminLogin)
	if wAdminLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin login, got %d", wAdminLogin.Code)
	}

	var adminLoginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAdminLogin.Body.Bytes(), &adminLoginResp)
	adminToken := adminLoginResp.Data.Token

	// 5. 管理员访问 /api/v1/admin/plans，创建带 AllowedModels 的新套餐
	newPlan := model.Plan{
		Name:          "旗舰大模型套餐",
		PriceCents:    5900,
		DurationDays:  30,
		AllowedModels: []string{"claude-3-7-sonnet", "deepseek-chat", "auto"},
		RateLimit:     60,
		Status:        "active",
	}
	planBytes, _ := json.Marshal(newPlan)
	reqCreatePlan, _ := http.NewRequest(http.MethodPost, "/api/v1/admin/plans", bytes.NewReader(planBytes))
	reqCreatePlan.Header.Set("Content-Type", "application/json")
	reqCreatePlan.Header.Set("Authorization", "Bearer "+adminToken)
	wCreatePlan := httptest.NewRecorder()
	router.ServeHTTP(wCreatePlan, reqCreatePlan)
	if wCreatePlan.Code != http.StatusOK {
		t.Fatalf("expected 200 on create plan, got %d: %s", wCreatePlan.Code, wCreatePlan.Body.String())
	}

	// 6. 管理员配置全局 OCR 模型
	ocrPayload := map[string]string{
		"ocrModel": "gemini-2.0-flash",
	}
	ocrBytes, _ := json.Marshal(ocrPayload)
	reqSetOcr, _ := http.NewRequest(http.MethodPost, "/api/v1/admin/settings/ocr", bytes.NewReader(ocrBytes))
	reqSetOcr.Header.Set("Content-Type", "application/json")
	reqSetOcr.Header.Set("Authorization", "Bearer "+adminToken)
	wSetOcr := httptest.NewRecorder()
	router.ServeHTTP(wSetOcr, reqSetOcr)
	if wSetOcr.Code != http.StatusOK {
		t.Fatalf("expected 200 on set ocr, got %d: %s", wSetOcr.Code, wSetOcr.Body.String())
	}
}
