package api

import (
	"fmt"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type PlanHandler struct {
	planService *service.PlanService
}

func NewPlanHandler() *PlanHandler {
	return &PlanHandler{
		planService: service.NewPlanService(),
	}
}

// ListActivePlans 用户端获取所有已上架套餐列表
func (h *PlanHandler) ListActivePlans(c *gin.Context) {
	plans, err := h.planService.ListActivePlans()
	if err != nil {
		response.Fail(c, 500, "获取套餐列表失败: "+err.Error())
		return
	}
	response.Success(c, plans)
}

// GetPlanDetail 获取单个套餐详情（用于订单确认页展示）
func (h *PlanHandler) GetPlanDetail(c *gin.Context) {
	idStr := c.Param("id")
	var id uint
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id == 0 {
		response.Fail(c, 400, "套餐 ID 不合法")
		return
	}

	plan, err := h.planService.GetPlanByID(id)
	if err != nil {
		response.Fail(c, 404, "套餐不存在或已下架")
		return
	}
	response.Success(c, plan)
}

