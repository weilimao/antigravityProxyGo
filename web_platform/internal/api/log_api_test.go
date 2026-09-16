package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupLogAPITestEnvironment(t *testing.T) (func(), *httptest.Server) {
	tempDir, err := os.MkdirTemp("", "antigravity_log_api_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "log_api_test.db")

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

	// 启动模拟 Relay 网关
	mockRelay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/api/admin/logs" {
			account := r.URL.Query().Get("account")
			if account == "" {
				account = r.URL.Query().Get("username")
			}

			resp := map[string]interface{}{
				"success":  true,
				"total":    1,
				"page":     1,
				"pageSize": 10,
				"list": []map[string]interface{}{
					{
						"id":         101,
						"reqId":      "req_test_001",
						"account":    account,
						"model":      "claude-3-7-sonnet",
						"statusCode": 200,
						"durationMs": 350,
					},
				},
				"summary": map[string]interface{}{
					"totalRequests": 1,
					"totalCost":     0.0125,
				},
				"accounts": []string{"alice", "bob"},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/api/admin/logs/detail" {
			reqID := r.URL.Query().Get("req_id")
			resp := map[string]interface{}{
				"success": true,
				"log": map[string]interface{}{
					"id":           101,
					"reqId":        reqID,
					"model":        "claude-3-7-sonnet",
					"requestBody":  `{"messages":[{"role":"user","content":"ping"}]}`,
					"responseBody": `{"choices":[{"message":{"content":"pong"}}]}`,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/api/admin/logs/accounts" {
			resp := map[string]interface{}{
				"success":  true,
				"accounts": []string{"alice", "bob", "auth_acc:tenant_1"},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			JWTSecret:        "test-log-jwt-secret-999",
			TokenExpireHours: 24,
		},
		Gateway: config.GoRelayGatewayConfig{
			GatewayURL: mockRelay.URL,
			AdminKey:   "sk-test-admin-key",
		},
	}

	teardown := func() {
		mockRelay.Close()
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = os.RemoveAll(tempDir)
	}

	return teardown, mockRelay
}

func TestLogAPIRoutesAndPermissions(t *testing.T) {
	teardown, _ := setupLogAPITestEnvironment(t)
	defer teardown()

	router := SetupRouter()

	// 1. 注册普通用户 alice 并获取 token
	regPayload := map[string]string{
		"username": "alice",
		"password": "password123",
		"email":    "alice@test.com",
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
	aliceToken := regResp.Data.Token

	// 2. 创建管理员用户并登录获取 adminToken
	adminUser := model.User{
		Username: "admin_boss",
		Role:     "admin",
		Status:   "active",
	}
	_ = adminUser.SetPassword("admin_pass_888")
	database.DB.Create(&adminUser)

	loginAdminPayload := map[string]string{
		"username": "admin_boss",
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

	// 3. 未登录访问用户端日志 => 401
	reqUnauth, _ := http.NewRequest(http.MethodGet, "/api/v1/user/logs", nil)
	wUnauth := httptest.NewRecorder()
	router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated user logs, got %d", wUnauth.Code)
	}

	// 4. 普通用户访问自身日志 => 200，并隔离只能传自身 username
	reqAliceLogs, _ := http.NewRequest(http.MethodGet, "/api/v1/user/logs?status=hit&page=1&pageSize=10", nil)
	reqAliceLogs.Header.Set("Authorization", "Bearer "+aliceToken)
	wAliceLogs := httptest.NewRecorder()
	router.ServeHTTP(wAliceLogs, reqAliceLogs)
	if wAliceLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 for user logs, got %d: %s", wAliceLogs.Code, wAliceLogs.Body.String())
	}

	var aliceResp struct {
		Code int `json:"code"`
		Data struct {
			Success bool `json:"success"`
			Total   int  `json:"total"`
			List    []struct {
				Account string `json:"account"`
			} `json:"list"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAliceLogs.Body.Bytes(), &aliceResp)
	if len(aliceResp.Data.List) == 0 || aliceResp.Data.List[0].Account != "alice" {
		t.Fatalf("expected queried account in list to be 'alice', got %+v", aliceResp.Data)
	}

	// 5. 普通用户访问日志详情 => 200
	reqAliceDetail, _ := http.NewRequest(http.MethodGet, "/api/v1/user/logs/detail?req_id=req_test_001", nil)
	reqAliceDetail.Header.Set("Authorization", "Bearer "+aliceToken)
	wAliceDetail := httptest.NewRecorder()
	router.ServeHTTP(wAliceDetail, reqAliceDetail)
	if wAliceDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 for log detail, got %d", wAliceDetail.Code)
	}

	// 6. 普通用户越权访问管理员日志端点 => 403 Forbidden
	reqForbidden, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/logs", nil)
	reqForbidden.Header.Set("Authorization", "Bearer "+aliceToken)
	wForbidden := httptest.NewRecorder()
	router.ServeHTTP(wForbidden, reqForbidden)
	if wForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when normal user accesses admin logs, got %d", wForbidden.Code)
	}

	// 7. 管理员访问全量日志 => 200
	reqAdminLogs, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/logs?page=1&pageSize=20", nil)
	reqAdminLogs.Header.Set("Authorization", "Bearer "+adminToken)
	wAdminLogs := httptest.NewRecorder()
	router.ServeHTTP(wAdminLogs, reqAdminLogs)
	if wAdminLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin logs, got %d: %s", wAdminLogs.Code, wAdminLogs.Body.String())
	}

	// 8. 管理员按特定账号筛选日志 => 200 且 account 正确传递
	reqAdminFilterLogs, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/logs?account=bob&status=success", nil)
	reqAdminFilterLogs.Header.Set("Authorization", "Bearer "+adminToken)
	wAdminFilterLogs := httptest.NewRecorder()
	router.ServeHTTP(wAdminFilterLogs, reqAdminFilterLogs)
	if wAdminFilterLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin filtered logs, got %d", wAdminFilterLogs.Code)
	}
	var adminFilterResp struct {
		Data struct {
			Success bool `json:"success"`
			List    []struct {
				Account string `json:"account"`
			} `json:"list"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAdminFilterLogs.Body.Bytes(), &adminFilterResp)
	if len(adminFilterResp.Data.List) == 0 || adminFilterResp.Data.List[0].Account != "bob" {
		t.Fatalf("expected admin filter account to be 'bob', got %+v", adminFilterResp.Data)
	}

	// 9. 管理员获取账号池列表 => 200
	reqAccounts, _ := http.NewRequest(http.MethodGet, "/api/v1/admin/logs/accounts", nil)
	reqAccounts.Header.Set("Authorization", "Bearer "+adminToken)
	wAccounts := httptest.NewRecorder()
	router.ServeHTTP(wAccounts, reqAccounts)
	if wAccounts.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin log accounts, got %d", wAccounts.Code)
	}
	if !strings.Contains(wAccounts.Body.String(), "alice") {
		t.Fatalf("expected accounts list to contain 'alice', got %s", wAccounts.Body.String())
	}
}
