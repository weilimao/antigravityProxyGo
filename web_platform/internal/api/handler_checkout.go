package api

import (
	"strconv"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type CheckoutHandler struct {
	paymentService *service.PaymentService
}

func NewCheckoutHandler() *CheckoutHandler {
	return &CheckoutHandler{
		paymentService: service.NewPaymentService(),
	}
}

// GetUpgradeQuote 获取升级至指定套餐的折算抵扣明细与应付总额
func (h *CheckoutHandler) GetUpgradeQuote(c *gin.Context) {
	userID := c.GetUint("user_id")
	planIDStr := c.Query("planId")
	if planIDStr == "" {
		response.Fail(c, 400, "缺少 planId 参数")
		return
	}

	planIDVal, err := strconv.ParseUint(planIDStr, 10, 64)
	if err != nil || planIDVal == 0 {
		response.Fail(c, 400, "无效的 planId 参数")
		return
	}

	quote, err := h.paymentService.CalculateUpgradeQuote(userID, uint(planIDVal))
	if err != nil {
		response.Fail(c, 400, err.Error())
		return
	}

	response.Success(c, quote)
}

// CreateOrder 发起套餐订阅切单请求，生成极客工坊收银台链接（支持折算升级抵扣）
func (h *CheckoutHandler) CreateOrder(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req struct {
		PlanID uint `json:"planId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "请选择需要订阅的套餐")
		return
	}

	order, payURL, err := h.paymentService.CreateRelayOrder(userID, req.PlanID)
	if err != nil {
		response.Fail(c, 500, err.Error())
		return
	}

	response.Success(c, gin.H{
		"orderNo":             order.OrderNo,
		"amountCents":         order.AmountCents,
		"originalAmountCents": order.OriginalAmountCents,
		"discountCents":       order.DiscountCents,
		"upgradeFromPlanId":   order.UpgradeFromPlanID,
		"status":              order.Status,
		"payUrl":              payURL,
	})
}

// GetOrderStatus 查询指定订单状态
func (h *CheckoutHandler) GetOrderStatus(c *gin.Context) {
	orderNo := c.Param("orderNo")
	if orderNo == "" {
		response.Fail(c, 400, "缺少订单编号")
		return
	}

	order, err := h.paymentService.GetOrderByNo(orderNo)
	if err != nil {
		response.Fail(c, 404, "订单未找到")
		return
	}

	var upgradeFromName string
	if order.UpgradeFromPlan != nil {
		upgradeFromName = order.UpgradeFromPlan.Name
	}

	var planName string
	var durationDays int
	if order.Plan != nil {
		planName = order.Plan.Name
		durationDays = order.Plan.DurationDays
	}

	response.Success(c, gin.H{
		"orderNo":             order.OrderNo,
		"status":              order.Status,
		"amountCents":         order.AmountCents,
		"originalAmountCents": order.OriginalAmountCents,
		"discountCents":       order.DiscountCents,
		"upgradeFromPlanId":   order.UpgradeFromPlanID,
		"upgradeFromPlanName": upgradeFromName,
		"paidAt":              order.PaidAt,
		"planName":            planName,
		"durationDays":        durationDays,
	})
}
