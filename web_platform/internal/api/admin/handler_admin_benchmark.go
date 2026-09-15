package admin

import (
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminBenchmarkHandler struct {
}

func NewAdminBenchmarkHandler() *AdminBenchmarkHandler {
	return &AdminBenchmarkHandler{}
}

func (h *AdminBenchmarkHandler) GetBenchmark(c *gin.Context) {
	res, err := service.GetGatewayBenchmark()
	if err != nil {
		response.Fail(c, 500, "获取测速数据失败: "+err.Error())
		return
	}
	response.Success(c, res)
}

func (h *AdminBenchmarkHandler) SetBenchmarkConfig(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数错误: "+err.Error())
		return
	}
	res, err := service.SaveGatewayBenchmarkConfig(req)
	if err != nil {
		response.Fail(c, 500, "保存配置失败: "+err.Error())
		return
	}
	response.Success(c, res)
}

func (h *AdminBenchmarkHandler) RunBenchmark(c *gin.Context) {
	res, err := service.RunGatewayBenchmark()
	if err != nil {
		response.Fail(c, 500, "触发测速失败: "+err.Error())
		return
	}
	response.Success(c, res)
}

func (h *AdminBenchmarkHandler) RunBenchmarkModel(c *gin.Context) {
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数错误: "+err.Error())
		return
	}
	res, err := service.RunGatewayBenchmarkModel(req.Model)
	if err != nil {
		response.Fail(c, 500, "触发单模型测速失败: "+err.Error())
		return
	}
	response.Success(c, res)
}

func (h *AdminBenchmarkHandler) GetBenchmarkModels(c *gin.Context) {
	res, err := service.GetGatewayBenchmarkModels()
	if err != nil {
		response.Fail(c, 500, "获取候选模型失败: "+err.Error())
		return
	}
	response.Success(c, res)
}
