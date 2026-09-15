package api

import (
	"strconv"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type KeyHandler struct {
	keyService *service.KeyService
}

func NewKeyHandler() *KeyHandler {
	return &KeyHandler{
		keyService: service.NewKeyService(),
	}
}

func (h *KeyHandler) ListKeys(c *gin.Context) {
	userID := c.GetUint("user_id")
	keys, err := h.keyService.ListKeys(userID)
	if err != nil {
		response.Fail(c, 500, "获取密钥列表失败: "+err.Error())
		return
	}
	response.Success(c, keys)
}

func (h *KeyHandler) CreateKey(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req struct {
		Name          string   `json:"name"`
		AllowedModels []string `json:"allowedModels"`
	}
	_ = c.ShouldBindJSON(&req)

	key, err := h.keyService.CreateKey(userID, req.Name, req.AllowedModels)
	if err != nil {
		response.Fail(c, 400, "创建密钥失败: "+err.Error())
		return
	}
	response.Success(c, key)
}

func (h *KeyHandler) DeleteKey(c *gin.Context) {
	userID := c.GetUint("user_id")
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		response.Fail(c, 400, "无效的密钥 ID")
		return
	}

	if err := h.keyService.DeleteKey(userID, uint(id)); err != nil {
		response.Fail(c, 500, "删除密钥失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "删除成功"})
}
