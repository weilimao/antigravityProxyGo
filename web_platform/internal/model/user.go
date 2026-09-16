package model

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"size:128;index" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Role         string    `gorm:"size:32;default:'user';not null" json:"role"` // "user" or "admin"
	Status       string    `gorm:"size:32;default:'active';not null" json:"status"`
	PlanID       *uint     `gorm:"index" json:"planId,omitempty"`
	PlanExpireAt int64     `gorm:"default:0" json:"planExpireAt"` // Unix timestamp in seconds, 0 = no active subscription
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`

	// 关联
	Plan *Plan `gorm:"foreignKey:PlanID" json:"plan,omitempty"`
}

func (u *User) IsAdmin() bool {
	return u.Role == "admin"
}

func (u *User) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	return nil
}

func (u *User) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

func (u *User) IsSubscriptionActive() bool {
	if u.IsAdmin() {
		return true
	}
	if u.PlanID == nil || *u.PlanID == 0 {
		return false
	}
	// 永久有效或未过期
	if u.PlanExpireAt == 0 || u.PlanExpireAt > time.Now().Unix() {
		return true
	}
	return false
}
