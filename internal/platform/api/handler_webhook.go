package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"antigravity-proxy/internal/platform/service"

	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	paymentService *service.PaymentService
}

func NewWebhookHandler() *WebhookHandler {
	return &WebhookHandler{
		paymentService: service.NewPaymentService(),
	}
}

// HandleRelayWebhook 处理极客工坊支付回调通知 (POST /api/v1/pay/notify/relay)
func (h *WebhookHandler) HandleRelayWebhook(c *gin.Context) {
	params := make(map[string]interface{})

	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "application/json") {
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err == nil && len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &params)
		}
	} else {
		_ = c.Request.ParseForm()
		for k, v := range c.Request.Form {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
	}

	// 若 Query 中有参数也一并并入
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 && params[k] == nil {
			params[k] = v[0]
		}
	}

	if err := h.paymentService.HandleRelayWebhook(params); err != nil {
		c.String(http.StatusBadRequest, "fail: "+err.Error())
		return
	}

	// 极客工坊与易支付协议要求返回纯文本 "success"
	c.String(http.StatusOK, "success")
}
