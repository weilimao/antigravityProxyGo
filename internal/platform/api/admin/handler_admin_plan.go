package admin

import (
	"strconv"

	"antigravity-proxy/internal/platform/model"
	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type AdminPlanHandler struct {
	planService *service.PlanService
}

func NewAdminPlanHandler() *AdminPlanHandler {
	return &AdminPlanHandler{
		planService: service.NewPlanService(),
	}
}

func (h *AdminPlanHandler) ListPlans(c *gin.Context) {
	plans, err := h.planService.ListAllPlans()
	if err != nil {
		response.Fail(c, 500, "获取套餐列表失败: "+err.Error())
		return
	}
	response.Success(c, plans)
}

func (h *AdminPlanHandler) CreatePlan(c *gin.Context) {
	var plan model.Plan
	if err := c.ShouldBindJSON(&plan); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.planService.CreatePlan(&plan); err != nil {
		response.Fail(c, 500, "创建套餐失败: "+err.Error())
		return
	}
	response.Success(c, plan)
}

func (h *AdminPlanHandler) UpdatePlan(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		response.Fail(c, 400, "无效的套餐 ID")
		return
	}

	var plan model.Plan
	if err := c.ShouldBindJSON(&plan); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.planService.UpdatePlan(uint(id), &plan); err != nil {
		response.Fail(c, 500, "更新套餐失败: "+err.Error())
		return
	}
	response.Success(c, plan)
}

func (h *AdminPlanHandler) DeletePlan(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		response.Fail(c, 400, "无效的套餐 ID")
		return
	}

	if err := h.planService.DeletePlan(uint(id)); err != nil {
		response.Fail(c, 500, "删除套餐失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "套餐已删除"})
}
