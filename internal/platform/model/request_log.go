package model

import "time"

// RequestLog MySQL 模式轻量请求日志模型
// 架构约束：坚决不存储请求体 (RequestBody) 与响应体 (ResponseBody)，仅存储轻量标量指标，防止数据库磁盘膨胀。
type RequestLog struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ReqID        string    `gorm:"size:64;index;not null" json:"reqId"`
	UserID       string    `gorm:"size:64;index;not null" json:"userId"`
	Account      string    `gorm:"size:128;index" json:"account"`
	ModelName    string    `gorm:"size:128;index;not null" json:"model"`
	InTokens     int       `gorm:"default:0" json:"inTokens"`
	OutTokens    int       `gorm:"default:0" json:"outTokens"`
	CachedTokens int       `gorm:"default:0" json:"cachedTokens"`
	Cost         float64   `gorm:"type:decimal(10,6);default:0" json:"cost"`
	InputCost    float64   `gorm:"type:decimal(10,6);default:0" json:"inputCost"`
	OutputCost   float64   `gorm:"type:decimal(10,6);default:0" json:"outputCost"`
	CachedCost   float64   `gorm:"type:decimal(10,6);default:0" json:"cachedCost"`
	DurationMs   int64     `gorm:"default:0" json:"durationMs"`
	FirstByteMs  int64     `gorm:"default:0" json:"firstByteMs"`
	StatusCode   int       `gorm:"default:200" json:"statusCode"`
	Method       string    `gorm:"size:16" json:"method"`
	Host         string    `gorm:"size:128" json:"host"`
	Path         string    `gorm:"size:255" json:"path"`
	SessionID    string    `gorm:"size:64;index" json:"sessionId"`
	Family       string    `gorm:"size:32" json:"family"`
	CacheStatus  string    `gorm:"size:16" json:"cacheStatus"`
	CreatedAt    time.Time `gorm:"index" json:"createdAt"`
}

func (RequestLog) TableName() string {
	return "request_logs"
}
