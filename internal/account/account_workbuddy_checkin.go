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
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+strings.TrimSpace(acc.AccessToken))
	if strings.TrimSpace(acc.ProjectID) != "" {
		headers.Set("X-User-Id", strings.TrimSpace(acc.ProjectID))
	}
	headers.Set("User-Agent", "WorkBuddy/5.5.2")
	headers.Set("X-IDE-Type", "WorkBuddy")
	headers.Set("X-IDE-Name", "WorkBuddy")
	headers.Set("X-IDE-Version", "5.5.2")
	headers.Set("X-Product", "WorkBuddy")
	headers.Set("Accept-Language", "zh")
	return headers
}

func postWorkBuddyEndpoint(ctx context.Context, client *http.Client, baseURL, path string, headers http.Header) (int, []byte, error) {
	fullURL := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader([]byte("{}")))
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

// FetchWorkBuddyCheckinStatus 探测指定 WorkBuddy 账号当前的每日签到活动与今日签到状态。
func FetchWorkBuddyCheckinStatus(acc *Account, customClient ...*http.Client) (*WorkBuddyCheckinStatus, error) {
	if acc == nil || acc.Provider != workbuddyProvider {
		return nil, errors.New("账号无效或非 WorkBuddy 类型")
	}
	if strings.TrimSpace(acc.AccessToken) == "" {
		return nil, errors.New("账号 AccessToken 为空")
	}

	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" {
		baseURL = DefaultWorkBuddyBaseURL
	}

	client := &http.Client{Timeout: defaultCheckinTimeout}
	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	}

	headers := buildWorkBuddyCheckinHeaders(acc)
	ctx := context.Background()

	// 优先请求 /v2/billing/meter/checkin-activity-status，404 时优雅降级 /billing/meter/checkin-activity-status
	statusCode, body, err := postWorkBuddyEndpoint(ctx, client, baseURL, "/v2/billing/meter/checkin-activity-status", headers)
	if err != nil {
		return nil, fmt.Errorf("签到状态接口请求失败: %w", err)
	}
	if statusCode == http.StatusNotFound {
		statusCode, body, err = postWorkBuddyEndpoint(ctx, client, baseURL, "/billing/meter/checkin-activity-status", headers)
		if err != nil {
			return nil, fmt.Errorf("降级请求签到状态失败: %w", err)
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

// ClaimWorkBuddyDailyCheckin 对指定 WorkBuddy 账号执行签到发包。
func ClaimWorkBuddyDailyCheckin(acc *Account, customClient ...*http.Client) (*WorkBuddyCheckinResult, error) {
	if acc == nil || acc.Provider != workbuddyProvider {
		return nil, errors.New("账号无效或非 WorkBuddy 类型")
	}
	if strings.TrimSpace(acc.AccessToken) == "" {
		return nil, errors.New("账号 AccessToken 为空")
	}

	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" {
		baseURL = DefaultWorkBuddyBaseURL
	}

	client := &http.Client{Timeout: defaultCheckinTimeout}
	if len(customClient) > 0 && customClient[0] != nil {
		client = customClient[0]
	}

	headers := buildWorkBuddyCheckinHeaders(acc)
	ctx := context.Background()

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

	// 业务响应判定
	if respData.Code == 0 {
		res.Success = true
		res.TodayCheckedIn = true
		res.AddedCredit = respData.Data.Credit
		res.StreakDays = respData.Data.StreakDays
		res.Message = fmt.Sprintf("签到成功，获得 %d 积分，连续签到 %d 天", respData.Data.Credit, respData.Data.StreakDays)
		return res, nil
	}

	// 业务错误（例如 10001 活动未开启，或已经签到过）
	res.Success = false
	res.Message = respData.Msg
	if respData.Code == 10001 || strings.Contains(respData.Msg, "未开启") || strings.Contains(respData.Msg, "已过期") {
		res.Skipped = true
	}
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
		if status.TodayCheckedIn {
			m.Lock()
			target.LastCheckinDate = today
			if status.StreakDays > 0 {
				target.CheckinStreak = status.StreakDays
			}
			m.Unlock()
			_ = m.SaveAccountsFor(true, workbuddyProvider)
			logMsg(fmt.Sprintf("ℹ️ [WorkBuddy签到] 账号 %s 今日已在其它端签到（连续 %d 天）", target.Email, status.StreakDays))
			return &WorkBuddyCheckinResult{
				Success:        true,
				AccountID:      target.ID,
				Email:          target.Email,
				TodayCheckedIn: true,
				StreakDays:     status.StreakDays,
				Message:        "今日已完成签到",
				Skipped:        true,
			}, nil
		}

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

	// 2. 执行签到发包
	res, err := ClaimWorkBuddyDailyCheckin(target)
	if err != nil {
		logMsg(fmt.Sprintf("❌ [WorkBuddy签到] 账号 %s 签到执行失败: %v", target.Email, err))
		return nil, err
	}

	m.Lock()
	target.LastCheckinDate = today
	if res.StreakDays > 0 {
		target.CheckinStreak = res.StreakDays
	}
	m.Unlock()

	_ = m.SaveAccountsFor(true, workbuddyProvider)

	if res.Success {
		logMsg(fmt.Sprintf("🎉 [WorkBuddy签到] 账号 %s 签到成功！获得 %d 积分，已连续签到 %d 天", target.Email, res.AddedCredit, res.StreakDays))
		// 签到成功后触发配额刷新拉取最新积分
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
