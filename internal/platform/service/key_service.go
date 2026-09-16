package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/platform/model"
)

type KeyService struct {
	bridge *RelayBridgeService
}

func NewKeyService() *KeyService {
	return &KeyService{
		bridge: NewRelayBridgeService(),
	}
}

func (s *KeyService) ListKeys(userID uint) ([]model.APIKey, error) {
	var keys []model.APIKey
	err := db.GlobalDB.Where("user_id = ?", userID).Order("id desc").Find(&keys).Error
	if err != nil {
		return nil, err
	}

	// 若连接到 18444 Relay 网关，尝试同步最新使用量并持久化回写
	if s.bridge != nil && len(keys) > 0 {
		var user model.User
		if errUser := db.GlobalDB.First(&user, userID).Error; errUser == nil && user.Username != "" {
			if usageMap, errUsage := s.bridge.FetchUserKeysUsage(user.Username); errUsage == nil && len(usageMap) > 0 {
				for i := range keys {
					if val, ok := usageMap[keys[i].Key]; ok && val != keys[i].UsedTokens {
						keys[i].UsedTokens = val
						_ = db.GlobalDB.Model(&model.APIKey{}).Where("id = ?", keys[i].ID).Update("used_tokens", val).Error
					}
				}
			}
		}
	}

	return keys, nil
}

func (s *KeyService) CreateKey(userID uint, name string, customModels []string) (*model.APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "默认调用密钥"
	}

	db := db.GlobalDB

	var user model.User
	if err := db.Preload("Plan").First(&user, userID).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	// 核心商业门禁: 必须具有有效订阅才能创建 API 调用密钥 (管理员支持特权豁免)
	if !user.IsSubscriptionActive() {
		return nil, errors.New("无有效套餐订阅，请先订阅套餐后再创建 API 调用密钥")
	}

	// 1. 提取管理员在管理后台为当前套餐配置的模型白名单
	var adminConfiguredModels []string
	if user.Plan != nil && len(user.Plan.AllowedModels) > 0 {
		adminConfiguredModels = user.Plan.AllowedModels
	}


	// 2. 核心授权准则: 只能使用 auto 和管理员在管理后台配置的模型
	allowedSet := make(map[string]struct{}, len(adminConfiguredModels)+1)
	allowedSet["auto"] = struct{}{}
	for _, m := range adminConfiguredModels {
		if m = strings.TrimSpace(m); m != "" {
			allowedSet[m] = struct{}{}
		}
	}

	// 若用户在前端自选了子集，必须严格受限于上述允许范围
	var finalAllowedModels []string
	finalAllowedModels = append(finalAllowedModels, "auto") // auto 必包含

	if len(customModels) > 0 {
		for _, cm := range customModels {
			cm = strings.TrimSpace(cm)
			if cm == "auto" {
				continue
			}
			if _, ok := allowedSet[cm]; ok {
				if !containsString(finalAllowedModels, cm) {
					finalAllowedModels = append(finalAllowedModels, cm)
				}
			}
		}
	} else {
		for _, am := range adminConfiguredModels {
			am = strings.TrimSpace(am)
			if am != "auto" && !containsString(finalAllowedModels, am) {
				finalAllowedModels = append(finalAllowedModels, am)
			}
		}
	}

	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	candidateKeyStr := fmt.Sprintf("sk-ant-%s", hex.EncodeToString(randBytes))

	var planTokenLimit int64
	var planRateLimit int = 30
	if user.Plan != nil {
		if user.Plan.RateLimit > 0 {
			planRateLimit = user.Plan.RateLimit
		}
		if user.Plan.TokenLimit > 0 {
			planTokenLimit = user.Plan.TokenLimit
		}
	}

	// 3. 桥接调用 18444 Relay 服务端生成/激活该 API Key
	if s.bridge != nil {
		relayKey, errRelay := s.bridge.CreateKeyOnRelay(user.Username, name, candidateKeyStr, finalAllowedModels, planTokenLimit)
		if errRelay == nil && relayKey != nil && relayKey.Key != "" {
			candidateKeyStr = relayKey.Key
		}
	}

	apiKey := model.APIKey{
		Key:           candidateKeyStr,
		UserID:        userID,
		Name:          name,
		AllowedModels: finalAllowedModels,
		RateLimit:     planRateLimit,
		LimitTokens:   planTokenLimit,
		Status:        "active",
	}

	if err := db.Create(&apiKey).Error; err != nil {
		return nil, err
	}

	return &apiKey, nil
}

func (s *KeyService) DeleteKey(userID uint, keyID uint) error {
	var k model.APIKey
	if err := db.GlobalDB.Where("id = ? AND user_id = ?", keyID, userID).First(&k).Error; err != nil {
		return err
	}

	var user model.User
	if err := db.GlobalDB.First(&user, userID).Error; err == nil && s.bridge != nil {
		_ = s.bridge.DeleteKeyOnRelay(user.Username, k.Key)
	}

	return db.GlobalDB.Delete(&k).Error
}
