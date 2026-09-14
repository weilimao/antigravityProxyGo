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

func TestAPIHandler_UserAutoConfig_MultiUserIsolation(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化设置管理器与数据
	settingsMgr := settings.NewManager()
	_, _ = settings.EnsureConfigExists(tempDir)
	settingsMgr.Init(tempDir)

	userMgr := NewUserManager()
	userMgr.Init(tempDir)

	authMgr := NewAuthManager(userMgr)

	// 创建用户 A 与用户 B
	userA, err := userMgr.AddUser("user-a", "pass-a", "User A")
	if err != nil {
		t.Fatalf("AddUser A failed: %v", err)
	}
	userB, err := userMgr.AddUser("user-b", "pass-b", "User B")
	if err != nil {
		t.Fatalf("AddUser B failed: %v", err)
	}

	sessionA, err := authMgr.Login("user-a", "pass-a")
	if err != nil {
		t.Fatalf("Login A failed: %v", err)
	}
	sessionB, err := authMgr.Login("user-b", "pass-b")
	if err != nil {
		t.Fatalf("Login B failed: %v", err)
	}

	apiHandler := NewAPIHandler(authMgr, nil, nil, nil, "", settingsMgr)

	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	// 2. 未授权访问拦截测试 (401 Unauthorized)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/models/auto-config", nil)
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for unauth request, got %d", w.Code)
		}
	}

	// 3. 用户 A 首次获取 Auto 配置 -> 初始状态为默认配置 (isUser: false)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/models/auto-config", nil)
		req.Header.Set("Authorization", "Bearer "+sessionA.Token)
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Success bool           `json:"success"`
			IsUser  bool           `json:"isUser"`
			Config  UserAutoConfig `json:"config"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !resp.Success {
			t.Errorf("expected success true")
		}
		if resp.IsUser {
			t.Errorf("expected isUser false before configuring")
		}
	}

	// 4. 用户 A 保存个人专属配置 (2 个候选模型)
	cfgA := UserAutoConfig{
		Enabled:          true,
		CandidateModels:  []string{"gemini-2.5-flash", "deepseek-chat"},
		UseBenchmarkPool: false,
	}
	{
		body, _ := json.Marshal(map[string]interface{}{"config": cfgA})
		req := httptest.NewRequest(http.MethodPost, "/api/models/auto-config", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sessionA.Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	}

	// 5. 验证用户 A 再次获取已成为专属配置 (isUser: true)
	{
		req := httptest.NewRequest(http.MethodGet, "/api/models/auto-config", nil)
		req.Header.Set("Authorization", "Bearer "+sessionA.Token)
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		var resp struct {
			Success bool           `json:"success"`
			IsUser  bool           `json:"isUser"`
			Config  UserAutoConfig `json:"config"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp.IsUser || !resp.Config.Enabled || len(resp.Config.CandidateModels) != 2 {
			t.Fatalf("User A auto config mismatch: %+v", resp)
		}
		if resp.Config.CandidateModels[0] != "gemini-2.5-flash" || resp.Config.CandidateModels[1] != "deepseek-chat" {
			t.Errorf("unexpected candidates for A: %v", resp.Config.CandidateModels)
		}
	}

	// 6. 多租户隔离验证: 用户 B 此时获取，绝不能读到用户 A 的私有配置
	{
		req := httptest.NewRequest(http.MethodGet, "/api/models/auto-config", nil)
		req.Header.Set("Authorization", "Bearer "+sessionB.Token)
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		var resp struct {
			Success bool           `json:"success"`
			IsUser  bool           `json:"isUser"`
			Config  UserAutoConfig `json:"config"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.IsUser {
			t.Errorf("User B should not have private auto config yet")
		}
		if len(resp.Config.CandidateModels) != 0 {
			t.Errorf("User B should not see User A's candidates, got: %v", resp.Config.CandidateModels)
		}
	}

	// 7. 用户 B 保存不同的专属配置
	cfgB := UserAutoConfig{
		Enabled:          true,
		CandidateModels:  []string{"nvidia/llama-3.3-70b"},
		UseBenchmarkPool: false,
	}
	{
		body, _ := json.Marshal(map[string]interface{}{"config": cfgB})
		req := httptest.NewRequest(http.MethodPost, "/api/models/auto-config", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sessionB.Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		apiHandler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	}

	// 8. 验证磁盘重载后两个用户的专属配置依然完好存在 (持久化测试)
	reloadedMgr := NewUserManager()
	reloadedMgr.Init(tempDir)
	reloadedUserA := reloadedMgr.GetUserByID(userA.ID)
	reloadedUserB := reloadedMgr.GetUserByID(userB.ID)

	if reloadedUserA == nil || reloadedUserA.AutoConfig == nil || len(reloadedUserA.AutoConfig.CandidateModels) != 2 {
		t.Fatalf("reloaded user A auto config lost or corrupted")
	}
	if reloadedUserB == nil || reloadedUserB.AutoConfig == nil || len(reloadedUserB.AutoConfig.CandidateModels) != 1 {
		t.Fatalf("reloaded user B auto config lost or corrupted")
	}

	// 9. 验证调度器 isAutoModelForSession 多租户优先级调度
	compatHandler := NewAPICompatHandler(authMgr, nil, nil, nil, nil, settingsMgr, nil)

	// A: 应该命中 A 的两个模型
	entryA, candsA, isAutoA := compatHandler.isAutoModelForSession("auto", sessionA)
	if !isAutoA || entryA == nil || len(candsA) != 2 || candsA[0] != "gemini-2.5-flash" {
		t.Fatalf("session A dispatch mismatch: isAuto=%v, cands=%v", isAutoA, candsA)
	}

	// B: 应该命中 B 的单个模型
	entryB, candsB, isAutoB := compatHandler.isAutoModelForSession("auto", sessionB)
	if !isAutoB || entryB == nil || len(candsB) != 1 || candsB[0] != "nvidia/llama-3.3-70b" {
		t.Fatalf("session B dispatch mismatch: isAuto=%v, cands=%v", isAutoB, candsB)
	}

	// C (无专属配置): 回退全局映射
	sessionC := &RelaySession{UserID: "non-existent-user", UserKey: "user-c"}
	_, candsC, isAutoC := compatHandler.isAutoModelForSession("auto", sessionC)
	if !isAutoC {
		t.Fatalf("session C should still match auto")
	}
	// 全局未配时应该严格为空候选池
	if len(candsC) != 0 {
		t.Fatalf("session C should fall back to empty global candidates, got: %v", candsC)
	}
}
