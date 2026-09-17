package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"antigravity-web-platform/internal/api/admin"
	"antigravity-web-platform/internal/api/middleware"

	"github.com/gin-gonic/gin"
)

func SetupRouter(distDir ...string) *gin.Engine {
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
	orderH := NewOrderHandler()
	webhookH := NewWebhookHandler()
	keyH := NewKeyHandler()
	autoH := NewAutoHandler()
	systemH := NewSystemHandler()
	logH := NewLogHandler()

	adminPlanH := admin.NewAdminPlanHandler()
	adminOrderH := admin.NewAdminOrderHandler()
	adminUserH := admin.NewAdminUserHandler()
	adminMappingH := admin.NewAdminMappingHandler()
	adminOcrH := admin.NewAdminOcrHandler()
	adminAutoH := admin.NewAdminAutoHandler()
	adminBenchmarkH := admin.NewAdminBenchmarkHandler()
	adminAccountH := admin.NewAdminAccountHandler()
	adminPaymentH := admin.NewAdminPaymentHandler()
	adminSystemH := admin.NewAdminSystemHandler()
	adminLogH := admin.NewAdminLogHandler()

	// 1. 公开端点
	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", authH.Register)
		v1.POST("/auth/login", authH.Login)
		v1.POST("/auth/logout", authH.Logout)
		v1.GET("/plans", planH.ListActivePlans)
		v1.GET("/plans/:id", planH.GetPlanDetail)
		v1.GET("/system/config", systemH.GetPublicConfig)

		// 极客工坊支付中继异步回调
		v1.POST("/pay/notify/relay", webhookH.HandleRelayWebhook)
		v1.GET("/pay/notify/relay", webhookH.HandleRelayWebhook)

		// 易支付官方直连异步回调
		v1.POST("/pay/notify/epay", webhookH.HandleEpayWebhook)
		v1.GET("/pay/notify/epay", webhookH.HandleEpayWebhook)
	}

	// 2. 普通登录用户端点
	userGroup := v1.Group("")
	userGroup.Use(middleware.AuthRequired())
	{
		userGroup.GET("/auth/me", authH.GetMe)
		userGroup.POST("/auth/password", authH.ChangePassword)

		// 订单与收银
		userGroup.GET("/checkout/quote", checkoutH.GetUpgradeQuote)
		userGroup.POST("/checkout/create", checkoutH.CreateOrder)
		userGroup.GET("/checkout/orders/:orderNo", checkoutH.GetOrderStatus)

		// 用户端订单管理 (对标 ProxySubForClash)
		userGroup.GET("/user/orders", orderH.ListMyOrders)
		userGroup.GET("/user/orders/:orderNo/pay-url", orderH.GetMyOrderPayURL)
		userGroup.POST("/user/orders/:orderNo/cancel", orderH.CancelMyOrder)

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
		adminGroup.GET("/models/other-groups", adminMappingH.GetOtherGroups)
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

		// 竞速测试 (Benchmark)
		adminGroup.GET("/benchmark", adminBenchmarkH.GetBenchmark)
		adminGroup.POST("/benchmark/config", adminBenchmarkH.SetBenchmarkConfig)
		adminGroup.POST("/benchmark/run", adminBenchmarkH.RunBenchmark)
		adminGroup.POST("/benchmark/run-model", adminBenchmarkH.RunBenchmarkModel)
		adminGroup.GET("/benchmark/models", adminBenchmarkH.GetBenchmarkModels)

		// 账号池管理
		adminGroup.GET("/accounts", adminAccountH.GetAccounts)
		adminGroup.POST("/accounts", adminAccountH.AddAccount)
		adminGroup.PUT("/accounts/:id", adminAccountH.UpdateAccount)
		adminGroup.DELETE("/accounts/:id", adminAccountH.DeleteAccount)
		adminGroup.PUT("/accounts/:id/toggle", adminAccountH.ToggleAccount)
		adminGroup.POST("/accounts/batch-delete", adminAccountH.BatchDeleteAccounts)
		adminGroup.POST("/accounts/pool-config", adminAccountH.SavePoolConfig)
		adminGroup.POST("/accounts/import", adminAccountH.ImportAccounts)
		adminGroup.GET("/accounts/export", adminAccountH.ExportAccounts)

		// 支付跳转配置
		adminGroup.GET("/settings/payment", adminPaymentH.GetPaymentConfig)
		adminGroup.POST("/settings/payment", adminPaymentH.SetPaymentConfig)

		// 系统全局与对外 API 配置
		adminGroup.GET("/settings/system", adminSystemH.GetSystemConfig)
		adminGroup.POST("/settings/system", adminSystemH.SetSystemConfig)

		// 请求命中日志管理 (全量/按账号筛选)
		adminGroup.GET("/logs", adminLogH.ListAdminLogs)
		adminGroup.GET("/logs/detail", adminLogH.GetAdminLogDetail)
		adminGroup.GET("/logs/accounts", adminLogH.GetLogAccounts)
	}

	// 静态文件与 SPA 路由支持 (仅当 distDir 指定且 index.html 存在时挂载)
	var staticPath string
	if len(distDir) > 0 && distDir[0] != "" {
		staticPath = distDir[0]
	} else {
		staticPath = "dist"
	}

	indexPath := filepath.Join(staticPath, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		// 托管 assets 等静态子目录
		assetsDir := filepath.Join(staticPath, "assets")
		if aInfo, aErr := os.Stat(assetsDir); aErr == nil && aInfo.IsDir() {
			r.Static("/assets", assetsDir)
		}

		// 根目录静态文件与 SPA 回退处理
		r.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "api route not found"})
				return
			}
			targetFile := filepath.Join(staticPath, filepath.Clean(path))
			if fInfo, fErr := os.Stat(targetFile); fErr == nil && !fInfo.IsDir() {
				c.File(targetFile)
				return
			}
			// SPA 前端路由回退 (强行禁用 HTML 缓存，保证发版后客户端立即拉取最新入口与JS/CSS)
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
			c.File(indexPath)
		})
	}

	return r
}
