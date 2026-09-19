package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
)

func setupTestLogDB(t *testing.T) (string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "antigravity.db")

	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}

	teardown := func() {
		if db.GlobalDB != nil {
			_ = db.GlobalDB.Close()
			db.GlobalDB = nil
		}
	}
	_ = dbPath
	return tempDir, teardown
}

func TestAdminLogs_PermissionsAndFiltering(t *testing.T) {
	tempDir, teardown := setupTestLogDB(t)
	defer teardown()

	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	userA, _ := userMgr.SyncOrAddUser("userA", "pass123", "User A")
	userB, _ := userMgr.SyncOrAddUser("userB", "pass123", "User B")

	authMgr := NewAuthManager(userMgr)
	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)
	statsTracker := NewStatsTracker(pricingMgr)
	statsTracker.Init(tempDir)
	defer statsTracker.Close()

	handler := NewAPIHandler(authMgr, statsTracker, nil, func(string) {}, "", nil, nil)

	// Seed logs:
	// 1. User A: HIT, 200, in 1000, out 200, cached 800, cost 0.005
	log1 := &db.RequestLog{
		ReqID:        "req-a-1",
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "local",
		UserID:       userA.Key,
		SessionID:    userA.ID,
		ModelName:    "gemini-3.0-flash-high",
		InTokens:     1000,
		OutTokens:    200,
		CachedTokens: 800,
		Cost:         0.005,
		DurationMs:   1500,
		FirstByteMs:  200,
		StatusCode:   200,
		Method:       "POST",
		Host:         "daily-cloudcode-pa.googleapis.com",
		Path:         "/v1internal:streamGenerateContent",
		CacheStatus:  "HIT",
		RequestBody:  `{"contents":[{"text":"hello from userA"}]}`,
	}
	// 2. User A: MISS, 200, in 500, out 50, cached 0, cost 0.002
	log2 := &db.RequestLog{
		ReqID:        "req-a-2",
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "local",
		UserID:       userA.Key,
		SessionID:    userA.ID,
		ModelName:    "gemini-2.5-flash-lite",
		InTokens:     500,
		OutTokens:    50,
		CachedTokens: 0,
		Cost:         0.002,
		DurationMs:   800,
		FirstByteMs:  150,
		StatusCode:   200,
		Method:       "POST",
		Host:         "daily-cloudcode-pa.googleapis.com",
		Path:         "/v1internal:streamGenerateContent",
		CacheStatus:  "MISS",
	}
	// 3. User B: error 500
	log3 := &db.RequestLog{
		ReqID:        "req-b-1",
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "local",
		UserID:       userB.Key,
		SessionID:    userB.ID,
		ModelName:    "claude-3-7-sonnet",
		InTokens:     2000,
		OutTokens:    0,
		CachedTokens: 0,
		Cost:         0.01,
		DurationMs:   5000,
		FirstByteMs:  5000,
		StatusCode:   500,
		Method:       "POST",
		Host:         "api.anthropic.com",
		Path:         "/v1/messages",
		CacheStatus:  "MISS",
	}
	// 4. Pool account with email format
	log4 := &db.RequestLog{
		ReqID:        "req-pool-1",
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "local",
		UserID:       "harold@nexusquantum.cloud",
		SessionID:    "pool-session-001",
		ModelName:    "deepseek-ai/deepseek-v4-flash-0731",
		InTokens:     1500,
		OutTokens:    100,
		CachedTokens: 0,
		Cost:         0.003,
		DurationMs:   2500,
		FirstByteMs:  800,
		StatusCode:   200,
		Method:       "POST",
		Host:         "integrate.api.nvidia.com",
		Path:         "/route/v1/messages",
		CacheStatus:  "NONE",
	}

	if err := db.InsertRequestLog(log1); err != nil {
		t.Fatalf("insert log1 failed: %v", err)
	}
	if err := db.InsertRequestLog(log2); err != nil {
		t.Fatalf("insert log2 failed: %v", err)
	}
	if err := db.InsertRequestLog(log3); err != nil {
		t.Fatalf("insert log3 failed: %v", err)
	}
	if err := db.InsertRequestLog(log4); err != nil {
		t.Fatalf("insert log4 failed: %v", err)
	}

	// Test 1: Unauthorized without admin key
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/admin/logs", nil)
	wNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wNoAuth, reqNoAuth)
	if wNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", wNoAuth.Code)
	}

	// Test 2: Admin queries all logs
	reqAll := httptest.NewRequest(http.MethodGet, "/api/admin/logs", nil)
	reqAll.Header.Set("Authorization", "Bearer sk-ant-admin")
	wAll := httptest.NewRecorder()
	handler.ServeHTTP(wAll, reqAll)
	if wAll.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wAll.Code, wAll.Body.String())
	}
	var resAll AdminLogsResponse
	if err := json.NewDecoder(wAll.Body).Decode(&resAll); err != nil {
		t.Fatalf("decode resAll failed: %v", err)
	}
	if resAll.Total != 4 {
		t.Errorf("expected 4 total logs, got %d", resAll.Total)
	}
	if resAll.Summary.TotalRequests != 4 {
		t.Errorf("expected 4 total requests in summary, got %d", resAll.Summary.TotalRequests)
	}
	for _, l := range resAll.List {
		parsedTime, err := time.Parse(time.RFC3339, l.Timestamp)
		if err != nil {
			t.Errorf("expected RFC3339 timestamp, got %q (err: %v)", l.Timestamp, err)
		} else if parsedTime.Year() != time.Now().Year() {
			t.Errorf("expected timestamp year %d, got %d in %q", time.Now().Year(), parsedTime.Year(), l.Timestamp)
		}
	}

	// Test 3: Filter by username="userA" (Account isolation)
	reqA := httptest.NewRequest(http.MethodGet, "/api/admin/logs?username=userA", nil)
	reqA.Header.Set("Authorization", "Bearer sk-ant-admin")
	wA := httptest.NewRecorder()
	handler.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wA.Code)
	}
	var resA AdminLogsResponse
	if err := json.NewDecoder(wA.Body).Decode(&resA); err != nil {
		t.Fatalf("decode resA failed: %v", err)
	}
	if resA.Total != 2 {
		t.Errorf("expected 2 logs for userA, got %d", resA.Total)
	}
	for _, l := range resA.List {
		if l.ReqID == "req-b-1" {
			t.Errorf("userB log leaked into userA results: %v", l)
		}
	}

	// Test 4: Filter by status="hit"
	reqHit := httptest.NewRequest(http.MethodGet, "/api/admin/logs?status=hit", nil)
	reqHit.Header.Set("Authorization", "Bearer sk-ant-admin")
	wHit := httptest.NewRecorder()
	handler.ServeHTTP(wHit, reqHit)
	var resHit AdminLogsResponse
	_ = json.NewDecoder(wHit.Body).Decode(&resHit)
	if resHit.Total != 1 || resHit.List[0].ReqID != "req-a-1" {
		t.Errorf("expected 1 hit log (req-a-1), got %d (%v)", resHit.Total, resHit.List)
	}

	// Test 5: Filter by status="error"
	reqErr := httptest.NewRequest(http.MethodGet, "/api/admin/logs?status=error", nil)
	reqErr.Header.Set("Authorization", "Bearer sk-ant-admin")
	wErr := httptest.NewRecorder()
	handler.ServeHTTP(wErr, reqErr)
	var resErr AdminLogsResponse
	_ = json.NewDecoder(wErr.Body).Decode(&resErr)
	if resErr.Total != 1 || resErr.List[0].ReqID != "req-b-1" {
		t.Errorf("expected 1 error log (req-b-1), got %d", resErr.Total)
	}

	// Test 6: Search query
	reqSearch := httptest.NewRequest(http.MethodGet, "/api/admin/logs?search=claude", nil)
	reqSearch.Header.Set("Authorization", "Bearer sk-ant-admin")
	wSearch := httptest.NewRecorder()
	handler.ServeHTTP(wSearch, reqSearch)
	var resSearch AdminLogsResponse
	_ = json.NewDecoder(wSearch.Body).Decode(&resSearch)
	if resSearch.Total != 1 || resSearch.List[0].Model != "claude-3-7-sonnet" {
		t.Errorf("expected 1 search result for claude, got %d", resSearch.Total)
	}

	// Test 7: Log detail
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/admin/logs/detail?req_id=req-a-1", nil)
	reqDetail.Header.Set("Authorization", "Bearer sk-ant-admin")
	wDetail := httptest.NewRecorder()
	handler.ServeHTTP(wDetail, reqDetail)
	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 for detail, got %d", wDetail.Code)
	}
	var resDetail struct {
		Success bool          `json:"success"`
		Log     db.RequestLog `json:"log"`
	}
	if err := json.NewDecoder(wDetail.Body).Decode(&resDetail); err != nil {
		t.Fatalf("decode resDetail failed: %v", err)
	}
	if resDetail.Log.ReqID != "req-a-1" || resDetail.Log.RequestBody != `{"contents":[{"text":"hello from userA"}]}` {
		t.Errorf("detail content mismatch: %+v", resDetail.Log)
	}

	// Test 8: Accounts list (must contain email account)
	reqAcc := httptest.NewRequest(http.MethodGet, "/api/admin/logs/accounts", nil)
	reqAcc.Header.Set("Authorization", "Bearer sk-ant-admin")
	wAcc := httptest.NewRecorder()
	handler.ServeHTTP(wAcc, reqAcc)
	if wAcc.Code != http.StatusOK {
		t.Fatalf("expected 200 for accounts, got %d", wAcc.Code)
	}
	var resAcc struct {
		Success  bool     `json:"success"`
		Accounts []string `json:"accounts"`
	}
	_ = json.NewDecoder(wAcc.Body).Decode(&resAcc)
	hasEmailAccount := false
	for _, acc := range resAcc.Accounts {
		if acc == "harold@nexusquantum.cloud" {
			hasEmailAccount = true
			break
		}
	}
	if !hasEmailAccount {
		t.Errorf("expected accounts to include 'harold@nexusquantum.cloud', got %v", resAcc.Accounts)
	}

	// Test 9: Filter by pool account (email format)
	reqPool := httptest.NewRequest(http.MethodGet, "/api/admin/logs?account=harold@nexusquantum.cloud", nil)
	reqPool.Header.Set("Authorization", "Bearer sk-ant-admin")
	wPool := httptest.NewRecorder()
	handler.ServeHTTP(wPool, reqPool)
	if wPool.Code != http.StatusOK {
		t.Fatalf("expected 200 for pool account query, got %d", wPool.Code)
	}
	var resPool AdminLogsResponse
	_ = json.NewDecoder(wPool.Body).Decode(&resPool)
	if resPool.Total != 1 || resPool.List[0].ReqID != "req-pool-1" {
		t.Errorf("expected 1 log for harold@nexusquantum.cloud, got %d", resPool.Total)
	}

	// Test 10: Legacy timestamp format ("01/02 15:04:05") converted to RFC3339
	logLegacy := &db.RequestLog{
		ReqID:       "req-legacy-1",
		Timestamp:   "08/29 13:00:01",
		Mode:        "local",
		UserID:      userA.Key,
		SessionID:   userA.ID,
		ModelName:   "gemini-legacy",
		InTokens:    100,
		OutTokens:   20,
		StatusCode:  200,
		CacheStatus: "MISS",
	}
	if err := db.InsertRequestLog(logLegacy); err != nil {
		t.Fatalf("insert legacy log failed: %v", err)
	}
	defer func() {
		if db.GlobalDB != nil {
			_, _ = db.GlobalDB.Exec("DELETE FROM request_logs WHERE req_id = 'req-legacy-1'")
		}
	}()

	reqLegacy := httptest.NewRequest(http.MethodGet, "/api/admin/logs?search=req-legacy-1", nil)
	reqLegacy.Header.Set("Authorization", "Bearer sk-ant-admin")
	wLegacy := httptest.NewRecorder()
	handler.ServeHTTP(wLegacy, reqLegacy)
	if wLegacy.Code != http.StatusOK {
		t.Fatalf("expected 200 for legacy log query, got %d", wLegacy.Code)
	}
	var resLegacy AdminLogsResponse
	_ = json.NewDecoder(wLegacy.Body).Decode(&resLegacy)
	if resLegacy.Total != 1 || len(resLegacy.List) == 0 {
		t.Fatalf("expected 1 legacy log, got %d", resLegacy.Total)
	}
	parsedLegacyTime, err := time.Parse(time.RFC3339, resLegacy.List[0].Timestamp)
	if err != nil {
		t.Errorf("expected legacy timestamp to be converted to RFC3339, got %q (err: %v)", resLegacy.List[0].Timestamp, err)
	} else if parsedLegacyTime.Year() != time.Now().Year() {
		t.Errorf("expected timestamp year %d, got %d", time.Now().Year(), parsedLegacyTime.Year())
	}
}

func TestAdminLogs_Global150LimitAndSummaryPreserved(t *testing.T) {
	tempDir, teardown := setupTestLogDB(t)
	defer teardown()

	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)
	statsTracker := NewStatsTracker(pricingMgr)
	statsTracker.Init(tempDir)
	defer statsTracker.Close()

	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	authMgr := NewAuthManager(userMgr)

	handler := NewAPIHandler(authMgr, statsTracker, nil, func(s string) {}, "", nil, nil)

	// 1. 模拟记录 300 次调用至 statsTracker (总请求数累计为 300)
	for i := 1; i <= 300; i++ {
		statsTracker.RecordUsage(RelaySample{
			ReqID:        fmt.Sprintf("req-agg-%d", i),
			UserID:       "user-cumulate",
			ModelName:    "gpt-4o",
			InTokens:     100,
			OutTokens:    50,
			CachedTokens: 20,
			StatusCode:   200,
		})
	}

	// 2. 模拟写入 200 条请求日志至数据库 (受全局 150 条限制自动修剪)
	for i := 1; i <= 200; i++ {
		_ = db.InsertRequestLog(&db.RequestLog{
			ReqID:        fmt.Sprintf("req-agg-%d", i),
			Timestamp:    time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			Mode:         "remote",
			UserID:       "user-cumulate",
			ModelName:    "gpt-4o",
			InTokens:     100,
			OutTokens:    50,
			CachedTokens: 20,
			DurationMs:   150,
			StatusCode:   200,
		})
	}

	// 3. 请求管理后台 /api/admin/logs 接口
	req := httptest.NewRequest(http.MethodGet, "/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer sk-ant-admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var res AdminLogsResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// 4. 断言明细记录数严格限制在 150 条以内
	if res.Total > 150 {
		t.Fatalf("expected res.Total <= 150, got %d", res.Total)
	}
	if res.Total != 150 {
		t.Fatalf("expected res.Total == 150 (all 150 retained), got %d", res.Total)
	}

	// 5. 断言全局累计请求数量与用量被完整保留 (请求数量继续记)
	if res.Summary.TotalRequests != 300 {
		t.Fatalf("expected Summary.TotalRequests == 300 preserved, got %d", res.Summary.TotalRequests)
	}
	if res.Summary.TotalInputTokens != 30000 {
		t.Fatalf("expected Summary.TotalInputTokens == 30000, got %d", res.Summary.TotalInputTokens)
	}
}
