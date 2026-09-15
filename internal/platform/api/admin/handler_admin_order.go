package admin

import (
	"strconv"

	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type AdminOrderHandler struct {
	paymentService *service.PaymentService
}

func NewAdminOrderHandler() *AdminOrderHandler {
	return &AdminOrderHandler{
		paymentService: service.NewPaymentService(),
	}
}

func (h *AdminOrderHandler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	status := c.Query("status")

	orders, total, err := h.paymentService.ListOrders(page, pageSize, status)
	if err != nil {
		response.Fail(c, 500, "获取订单列表失败: "+err.Error())
		return
	}

	response.SuccessPage(c, orders, total, page, pageSize)
}

func (h *AdminOrderHandler) FulfillOrder(c *gin.Context) {
	orderNo := c.Param("orderNo")
	if orderNo == "" {
		response.Fail(c, 400, "订单号不能为空")
		return
	}

	if err := h.paymentService.ManualFulfillOrder(orderNo); err != nil {
		response.Fail(c, 500, "手动补单失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "订单已成功补单并激活对应套餐权限"})
}
