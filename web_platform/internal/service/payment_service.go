package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	planService    *PlanService
	settingService *SettingService
}

func NewPaymentService() *PaymentService {
	return &PaymentService{
		planService:    NewPlanService(),
		settingService: NewSettingService(),
	}
}

// CreateRelayOrder 创建本地订单并根据当前配置跳转（支持极客工坊切单中继、易支付直连与沙箱模拟）
func (s *PaymentService) CreateRelayOrder(userID uint, planID uint) (*model.Order, string, error) {
	db := database.DB

	payCfg, err := s.settingService.GetPaymentConfig()
	if err != nil {
		return nil, "", fmt.Errorf("读取支付配置失败: %w", err)
	}

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

	subject := fmt.Sprintf("Antigravity 会员订阅 - %s", plan.Name)

	// 1. 本地开发模拟沙箱模式 (Fake)
	if payCfg.PayProvider == "fake" {
		return &order, payCfg.PayReturnURL, db.Transaction(func(tx *gorm.DB) error {
			now := time.Now()
			order.Status = "paid"
			order.PaidAt = &now
			order.PayURL = payCfg.PayReturnURL
			if err := tx.Save(&order).Error; err != nil {
				return err
			}
			return s.planService.ActivatePlanTx(tx, order.UserID, order.PlanID)
		})
	}

	// 2. 易支付 / 切单中继模式
	relayURL := strings.TrimSpace(payCfg.RelayURL)
	if relayURL != "" {
		// 2.1 极客工坊切单中继模式 (对标 ProxySubForClash _create_relay_pay_url)
		payload := map[string]interface{}{
			"relay_order_no": order.OrderNo,
			"amount_cents":   order.AmountCents,
			"subject":        subject,
			"return_url":     payCfg.PayReturnURL,
			"notify_url":     payCfg.RelayNotifyURL,
		}
		secret := payCfg.RelaySecret
		if secret == "" && config.GlobalConfig != nil {
			secret = config.GlobalConfig.Payment.RelaySecret
		}
		payload["sign"] = crypto.GenerateRelaySign(payload, secret)

		reqBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Post(relayURL, "application/json", bytes.NewReader(reqBytes))
		if err != nil {
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
		base := strings.TrimSpace(payCfg.RelayCheckoutBase)
		if base != "" {
			if data.OrderNo != "" {
				finalPayURL = fmt.Sprintf("%s/checkout/%s", strings.TrimRight(base, "/"), data.OrderNo)
			} else if strings.Contains(data.PayURL, "/checkout/") {
				parts := strings.Split(data.PayURL, "/checkout/")
				if len(parts) > 1 {
					finalPayURL = fmt.Sprintf("%s/checkout/%s", strings.TrimRight(base, "/"), parts[1])
				}
			} else if strings.Contains(data.PayURL, "/#/checkout/") {
				parts := strings.Split(data.PayURL, "/#/checkout/")
				if len(parts) > 1 {
					finalPayURL = fmt.Sprintf("%s/checkout/%s", strings.TrimRight(base, "/"), parts[1])
				}
			}
		}

		// 全局去 Hash 清洗兜底：确保无论是 base 重写还是远端原样返回，均使用标准 Direct Path 直达收银台
		if strings.Contains(finalPayURL, "/#/checkout/") {
			finalPayURL = strings.Replace(finalPayURL, "/#/checkout/", "/checkout/", 1)
		}

		order.RelayOrderNo = data.OrderNo
		order.PayURL = finalPayURL
		_ = db.Save(&order)
		return &order, finalPayURL, nil
	}

	// 2.2 易支付直连模式 (EPay Direct)
	epayURL := strings.TrimSpace(payCfg.EpayURL)
	epayPID := strings.TrimSpace(payCfg.EpayPID)
	epayKey := strings.TrimSpace(payCfg.EpayKey)
	if epayURL == "" || epayPID == "" || epayKey == "" {
		return &order, "", errors.New("易支付通道未配置: 请在后台「支付跳转配置」填写中继 URL 或易支付网关地址/PID/KEY")
	}

	epayType := strings.TrimSpace(payCfg.EpayType)
	if epayType == "" {
		epayType = "alipay"
	}

	moneyStr := fmt.Sprintf("%.2f", float64(order.AmountCents)/100.0)
	params := map[string]interface{}{
		"pid":          epayPID,
		"type":         epayType,
		"out_trade_no": order.OrderNo,
		"notify_url":   payCfg.EpayNotifyURL,
		"return_url":   payCfg.PayReturnURL,
		"name":         subject,
		"money":        moneyStr,
	}

	sign := crypto.GenerateEpaySign(params, epayKey)

	// 组装易支付 submit.php 跳转 URL
	urlVals := url.Values{}
	urlVals.Set("pid", epayPID)
	urlVals.Set("type", epayType)
	urlVals.Set("out_trade_no", order.OrderNo)
	urlVals.Set("notify_url", payCfg.EpayNotifyURL)
	urlVals.Set("return_url", payCfg.PayReturnURL)
	urlVals.Set("name", subject)
	urlVals.Set("money", moneyStr)
	urlVals.Set("sign", sign)
	urlVals.Set("sign_type", "MD5")

	finalPayURL := fmt.Sprintf("%s/submit.php?%s", strings.TrimRight(epayURL, "/"), urlVals.Encode())
	order.PayURL = finalPayURL
	_ = db.Save(&order)

	return &order, finalPayURL, nil
}

// HandleRelayWebhook 处理极客工坊支付成功异步回调
func (s *PaymentService) HandleRelayWebhook(params map[string]interface{}) error {
	payCfg, err := s.settingService.GetPaymentConfig()
	if err != nil {
		return fmt.Errorf("读取支付配置失败: %w", err)
	}

	secret := strings.TrimSpace(payCfg.RelaySecret)
	if secret == "" && config.GlobalConfig != nil {
		secret = config.GlobalConfig.Payment.RelaySecret
	}

	// 1. 验证极客工坊 HMAC-SHA256 通信签名
	if !crypto.VerifyRelaySign(params, secret) {
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

	db := database.DB
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

// HandleEpayWebhook 处理易支付官方直连支付成功异步回调
func (s *PaymentService) HandleEpayWebhook(params map[string]interface{}) error {
	payCfg, err := s.settingService.GetPaymentConfig()
	if err != nil {
		return fmt.Errorf("读取支付配置失败: %w", err)
	}

	epayKey := strings.TrimSpace(payCfg.EpayKey)
	if epayKey == "" {
		return errors.New("易支付通信密钥未配置，拒绝回调")
	}

	// 1. 验证易支付 MD5 通信签名
	if !crypto.VerifyEpaySign(params, epayKey) {
		return errors.New("易支付签名校验失败，拒绝未授权请求")
	}

	tradeStatus := fmt.Sprintf("%v", params["trade_status"])
	if tradeStatus != "TRADE_SUCCESS" {
		return nil
	}

	outTradeNo := fmt.Sprintf("%v", params["out_trade_no"])
	if outTradeNo == "" {
		return errors.New("缺少 out_trade_no 字段")
	}

	db := database.DB
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
