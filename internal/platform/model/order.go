package model

import (
	"time"
)

type Order struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	OrderNo      string     `gorm:"size:64;uniqueIndex;not null" json:"orderNo"` // 本地 A 站订单号
	UserID       uint       `gorm:"index;not null" json:"userId"`
	PlanID       uint       `gorm:"index;not null" json:"planId"`
	AmountCents         int64      `gorm:"not null" json:"amountCents"`
	OriginalAmountCents int64      `gorm:"default:0" json:"originalAmountCents"`
	DiscountCents       int64      `gorm:"default:0" json:"discountCents"`
	UpgradeFromPlanID   *uint      `gorm:"index" json:"upgradeFromPlanId,omitempty"`
	Status              string     `gorm:"size:32;default:'pending';index;not null" json:"status"` // pending / paid / cancelled / expired
	RelayOrderNo        string     `gorm:"size:64;index" json:"relayOrderNo"`                      // 极客工坊 B 站订单号
	PayURL              string     `gorm:"size:512" json:"payUrl"`                                 // 收银台跳转 URL
	PaidAt              *time.Time `json:"paidAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`

	// 关联
	User              *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Plan              *Plan `gorm:"foreignKey:PlanID" json:"plan,omitempty"`
	UpgradeFromPlan   *Plan `gorm:"foreignKey:UpgradeFromPlanID" json:"upgradeFromPlan,omitempty"`
}
