package admin

import (
	"strings"

	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type AdminOcrHandler struct {
	settingService *service.SettingService
}

func NewAdminOcrHandler() *AdminOcrHandler {
	return &AdminOcrHandler{
		settingService: service.NewSettingService(),
	}
}

func (h *AdminOcrHandler) GetOcrModel(c *gin.Context) {
	models, err := h.settingService.GetOCRModels()
	if err != nil {
		response.Fail(c, 500, "读取 OCR 候选模型列表失败: "+err.Error())
		return
	}
	primary, _ := h.settingService.GetOCRModel()
	response.Success(c, gin.H{
		"ocrModel":  primary,
		"ocrModels": models,
	})
}

func (h *AdminOcrHandler) PullOcrModel(c *gin.Context) {
	primary, models, err := h.settingService.PullOCRModelFromGateway()
	if err != nil {
		response.Fail(c, 500, "从远端服务端网关同步 OCR 模型失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{
		"message":   "成功从远端网关拉取最新 OCR 降级模型候选池",
		"ocrModel":  primary,
		"ocrModels": models,
	})
}

func (h *AdminOcrHandler) SetOcrModel(c *gin.Context) {
	var req struct {
		OcrModel  string   `json:"ocrModel"`
		OcrModels []string `json:"ocrModels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "请求体解析失败")
		return
	}

	targets := req.OcrModels
	if len(targets) == 0 && strings.TrimSpace(req.OcrModel) != "" {
		targets = []string{strings.TrimSpace(req.OcrModel)}
	}

	if err := h.settingService.SetOCRModels(targets); err != nil {
		response.Fail(c, 500, "保存 OCR 模型失败: "+err.Error())
		return
	}

	primary, _ := h.settingService.GetOCRModel()
	models, _ := h.settingService.GetOCRModels()

	response.Success(c, gin.H{
		"message":   "OCR 图片降级竞速模型候选池已更新并同步热推网关",
		"ocrModel":  primary,
		"ocrModels": models,
	})
}

