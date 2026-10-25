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

// HasGeminiGroup 检查配额桶列表中是否已包含常规 Gemini Models 分组（排除 Gemini 4 Argon 独立分组）
func HasGeminiGroup(buckets []account.QuotaBucket) bool {
	for _, b := range buckets {
		lower := strings.ToLower(b.Group)
		if strings.Contains(lower, "gemini") && !strings.Contains(lower, "argon") {
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
		if geminiBucket == nil && strings.Contains(lowerModel, "gemini") && !strings.Contains(lowerModel, "argon") {
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

// MergeQuotaBuckets 合并 Summary 和 Detail 配额数据，确保 Gemini Models、Claude and GPT models 及 Gemini 4 Argon 顺序规范完整
func MergeQuotaBuckets(summaryBuckets []account.QuotaBucket, detailBytes []byte) []account.QuotaBucket {
	var out []account.QuotaBucket

	geminiBucket, claudeBucket := ExtractModelQuotasFromDetail(detailBytes)

	// 1. 常规 Gemini Models 优先置顶显示
	hasGemini := false
	for _, b := range summaryBuckets {
		lower := strings.ToLower(b.Group)
		if strings.Contains(lower, "gemini") && !strings.Contains(lower, "argon") {
			out = append(out, b)
			hasGemini = true
		}
	}
	if !hasGemini && geminiBucket != nil {
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

	// 2. 追加 Claude and GPT models 分组
	hasClaude := false
	for _, b := range summaryBuckets {
		lower := strings.ToLower(b.Group)
		if strings.Contains(lower, "claude") || strings.Contains(lower, "gpt") {
			out = append(out, b)
			hasClaude = true
		}
	}
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

	// 3. 追加 Gemini 4 Argon 独立分组
	for _, b := range summaryBuckets {
		lower := strings.ToLower(b.Group)
		if strings.Contains(lower, "argon") {
			out = append(out, b)
		}
	}

	// 4. 追加其他未涵盖的分组（若未来上游推出新分组）
	for _, b := range summaryBuckets {
		lower := strings.ToLower(b.Group)
		isStandardGemini := strings.Contains(lower, "gemini") && !strings.Contains(lower, "argon")
		isClaude := strings.Contains(lower, "claude") || strings.Contains(lower, "gpt")
		isArgon := strings.Contains(lower, "argon")
		if !isStandardGemini && !isClaude && !isArgon {
			out = append(out, b)
		}
	}

	return out
}
