package api

import (
	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

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

// CreateOrder 发起套餐订阅切单请求，生成极客工坊收银台链接
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
		"orderNo":     order.OrderNo,
		"amountCents": order.AmountCents,
		"status":      order.Status,
		"payUrl":      payURL,
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

	response.Success(c, gin.H{
		"orderNo":      order.OrderNo,
		"status":       order.Status,
		"amountCents":  order.AmountCents,
		"paidAt":       order.PaidAt,
		"planName":     order.Plan.Name,
		"durationDays": order.Plan.DurationDays,
	})
}
