package admin

import (
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminAccountHandler struct {
	accountService *service.AccountService
}

func NewAdminAccountHandler() *AdminAccountHandler {
	return &AdminAccountHandler{
		accountService: service.NewAccountService(),
	}
}

// GetAccounts 获取所有通道账号及全局号池配置
func (h *AdminAccountHandler) GetAccounts(c *gin.Context) {
	data, err := h.accountService.GetAccountsData()
	if err != nil {
		response.Fail(c, 500, "读取账号池数据失败: "+err.Error())
		return
	}
	response.Success(c, data)
}

// SavePoolConfig 更新号池全局调度算法与并发上限
func (h *AdminAccountHandler) SavePoolConfig(c *gin.Context) {
	var req model.PoolConfigDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	if err := h.accountService.SavePoolConfig(&req); err != nil {
		response.Fail(c, 500, "保存号池配置失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "号池调度与并发配置已更新",
	})
}

// AddAccount 添加新账号
func (h *AdminAccountHandler) AddAccount(c *gin.Context) {
	var req model.AddAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数验证失败: "+err.Error())
		return
	}

	acc, err := h.accountService.AddAccount(&req)
	if err != nil {
		response.Fail(c, 500, "添加账号失败: "+err.Error())
		return
	}

	response.Success(c, acc)
}

// UpdateAccount 编辑指定账号
func (h *AdminAccountHandler) UpdateAccount(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Fail(c, 400, "缺少账号 ID")
		return
	}

	var req model.UpdateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数验证失败: "+err.Error())
		return
	}

	acc, err := h.accountService.UpdateAccount(id, &req)
	if err != nil {
		response.Fail(c, 500, "更新账号失败: "+err.Error())
		return
	}

	response.Success(c, acc)
}

// ToggleAccount 快速启用或停用单个账号
func (h *AdminAccountHandler) ToggleAccount(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Fail(c, 400, "缺少账号 ID")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数验证失败: "+err.Error())
		return
	}

	if err := h.accountService.ToggleAccount(id, req.Enabled); err != nil {
		response.Fail(c, 500, "切换账号启停状态失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "账号状态已更新",
		"enabled": req.Enabled,
	})
}

// DeleteAccount 删除单个账号
func (h *AdminAccountHandler) DeleteAccount(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Fail(c, 400, "缺少账号 ID")
		return
	}

	if err := h.accountService.DeleteAccount(id); err != nil {
		response.Fail(c, 500, "删除账号失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "账号已成功移除",
	})
}

// BatchDeleteAccounts 批量删除账号
func (h *AdminAccountHandler) BatchDeleteAccounts(c *gin.Context) {
	var req model.BatchDeleteAccountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "参数格式错误: "+err.Error())
		return
	}

	count, err := h.accountService.BatchDeleteAccounts(req.IDs)
	if err != nil {
		response.Fail(c, 500, "批量删除账号失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "批量删除成功",
		"deleted": count,
	})
}

// ImportAccounts 批量导入账号
func (h *AdminAccountHandler) ImportAccounts(c *gin.Context) {
	var req model.ImportAccountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, "导入参数格式错误: "+err.Error())
		return
	}

	count, err := h.accountService.ImportAccounts(req.Accounts)
	if err != nil {
		response.Fail(c, 500, "导入账号失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message":  "导入成功",
		"imported": count,
	})
}

// ExportAccounts 导出账号配置
func (h *AdminAccountHandler) ExportAccounts(c *gin.Context) {
	channel := c.Query("channel")
	accounts, err := h.accountService.ExportAccounts(channel)
	if err != nil {
		response.Fail(c, 500, "导出账号失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"channel":  channel,
		"count":    len(accounts),
		"accounts": accounts,
	})
}
