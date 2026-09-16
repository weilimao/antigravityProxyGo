package service

import (
	"context"
	"errors"
	"log"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"gorm.io/gorm"
)

const OrderExpireMinutes = 15 // 待支付订单自动取消超时时间（分钟）

type OrderService struct{}

func NewOrderService() *OrderService {
	return &OrderService{}
}

// UserOrderDTO 返回给前端的用户订单数据传输对象
type UserOrderDTO struct {
	ID            uint        `json:"id"`
	OrderNo       string      `json:"orderNo"`
	UserID        uint        `json:"userId"`
	PlanID        uint        `json:"planId"`
	PlanName      string      `json:"planName"`
	DurationDays  int         `json:"durationDays"`
	AmountCents   int64       `json:"amountCents"`
	Status        string      `json:"status"` // pending / paid / cancelled
	PayURL        string      `json:"payUrl"`
	PaidAt        *time.Time  `json:"paidAt,omitempty"`
	CreatedAt     time.Time   `json:"createdAt"`
	ExpireAt      time.Time   `json:"expireAt"`
	ExpireSeconds int         `json:"expireSeconds"`
	Plan          *model.Plan `json:"plan,omitempty"`
}

// CheckAndExpireOrder 单笔订单超时判定：若处于 pending 且已超过 15 分钟，就地更新为 cancelled
func (s *OrderService) CheckAndExpireOrder(tx *gorm.DB, order *model.Order) bool {
	if order.Status != "pending" {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(OrderExpireMinutes) * time.Minute)
	if order.CreatedAt.Before(cutoff) {
		order.Status = "cancelled"
		tx.Model(order).Update("status", "cancelled")
		return true
	}
	return false
}

// CancelExpiredOrders 批量将创建超过 15 分钟且处于 pending 状态的订单标记为 cancelled
func (s *OrderService) CancelExpiredOrders() (int64, error) {
	cutoff := time.Now().Add(-time.Duration(OrderExpireMinutes) * time.Minute)
	res := database.DB.Model(&model.Order{}).
		Where("status = ? AND created_at < ?", "pending", cutoff).
		Update("status", "cancelled")
	return res.RowsAffected, res.Error
}

// CancelUserOrder 用户主动取消待支付订单
func (s *OrderService) CancelUserOrder(userID uint, orderNo string) error {
	var order model.Order
	if err := database.DB.Where("order_no = ? AND user_id = ?", orderNo, userID).First(&order).Error; err != nil {
		return errors.New("订单不存在或无权操作")
	}

	if order.Status != "pending" {
		if order.Status == "paid" {
			return errors.New("该订单已支付成功，无法取消")
		}
		return errors.New("该订单已被取消或已失效")
	}

	order.Status = "cancelled"
	return database.DB.Model(&order).Update("status", "cancelled").Error
}

// ListUserOrders 获取指定用户的订单列表（附带计算超时剩余秒数）
func (s *OrderService) ListUserOrders(userID uint, page, pageSize int, status string) ([]UserOrderDTO, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	// 查库前先触发一次当前用户的批量过期检查
	cutoff := time.Now().Add(-time.Duration(OrderExpireMinutes) * time.Minute)
	database.DB.Model(&model.Order{}).
		Where("user_id = ? AND status = ? AND created_at < ?", userID, "pending", cutoff).
		Update("status", "cancelled")

	var orders []model.Order
	var total int64
	query := database.DB.Model(&model.Order{}).Where("user_id = ?", userID)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Plan").Order("id desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&orders).Error
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	expireDelta := time.Duration(OrderExpireMinutes) * time.Minute
	var list []UserOrderDTO

	for _, o := range orders {
		expAt := o.CreatedAt.Add(expireDelta)
		expSecs := 0
		st := o.Status
		if st == "pending" {
			rem := int(expAt.Sub(now).Seconds())
			if rem > 0 {
				expSecs = rem
			} else {
				st = "cancelled"
				expSecs = 0
			}
		}

		planName := ""
		durationDays := 0
		if o.Plan != nil {
			planName = o.Plan.Name
			durationDays = o.Plan.DurationDays
		}

		dto := UserOrderDTO{
			ID:            o.ID,
			OrderNo:       o.OrderNo,
			UserID:        o.UserID,
			PlanID:        o.PlanID,
			PlanName:      planName,
			DurationDays:  durationDays,
			AmountCents:   o.AmountCents,
			Status:        st,
			PayURL:        o.PayURL,
			PaidAt:        o.PaidAt,
			CreatedAt:     o.CreatedAt,
			ExpireAt:      expAt,
			ExpireSeconds: expSecs,
			Plan:          o.Plan,
		}
		list = append(list, dto)
	}

	return list, total, nil
}

// GetUserOrderPayURL 针对待支付订单重新获取跳转收银台地址
func (s *OrderService) GetUserOrderPayURL(userID uint, orderNo string) (string, error) {
	var order model.Order
	if err := database.DB.Preload("Plan").Where("order_no = ? AND user_id = ?", orderNo, userID).First(&order).Error; err != nil {
		return "", errors.New("订单未找到")
	}

	// 检查是否超时
	if s.CheckAndExpireOrder(database.DB, &order) {
		return "", errors.New("订单已超过15分钟未支付，已自动取消")
	}

	if order.Status != "pending" {
		if order.Status == "paid" {
			return "", errors.New("该订单已支付完成，无需重复付款")
		}
		return "", errors.New("该订单已关闭，请重新选购")
	}

	if order.PayURL == "" {
		return "", errors.New("该订单未生成有效支付链接，请重新发起订购")
	}

	return order.PayURL, nil
}

// StartOrderExpiryWorker 启动常驻后台协程，定时（每 30 秒）自动取消超时订单
func StartOrderExpiryWorker(ctx context.Context) {
	go func() {
		log.Println("[OrderWorker] 订单 15 分钟超时自动取消后台扫描协程已启动")
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[OrderWorker] 订单超时扫描协程已停止")
				return
			case <-ticker.C:
				svc := NewOrderService()
				affected, err := svc.CancelExpiredOrders()
				if err != nil {
					log.Printf("[OrderWorker] 扫描超时订单发生异常: %v\n", err)
				} else if affected > 0 {
					log.Printf("[OrderWorker] 已自动将 %d 笔超过 15 分钟未支付的待支付订单置为 cancelled\n", affected)
				}
			}
		}
	}()
}
