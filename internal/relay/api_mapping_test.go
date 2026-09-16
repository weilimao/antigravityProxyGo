package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"antigravity-proxy/internal/settings"
)

func TestAPIHandler_ModelMappingEndpoints(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化设置管理器与数据
	settingsMgr := settings.NewManager()
	_, _ = settings.EnsureConfigExists(tempDir)
	settingsMgr.Init(tempDir)

	userMgr := NewUserManager()
	userMgr.Init(tempDir)

	authMgr := NewAuthManager(userMgr)

	// 创建测试管理员用户与登录 Session
	_, err := userMgr.EnsureAdminUser("test-admin", "secret123", "Test Admin")
	if err != nil {
		t.Fatalf("EnsureAdminUser failed: %v", err)
	}

	session, err := authMgr.Login("test-admin", "secret123")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	apiHandler := NewAPIHandler(authMgr, nil, nil, nil, "", settingsMgr, nil)

	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	// 2. 测试未授权访问 (Missing Token) -> 预期 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/models/mapping", nil)
	unauthW := httptest.NewRecorder()
	apiHandler.ServeHTTP(unauthW, unauthReq)
	if unauthW.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated request, got %d", unauthW.Code)
	}

	// 3. 测试获取模型映射 GET /api/models/mapping
	getReq := httptest.NewRequest(http.MethodGet, "/api/models/mapping", nil)
	getReq.Header.Set("Authorization", "Bearer "+session.Token)
	getW := httptest.NewRecorder()
	apiHandler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 for authenticated GET, got %d: %s", getW.Code, getW.Body.String())
	}

	var getResp struct {
		Success  bool                        `json:"success"`
		Mappings []settings.ModelMappingEntry `json:"mappings"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode GET response: %v", err)
	}
	if !getResp.Success {
		t.Errorf("expected success true in GET response")
	}

	// 4. 测试更新模型映射 POST /api/models/mapping
	newMappings := append(getResp.Mappings, settings.ModelMappingEntry{
		ClientModel:    "custom-claude-test",
		TargetModel:    "deepseek-ai/deepseek-v4",
		Expose:         true,
		TargetProvider: "nvidia",
		OwnedBy:        "nvidia",
	})

	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"mappings": newMappings,
	})

	postReq := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(bodyBytes))
	postReq.Header.Set("Authorization", "Bearer "+session.Token)
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	apiHandler.ServeHTTP(postW, postReq)

	if postW.Code != http.StatusOK {
		t.Fatalf("expected 200 for POST, got %d: %s", postW.Code, postW.Body.String())
	}

	// 5. 验证持久化与设置管理器更新
	savedMappings := settingsMgr.GetRelayModelMapping()
	found := false
	for _, m := range savedMappings {
		if m.ClientModel == "custom-claude-test" && m.TargetModel == "deepseek-ai/deepseek-v4" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected new mapping to be saved in settingsMgr")
	}

	// 验证磁盘上的 config.json
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read config.json: %v", err)
	}
	if !bytes.Contains(cfgData, []byte("custom-claude-test")) {
		t.Errorf("expected custom-claude-test to be persisted in config.json")
	}
}
