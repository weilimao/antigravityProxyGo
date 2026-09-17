package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/crypto"
)

// TestPaymentConfig_ReadWriteDefault 测试支付跳转配置的默认回退与落库持久化
func TestPaymentConfig_ReadWriteDefault(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()

	// 1. 默认回退读取
	cfg, err := settingSvc.GetPaymentConfig()
	if err != nil {
		t.Fatalf("get default payment config failed: %v", err)
	}
	if cfg.PayProvider != "epay" {
		t.Fatalf("expected default payProvider 'epay', got '%s'", cfg.PayProvider)
	}
	if cfg.EpayType != "alipay" {
		t.Fatalf("expected default epayType 'alipay', got '%s'", cfg.EpayType)
	}

	// 2. 自定义配置持久化
	newCfg := model.PaymentConfig{
		PayProvider:       "epay",
		SiteURL:           "http://mysite.com",
		PayReturnURL:      "http://mysite.com/#/orders",
		RelayURL:          "http://127.0.0.1:8080/api/v1/relay/create",
		RelayCheckoutBase: "http://crosslinkdev.online:8080",
		RelaySecret:       "relay_secret_test_999",
		RelayNotifyURL:    "http://mysite.com/api/v1/pay/notify/relay",
		EpayURL:           "https://epay.test.com",
		EpayPID:           "99001",
		EpayKey:           "epay_key_test_888",
		EpayType:          "wxpay",
		EpayNotifyURL:     "http://mysite.com/api/v1/pay/notify/epay",
	}

	if err := settingSvc.SetPaymentConfig(&newCfg); err != nil {
		t.Fatalf("save payment config failed: %v", err)
	}

	// 3. 再次读取验证断言
	saved, err := settingSvc.GetPaymentConfig()
	if err != nil {
		t.Fatalf("re-read payment config failed: %v", err)
	}
	if saved.EpayPID != "99001" || saved.EpayType != "wxpay" || saved.RelayCheckoutBase != "http://crosslinkdev.online:8080" {
		t.Fatalf("saved payment config mismatch: %+v", saved)
	}
}

// TestPaymentFlow_FakeMode 测试本地开发模拟沙箱模式
func TestPaymentFlow_FakeMode(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()
	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	// 启用 fake 模式
	_ = settingSvc.SetPaymentConfig(&model.PaymentConfig{
		PayProvider:  "fake",
		PayReturnURL: "http://localhost:6688/#/dashboard",
	})

	user, _, _ := authSvc.Register("fake_user", "fake@example.com", "pass123")
	plan := model.Plan{
		Name:         "沙箱测试套餐",
		PriceCents:   1000,
		DurationDays: 30,
		Status:       "active",
	}
	_ = planSvc.CreatePlan(&plan)

	order, payURL, err := paymentSvc.CreateRelayOrder(user.ID, plan.ID)
	if err != nil {
		t.Fatalf("create fake order failed: %v", err)
	}

	if payURL != "http://localhost:6688/#/dashboard" {
		t.Fatalf("expected payURL 'http://localhost:6688/#/dashboard', got '%s'", payURL)
	}

	// 验证订单已自动完成
	if order.Status != "paid" || order.PaidAt == nil {
		t.Fatalf("expected order to be paid immediately in fake mode")
	}

	// 验证用户套餐已激活
	profile, _ := authSvc.GetProfile(user.ID)
	if !profile.IsSubscriptionActive() {
		t.Fatalf("expected user subscription active in fake mode")
	}
}

// TestPaymentFlow_RelayMode 测试 B 站切单中继与基准收银台重写
func TestPaymentFlow_RelayMode(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	// 启动一个模拟 B 站切单服务
	secret := "relay_secret_test_555"
	mockRelayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)

		// 验证 HMAC 签名
		if !crypto.VerifyRelaySign(req, secret) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"order_no": "B_ORDER_9988",
			"pay_url":  "http://127.0.0.1:8080/#/checkout/B_ORDER_9988",
		})
	}))
	defer mockRelayServer.Close()

	settingSvc := NewSettingService()
	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	_ = settingSvc.SetPaymentConfig(&model.PaymentConfig{
		PayProvider:       "epay",
		RelayURL:          mockRelayServer.URL,
		RelaySecret:       secret,
		RelayCheckoutBase: "http://crosslinkdev.online:8080",
		PayReturnURL:      "http://localhost:6688/#/dashboard",
		RelayNotifyURL:    "http://localhost:8100/api/v1/pay/notify/relay",
	})

	user, _, _ := authSvc.Register("relay_user", "relay@example.com", "pass123")
	plan := model.Plan{
		Name:         "中继切单测试套餐",
		PriceCents:   2900,
		DurationDays: 30,
		Status:       "active",
	}
	_ = planSvc.CreatePlan(&plan)

	order, payURL, err := paymentSvc.CreateRelayOrder(user.ID, plan.ID)
	if err != nil {
		t.Fatalf("create relay order failed: %v", err)
	}

	expectedPrefix := "http://crosslinkdev.online:8080/checkout/B_ORDER_9988"
	if payURL != expectedPrefix {
		t.Fatalf("expected payURL '%s', got '%s'", expectedPrefix, payURL)
	}

	if order.RelayOrderNo != "B_ORDER_9988" {
		t.Fatalf("expected relay order no 'B_ORDER_9988', got '%s'", order.RelayOrderNo)
	}
	if order.Status != "pending" {
		t.Fatalf("expected order status 'pending', got '%s'", order.Status)
	}
}

// TestPaymentFlow_EpayDirectAndWebhook 测试易支付直连 submit.php 生成与 MD5 Webhook 回调履约
func TestPaymentFlow_EpayDirectAndWebhook(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()
	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	epayKey := "epay_md5_secret_key_888"
	_ = settingSvc.SetPaymentConfig(&model.PaymentConfig{
		PayProvider:   "epay",
		RelayURL:      "", // 留空触发易支付直连模式
		EpayURL:       "https://epay.open.cn",
		EpayPID:       "123456",
		EpayKey:       epayKey,
		EpayType:      "alipay",
		PayReturnURL:  "http://mysite.com/#/dashboard",
		EpayNotifyURL: "http://mysite.com/api/v1/pay/notify/epay",
	})

	user, _, _ := authSvc.Register("epay_user", "epay@example.com", "pass123")
	plan := model.Plan{
		Name:          "易支付直连套餐",
		PriceCents:    5900,
		DurationDays:  30,
		AllowedModels: []string{"claude-3-7-sonnet"},
		Status:        "active",
	}
	_ = planSvc.CreatePlan(&plan)

	// 1. 创建直连订单
	order, payURL, err := paymentSvc.CreateRelayOrder(user.ID, plan.ID)
	if err != nil {
		t.Fatalf("create epay direct order failed: %v", err)
	}

	// 验证生成的 URL 格式
	u, err := url.Parse(payURL)
	if err != nil {
		t.Fatalf("invalid generated payURL: %v", err)
	}
	if u.Scheme != "https" || u.Host != "epay.open.cn" || u.Path != "/submit.php" {
		t.Fatalf("payURL path mismatch: %s", payURL)
	}

	q := u.Query()
	if q.Get("pid") != "123456" || q.Get("money") != "59.00" || q.Get("out_trade_no") != order.OrderNo {
		t.Fatalf("query params mismatch: %+v", q)
	}

	// 验证参数中的 sign 签名有效性
	paramsMap := make(map[string]interface{})
	for k, v := range q {
		if len(v) > 0 {
			paramsMap[k] = v[0]
		}
	}
	if !crypto.VerifyEpaySign(paramsMap, epayKey) {
		t.Fatalf("expected generated epay MD5 sign to be valid")
	}

	// 2. 模拟易支付官方直连回调
	notifyParams := map[string]interface{}{
		"pid":          "123456",
		"type":         "alipay",
		"out_trade_no": order.OrderNo,
		"trade_no":     "EPAY_TXN_OFFICIAL_12345678",
		"trade_status": "TRADE_SUCCESS",
		"money":        "59.00",
		"name":         "Antigravity 会员订阅 - 易支付直连套餐",
	}
	notifyParams["sign"] = crypto.GenerateEpaySign(notifyParams, epayKey)
	notifyParams["sign_type"] = "MD5"

	if err := paymentSvc.HandleEpayWebhook(notifyParams); err != nil {
		t.Fatalf("handle epay webhook failed: %v", err)
	}

	// 3. 断言订单状态扭转为 paid
	updatedOrder, _ := paymentSvc.GetOrderByNo(order.OrderNo)
	if updatedOrder.Status != "paid" || updatedOrder.PaidAt == nil {
		t.Fatalf("expected order to be marked paid")
	}

	// 4. 断言用户套餐已激活生效
	buyer, _ := authSvc.GetProfile(user.ID)
	if !buyer.IsSubscriptionActive() {
		t.Fatalf("expected buyer subscription active after epay webhook")
	}
	if buyer.PlanID == nil || *buyer.PlanID != plan.ID {
		t.Fatalf("expected buyer planID to match")
	}

	// 5. 篡改签名断言拦截
	tamperedParams := map[string]interface{}{
		"out_trade_no": order.OrderNo,
		"trade_status": "TRADE_SUCCESS",
		"sign":         "invalid_fake_sign_xyz",
	}
	if err := paymentSvc.HandleEpayWebhook(tamperedParams); err == nil {
		t.Fatalf("expected tampered epay webhook to fail, but succeeded")
	}
}

// TestPaymentFlow_AmountTamperRejected_Relay 测试切单中继下金额篡改（如订单88元但回调0.01元）被拦截拒绝
func TestPaymentFlow_AmountTamperRejected_Relay(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	secret := "relay_secret_test_tamper_1"
	mockRelayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"order_no": "B_ORDER_TAMPER",
			"pay_url":  "http://127.0.0.1:8080/checkout/B_ORDER_TAMPER",
		})
	}))
	defer mockRelayServer.Close()

	settingSvc := NewSettingService()
	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	_ = settingSvc.SetPaymentConfig(&model.PaymentConfig{
		PayProvider:    "epay",
		RelayURL:       mockRelayServer.URL,
		RelaySecret:    secret,
		RelayNotifyURL: "http://localhost:8100/api/v1/pay/notify/relay",
	})

	user, _, _ := authSvc.Register("tamper_user", "tamper@example.com", "pass123")
	plan := model.Plan{
		Name:         "高端定制方案",
		PriceCents:   8800, // 88.00 元
		DurationDays: 30,
		Status:       "active",
	}
	_ = planSvc.CreatePlan(&plan)

	// 创建订单
	order, _, err := paymentSvc.CreateRelayOrder(user.ID, plan.ID)
	if err != nil {
		t.Fatalf("create relay order failed: %v", err)
	}

	// 攻击者伪造 0.01 元合法签名的中继回调
	tamperedParams := map[string]interface{}{
		"out_trade_no": order.OrderNo,
		"trade_no":     "RELAY_TXN_FAKE_001",
		"trade_status": "TRADE_SUCCESS",
		"money":        "0.01", // 恶意篡改为 1 分钱
	}
	tamperedParams["sign"] = crypto.GenerateRelaySign(tamperedParams, secret)

	err = paymentSvc.HandleRelayWebhook(tamperedParams)
	if err == nil {
		t.Fatalf("expected amount mismatch error, got nil")
	}

	// 验证订单状态仍为 pending
	dbOrder, _ := paymentSvc.GetOrderByNo(order.OrderNo)
	if dbOrder.Status != "pending" || dbOrder.PaidAt != nil {
		t.Fatalf("expected order status to remain pending, got %s", dbOrder.Status)
	}

	// 验证用户订阅并未被开通
	profile, _ := authSvc.GetProfile(user.ID)
	if profile.IsSubscriptionActive() {
		t.Fatalf("expected user subscription to remain inactive")
	}

	// 再次发送全额 88.00 元合法签名回调，验证可以正常开通
	validParams := map[string]interface{}{
		"out_trade_no": order.OrderNo,
		"trade_no":     "RELAY_TXN_REAL_001",
		"trade_status": "TRADE_SUCCESS",
		"money":        "88.00",
	}
	validParams["sign"] = crypto.GenerateRelaySign(validParams, secret)

	if err := paymentSvc.HandleRelayWebhook(validParams); err != nil {
		t.Fatalf("handle valid relay webhook failed: %v", err)
	}

	// 验证开通成功
	dbOrder2, _ := paymentSvc.GetOrderByNo(order.OrderNo)
	if dbOrder2.Status != "paid" {
		t.Fatalf("expected order to be paid after valid webhook")
	}

	// 验证重复回调幂等处理
	if err := paymentSvc.HandleRelayWebhook(validParams); err != nil {
		t.Fatalf("expected idempotent webhook call to succeed, got %v", err)
	}
}

// TestPaymentFlow_AmountTamperRejected_Epay 测试易支付直连模式下金额篡改被拦截
func TestPaymentFlow_AmountTamperRejected_Epay(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	epayKey := "epay_key_tamper_test_2"
	settingSvc := NewSettingService()
	authSvc := NewAuthService()
	planSvc := NewPlanService()
	paymentSvc := NewPaymentService()

	_ = settingSvc.SetPaymentConfig(&model.PaymentConfig{
		PayProvider:   "epay",
		EpayURL:       "https://epay.test.com",
		EpayPID:       "8888",
		EpayKey:       epayKey,
		EpayType:      "alipay",
		EpayNotifyURL: "http://mysite.com/api/v1/pay/notify/epay",
	})

	user, _, _ := authSvc.Register("epay_tamper", "epay_tamper@example.com", "pass123")
	plan := model.Plan{
		Name:         "标准套餐",
		PriceCents:   4500, // 45.00 元
		DurationDays: 30,
		Status:       "active",
	}
	_ = planSvc.CreatePlan(&plan)

	order, _, err := paymentSvc.CreateRelayOrder(user.ID, plan.ID)
	if err != nil {
		t.Fatalf("create order failed: %v", err)
	}

	// 攻击者篡改金额为 0.05 元并发起 MD5 签名
	tamperedParams := map[string]interface{}{
		"pid":          "8888",
		"type":         "alipay",
		"out_trade_no": order.OrderNo,
		"trade_no":     "EPAY_TXN_TAMPER_002",
		"trade_status": "TRADE_SUCCESS",
		"money":        "0.05",
	}
	tamperedParams["sign"] = crypto.GenerateEpaySign(tamperedParams, epayKey)

	err = paymentSvc.HandleEpayWebhook(tamperedParams)
	if err == nil {
		t.Fatalf("expected epay amount mismatch error, got nil")
	}

	// 验证订单未开通
	dbOrder, _ := paymentSvc.GetOrderByNo(order.OrderNo)
	if dbOrder.Status != "pending" {
		t.Fatalf("expected order to remain pending")
	}

	profile, _ := authSvc.GetProfile(user.ID)
	if profile.IsSubscriptionActive() {
		t.Fatalf("expected subscription to remain inactive")
	}
}
