package model

import (
	"time"
)

type APIKey struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Key           string    `gorm:"size:64;uniqueIndex;not null" json:"key"` // sk-ant-xxx
	UserID        uint      `gorm:"index;not null" json:"userId"`
	Name          string    `gorm:"size:64;not null" json:"name"`
	AllowedModels []string  `gorm:"serializer:json" json:"allowedModels"` // 继承或用户自定义模型子集
	RateLimit     int       `gorm:"default:30" json:"rateLimit"`
	LimitTokens   int64     `gorm:"default:0" json:"limitTokens"` // 0 = 跟随套餐总配额
	UsedTokens    int64     `gorm:"default:0" json:"usedTokens"`
	Status        string    `gorm:"size:32;default:'active';not null" json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`

	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}
