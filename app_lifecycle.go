package main

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/corelog"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/diagserver"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/patch"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/quota"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/sigcache"
	"antigravity-proxy/internal/stats"
	"antigravity-proxy/internal/tray"
	"antigravity-proxy/internal/update"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_lifecycle.go: App 生命周期 — startup 启动 / startNetWatch-onNetRecover-reconnectRemoteSafely 网络恢复重连 / shutdown 收尾 / domReady DOM 就绪。
// 从 app.go 按职责拆分而出,同 main 包内共享 App 结构体与全局符号,物理搬移,逻辑逐行等价,零回归。

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
	a.dialogSvc = dialogs.NewWailsDialogs(a.settingsMgr, a.AddLog)

	// Ensure registry key points to the correct/current executable path if autostart is enabled
	if a.settingsMgr.GetAutoStart() {
		_ = a.settingsMgr.SetAutoStart(true)
	}

	activeDir := a.settingsMgr.GetActiveDataDirectory()

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
				wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(true))
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
		func(allocatedAccount, userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs, firstByteMs int64, statusCode int, reqID string) {
			if a.relayStatsMgr != nil {
				rate := a.statsTracker.GetPricingMgr().GetPricingForModel(modelName)
				nonCachedIn := inTokens - cachedTokens
				if nonCachedIn < 0 {
					nonCachedIn = 0
				}
				inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
				outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
				cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0
				totalCost := inputCost + outputCost + cachedCost

				dbItem := &db.RequestLog{
					ReqID:        reqID,
					Timestamp:   time.Now().Format(time.RFC3339),
					Mode:        "remote_relay",
					UserID:      userID,
					ModelName:   modelName,
					InTokens:    inTokens,
					OutTokens:   outTokens,
					CachedTokens: cachedTokens,
					Cost:        totalCost,
					InputCost:   inputCost,
					OutputCost:  outputCost,
					CachedCost:  cachedCost,
					DurationMs:  durationMs,
					FirstByteMs: firstByteMs,
					StatusCode:  statusCode,
					Method:      method,
					Host:        host,
					Path:        path,
					SessionID:   sessionID,
				}
				_ = db.InsertRequestLog(dbItem)

				a.statsTracker.AddRequestLogInMemoryOnly(&stats.RequestLog{
					ID:          reqID,
					Timestamp:   time.Now().Format("01/02 15:04:05"),
					Method:      method,
					Host:        host,
					Path:        path,
					Model:       modelName,
					Account:     allocatedAccount,
					InTokens:    inTokens,
					OutTokens:   outTokens,
					CachedTokens: cachedTokens,
					Cost:        totalCost,
					StatusCode:  statusCode,
					SessionID:   sessionID,
					DurationMs:  durationMs,
					FirstByteMs: firstByteMs,
				})

				a.relayStatsMgr.RecordUsage(relay.RelaySample{
					ReqID:        reqID,
					UserID:       userID,
					UserKey:      apiKeyID,
					ModelName:    modelName,
					InTokens:     inTokens,
					OutTokens:    outTokens,
					CachedTokens: cachedTokens,
					Method:       method,
					Host:         host,
					Path:         path,
					SessionID:    sessionID,
					DurationMs:   durationMs,
					FirstByteMs:  firstByteMs,
					StatusCode:   statusCode,
				})
			}
			if a.relayUserMgr != nil && apiKeyID != "" {
				family := relay.DetectAPIKeyFamily(modelName)
				totalTokens := int64(inTokens + outTokens)
				a.relayUserMgr.RecordAPIKeyUsage(userID, apiKeyID, family == relay.FamilyClaude, totalTokens)
			}
		},
		func(userID, apiKeyID, modelName string) error {
			if a.relayUserMgr == nil || a.relayStatsMgr == nil {
				return nil
			}
			user := a.relayUserMgr.GetUserByID(userID)
			if user == nil {
				return fmt.Errorf("user not found")
			}
			if user.Quotas.ExpireAt > 0 && time.Now().Unix() > user.Quotas.ExpireAt {
				return fmt.Errorf("account expired")
			}

			if apiKeyID != "" {
				for _, key := range user.APIKeys {
					if key.ID == apiKeyID {
						family := relay.DetectAPIKeyFamily(modelName)
						switch family {
						case relay.FamilyClaude:
							if key.LimitClaudeTokens > 0 && key.UsedClaudeTokens >= key.LimitClaudeTokens {
								return fmt.Errorf("API Key Claude token limit exceeded (%d / %d)", key.UsedClaudeTokens, key.LimitClaudeTokens)
							}
						case relay.FamilyNvidia:
							if key.LimitNvidiaTokens > 0 && key.UsedNvidiaTokens >= key.LimitNvidiaTokens {
								return fmt.Errorf("API Key NVIDIA token limit exceeded (%d / %d)", key.UsedNvidiaTokens, key.LimitNvidiaTokens)
							}
						default:
							if key.LimitGeminiTokens > 0 && key.UsedGeminiTokens >= key.LimitGeminiTokens {
								return fmt.Errorf("API Key Gemini token limit exceeded (%d / %d)", key.UsedGeminiTokens, key.LimitGeminiTokens)
							}
						}
						break
					}
				}
			}

			family := relay.DetectAPIKeyFamily(modelName)
			var quota relay.ModelQuota
			var familyKeyword string
			switch family {
			case relay.FamilyClaude:
				quota = user.Quotas.Claude
				familyKeyword = "claude"
			case relay.FamilyNvidia:
				quota = user.Quotas.Nvidia
				familyKeyword = relay.NvidiaQuotaFamily
			default:
				quota = user.Quotas.Gemini
				familyKeyword = "gemini"
			}

			if !quota.EnableFixed && !quota.EnableHourly && !quota.EnableDaily {
				return fmt.Errorf("model series unauthorized")
			}

			stats := a.relayStatsMgr.GetUserStats(userID)

			if quota.EnableFixed {
				var usedTokens int64
				if quota.ResetAt != "" {
					var err error
					usedTokens, err = db.GetTokensForUserModelFamilySince(userID, familyKeyword, quota.ResetAt)
					if err != nil {
						return fmt.Errorf("failed to check fixed quota")
					}
				} else {
					if stats != nil {
						for mName, mStats := range stats.Models {
							if relay.MatchModelFamily(mName, family) {
								usedTokens += int64(mStats.InputTokens + mStats.OutputTokens)
							}
						}
					}
				}
				if usedTokens >= quota.FixedTokens {
					return fmt.Errorf("fixed token limit exceeded (%d / %d)", usedTokens, quota.FixedTokens)
				}
			}

			if quota.EnableHourly && quota.HourlyHours > 0 {
				usedTokens, _, err := relay.GetActiveWindow(userID, familyKeyword, relay.QuotaTypeHourly(family), quota.HourlyHours, true)
				if err != nil {
					return fmt.Errorf("failed to check hourly quota")
				}
				if usedTokens >= quota.HourlyTokens {
					return fmt.Errorf("hourly token limit exceeded (%d / %d)", usedTokens, quota.HourlyTokens)
				}
			}

			if quota.EnableDaily && quota.DailyDays > 0 {
				usedTokens, _, err := relay.GetActiveWindow(userID, familyKeyword, relay.QuotaTypeDaily(family), quota.DailyDays*24, true)
				if err != nil {
					return fmt.Errorf("failed to check daily quota")
				}
				if usedTokens >= quota.DailyTokens {
					return fmt.Errorf("daily token limit exceeded (%d / %d)", usedTokens, quota.DailyTokens)
				}
			}

			return nil
		},
	)
	proxyHandler.SettingsMgr = a.settingsMgr

	a.proxyEngine = proxy.NewProxyEngine(proxyHandler, a.AddLog, func(isRunning bool) {
		wailsRuntime.EventsEmit(a.ctx, "state", isRunning)
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
					wailsRuntime.EventsEmit(a.ctx, "logs:batch", batch)
				}
			}
		}
	}(monitorCtx)

	a.initTray()

	// 启动网络连通性监听:网络从断→通时触发连接池重置 + 远程中继自动重连,
	// 从根本修复"网络断开后程序废了、再联网也无法使用中继服务"的问题。
	a.startNetWatch()
}

// startNetWatch 启动网络连通性监听器。
// 仅在配置了远程中继(或代理引擎在跑)时才有意义;未配置也不报错。
// 网络从离线恢复到在线时触发 onNetRecover 回调。
func (a *App) startNetWatch() {
	a.netWatch = proxy.NewNetWatch(a.onNetRecover)
	a.netWatch.Start()
	if a.settingsMgr.GetEnableSystemLog() {
		a.AddLog("📡 [网络监听] 网络连通性监听器已启动,断网恢复后将自动重置连接与重连远程中继")
	}
}

// onNetRecover 网络从断→通的边沿恢复回调。
// 重置代理引擎本地连接池(消除休眠/断网残留死连接),
// 并在已配置远程中继凭据时尝试自动重连。
func (a *App) onNetRecover() {
	a.AddLog("🌐 [网络恢复] 检测到网络已恢复,正在重置本地连接池与远程中继链路...")

	// 1. 重置代理引擎连接池与活跃隧道,强制客户端重建连接
	if a.proxyEngine != nil {
		a.proxyEngine.ResetConnections()
	}
	if a.remoteRelay != nil {
		a.proxyEngine.ResetRemoteClient()
	}

	// 2. 已配置远程中继凭据但当前未连接(或被健康检查标为离线)→ 自动重连
	if a.remoteRelay != nil && a.settingsMgr.GetRemoteEnabled() {
		host := a.settingsMgr.GetRemoteHost()
		port := a.settingsMgr.GetRemotePort()
		path := a.settingsMgr.GetRemotePath()
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host != "" && key != "" {
			go func() {
				if err := a.reconnectRemoteSafely(host, port, path, key, pwd); err != nil {
					a.AddLog(fmt.Sprintf("⚠️ [网络恢复] 自动重连远程中继失败: %v", err))
				} else {
					a.AddLog("✅ [网络恢复] 远程中继自动重连成功")
				}
			}()
		}
	}
}

// reconnectRemoteSafely 线程安全地执行一次远程中继重连。
// 通过 reloginMu 串行化,避免网络恢复回调与健康检查重连回调并发重复 Login。
func (a *App) reconnectRemoteSafely(host, port, path, key, pwd string) error {
	a.reloginMu.Lock()
	defer a.reloginMu.Unlock()

	// 若此刻已连接,无需重连(健康检查可能已抢先恢复)
	if a.remoteRelay != nil && a.remoteRelay.IsConnected() {
		return nil
	}
	return a.connectRemote(host, port, path, key, pwd)
}

func (a *App) shutdown() {
	tray.QuitTray()

	// 停止网络监听,释放后台探测 goroutine
	if a.netWatch != nil {
		a.netWatch.Stop()
	}

	if a.autoTriggerScheduler != nil {
		a.autoTriggerScheduler.Stop()
	}

	if a.monitorCancel != nil {
		a.monitorCancel()
	}

	a.stopRelayServer()
	if a.proxyEngine != nil {
		a.proxyEngine.Stop()
	}
	if a.sessionRouter != nil {
		a.sessionRouter.SaveToDisk()
	}

	// 停止账号冷静期/Token 刷新监控 goroutine,避免进程退出后仍持 ticker 句柄残留。
	// 此前这两个 Stop 方法从未被调用,是进程退出后"任务管理器残留不消失"的孤儿协程来源之一。
	if a.accountMgr != nil {
		a.accountMgr.StopCooldownMonitor()
		a.accountMgr.StopTokenRefreshMonitor()
	}

	// Clean up patches on exit
	homeDir, _ := os.UserHomeDir()
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
	_ = patch.PatchAll(false, a.settingsMgr.GetDefaultUserDataPath(), homeDir, caCertPath, func(s string) {})

	// 通知 corelog 消费者排空残留日志并退出，避免进程结束时日志被截断，
	// 也释放唯一向 os.Stdout 写入的后台 goroutine。
	corelog.Stop()

	// 通知签名缓存的清理协程退出，释放后台 goroutine。
	sigcache.StopGlobal()

	// 关闭 pprof 诊断服务,释放其 listener 句柄与后台 serve goroutine,
	// 避免"退出窗口后 18765 端口仍被占用、进程残留"。
	diagserver.Stop()
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
		"settings:get-bypass-override-prefixes":        a.settingsMgr.GetBypassOverridePrefixes(),
		"settings:get-custom-thinking-override-enabled": a.settingsMgr.GetCustomThinkingOverrideEnabled(),
		"settings:get-custom-thinking-supports":         a.settingsMgr.GetCustomThinkingSupports(),
		"settings:get-custom-thinking-budget":           a.settingsMgr.GetCustomThinkingBudget(),
		"settings:get-custom-thinking-min-budget":       a.settingsMgr.GetCustomThinkingMinBudget(),
		"settings:get-custom-max-output-tokens":         a.settingsMgr.GetCustomMaxOutputTokens(),
		"settings:get-reasoning-as-text":                a.settingsMgr.GetReasoningAsText(),
		"settings:get-enable-thinking-mode":             a.settingsMgr.GetEnableThinkingMode(),
		"settings:get-language":                         a.settingsMgr.GetLanguage(),
		"settings:get-session-optimization":             a.settingsMgr.GetSessionOptimization(),
		"settings:get-ocr-model":                        a.settingsMgr.GetOcrModel(),
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
		wailsRuntime.WindowShow(ctx)
		a.SetWindowVisible(true)
	}
}
