package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAPIKeyQuotaEnforce_Exceeded_429 验证当 Token 超限时（如用户场景中 1,083,970 / 1,000,000），
// /route/v1/messages, /route/v1/chat/completions, /v1/chat/completions 等中继入口必须严格熔断，
// 返回 429 Too Many Requests 与 insufficient_quota 错误，彻底杜绝超额继续调用。
func TestAPIKeyQuotaEnforce_Exceeded_429(t *testing.T) {
	userMgr := NewUserManager()
	authMgr := NewAuthManager(userMgr)
	compatHandler := NewAPICompatHandler(authMgr, nil, nil, nil, nil, nil, nil)

	// 创建测试用户与 API Key，设定总限额 1,000,000 Tokens
	testUser, err := userMgr.AddUser("quota_user", "pass123", "test remark")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	testKey, err := userMgr.CreateAPIKeyWithOptions(
		testUser.ID,
		"TestKey",
		"sk-ant-test-quota-exceeded-key",
		[]string{"auto", "claude-3-7-sonnet", "gemini-2.5-flash"},
		0,
		0,
		1000000, // LimitTokens = 1,000,000
	)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// 模拟已用量 1,083,970 (即用户反馈的 108.4% 超限场景)
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, true, 1083970)

	t.Cleanup(func() {
		_ = userMgr.RemoveUser(testUser.ID)
	})

	// 1. 测试 /route/v1/messages (Anthropic 协议入口，即 Claude Code 使用的入口)
	anthBody := []byte(`{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"hi"}]}`)
	reqAnth := httptest.NewRequest(http.MethodPost, "/route/v1/messages", bytes.NewReader(anthBody))
	reqAnth.Header.Set("Authorization", "Bearer "+testKey.Key)
	reqAnth.Header.Set("Content-Type", "application/json")
	rrAnth := httptest.NewRecorder()

	compatHandler.ServeHTTP(rrAnth, reqAnth)

	if rrAnth.Code != http.StatusTooManyRequests {
		t.Fatalf("expected /route/v1/messages to return 429 on quota exceeded, got %d: body=%s", rrAnth.Code, rrAnth.Body.String())
	}

	var respAnth map[string]interface{}
	if err := json.Unmarshal(rrAnth.Body.Bytes(), &respAnth); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}
	errObj, ok := respAnth["error"].(map[string]interface{})
	if !ok || errObj["code"] != "insufficient_quota" {
		t.Fatalf("expected error code 'insufficient_quota', got: %v", respAnth)
	}

	// 2. 测试 /route/v1/chat/completions (OpenAI 兼容协议入口，带 auto 模型)
	openBody := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	reqOpen := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", bytes.NewReader(openBody))
	reqOpen.Header.Set("Authorization", "Bearer "+testKey.Key)
	reqOpen.Header.Set("Content-Type", "application/json")
	rrOpen := httptest.NewRecorder()

	compatHandler.ServeHTTP(rrOpen, reqOpen)

	if rrOpen.Code != http.StatusTooManyRequests {
		t.Fatalf("expected /route/v1/chat/completions to return 429 on quota exceeded, got %d: body=%s", rrOpen.Code, rrOpen.Body.String())
	}

	// 3. 测试 /v1/chat/completions 直连端点
	reqDirect := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(openBody))
	reqDirect.Header.Set("Authorization", "Bearer "+testKey.Key)
	reqDirect.Header.Set("Content-Type", "application/json")
	rrDirect := httptest.NewRecorder()

	compatHandler.ServeHTTP(rrDirect, reqDirect)

	if rrDirect.Code != http.StatusTooManyRequests {
		t.Fatalf("expected /v1/chat/completions to return 429 on quota exceeded, got %d: body=%s", rrDirect.Code, rrDirect.Body.String())
	}
}

// TestAPIKeyQuotaEnforce_WithinLimit_Pass 验证在配额范围内时（如已用 500,000 / 1,000,000），
// 配额检查顺利放行，不会被误拦截为 429。
func TestAPIKeyQuotaEnforce_WithinLimit_Pass(t *testing.T) {
	userMgr := NewUserManager()

	testUser, err := userMgr.AddUser("within_quota_user", "pass123", "test remark")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	testKey, err := userMgr.CreateAPIKeyWithOptions(
		testUser.ID,
		"TestKey2",
		"sk-ant-test-quota-within-key",
		[]string{"auto", "claude-3-7-sonnet"},
		0,
		0,
		1000000, // LimitTokens = 1,000,000
	)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// 已用 500,000，未超限
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, true, 500000)

	t.Cleanup(func() {
		_ = userMgr.RemoveUser(testUser.ID)
	})

	// 校验 CheckAPIKeyQuota 必须返回 nil
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "claude-3-7-sonnet"); err != nil {
		t.Fatalf("expected CheckAPIKeyQuota to pass within limit, got error: %v", err)
	}
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "auto"); err != nil {
		t.Fatalf("expected CheckAPIKeyQuota to pass within limit for auto, got error: %v", err)
	}
}

// TestAPIKeyQuotaEnforce_FamilyLimits_BackwardCompatible 验证向后兼容旧版按模型家族细分限额：
// 当未设置 LimitTokens，仅设置 LimitClaudeTokens 时，调用 Claude 超限能被正确拦截。
func TestAPIKeyQuotaEnforce_FamilyLimits_BackwardCompatible(t *testing.T) {
	userMgr := NewUserManager()

	testUser, err := userMgr.AddUser("family_quota_user", "pass123", "test remark")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	testKey, err := userMgr.CreateAPIKeyWithOptions(
		testUser.ID,
		"FamilyKey",
		"sk-ant-test-family-key",
		[]string{"claude-3-7-sonnet", "gemini-2.5-flash"},
		500000, // Gemini: 500,000
		100000, // Claude: 100,000
	)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	// Claude 已用 120,000 (超限)；Gemini 已用 50,000 (未超限)
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, true, 120000)
	userMgr.RecordAPIKeyUsage(testUser.ID, testKey.ID, false, 50000)

	t.Cleanup(func() {
		_ = userMgr.RemoveUser(testUser.ID)
	})

	// Claude 应被拦截
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "claude-3-7-sonnet"); err == nil {
		t.Fatalf("expected Claude to be blocked due to LimitClaudeTokens exceeded")
	}

	// Gemini 应放行
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "gemini-2.5-flash"); err != nil {
		t.Fatalf("expected Gemini to pass, got err: %v", err)
	}
}

// TestAPIKeyQuotaEnforce_ExpiredAccount 验证当账户过期（ExpireAt）时严格拦截
func TestAPIKeyQuotaEnforce_ExpiredAccount(t *testing.T) {
	userMgr := NewUserManager()

	testUser, err := userMgr.AddUser("expired_user", "pass123", "test remark")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	// 设定账号已过期（1 小时前）
	testUser.Quotas.ExpireAt = time.Now().Add(-1 * time.Hour).Unix()

	testKey, err := userMgr.CreateAPIKeyWithOptions(
		testUser.ID,
		"ExpiredKey",
		"sk-ant-test-expired-key",
		[]string{"auto"},
		0,
		0,
		1000000,
	)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	t.Cleanup(func() {
		_ = userMgr.RemoveUser(testUser.ID)
	})

	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "auto"); err == nil {
		t.Fatalf("expected expired account to be blocked")
	}
}

// TestAPIKeyQuotaEnforce_SubscriptionExpired_EndToEnd 验证端到端套餐过期拦截、续费恢复与永久有效场景
func TestAPIKeyQuotaEnforce_SubscriptionExpired_EndToEnd(t *testing.T) {
	userMgr := NewUserManager()
	authMgr := NewAuthManager(userMgr)
	compatHandler := NewAPICompatHandler(authMgr, nil, nil, nil, nil, nil, nil)

	// 创建测试用户与有效 Key
	testUser, err := userMgr.AddUser("expire_e2e_user", "pass123", "test e2e expire")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	testKey, err := userMgr.CreateAPIKeyWithOptions(
		testUser.ID,
		"ExpireTestKey",
		"sk-ant-test-expire-e2e-key",
		[]string{"auto", "claude-3-7-sonnet"},
		0,
		0,
		1000000,
	)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	t.Cleanup(func() {
		_ = userMgr.RemoveUser(testUser.ID)
	})

	// 1. 初始状态：未过期（1 小时后到期），应该正常放行
	futureExpire := time.Now().Add(1 * time.Hour).Unix()
	if err := userMgr.UpdateUserExpireAt(testUser.Key, futureExpire); err != nil {
		t.Fatalf("failed to update expireAt: %v", err)
	}
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "claude-3-7-sonnet"); err != nil {
		t.Fatalf("expected quota check to pass before expiration, got: %v", err)
	}

	// 2. 套餐到期（10 分钟前到期）：必须立即拦截
	pastExpire := time.Now().Add(-10 * time.Minute).Unix()
	if err := userMgr.UpdateUserExpireAt(testUser.Key, pastExpire); err != nil {
		t.Fatalf("failed to update expireAt to past: %v", err)
	}

	// 2.1 请求 /route/v1/messages 验证拦截为 429
	anthBody := []byte(`{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"hi"}]}`)
	reqAnth := httptest.NewRequest(http.MethodPost, "/route/v1/messages", bytes.NewReader(anthBody))
	reqAnth.Header.Set("Authorization", "Bearer "+testKey.Key)
	reqAnth.Header.Set("Content-Type", "application/json")
	rrAnth := httptest.NewRecorder()
	compatHandler.ServeHTTP(rrAnth, reqAnth)

	if rrAnth.Code != http.StatusTooManyRequests {
		t.Fatalf("expected /route/v1/messages to return 429 when expired, got %d: body=%s", rrAnth.Code, rrAnth.Body.String())
	}
	if !bytes.Contains(rrAnth.Body.Bytes(), []byte("subscription expired")) {
		t.Fatalf("expected response body to contain 'subscription expired', got: %s", rrAnth.Body.String())
	}

	// 2.2 请求 /route/v1/chat/completions 验证拦截为 429
	openBody := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	reqOpen := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", bytes.NewReader(openBody))
	reqOpen.Header.Set("Authorization", "Bearer "+testKey.Key)
	reqOpen.Header.Set("Content-Type", "application/json")
	rrOpen := httptest.NewRecorder()
	compatHandler.ServeHTTP(rrOpen, reqOpen)

	if rrOpen.Code != http.StatusTooManyRequests {
		t.Fatalf("expected /route/v1/chat/completions to return 429 when expired, got %d: body=%s", rrOpen.Code, rrOpen.Body.String())
	}
	if !bytes.Contains(rrOpen.Body.Bytes(), []byte("subscription expired")) {
		t.Fatalf("expected response body to contain 'subscription expired', got: %s", rrOpen.Body.String())
	}

	// 3. 用户续费：更新到期时间至 7 天后，必须恢复放行
	renewExpire := time.Now().Add(7 * 24 * time.Hour).Unix()
	if err := userMgr.UpdateUserExpireAt(testUser.Key, renewExpire); err != nil {
		t.Fatalf("failed to update expireAt for renewal: %v", err)
	}
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "claude-3-7-sonnet"); err != nil {
		t.Fatalf("expected quota check to pass after renewal, got: %v", err)
	}

	// 4. 永久套餐：ExpireAt = 0，必须永久有效放行
	if err := userMgr.UpdateUserExpireAt(testUser.Key, 0); err != nil {
		t.Fatalf("failed to update expireAt to permanent (0): %v", err)
	}
	if err := userMgr.CheckAPIKeyQuota(testUser.ID, testKey.ID, "claude-3-7-sonnet"); err != nil {
		t.Fatalf("expected permanent plan (expireAt=0) to pass, got: %v", err)
	}
}

