package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-proxy/internal/settings"
)

func TestAdminBridge_FullFlow(t *testing.T) {
	tmpDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tmpDir)

	authMgr := NewAuthManager(userMgr)
	handler := NewAPIHandler(authMgr, nil, nil, nil, "", nil, nil)

	// 1. 未授权访问拒绝
	reqNoAuth := httptest.NewRequest(http.MethodPost, "/api/admin/users/sync", bytes.NewReader([]byte(`{"username":"test_user"}`)))
	wNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wNoAuth, reqNoAuth)
	if wNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthenticated call, got %d", wNoAuth.Code)
	}

	// 2. 使用 admin key 同步创建用户
	syncBody := []byte(`{"username":"alice","password":"password123","remark":"web platform user"}`)
	reqSync := httptest.NewRequest(http.MethodPost, "/api/admin/users/sync", bytes.NewReader(syncBody))
	reqSync.Header.Set("Authorization", "Bearer sk-ant-admin")
	wSync := httptest.NewRecorder()
	handler.ServeHTTP(wSync, reqSync)

	if wSync.Code != http.StatusOK {
		t.Fatalf("users/sync failed with code %d: %s", wSync.Code, wSync.Body.String())
	}
	var syncRes struct {
		Success bool `json:"success"`
		User    struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"user"`
	}
	if err := json.Unmarshal(wSync.Body.Bytes(), &syncRes); err != nil || !syncRes.Success {
		t.Fatalf("unmarshal sync res failed: %v", err)
	}
	if syncRes.User.Key != "alice" {
		t.Errorf("expected user key 'alice', got %q", syncRes.User.Key)
	}

	// 3. 桥接为 alice 创建受限 API Key (只授权 auto 和 gemini-2.5-flash)
	createKeyBody := []byte(`{
		"username": "alice",
		"name": "Alice OpenCode Key",
		"allowedModels": ["gemini-2.5-flash"]
	}`)
	reqCreateKey := httptest.NewRequest(http.MethodPost, "/api/admin/keys/create", bytes.NewReader(createKeyBody))
	reqCreateKey.Header.Set("Authorization", "Bearer sk-ant-admin")
	wCreateKey := httptest.NewRecorder()
	handler.ServeHTTP(wCreateKey, reqCreateKey)

	if wCreateKey.Code != http.StatusOK {
		t.Fatalf("keys/create failed with code %d: %s", wCreateKey.Code, wCreateKey.Body.String())
	}

	var keyRes struct {
		Success bool       `json:"success"`
		Key     UserAPIKey `json:"key"`
	}
	if err := json.Unmarshal(wCreateKey.Body.Bytes(), &keyRes); err != nil || !keyRes.Success {
		t.Fatalf("unmarshal key res failed: %v", err)
	}

	// 验证白名单自动包含 auto 以及 gemini-2.5-flash
	models := keyRes.Key.AllowedModels
	hasAuto := false
	hasFlash := false
	for _, m := range models {
		if m == "auto" {
			hasAuto = true
		}
		if m == "gemini-2.5-flash" {
			hasFlash = true
		}
	}
	if !hasAuto || !hasFlash {
		t.Fatalf("expected allowedModels to contain 'auto' and 'gemini-2.5-flash', got %v", models)
	}

	// 4. 验证模型授权校验 IsModelAuthorizedForAPIKey
	// 4.1 auto 模型必须被放行 (用于并发竞速)
	if err := userMgr.IsModelAuthorizedForAPIKey(syncRes.User.ID, keyRes.Key.ID, "auto"); err != nil {
		t.Errorf("expected 'auto' to be authorized, got err: %v", err)
	}
	// 4.2 gemini-2.5-flash 必须被放行 (后台配置的模型)
	if err := userMgr.IsModelAuthorizedForAPIKey(syncRes.User.ID, keyRes.Key.ID, "gemini-2.5-flash"); err != nil {
		t.Errorf("expected 'gemini-2.5-flash' to be authorized, got err: %v", err)
	}
	// 4.3 未授权模型 claude-3-7-sonnet 必须被拦截
	if err := userMgr.IsModelAuthorizedForAPIKey(syncRes.User.ID, keyRes.Key.ID, "claude-3-7-sonnet"); err == nil {
		t.Errorf("expected unauthorized model 'claude-3-7-sonnet' to be rejected, but got nil")
	}

	// 5. 桥接删除 API Key
	delBody := []byte(`{"username":"alice","key":"` + keyRes.Key.Key + `"}`)
	reqDel := httptest.NewRequest(http.MethodPost, "/api/admin/keys/delete", bytes.NewReader(delBody))
	reqDel.Header.Set("Authorization", "Bearer sk-ant-admin")
	wDel := httptest.NewRecorder()
	handler.ServeHTTP(wDel, reqDel)

	if wDel.Code != http.StatusOK {
		t.Fatalf("keys/delete failed with code %d: %s", wDel.Code, wDel.Body.String())
	}

	// 验证 Key 已被彻底剔除
	u := userMgr.GetUserByID(syncRes.User.ID)
	for _, k := range u.APIKeys {
		if k.Key == keyRes.Key.Key {
			t.Errorf("expected key %q to be deleted, but still exists", keyRes.Key.Key)
		}
	}

	// 6. 验证非法/未注册的 sk-ant- fake key 严格被拒绝，严禁 bypass
	_, errFake := authMgr.ValidateToken("sk-ant-fake1234567890")
	if errFake == nil {
		t.Errorf("expected unregistered sk-ant- token to be rejected, but passed")
	}
}

func TestAdminBridge_OCRAndMapping(t *testing.T) {
	tmpDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tmpDir)

	authMgr := NewAuthManager(userMgr)
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tmpDir)

	handler := NewAPIHandler(authMgr, nil, nil, nil, "", settingsMgr, nil)

	// 1. 未授权访问 OCR 接口 -> 403
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/admin/settings/ocr", nil)
	wNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wNoAuth, reqNoAuth)
	if wNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthenticated OCR GET, got %d", wNoAuth.Code)
	}

	reqPostNoAuth := httptest.NewRequest(http.MethodPost, "/api/admin/settings/ocr", bytes.NewReader([]byte(`{"ocrModel":"gemini-2.5-pro"}`)))
	wPostNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wPostNoAuth, reqPostNoAuth)
	if wPostNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthenticated OCR POST, got %d", wPostNoAuth.Code)
	}

	// 2. 验证未设置时初始 OCR 模型为空字符串 (无硬编码默认值)
	reqGetInitial := httptest.NewRequest(http.MethodGet, "/api/admin/settings/ocr", nil)
	reqGetInitial.Header.Set("Authorization", "Bearer sk-ant-admin")
	wGetInitial := httptest.NewRecorder()
	handler.ServeHTTP(wGetInitial, reqGetInitial)
	if wGetInitial.Code != http.StatusOK {
		t.Fatalf("initial OCR GET failed: %d", wGetInitial.Code)
	}
	var initRes struct {
		Success  bool   `json:"success"`
		OcrModel string `json:"ocrModel"`
	}
	_ = json.Unmarshal(wGetInitial.Body.Bytes(), &initRes)
	if initRes.OcrModel != "" {
		t.Errorf("expected empty initial ocrModel, got %q", initRes.OcrModel)
	}

	// 3. 管理员显式设置 OCR 模型
	postBody := []byte(`{"ocrModel":"gemini-2.5-pro","ocrModels":["gemini-2.5-pro","gemini-2.5-flash"]}`)
	reqSetOcr := httptest.NewRequest(http.MethodPost, "/api/admin/settings/ocr", bytes.NewReader(postBody))
	reqSetOcr.Header.Set("Authorization", "Bearer sk-ant-admin")
	wSetOcr := httptest.NewRecorder()
	handler.ServeHTTP(wSetOcr, reqSetOcr)
	if wSetOcr.Code != http.StatusOK {
		t.Fatalf("OCR POST failed with code %d: %s", wSetOcr.Code, wSetOcr.Body.String())
	}

	// 3. 管理员读取 OCR 模型
	reqGetOcr := httptest.NewRequest(http.MethodGet, "/api/admin/settings/ocr", nil)
	reqGetOcr.Header.Set("Authorization", "Bearer sk-ant-admin")
	wGetOcr := httptest.NewRecorder()
	handler.ServeHTTP(wGetOcr, reqGetOcr)
	if wGetOcr.Code != http.StatusOK {
		t.Fatalf("OCR GET failed with code %d: %s", wGetOcr.Code, wGetOcr.Body.String())
	}

	var ocrRes struct {
		Success   bool     `json:"success"`
		OcrModel  string   `json:"ocrModel"`
		OcrModels []string `json:"ocrModels"`
	}
	if err := json.Unmarshal(wGetOcr.Body.Bytes(), &ocrRes); err != nil || !ocrRes.Success {
		t.Fatalf("unmarshal ocr res failed: %v", err)
	}
	if ocrRes.OcrModel != "gemini-2.5-pro" {
		t.Errorf("expected ocrModel 'gemini-2.5-pro', got %q", ocrRes.OcrModel)
	}

	// 4. 管理员通过 sk-ant-admin 下发模型映射
	mappingBody := []byte(`{
		"mappings": [
			{
				"clientModel": "claude-3-7-sonnet",
				"targetModel": "deepseek-ai/deepseek-v3",
				"targetProvider": "other",
				"expose": true
			}
		]
	}`)
	reqSetMap := httptest.NewRequest(http.MethodPost, "/api/models/mapping", bytes.NewReader(mappingBody))
	reqSetMap.Header.Set("Authorization", "Bearer sk-ant-admin")
	wSetMap := httptest.NewRecorder()
	handler.ServeHTTP(wSetMap, reqSetMap)
	if wSetMap.Code != http.StatusOK {
		t.Fatalf("mapping POST failed with code %d: %s", wSetMap.Code, wSetMap.Body.String())
	}

	// 5. 管理员读取模型映射
	reqGetMap := httptest.NewRequest(http.MethodGet, "/api/models/mapping", nil)
	reqGetMap.Header.Set("Authorization", "Bearer sk-ant-admin")
	wGetMap := httptest.NewRecorder()
	handler.ServeHTTP(wGetMap, reqGetMap)
	if wGetMap.Code != http.StatusOK {
		t.Fatalf("mapping GET failed with code %d: %s", wGetMap.Code, wGetMap.Body.String())
	}

	var mapRes struct {
		Success  bool                         `json:"success"`
		Mappings []settings.ModelMappingEntry `json:"mappings"`
	}
	if err := json.Unmarshal(wGetMap.Body.Bytes(), &mapRes); err != nil || !mapRes.Success {
		t.Fatalf("unmarshal mapping res failed: %v", err)
	}
	if len(mapRes.Mappings) != 1 || mapRes.Mappings[0].ClientModel != "claude-3-7-sonnet" {
		t.Errorf("expected clientModel 'claude-3-7-sonnet', got %+v", mapRes.Mappings)
	}
}

func TestAdminUserKeysUsage(t *testing.T) {
	tmpDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tmpDir)

	testUser, err := userMgr.SyncOrAddUser("test_usage_user", "password123", "for usage test")
	if err != nil {
		t.Fatalf("failed to sync user: %v", err)
	}

	testKey, err := userMgr.CreateAPIKeyWithOptions(testUser.ID, "测试Key1", "sk-ant-test-usage-key-111", []string{"auto", "claude-3-7-sonnet"}, 0, 0)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// 记录用量：Gemini 12000, Claude 8000, Nvidia 5000
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, false, 12000)
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, true, 8000)
	userMgr.RecordAPIKeyUsageForFamily(testUser.ID, testKey.ID, FamilyNvidia, 5000)

	authMgr := NewAuthManager(userMgr)
	handler := NewAPIHandler(authMgr, nil, nil, nil, "", nil, nil)

	// 1. 无凭证请求应返回 403
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/admin/users/keys-usage?username=test_usage_user", nil)
	wNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wNoAuth, reqNoAuth)
	if wNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on no auth, got %d", wNoAuth.Code)
	}

	// 2. 带 sk-ant-admin 正常请求
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/admin/users/keys-usage?username=test_usage_user", nil)
	reqAuth.Header.Set("Authorization", "Bearer sk-ant-admin")
	wAuth := httptest.NewRecorder()
	handler.ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 on keys-usage, got %d: %s", wAuth.Code, wAuth.Body.String())
	}

	var res struct {
		Success   bool             `json:"success"`
		Username  string           `json:"username"`
		Usages    map[string]int64 `json:"usages"`
		TotalUsed int64            `json:"totalUsed"`
	}
	if err := json.Unmarshal(wAuth.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success true, got false")
	}
	if res.TotalUsed != 25000 {
		t.Fatalf("expected totalUsed 25000, got %d", res.TotalUsed)
	}
	if res.Usages[testKey.Key] != 25000 {
		t.Fatalf("expected key usage 25000, got %d", res.Usages[testKey.Key])
	}
}

