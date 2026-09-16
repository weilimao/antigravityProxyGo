package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"antigravity-proxy/internal/settings"
)

func TestAdminAuth_Enforcement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "relay_admin_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	userMgr := NewUserManager()
	userMgr.Init(tempDir)

	// 1. 初始化管理员账号
	adminUser, err := userMgr.EnsureAdminUser("admin", "Admin@18444#2026", "超级管理员")
	if err != nil {
		t.Fatalf("EnsureAdminUser failed: %v", err)
	}
	if !adminUser.IsAdminUser() {
		t.Fatalf("Expected adminUser.IsAdminUser() to be true")
	}

	// 2. 初始化普通中继用户
	normalUser, err := userMgr.AddUser("normal_user", "password123", "普通用户")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	if normalUser.IsAdminUser() {
		t.Fatalf("Expected normalUser.IsAdminUser() to be false")
	}

	authMgr := NewAuthManager(userMgr)

	// 3. 管理员登录验证
	adminSession, err := authMgr.Login("admin", "Admin@18444#2026")
	if err != nil {
		t.Fatalf("Admin login failed: %v", err)
	}
	if !adminSession.IsAdmin {
		t.Fatalf("Expected adminSession.IsAdmin to be true")
	}

	// 4. 合法中继用户登录应成功建立具备管理权限的 Session (IsAdmin 为 true)
	normalSession, err := authMgr.Login("normal_user", "password123")
	if err != nil {
		t.Fatalf("Expected normal user login to succeed, got error: %v", err)
	}
	if !normalSession.IsAdmin {
		t.Fatalf("Expected normalSession.IsAdmin to be true")
	}

	// 5. 测试接口级权限拦截与放行
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)

	handler := NewAPIHandler(authMgr, nil, nil, nil, "", settingsMgr, nil)

	// (A) 未携带 Token 访问 POST /api/models/mapping -> 401
	payload := map[string]interface{}{
		"mappings": []settings.ModelMappingEntry{
			{ClientModel: "test-model", TargetModel: "real-model"},
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	reqNoToken := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(bodyBytes))
	recNoToken := httptest.NewRecorder()
	handler.ServeHTTP(recNoToken, reqNoToken)
	if recNoToken.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for no token, got %d", recNoToken.Code)
	}

	// (B) 合法中继用户的登录 Session Token 访问 POST /api/models/mapping -> 200 OK (支持云端或穿透服务管理)
	reqNormalSession := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(bodyBytes))
	reqNormalSession.Header.Set("Authorization", "Bearer "+normalSession.Token)
	recNormalSession := httptest.NewRecorder()
	handler.ServeHTTP(recNormalSession, reqNormalSession)
	if recNormalSession.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for normal user session token, got %d: %s", recNormalSession.Code, recNormalSession.Body.String())
	}

	// (C) 普通用户的 API Key 作为 Bearer Token -> 403 (非 admin Session)
	apiKey, err := userMgr.CreateAPIKey(normalUser.ID, "client-key")
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	reqNormalKey := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(bodyBytes))
	reqNormalKey.Header.Set("Authorization", "Bearer "+apiKey.Key)
	recNormalKey := httptest.NewRecorder()
	handler.ServeHTTP(recNormalKey, reqNormalKey)
	if recNormalKey.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for normal API key, got %d", recNormalKey.Code)
	}

	// (D) 管理员 Token 访问 POST /api/models/mapping -> 200
	reqAdmin := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(bodyBytes))
	reqAdmin.Header.Set("Authorization", "Bearer "+adminSession.Token)
	recAdmin := httptest.NewRecorder()
	handler.ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for admin token, got %d: %s", recAdmin.Code, recAdmin.Body.String())
	}
}
