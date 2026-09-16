package service

import (
	"testing"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
)

func TestOrderAutoExpiry_15Minutes(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	orderSvc := NewOrderService()
	db := database.DB

	// 1. 插入一个 16 分钟前的待支付订单（已超时）
	oldTime := time.Now().Add(-16 * time.Minute)
	expiredOrder := model.Order{
		OrderNo:     "ORD_EXPIRED_001",
		UserID:      1,
		PlanID:      1,
		AmountCents: 9900,
		Status:      "pending",
		CreatedAt:   oldTime,
		UpdatedAt:   oldTime,
	}
	if err := db.Create(&expiredOrder).Error; err != nil {
		t.Fatalf("创建超时订单失败: %v", err)
	}

	// 2. 插入一个 5 分钟前的待支付订单（未超时）
	freshTime := time.Now().Add(-5 * time.Minute)
	freshOrder := model.Order{
		OrderNo:     "ORD_FRESH_002",
		UserID:      1,
		PlanID:      1,
		AmountCents: 9900,
		Status:      "pending",
		CreatedAt:   freshTime,
		UpdatedAt:   freshTime,
	}
	if err := db.Create(&freshOrder).Error; err != nil {
		t.Fatalf("创建正常订单失败: %v", err)
	}

	// 3. 执行批量取消超时订单
	affected, err := orderSvc.CancelExpiredOrders()
	if err != nil {
		t.Fatalf("CancelExpiredOrders 发生错误: %v", err)
	}
	if affected != 1 {
		t.Errorf("预期取消 1 笔超时订单，实际取消了 %d 笔", affected)
	}

	// 4. 验证数据库中的状态
	var checkExpired, checkFresh model.Order
	db.First(&checkExpired, expiredOrder.ID)
	db.First(&checkFresh, freshOrder.ID)

	if checkExpired.Status != "cancelled" {
		t.Errorf("超时订单预期状态为 cancelled，实际为: %s", checkExpired.Status)
	}
	if checkFresh.Status != "pending" {
		t.Errorf("未超时订单预期状态为 pending，实际为: %s", checkFresh.Status)
	}
}

func TestUserCancelOrder(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	orderSvc := NewOrderService()
	db := database.DB

	// 创建用户待支付订单
	order := model.Order{
		OrderNo:     "ORD_CANCEL_001",
		UserID:      10,
		PlanID:      1,
		AmountCents: 5000,
		Status:      "pending",
		CreatedAt:   time.Now(),
	}
	db.Create(&order)

	// 1. 用户取消自己的订单
	err := orderSvc.CancelUserOrder(10, order.OrderNo)
	if err != nil {
		t.Fatalf("用户取消订单失败: %v", err)
	}

	var updated model.Order
	db.First(&updated, order.ID)
	if updated.Status != "cancelled" {
		t.Fatalf("订单取消后状态不为 cancelled: %s", updated.Status)
	}

	// 2. 再次尝试取消已取消的订单应报错
	err = orderSvc.CancelUserOrder(10, order.OrderNo)
	if err == nil {
		t.Fatalf("重复取消已关闭的订单预期报错，但未报错")
	}

	// 3. 非本用户尝试取消应报错
	err = orderSvc.CancelUserOrder(99, order.OrderNo)
	if err == nil {
		t.Fatalf("越权取消非自己订单预期报错，但未报错")
	}
}

func TestGetUserOrderPayURL_ExpiredCheck(t *testing.T) {
	teardown := setupTestEnvironment(t)
	defer teardown()

	orderSvc := NewOrderService()
	db := database.DB

	// 创建已超时的订单并附带支付链接
	expiredTime := time.Now().Add(-20 * time.Minute)
	order := model.Order{
		OrderNo:     "ORD_PAY_EXP_001",
		UserID:      20,
		PlanID:      1,
		AmountCents: 6600,
		Status:      "pending",
		PayURL:      "https://pay.example.com/checkout/123",
		CreatedAt:   expiredTime,
	}
	db.Create(&order)

	// 尝试去支付超时订单，预期拦截并自动取消
	url, err := orderSvc.GetUserOrderPayURL(20, order.OrderNo)
	if err == nil {
		t.Fatalf("对已超时的订单获取支付链接预期失败，但返回了链接: %s", url)
	}

	var updated model.Order
	db.First(&updated, order.ID)
	if updated.Status != "cancelled" {
		t.Fatalf("超时订单在获取链接时未被就地置为 cancelled，当前状态为: %s", updated.Status)
	}
}
