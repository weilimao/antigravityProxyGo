package account

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// account_workbuddy_checkin_intl_test.go: WorkBuddy 国际版 (workbuddy.ai) 每日活跃奖励 (Bonus Pack) 独立测试。
// 遵循沙箱隔离与严格 Teardown 准则，所有本地状态落盘均走 t.TempDir()。

func TestFetchWorkBuddyCheckinStatus_International_TodayNotClaimed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/billing/meter/get-user-resource-free-packages" {
			t.Errorf("请求路径错误: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("User-Agent") != "WorkBuddy/5.7.6" {
			t.Errorf("国际版 User-Agent 期望 WorkBuddy/5.7.6, 实际: %s", r.Header.Get("User-Agent"))
		}

		// 模拟历史上有 8 个 Bonus Pack，但今天尚未产生
		yesterdayMs := time.Now().Add(-24 * time.Hour).UnixMilli()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"TotalCount": 8,
				"Accounts": []map[string]interface{}{
					{
						"PackageCode":         WorkBuddyPackageBonusPack,
						"PackageName":         "Bonus Pack",
						"CreateTime":          yesterdayMs,
						"CycleCapacitySize":   30,
						"CycleCapacityRemain": 30,
					},
				},
			},
		})
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-1",
		Email:       "weilimao0714@gmail.com",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-intl-token",
		Enabled:     true,
	}

	status, err := FetchWorkBuddyCheckinStatus(acc)
	if err != nil {
		t.Fatalf("探测国际版活跃状态失败: %v", err)
	}

	if !status.Active {
		t.Errorf("期望 Active=true, 实际: false")
	}
	if status.TodayCheckedIn {
		t.Errorf("期望 TodayCheckedIn=false, 实际: true")
	}
	if status.StreakDays != 8 {
		t.Errorf("期望 StreakDays=8, 实际: %d", status.StreakDays)
	}
	if status.DailyCredit != 30 {
		t.Errorf("期望 DailyCredit=30, 实际: %d", status.DailyCredit)
	}
	if status.ClaimButtonText != "激活今日活跃" {
		t.Errorf("期望 ClaimButtonText='激活今日活跃', 实际: %s", status.ClaimButtonText)
	}
}

func TestFetchWorkBuddyCheckinStatus_International_TodayClaimed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		todayMs := time.Now().UnixMilli()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"TotalCount": 9,
				"Accounts": []map[string]interface{}{
					{
						"PackageCode":         WorkBuddyPackageBonusPack,
						"PackageName":         "Bonus Pack",
						"CreateTime":          todayMs,
						"CycleCapacitySize":   30,
						"CycleCapacityRemain": 30,
					},
				},
			},
		})
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-claimed",
		Email:       "weilimao0714@gmail.com",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-intl-token",
		Enabled:     true,
	}

	status, err := FetchWorkBuddyCheckinStatus(acc)
	if err != nil {
		t.Fatalf("探测国际版活跃状态失败: %v", err)
	}

	if !status.TodayCheckedIn {
		t.Errorf("期望 TodayCheckedIn=true, 实际: false")
	}
	if status.ClaimButtonText != "今日已领" {
		t.Errorf("期望 ClaimButtonText='今日已领', 实际: %s", status.ClaimButtonText)
	}
}

func TestClaimWorkBuddyDailyCheckin_International_AlreadyClaimed(t *testing.T) {
	chatCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			chatCalled = true
		}
		todayMs := time.Now().UnixMilli()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"TotalCount": 5,
				"Accounts": []map[string]interface{}{
					{
						"PackageCode":       WorkBuddyPackageBonusPack,
						"CreateTime":        todayMs,
						"CycleCapacitySize": 30,
					},
				},
			},
		})
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-already",
		Email:       "user@gmail.com",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-token",
		Enabled:     true,
	}

	res, err := ClaimWorkBuddyDailyCheckin(acc)
	if err != nil {
		t.Fatalf("今日已领时调用失败: %v", err)
	}
	if !res.Success || !res.TodayCheckedIn {
		t.Errorf("期望已领成功返回, 实际: %+v", res)
	}
	if chatCalled {
		t.Errorf("今日已领情况下不应重复调用 chat/completions 促发心跳")
	}
}

func TestClaimWorkBuddyDailyCheckin_International_TriggerAndClaim(t *testing.T) {
	var callCount int32
	var chatReceived bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "chat/completions") {
			chatReceived = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), DefaultWorkBuddyModel) {
				t.Errorf("微心跳应使用默认 0 积分模型 %s", DefaultWorkBuddyModel)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n`))
			return
		}

		if strings.Contains(r.URL.Path, "get-user-resource-free-packages") {
			curr := atomic.AddInt32(&callCount, 1)
			if curr == 1 {
				// 第一次查询：今日未领
				yesterdayMs := time.Now().Add(-24 * time.Hour).UnixMilli()
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"code": 0,
					"data": map[string]interface{}{
						"TotalCount": 3,
						"Accounts": []map[string]interface{}{
							{
								"PackageCode":       WorkBuddyPackageBonusPack,
								"CreateTime":        yesterdayMs,
								"CycleCapacitySize": 30,
							},
						},
					},
				})
				return
			}
			// 第二次查询：微心跳促发后，云端新增今日 Bonus Pack
			todayMs := time.Now().UnixMilli()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"TotalCount": 4,
					"Accounts": []map[string]interface{}{
						{
							"PackageCode":       WorkBuddyPackageBonusPack,
							"CreateTime":        todayMs,
							"CycleCapacitySize": 30,
						},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-trigger",
		Email:       "weilimao0714@gmail.com",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-intl-token",
		Enabled:     true,
	}

	res, err := ClaimWorkBuddyDailyCheckin(acc)
	if err != nil {
		t.Fatalf("激活今日活跃失败: %v", err)
	}

	if !chatReceived {
		t.Errorf("期望向模型端点发送微心跳促发云端挂载，但未检测到请求")
	}
	if !res.Success {
		t.Errorf("期望 Success=true, 实际: false (%s)", res.Message)
	}
	if res.AddedCredit != 30 {
		t.Errorf("期望 AddedCredit=30, 实际: %d", res.AddedCredit)
	}
	if res.StreakDays != 4 {
		t.Errorf("期望 StreakDays=4, 实际: %d", res.StreakDays)
	}
}

func TestManager_CheckinWorkBuddyAccount_International_FullFlow(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	quotaRefreshed := false
	mgr.FetchQuota = func(a *Account) (*QuotaResult, error) {
		quotaRefreshed = true
		return &QuotaResult{}, nil
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "chat/completions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: [DONE]\n\n`))
			return
		}
		if strings.Contains(r.URL.Path, "get-user-resource-free-packages") {
			todayMs := time.Now().UnixMilli()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"TotalCount": 10,
					"Accounts": []map[string]interface{}{
						{
							"PackageCode":       WorkBuddyPackageBonusPack,
							"CreateTime":        todayMs,
							"CycleCapacitySize": 30,
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-manager",
		Email:       "weilimao0714@gmail.com",
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-token",
		Enabled:     true,
	}
	mgr.AddAccount(acc)

	var logMsgs []string
	res, err := mgr.CheckinWorkBuddyAccount(acc.ID, false, func(msg string) {
		logMsgs = append(logMsgs, msg)
	})
	if err != nil {
		t.Fatalf("Manager 国际版签到全流程失败: %v", err)
	}
	if !res.Success {
		t.Fatalf("签到结果期望 Success=true, 实际: false (%s)", res.Message)
	}

	today := time.Now().Format("2006-01-02")
	target := mgr.GetAccountByID(acc.ID)
	if target.LastCheckinDate != today {
		t.Errorf("期望 LastCheckinDate 更新为 %s, 实际: %s", today, target.LastCheckinDate)
	}

	// 验证触发配额刷新
	time.Sleep(700 * time.Millisecond)
	if !quotaRefreshed {
		t.Errorf("期望成功后触发 FetchQuota 刷新配额")
	}

	// 验证当日防重幂等
	res2, err2 := mgr.CheckinWorkBuddyAccount(acc.ID, false, nil)
	if err2 != nil {
		t.Fatalf("二次调用不应报错: %v", err2)
	}
	if !res2.Skipped {
		t.Errorf("二次调用期望 Skipped=true, 实际: false")
	}
}

func TestClaimWorkBuddyDailyCheckin_International_TriggerNoImmediatePackage(t *testing.T) {
	var pluginAccountsCalled bool
	var dosageNotifyCalled bool
	var chatReceived bool
	var reportReceived bool
	var reportedEventsCount int
	var reportedUserId string

	expectedUID := "test-intl-user-uid-1234"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "plugin/accounts") {
			pluginAccountsCalled = true
			if r.Header.Get("X-User-Id") != expectedUID {
				t.Errorf("plugin/accounts 缺少正确 X-User-Id, 实际: %s", r.Header.Get("X-User-Id"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			return
		}
		if strings.Contains(r.URL.Path, "get-dosage-notify") {
			dosageNotifyCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			return
		}
		if strings.Contains(r.URL.Path, "chat/completions") {
			chatReceived = true
			if r.Header.Get("X-Agent-Intent") != "craft" {
				t.Errorf("chat/completions 期望 X-Agent-Intent=craft, 实际: %s", r.Header.Get("X-Agent-Intent"))
			}
			if r.Header.Get("X-User-Id") != expectedUID {
				t.Errorf("chat/completions 期望 X-User-Id=%s, 实际: %s", expectedUID, r.Header.Get("X-User-Id"))
			}
			if r.Header.Get("X-Conversation-ID") == "" {
				t.Errorf("chat/completions 期望携带 X-Conversation-ID")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "growthEvent") || !strings.Contains(string(body), "chat_request_send") {
				t.Errorf("chat/completions 必须携带 growthEvent 遥测参数, 实际请求体: %s", string(body))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n`))
			return
		}
		if strings.Contains(r.URL.Path, "v2/report") {
			reportReceived = true
			body, _ := io.ReadAll(r.Body)
			var evts []map[string]interface{}
			_ = json.Unmarshal(body, &evts)
			reportedEventsCount = len(evts)
			if len(evts) > 0 {
				if uid, ok := evts[0]["userId"].(string); ok {
					reportedUserId = uid
				}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "msg": "OK"})
			return
		}
		if strings.Contains(r.URL.Path, "get-user-resource-free-packages") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"TotalCount": 0,
					"Accounts":   []map[string]interface{}{},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:          "wb-intl-no-immediate",
		Email:       "weilimao95@gmail.com",
		ProjectID:   expectedUID,
		Provider:    workbuddyProvider,
		BaseURL:     server.URL,
		AccessToken: "test-intl-token",
		Enabled:     true,
	}

	res, err := ClaimWorkBuddyDailyCheckin(acc)
	if err != nil {
		t.Fatalf("调用不应报错: %v", err)
	}

	if !pluginAccountsCalled {
		t.Errorf("期望调用 /v2/plugin/accounts 握手")
	}
	if !dosageNotifyCalled {
		t.Errorf("期望调用 /v2/billing/meter/get-dosage-notify 握手")
	}
	if !chatReceived {
		t.Errorf("期望向 /v2/chat/completions 发送 Craft 活跃请求")
	}
	if !reportReceived {
		t.Errorf("期望向 /v2/report 上报完整遥测事件")
	}
	if reportedEventsCount != 5 {
		t.Errorf("期望上报 5 大遥测事件链 (plugin_status + chat_request_send + message + responses), 实际: %d", reportedEventsCount)
	}
	if reportedUserId != expectedUID {
		t.Errorf("遥测事件中的 userId 期望为腾讯云官方 uid (%s), 实际: %s", expectedUID, reportedUserId)
	}
	if !res.Success {
		t.Errorf("已成功向官方上报 Craft 活跃事件时 Success 期望为 true, 实际: false")
	}
	if res.TodayCheckedIn {
		t.Errorf("云端今日尚未到账（T+1 异步发放）时 TodayCheckedIn 期望为 false, 实际: true")
	}
	if !strings.Contains(res.Message, "T+1") {
		t.Errorf("提示信息期望包含 T+1 说明, 实际: %s", res.Message)
	}
}

func TestManager_CheckinWorkBuddyAccount_International_DirtyStateHealing(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "plugin/accounts") ||
			strings.Contains(r.URL.Path, "get-dosage-notify") ||
			strings.Contains(r.URL.Path, "v2/report") ||
			strings.Contains(r.URL.Path, "chat/completions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`data: [DONE]\n\n`))
			return
		}
		if strings.Contains(r.URL.Path, "get-user-resource-free-packages") {
			// 云端目前总包数为 0
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"TotalCount": 0,
					"Accounts":   []map[string]interface{}{},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close() // Teardown

	acc := &Account{
		ID:              "wb-intl-dirty",
		Email:           "weilimao95@gmail.com",
		ProjectID:       "uid-dirty-1",
		Provider:        workbuddyProvider,
		BaseURL:         server.URL,
		AccessToken:     "test-token",
		Enabled:         true,
		LastCheckinDate: "2026-10-10", // 注入历史脏签到日期
		CheckinStreak:   1,            // 注入虚标天数
	}
	mgr.AddAccount(acc)

	res, err := mgr.CheckinWorkBuddyAccount(acc.ID, true, nil)
	if err != nil {
		t.Fatalf("Manager 签到调用失败: %v", err)
	}
	if !res.Success {
		t.Errorf("上报成功期望 Success=true，实际: false")
	}

	target := mgr.GetAccountByID(acc.ID)
	// 验证自愈：云端未到账时，脏 LastCheckinDate 必须被清空，CheckinStreak 必须同步为真实云端天数 0
	if target.LastCheckinDate != "" {
		t.Errorf("自愈失败: LastCheckinDate 脏数据未被清空，实际: %s", target.LastCheckinDate)
	}
	if target.CheckinStreak != 0 {
		t.Errorf("自愈失败: CheckinStreak 期望纠偏为云端真实天数 0，实际: %d", target.CheckinStreak)
	}
}
