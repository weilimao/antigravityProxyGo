package model

import (
	"strings"
	"time"
)

type QuotaBucket struct {
	EnableFixed  bool    `json:"enableFixed"`
	FixedTokens  int64   `json:"fixedTokens"`
	EnableHourly bool    `json:"enableHourly"`
	HourlyHours  float64 `json:"hourlyHours"`
	HourlyTokens int64   `json:"hourlyTokens"`
	EnableDaily  bool    `json:"enableDaily"`
	DailyDays    float64 `json:"dailyDays"`
	DailyTokens  int64   `json:"dailyTokens"`
}

type PlanQuotas struct {
	Gemini    QuotaBucket `json:"gemini"`
	Claude    QuotaBucket `json:"claude"`
	Nvidia    QuotaBucket `json:"nvidia"`
	Grok      QuotaBucket `json:"grok"`
	Workbuddy QuotaBucket `json:"workbuddy"`
	RateLimit int         `json:"rateLimit"` // 每分钟请求频次
}

type Plan struct {
	ID            uint        `gorm:"primaryKey" json:"id"`
	Name          string      `gorm:"size:64;not null" json:"name"`
	Type          string      `gorm:"size:32;default:'subscription';not null" json:"type"` // subscription (订阅型) 或 addon (增值型/Token加油包)
	Tier          string      `gorm:"size:32;default:'pro';not null" json:"tier"`          // 订阅等级: pro, max, max+, max++
	Description   string      `gorm:"size:255" json:"description"`
	PriceCents    int64       `gorm:"not null" json:"priceCents"`                          // 金额（分），例如 3900 = 39.00 元
	DurationDays  int         `gorm:"not null" json:"durationDays"`                        // 有效天数，0 表示永久
	AllowedModels []string    `gorm:"serializer:json" json:"allowedModels"`                // 授权调用的模型列表
	AutoModels    []string    `gorm:"serializer:json" json:"autoModels"`                   // auto 智能路由实际包含的说明模型列表
	Quotas        PlanQuotas  `gorm:"serializer:json" json:"quotas"`                       // 模型配额
	TokenLimit    int64       `gorm:"default:0;not null" json:"tokenLimit"`                // Token 配额限制，0 表示不限制
	RateLimit     int         `gorm:"default:30;not null" json:"rateLimit"`                // RPM 速率
	SortOrder     int         `gorm:"default:0" json:"sortOrder"`
	Status        string      `gorm:"size:32;default:'active';not null" json:"status"`     // active or inactive
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}

// GetTierWeight 返回订阅等级权重 (Pro: 1, MAX: 2, MAX+: 3, MAX++: 4)
func GetTierWeight(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "max++":
		return 4
	case "max+":
		return 3
	case "max":
		return 2
	case "pro":
		return 1
	default:
		return 1 // 默认为 Pro
	}
}

// NormalizeTier 格式化等级标准展示名
func NormalizeTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "max++":
		return "MAX++"
	case "max+":
		return "MAX+"
	case "max":
		return "MAX"
	case "pro":
		return "Pro"
	default:
		return "Pro"
	}
}
