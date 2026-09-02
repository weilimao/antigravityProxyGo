package relay

import (
	"testing"
	"time"
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
