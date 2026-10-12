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

// quota_workbuddy.go: WorkBuddy 官方套餐与资源包配额探测逻辑。
//
// 官方协议对齐:
//   - 端点: POST {BaseURL}/billing/meter/get-user-resource-summary
//   - 认证: Authorization: Bearer {token} + WorkBuddy 桌面端指纹请求头
//   - 资源包识别:
//       * TCACA_code_007_nzdH5h4Nl0: 活跃奖励包 (Bonus Pack / 30 积分/天)
//       * TCACA_code_035_ArVxJcGDsm: 国际版免费套餐月度基础包 (100 积分)
//   - 优雅降级: 若遇到 CAM 策略未授权 (如国内邮箱账号在海外集群无资源包，返回 500 / 20017)，
//     自动标记 NoQuota 并返回空桶，杜绝冷启动报错与误进冷静期。

// formatPoints formats a float64 point number cleanly as an integer or two-decimal string.
func formatPoints(val float64) string {
	if val == float64(int64(val)) {
		return strconv.FormatInt(int64(val), 10)
	}
	return fmt.Sprintf("%.2f", val)
}

// fetchWorkBuddyQuota 向 WorkBuddy 官方套餐端点 /billing/meter/get-user-resource-summary 发送请求，
// 实时拉取并聚合各资源包的剩余积分（CycleRemainCapacity），返回真实积分余额、细分结构与套餐版本。
func fetchWorkBuddyQuota(acc *account.Account) (*account.QuotaResult, error) {
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
		return nil, errors.New("账号 AccessToken 为空")
	}

	summaryURL := strings.TrimRight(baseURL, "/") + "/billing/meter/get-user-resource-summary"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, summaryURL, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("创建配额探测请求失败: %w", err)
	}

	headers := account.BuildWorkBuddyHeaders(acc)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second, Transport: netutil.NewTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("配额请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("凭证已失效 (HTTP %d)，请重新授权或导入", resp.StatusCode)
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
		}, nil
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
		return nil, fmt.Errorf("解析配额响应 JSON 失败: %w", err)
	}

	if respData.Code != 0 {
		// 业务错误码（如 20017 CAM 错误等）：自动标记免配额探测，不再请求配额积分
		acc.NoQuota = true
		fmt.Printf("[WorkBuddyQuota] 账号 %s 上游配额服务返回错误 (code=%d, msg=%s)，已自动标记免配额探测，不再请求配额积分\n", acc.Email, respData.Code, respData.Msg)
		return &account.QuotaResult{
			Tier:    "Free",
			Buckets: []account.QuotaBucket{},
		}, nil
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

	// 探测成功，自愈可能存在的历史误标记免配额状态
	acc.NoQuota = false

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
	}, nil
}
