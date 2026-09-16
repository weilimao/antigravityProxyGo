package api

import (
	"strconv"
	"strings"

	"antigravity-proxy/internal/platform/pkg/response"
	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type LogHandler struct {
	bridge *service.RelayBridgeService
}

func NewLogHandler() *LogHandler {
	return &LogHandler{
		bridge: service.NewRelayBridgeService(),
	}
}

func (h *LogHandler) ListUserLogs(c *gin.Context) {
	username := c.GetString("username")
	if username == "" {
		response.Fail(c, 401, "请先登录")
		return
	}

	account := strings.TrimSpace(c.Query("account"))
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "15"))

	targetAccount := account
	if targetAccount == "" || targetAccount == "all" {
		role := c.GetString("role")
		if role != "admin" {
			targetAccount = username
		} else {
			targetAccount = ""
		}
	}

	res, err := h.bridge.FetchLogs(targetAccount, status, search, page, pageSize)
	if err != nil {
		response.Fail(c, 500, "获取请求日志失败: "+err.Error())
		return
	}

	response.Success(c, res)
}

func (h *LogHandler) GetUserLogDetail(c *gin.Context) {
	reqID := strings.TrimSpace(c.Query("req_id"))
	id := strings.TrimSpace(c.Query("id"))

	log, err := h.bridge.FetchLogDetail(reqID, id)
	if err != nil {
		response.Fail(c, 404, "获取日志详情失败: "+err.Error())
		return
	}

	response.Success(c, log)
}

func (h *LogHandler) GetUserLogAccounts(c *gin.Context) {
	accounts, err := h.bridge.FetchLogAccounts()
	if err != nil {
		response.Fail(c, 500, "获取日志账号列表失败: "+err.Error())
		return
	}

	response.Success(c, accounts)
}
