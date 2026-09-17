package service

import (
	"errors"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"gorm.io/gorm"
)

type PlanService struct {
	bridge *RelayBridgeService
}

func NewPlanService() *PlanService {
	return &PlanService{
		bridge: NewRelayBridgeService(),
	}
}

func (s *PlanService) ListActivePlans() ([]model.Plan, error) {
	var plans []model.Plan
	err := database.DB.Where("status = ?", "active").Order("sort_order asc, id asc").Find(&plans).Error
	return plans, err
}

func (s *PlanService) ListAllPlans() ([]model.Plan, error) {
	var plans []model.Plan
	err := database.DB.Order("sort_order asc, id asc").Find(&plans).Error
	return plans, err
}

func (s *PlanService) GetPlanByID(id uint) (*model.Plan, error) {
	var plan model.Plan
	err := database.DB.First(&plan, id).Error
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *PlanService) CreatePlan(plan *model.Plan) error {
	if plan.Name == "" {
		return errors.New("套餐名称不能为空")
	}
	if plan.PriceCents < 100 {
		return errors.New("套餐售价最低不能低于 1 元")
	}
	if plan.Type == "" {
		plan.Type = "subscription"
	}
	if plan.Type == "subscription" {
		if plan.Tier == "" {
			plan.Tier = "pro"
		}
	} else {
		plan.Tier = ""
	}
	if plan.Status == "" {
		plan.Status = "active"
	}
	return database.DB.Create(plan).Error
}

func (s *PlanService) UpdatePlan(id uint, req *model.Plan) error {
	var existing model.Plan
	if err := database.DB.First(&existing, id).Error; err != nil {
		return errors.New("套餐不存在")
	}
	if req.PriceCents < 100 {
		return errors.New("套餐售价最低不能低于 1 元")
	}

	existing.Name = req.Name
	if req.Type != "" {
		existing.Type = req.Type
	}
	if existing.Type == "subscription" {
		if req.Tier != "" {
			existing.Tier = req.Tier
		} else if existing.Tier == "" {
			existing.Tier = "pro"
		}
	} else {
		existing.Tier = ""
	}
	existing.Description = req.Description
	existing.PriceCents = req.PriceCents
	existing.DurationDays = req.DurationDays
	existing.AllowedModels = req.AllowedModels
	existing.AutoModels = req.AutoModels
	existing.Quotas = req.Quotas
	existing.TokenLimit = req.TokenLimit
	existing.RateLimit = req.RateLimit
	existing.SortOrder = req.SortOrder
	existing.Status = req.Status

	if err := database.DB.Save(&existing).Error; err != nil {
		return err
	}

	// 级联同步更新: 绑定了此套餐的所有用户的已建 API Key 的 AllowedModels 与 RateLimit/TokenLimit
	var users []model.User
	if err := database.DB.Where("plan_id = ?", id).Find(&users).Error; err == nil && len(users) > 0 {
		userIDs := make([]uint, 0, len(users))
		userMap := make(map[uint]string, len(users))
		userExtraMap := make(map[uint]int64, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
			userMap[u.ID] = u.Username
			userExtraMap[u.ID] = u.ExtraTokens
		}

		var keys []model.APIKey
		if err := database.DB.Where("user_id IN ?", userIDs).Find(&keys).Error; err == nil {
			for _, k := range keys {
				k.AllowedModels = existing.AllowedModels
				if existing.RateLimit > 0 {
					k.RateLimit = existing.RateLimit
				}
				if existing.TokenLimit > 0 {
					extra := userExtraMap[k.UserID]
					k.LimitTokens = existing.TokenLimit + extra
				}
				_ = database.DB.Save(&k).Error

				// 桥接同步至 18444 Relay 网关 (支持幂等覆盖更新)
				if s.bridge != nil {
					if uname, ok := userMap[k.UserID]; ok && uname != "" {
						_, _ = s.bridge.CreateKeyOnRelay(uname, k.Name, k.Key, existing.AllowedModels, k.LimitTokens)
					}
				}
			}
		}
	}

	return nil
}

func (s *PlanService) DeletePlan(id uint) error {
	return database.DB.Delete(&model.Plan{}, id).Error
}

// ActivatePlan 激活/续费用户套餐（独立事务入口）
func (s *PlanService) ActivatePlan(userID uint, planID uint) error {
	return database.DB.Transaction(func(tx *gorm.DB) error {
		return s.ActivatePlanTx(tx, userID, planID)
	})
}

// ActivatePlanTx 在给定的事务/DB上下文内激活或顺延套餐，避免多重嵌套锁库
func (s *PlanService) ActivatePlanTx(tx *gorm.DB, userID uint, planID uint) error {
	var user model.User
	if err := tx.First(&user, userID).Error; err != nil {
		return errors.New("用户不存在")
	}

	var plan model.Plan
	if err := tx.First(&plan, planID).Error; err != nil {
		return errors.New("套餐不存在")
	}

	// 1. 若为加油包 (addon) 套餐：不覆盖基础订阅方案，累加 Token 额度并根据后台配置顺延有效时长（订阅失效亦可使用至加油包结束）
	if plan.Type == "addon" {
		if plan.TokenLimit > 0 {
			user.ExtraTokens += plan.TokenLimit
		}

		// 购买有效期由后台配置：若配置了有效天数且当前非永久会员，顺延到期时间
		if plan.DurationDays > 0 && user.PlanExpireAt != 0 {
			addSeconds := int64(plan.DurationDays) * 86400
			now := time.Now().Unix()
			if user.PlanExpireAt > now {
				user.PlanExpireAt += addSeconds
			} else {
				user.PlanExpireAt = now + addSeconds
			}
		}

		if err := tx.Save(&user).Error; err != nil {
			return err
		}

		// 自动同步调大用户名下所有已有 API Key 的 LimitTokens，并同步最新有效截止时间
		var keys []model.APIKey
		if err := tx.Where("user_id = ?", user.ID).Find(&keys).Error; err == nil {
			for _, k := range keys {
				if k.LimitTokens > 0 {
					k.LimitTokens += plan.TokenLimit
				} else if user.PlanID != nil {
					var basePlan model.Plan
					if err := tx.First(&basePlan, *user.PlanID).Error; err == nil && basePlan.TokenLimit > 0 {
						k.LimitTokens = basePlan.TokenLimit + user.ExtraTokens
					}
				}
				_ = tx.Save(&k).Error

				// 桥接同步至 18444 Relay 网关 (传入最新的有效截止时间)
				if s.bridge != nil {
					_, _ = s.bridge.CreateKeyOnRelay(user.Username, k.Name, k.Key, k.AllowedModels, k.LimitTokens, user.PlanExpireAt)
				}
			}
		}
		return nil
	}

	// 2. 若为基础订阅套餐 (subscription)：执行激活或顺延到期时间
	now := time.Now().Unix()
	var newExpire int64

	isCrossPlanUpgrade := user.PlanID != nil && *user.PlanID != planID && (user.PlanExpireAt == 0 || user.PlanExpireAt > now)

	// 如果 durationDays == 0，表示永久有效
	if plan.DurationDays == 0 {
		newExpire = 0
	} else {
		addSeconds := int64(plan.DurationDays) * 86400
		// 若当前套餐仍在有效期内且为同方案续费，顺延时长；否则从当前时间开始计算
		if user.PlanID != nil && *user.PlanID == planID && user.PlanExpireAt > now {
			newExpire = user.PlanExpireAt + addSeconds
		} else {
			newExpire = now + addSeconds
		}
	}

	// 更新用户订阅
	user.PlanID = &plan.ID
	user.PlanExpireAt = newExpire
	if err := tx.Save(&user).Error; err != nil {
		return err
	}

	// 若为跨套餐升级：用户此前剩余 Token 已按比例折算并从新套餐价格中扣减抵扣，新套餐享有全新完整额度，
	// 原子重置用户所有已建 API Key 的本地已用 Token 与中继网关用量计数器
	if isCrossPlanUpgrade {
		_ = tx.Model(&model.APIKey{}).Where("user_id = ?", user.ID).Updates(map[string]interface{}{
			"used_tokens":        0,
			"used_gemini_tokens": 0,
			"used_claude_tokens": 0,
			"used_nvidia_tokens": 0,
			"used_grok_tokens":   0,
		}).Error

		if s.bridge != nil && user.Username != "" {
			_ = s.bridge.ResetUserKeysUsage(user.Username)
		}
	}

	// 桥接同步到期时间至 18444 Relay 网关
	if s.bridge != nil {
		_ = s.bridge.SyncUserExpireToRelay(user.Username, newExpire)
	}

	// 自动更新用户现有所有 API Key 的 AllowedModels 为新套餐的模型白名单，限额保留增值额度
	var keys []model.APIKey
	if err := tx.Where("user_id = ?", user.ID).Find(&keys).Error; err == nil {
		for _, k := range keys {
			k.AllowedModels = plan.AllowedModels
			k.RateLimit = plan.RateLimit
			var finalLimit int64
			if plan.TokenLimit > 0 {
				finalLimit = plan.TokenLimit + user.ExtraTokens
			}
			k.LimitTokens = finalLimit
			if isCrossPlanUpgrade {
				k.UsedTokens = 0
				k.UsedGeminiTokens = 0
				k.UsedClaudeTokens = 0
				k.UsedNvidiaTokens = 0
				k.UsedGrokTokens = 0
			}
			_ = tx.Save(&k).Error

			// 桥接同步至 18444 Relay 网关
			if s.bridge != nil {
				_, _ = s.bridge.CreateKeyOnRelay(user.Username, k.Name, k.Key, plan.AllowedModels, finalLimit, newExpire)
			}
		}
	}

	return nil
}
