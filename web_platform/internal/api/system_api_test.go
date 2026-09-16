package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupSystemAPITestEnv(t *testing.T) (*gorm.DB, func()) {
	tempDir, err := os.MkdirTemp("", "antigravity_sysapi_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "sysapi_test.db")

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

	database.DB = db

	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			JWTSecret:        "test-system-jwt-secret-888",
			TokenExpireHours: 24,
		},
		Gateway: config.GoRelayGatewayConfig{
			GatewayURL: "http://127.0.0.1:18444",
		},
	}

	teardown := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = os.RemoveAll(tempDir)
	}

	return db, teardown
}

func TestSystemAPI_Endpoints(t *testing.T) {
	_, teardown := setupSystemAPITestEnv(t)
	defer teardown()

	router := SetupRouter()
	authSvc := service.NewAuthService()

	// 1. 公开端点无需认证：GET /api/v1/system/config
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/system/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/v1/system/config, got %d, body: %s", w.Code, w.Body.String())
	}

	var pubResp struct {
		Code int                `json:"code"`
		Data model.SystemConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pubResp); err != nil {
		t.Fatalf("failed to unmarshal public config response: %v", err)
	}
	if pubResp.Data.APIBaseURL != "http://127.0.0.1:18444" {
		t.Errorf("expected default APIBaseURL 'http://127.0.0.1:18444', got '%s'", pubResp.Data.APIBaseURL)
	}

	// 2. 权限校验：匿名用户直接访问管理后台接口 -> 401
	adminReq, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/settings/system", nil)
	wAdmin := httptest.NewRecorder()
	router.ServeHTTP(wAdmin, adminReq)
	if wAdmin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", wAdmin.Code)
	}

	// 3. 权限校验：普通用户访问管理后台接口 -> 403 Forbidden
	_, userToken, err := authSvc.Register("normal_user", "user@example.com", "pass123456")
	if err != nil {
		t.Fatalf("register normal user failed: %v", err)
	}

	normReq, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/settings/system", nil)
	normReq.Header.Set("Authorization", "Bearer "+userToken)
	wNorm := httptest.NewRecorder()
	router.ServeHTTP(wNorm, normReq)
	if wNorm.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for normal user, got %d", wNorm.Code)
	}

	// 4. 管理员配置更新流程
	adminUser, _, err := authSvc.Register("admin_user", "admin@example.com", "TestAdminPass@2026")
	if err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	// 提升为 admin 并通过 Login 重新签发含 admin 权限的 token
	database.DB.Model(&model.User{}).Where("id = ?", adminUser.ID).Update("role", "admin")
	_, adminToken, err := authSvc.Login("admin_user", "TestAdminPass@2026")
	if err != nil {
		t.Fatalf("admin login failed: %v", err)
	}

	// 4.1 管理员 POST 更新配置
	payload := model.SystemConfig{
		APIBaseURL:   "https://relay.ai-enterprise.com:18444",
		SiteName:     "Enterprise Relay Hub",
		Announcement: "系统维护升级完毕",
	}
	payloadBytes, _ := json.Marshal(payload)

	postReq, _ := http.NewRequest(http.MethodPost, "/api/v1/admin/settings/system", bytes.NewReader(payloadBytes))
	postReq.Header.Set("Authorization", "Bearer "+adminToken)
	postReq.Header.Set("Content-Type", "application/json")
	wPost := httptest.NewRecorder()
	router.ServeHTTP(wPost, postReq)

	if wPost.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin update, got %d, body: %s", wPost.Code, wPost.Body.String())
	}

	// 4.2 管理员 GET 校验更新
	getReq, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/settings/system", nil)
	getReq.Header.Set("Authorization", "Bearer "+adminToken)
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, getReq)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin get, got %d", wGet.Code)
	}

	var adminGetResp struct {
		Code int                `json:"code"`
		Data model.SystemConfig `json:"data"`
	}
	_ = json.Unmarshal(wGet.Body.Bytes(), &adminGetResp)
	if adminGetResp.Data.APIBaseURL != "https://relay.ai-enterprise.com:18444" {
		t.Errorf("expected updated APIBaseURL 'https://relay.ai-enterprise.com:18444', got '%s'", adminGetResp.Data.APIBaseURL)
	}

	// 4.3 前台公开接口 GET 校验最新生效配置
	wPubAfter := httptest.NewRecorder()
	reqPubAfter, _ := http.NewRequest(http.MethodGet, "/api/v1/system/config", nil)
	router.ServeHTTP(wPubAfter, reqPubAfter)

	if wPubAfter.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wPubAfter.Code)
	}
	var pubAfterResp struct {
		Code int                `json:"code"`
		Data model.SystemConfig `json:"data"`
	}
	_ = json.Unmarshal(wPubAfter.Body.Bytes(), &pubAfterResp)
	if pubAfterResp.Data.APIBaseURL != "https://relay.ai-enterprise.com:18444" {
		t.Errorf("expected public endpoint to return updated APIBaseURL, got '%s'", pubAfterResp.Data.APIBaseURL)
	}
}
