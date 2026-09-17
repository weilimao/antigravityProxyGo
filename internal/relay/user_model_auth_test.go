package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"antigravity-proxy/internal/settings"
)

// TestIsModelAuthorizedForAPIKey 覆盖 API Key 模型授权校验(精确匹配)的核心语义:
// 空白名单=全部允许(兼容旧数据); 非空=仅精确命中放行; 兜底分支(official/default_bypass
// 及找不到 user/key)放行; 大小写敏感; 变体后缀(如 -thinking)不自动覆盖。
func TestIsModelAuthorizedForAPIKey(t *testing.T) {
	m := NewUserManager()
	user := &RelayUser{
		ID:        "u1",
		Key:       "user1",
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	user.APIKeys = []UserAPIKey{
		{ID: "k1", Name: "no-limit", Key: "sk-ant-k1", AllowedModels: nil},
		{ID: "k2", Name: "whitelist", Key: "sk-ant-k2", AllowedModels: []string{"claude-sonnet-4-6", "gemini-2.5-pro"}},
	}
	m.users = []*RelayUser{user}

	tests := []struct {
		name    string
		userID  string
		apiKeyID string
		model   string
		wantErr bool
	}{
		{"empty whitelist allows all", "u1", "k1", "anything", false},
		{"empty whitelist allows variant", "u1", "k1", "claude-sonnet-4-6-thinking", false},
		{"whitelist exact match allowed", "u1", "k2", "claude-sonnet-4-6", false},
		{"whitelist exact match allowed 2", "u1", "k2", "gemini-2.5-pro", false},
		{"whitelist variant rejected (exact match)", "u1", "k2", "claude-sonnet-4-6-thinking", true},
		{"whitelist unlisted rejected", "u1", "k2", "gpt-4o", true},
		{"whitelist case sensitive", "u1", "k2", "Claude-Sonnet-4-6", true},
		{"official bypass keyID allows", "u1", "official_bypass", "anything", false},
		{"default bypass keyID allows", "u1", "default_bypass", "anything", false},
		{"empty apiKeyID allows", "u1", "", "anything", false},
		{"empty model allows", "u1", "k2", "", false},
		{"unknown user allows", "unknown", "k2", "anything", false},
		{"unknown key allows (bypass semantics)", "u1", "k-unknown", "anything", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.IsModelAuthorizedForAPIKey(tt.userID, tt.apiKeyID, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsModelAuthorizedForAPIKey(%q, %q, %q) err=%v, wantErr=%v",
					tt.userID, tt.apiKeyID, tt.model, err, tt.wantErr)
			}
		})
	}
}

// TestUpdateAPIKeyQuotaSetsAllowedModels 验证 UpdateAPIKeyQuota 同时写入限额与授权白名单,
// 且清空白名单(nil)后恢复"全部允许"语义, 与 omitempty JSON 序列化口径一致。
func TestUpdateAPIKeyQuotaSetsAllowedModels(t *testing.T) {
	m := NewUserManager()
	user := &RelayUser{
		ID:        "u1",
		Key:       "user1",
		Enabled:   true,
		CreatedAt: time.Now(),
		APIKeys:   []UserAPIKey{{ID: "k1", Name: "k", Key: "sk-ant-k1"}},
	}
	m.users = []*RelayUser{user}

	// 设置白名单
	if err := m.UpdateAPIKeyQuota("u1", "k1", 1000, 2000, []string{"gemini-2.5-pro"}); err != nil {
		t.Fatalf("UpdateAPIKeyQuota failed: %v", err)
	}
	got := user.APIKeys[0]
	if got.LimitGeminiTokens != 1000 || got.LimitClaudeTokens != 2000 {
		t.Errorf("limits not set: gemini=%d claude=%d", got.LimitGeminiTokens, got.LimitClaudeTokens)
	}
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gemini-2.5-pro" {
		t.Errorf("allowedModels not set: %v", got.AllowedModels)
	}
	// 白名单内放行
	if err := m.IsModelAuthorizedForAPIKey("u1", "k1", "gemini-2.5-pro"); err != nil {
		t.Errorf("authorized model rejected: %v", err)
	}
	// 白名单外拒绝
	if err := m.IsModelAuthorizedForAPIKey("u1", "k1", "claude-sonnet-4-6"); err == nil {
		t.Errorf("unauthorized model allowed")
	}
	// 清空白名单(传 nil) → 恢复全部允许
	if err := m.UpdateAPIKeyQuota("u1", "k1", 1000, 2000, nil); err != nil {
		t.Fatalf("UpdateAPIKeyQuota clear failed: %v", err)
	}
	if err := m.IsModelAuthorizedForAPIKey("u1", "k1", "anything"); err != nil {
		t.Errorf("after clearing whitelist, model rejected: %v", err)
	}
}

// TestGetAllowedModelsForAPIKey 验证读取 Key 授权模型列表方法
func TestGetAllowedModelsForAPIKey(t *testing.T) {
	m := NewUserManager()
	user := &RelayUser{
		ID:        "u1",
		Key:       "user1",
		Enabled:   true,
		CreatedAt: time.Now(),
		APIKeys: []UserAPIKey{
			{ID: "k1", Name: "no-whitelist", Key: "sk-ant-k1", AllowedModels: nil},
			{ID: "k2", Name: "whitelist", Key: "sk-ant-k2", AllowedModels: []string{"claude-sonnet-4-6", "gemini-2.5-pro"}},
		},
	}
	m.users = []*RelayUser{user}

	if got := m.GetAllowedModelsForAPIKey("u1", "k1"); len(got) != 0 {
		t.Errorf("expected empty for k1, got %v", got)
	}
	got2 := m.GetAllowedModelsForAPIKey("u1", "k2")
	if len(got2) != 2 || got2[0] != "claude-sonnet-4-6" || got2[1] != "gemini-2.5-pro" {
		t.Errorf("expected [claude-sonnet-4-6 gemini-2.5-pro], got %v", got2)
	}
	if got := m.GetAllowedModelsForAPIKey("u1", "non-existent"); got != nil {
		t.Errorf("expected nil for non-existent key, got %v", got)
	}
	if got := m.GetAllowedModelsForAPIKey("non-existent-user", "k2"); got != nil {
		t.Errorf("expected nil for non-existent user, got %v", got)
	}
}

// TestHandleModels_APIKeyWhitelistFiltering 验证通过指定 API Key 请求 /v1/models 时，
// 严格根据 Key 绑定的 AllowedModels 白名单进行隔离过滤，只能拉取到已授权的模型。
func TestHandleModels_APIKeyWhitelistFiltering(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)
	_ = settingsMgr.SetRelayModelMapping([]settings.ModelMappingEntry{
		{ClientModel: "gpt-4o", TargetModel: "target-1", Expose: true},
		{ClientModel: "claude-3-5-sonnet", TargetModel: "target-2", Expose: true},
		{ClientModel: "gemini-2.5-pro", TargetModel: "target-3", Expose: true},
	})

	userMgr := NewUserManager()
	user := &RelayUser{
		ID:        "u1",
		Key:       "user1",
		Enabled:   true,
		CreatedAt: time.Now(),
		APIKeys: []UserAPIKey{
			{
				ID:            "k_scoped",
				Name:          "scoped",
				Key:           "sk-ant-scoped",
				AllowedModels: []string{"gpt-4o", "claude-3-5-sonnet"},
			},
			{
				ID:            "k_all",
				Name:          "all",
				Key:           "sk-ant-all",
				AllowedModels: nil,
			},
		},
	}
	userMgr.users = []*RelayUser{user}
	authMgr := NewAuthManager(userMgr)
	handler := NewAPICompatHandler(authMgr, nil, nil, nil, nil, settingsMgr, nil)

	// 1. 携带受限 Key 的 session 访问 /v1/models
	reqScoped := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rrScoped := httptest.NewRecorder()
	sessionScoped := &RelaySession{UserID: "u1", APIKeyID: "k_scoped"}

	handler.handleModels(rrScoped, reqScoped, sessionScoped)
	if rrScoped.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrScoped.Code)
	}

	var respScoped struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rrScoped.Body.Bytes(), &respScoped); err != nil {
		t.Fatalf("failed to unmarshal scoped response: %v", err)
	}

	scopedIDs := make(map[string]bool)
	for _, m := range respScoped.Data {
		scopedIDs[m.ID] = true
	}

	if !scopedIDs["gpt-4o"] {
		t.Errorf("expected gpt-4o in scoped response")
	}
	if !scopedIDs["claude-3-5-sonnet"] {
		t.Errorf("expected claude-3-5-sonnet in scoped response")
	}
	if scopedIDs["gemini-2.5-pro"] {
		t.Errorf("gemini-2.5-pro should NOT be present in scoped response for key with whitelist")
	}
	if len(respScoped.Data) != 2 {
		t.Errorf("expected exactly 2 models, got %d: %+v", len(respScoped.Data), respScoped.Data)
	}

	// 2. 携带未限制 Key 的 session 访问 /v1/models，应获取全部 3 个模型
	reqAll := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rrAll := httptest.NewRecorder()
	sessionAll := &RelaySession{UserID: "u1", APIKeyID: "k_all"}

	handler.handleModels(rrAll, reqAll, sessionAll)
	if rrAll.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrAll.Code)
	}

	var respAll struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rrAll.Body.Bytes(), &respAll); err != nil {
		t.Fatalf("failed to unmarshal all response: %v", err)
	}
	if len(respAll.Data) != 3 {
		t.Errorf("expected 3 models for unrestricted key, got %d", len(respAll.Data))
	}
}

