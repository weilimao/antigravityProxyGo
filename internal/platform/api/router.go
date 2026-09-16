package api

import (
	"net/http"

	"antigravity-proxy/internal/platform/api/admin"
	"antigravity-proxy/internal/platform/api/middleware"

	"github.com/gin-gonic/gin"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()
	r.Use(middleware.Cors())

	// 健康检查
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"server": "antigravity-web-platform",
		})
	})

	authH := NewAuthHandler()
	planH := NewPlanHandler()
	checkoutH := NewCheckoutHandler()
	webhookH := NewWebhookHandler()
	keyH := NewKeyHandler()
	autoH := NewAutoHandler()
	logH := NewLogHandler()

	adminPlanH := admin.NewAdminPlanHandler()
	adminOrderH := admin.NewAdminOrderHandler()
	adminUserH := admin.NewAdminUserHandler()
	adminMappingH := admin.NewAdminMappingHandler()
	adminOcrH := admin.NewAdminOcrHandler()
	adminAutoH := admin.NewAdminAutoHandler()
	adminLogH := admin.NewAdminLogHandler()

	// 1. 公开端点
	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", authH.Register)
		v1.POST("/auth/login", authH.Login)
		v1.GET("/plans", planH.ListActivePlans)

		// 极客工坊支付中继异步回调
		v1.POST("/pay/notify/relay", webhookH.HandleRelayWebhook)
		v1.GET("/pay/notify/relay", webhookH.HandleRelayWebhook)
	}

	// 2. 普通登录用户端点
	userGroup := v1.Group("")
	userGroup.Use(middleware.AuthRequired())
	{
		userGroup.GET("/auth/me", authH.GetMe)
		userGroup.POST("/auth/password", authH.ChangePassword)

		// 订单与收银
		userGroup.POST("/checkout/create", checkoutH.CreateOrder)
		userGroup.GET("/checkout/orders/:orderNo", checkoutH.GetOrderStatus)

		// 密钥管理
		userGroup.GET("/keys", keyH.ListKeys)
		userGroup.POST("/keys", keyH.CreateKey)
		userGroup.DELETE("/keys/:id", keyH.DeleteKey)

		// 专属 Auto 竞速配置
		userGroup.GET("/user/auto-config", autoH.GetUserAutoConfig)
		userGroup.POST("/user/auto-config", autoH.SetUserAutoConfig)

		// 请求命中模型日志 (用户端按账号隔离/筛选)
		userGroup.GET("/user/logs", logH.ListUserLogs)
		userGroup.GET("/user/logs/detail", logH.GetUserLogDetail)
		userGroup.GET("/user/logs/accounts", logH.GetUserLogAccounts)
	}

	// 3. 管理后台端点
	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.AuthRequired(), middleware.AdminRequired())
	{
		// 套餐管理 (含 AllowedModels 白名单)
		adminGroup.GET("/plans", adminPlanH.ListPlans)
		adminGroup.POST("/plans", adminPlanH.CreatePlan)
		adminGroup.PUT("/plans/:id", adminPlanH.UpdatePlan)
		adminGroup.DELETE("/plans/:id", adminPlanH.DeletePlan)

		// 订单管理与手动补单
		adminGroup.GET("/orders", adminOrderH.ListOrders)
		adminGroup.POST("/orders/:orderNo/fulfill", adminOrderH.FulfillOrder)

		// 用户管理与套餐分配
		adminGroup.GET("/users", adminUserH.ListUsers)
		adminGroup.PUT("/users/:id/status", adminUserH.ToggleUserStatus)
		adminGroup.POST("/users/:id/assign-plan", adminUserH.AssignPlan)

		// 模型映射
		adminGroup.GET("/models/mappings", adminMappingH.GetMappings)
		adminGroup.POST("/models/mappings", adminMappingH.SetMappings)
		adminGroup.POST("/models/mappings/pull", adminMappingH.PullMappings)
		adminGroup.GET("/models/mapping-clients", adminMappingH.GetMappingClientModels)
		adminGroup.GET("/models/available", adminMappingH.GetAvailableModels)
		adminGroup.POST("/models/fetch-channel", adminMappingH.FetchChannelModels)
		adminGroup.POST("/models/fetch-other", adminMappingH.FetchOtherModels)

		// OCR 图像自愈降级模型配置
		adminGroup.GET("/settings/ocr", adminOcrH.GetOcrModel)
		adminGroup.POST("/settings/ocr", adminOcrH.SetOcrModel)
		adminGroup.POST("/settings/ocr/pull", adminOcrH.PullOcrModel)

		// Auto 并发竞速全局规则
		adminGroup.GET("/models/auto-config", adminAutoH.GetAutoConfig)
		adminGroup.POST("/models/auto-config", adminAutoH.SetAutoConfig)
		adminGroup.POST("/models/auto-config/pull", adminAutoH.PullAutoConfig)

		// 请求命中日志管理 (全量/按账号筛选)
		adminGroup.GET("/logs", adminLogH.ListAdminLogs)
		adminGroup.GET("/logs/detail", adminLogH.GetAdminLogDetail)
		adminGroup.GET("/logs/accounts", adminLogH.GetLogAccounts)
	}

	return r
}
