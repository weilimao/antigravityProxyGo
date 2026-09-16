package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/benchmark"
	"antigravity-proxy/internal/db"
	platformdb "antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/quota"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

// ServerConfig 独立服务器启动配置
type ServerConfig struct {
	Host        string
	Port        string
	DataDir     string
	EnableProxy bool
	ProxyPort   string
}

// ServerInstance 服务端全局运行时容器
type ServerInstance struct {
	Config             ServerConfig
	SettingsMgr        *settings.Manager
	PricingMgr         *pricing.Manager
	StatsTracker       *stats.Tracker
	UsageTracker       *stats.UsageTracker
	ErrLogger          *stats.RetryErrorLogger
	AccountMgr         *account.Manager
	SessionRouter      *session.Router
	AuthMgr            *quota.AuthManager
	QuotaSvc           *quota.QuotaService
	PacketCap          *stats.PacketCapturer
	ProxyEngine        *proxy.ProxyEngine
	RelayUserMgr       *relay.UserManager
	RelayPackageMgr    *relay.PackageManager
	RelayAuthMgr       *relay.AuthManager
	RelayStatsMgr      *relay.StatsTracker
	RelayAPIMgr        *relay.APIHandler
	RelayCompatAPIMgr  *relay.APICompatHandler
	RelayServer        *relay.RelayServer
	BenchmarkScheduler *benchmark.Scheduler

	logFn     func(string)
	isRunning bool
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// ResolveDefaultDataDir 根据操作系统确定服务端默认数据目录
func ResolveDefaultDataDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	switch runtime.GOOS {
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "antigravity-proxy-desktop")
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "antigravity-proxy-desktop")
	default:
		// Linux 及其他 Unix 系统遵循 XDG 规范，存储于 ~/.config/antigravity-proxy
		return filepath.Join(homeDir, ".config", "antigravity-proxy")
	}
}

// NewServerInstance 初始化并装配独立服务器实例
func NewServerInstance(cfg ServerConfig, logFn func(string)) (*ServerInstance, error) {
	if logFn == nil {
		logFn = func(msg string) {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
		}
	}

	if cfg.DataDir == "" {
		cfg.DataDir = ResolveDefaultDataDir()
	}

	// 确保目标数据目录存在
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst := &ServerInstance{
		Config: cfg,
		logFn:  logFn,
		ctx:    ctx,
		cancel: cancel,
	}

	// 1. 初始化设置管理器
	inst.SettingsMgr = settings.NewManager()
	_, _ = settings.EnsureConfigExists(cfg.DataDir)
	inst.SettingsMgr.Init(cfg.DataDir)

	// 端口覆盖与确认
	if cfg.Port != "" {
		_ = inst.SettingsMgr.SetRelayPort(cfg.Port)
	} else {
		port := inst.SettingsMgr.GetRelayPort()
		if port == "" {
			port = "18444"
			_ = inst.SettingsMgr.SetRelayPort(port)
		}
		inst.Config.Port = port
	}

	// 2. 初始化 SQLite 数据库
	if err := db.InitDB(cfg.DataDir); err != nil {
		logFn(fmt.Sprintf("⚠️ [DB] SQLite 初始化失败: %v", err))
		return nil, fmt.Errorf("db initialization failed: %w", err)
	}

	// 启动时异步清理超出 150 条的旧请求日志
	go func() {
		if err := db.PruneAllUsersRequestLogs(150); err != nil {
			logFn(fmt.Sprintf("⚠️ [DB] 修剪旧请求日志失败: %v", err))
		}
	}()

	// 初始化业务数据库 (支持远程 MySQL 模式与本地 SQLite 双模自动探测)
	mysqlEnabled := inst.SettingsMgr.GetPlatformMySQLMode() ||
		inst.SettingsMgr.GetRemoteEnabled() ||
		os.Getenv("ANTIGRAVITY_MYSQL_ENABLED") == "true" ||
		os.Getenv("ANTIGRAVITY_MYSQL_DSN") != ""

	remoteHost := inst.SettingsMgr.GetRemoteHost()
	if remoteHost == "" {
		remoteHost = os.Getenv("ANTIGRAVITY_MYSQL_HOST")
	}
	remotePort := inst.SettingsMgr.GetRemotePort()
	if remotePort == "" {
		remotePort = os.Getenv("ANTIGRAVITY_MYSQL_PORT")
	}

	if errPDB := platformdb.InitDB(platformdb.Config{
		DataDir:        cfg.DataDir,
		RemoteEnabled:  mysqlEnabled,
		RemoteHost:     remoteHost,
		RemotePort:     remotePort,
		RemoteUser:     "root",
		RemotePassword: "ProxySub2026SecDbPass99",
		RemoteDBName:   "antigravity_platform",
	}); errPDB != nil {
		logFn(fmt.Sprintf("ℹ️ [PlatformDB] 初始化提示: %v", errPDB))
	} else {
		logFn("✅ [PlatformDB] 平台业务数据库初始化就绪")
	}

	// 3. 初始化计费与统计管理器
	inst.PricingMgr = pricing.NewManager()
	inst.PricingMgr.Init(cfg.DataDir)

	inst.StatsTracker = stats.NewTracker(inst.PricingMgr)
	inst.StatsTracker.Init(cfg.DataDir)

	inst.UsageTracker = stats.NewUsageTracker(inst.PricingMgr)
	inst.UsageTracker.Init(cfg.DataDir)

	inst.ErrLogger = stats.NewRetryErrorLogger()
	inst.ErrLogger.Init(cfg.DataDir)

	// 4. 初始化账号池与会话路由
	inst.AccountMgr = account.NewManager()
	inst.SessionRouter = session.NewRouter()

	inst.AuthMgr = quota.NewAuthManager(inst.AccountMgr)
	inst.QuotaSvc = quota.NewQuotaService()
	inst.QuotaSvc.Init(cfg.DataDir)

	inst.AccountMgr.FetchQuota = func(acc *account.Account) (*account.QuotaResult, error) {
		return inst.QuotaSvc.FetchQuota(acc, inst.AuthMgr.RefreshToken, inst.AccountMgr.UpdateAccessToken)
	}
	inst.AccountMgr.RefreshToken = func(acc *account.Account) (string, error) {
		return inst.AuthMgr.RefreshToken(acc)
	}
	inst.AccountMgr.Init(cfg.DataDir)
	inst.SessionRouter.Init(cfg.DataDir)

	// 5. 初始化抓包组件
	inst.PacketCap = stats.NewPacketCapturer(
		func(id string) (string, string, string, error) {
			acc := inst.AccountMgr.GetAccountByID(id)
			if acc == nil {
				return "", "", "", fmt.Errorf("account not found")
			}
			return acc.AccessToken, acc.RefreshToken, acc.ProjectID, nil
		},
		func(id string) (string, error) {
			acc := inst.AccountMgr.GetAccountByID(id)
			if acc == nil {
				return "", fmt.Errorf("account not found")
			}
			return inst.AuthMgr.RefreshToken(acc)
		},
		func() bool {
			return inst.SettingsMgr.GetEnablePacketCapture()
		},
	)
	inst.PacketCap.Init(cfg.DataDir)

	// 6. 装配代理引擎
	proxyHandler := proxy.NewProxyHandler(
		inst.AccountMgr,
		inst.SessionRouter,
		inst.StatsTracker,
		inst.UsageTracker,
		inst.ErrLogger,
		inst.PacketCap,
		inst.logFn,
		inst.AccountMgr.FetchQuota,
		inst.AuthMgr.RefreshToken,
		inst.QuotaSvc.SetCapturedProject,
		inst.QuotaSvc.GetStoredProject,
		inst.SettingsMgr.GetMaxRetries,
		inst.SettingsMgr.GetMaxRetryDelay,
		func() int64 { return int64(inst.SettingsMgr.GetMaxRequestBodyMB()) * 1024 * 1024 },
		inst.SettingsMgr.GetRequestTimeout,
		inst.relayRecordUsage,
		inst.relayAuthorize,
	)
	proxyHandler.SettingsMgr = inst.SettingsMgr
	inst.ProxyEngine = proxy.NewProxyEngine(proxyHandler, inst.logFn, nil)

	// 7. 装配中继服务 Relay 组件
	inst.RelayUserMgr = relay.NewUserManager()
	inst.RelayUserMgr.Init(cfg.DataDir)
	if platformdb.GlobalDB != nil {
		inst.RelayUserMgr.SetDB(platformdb.GlobalDB)
	}

	inst.RelayPackageMgr = relay.NewPackageManager()
	inst.RelayPackageMgr.Init(cfg.DataDir)

	inst.RelayAuthMgr = relay.NewAuthManager(inst.RelayUserMgr)
	inst.RelayStatsMgr = relay.NewStatsTracker(inst.PricingMgr)
	inst.RelayStatsMgr.Init(cfg.DataDir)

	caCertPath := filepath.Join(cfg.DataDir, "certs", "certs", "ca.pem")
	caKeyPath := filepath.Join(cfg.DataDir, "certs", "keys", "ca.private.key")

	ensureServerCACert := func() ([]byte, error) {
		if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
			if genErr := proxy.GenerateCA(caCertPath, caKeyPath); genErr != nil {
				inst.logFn(fmt.Sprintf("⚠️ 自动签发服务端 CA 根证书失败: %v", genErr))
				return nil, genErr
			}
			inst.logFn(fmt.Sprintf("✅ 已成功为独立服务端自动签发 CA 根证书: %s", caCertPath))
		}
		return os.ReadFile(caCertPath)
	}
	// 服务启动时主动确保一次
	if _, err := ensureServerCACert(); err != nil {
		inst.logFn(fmt.Sprintf("⚠️ 检查/生成服务端 CA 证书提示: %v", err))
	}

	inst.RelayAPIMgr = relay.NewAPIHandler(inst.RelayAuthMgr, inst.RelayStatsMgr, inst.RelayPackageMgr, inst.logFn, caCertPath, inst.SettingsMgr, inst.AccountMgr)
	inst.RelayAPIMgr.SetCACertProvider(ensureServerCACert)
	inst.RelayAPIMgr.SetDataDir(cfg.DataDir)
	inst.RelayAPIMgr.SetGlobalStatsTracker(inst.StatsTracker)
	inst.RelayAPIMgr.SetOnSyncReload(func(filename string) {
		logFn(fmt.Sprintf("🔄 [Sync] 收到远端数据推送 (%s)，重新加载配置与号池...", filename))
		inst.AccountMgr.LoadAccounts()
		inst.RelayUserMgr.LoadFromDisk()
		inst.RelayPackageMgr.LoadFromDisk()
		inst.PricingMgr.Reload()
		inst.SettingsMgr.Reload()
	})

	inst.RelayCompatAPIMgr = relay.NewAPICompatHandler(
		inst.RelayAuthMgr,
		inst.AccountMgr,
		inst.SessionRouter,
		inst.RelayStatsMgr,
		inst.UsageTracker,
		inst.SettingsMgr,
		inst.logFn,
	)
	inst.RelayCompatAPIMgr.SetGlobalStatsTracker(inst.StatsTracker)
	inst.RelayCompatAPIMgr.WireOcrRouteResolver()

	inst.BenchmarkScheduler = benchmark.NewScheduler(inst.SettingsMgr, inst.RelayCompatAPIMgr, inst.logFn, nil)
	inst.RelayAPIMgr.SetBenchmarkScheduler(inst.BenchmarkScheduler)

	inst.RelayServer = relay.NewRelayServer(
		inst.ProxyEngine,
		inst.RelayAuthMgr,
		inst.RelayAPIMgr,
		inst.RelayCompatAPIMgr,
		inst.logFn,
		proxy.RelayUserCtxKey,
		proxy.RelayAPIKeyCtxKey,
	)

	return inst, nil
}

// Start 启动独立服务器监听
func (s *ServerInstance) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return nil
	}

	// 启动定期清理过期会话协程
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.RelayAuthMgr != nil {
					s.RelayAuthMgr.CleanExpired()
				}
				_ = db.PruneAllUsersRequestLogs(150)
			}
		}
	}()

	if s.BenchmarkScheduler != nil {
		s.BenchmarkScheduler.Start()
	}

	// 启动 Relay Server
	port := s.Config.Port
	if port == "" {
		port = "18444"
	}
	if err := s.RelayServer.Start(port, "", ""); err != nil {
		return fmt.Errorf("failed to start relay server on port %s: %w", port, err)
	}

	// 若显式开启了本地 MITM Proxy 引擎
	if s.Config.EnableProxy {
		if err := s.ProxyEngine.Start(s.Config.DataDir); err != nil {
			s.logFn(fmt.Sprintf("⚠️ Proxy engine start failed: %v", err))
		}
	}

	s.isRunning = true
	s.logFn(fmt.Sprintf("🚀 Antigravity Headless Server running at port %s (Data: %s)", port, s.Config.DataDir))
	return nil
}

// Shutdown 优雅关闭服务端
func (s *ServerInstance) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return nil
	}
	s.logFn("🛑 Initiating graceful shutdown...")

	if s.cancel != nil {
		s.cancel()
	}

	done := make(chan struct{})
	go func() {
		if s.RelayServer != nil {
			s.RelayServer.Stop()
		}
		if s.ProxyEngine != nil {
			s.ProxyEngine.Stop()
		}
		if s.RelayStatsMgr != nil {
			s.RelayStatsMgr.Close()
		}
		if s.BenchmarkScheduler != nil {
			s.BenchmarkScheduler.Stop()
		}
		if s.RelayUserMgr != nil {
			_ = s.RelayUserMgr.Close()
		}
		platformdb.CloseDB()
		db.CloseDB()
		close(done)
	}()

	select {
	case <-done:
		s.isRunning = false
		s.logFn("✅ Antigravity Headless Server stopped cleanly")
		return nil
	case <-ctx.Done():
		s.isRunning = false
		s.logFn("⚠️ Shutdown timed out, forcing exit")
		return ctx.Err()
	}
}

