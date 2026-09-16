package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/settings"
)

type mockBenchmarkScheduler struct {
	runNowCalled      atomic.Int32
	runModelNowCalled atomic.Int32
	lastModelTested   string
	running           bool
}

func (m *mockBenchmarkScheduler) RunNow() {
	m.runNowCalled.Add(1)
}

func (m *mockBenchmarkScheduler) RunModelNow(model string) {
	m.runModelNowCalled.Add(1)
	m.lastModelTested = model
}

func (m *mockBenchmarkScheduler) IsRunning() bool {
	return m.running
}

func (m *mockBenchmarkScheduler) LastRun() time.Time {
	return time.Time{}
}

func (m *mockBenchmarkScheduler) PendingModels() []string {
	return nil
}

func (m *mockBenchmarkScheduler) ResetLastRun() {}

func TestAPIHandler_AdminBenchmarkRun(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "benchmark_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	_, _ = userMgr.EnsureAdminUser("admin", "Admin@18444#2026", "超级管理员")
	authMgr := NewAuthManager(userMgr)
	adminSession, err := authMgr.Login("admin", "Admin@18444#2026")
	if err != nil {
		t.Fatalf("admin login failed: %v", err)
	}

	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)

	handler := NewAPIHandler(authMgr, nil, nil, func(string) {}, "", settingsMgr, nil)

	// Case 1: 未注入 benchmarkScheduler 时请求 /api/admin/benchmark/run -> 500
	req1 := httptest.NewRequest(http.MethodPost, "/api/admin/benchmark/run", nil)
	req1.Header.Set("Authorization", "Bearer "+adminSession.Token)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when scheduler is nil, got: %d", rec1.Code)
	}
	var errResp map[string]interface{}
	_ = json.Unmarshal(rec1.Body.Bytes(), &errResp)
	if errResp["error"] != "benchmark scheduler not running" {
		t.Fatalf("expected 'benchmark scheduler not running', got: %v", errResp["error"])
	}

	// Case 2: 注入 mock scheduler 后请求 /api/admin/benchmark/run -> 200 & success: true
	mockSched := &mockBenchmarkScheduler{}
	handler.SetBenchmarkScheduler(mockSched)

	req2 := httptest.NewRequest(http.MethodPost, "/api/admin/benchmark/run", nil)
	req2.Header.Set("Authorization", "Bearer "+adminSession.Token)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after scheduler injected, got: %d", rec2.Code)
	}
	var okResp map[string]interface{}
	_ = json.Unmarshal(rec2.Body.Bytes(), &okResp)
	if okResp["success"] != true {
		t.Fatalf("expected success=true, got: %v", okResp)
	}

	// Case 3: 请求单个模型测速 /api/admin/benchmark/run-model
	reqBody, _ := json.Marshal(map[string]string{"model": "test/model-1"})
	req3 := httptest.NewRequest(http.MethodPost, "/api/admin/benchmark/run-model", bytes.NewReader(reqBody))
	req3.Header.Set("Authorization", "Bearer "+adminSession.Token)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 on run-model, got: %d", rec3.Code)
	}
}
