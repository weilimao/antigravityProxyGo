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

	// 2. 初始化数据库连接与迁移
	if _, err := database.InitDB(cfg); err != nil {
		log.Fatalf("❌ Failed to init database: %v", err)
	}

	// 3. 构建路由
	router := api.SetupRouter()

	printBanner(cfg)

	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// 4. 协程启动 HTTP 服务
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ ListenAndServe failed: %v", err)
		}
	}()

	// 5. 监听终止信号实现优雅关机
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal [%v], shutting down Web Platform...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("⚠️ Server forced to shutdown: %v", err)
	}

	log.Println("Web Platform gracefully terminated.")
}
