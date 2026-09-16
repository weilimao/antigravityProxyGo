package admin

import (
	"net/http"

	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminPaymentHandler struct {
	settingService *service.SettingService
}

func NewAdminPaymentHandler() *AdminPaymentHandler {
	return &AdminPaymentHandler{
		settingService: service.NewSettingService(),
	}
}

// GetPaymentConfig 获取当前支付跳转核心配置
func (h *AdminPaymentHandler) GetPaymentConfig(c *gin.Context) {
	cfg, err := h.settingService.GetPaymentConfig()
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, "获取支付配置失败: "+err.Error())
		return
	}
	response.Success(c, cfg)
}

// SetPaymentConfig 保存支付跳转核心配置
func (h *AdminPaymentHandler) SetPaymentConfig(c *gin.Context) {
	var req model.PaymentConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, "请求参数格式错误: "+err.Error())
		return
	}

	if err := h.settingService.SetPaymentConfig(&req); err != nil {
		response.Fail(c, http.StatusInternalServerError, "保存支付配置失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "支付跳转配置保存成功",
		"config":  req,
	})
}
