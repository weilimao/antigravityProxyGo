package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

// account_workbuddy_checkin.go: WorkBuddy 每日活跃签到送积分业务逻辑。
//
// 官方协议对齐:
//   - 状态查询: POST {BaseURL}/v2/billing/meter/checkin-activity-status
//   - 签到领取: POST {BaseURL}/v2/billing/meter/daily-checkin
//   - 认证方式: Bearer Token + X-User-Id
//   - 防风控与幂等: 本地 LastCheckinDate 记录单日签到状态，避免重复请求上游。

const (
	defaultCheckinTimeout = 15 * time.Second
)

// WorkBuddyCheckinStatus 对应 checkin-activity-status 接口返回的数据结构。
type WorkBuddyCheckinStatus struct {
	Active            bool     `json:"active"`
	TodayCheckedIn    bool     `json:"todayCheckedIn"`
	StreakDays        int      `json:"streakDays"`
	DailyCredit       int64    `json:"dailyCredit"`
	TodayCredit       int64    `json:"todayCredit"`
	IsStreakDay       bool     `json:"isStreakDay"`
	NextStreakDay     int      `json:"nextStreakDay"`
	StreakBonusDays   int      `json:"streakBonusDays"`
	StreakBonusCredit int64    `json:"streakBonusCredit"`
	CheckinDates      []string `json:"checkinDates"`
	ThemeName         string   `json:"themeName"`
	Season            int      `json:"season"`
	ActivityName      string   `json:"activityName"`
	ClaimButtonText   string   `json:"claimButtonText"`
}

// WorkBuddyCheckinResult 是对账号执行签到后的统一反馈结果。
type WorkBuddyCheckinResult struct {
	Success        bool   `json:"success"`
	AccountID      string `json:"accountId"`
	Email          string `json:"email"`
	TodayCheckedIn bool   `json:"todayCheckedIn"`
	AddedCredit    int64  `json:"addedCredit"`
	StreakDays     int    `json:"streakDays"`
	Message        string `json:"message"`
	Skipped        bool   `json:"skipped"`
}

func buildWorkBuddyCheckinHeaders(acc *Account) http.Header {
	headers := make(http.Header)
	hMap := BuildWorkBuddyHeaders(acc)
	for k, v := range hMap {
		headers.Set(k, v)
	}
	headers.Set("Accept-Language", "zh")
	return headers
}

func postWorkBuddyEndpoint(ctx context.Context, client *http.Client, baseURL, path string, headers http.Header) (int, []byte, error) {
	return postWorkBuddyEndpointJSON(ctx, client, baseURL, path, headers, []byte("{}"))
}

func postWorkBuddyEndpointJSON(ctx context.Context, client *http.Client, baseURL, path string, headers http.Header, bodyBytes []byte) (int, []byte, error) {
	fullURL := strings.TrimRight(baseURL, "/") + path
	if len(bodyBytes) == 0 {
		bodyBytes = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, err
	}
	req.Header = headers

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// FetchWorkBuddyCheckinStatus 探测指定 WorkBuddy 账号当前的每日签到/活跃活动与今日状态。
func FetchWorkBuddyCheckinStatus(acc *Account, customClient ...*http.Client) (*WorkBuddyCheckinStatus, error) {
	if acc == nil || acc.Provider != workbuddyProvider {
		return nil, errors.New("账号无效或非 WorkBuddy 类型")
	}
	if strings.TrimSpace(acc.AccessToken) == "" {
		return nil, errors.New("账号 AccessToken 为空")
	}

	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
		if acc.IsWorkBuddyDomestic() {
			baseURL = DefaultWorkBuddyDomesticBaseURL
		} else {
			baseURL = DefaultWorkBuddyBaseURL
		}
	}

	client := netutil.NewClient(defaultCheckinTimeout)
	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	}

	headers := buildWorkBuddyCheckinHeaders(acc)
	ctx := context.Background()

	// 国际版 (www.workbuddy.ai) 与国内版 (copilot.tencent.com) 协议分流
	if !acc.IsWorkBuddyDomestic() {
		return fetchInternationalActivityStatus(ctx, client, baseURL, headers, acc)
	}

	return fetchDomesticCheckinStatus(ctx, client, baseURL, headers)
}

// fetchDomesticCheckinStatus 处理国内版“Buddy 加油站”每日签到活动状态探测。
func fetchDomesticCheckinStatus(ctx context.Context, client *http.Client, baseURL string, headers http.Header) (*WorkBuddyCheckinStatus, error) {
	statusCode, body, err := postWorkBuddyEndpoint(ctx, client, baseURL, "/v2/billing/meter/checkin-activity-status", headers)
	if err != nil {
		return nil, fmt.Errorf("签到状态接口请求失败: %w", err)
	}
	if statusCode == http.StatusNotFound {
		statusCode, body, err = postWorkBuddyEndpoint(ctx, client, baseURL, "/billing/meter/checkin-activity-status", headers)
		if err != nil || statusCode == http.StatusNotFound {
			statusCode, body, err = postWorkBuddyEndpoint(ctx, client, baseURL, "/billing/meter/checkin-status", headers)
			if err != nil {
				return nil, fmt.Errorf("降级请求签到状态失败: %w", err)
			}
		}
	}

	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return nil, fmt.Errorf("凭据失效 (HTTP %d)，请重新授权", statusCode)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回异常 (HTTP %d): %s", statusCode, string(body))
	}

	var respData struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Active            bool     `json:"active"`
			TodayCheckedIn    bool     `json:"today_checked_in"`
			StreakDays        int      `json:"streak_days"`
			DailyCredit       int64    `json:"daily_credit"`
			TodayCredit       int64    `json:"today_credit"`
			IsStreakDay       bool     `json:"is_streak_day"`
			NextStreakDay     int      `json:"next_streak_day"`
			StreakBonusDays   int      `json:"streak_bonus_days"`
			StreakBonusCredit int64    `json:"streak_bonus_credit"`
			CheckinDates      []string `json:"checkin_dates"`
			ThemeName         string   `json:"theme_name"`
			Season            int      `json:"season"`
			ActivityName      string   `json:"activity_name"`
			ClaimButtonText   string   `json:"claim_button_text"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &respData); err != nil {
		return nil, fmt.Errorf("解析签到状态响应 JSON 失败: %w", err)
	}

	return &WorkBuddyCheckinStatus{
		Active:            respData.Data.Active,
		TodayCheckedIn:    respData.Data.TodayCheckedIn,
		StreakDays:        respData.Data.StreakDays,
		DailyCredit:       respData.Data.DailyCredit,
		TodayCredit:       respData.Data.TodayCredit,
		IsStreakDay:       respData.Data.IsStreakDay,
		NextStreakDay:     respData.Data.NextStreakDay,
		StreakBonusDays:   respData.Data.StreakBonusDays,
		StreakBonusCredit: respData.Data.StreakBonusCredit,
		CheckinDates:      respData.Data.CheckinDates,
		ThemeName:         respData.Data.ThemeName,
		Season:            respData.Data.Season,
		ActivityName:      respData.Data.ActivityName,
		ClaimButtonText:   respData.Data.ClaimButtonText,
	}, nil
}

// fetchInternationalActivityStatus 处理国际版每日活跃赠送包 (Bonus Pack) 状态探测。
func fetchInternationalActivityStatus(ctx context.Context, client *http.Client, baseURL string, headers http.Header, acc *Account) (*WorkBuddyCheckinStatus, error) {
	todayStr := time.Now().Format("2006-01-02")
	queryPayload := map[string]interface{}{
		"PageNumber":   1,
		"PageSize":     50,
		"Status":       []int{0, 3},
		"PackageCodes": []string{WorkBuddyPackageBonusPack},
	}
	bodyBytes, _ := json.Marshal(queryPayload)

	statusCode, body, err := postWorkBuddyEndpointJSON(ctx, client, baseURL, "/billing/meter/get-user-resource-free-packages", headers, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("国际版活跃资源包接口请求失败: %w", err)
	}

	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return nil, fmt.Errorf("凭据失效 (HTTP %d)，请重新授权", statusCode)
	}

	if statusCode == http.StatusOK {
		var respData struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Accounts []struct {
					PackageCode         string `json:"PackageCode"`
					PackageName         string `json:"PackageName"`
					CreateTime          int64  `json:"CreateTime"`
					CycleCapacitySize   int64  `json:"CycleCapacitySize"`
					CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
					CycleStartTime      string `json:"CycleStartTime"`
					CycleEndTime        string `json:"CycleEndTime"`
				} `json:"Accounts"`
				PackageList []struct {
					PackageCode         string `json:"PackageCode"`
					PackageName         string `json:"PackageName"`
					CreateTime          int64  `json:"CreateTime"`
					CycleCapacitySize   int64  `json:"CycleCapacitySize"`
					CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
					CycleStartTime      string `json:"CycleStartTime"`
					CycleEndTime        string `json:"CycleEndTime"`
				} `json:"PackageList"`
				TotalCount int `json:"TotalCount"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &respData); err == nil && respData.Code == 0 {
			todayCheckedIn := false
			items := respData.Data.Accounts
			if len(items) == 0 && len(respData.Data.PackageList) > 0 {
				items = respData.Data.PackageList
			}
			totalCount := respData.Data.TotalCount
			if totalCount == 0 && len(items) > 0 {
				totalCount = len(items)
			}

			for _, a := range items {
				if a.PackageCode == WorkBuddyPackageBonusPack {
					// 比对毫秒时间戳对应的本地自然日
					if a.CreateTime > 0 {
						day := time.UnixMilli(a.CreateTime).Format("2006-01-02")
						if day == todayStr {
							todayCheckedIn = true
							break
						}
					}
					if len(a.CycleStartTime) >= 10 && a.CycleStartTime[:10] == todayStr {
						todayCheckedIn = true
						break
					}
				}
			}

			claimText := "激活今日活跃"
			if todayCheckedIn {
				claimText = "今日已领"
			}

			return &WorkBuddyCheckinStatus{
				Active:          true, // 国际版限免长期有效
				TodayCheckedIn:  todayCheckedIn,
				StreakDays:      totalCount,
				DailyCredit:     30,
				TodayCredit:     30,
				ThemeName:       "每日活跃奖励",
				ActivityName:    "WorkBuddy 活跃奖励 (30 积分/天)",
				ClaimButtonText: claimText,
			}, nil
		}
	}

	// 降级兜底：请求 /billing/meter/get-user-resource-summary
	summaryCode, summaryBody, sErr := postWorkBuddyEndpoint(ctx, client, baseURL, "/billing/meter/get-user-resource-summary", headers)
	if sErr == nil && summaryCode == http.StatusOK {
		var summaryData struct {
			Code int `json:"code"`
			Data struct {
				Packages []struct {
					PackageCode string `json:"PackageCode"`
					TotalCount  int    `json:"TotalCount"`
				} `json:"Packages"`
			} `json:"data"`
		}
		if json.Unmarshal(summaryBody, &summaryData) == nil && summaryData.Code == 0 {
			count := 0
			for _, p := range summaryData.Data.Packages {
				if p.PackageCode == WorkBuddyPackageBonusPack {
					count = p.TotalCount
					break
				}
			}
			todayCheckedIn := (acc != nil && acc.LastCheckinDate == todayStr)
			return &WorkBuddyCheckinStatus{
				Active:          true,
				TodayCheckedIn:  todayCheckedIn,
				StreakDays:      count,
				DailyCredit:     30,
				TodayCredit:     30,
				ThemeName:       "每日活跃奖励",
				ActivityName:    "WorkBuddy 活跃奖励 (30 积分/天)",
				ClaimButtonText: "激活今日活跃",
			}, nil
		}
	}

	return nil, fmt.Errorf("探测国际版活跃状态失败 (HTTP %d): %s", statusCode, string(body))
}

// ClaimWorkBuddyDailyCheckin 对指定 WorkBuddy 账号执行每日签到或活跃奖励激活。
func ClaimWorkBuddyDailyCheckin(acc *Account, customClient ...*http.Client) (*WorkBuddyCheckinResult, error) {
	if acc == nil || acc.Provider != workbuddyProvider {
		return nil, errors.New("账号无效或非 WorkBuddy 类型")
	}
	if strings.TrimSpace(acc.AccessToken) == "" {
		return nil, errors.New("账号 AccessToken 为空")
	}

	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
		if acc.IsWorkBuddyDomestic() {
			baseURL = DefaultWorkBuddyDomesticBaseURL
		} else {
			baseURL = DefaultWorkBuddyBaseURL
		}
	}

	client := netutil.NewClient(defaultCheckinTimeout)
	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	}

	headers := buildWorkBuddyCheckinHeaders(acc)
	ctx := context.Background()

	// 国际版与国内版分流执行
	if !acc.IsWorkBuddyDomestic() {
		return claimInternationalActivity(ctx, client, baseURL, headers, acc)
	}

	return claimDomesticCheckin(ctx, client, baseURL, headers, acc)
}

// claimDomesticCheckin 执行国内版签到领包。
func claimDomesticCheckin(ctx context.Context, client *http.Client, baseURL string, headers http.Header, acc *Account) (*WorkBuddyCheckinResult, error) {
	statusCode, body, err := postWorkBuddyEndpoint(ctx, client, baseURL, "/v2/billing/meter/daily-checkin", headers)
	if err != nil {
		return nil, fmt.Errorf("执行每日签到请求失败: %w", err)
	}
	if statusCode == http.StatusNotFound {
		statusCode, body, err = postWorkBuddyEndpoint(ctx, client, baseURL, "/billing/meter/daily-checkin", headers)
		if err != nil {
			return nil, fmt.Errorf("降级请求执行每日签到失败: %w", err)
		}
	}

	res := &WorkBuddyCheckinResult{
		AccountID: acc.ID,
		Email:     acc.Email,
	}

	var respData struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Credit      int64 `json:"credit"`
			StreakDays  int   `json:"streak_days"`
			IsStreakDay bool  `json:"is_streak_day"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &respData); err != nil {
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("执行签到失败 (HTTP %d): %s", statusCode, string(body))
		}
		return nil, fmt.Errorf("解析签到响应失败: %w", err)
	}

	if respData.Code == 0 {
		res.Success = true
		res.TodayCheckedIn = true
		res.AddedCredit = respData.Data.Credit
		res.StreakDays = respData.Data.StreakDays
		res.Message = fmt.Sprintf("签到成功，获得 %d 积分，连续签到 %d 天", respData.Data.Credit, respData.Data.StreakDays)
		return res, nil
	}

	res.Success = false
	res.Message = respData.Msg
	if respData.Code == 10001 || strings.Contains(respData.Msg, "未开启") || strings.Contains(respData.Msg, "已过期") {
		res.Skipped = true
	}
	return res, nil
}

// claimInternationalActivity 执行国际版活跃奖励激活与挂载。
// 对齐官方客户端 UserGrowth / PluginStatusDailyReporter 协议。
func claimInternationalActivity(ctx context.Context, client *http.Client, baseURL string, headers http.Header, acc *Account) (*WorkBuddyCheckinResult, error) {
	res := &WorkBuddyCheckinResult{
		AccountID: acc.ID,
		Email:     acc.Email,
	}

	// 1. 先探测当前今日是否已具备 Bonus Pack
	status, _ := fetchInternationalActivityStatus(ctx, client, baseURL, headers, acc)
	if status != nil && status.TodayCheckedIn {
		res.Success = true
		res.TodayCheckedIn = true
		res.AddedCredit = 0
		res.StreakDays = status.StreakDays
		res.Message = fmt.Sprintf("今日活跃奖励已到账（30 积分），无需重复激活，已累计活跃 %d 天", status.StreakDays)
		return res, nil
	}

	effectiveUID := GetWorkBuddyEffectiveUID(acc)
	if effectiveUID == "" {
		effectiveUID = acc.ProjectID
	}
	if effectiveUID == "" {
		effectiveUID = acc.ID
	}
	effectiveEmail := GetWorkBuddyEffectiveEmail(acc)
	if effectiveEmail == "" {
		effectiveEmail = acc.Email
	}

	// 2. 前置官方账号与账单握手 (1:1 对齐官方客户端 codebuddy-headless 行为)
	preHeaders := headers.Clone()
	preHeaders.Set("X-User-Id", effectiveUID)
	preHeaders.Set("X-Current-Account-Id", effectiveUID)
	_, _, _ = postWorkBuddyEndpoint(ctx, client, baseURL, "/v2/plugin/accounts", preHeaders)
	_, _, _ = postWorkBuddyEndpointJSON(ctx, client, baseURL, "/v2/billing/meter/get-dosage-notify", preHeaders, []byte(`{"product":"WorkBuddy"}`))

	// 3. 构造 Craft 专属请求参数与 growthEvent 遥测参数
	convID := GenerateWorkBuddyUUID()
	reqID := GenerateWorkBuddyHex32()
	msgID := GenerateWorkBuddyHex32()

	growthEventObj := []map[string]interface{}{
		{
			"eventCode": "chat_request_send",
			"id":        convID,
			"extra": map[string]interface{}{
				"inputLength":      18,
				"requestModelId":   DefaultWorkBuddyModel,
				"requestModelName": "Deepseek-V4.1-Flash",
				"mode":             "craft",
				"command":          "",
			},
		},
	}
	growthEventJSON, _ := json.Marshal(growthEventObj)

	// 采用 0 积分免费模型 deepseek-v4.1-flash 发送一个微心跳 (stream: true)
	pingPayload := map[string]interface{}{
		"model":  DefaultWorkBuddyModel,
		"stream": true,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Hello WorkBuddy"},
		},
		"max_tokens": 1,
		"extra_vars": map[string]interface{}{
			"growthEvent": string(growthEventJSON),
		},
	}
	pingBytes, _ := json.Marshal(pingPayload)

	chatHeaders := headers.Clone()
	chatHeaders.Set("Content-Type", "application/json")
	chatHeaders.Set("Accept", "text/event-stream")
	chatHeaders.Set("X-User-Id", effectiveUID)
	chatHeaders.Set("X-Current-Account-Id", effectiveUID)
	chatHeaders.Set("X-Conversation-ID", convID)
	chatHeaders.Set("X-Conversation-Request-ID", reqID)
	chatHeaders.Set("X-Conversation-Message-ID", msgID)
	chatHeaders.Set("X-Request-ID", msgID)
	chatHeaders.Set("X-Root-Request-ID", reqID)
	chatHeaders.Set("X-Agent-Intent", "craft")
	chatHeaders.Set("X-Agent-Type", "main")
	chatHeaders.Set("X-IDE-Type", "WorkBuddy")
	chatHeaders.Set("X-IDE-Name", "WorkBuddy")
	chatHeaders.Set("X-IDE-Version", "5.7.6")
	chatHeaders.Set("X-Product", "WorkBuddy")
	chatHeaders.Set("User-Agent", "WorkBuddy/5.7.6")
	chatHeaders.Set("X-Private-Data", "false")

	code, body, chatErr := postWorkBuddyEndpointJSON(ctx, client, baseURL, "/v2/chat/completions", chatHeaders, pingBytes)
	if chatErr != nil {
		return nil, fmt.Errorf("触发国际版活跃状态请求失败: %w", chatErr)
	}

	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return nil, fmt.Errorf("凭据失效 (HTTP %d)，请重新授权", code)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("触发活跃返回异常 (HTTP %d): %s", code, string(body))
	}

	// 4. 对齐官方客户端，向 /v2/report 完整上报 5 大遥测事件链
	nowMs := time.Now().UnixMilli()
	reportEvents := []map[string]interface{}{
		{
			"eventCode":     "plugin_status",
			"timestamp":     nowMs,
			"reportDelay":   0,
			"status":        "info",
			"text":          "pluginStart",
			"isInterval":    false,
			"buddyId":       "",
			"buddyName":     "",
			"timezone":      "Asia/Shanghai",
			"userId":        effectiveUID,
			"username":      effectiveEmail,
			"userNickname":  effectiveEmail,
			"product":       "SaaS",
			"releaseDate":   1791345433118,
			"commit":        "3e43260f771393d75c6504db99e751eb03dea8db",
			"os":            "Windows_NT(10.0.22631)",
			"osRelease":     "10.0.22631",
			"ideType":       "workbuddy",
			"ideName":       "WorkBuddy",
			"ideVersion":    "0.2.26",
			"pluginVersion": "5.7.6",
			"env":           "production",
		},
		{
			"eventCode":        "chat_request_send",
			"timestamp":        nowMs + 10,
			"conversationId":   convID,
			"requestId":        reqID,
			"messageId":        msgID,
			"userId":           effectiveUID,
			"username":         effectiveEmail,
			"userNickname":     effectiveEmail,
			"mode":             "craft",
			"agentName":        "cli",
			"agentType":        "main",
			"extName":          "workbuddy-desktop",
			"status":           "success",
			"requestModelId":   DefaultWorkBuddyModel,
			"requestModelName": "Deepseek-V4.1-Flash",
			"timezone":         "Asia/Shanghai",
			"product":          "SaaS",
			"releaseDate":      1791345433118,
			"commit":           "3e43260f771393d75c6504db99e751eb03dea8db",
			"os":               "Windows_NT(10.0.22631)",
			"osRelease":        "10.0.22631",
			"ideType":          "workbuddy",
			"ideName":          "WorkBuddy",
			"ideVersion":       "0.2.26",
			"pluginVersion":    "5.7.6",
			"env":              "production",
		},
		{
			"eventCode":        "chat_message_send",
			"timestamp":        nowMs + 20,
			"conversationId":   convID,
			"requestId":        reqID,
			"messageId":        msgID,
			"userId":           effectiveUID,
			"username":         effectiveEmail,
			"userNickname":     effectiveEmail,
			"role":             "user",
			"mode":             "craft",
			"agentName":        "cli",
			"agentType":        "main",
			"extName":          "workbuddy-desktop",
			"requestModelId":   DefaultWorkBuddyModel,
			"requestModelName": "Deepseek-V4.1-Flash",
			"timezone":         "Asia/Shanghai",
			"product":          "SaaS",
			"releaseDate":      1791345433118,
			"commit":           "3e43260f771393d75c6504db99e751eb03dea8db",
			"os":               "Windows_NT(10.0.22631)",
			"osRelease":        "10.0.22631",
			"ideType":          "workbuddy",
			"ideName":          "WorkBuddy",
			"ideVersion":       "0.2.26",
			"pluginVersion":    "5.7.6",
			"env":              "production",
		},
		{
			"eventCode":        "chat_message_response",
			"timestamp":        nowMs + 100,
			"conversationId":   convID,
			"requestId":        reqID,
			"messageId":        msgID,
			"userId":           effectiveUID,
			"username":         effectiveEmail,
			"userNickname":     effectiveEmail,
			"role":             "assistant",
			"mode":             "craft",
			"agentName":        "cli",
			"agentType":        "main",
			"extName":          "workbuddy-desktop",
			"requestModelId":   DefaultWorkBuddyModel,
			"requestModelName": "Deepseek-V4.1-Flash",
			"status":           "success",
			"timezone":         "Asia/Shanghai",
			"product":          "SaaS",
			"releaseDate":      1791345433118,
			"commit":           "3e43260f771393d75c6504db99e751eb03dea8db",
			"os":               "Windows_NT(10.0.22631)",
			"osRelease":        "10.0.22631",
			"ideType":          "workbuddy",
			"ideName":          "WorkBuddy",
			"ideVersion":       "0.2.26",
			"pluginVersion":    "5.7.6",
			"env":              "production",
		},
		{
			"eventCode":        "chat_request_response",
			"timestamp":        nowMs + 120,
			"conversationId":   convID,
			"requestId":        reqID,
			"messageId":        msgID,
			"userId":           effectiveUID,
			"username":         effectiveEmail,
			"userNickname":     effectiveEmail,
			"mode":             "craft",
			"agentName":        "cli",
			"agentType":        "main",
			"extName":          "workbuddy-desktop",
			"status":           "success",
			"requestModelId":   DefaultWorkBuddyModel,
			"requestModelName": "Deepseek-V4.1-Flash",
			"timezone":         "Asia/Shanghai",
			"product":          "SaaS",
			"releaseDate":      1791345433118,
			"commit":           "3e43260f771393d75c6504db99e751eb03dea8db",
			"os":               "Windows_NT(10.0.22631)",
			"osRelease":        "10.0.22631",
			"ideType":          "workbuddy",
			"ideName":          "WorkBuddy",
			"ideVersion":       "0.2.26",
			"pluginVersion":    "5.7.6",
			"env":              "production",
		},
	}
	reportBytes, _ := json.Marshal(reportEvents)
	reportHeaders := headers.Clone()
	reportHeaders.Set("Content-Type", "application/json; charset=UTF-8")
	reportHeaders.Set("X-User-Id", effectiveUID)
	reportHeaders.Set("X-Current-Account-Id", effectiveUID)
	_, _, _ = postWorkBuddyEndpointJSON(ctx, client, baseURL, "/v2/report", reportHeaders, reportBytes)

	// 5. 校验云端是否已下发 Bonus Pack（国际版通常为 T+1 次日凌晨异步到账）
	time.Sleep(500 * time.Millisecond)
	afterStatus, _ := fetchInternationalActivityStatus(ctx, client, baseURL, headers, acc)
	if afterStatus != nil && afterStatus.TodayCheckedIn {
		res.Success = true
		res.TodayCheckedIn = true
		res.AddedCredit = 30
		res.StreakDays = afterStatus.StreakDays
		res.Message = fmt.Sprintf("每日活跃奖励激活成功！获得 30 积分，已累计活跃 %d 天", afterStatus.StreakDays)
		return res, nil
	}

	streak := 0
	if status != nil && status.StreakDays > 0 {
		streak = status.StreakDays
	} else if afterStatus != nil && afterStatus.StreakDays > 0 {
		streak = afterStatus.StreakDays
	}

	res.Success = true
	res.TodayCheckedIn = false
	res.AddedCredit = 0
	res.StreakDays = streak
	res.Skipped = false
	res.Message = fmt.Sprintf("已成功上报今日官方 Craft 活跃与成长事件！活跃奖励（30 积分）将于次日凌晨 T+1 结算下发（当前累计活跃 %d 天）", streak)
	return res, nil
}

// CheckinWorkBuddyAccount 为指定账号执行单日安全签到并就地更新状态与落盘。
func (m *Manager) CheckinWorkBuddyAccount(accountID string, force bool, logger func(string)) (*WorkBuddyCheckinResult, error) {
	m.RLock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == accountID && a.Provider == workbuddyProvider {
			target = a
			break
		}
	}
	m.RUnlock()

	if target == nil {
		return nil, errors.New("未找到对应的 WorkBuddy 账号")
	}

	logMsg := func(msg string) {
		if logger != nil {
			logger(msg)
		}
	}

	today := time.Now().Format("2006-01-02")
	if !force && target.LastCheckinDate == today {
		return &WorkBuddyCheckinResult{
			Success:        true,
			AccountID:      target.ID,
			Email:          target.Email,
			TodayCheckedIn: true,
			StreakDays:     target.CheckinStreak,
			Message:        "今日已完成签到",
			Skipped:        true,
		}, nil
	}

	// 1. 先探测签到活动状态
	status, err := FetchWorkBuddyCheckinStatus(target)
	if err != nil {
		logMsg(fmt.Sprintf("⚠️ [WorkBuddy签到] 账号 %s 查询签到状态失败: %v", target.Email, err))
		// 如果状态查询失败但非 401，尝试直接请求领取
	} else {
		m.Lock()
		if status.StreakDays >= 0 {
			target.CheckinStreak = status.StreakDays
		}
		if status.TodayCheckedIn {
			target.LastCheckinDate = today
			m.Unlock()
			_ = m.SaveAccountsFor(true, workbuddyProvider)
			logMsg(fmt.Sprintf("ℹ️ [WorkBuddy签到] 账号 %s 今日活跃/签到已生效（累计 %d 天）", target.Email, status.StreakDays))
			return &WorkBuddyCheckinResult{
				Success:        true,
				AccountID:      target.ID,
				Email:          target.Email,
				TodayCheckedIn: true,
				StreakDays:     status.StreakDays,
				Message:        "今日已完成签到/活跃奖励已到账",
				Skipped:        true,
			}, nil
		}

		// 自愈清理：若云端今日未下发，但本地残留了历史或脏 LastCheckinDate，清空之
		if !target.IsWorkBuddyDomestic() && target.LastCheckinDate != "" {
			target.LastCheckinDate = ""
		}
		m.Unlock()

		if !status.Active {
			// 活动尚未开启或不在开放周期，记录为今日已探测避免频繁打接口
			m.Lock()
			target.LastCheckinDate = today
			m.Unlock()
			_ = m.SaveAccountsFor(true, workbuddyProvider)
			logMsg(fmt.Sprintf("ℹ️ [WorkBuddy签到] 账号 %s 暂无开放中的每日签到活动", target.Email))
			return &WorkBuddyCheckinResult{
				Success:        false,
				AccountID:      target.ID,
				Email:          target.Email,
				TodayCheckedIn: false,
				Message:        "当前暂无开放中的签到活动",
				Skipped:        true,
			}, nil
		}
	}

	// 2. 执行签到发包 / 活跃上报
	res, err := ClaimWorkBuddyDailyCheckin(target)
	if err != nil {
		logMsg(fmt.Sprintf("❌ [WorkBuddy签到] 账号 %s 签到执行失败: %v", target.Email, err))
		return nil, err
	}

	m.Lock()
	if res.TodayCheckedIn {
		target.LastCheckinDate = today
		if res.StreakDays > 0 {
			target.CheckinStreak = res.StreakDays
		}
	} else if !target.IsWorkBuddyDomestic() {
		// 国际版次日异步到账：清理本地脏标记，准确记录云端累计天数
		target.LastCheckinDate = ""
		target.CheckinStreak = res.StreakDays
	}
	m.Unlock()

	_ = m.SaveAccountsFor(true, workbuddyProvider)

	if res.Success {
		if res.TodayCheckedIn {
			logMsg(fmt.Sprintf("🎉 [WorkBuddy签到] 账号 %s 签到成功！获得 %d 积分，已连续签到 %d 天", target.Email, res.AddedCredit, res.StreakDays))
		} else {
			logMsg(fmt.Sprintf("🚀 [WorkBuddy活跃] 账号 %s: %s", target.Email, res.Message))
		}
		// 签到/激活成功后触发配额刷新拉取最新积分
		if m.FetchQuota != nil {
			go func(acc *Account) {
				time.Sleep(500 * time.Millisecond) // 等待服务端额度落库
				_, _ = m.FetchQuota(acc)
				if m.OnAccountsUpdated != nil {
					m.RLock()
					accs := m.accounts
					m.RUnlock()
					m.OnAccountsUpdated(accs)
				}
			}(target)
		}
	} else if res.Skipped {
		logMsg(fmt.Sprintf("ℹ️ [WorkBuddy签到] 账号 %s 签到结果: %s", target.Email, res.Message))
	} else {
		logMsg(fmt.Sprintf("⚠️ [WorkBuddy签到] 账号 %s: %s", target.Email, res.Message))
	}

	return res, nil
}

// RunDailyCheckinForWorkBuddyPool 为 WorkBuddy 号池中全部启用的账号执行一轮签到。
func (m *Manager) RunDailyCheckinForWorkBuddyPool(force bool, logger func(string)) []*WorkBuddyCheckinResult {
	m.RLock()
	var targets []*Account
	for _, a := range m.accounts {
		if a.Provider == workbuddyProvider && a.Enabled && a.AccessToken != "" {
			targets = append(targets, a)
		}
	}
	m.RUnlock()

	var results []*WorkBuddyCheckinResult
	for _, acc := range targets {
		res, err := m.CheckinWorkBuddyAccount(acc.ID, force, logger)
		if err != nil {
			results = append(results, &WorkBuddyCheckinResult{
				Success:   false,
				AccountID: acc.ID,
				Email:     acc.Email,
				Message:   err.Error(),
			})
		} else if res != nil {
			results = append(results, res)
		}
		// 间隔 300ms，保持礼貌调度
		time.Sleep(300 * time.Millisecond)
	}

	return results
}
