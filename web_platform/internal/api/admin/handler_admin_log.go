package admin

import (
	"strconv"
	"strings"

	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminLogHandler struct {
	bridge *service.RelayBridgeService
}

func NewAdminLogHandler() *AdminLogHandler {
	return &AdminLogHandler{
		bridge: service.NewRelayBridgeService(),
	}
}

func (h *AdminLogHandler) ListAdminLogs(c *gin.Context) {
	account := strings.TrimSpace(c.Query("account"))
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	targetAccount := account
	if targetAccount == "all" {
		targetAccount = ""
	}

	res, err := h.bridge.FetchLogs(targetAccount, status, search, page, pageSize)
	if err != nil {
		response.Fail(c, 500, "获取请求日志失败: "+err.Error())
		return
	}

	response.Success(c, res)
}

func (h *AdminLogHandler) GetAdminLogDetail(c *gin.Context) {
	reqID := strings.TrimSpace(c.Query("req_id"))
	id := strings.TrimSpace(c.Query("id"))

	log, err := h.bridge.FetchLogDetail(reqID, id)
	if err != nil {
		response.Fail(c, 404, "获取日志详情失败: "+err.Error())
		return
	}

	response.Success(c, log)
}

func (h *AdminLogHandler) GetLogAccounts(c *gin.Context) {
	accounts, err := h.bridge.FetchLogAccounts()
	if err != nil {
		response.Fail(c, 500, "获取日志账号列表失败: "+err.Error())
		return
	}

	response.Success(c, accounts)
}
