package admin

import (
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminMappingHandler struct {
	settingService *service.SettingService
	syncService    *service.GatewaySyncService
}

func NewAdminMappingHandler() *AdminMappingHandler {
	return &AdminMappingHandler{
		settingService: service.NewSettingService(),
		syncService:    service.NewGatewaySyncService(),
	}
}

func (h *AdminMappingHandler) GetMappings(c *gin.Context) {
	mappings, err := h.settingService.GetModelMappings()
	if err != nil {
		response.Fail(c, 500, "读取模型映射失败: "+err.Error())
		return
	}

	// 若本地为空，尝试向现网 Go Relay 服务端网关拉取一次
	if len(mappings) == 0 {
		if remoteMappings, pullErr := h.settingService.PullModelMappingsFromGateway(); pullErr == nil && len(remoteMappings) > 0 {
			mappings = remoteMappings
		}
	}

	response.Success(c, mappings)
}

func (h *AdminMappingHandler) PullMappings(c *gin.Context) {
	mappings, err := h.settingService.PullModelMappingsFromGateway()
	if err != nil {
		response.Fail(c, 500, "从远端服务端网关同步模型映射失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{
		"message":  "成功从远端网关拉取最新模型映射配置",
		"mappings": mappings,
		"count":    len(mappings),
	})
}

func (h *AdminMappingHandler) SetMappings(c *gin.Context) {
	var req struct {
		Mappings []model.ModelMappingEntry `json:"mappings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.settingService.SetModelMappings(req.Mappings); err != nil {
		response.Fail(c, 500, "保存模型映射失败: "+err.Error())
		return
	}

	// 异步向现网 Go Relay 网关热下发
	_ = h.syncService.SyncModelMappingsToGateway(req.Mappings)

	response.Success(c, gin.H{
		"message": "模型映射已保存并同步",
		"count":   len(req.Mappings),
	})
}

func (h *AdminMappingHandler) GetAvailableModels(c *gin.Context) {
	models, err := h.settingService.GetAvailableModels()
	if err != nil {
		response.Fail(c, 500, "获取可用模型列表失败: "+err.Error())
		return
	}
	response.Success(c, models)
}

func (h *AdminMappingHandler) GetMappingClientModels(c *gin.Context) {
	models, err := h.settingService.GetMappingClientModels()
	if err != nil {
		response.Fail(c, 500, "获取映射对外模型列表失败: "+err.Error())
		return
	}
	response.Success(c, models)
}

func (h *AdminMappingHandler) GetOtherGroups(c *gin.Context) {
	res, err := h.syncService.GetGatewayOtherGroups()
	if err != nil {
		response.Fail(c, 500, "获取其他组失败: "+err.Error())
		return
	}
	// res 已经是 map[string]interface{}
	c.JSON(200, res)
}

func (h *AdminMappingHandler) FetchChannelModels(c *gin.Context) {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}
	res, err := h.syncService.FetchGatewayChannelModels(req.Channel)
	if err != nil {
		response.Fail(c, 500, "获取模型快照失败: "+err.Error())
		return
	}
	c.JSON(200, res)
}

func (h *AdminMappingHandler) FetchOtherModels(c *gin.Context) {
	var req struct {
		GroupId string `json:"groupId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}
	res, err := h.syncService.FetchGatewayOtherGroupModels(req.GroupId)
	if err != nil {
		response.Fail(c, 500, "获取其他组模型快照失败: "+err.Error())
		return
	}
	c.JSON(200, res)
}

