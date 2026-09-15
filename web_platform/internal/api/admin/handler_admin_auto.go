package admin

import (
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminAutoHandler struct {
	settingService *service.SettingService
}

func NewAdminAutoHandler() *AdminAutoHandler {
	return &AdminAutoHandler{
		settingService: service.NewSettingService(),
	}
}

func (h *AdminAutoHandler) GetAutoConfig(c *gin.Context) {
	cfg, err := h.settingService.GetAutoRacingConfig()
	if err != nil {
		response.Fail(c, 500, "读取 Auto 竞速配置失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}

func (h *AdminAutoHandler) PullAutoConfig(c *gin.Context) {
	cfg, err := h.settingService.PullAutoConfigFromGateway()
	if err != nil {
		response.Fail(c, 500, "从 Go Relay 服务端网关拉取失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}

func (h *AdminAutoHandler) SetAutoConfig(c *gin.Context) {
	var req model.AutoRacingConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.settingService.SetAutoRacingConfig(&req); err != nil {
		response.Fail(c, 500, "保存 Auto 竞速配置失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "全局 Auto 并发竞速默认规则已更新并同步至网关",
	})
}
