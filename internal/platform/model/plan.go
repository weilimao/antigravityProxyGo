package model

import (
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
	RateLimit int         `json:"rateLimit"` // 每分钟请求频次
}

type Plan struct {
	ID            uint        `gorm:"primaryKey" json:"id"`
	Name          string      `gorm:"size:64;not null" json:"name"`
	Description   string      `gorm:"size:255" json:"description"`
	PriceCents    int64       `gorm:"not null" json:"priceCents"` // 金额（分），例如 3900 = 39.00 元
	DurationDays  int         `gorm:"default:30;not null" json:"durationDays"` // 有效天数，0 表示永久
	AllowedModels []string    `gorm:"serializer:json" json:"allowedModels"` // 授权调用的模型列表
	Quotas        PlanQuotas  `gorm:"serializer:json" json:"quotas"`        // 模型配额
	TokenLimit    int64       `gorm:"default:0;not null" json:"tokenLimit"` // Token 配额限制，0 表示不限制
	RateLimit     int         `gorm:"default:30;not null" json:"rateLimit"` // RPM 速率
	SortOrder     int         `gorm:"default:0" json:"sortOrder"`
	Status        string      `gorm:"size:32;default:'active';not null" json:"status"` // active or inactive
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}
