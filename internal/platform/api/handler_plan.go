package api

import (
	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

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
