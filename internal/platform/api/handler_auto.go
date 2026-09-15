package api

import (
	"antigravity-proxy/internal/platform/model"
	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type AutoHandler struct {
	settingService *service.SettingService
}

func NewAutoHandler() *AutoHandler {
	return &AutoHandler{
		settingService: service.NewSettingService(),
	}
}

// GetUserAutoConfig 获取当前登录用户专属的 Auto 竞速配置
func (h *AutoHandler) GetUserAutoConfig(c *gin.Context) {
	userID := c.GetUint("user_id")
	cfg, err := h.settingService.GetUserAutoConfig(userID)
	if err != nil {
		response.Fail(c, 500, "读取 Auto 竞速配置失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}

// SetUserAutoConfig 保存当前登录用户的私有 Auto 竞速配置
func (h *AutoHandler) SetUserAutoConfig(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req model.AutoRacingConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.settingService.SetUserAutoConfig(userID, &req); err != nil {
		response.Fail(c, 500, "保存配置失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "专属 Auto 竞速配置已保存"})
}
