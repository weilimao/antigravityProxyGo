package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

func TestAPIHandler_AdminGlobalStats(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化设置、用户与认证管理器
	settingsMgr := settings.NewManager()
	_, _ = settings.EnsureConfigExists(tempDir)
	settingsMgr.Init(tempDir)

	userMgr := NewUserManager()
	userMgr.Init(tempDir)

	authMgr := NewAuthManager(userMgr)

	// 创建管理员用户并登录
	_, err := userMgr.EnsureAdminUser("admin-user", "adminpass123", "Super Admin")
	if err != nil {
		t.Fatalf("EnsureAdminUser failed: %v", err)
	}
	adminSession, err := authMgr.Login("admin-user", "adminpass123")
	if err != nil {
		t.Fatalf("Admin login failed: %v", err)
	}

	// 创建普通用户并登录
	normalUser, err := userMgr.AddUser("normal-user", "userpass123", "Normal User")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	normalSession, err := authMgr.Login("normal-user", "userpass123")
	if err != nil {
		t.Fatalf("Normal user login failed: %v", err)
	}

	// 初始化计费与全局 StatsTracker
	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)

	globalTracker := stats.NewTracker(pricingMgr)
	globalTracker.Init(tempDir)
	// 往全局 Tracker 记录两条请求
	globalTracker.TrackRequest("gemini-3.8-flash-high", 1000, 200, 500)
	globalTracker.TrackRequest("claude-opus-4-6-thinking", 2000, 300, 1000)

	// 初始化中继 StatsTracker
	relayStatsMgr := NewStatsTracker(pricingMgr)
	relayStatsMgr.Init(tempDir)

	// 普通用户消费一条中继请求
	relayStatsMgr.RecordUsage(RelaySample{
		ReqID:     "req-normal-1",
		UserID:    normalUser.ID,
		UserKey:   normalUser.Key,
		ModelName: "gemini-2.5-flash",
		InTokens:  300,
		OutTokens: 50,
	})

	apiHandler := NewAPIHandler(authMgr, relayStatsMgr, nil, nil, "", settingsMgr)
	apiHandler.SetGlobalStatsTracker(globalTracker)

	// 严密清理测试沙箱
	t.Cleanup(func() {
		relayStatsMgr.Close()
		_ = os.RemoveAll(tempDir)
	})

	// 测试 1: 普通用户访问 /api/stats -> 应返回普通用户自身的消费 (InTokens=300, Requests=1)
	reqNormal := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	reqNormal.Header.Set("Authorization", "Bearer "+normalSession.Token)
	wNormal := httptest.NewRecorder()
	apiHandler.ServeHTTP(wNormal, reqNormal)

	if wNormal.Code != http.StatusOK {
		t.Fatalf("expected 200 for normal user, got %d: %s", wNormal.Code, wNormal.Body.String())
	}
	var normalResp RelayUserStats
	if err := json.NewDecoder(wNormal.Body).Decode(&normalResp); err != nil {
		t.Fatalf("failed to decode normal user stats: %v", err)
	}
	if normalResp.TotalRequests != 1 {
		t.Errorf("expected normal user requests=1, got %d", normalResp.TotalRequests)
	}
	if normalResp.TotalInputTokens != 300 {
		t.Errorf("expected normal user inputTokens=300, got %d", normalResp.TotalInputTokens)
	}

	// 测试 2: 管理员用户访问 /api/stats -> 管理员无中继消费，但注入了全局大盘，应返回全局大盘数据 (TotalRequests=2, Tokens=3000)
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminSession.Token)
	wAdmin := httptest.NewRecorder()
	apiHandler.ServeHTTP(wAdmin, reqAdmin)

	if wAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin user, got %d: %s", wAdmin.Code, wAdmin.Body.String())
	}
	var adminResp RelayUserStats
	if err := json.NewDecoder(wAdmin.Body).Decode(&adminResp); err != nil {
		t.Fatalf("failed to decode admin stats: %v", err)
	}
	if adminResp.TotalRequests != 2 {
		t.Errorf("expected admin global requests=2, got %d", adminResp.TotalRequests)
	}
	if adminResp.TotalInputTokens != 3000 {
		t.Errorf("expected admin global inputTokens=3000, got %d", adminResp.TotalInputTokens)
	}
	if len(adminResp.Models) != 2 {
		t.Errorf("expected admin global models count=2, got %d", len(adminResp.Models))
	}
	if _, ok := adminResp.Models["gemini-3.8-flash-high"]; !ok {
		t.Errorf("expected gemini-3.8-flash-high in admin global models")
	}
}
