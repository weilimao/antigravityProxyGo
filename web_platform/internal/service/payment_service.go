package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/crypto"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// parseMoneyToCents 将易支付/中继协议传入的 money 字段（单位：元）解析并换算为整数分
func parseMoneyToCents(val interface{}) (int64, error) {
	if val == nil {
		return 0, errors.New("empty money")
	}
	valStr := strings.TrimSpace(fmt.Sprintf("%v", val))
	if valStr == "" {
		return 0, errors.New("empty money string")
	}
	f, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * 100)), nil
}

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

type UpgradeQuote struct {
	PlanID                 uint   `json:"planId"`
	PlanName               string `json:"planName"`
	PriceCents             int64  `json:"priceCents"`
	IsUpgrade              bool   `json:"isUpgrade"`
	CanUpgrade             bool   `json:"canUpgrade"`
	CannotUpgradeReason    string `json:"cannotUpgradeReason,omitempty"`
	CurrentPlanID          *uint  `json:"currentPlanId,omitempty"`
	CurrentPlanName        string `json:"currentPlanName,omitempty"`
	CurrentPlanTier        string `json:"currentPlanTier,omitempty"`
	TargetPlanTier         string `json:"targetPlanTier,omitempty"`
	CurrentPlanPriceCents  int64  `json:"currentPlanPriceCents,omitempty"`
	CurrentPlanTokenLimit  int64  `json:"currentPlanTokenLimit,omitempty"`
	CurrentUsedTokens      int64  `json:"currentUsedTokens"`
	CurrentRemainingTokens int64  `json:"currentRemainingTokens"`
	RemainingFeeCents      int64  `json:"remainingFeeCents"`
	DiscountCents          int64  `json:"discountCents"`
	FinalAmountCents       int64  `json:"finalAmountCents"`
}

// CalculateUpgradeQuote 计算指定用户升级至目标套餐的折算抵扣明细与最终实付金额
func (s *PaymentService) CalculateUpgradeQuote(userID uint, targetPlanID uint) (*UpgradeQuote, error) {
	db := database.DB

	var targetPlan model.Plan
	if err := db.First(&targetPlan, targetPlanID).Error; err != nil {
		return nil, errors.New("目标套餐不存在")
	}

	quote := &UpgradeQuote{
		PlanID:           targetPlan.ID,
		PlanName:         targetPlan.Name,
		PriceCents:       targetPlan.PriceCents,
		IsUpgrade:        false,
		CanUpgrade:       true,
		TargetPlanTier:   targetPlan.Tier,
		FinalAmountCents: targetPlan.PriceCents,
	}

	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	now := time.Now().Unix()
	hasActiveSubscription := user.PlanID != nil && *user.PlanID > 0 && (user.PlanExpireAt == 0 || user.PlanExpireAt > now)

	// 若目标套餐是加油包，或者用户当前没有尚未过期的基础会员订阅，不进行升级折算
	if targetPlan.Type == "addon" || !hasActiveSubscription {
		return quote, nil
	}

	// 用户持有有效订阅且目标套餐为基础会员订阅方案：先获取当前方案
	var currentPlan model.Plan
	if err := db.First(&currentPlan, *user.PlanID).Error; err != nil {
		return quote, nil
	}

	quote.CurrentPlanID = user.PlanID
	quote.CurrentPlanName = currentPlan.Name
	quote.CurrentPlanTier = currentPlan.Tier
	quote.CurrentPlanPriceCents = currentPlan.PriceCents
	quote.CurrentPlanTokenLimit = currentPlan.TokenLimit

	// 1. 同套餐重复购买拦截
	if user.PlanID != nil && *user.PlanID == targetPlanID {
		quote.CanUpgrade = false
		quote.CannotUpgradeReason = "您当前已在该方案生效期内，无需重复订购；如需续费请在到期后再行操作"
		return quote, nil
	}

	// 2. 严格按层级判定 (Pro < MAX < MAX+ < MAX++)，严禁降级或同级折算购买
	currentWeight := model.GetTierWeight(currentPlan.Tier)
	targetWeight := model.GetTierWeight(targetPlan.Tier)
	if targetWeight <= currentWeight {
		quote.IsUpgrade = false
		quote.CanUpgrade = false
		quote.CannotUpgradeReason = fmt.Sprintf("您当前方案【%s】(%s) 等级高于或等于目标方案(%s)，系统暂不支持降级订购",
			currentPlan.Name, model.NormalizeTier(currentPlan.Tier), model.NormalizeTier(targetPlan.Tier))
		return quote, nil
	}

	quote.IsUpgrade = true

	// 若连接到 18444 Relay 网关，先拉取最新用量持久化（key 保留字反引号转义）
	if s.planService != nil && s.planService.bridge != nil && user.Username != "" {
		if usageMap, errUsage := s.planService.bridge.FetchUserKeysUsage(user.Username); errUsage == nil {
			for keyStr, val := range usageMap {
				_ = db.Model(&model.APIKey{}).Where("user_id = ? AND `key` = ?", user.ID, keyStr).Update("used_tokens", val).Error
			}
		}
	}

	// 汇总用户名下所有 API Key 实际已消耗量
	var totalUsedTokens int64
	db.Model(&model.APIKey{}).Where("user_id = ?", user.ID).Select("COALESCE(SUM(used_tokens), 0)").Scan(&totalUsedTokens)
	quote.CurrentUsedTokens = totalUsedTokens

	var currentRemainingTokens int64
	if currentPlan.TokenLimit > 0 {
		if totalUsedTokens < currentPlan.TokenLimit {
			currentRemainingTokens = currentPlan.TokenLimit - totalUsedTokens
		} else {
			currentRemainingTokens = 0
		}
	}
	quote.CurrentRemainingTokens = currentRemainingTokens

	var remainingFeeCents int64
	if currentPlan.TokenLimit > 0 && currentRemainingTokens > 0 && currentPlan.PriceCents > 0 {
		fee := float64(currentPlan.PriceCents) * (float64(currentRemainingTokens) / float64(currentPlan.TokenLimit))
		remainingFeeCents = int64(math.Round(fee))
		if remainingFeeCents > currentPlan.PriceCents {
			remainingFeeCents = currentPlan.PriceCents
		}
		if remainingFeeCents < 0 {
			remainingFeeCents = 0
		}
	}
	quote.RemainingFeeCents = remainingFeeCents

	discountCents := remainingFeeCents
	// 升级换购最大折算抵扣限制：必须保证实付金额最低不低于 100 分 (1.00 元)
	maxDiscount := targetPlan.PriceCents - 100
	if maxDiscount < 0 {
		maxDiscount = 0
	}
	if discountCents > maxDiscount {
		discountCents = maxDiscount
	}
	quote.DiscountCents = discountCents
	quote.FinalAmountCents = targetPlan.PriceCents - discountCents
	if quote.FinalAmountCents < 100 {
		quote.FinalAmountCents = 100
	}

	return quote, nil
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

	if plan.PriceCents < 100 {
		return nil, "", errors.New("套餐售价异常，最低不得低于 1 元")
	}

	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, "", errors.New("用户不存在")
	}
	now := time.Now().Unix()
	hasActiveSubscription := user.PlanID != nil && *user.PlanID > 0 && (user.PlanExpireAt == 0 || user.PlanExpireAt > now)

	// 订阅套餐防重复购买与升级折算判定：
	// 若当前已有生效中的订阅（未到期或永久），购买相同套餐拦截；购买其他套餐按当前剩余 Token 数折算剩余费用立减抵扣
	finalAmountCents := plan.PriceCents
	var originalAmountCents int64 = plan.PriceCents
	var discountCents int64 = 0
	var upgradeFromPlanID *uint = nil

	if plan.Type == "" || plan.Type == "subscription" {
		if hasActiveSubscription {
			if user.PlanID != nil && *user.PlanID == planID {
				return nil, "", errors.New("您当前已在该方案生效期内，无需重复订购；如需续费请在到期后再行操作")
			}
			quote, errQuote := s.CalculateUpgradeQuote(userID, planID)
			if errQuote != nil {
				return nil, "", errQuote
			}
			if quote != nil && !quote.CanUpgrade {
				if quote.CannotUpgradeReason != "" {
					return nil, "", errors.New(quote.CannotUpgradeReason)
				}
				return nil, "", errors.New("系统暂不支持降级订购")
			}
			if quote != nil && quote.IsUpgrade {
				discountCents = quote.DiscountCents
				finalAmountCents = quote.FinalAmountCents
				upgradeFromPlanID = quote.CurrentPlanID
			}
		}
	}

	// 加油包限购判定：加油包只能订阅了的账户购买
	if plan.Type == "addon" {
		if !hasActiveSubscription {
			return nil, "", errors.New("加油包仅限持有有效会员订阅的账户购买，请先开通会员订阅")
		}
	}

	// 订单支付金额硬性保底：实付金额最低不能低于 100 分 (1.00 元)
	if finalAmountCents < 100 {
		finalAmountCents = 100
	}

	orderNo := fmt.Sprintf("ORD_%d_%s", time.Now().Unix(), uuid.New().String()[:8])
	order := model.Order{
		OrderNo:             orderNo,
		UserID:              userID,
		PlanID:              planID,
		AmountCents:         finalAmountCents,
		OriginalAmountCents: originalAmountCents,
		DiscountCents:       discountCents,
		UpgradeFromPlanID:   upgradeFromPlanID,
		Status:              "pending",
	}

	if err := db.Create(&order).Error; err != nil {
		return nil, "", fmt.Errorf("创建订单失败: %w", err)
	}

	subjectPrefix := "Antigravity 会员订阅"
	if plan.Type == "addon" {
		subjectPrefix = "Antigravity 算力加油包购买"
	}
	subject := fmt.Sprintf("%s - %s", subjectPrefix, plan.Name)

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
			return &order, "", fmt.Errorf("无法连接极客工坊收银结算服务 (%v)，请检查网络或配置", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return &order, "", fmt.Errorf("极客工坊收银结算服务异常: HTTP %d", resp.StatusCode)
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
		return &order, "", errors.New("易支付通道未配置: 请在后台「支付跳转配置」填写切单 URL 或易支付网关地址/PID/KEY")
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

		// 2. 金额一致性双重核验 (防止金额篡改/低价买高额套餐)
		if moneyVal, hasMoney := params["money"]; hasMoney && moneyVal != nil {
			paidCents, err := parseMoneyToCents(moneyVal)
			if err != nil {
				return fmt.Errorf("解析回调金额失败: %w", err)
			}
			if paidCents != order.AmountCents {
				return fmt.Errorf("支付金额不匹配拦截: expected=%d, got=%d", order.AmountCents, paidCents)
			}
		}

		now := time.Now()
		// 3. 原子状态更新: 允许 pending 或因网络延迟被误置为 cancelled 的订单在收到真实付款后自动挽回流转为 'paid'
		res := tx.Model(&model.Order{}).
			Where("order_no = ? AND status IN ('pending', 'cancelled')", outTradeNo).
			Updates(map[string]interface{}{
				"status":  "paid",
				"paid_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 说明并发回调已处理，安全幂等退出
			return nil
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

		// 2. 金额一致性双重核验 (防止金额篡改/低价买高额套餐)
		if moneyVal, hasMoney := params["money"]; hasMoney && moneyVal != nil {
			paidCents, err := parseMoneyToCents(moneyVal)
			if err != nil {
				return fmt.Errorf("解析回调金额失败: %w", err)
			}
			if paidCents != order.AmountCents {
				return fmt.Errorf("支付金额不匹配拦截: expected=%d, got=%d", order.AmountCents, paidCents)
			}
		}

		now := time.Now()
		// 3. 原子状态更新: 允许 pending 或因超时/延迟被取消的订单收到真实付款后自动履约
		res := tx.Model(&model.Order{}).
			Where("order_no = ? AND status IN ('pending', 'cancelled')", outTradeNo).
			Updates(map[string]interface{}{
				"status":  "paid",
				"paid_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 说明并发回调已处理，安全幂等退出
			return nil
		}

		return s.planService.ActivatePlanTx(tx, order.UserID, order.PlanID)
	})
}

func (s *PaymentService) GetOrderByNo(orderNo string) (*model.Order, error) {
	var order model.Order
	err := database.DB.Preload("Plan").Preload("User").Preload("UpgradeFromPlan").Where("order_no = ?", orderNo).First(&order).Error
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
	err := query.Preload("Plan").Preload("User").Preload("UpgradeFromPlan").Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
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
