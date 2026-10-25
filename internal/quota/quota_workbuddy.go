package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

func formatPoints(val float64) string {
	if val == float64(int64(val)) {
		return strconv.FormatInt(int64(val), 10)
	}
	return fmt.Sprintf("%.2f", val)
}

// doFetchWorkBuddyQuota 执行单次对 WorkBuddy 官方配额端点 /billing/meter/get-user-resource-summary 的探测。
// 返回 (QuotaResult, httpStatusCode, error)。
func doFetchWorkBuddyQuota(acc *account.Account) (*account.QuotaResult, int, error) {
	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
		if acc.IsWorkBuddyDomestic() {
			baseURL = account.DefaultWorkBuddyDomesticBaseURL
		} else {
			baseURL = account.DefaultWorkBuddyBaseURL
		}
	}

	token := strings.TrimSpace(acc.AccessToken)
	if token == "" {
		return nil, 0, errors.New("账号 AccessToken 为空")
	}

	summaryURL := strings.TrimRight(baseURL, "/") + "/billing/meter/get-user-resource-summary"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, summaryURL, strings.NewReader("{}"))
	if err != nil {
		return nil, 0, fmt.Errorf("创建配额探测请求失败: %w", err)
	}

	headers := account.BuildWorkBuddyHeaders(acc)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second, Transport: netutil.NewTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("配额请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		bodyStr := string(b)
		// 腾讯云 CAM 计量资源包未开通/无权限或端点异常（如 20017 / system policy not verify 500 错误）
		// 自动标记免配额探测，返回空桶，绝不进冷静期，保持账号正常活跃推理。
		acc.NoQuota = true
		fmt.Printf("[WorkBuddyQuota] 账号 %s 上游配额接口返回异常 (HTTP %d: %s)，已自动标记免配额探测，不再请求配额积分\n", acc.Email, resp.StatusCode, bodyStr)
		return &account.QuotaResult{
			Tier:    "Free",
			Buckets: []account.QuotaBucket{},
		}, resp.StatusCode, nil
	}

	var respData struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Packages []struct {
				PackageCode         string `json:"PackageCode"`
				CycleTotalCapacity  string `json:"CycleTotalCapacity"`
				CycleRemainCapacity string `json:"CycleRemainCapacity"`
				CycleUsedCapacity   string `json:"CycleUsedCapacity"`
				CapacityUnit        string `json:"CapacityUnit"`
			} `json:"Packages"`
			IsPaidUser bool `json:"IsPaidUser"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("解析配额响应 JSON 失败: %w", err)
	}

	if respData.Code != 0 {
		// 业务错误码（如 20017 CAM 错误等）：自动标记免配额探测，不再请求配额积分
		acc.NoQuota = true
		fmt.Printf("[WorkBuddyQuota] 账号 %s 上游配额服务返回错误 (code=%d, msg=%s)，已自动标记免配额探测，不再请求配额积分\n", acc.Email, respData.Code, respData.Msg)
		return &account.QuotaResult{
			Tier:    "Free",
			Buckets: []account.QuotaBucket{},
		}, resp.StatusCode, nil
	}

	totalRemain := float64(0)
	totalCapacity := float64(0)
	bonusRemain := float64(0)

	for _, pkg := range respData.Data.Packages {
		remainVal, _ := strconv.ParseFloat(strings.TrimSpace(pkg.CycleRemainCapacity), 64)
		totalVal, _ := strconv.ParseFloat(strings.TrimSpace(pkg.CycleTotalCapacity), 64)

		if remainVal > 0 {
			totalRemain += remainVal
			if pkg.PackageCode == account.WorkBuddyPackageBonusPack {
				bonusRemain += remainVal
			}
		}
		if totalVal > 0 {
			totalCapacity += totalVal
		}
	}

	// 探测成功，自愈可能存在的历史误标记免配额与失效状态
	acc.NoQuota = false
	acc.SetAuthExpired(false)

	tier := "Free"
	if respData.Data.IsPaidUser {
		tier = "Pro"
	}

	// 组装呈现文案：若存在活跃奖励包，则细化展示基础与活跃奖励分布
	var modelID string
	if bonusRemain > 0 {
		baseRemain := totalRemain - bonusRemain
		if baseRemain < 0 {
			baseRemain = 0
		}
		modelID = fmt.Sprintf("积分余额: %s (基础 %s + 活跃奖励 %s)", formatPoints(totalRemain), formatPoints(baseRemain), formatPoints(bonusRemain))
	} else {
		modelID = fmt.Sprintf("积分余额: %s", formatPoints(totalRemain))
	}

	remainPercent := 100
	remainFraction := 1.0
	if totalCapacity > 0 {
		remainFraction = totalRemain / totalCapacity
		remainPercent = int(remainFraction * 100)
		if remainPercent > 100 {
			remainPercent = 100
		}
	}

	credits := totalRemain
	return &account.QuotaResult{
		Tier: tier,
		Buckets: []account.QuotaBucket{
			{
				Group:             "WorkBuddy 官方账号",
				ModelID:           modelID,
				RemainingFraction: remainFraction,
				RemainPercent:     remainPercent,
			},
		},
		Credits: &credits,
	}, resp.StatusCode, nil
}

// fetchWorkBuddyQuota 向 WorkBuddy 官方套餐端点 /billing/meter/get-user-resource-summary 发送请求，
// 实时拉取并聚合各资源包的剩余积分（CycleRemainCapacity），返回真实积分余额、细分结构与套餐版本。
// 当探测到凭证失效（401/403）时，若配置有 RefreshToken，会自动尝试换取新 Token 并自愈重试；仅在换取失败或无 RefreshToken 时报错提示用户重新登录。
func fetchWorkBuddyQuota(acc *account.Account, refreshCallback func(*account.Account) (string, error)) (*account.QuotaResult, error) {
	res, statusCode, err := doFetchWorkBuddyQuota(acc)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		rfToken := strings.TrimSpace(acc.GetRefreshToken())
		if rfToken != "" {
			fmt.Printf("[WorkBuddyQuota] 账号 %s 凭证过期 (HTTP %d)，尝试使用 RefreshToken 自动换取新 Token...\n", acc.Email, statusCode)
			var refErr error
			if refreshCallback != nil {
				_, refErr = refreshCallback(acc)
			} else {
				_, refErr = account.RefreshWorkBuddyToken(acc)
			}
			if refErr == nil {
				// 换取新 Token 成功，自动重试配额探测
				retryRes, retryStatus, retryErr := doFetchWorkBuddyQuota(acc)
				if retryErr != nil {
					return nil, retryErr
				}
				if retryStatus == http.StatusOK {
					fmt.Printf("[WorkBuddyQuota] 账号 %s 自动换取 Token 成功且配额重试探测通过\n", acc.Email)
					acc.SetAuthExpired(false)
					return retryRes, nil
				}
				if retryStatus == http.StatusUnauthorized || retryStatus == http.StatusForbidden {
					acc.SetAuthExpired(true)
					return nil, fmt.Errorf("凭证已失效（HTTP %d），且自动换取 Token 后仍无权限，需要重新登录导入", retryStatus)
				}
				return retryRes, nil
			}
			acc.SetAuthExpired(true)
			return nil, fmt.Errorf("凭证已失效（HTTP %d），且自动换取 Token 失败，需要重新登录导入: %w", statusCode, refErr)
		}
		acc.SetAuthExpired(true)
		return nil, fmt.Errorf("凭证已失效（HTTP %d），需要重新登录导入", statusCode)
	}
	return res, nil
}
