package admin

import (
	"strconv"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminUserHandler struct {
	planService *service.PlanService
}

func NewAdminUserHandler() *AdminUserHandler {
	return &AdminUserHandler{
		planService: service.NewPlanService(),
	}
}

func (h *AdminUserHandler) ListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	search := c.Query("search")

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	var users []model.User
	var total int64
	query := database.DB.Model(&model.User{})

	if search != "" {
		like := "%" + search + "%"
		query = query.Where("username LIKE ? OR email LIKE ?", like, like)
	}

	query.Count(&total)
	err := query.Preload("Plan").Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	if err != nil {
		response.Fail(c, 500, "获取用户列表失败: "+err.Error())
		return
	}

	// 构造安全返回，屏蔽密码哈希
	type SafeUser struct {
		ID           uint        `json:"id"`
		Username     string      `json:"username"`
		Email        string      `json:"email"`
		Role         string      `json:"role"`
		Status       string      `json:"status"`
		PlanID       *uint       `json:"planId"`
		PlanExpireAt int64       `json:"planExpireAt"`
		IsActive     bool        `json:"isActive"`
		Plan         *model.Plan `json:"plan"`
	}

	safeUsers := make([]SafeUser, len(users))
	for i, u := range users {
		safeUsers[i] = SafeUser{
			ID:           u.ID,
			Username:     u.Username,
			Email:        u.Email,
			Role:         u.Role,
			Status:       u.Status,
			PlanID:       u.PlanID,
			PlanExpireAt: u.PlanExpireAt,
			IsActive:     u.IsSubscriptionActive(),
			Plan:         u.Plan,
		}
	}

	response.SuccessPage(c, safeUsers, total, page, pageSize)
}

func (h *AdminUserHandler) ToggleUserStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		response.Fail(c, 400, "无效的用户 ID")
		return
	}

	var user model.User
	if err := database.DB.First(&user, id).Error; err != nil {
		response.Fail(c, 404, "用户不存在")
		return
	}

	if user.Status == "active" {
		user.Status = "disabled"
	} else {
		user.Status = "active"
	}

	if err := database.DB.Save(&user).Error; err != nil {
		response.Fail(c, 500, "更新状态失败")
		return
	}

	response.Success(c, gin.H{"status": user.Status})
}

func (h *AdminUserHandler) AssignPlan(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		response.Fail(c, 400, "无效的用户 ID")
		return
	}

	var req struct {
		PlanID uint `json:"planId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "请指定要分配的套餐 ID")
		return
	}

	if err := h.planService.ActivatePlan(uint(id), req.PlanID); err != nil {
		response.Fail(c, 500, "分配套餐失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{"message": "套餐已成功赋予该用户"})
}
