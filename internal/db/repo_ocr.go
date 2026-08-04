package db

import (
	"database/sql"
	"time"
)

// SaveOcrCache 将 OCR 结果持久化写入 SQLite 数据库 (带 24h 过期时间)
func SaveOcrCache(cacheKey, ocrText string, expiresAt time.Time) error {
	if GlobalDB == nil || cacheKey == "" {
		return nil
	}

	query := `INSERT INTO ocr_cache (cache_key, ocr_text, expires_at)
		VALUES (?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			ocr_text = excluded.ocr_text,
			expires_at = excluded.expires_at;`

	_, err := GlobalDB.Exec(query, cacheKey, ocrText, expiresAt)
	return err
}

// GetOcrCache 从 SQLite 数据库读取未过期的 OCR 记录
func GetOcrCache(cacheKey string) (string, bool) {
	if GlobalDB == nil || cacheKey == "" {
		return "", false
	}

	var ocrText string
	var expiresAt time.Time
	query := `SELECT ocr_text, expires_at FROM ocr_cache WHERE cache_key = ?;`

	err := GlobalDB.QueryRow(query, cacheKey).Scan(&ocrText, &expiresAt)
	if err != nil {
		if err != sql.ErrNoRows {
			_ = err
		}
		return "", false
	}

	// 如果已过期，主动淘汰并返回 false
	if time.Now().After(expiresAt) {
		_, _ = GlobalDB.Exec(`DELETE FROM ocr_cache WHERE cache_key = ?;`, cacheKey)
		return "", false
	}

	return ocrText, true
}

// CleanExpiredOcrCache 清理数据库中过期的 OCR 缓存记录
func CleanExpiredOcrCache() error {
	if GlobalDB == nil {
		return nil
	}
	_, err := GlobalDB.Exec(`DELETE FROM ocr_cache WHERE expires_at <= ?;`, time.Now())
	return err
}
