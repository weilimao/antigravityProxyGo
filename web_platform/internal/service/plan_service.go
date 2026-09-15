package service

import (
	"errors"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"gorm.io/gorm"
)

type PlanService struct{}

func NewPlanService() *PlanService {
	return &PlanService{}
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

	existing.Name = req.Name
	existing.Description = req.Description
	existing.PriceCents = req.PriceCents
	existing.DurationDays = req.DurationDays
	existing.AllowedModels = req.AllowedModels
	existing.Quotas = req.Quotas
	existing.RateLimit = req.RateLimit
	existing.SortOrder = req.SortOrder
	existing.Status = req.Status

	return database.DB.Save(&existing).Error
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

	// 2. 自动更新用户现有所有 API Key 的 AllowedModels 为新套餐的模型白名单
	var keys []model.APIKey
	if err := tx.Where("user_id = ?", user.ID).Find(&keys).Error; err == nil {
		for _, k := range keys {
			k.AllowedModels = plan.AllowedModels
			k.RateLimit = plan.RateLimit
			_ = tx.Save(&k).Error
		}
	}

	return nil
}
