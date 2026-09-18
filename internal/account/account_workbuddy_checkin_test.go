package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// account_workbuddy_checkin_test.go: WorkBuddy 每日活跃签到业务测试与边界测试。
// 遵循 Teardown 隔离规范，所有磁盘写入走 t.TempDir() 自动沙箱清理。

func TestFetchWorkBuddyCheckinStatus_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/billing/meter/checkin-activity-status" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-wb-token" {
			t.Errorf("Authorization 头缺失或错误: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-User-Id") != "uid-12345" {
			t.Errorf("X-User-Id 头错误: %s", r.Header.Get("X-User-Id"))
		}
		if r.Header.Get("User-Agent") != "WorkBuddy/5.5.2" {
			t.Errorf("User-Agent 头错误: %s", r.Header.Get("User-Agent"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"active":           true,
				"today_checked_in": false,
				"streak_days":      2,
				"daily_credit":     30,
				"today_credit":     30,
				"theme_name":       "Buddy 加油站",
				"season":           1,
				"activity_name":    "本期：专家能量包",
			},
		})
	}))
	defer server.Close()

	acc := &Account{
		ID:          "wb-test-1",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-wb-token",
		ProjectID:   "uid-12345",
		Enabled:     true,
	}

	status, err := FetchWorkBuddyCheckinStatus(acc)
	if err != nil {
		t.Fatalf("探测签到状态失败: %v", err)
	}

	if !status.Active {
		t.Errorf("期望 active=true, 实际: false")
	}
	if status.TodayCheckedIn {
		t.Errorf("期望 todayCheckedIn=false, 实际: true")
	}
	if status.StreakDays != 2 {
		t.Errorf("期望 streakDays=2, 实际: %d", status.StreakDays)
	}
	if status.DailyCredit != 30 {
		t.Errorf("期望 dailyCredit=30, 实际: %d", status.DailyCredit)
	}
}

func TestFetchWorkBuddyCheckinStatus_FallbackV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/billing/meter/checkin-activity-status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/billing/meter/checkin-activity-status" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"active":           true,
					"today_checked_in": true,
					"streak_days":      5,
				},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	acc := &Account{
		ID:          "wb-test-v1",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-wb-token",
		Enabled:     true,
	}

	status, err := FetchWorkBuddyCheckinStatus(acc)
	if err != nil {
		t.Fatalf("降级接口探测失败: %v", err)
	}
	if !status.TodayCheckedIn {
		t.Errorf("期望 todayCheckedIn=true, 实际: false")
	}
	if status.StreakDays != 5 {
		t.Errorf("期望 streakDays=5, 实际: %d", status.StreakDays)
	}
}

func TestClaimWorkBuddyDailyCheckin_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/billing/meter/daily-checkin") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"credit":      30,
				"streak_days": 3,
			},
		})
	}))
	defer server.Close()

	acc := &Account{
		ID:          "wb-test-claim",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-wb-token",
		Enabled:     true,
	}

	res, err := ClaimWorkBuddyDailyCheckin(acc)
	if err != nil {
		t.Fatalf("执行签到调用失败: %v", err)
	}
	if !res.Success {
		t.Errorf("期望 Success=true, 实际: false (msg=%s)", res.Message)
	}
	if res.AddedCredit != 30 {
		t.Errorf("期望 AddedCredit=30, 实际: %d", res.AddedCredit)
	}
	if res.StreakDays != 3 {
		t.Errorf("期望 StreakDays=3, 实际: %d", res.StreakDays)
	}
}

func TestClaimWorkBuddyDailyCheckin_InactiveOrExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 10001,
			"msg":  "签到活动未开启或已过期",
		})
	}))
	defer server.Close()

	acc := &Account{
		ID:          "wb-test-expired",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-wb-token",
		Enabled:     true,
	}

	res, err := ClaimWorkBuddyDailyCheckin(acc)
	if err != nil {
		t.Fatalf("业务返回不应作为网络 error 抛出: %v", err)
	}
	if res.Success {
		t.Errorf("期望 Success=false, 实际: true")
	}
	if !res.Skipped {
		t.Errorf("期望 Skipped=true, 实际: false")
	}
	if !strings.Contains(res.Message, "未开启") {
		t.Errorf("期望包含未开启提示, 实际: %s", res.Message)
	}
}

func TestManager_CheckinWorkBuddyAccount_Idempotent(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	today := time.Now().Format("2006-01-02")
	acc := &Account{
		ID:              "wb-idempotent-1",
		Email:           "wb-user@workbuddy.ai",
		Provider:        workbuddyProvider,
		BaseURL:         "http://127.0.0.1:9999", // 故意给非法地址，断言幂等拦截不会发生网络请求
		AccessToken:     "token-xyz",
		Enabled:         true,
		LastCheckinDate: today,
		CheckinStreak:   4,
	}
	mgr.AddAccount(acc)

	res, err := mgr.CheckinWorkBuddyAccount(acc.ID, false, nil)
	if err != nil {
		t.Fatalf("幂等调用不应抛错: %v", err)
	}
	if !res.Skipped {
		t.Errorf("期望 Skipped=true, 实际: false")
	}
	if res.Message != "今日已完成签到" {
		t.Errorf("期望'今日已完成签到', 实际: %s", res.Message)
	}
}

func TestManager_CheckinWorkBuddyAccount_FullFlow(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	quotaFetched := false
	mgr.FetchQuota = func(a *Account) (*QuotaResult, error) {
		quotaFetched = true
		return &QuotaResult{}, nil
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "checkin-activity-status") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"active":           true,
					"today_checked_in": false,
					"streak_days":      0,
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "daily-checkin") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"credit":      30,
					"streak_days": 1,
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	acc := &Account{
		ID:          "wb-full-flow",
		Email:       "checkin@workbuddy.ai",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "valid-token",
		Enabled:     true,
	}
	mgr.AddAccount(acc)

	var logRecords []string
	logger := func(msg string) {
		logRecords = append(logRecords, msg)
	}

	res, err := mgr.CheckinWorkBuddyAccount(acc.ID, false, logger)
	if err != nil {
		t.Fatalf("签到执行异常: %v", err)
	}
	if !res.Success {
		t.Fatalf("期望 Success=true, 实际: false (msg=%s)", res.Message)
	}
	if res.AddedCredit != 30 {
		t.Errorf("期望 AddedCredit=30, 实际: %d", res.AddedCredit)
	}
	if res.StreakDays != 1 {
		t.Errorf("期望 StreakDays=1, 实际: %d", res.StreakDays)
	}

	today := time.Now().Format("2006-01-02")
	target := mgr.GetAccountByID(acc.ID)
	if target.LastCheckinDate != today {
		t.Errorf("账号 LastCheckinDate 期望为 %s, 实际: %s", today, target.LastCheckinDate)
	}
	if target.CheckinStreak != 1 {
		t.Errorf("账号 CheckinStreak 期望为 1, 实际: %d", target.CheckinStreak)
	}

	// 等待异步 FetchQuota 触发
	time.Sleep(800 * time.Millisecond)
	if !quotaFetched {
		t.Errorf("签到成功后未触发 FetchQuota 刷新配额")
	}

	if len(logRecords) == 0 {
		t.Errorf("期望产生日志记录，实际为空")
	}
}

func TestManager_RunDailyCheckinForWorkBuddyPool(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "checkin-activity-status") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"active":           true,
					"today_checked_in": false,
					"streak_days":      0,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"credit":      30,
				"streak_days": 1,
			},
		})
	}))
	defer server.Close()

	acc1 := &Account{
		ID:          "wb-pool-1",
		Email:       "wb1@test.ai",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "token-1",
		Enabled:     true,
	}
	acc2 := &Account{
		ID:          "wb-pool-disabled",
		Email:       "wb2@test.ai",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "token-2",
		Enabled:     false, // 禁用账号不应发起签到
	}
	accOther := &Account{
		ID:          "other-1",
		Email:       "other@test.ai",
		Provider:    "other",
		BaseURL:     server.URL,
		AccessToken: "token-other",
		Enabled:     true,
	}

	mgr.AddAccount(acc1)
	mgr.AddAccount(acc2)
	mgr.AddAccount(accOther)

	results := mgr.RunDailyCheckinForWorkBuddyPool(false, nil)
	if len(results) != 1 {
		t.Fatalf("期望处理 1 个有效账号，实际处理: %d", len(results))
	}
	if results[0].AccountID != "wb-pool-1" {
		t.Errorf("处理账号 ID 不匹配: %s", results[0].AccountID)
	}
	if !results[0].Success {
		t.Errorf("签到期望成功，实际: %s", results[0].Message)
	}
}
