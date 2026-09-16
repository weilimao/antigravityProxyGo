package admin

import (
	"net/http"

	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminSystemHandler struct {
	settingService *service.SettingService
}

func NewAdminSystemHandler() *AdminSystemHandler {
	return &AdminSystemHandler{
		settingService: service.NewSettingService(),
	}
}

// GetSystemConfig 管理员获取系统全局配置（API 基准地址等）
func (h *AdminSystemHandler) GetSystemConfig(c *gin.Context) {
	cfg, err := h.settingService.GetSystemConfig()
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, "获取系统配置失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}

// SetSystemConfig 管理员更新系统全局配置
func (h *AdminSystemHandler) SetSystemConfig(c *gin.Context) {
	var req model.SystemConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, "请求参数格式错误: "+err.Error())
		return
	}

	if err := h.settingService.SetSystemConfig(&req); err != nil {
		response.Fail(c, http.StatusInternalServerError, "保存系统配置失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "系统配置保存成功",
		"config":  req,
	})
}
