package quota

import (
	"encoding/json"
	"math"
	"strings"

	"antigravity-proxy/internal/account"
)

// ModelQuotaDetail 代表 retrieveUserQuota 返回的单模型配额信息
type ModelQuotaDetail struct {
	ModelID           string  `json:"modelId"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
	TokenType         string  `json:"tokenType"`
}

// RetrieveUserQuotaDetailResponse 代表 retrieveUserQuota 接口的返回结构
type RetrieveUserQuotaDetailResponse struct {
	Buckets []ModelQuotaDetail `json:"buckets"`
}

// ParseUserQuotaSummary 解析 retrieveUserQuotaSummary 响应
func ParseUserQuotaSummary(resBytes []byte) []account.QuotaBucket {
	var summaryResp RetrieveUserQuotaSummaryResponse
	if err := json.Unmarshal(resBytes, &summaryResp); err != nil || len(summaryResp.Groups) == 0 {
		return nil
	}

	var buckets []account.QuotaBucket
	for _, g := range summaryResp.Groups {
		for _, b := range g.Buckets {
			name := b.DisplayName
			if name == "" {
				name = b.BucketID
			}
			remFraction := b.RemainingFraction
			buckets = append(buckets, account.QuotaBucket{
				ModelID:           name,
				Group:             g.DisplayName,
				RemainingFraction: remFraction,
				RemainPercent:     int(math.Round(remFraction * 100)),
				ResetTime:         b.ResetTime,
			})
		}
	}
	return buckets
}

// HasGeminiGroup 检查配额桶列表中是否已包含 Gemini Models 分组
func HasGeminiGroup(buckets []account.QuotaBucket) bool {
	for _, b := range buckets {
		if strings.Contains(strings.ToLower(b.Group), "gemini") {
			return true
		}
	}
	return false
}

// ExtractModelQuotasFromDetail 从 retrieveUserQuota 详情中提取 Gemini 与 Claude/GPT 的配额概况
func ExtractModelQuotasFromDetail(resBytes []byte) (geminiBucket *account.QuotaBucket, claudeBucket *account.QuotaBucket) {
	if len(resBytes) == 0 {
		return nil, nil
	}
	var detailResp RetrieveUserQuotaDetailResponse
	if err := json.Unmarshal(resBytes, &detailResp); err != nil || len(detailResp.Buckets) == 0 {
		return nil, nil
	}

	for _, b := range detailResp.Buckets {
		lowerModel := strings.ToLower(b.ModelID)
		if geminiBucket == nil && strings.Contains(lowerModel, "gemini") {
			geminiBucket = &account.QuotaBucket{
				ModelID:           "Five Hour Limit Remaining",
				Group:             "Gemini Models",
				RemainingFraction: b.RemainingFraction,
				RemainPercent:     int(math.Round(b.RemainingFraction * 100)),
				ResetTime:         b.ResetTime,
			}
		}
		if claudeBucket == nil && (strings.Contains(lowerModel, "claude") || strings.HasPrefix(lowerModel, "gpt")) {
			claudeBucket = &account.QuotaBucket{
				ModelID:           "Five Hour Limit Remaining",
				Group:             "Claude and GPT models",
				RemainingFraction: b.RemainingFraction,
				RemainPercent:     int(math.Round(b.RemainingFraction * 100)),
				ResetTime:         b.ResetTime,
			}
		}
		if geminiBucket != nil && claudeBucket != nil {
			break
		}
	}
	return geminiBucket, claudeBucket
}

// MergeQuotaBuckets 合并 Summary 和 Detail 配额数据，确保 Gemini Models 和 Claude and GPT models 均完整
func MergeQuotaBuckets(summaryBuckets []account.QuotaBucket, detailBytes []byte) []account.QuotaBucket {
	var out []account.QuotaBucket

	geminiBucket, claudeBucket := ExtractModelQuotasFromDetail(detailBytes)

	// 1. 如果已有或补齐 Gemini Models，Gemini 优先置顶显示
	if HasGeminiGroup(summaryBuckets) {
		for _, b := range summaryBuckets {
			if strings.Contains(strings.ToLower(b.Group), "gemini") {
				out = append(out, b)
			}
		}
	} else if geminiBucket != nil {
		out = append(out,
			account.QuotaBucket{
				ModelID:           "Weekly Limit Remaining",
				Group:             "Gemini Models",
				RemainingFraction: 1.0,
				RemainPercent:     100,
				ResetTime:         "",
			},
			*geminiBucket,
		)
	}

	// 2. 追加 Claude and GPT models 或其他分组
	hasClaude := false
	for _, b := range summaryBuckets {
		if !strings.Contains(strings.ToLower(b.Group), "gemini") {
			out = append(out, b)
			if strings.Contains(strings.ToLower(b.Group), "claude") {
				hasClaude = true
			}
		}
	}

	// 若 Summary 完全为空且有 claudeBucket，兜底组装 Claude 组
	if !hasClaude && claudeBucket != nil {
		out = append(out,
			account.QuotaBucket{
				ModelID:           "Weekly Limit Remaining",
				Group:             "Claude and GPT models",
				RemainingFraction: 1.0,
				RemainPercent:     100,
				ResetTime:         "",
			},
			*claudeBucket,
		)
	}

	return out
}
