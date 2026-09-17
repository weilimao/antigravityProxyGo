package main

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/antigravitybg"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/benchmark"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/diagserver"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/eventsgate"
	"antigravity-proxy/internal/externalconfig"
	"antigravity-proxy/internal/fileutil"
	"antigravity-proxy/internal/patch"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/quota"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"antigravity-proxy/internal/update"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_lifecycle.go: App 生命周期 — startup 启动 / startNetWatch-onNetRecover-reconnectRemoteSafely 网络恢复重连 / shutdown 收尾 / domReady DOM 就绪。
// 从 app.go 按职责拆分而出,同 main 包内共享 App 结构体与全局符号,物理搬移,逻辑逐行等价,零回归。

// warnIfDiskSpaceLow 启动期磁盘空间预检:数据目录所在卷剩余空间低于告警线时,
// 经 AddLog 向用户高声量通告(前端日志面板可见)。纯预警、不拦截启动;
// 真正的写盘保护在 fileutil.WriteFileAtomic 的预检里(剩余 <8MiB+写入量时拒绝落盘并保留原文件)。
func (a *App) warnIfDiskSpaceLow(dir string) {
	const warnBytes = 512 << 20 // 512 MiB
	avail, err := fileutil.AvailableBytes(dir)
	if err != nil || avail >= warnBytes {
		return
	}
	a.AddLog(fmt.Sprintf("💾 [磁盘告警] 数据目录所在磁盘剩余空间仅 %.0f MB(低于 %d MB 告警线)。继续用满时本应用将进入落盘保护(拒绝写盘、原有数据文件保持不动),请尽快清理磁盘空间。",
		float64(avail)/(1<<20), warnBytes>>20))
}

// enabledNvidiaAccountCount 返回当前启用中的 NVIDIA 账号数 —— 对冲并发上限的动态基准
// (禁用账号不参与对冲候选,不计入上限;号池 0/1 时返回原值,由 min(2) 口径兜住下限)。
func (a *App) enabledNvidiaAccountCount() int {
	if a.accountMgr == nil {
		return 0
	}
	n := 0
	for _, acc := range a.accountMgr.GetRawAccountsByProvider("nvidia") {
		if acc != nil && acc.Enabled {
			n++
		}
	}
	return n
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 0. 启动进程内 pprof 诊断端点 (仅 127.0.0.1:18765)。
	// 用于在程序出现死等型整体卡死时，抓取全部 goroutine 调用栈现场，
	// 精确定位阻塞点。启动失败不影响主流程。
	diagserver.Start()

	// 1. Initialize Settings Manager
	a.settingsMgr = settings.NewManager()
	homeDir, _ := os.UserHomeDir()
	var defaultUserData string
	if runtime.GOOS == "windows" {
		defaultUserData = filepath.Join(homeDir, "AppData", "Roaming", "antigravity-proxy-desktop")
	} else {
		defaultUserData = filepath.Join(homeDir, "Library", "Application Support", "antigravity-proxy-desktop")
	}
	_, _ = settings.EnsureConfigExists(defaultUserData)
	a.settingsMgr.Init(defaultUserData)

	// Initialize unified file dialog service (依赖注入：settingsMgr + AddLog)
	a.dialogSvc = dialogs.NewWailsDialogs(a.settingsMgr, a.AddLog).
		// 目录记忆持久化：位于应用数据根目录(不随数据目录迁移)
		WithMemory(dialogs.NewFileDirMemory(a.settingsMgr.GetDefaultUserDataPath())).
		// 导出保存成功后自动定位文件（explorer /select 精确定位并选中文件）
		WithReveal(func(filePath string) {
			a.ShowItemInFolder(filePath)
		})

	// Ensure registry key points to the correct/current executable path if autostart is enabled
	if a.settingsMgr.GetAutoStart() {
		_ = a.settingsMgr.SetAutoStart(true)
	}

	activeDir := a.settingsMgr.GetActiveDataDirectory()

	// [P2] 启动期磁盘空间预检:数据盘接近写满时第一时间告警(落盘防线见 internal/fileutil)。
	a.warnIfDiskSpaceLow(activeDir)

	// 0a. 接线周期性 goroutine 全栈快照落盘 (每 5s 滚动保留 6 份)。
	// 当程序出现死等型整体卡死、连 pprof HTTP 端点 (18765) 也连不上时，
	// <activeDir>/diag/goroutines_LATEST.txt 即为离线取证的唯一现场来源。
	// 内部 sync.Once 启动独立 goroutine,失败静默,靠进程退出回收,不泄漏句柄。
	diagserver.StartAutoDump(diagserver.SnapshotDirFor(activeDir))

	// 2. Initialize Pricing
	a.pricingMgr = pricing.NewManager()
	a.pricingMgr.Init(activeDir)

	// 3. Initialize Stats & Usage Logger
	if err := db.InitDB(activeDir); err != nil {
		fmt.Printf("⚠️ SQLite Database initialization failed: %v\n", err)
	}
	a.statsTracker = stats.NewTracker(a.pricingMgr)
	a.statsTracker.Init(activeDir)

	a.usageTracker = stats.NewUsageTracker(a.pricingMgr)
	a.usageTracker.Init(activeDir)

	a.errLogger = stats.NewRetryErrorLogger()
	a.errLogger.Init(activeDir)

	// 4. Initialize Accounts & Session Router
	a.accountMgr = account.NewManager()
	a.workbuddyOAuthMgr = account.NewWorkBuddyOAuthManager(a.accountMgr)
	a.workbuddyOAuthMgr.SetOnSuccess(func(acc *account.Account) {
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🎉 [WorkBuddy] 官方网页授权登录成功: %s (UID: %s)", acc.Email, acc.ProjectID))
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "workbuddy:oauth-success", map[string]interface{}{
				"id":    acc.ID,
				"email": acc.Email,
				"uid":   acc.ProjectID,
			})
		}
	})
	a.sessionRouter = session.NewRouter()

	// Setup Callbacks
	a.accountMgr.OnAccountsUpdated = func(accs []*account.Account) {
		// 统一走 emitAccountsRes 广播完整载荷(含 otherGroups/nvidiaLBMode 等),
		// 避免薄载荷覆盖把前端 otherGroups 冲成 undefined 导致 Other 分组子 Tab 偶发消失。
		a.emitAccountsRes()
	}

	a.accountMgr.OnQuotaUpdated = func(accountId string, res *account.QuotaResult) {
		wailsRuntime.EventsEmit(a.ctx, "quota-updated", map[string]interface{}{
			"accountId": accountId,
			"buckets":   res.Buckets,
			"tier":      res.Tier,
			"credits":   res.Credits,
		})
	}

	a.accountMgr.OnAccountDisabled = func(accountId string) {
		a.sessionRouter.InvalidateByAccountId(accountId)
	}

	a.accountMgr.OnQuotaRestored = func(accountId string, categories []string) {
		acc := a.accountMgr.GetAccountByID(accountId)
		email := accountId
		if acc != nil {
			email = acc.Email
		}
		a.AddLog(fmt.Sprintf("🔄 [配额恢复] 检测到账号 %s 的 %s 配额限制已解除正常，可继续承接请求。", email, strings.Join(categories, ", ")))

	}

	a.authMgr = quota.NewAuthManager(a.accountMgr)
	a.quotaSvc = quota.NewQuotaService()
	a.quotaSvc.Init(activeDir)
	a.accountMgr.FetchQuota = func(acc *account.Account) (*account.QuotaResult, error) {
		return a.quotaSvc.FetchQuota(acc, a.authMgr.RefreshToken, a.accountMgr.UpdateAccessToken)
	}

	a.accountMgr.RefreshToken = func(acc *account.Account) (string, error) {
		return a.authMgr.RefreshToken(acc)
	}

	a.accountMgr.Init(activeDir)
	a.sessionRouter.Init(activeDir)

	// 5. Initialize Auto Trigger Task Scheduler
	a.autoTriggerScheduler = autotrigger.NewScheduler(
		a.accountMgr,
		a.quotaSvc,
		a.authMgr,
		a.AddLog,
	)
	a.autoTriggerScheduler.Start()

	// Initialize relay managers early so they are always available
	a.ensureRelayInitialized()

	// 5. Initialize Packet Capturer
	a.packetCap = stats.NewPacketCapturer(
		func(id string) (string, string, string, error) {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				return "", "", "", fmt.Errorf("账号不存在")
			}
			return acc.AccessToken, acc.RefreshToken, acc.ProjectID, nil
		},
		func(id string) (string, error) {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				return "", fmt.Errorf("账号不存在")
			}
			return a.authMgr.RefreshToken(acc)
		},
		func() bool {
			return a.settingsMgr.GetEnablePacketCapture()
		},
	)
	a.packetCap.Init(activeDir)

	// 6. Initialize AI 计费生成器(AI 一键生成计费配置后端)。与 packetCap 同款
	// getAccountTokens/refreshAccount 闭包注入(架构与 NewPacketCapturer 逐字对齐),
	// 直连 daily-cloudcode-pa 经 gemini-2.5-flash-lite 给候选模型生成单价。
	a.aiPricingGen = pricing.NewAIPriceGenerator(
		func(id string) (string, string, string, error) {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				return "", "", "", fmt.Errorf("账号不存在")
			}
			return acc.AccessToken, acc.RefreshToken, acc.ProjectID, nil
		},
		func(id string) (string, error) {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				return "", fmt.Errorf("账号不存在")
			}
			return a.authMgr.RefreshToken(acc)
		},
		a.AddLog,
	)

	// Bind UI update callbacks with concurrent-safe throttling to prevent UI rendering freeze
	var lastEmitTime time.Time
	var emitMu sync.Mutex
	var pendingTimer *time.Timer

	triggerStatsUpdate := func() {
		emitMu.Lock()
		defer emitMu.Unlock()

		now := time.Now()
		elapsed := now.Sub(lastEmitTime)
		const throttleInterval = 1000 * time.Millisecond

		sendUpdate := func() {
			if a.IsWindowVisibleAndActive() {
				a.emitEvent("stats-updated", a.getStatsPayload(true))
			}
			lastEmitTime = time.Now()
			if pendingTimer != nil {
				pendingTimer.Stop()
				pendingTimer = nil
			}
		}

		if elapsed >= throttleInterval {
			sendUpdate()
		} else {
			if pendingTimer == nil {
				pendingTimer = time.AfterFunc(throttleInterval-elapsed, func() {
					emitMu.Lock()
					sendUpdate()
					emitMu.Unlock()
				})
			}
		}
	}

	a.statsTracker.SetOnPayloadUpdate(func() {
		triggerStatsUpdate()
	})

	a.usageTracker.SetOnPayloadUpdate(func() {
		triggerStatsUpdate()
	})

	// 6. Initialize Proxy Engine
	proxyHandler := proxy.NewProxyHandler(
		a.accountMgr,
		a.sessionRouter,
		a.statsTracker,
		a.usageTracker,
		a.errLogger,
		a.packetCap,
		a.AddLog,
		a.accountMgr.FetchQuota,
		a.authMgr.RefreshToken,
		a.quotaSvc.SetCapturedProject,
		a.quotaSvc.GetStoredProject,
		a.settingsMgr.GetMaxRetries,
		a.settingsMgr.GetMaxRetryDelay,
		func() int64 { return int64(a.settingsMgr.GetMaxRequestBodyMB()) * 1024 * 1024 },
		a.settingsMgr.GetRequestTimeout,
		a.relayRecordUsage,
		a.relayAuthorize,
	)
	proxyHandler.SettingsMgr = a.settingsMgr

	a.proxyEngine = proxy.NewProxyEngine(proxyHandler, a.AddLog, func(isRunning bool) {
		a.emitEvent("state", isRunning)
	})

	// 7. Initialize Update Manager
	tempDir := filepath.Join(os.TempDir(), "antigravity-proxy-updates")
	a.updateMgr = update.NewManager(appVersion, tempDir)

	// Apply patches and start proxy
	a.AddLog("🖥️ Antigravity Proxy UI Started")
	a.proxyEngine.SetMode(a.settingsMgr.GetIsInterceptMode())
	a.proxyEngine.UpdateSecurityRules(
		a.settingsMgr.GetRelaySSRFBlock(),
		a.settingsMgr.GetRelayPortBlock(),
		a.settingsMgr.GetRelayDomainFilter(),
		a.settingsMgr.GetRelayDomainWhitelist(),
	)
	// 7a. Initialize Relay components
	a.ensureRelayInitialized()
	if a.settingsMgr.GetRelayEnabled() {
		relayPort := a.settingsMgr.GetRelayPort()
		if relayPort == "" {
			relayPort = "18444"
		}
		if err := a.startRelayServer(relayPort); err != nil {
			a.AddLog(fmt.Sprintf("❌ Failed to auto-start relay server: %v", err))
		}
	}

	// 7b. Initialize RemoteRelay (client mode)
	a.remoteRelay = proxy.NewRemoteRelay(a.AddLog)

	// Auto-connect to remote relay if enabled
	if a.settingsMgr.GetRemoteEnabled() {
		host := a.settingsMgr.GetRemoteHost()
		port := a.settingsMgr.GetRemotePort()
		if port == "" {
			port = a.settingsMgr.GetRelayPort()
			if port == "" {
				port = "18444"
			}
		}
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host != "" && key != "" {
			path := a.settingsMgr.GetRemotePath()
			a.AddLog(fmt.Sprintf("🔄 正在自动连接远程中继 %s:%s%s...", host, port, path))
			go func() {
				if err := a.connectRemote(host, port, path, key, pwd); err != nil {
					a.AddLog(fmt.Sprintf("❌ 自动连接远程中继失败: %v", err))
				} else {
					a.AddLog("🌐 远程中继自动连接成功")
				}
				a.emitRemoteState()
			}()
		}
	}

	if err := a.proxyEngine.Start(activeDir); err != nil {
		a.AddLog(fmt.Sprintf("❌ Failed to start Proxy Engine: %v", err))
	}

	// Apply system environment integrations in background
	go func() {
		caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		_ = patch.PatchAll(true, defaultUserData, homeDir, caCertPath, a.AddLog)
	}()

	// Start Memory Monitor
	monitorCtx, cancel := context.WithCancel(ctx)
	a.monitorCancel = cancel
	go a.startMemoryMonitor(monitorCtx)

	// Start Logs Batch Flusher
	go func(ctx context.Context) {
		// 1.5s cadence (was 1s): halves the number of DOM-mutation batches the
		// console ring buffer processes per minute under heavy traffic, with no
		// perceptible lag for human log reading.
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.pendingLogsMu.Lock()
				if len(a.pendingLogs) == 0 {
					a.pendingLogsMu.Unlock()
					continue
				}
				batch := a.pendingLogs
				a.pendingLogs = nil
				a.pendingLogsMu.Unlock()

				if a.IsWindowVisibleAndActive() {
					a.emitEvent("logs:batch", batch)
				}
			}
		}
	}(monitorCtx)

	a.initTray()

	// 构造节流派发门:1s 滑窗 + 窗口不可见即丢,主要用于
	// stats-updated/logs:batch/state/memory-stats-updated 等高频周期事件,
	// 避免主线程 PostMessage 队列被洪峰挤爆。
	a.eventsGate = eventsgate.New(
		func(name string, payload any) {
			if a.ctx == nil {
				return
			}
			wailsRuntime.EventsEmit(a.ctx, name, payload)
		},
		a.IsWindowVisibleAndActive,
		eventsgate.WithMinInterval(1*time.Second),
		eventsgate.WithAlwaysKeep("benchmark-updated"),
	)

	// 初始化外部 Agent 配置管理器:管理 OpenCode / Claude Code 等 CLI Agent 的配置文件。
	// 后端只做文件 I/O(读 JSON 字符串返回前端、前端写 JSON 字符串落盘)。
	// 扩展新 Agent 只需在此追加一行 RegisterAgent + 前端新增一个 schema 文件。
	a.externalConfigMgr = externalconfig.NewManager()
	a.externalConfigMgr.RegisterAgent("opencode", "OpenCode",
		filepath.Join(homeDir, ".config", "opencode", "opencode.json"))
	a.externalConfigMgr.RegisterAgent("claude-code", "Claude Code",
		filepath.Join(homeDir, ".claude", "settings.json"))
	a.externalConfigMgr.RegisterAgent("codex", "Codex",
		filepath.Join(homeDir, ".codex", "config.toml"))

	// 初始化 Antigravity 桌面端壁纸与外观管理器
	a.antigravityBgMgr = antigravitybg.NewManager(a.settingsMgr.GetActiveDataDirectory())

	// 启动模型测速调度器: 定时向配置模型发最小流式请求测首帧/总耗时, 经中继回环复用
	// 全部路由链路; 回环监听 127.0.0.1:0, handler 复用已装配的 relayCompatAPIMgr。
	// 放在 eventsGate 构造之后, 使 benchmark-updated 事件走节流门派发。
	a.benchmarkScheduler = benchmark.NewScheduler(a.settingsMgr, a.relayCompatAPIMgr, a.AddLog, a.emitEvent)
	if a.relayAPIMgr != nil {
		a.relayAPIMgr.SetBenchmarkScheduler(a.benchmarkScheduler)
	}
	a.benchmarkScheduler.Start()

	// 启动网络连通性监听:网络从断→通时触发连接池重置 + 远程中继自动重连,
	// 从根本修复"网络断开后程序废了、再联网也无法使用中继服务"的问题。
	// 具体实现(startNetWatch / onNetRecover / reconnectRemoteSafely)与 shutdown 已迁至
	// app_lifecycle_shutdown.go,此处仅保留调用点。
	a.startNetWatch()
}

func (a *App) domReady(ctx context.Context) {
	// 同步全局 ReasoningAsText 与 EnableThinkingMode 状态
	relay.SetGlobalReasoningAsText(a.settingsMgr.GetReasoningAsText())
	relay.SetGlobalEnableThinkingMode(a.settingsMgr.GetEnableThinkingMode())

	// Pre-populate window.wailsConfigCache before DOM loads
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	defaultDir := a.settingsMgr.GetDefaultUserDataPath()
	cache := map[string]interface{}{
		"settings:get-dir-sync": map[string]string{
			"activeDir":  activeDir,
			"defaultDir": defaultDir,
		},
		"settings:get-system-log-enabled":     a.settingsMgr.GetEnableSystemLog(),
		"settings:get-packet-capture-enabled": a.settingsMgr.GetEnablePacketCapture(),
		"settings:get-startup-options": map[string]bool{
			"autoStart":   a.settingsMgr.GetAutoStart(),
			"silentStart": a.settingsMgr.GetSilentStart(),
		},
		"settings:get-max-retries":         a.settingsMgr.GetMaxRetries(),
		"settings:get-max-retry-delay":     a.settingsMgr.GetMaxRetryDelay(),
		"settings:get-max-request-body-mb": a.settingsMgr.GetMaxRequestBodyMB(),
		"settings:get-request-timeout":     a.settingsMgr.GetRequestTimeout(),
		"get-userdata-path":                defaultDir,
		"relay:get-config": map[string]interface{}{
			"enabled": a.settingsMgr.GetRelayEnabled(),
			"port":    a.settingsMgr.GetRelayPort(),
		},
		"settings:get-fallback-proxy-ports":             a.settingsMgr.GetFallbackProxyPorts(),
		"settings:get-custom-socks5-address":            a.settingsMgr.GetCustomSocks5Address(),
		"settings:get-custom-socks5-enabled":            a.settingsMgr.GetCustomSocks5Enabled(),
		"settings:get-custom-socks5-username":           a.settingsMgr.GetCustomSocks5Username(),
		"settings:get-custom-socks5-password":           a.settingsMgr.GetCustomSocks5Password(),
		"settings:get-fallback-proxy-address":           a.settingsMgr.GetFallbackProxyAddress(),
		"settings:get-fallback-proxy-enabled":           a.settingsMgr.GetFallbackProxyEnabled(),
		"settings:get-fallback-proxy-username":          a.settingsMgr.GetFallbackProxyUsername(),
		"settings:get-fallback-proxy-password":          a.settingsMgr.GetFallbackProxyPassword(),
		"settings:get-prompt-prefix":                    a.settingsMgr.GetPromptPrefix(),
		"settings:get-custom-model-override-enabled":    a.settingsMgr.GetCustomModelOverrideEnabled(),
		"settings:get-custom-model-override-id":         a.settingsMgr.GetCustomModelOverrideID(),
		"settings:get-bypass-override-prefixes":         a.settingsMgr.GetBypassOverridePrefixes(),
		"settings:get-custom-thinking-override-enabled": a.settingsMgr.GetCustomThinkingOverrideEnabled(),
		"settings:get-custom-thinking-supports":         a.settingsMgr.GetCustomThinkingSupports(),
		"settings:get-custom-thinking-budget":           a.settingsMgr.GetCustomThinkingBudget(),
		"settings:get-custom-thinking-min-budget":       a.settingsMgr.GetCustomThinkingMinBudget(),
		"settings:get-custom-max-output-tokens":         a.settingsMgr.GetCustomMaxOutputTokens(),
		"settings:get-reasoning-as-text":                a.settingsMgr.GetReasoningAsText(),
		"settings:get-enable-thinking-mode":             a.settingsMgr.GetEnableThinkingMode(),
		"settings:get-language":                         a.settingsMgr.GetLanguage(),
		"settings:get-account-layout":                   a.settingsMgr.GetAccountLayout(),
		"settings:get-account-grid-columns":             a.settingsMgr.GetAccountGridColumns(),
		"settings:get-session-optimization":             a.settingsMgr.GetSessionOptimization(),
		"settings:get-ocr-model":                        a.settingsMgr.GetOcrModel(),
		"settings:get-ocr-models":                       a.settingsMgr.GetOcrModels(),
		"settings:get-nvidia-worker-proxy": map[string]interface{}{
			"nvidiaWorkerProxyUrl":     a.settingsMgr.GetNvidiaWorkerProxyURL(),
			"nvidiaWorkerProxyEnabled": a.settingsMgr.IsNvidiaWorkerProxyEnabled(),
		},
		"settings:get-nvidia-dedicated-proxy": map[string]interface{}{
			"address":  a.settingsMgr.GetNvidiaDedicatedProxyAddress(),
			"enabled":  a.settingsMgr.GetNvidiaDedicatedProxyEnabled(),
			"username": a.settingsMgr.GetNvidiaDedicatedProxyUsername(),
			"password": a.settingsMgr.GetNvidiaDedicatedProxyPassword(),
		},
		// NVIDIA 对冲请求(默认关):前端 sendSync 读 wailsConfigCache(见 ipc.ts)。
		// poolSize 为当前启用中的 NVIDIA 账号数,前端据此动态渲染并发上限。
		"settings:get-nvidia-hedge": map[string]interface{}{
			"enabled":     a.settingsMgr.IsNvidiaHedgeEnabled(),
			"delayMs":     a.settingsMgr.GetNvidiaHedgeDelayMs(),
			"maxParallel": a.settingsMgr.GetNvidiaHedgeMaxParallel(),
			"immediate":   a.settingsMgr.IsNvidiaHedgeImmediate(),
			"poolSize":    a.enabledNvidiaAccountCount(),
		},
	}

	bytesCache, _ := json.Marshal(cache)
	js := fmt.Sprintf("window.wailsConfigCache = %s; if (window.initWailsReady) { window.initWailsReady(); }", string(bytesCache))
	wailsRuntime.WindowExecJS(ctx, js)

	// Check if this was an autostart and silent start is enabled.
	// If not, show the window (since we set StartHidden: true in main.go)
	isAutostart := false
	for _, arg := range os.Args {
		if arg == "--autostart" || arg == "-autostart" {
			isAutostart = true
			break
		}
	}

	if !(isAutostart && a.settingsMgr.GetSilentStart()) {
		// 走统一显示入口:正规 WindowShow + Win32 跨线程保底。
		// domReady 此时已在 goroutine 中,showMainWindow 内部再 go 两路,
		// 双层 go 无害且确保启动时序不阻塞 domReady 回调本身。
		go a.showMainWindow()
	}

	// DOM 就绪后立即异步推送一次实时内存数据
	go a.emitMemoryStats()

	// 首屏初始化完成后异步主动回收 Go 冷启动期解包与初始化产生的闲置物理内存
	go func() {
		time.Sleep(1 * time.Second)
		debug.FreeOSMemory()
		stats.TrimProcessWorkingSet()
		a.emitMemoryStats()
	}()
}
