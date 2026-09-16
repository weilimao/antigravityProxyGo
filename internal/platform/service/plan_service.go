package service

import (
	"errors"
	"time"

	"antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/platform/model"

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
	err := db.GlobalDB.Where("status = ?", "active").Order("sort_order asc, id asc").Find(&plans).Error
	return plans, err
}

func (s *PlanService) ListAllPlans() ([]model.Plan, error) {
	var plans []model.Plan
	err := db.GlobalDB.Order("sort_order asc, id asc").Find(&plans).Error
	return plans, err
}

func (s *PlanService) GetPlanByID(id uint) (*model.Plan, error) {
	var plan model.Plan
	err := db.GlobalDB.First(&plan, id).Error
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *PlanService) CreatePlan(plan *model.Plan) error {
	if plan.Name == "" {
		return errors.New("套餐名称不能为空")
	}
	if plan.Status == "" {
		plan.Status = "active"
	}
	return db.GlobalDB.Create(plan).Error
}

func (s *PlanService) UpdatePlan(id uint, req *model.Plan) error {
	var existing model.Plan
	if err := db.GlobalDB.First(&existing, id).Error; err != nil {
		return errors.New("套餐不存在")
	}

	existing.Name = req.Name
	existing.Description = req.Description
	existing.PriceCents = req.PriceCents
	existing.DurationDays = req.DurationDays
	existing.AllowedModels = req.AllowedModels
	existing.Quotas = req.Quotas
	existing.TokenLimit = req.TokenLimit
	existing.RateLimit = req.RateLimit
	existing.SortOrder = req.SortOrder
	existing.Status = req.Status

	if err := db.GlobalDB.Save(&existing).Error; err != nil {
		return err
	}

	// 级联同步更新: 绑定了此套餐的所有用户的已建 API Key 的 AllowedModels 与 RateLimit/TokenLimit
	var users []model.User
	if err := db.GlobalDB.Where("plan_id = ?", id).Find(&users).Error; err == nil && len(users) > 0 {
		userIDs := make([]uint, 0, len(users))
		userMap := make(map[uint]string, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
			userMap[u.ID] = u.Username
		}

		var keys []model.APIKey
		if err := db.GlobalDB.Where("user_id IN ?", userIDs).Find(&keys).Error; err == nil {
			for _, k := range keys {
				k.AllowedModels = existing.AllowedModels
				if existing.RateLimit > 0 {
					k.RateLimit = existing.RateLimit
				}
				if existing.TokenLimit > 0 {
					k.LimitTokens = existing.TokenLimit
				}
				_ = db.GlobalDB.Save(&k).Error

				// 桥接同步至 18444 Relay 网关 (支持幂等覆盖更新)
				if s.bridge != nil {
					if uname, ok := userMap[k.UserID]; ok && uname != "" {
						_, _ = s.bridge.CreateKeyOnRelay(uname, k.Name, k.Key, existing.AllowedModels, existing.TokenLimit)
					}
				}
			}
		}
	}

	return nil
}

func (s *PlanService) DeletePlan(id uint) error {
	return db.GlobalDB.Delete(&model.Plan{}, id).Error
}

// ActivatePlan 激活/续费用户套餐（独立事务入口）
func (s *PlanService) ActivatePlan(userID uint, planID uint) error {
	return db.GlobalDB.Transaction(func(tx *gorm.DB) error {
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

	now := time.Now().Unix()
	var newExpire int64

	// 如果 durationDays == 0，表示永久有效
	if plan.DurationDays == 0 {
		newExpire = 0
	} else {
		addSeconds := int64(plan.DurationDays) * 86400
		// 若当前套餐仍在有效期内，顺延时长；否则从当前时间开始计算
		if user.PlanID != nil && *user.PlanID == planID && user.PlanExpireAt > now {
			newExpire = user.PlanExpireAt + addSeconds
		} else {
			newExpire = now + addSeconds
		}
	}

	// 1. 更新用户订阅
	user.PlanID = &plan.ID
	user.PlanExpireAt = newExpire
	if err := tx.Save(&user).Error; err != nil {
		return err
	}

	// 桥接同步到期时间至 18444 Relay 网关
	if s.bridge != nil {
		_ = s.bridge.SyncUserExpireToRelay(user.Username, newExpire)
	}

	// 2. 自动更新用户现有所有 API Key 的 AllowedModels 为新套餐的模型白名单
	var keys []model.APIKey
	if err := tx.Where("user_id = ?", user.ID).Find(&keys).Error; err == nil {
		for _, k := range keys {
			k.AllowedModels = plan.AllowedModels
			k.RateLimit = plan.RateLimit
			k.LimitTokens = plan.TokenLimit
			_ = tx.Save(&k).Error

			// 桥接同步至 18444 Relay 网关
			if s.bridge != nil {
				_, _ = s.bridge.CreateKeyOnRelay(user.Username, k.Name, k.Key, plan.AllowedModels, plan.TokenLimit, newExpire)
			}
		}
	}

	return nil
}
