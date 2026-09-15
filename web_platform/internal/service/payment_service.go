package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/crypto"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PaymentService struct {
	planService *PlanService
}

func NewPaymentService() *PaymentService {
	return &PaymentService{
		planService: NewPlanService(),
	}
}

// CreateRelayOrder 创建本地订单并向极客工坊切单中继发起收银台链接创建
func (s *PaymentService) CreateRelayOrder(userID uint, planID uint) (*model.Order, string, error) {
	db := database.DB
	cfg := config.GlobalConfig

	var plan model.Plan
	if err := db.First(&plan, planID).Error; err != nil {
		return nil, "", errors.New("套餐不存在")
	}

	if plan.Status != "active" {
		return nil, "", errors.New("该套餐已下架")
	}

	orderNo := fmt.Sprintf("ORD_%d_%s", time.Now().Unix(), uuid.New().String()[:8])
	order := model.Order{
		OrderNo:     orderNo,
		UserID:      userID,
		PlanID:      planID,
		AmountCents: plan.PriceCents,
		Status:      "pending",
	}

	if err := db.Create(&order).Error; err != nil {
		return nil, "", fmt.Errorf("创建订单失败: %w", err)
	}

	// 构造极客工坊切单 Payload (对标 ProxySubForClash/payments.py _create_relay_pay_url)
	payload := map[string]interface{}{
		"relay_order_no": order.OrderNo,
		"amount_cents":   order.AmountCents,
		"subject":        fmt.Sprintf("Antigravity 会员订阅 - %s", plan.Name),
		"return_url":     cfg.Payment.ReturnURL,
		"notify_url":     cfg.Payment.NotifyURL,
	}
	payload["sign"] = crypto.GenerateRelaySign(payload, cfg.Payment.RelaySecret)

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(cfg.Payment.RelayURL, "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		// 若远程极客工坊服务暂不可达，给出明确提示
		return &order, "", fmt.Errorf("无法连接极客工坊中继收银服务 (%v)，请检查网络或配置", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &order, "", fmt.Errorf("极客工坊中继收银服务异常: HTTP %d", resp.StatusCode)
	}

	var data struct {
		OrderNo string `json:"order_no"`
		PayURL  string `json:"pay_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return &order, "", fmt.Errorf("解析极客工坊响应失败: %w", err)
	}

	if data.PayURL == "" {
		return &order, "", errors.New("极客工坊未返回有效支付地址")
	}

	finalPayURL := data.PayURL

	// 若后台配置了自定义中继收银台基准跳转地址 (relay_checkout_base)，优先重写前缀
	base := strings.TrimSpace(cfg.Payment.RelayCheckoutBase)
	if base != "" {
		if data.OrderNo != "" {
			finalPayURL = fmt.Sprintf("%s/#/checkout/%s", strings.TrimRight(base, "/"), data.OrderNo)
		} else if strings.Contains(data.PayURL, "/#/checkout/") {
			parts := strings.Split(data.PayURL, "/#/checkout/")
			if len(parts) > 1 {
				finalPayURL = fmt.Sprintf("%s/#/checkout/%s", strings.TrimRight(base, "/"), parts[1])
			}
		}
	}

	order.RelayOrderNo = data.OrderNo
	order.PayURL = finalPayURL
	_ = db.Save(&order)

	return &order, finalPayURL, nil
}

// HandleRelayWebhook 处理极客工坊支付成功异步回调
func (s *PaymentService) HandleRelayWebhook(params map[string]interface{}) error {
	cfg := config.GlobalConfig
	db := database.DB

	// 1. 验证极客工坊 HMAC-SHA256 通信签名
	if !crypto.VerifyRelaySign(params, cfg.Payment.RelaySecret) {
		return errors.New("切单签名校验失败，拒绝未授权请求")
	}

	tradeStatus := fmt.Sprintf("%v", params["trade_status"])
	if tradeStatus != "TRADE_SUCCESS" {
		return nil
	}

	outTradeNo := fmt.Sprintf("%v", params["out_trade_no"])
	if outTradeNo == "" {
		return errors.New("缺少 out_trade_no 字段")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Where("order_no = ?", outTradeNo).First(&order).Error; err != nil {
			return fmt.Errorf("订单未找到: %s", outTradeNo)
		}

		// 幂等保护: 已支付则直接返回成功
		if order.Status == "paid" {
			return nil
		}

		now := time.Now()
		order.Status = "paid"
		order.PaidAt = &now
		if err := tx.Save(&order).Error; err != nil {
			return err
		}

		// 激活/顺延用户套餐与对应模型白名单
		return s.planService.ActivatePlanTx(tx, order.UserID, order.PlanID)
	})
}

func (s *PaymentService) GetOrderByNo(orderNo string) (*model.Order, error) {
	var order model.Order
	err := database.DB.Preload("Plan").Preload("User").Where("order_no = ?", orderNo).First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *PaymentService) ListOrders(page, pageSize int, status string) ([]model.Order, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	var orders []model.Order
	var total int64
	query := database.DB.Model(&model.Order{})

	if status != "" {
		query = query.Where("status = ?", status)
	}

	query.Count(&total)
	err := query.Preload("Plan").Preload("User").Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}

// ManualFulfillOrder 管理员后台手动核销补单
func (s *PaymentService) ManualFulfillOrder(orderNo string) error {
	db := database.DB

	return db.Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			return errors.New("订单不存在")
		}

		if order.Status == "paid" {
			return nil
		}

		now := time.Now()
		order.Status = "paid"
		order.PaidAt = &now
		if err := tx.Save(&order).Error; err != nil {
			return err
		}

		return s.planService.ActivatePlanTx(tx, order.UserID, order.PlanID)
	})
}
