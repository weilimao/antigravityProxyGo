package api

import (
	"net/http"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type SystemHandler struct {
	settingService *service.SettingService
}

func NewSystemHandler() *SystemHandler {
	return &SystemHandler{
		settingService: service.NewSettingService(),
	}
}

// GetPublicConfig 获取前台展示所需的系统公开配置（API 根地址等）
func (h *SystemHandler) GetPublicConfig(c *gin.Context) {
	cfg, err := h.settingService.GetSystemConfig()
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, "获取系统配置失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}
