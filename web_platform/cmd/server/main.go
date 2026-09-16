package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"antigravity-web-platform/internal/api"
	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/service"
)

const version = "v1.0.0"

func printBanner(cfg *config.Config) {
	banner := `
   ___         __  _                       _ __         _      __    __  
  / _ | ___   / /_(_)__ ________ __ _____ (_) /___ __  | | /| / /__ / /  
 / __ |/ _ \ / __/ / _ '/ __/ _ // // / / // __/ // /  | |/ |/ / -_) _ \ 
/_/ |_/_//_/ \__/_/\_, /_/  \_,_/\_,_/_//_//__/\_, /   |__/|__/\__/_.__/ 
                  /___/                        /___/                     
`
	fmt.Print(banner)
	fmt.Println("=========================================================================")
	fmt.Printf(" Antigravity Web Platform (%s)\n", version)
	fmt.Printf(" - Web Host : %s:%s\n", cfg.Server.Host, cfg.Server.Port)
	fmt.Printf(" - Database : %s (%s)\n", cfg.Database.Type, cfg.Database.DSN)
	if cfg.Redis.Enabled {
		fmt.Printf(" - Redis    : %s:%s (DB %d 会话隔离)\n", cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.DB)
	} else {
		fmt.Printf(" - Redis    : 已停用 (纯 JWT 模式)\n")
	}
	fmt.Printf(" - Payment  : GeekTools Relay (%s)\n", cfg.Payment.RelayURL)
	fmt.Printf(" - Gateway  : Go Relay Server (%s)\n", cfg.Gateway.GatewayURL)
	fmt.Printf(" - Health   : http://%s:%s/api/health\n", cfg.Server.Host, cfg.Server.Port)
	fmt.Println("=========================================================================")
}

func main() {
	var (
		configPath string
		portFlag   string
	)

	flag.StringVar(&configPath, "c", "config.yaml", "Path to configuration file")
	flag.StringVar(&configPath, "config", "config.yaml", "Path to configuration file")
	flag.StringVar(&portFlag, "p", "", "Server port override")
	flag.StringVar(&portFlag, "port", "", "Server port override")
	flag.Parse()

	// 1. 加载配置
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	if portFlag != "" {
		cfg.Server.Port = portFlag
	}

	// 优先打印启动横幅，提升交互体验
	printBanner(cfg)

	// 2. 初始化数据库连接与迁移
	log.Println("[1/3] 正在连接数据库并同步数据表结构...")
	if _, err := database.InitDB(cfg); err != nil {
		log.Fatalf("❌ Failed to init database: %v", err)
	}
	log.Println("[1/3] 数据库初始化成功！")

	// 3. 初始化远端 Redis 连接池 (DB 1)
	log.Println("[2/3] 正在连接远端 Redis 会话服务 (DB 1)...")
	if _, err := database.InitRedis(cfg); err != nil {
		log.Printf("⚠️ [Redis] 远端 Redis 连接失败: %v (将降级使用纯 JWT 本地验签模式)", err)
	} else {
		log.Println("[2/3] 远端 Redis 会话服务连接成功！")
	}

	// 4. 构建路由
	log.Println("[3/3] 正在注册 API 路由与启动监听...")
	router := api.SetupRouter()

	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// 4. 协程启动 HTTP 服务与订单超时监控
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	service.StartOrderExpiryWorker(workerCtx)

	go func() {
		log.Printf("🚀 后端 API 服务已就绪并开始监听: http://%s (健康检查: http://%s/api/health)\n", addr, addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ ListenAndServe failed: %v", err)
		}
	}()

	// 5. 监听终止信号实现优雅关机
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal [%v], shutting down Web Platform...", sig)
	workerCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("⚠️ Server forced to shutdown: %v", err)
	}

	log.Println("Web Platform gracefully terminated.")
}
