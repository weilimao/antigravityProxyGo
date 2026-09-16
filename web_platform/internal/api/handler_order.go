package api

import (
	"strconv"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type OrderHandler struct {
	orderService *service.OrderService
}

func NewOrderHandler() *OrderHandler {
	return &OrderHandler{
		orderService: service.NewOrderService(),
	}
}

// ListMyOrders 查询当前登录用户的订单列表
func (h *OrderHandler) ListMyOrders(c *gin.Context) {
	userID := c.GetUint("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	status := c.Query("status")

	orders, total, err := h.orderService.ListUserOrders(userID, page, pageSize, status)
	if err != nil {
		response.Fail(c, 500, "获取订单记录失败: "+err.Error())
		return
	}

	response.SuccessPage(c, orders, total, page, pageSize)
}

// GetMyOrderPayURL 针对未支付的订单重新获取前往收银台的跳转地址
func (h *OrderHandler) GetMyOrderPayURL(c *gin.Context) {
	userID := c.GetUint("user_id")
	orderNo := c.Param("orderNo")
	if orderNo == "" {
		response.Fail(c, 400, "缺少订单编号")
		return
	}

	payURL, err := h.orderService.GetUserOrderPayURL(userID, orderNo)
	if err != nil {
		response.Fail(c, 400, err.Error())
		return
	}

	response.Success(c, gin.H{
		"orderNo": orderNo,
		"payUrl":  payURL,
	})
}

// CancelMyOrder 用户主动取消未支付订单
func (h *OrderHandler) CancelMyOrder(c *gin.Context) {
	userID := c.GetUint("user_id")
	orderNo := c.Param("orderNo")
	if orderNo == "" {
		response.Fail(c, 400, "缺少订单编号")
		return
	}

	if err := h.orderService.CancelUserOrder(userID, orderNo); err != nil {
		response.Fail(c, 400, err.Error())
		return
	}

	response.Success(c, gin.H{
		"orderNo": orderNo,
		"status":  "cancelled",
		"message": "订单已成功取消",
	})
}
