package relay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// SetDB 为 UserManager 注入 GORM 数据库实例，并自动切换为 DB 存储模式
func (m *UserManager) SetDB(db *gorm.DB) {
	m.Lock()
	m.gormDB = db
	if m.usageStore != nil {
		_ = m.usageStore.Close()
	}
	m.usageStore = NewUserUsageStore(db, func() {
		m.SaveToDisk()
	})
	m.Unlock()

	// 注入 DB 后，自动同步数据库中的用户与 API Key 至内存缓存
	m.SyncFromDB()
}

// Close 关闭 UserManager 及其绑定的用量存储引擎
func (m *UserManager) Close() error {
	m.Lock()
	defer m.Unlock()
	if m.usageStore != nil {
		return m.usageStore.Close()
	}
	return nil
}

// SyncFromDB 从数据库全量加载已激活的用户与 API Key 至内存缓存
func (m *UserManager) SyncFromDB() {
	if m.gormDB == nil {
		return
	}

	type dbUserRow struct {
		ID           uint   `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		Role         string `gorm:"column:role"`
		PasswordHash string `gorm:"column:password_hash"`
		Status       string `gorm:"column:status"`
		PlanExpireAt int64  `gorm:"column:plan_expire_at"`
	}

	type dbKeyRow struct {
		ID               uint      `gorm:"column:id"`
		UserID           uint      `gorm:"column:user_id"`
		Name             string    `gorm:"column:name"`
		Key              string    `gorm:"column:key"`
		Status           string    `gorm:"column:status"`
		AllowedModels    string    `gorm:"column:allowed_models"`
		LimitTokens      int64     `gorm:"column:limit_tokens"`
		UsedTokens       int64     `gorm:"column:used_tokens"`
		UsedGeminiTokens int64     `gorm:"column:used_gemini_tokens"`
		UsedClaudeTokens int64     `gorm:"column:used_claude_tokens"`
		UsedNvidiaTokens int64     `gorm:"column:used_nvidia_tokens"`
		UsedGrokTokens   int64     `gorm:"column:used_grok_tokens"`
		CreatedAt        time.Time `gorm:"column:created_at"`
	}

	var userRows []dbUserRow
	if err := m.gormDB.Table("users").Where("status = 'active'").Find(&userRows).Error; err != nil {
		return
	}

	var keyRows []dbKeyRow
	if err := m.gormDB.Table("api_keys").Where("status = 'active'").Find(&keyRows).Error; err != nil {
		return
	}

	m.Lock()
	defer m.Unlock()

	// 组织 DB 用户映射
	userMap := make(map[uint]*RelayUser, len(userRows))
	for _, ur := range userRows {
		userIDStr := strconv.FormatUint(uint64(ur.ID), 10)
		var existing *RelayUser
		for _, u := range m.users {
			if u.ID == userIDStr || u.Key == ur.Username {
				existing = u
				break
			}
		}

		if existing == nil {
			existing = &RelayUser{
				ID:           userIDStr,
				Key:          ur.Username,
				PasswordHash: ur.PasswordHash,
				Enabled:      true,
				Role:         ur.Role,
				IsAdmin:      ur.Role == "admin",
				CreatedAt:    time.Now(),
				APIKeys:      make([]UserAPIKey, 0),
			}
			existing.Quotas.ExpireAt = ur.PlanExpireAt
			m.users = append(m.users, existing)
		} else {
			existing.Enabled = true
			existing.Role = ur.Role
			existing.IsAdmin = ur.Role == "admin"
			existing.Quotas.ExpireAt = ur.PlanExpireAt
			if ur.PasswordHash != "" {
				existing.PasswordHash = ur.PasswordHash
			}
		}
		userMap[ur.ID] = existing
	}

	// 挂载 API Key
	for _, kr := range keyRows {
		user, exists := userMap[kr.UserID]
		if !exists {
			continue
		}

		var allowedModels []string
		if kr.AllowedModels != "" {
			_ = json.Unmarshal([]byte(kr.AllowedModels), &allowedModels)
		}

		keyIDStr := strconv.FormatUint(uint64(kr.ID), 10)
		var matchedKey *UserAPIKey
		for i := range user.APIKeys {
			if user.APIKeys[i].Key == kr.Key || user.APIKeys[i].ID == keyIDStr {
				matchedKey = &user.APIKeys[i]
				break
			}
		}

		if matchedKey == nil {
			user.APIKeys = append(user.APIKeys, UserAPIKey{
				ID:                keyIDStr,
				Name:              kr.Name,
				Key:               kr.Key,
				CreatedAt:         kr.CreatedAt,
				AllowedModels:     allowedModels,
				LimitTokens:       kr.LimitTokens,
				LimitGeminiTokens: kr.LimitTokens,
				LimitClaudeTokens: kr.LimitTokens,
				LimitNvidiaTokens: kr.LimitTokens,
				LimitGrokTokens:   kr.LimitTokens,
				UsedTokens:        kr.UsedTokens,
				UsedGeminiTokens:  kr.UsedGeminiTokens,
				UsedClaudeTokens:  kr.UsedClaudeTokens,
				UsedNvidiaTokens:  kr.UsedNvidiaTokens,
				UsedGrokTokens:    kr.UsedGrokTokens,
			})
		} else {
			matchedKey.Name = kr.Name
			matchedKey.AllowedModels = allowedModels
			matchedKey.LimitTokens = kr.LimitTokens
			if kr.LimitTokens > 0 {
				matchedKey.LimitGeminiTokens = kr.LimitTokens
				matchedKey.LimitClaudeTokens = kr.LimitTokens
				matchedKey.LimitNvidiaTokens = kr.LimitTokens
				matchedKey.LimitGrokTokens = kr.LimitTokens
			}
			matchedKey.UsedTokens = kr.UsedTokens
			matchedKey.UsedGeminiTokens = kr.UsedGeminiTokens
			matchedKey.UsedClaudeTokens = kr.UsedClaudeTokens
			matchedKey.UsedNvidiaTokens = kr.UsedNvidiaTokens
			matchedKey.UsedGrokTokens = kr.UsedGrokTokens
		}
	}
	m.saveToDiskLocked()
}

// findAndCacheKeyFromDB 在内存未命中时，定向回表查询 DB 并即时回填缓存
func (m *UserManager) findAndCacheKeyFromDB(token string) (*RelayUser, *UserAPIKey, error) {
	if m.gormDB == nil || token == "" {
		return nil, nil, fmt.Errorf("db not available or token empty")
	}

	type dbKeyRow struct {
		ID               uint      `gorm:"column:id"`
		UserID           uint      `gorm:"column:user_id"`
		Name             string    `gorm:"column:name"`
		Key              string    `gorm:"column:key"`
		Status           string    `gorm:"column:status"`
		AllowedModels    string    `gorm:"column:allowed_models"`
		LimitTokens      int64     `gorm:"column:limit_tokens"`
		UsedTokens       int64     `gorm:"column:used_tokens"`
		UsedGeminiTokens int64     `gorm:"column:used_gemini_tokens"`
		UsedClaudeTokens int64     `gorm:"column:used_claude_tokens"`
		UsedNvidiaTokens int64     `gorm:"column:used_nvidia_tokens"`
		UsedGrokTokens   int64     `gorm:"column:used_grok_tokens"`
		CreatedAt        time.Time `gorm:"column:created_at"`
	}

	var kr dbKeyRow
	if err := m.gormDB.Table("api_keys").Where("`key` = ? AND status = 'active'", token).First(&kr).Error; err != nil {
		return nil, nil, err
	}

	type dbUserRow struct {
		ID           uint   `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		Role         string `gorm:"column:role"`
		PasswordHash string `gorm:"column:password_hash"`
		Status       string `gorm:"column:status"`
		PlanExpireAt int64  `gorm:"column:plan_expire_at"`
	}
	var ur dbUserRow
	if err := m.gormDB.Table("users").Where("id = ?", kr.UserID).First(&ur).Error; err != nil {
		ur.ID = kr.UserID
		ur.Username = fmt.Sprintf("user_%d", kr.UserID)
		ur.Role = "user"
		ur.Status = "active"
	}

	var allowedModels []string
	if kr.AllowedModels != "" {
		_ = json.Unmarshal([]byte(kr.AllowedModels), &allowedModels)
	}

	m.Lock()
	defer m.Unlock()

	userIDStr := strconv.FormatUint(uint64(ur.ID), 10)
	var targetUser *RelayUser
	for _, u := range m.users {
		if u.ID == userIDStr || u.Key == ur.Username {
			targetUser = u
			break
		}
	}
	if targetUser == nil {
		targetUser = &RelayUser{
			ID:           userIDStr,
			Key:          ur.Username,
			PasswordHash: ur.PasswordHash,
			Enabled:      ur.Status == "active",
			Role:         ur.Role,
			IsAdmin:      ur.Role == "admin",
			CreatedAt:    time.Now(),
			APIKeys:      make([]UserAPIKey, 0),
		}
		targetUser.Quotas.ExpireAt = ur.PlanExpireAt
		m.users = append(m.users, targetUser)
	} else {
		targetUser.Quotas.ExpireAt = ur.PlanExpireAt
	}

	keyIDStr := strconv.FormatUint(uint64(kr.ID), 10)
	for i := range targetUser.APIKeys {
		if targetUser.APIKeys[i].Key == kr.Key {
			targetUser.APIKeys[i].AllowedModels = allowedModels
			targetUser.APIKeys[i].LimitTokens = kr.LimitTokens
			targetUser.APIKeys[i].LimitGeminiTokens = kr.LimitTokens
			targetUser.APIKeys[i].LimitClaudeTokens = kr.LimitTokens
			targetUser.APIKeys[i].LimitNvidiaTokens = kr.LimitTokens
			targetUser.APIKeys[i].LimitGrokTokens = kr.LimitTokens
			targetUser.APIKeys[i].UsedTokens = kr.UsedTokens
			targetUser.APIKeys[i].UsedGeminiTokens = kr.UsedGeminiTokens
			targetUser.APIKeys[i].UsedClaudeTokens = kr.UsedClaudeTokens
			targetUser.APIKeys[i].UsedNvidiaTokens = kr.UsedNvidiaTokens
			targetUser.APIKeys[i].UsedGrokTokens = kr.UsedGrokTokens
			m.saveToDiskLocked()
			return targetUser, &targetUser.APIKeys[i], nil
		}
	}

	newKey := UserAPIKey{
		ID:                keyIDStr,
		Name:              kr.Name,
		Key:               kr.Key,
		CreatedAt:         kr.CreatedAt,
		AllowedModels:     allowedModels,
		LimitTokens:       kr.LimitTokens,
		LimitGeminiTokens: kr.LimitTokens,
		LimitClaudeTokens: kr.LimitTokens,
		LimitNvidiaTokens: kr.LimitTokens,
		LimitGrokTokens:   kr.LimitTokens,
		UsedTokens:        kr.UsedTokens,
		UsedGeminiTokens:  kr.UsedGeminiTokens,
		UsedClaudeTokens:  kr.UsedClaudeTokens,
		UsedNvidiaTokens:  kr.UsedNvidiaTokens,
		UsedGrokTokens:    kr.UsedGrokTokens,
	}
	targetUser.APIKeys = append(targetUser.APIKeys, newKey)
	m.saveToDiskLocked()
	return targetUser, &targetUser.APIKeys[len(targetUser.APIKeys)-1], nil
}
