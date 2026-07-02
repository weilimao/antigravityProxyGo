package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/cert"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/patch"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/quota"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"antigravity-proxy/internal/tray"
	"antigravity-proxy/internal/update"
	"encoding/base32"
)

type App struct {
	ctx           context.Context
	settingsMgr   *settings.Manager
	accountMgr    *account.Manager
	sessionRouter *session.Router
	pricingMgr    *pricing.Manager
	statsTracker  *stats.Tracker
	usageTracker  *stats.UsageTracker
	errLogger     *stats.RetryErrorLogger
	packetCap     *stats.PacketCapturer
	authMgr       *quota.AuthManager
	proxyEngine   *proxy.ProxyEngine
	updateMgr     *update.Manager
	dialogSvc     dialogs.Dialogs
	logBuffer     []string
	logBufferMu   sync.Mutex
	monitorCancel context.CancelFunc
	quotaSvc      *quota.QuotaService
	isQuitting    bool
	isQuittingMu  sync.RWMutex
	isWindowVisible   bool
	isWindowVisibleMu sync.RWMutex
	// Relay server components
	relayUserMgr    *relay.UserManager
	relayPackageMgr *relay.PackageManager
	relayAuthMgr    *relay.AuthManager
	relayStatsMgr     *relay.StatsTracker
	relayAPIMgr       *relay.APIHandler
	relayCompatAPIMgr *relay.APICompatHandler
	relayServer       *relay.RelayServer
	remoteRelay          *proxy.RemoteRelay
	autoTriggerScheduler *autotrigger.Scheduler
}

func NewApp() *App {
	return &App{
		logBuffer: make([]string, 0),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

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
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
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
		a.AddLog(fmt.Sprintf("🔄 [自动触发] 检测到账号 %s 的配额限制已恢复 (%s)，触发自动化任务...", email, strings.Join(categories, ", ")))

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

		// Bind UI update callbacks
	a.statsTracker.SetOnPayloadUpdate(func() {
		if a.IsWindowVisibleAndActive() {
			wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(true))
		}
	})

	a.usageTracker.SetOnPayloadUpdate(func() {
		if a.IsWindowVisibleAndActive() {
			wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(true))
		}
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
		func(userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs int64, statusCode int, reqID string) {
			if f, err := os.OpenFile(`B:\antigravityProxy\data\debug.log`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				f.WriteString(fmt.Sprintf("[%s] relayStatsCallback: UserID=%s, ReqID=%s, Model=%s\n", time.Now().Format(time.RFC3339), userID, reqID, modelName))
				f.Close()
			}
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
					Timestamp:    time.Now().Format(time.RFC3339),
					Mode:         "remote_relay",
					UserID:       userID,
					ModelName:    modelName,
					InTokens:     inTokens,
					OutTokens:    outTokens,
					CachedTokens: cachedTokens,
					Cost:         totalCost,
					InputCost:    inputCost,
					OutputCost:   outputCost,
					CachedCost:   cachedCost,
					DurationMs:   durationMs,
					StatusCode:   statusCode,
					Method:       method,
					Host:         host,
					Path:         path,
					SessionID:    sessionID,
				}
				_ = db.InsertRequestLog(dbItem)

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
					StatusCode:   statusCode,
				})
			}
			if a.relayUserMgr != nil && apiKeyID != "" {
				isClaude := strings.Contains(strings.ToLower(modelName), "claude")
				totalTokens := int64(inTokens + outTokens)
				a.relayUserMgr.RecordAPIKeyUsage(userID, apiKeyID, isClaude, totalTokens)
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
						isClaude := strings.Contains(strings.ToLower(modelName), "claude")
						if isClaude {
							if key.LimitClaudeTokens > 0 && key.UsedClaudeTokens >= key.LimitClaudeTokens {
								return fmt.Errorf("API Key Claude token limit exceeded (%d / %d)", key.UsedClaudeTokens, key.LimitClaudeTokens)
							}
						} else {
							if key.LimitGeminiTokens > 0 && key.UsedGeminiTokens >= key.LimitGeminiTokens {
								return fmt.Errorf("API Key Gemini token limit exceeded (%d / %d)", key.UsedGeminiTokens, key.LimitGeminiTokens)
							}
						}
						break
					}
				}
			}

			isClaude := strings.Contains(strings.ToLower(modelName), "claude")
			var quota relay.ModelQuota
			if isClaude {
				quota = user.Quotas.Claude
			} else {
				quota = user.Quotas.Gemini
			}

			if !quota.EnableFixed && !quota.EnableHourly && !quota.EnableDaily {
				return fmt.Errorf("model series unauthorized")
			}

			stats := a.relayStatsMgr.GetUserStats(userID)

			familyKeyword := "gemini"
			if isClaude {
				familyKeyword = "claude"
			}

			if quota.EnableFixed {
				var lifetimeTokens int64
				if stats != nil {
					for mName, mStats := range stats.Models {
						if (isClaude && strings.Contains(strings.ToLower(mName), "claude")) || 
						   (!isClaude && !strings.Contains(strings.ToLower(mName), "claude")) {
							lifetimeTokens += int64(mStats.InputTokens + mStats.OutputTokens)
						}
					}
				}
				if lifetimeTokens >= quota.FixedTokens {
					return fmt.Errorf("fixed token limit exceeded")
				}
			}

			if quota.EnableHourly && quota.HourlyHours > 0 {
				periodDuration := time.Duration(quota.HourlyHours) * time.Hour
				since := time.Now().Add(-periodDuration).Format(time.RFC3339)
				usedTokens, err := db.GetTokensForUserModelFamilySince(userID, familyKeyword, since)
				if err != nil {
					return fmt.Errorf("failed to check hourly quota")
				}
				if usedTokens >= quota.HourlyTokens {
					return fmt.Errorf("hourly token limit exceeded (%d / %d)", usedTokens, quota.HourlyTokens)
				}
			}

			if quota.EnableDaily && quota.DailyDays > 0 {
				periodDuration := time.Duration(quota.DailyDays*24) * time.Hour
				since := time.Now().Add(-periodDuration).Format(time.RFC3339)
				usedTokens, err := db.GetTokensForUserModelFamilySince(userID, familyKeyword, since)
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
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host != "" && key != "" {
			a.AddLog(fmt.Sprintf("🔄 正在自动连接远程中继 %s:%s...", host, port))
			go func() {
				if err := a.connectRemote(host, port, key, pwd); err != nil {
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

	a.initTray()
}

func (a *App) shutdown() {
	tray.QuitTray()

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

	// Clean up patches on exit
	homeDir, _ := os.UserHomeDir()
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
	_ = patch.PatchAll(false, a.settingsMgr.GetDefaultUserDataPath(), homeDir, caCertPath, func(s string) {})
}

func (a *App) AddLog(msg string) {
	if a.settingsMgr != nil && !a.settingsMgr.GetEnableSystemLog() {
		return
	}
	timestamp := time.Now().Format("15:04:05.000")
	formatted := fmt.Sprintf("[%s] %s", timestamp, msg)

	// 同时输出至标准输出，以便在终端中展示日志
	fmt.Println(formatted)

	a.logBufferMu.Lock()
	a.logBuffer = append(a.logBuffer, formatted)
	if len(a.logBuffer) > 50 {
		a.logBuffer = a.logBuffer[1:]
	}
	a.logBufferMu.Unlock()

	if a.IsWindowVisibleAndActive() {
		wailsRuntime.EventsEmit(a.ctx, "log", formatted)
	}
}

// OpenPath opens system browser or path
func (a *App) OpenPath(p string) {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			wailsRuntime.BrowserOpenURL(a.ctx, p)
		} else {
			_ = exec.Command("cmd", "/c", "start", "", p).Start()
		}
	} else if runtime.GOOS == "darwin" {
		_ = exec.Command("open", p).Start()
	}
}

// ShowItemInFolder displays file in native file manager
func (a *App) ShowItemInFolder(p string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("explorer", "/select,", p).Start()
	} else if runtime.GOOS == "darwin" {
		_ = exec.Command("open", "-R", p).Start()
	}
}

func (a *App) domReady(ctx context.Context) {
	// Pre-populate window.wailsConfigCache before DOM loads
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	defaultDir := a.settingsMgr.GetDefaultUserDataPath()
	cache := map[string]interface{}{
		"settings:get-dir-sync": map[string]string{
			"activeDir":  activeDir,
			"defaultDir": defaultDir,
		},
		"settings:get-system-log-enabled": a.settingsMgr.GetEnableSystemLog(),
		"settings:get-packet-capture-enabled": a.settingsMgr.GetEnablePacketCapture(),
		"settings:get-startup-options": map[string]bool{
			"autoStart":   a.settingsMgr.GetAutoStart(),
			"silentStart": a.settingsMgr.GetSilentStart(),
		},
		"settings:get-max-retries": a.settingsMgr.GetMaxRetries(),
		"get-userdata-path":        defaultDir,
		"relay:get-config": map[string]interface{}{
			"enabled": a.settingsMgr.GetRelayEnabled(),
			"port":    a.settingsMgr.GetRelayPort(),
		},
		"settings:get-fallback-proxy-ports": a.settingsMgr.GetFallbackProxyPorts(),
		"settings:get-custom-socks5-address": a.settingsMgr.GetCustomSocks5Address(),
		"settings:get-custom-socks5-enabled": a.settingsMgr.GetCustomSocks5Enabled(),
		"settings:get-custom-socks5-username": a.settingsMgr.GetCustomSocks5Username(),
		"settings:get-custom-socks5-password": a.settingsMgr.GetCustomSocks5Password(),
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

// IPCSend routes Electron's send requests
func (a *App) IPCSend(channel string, argsJSON string) {
	var args []interface{}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// 模块化分流：交给设置 IPC 处理器，防止 app.go 持续膨胀
	if a.handleSettingsIPCSend(channel, args) {
		return
	}

	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	getBoolArg := func(idx int) bool {
		if idx < len(args) {
			if b, ok := args[idx].(bool); ok {
				return b
			}
		}
		return false
	}

	getIntArg := func(idx int) int {
		if idx < len(args) {
			if f, ok := args[idx].(float64); ok {
				return int(f)
			}
			if i, ok := args[idx].(int); ok {
				return i
			}
		}
		return 0
	}

	switch channel {
	case "accounts:get":
		accs := a.accountMgr.GetAccounts()
		a.AddLog(fmt.Sprintf("🔄 [账号同步] 收到前端获取请求，当前后端已加载账号数: %d", len(accs)))
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          accs,
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})

	case "accounts:remove":
		a.accountMgr.RemoveAccount(getStringArg(0))

	case "accounts:toggle-enabled":
		a.accountMgr.UpdateAccountEnabled(getStringArg(0), getBoolArg(1))
		acc := a.accountMgr.GetAccountByID(getStringArg(0))
		if acc != nil {
			statusStr := "disabled"
			if getBoolArg(1) {
				statusStr = "enabled"
			}
			a.AddLog(fmt.Sprintf("🔄 Account %s is now %s in the pool.", acc.Email, statusStr))
		}

	case "accounts:toggle-overages":
		a.accountMgr.UpdateAccountOverages(getStringArg(0), getBoolArg(1))
		acc := a.accountMgr.GetAccountByID(getStringArg(0))
		if acc != nil {
			statusStr := "disabled"
			if getBoolArg(1) {
				statusStr = "enabled"
			}
			a.AddLog(fmt.Sprintf("🔄 Account %s AI Credit Overages is now %s.", acc.Email, statusStr))
		}

	case "accounts:export-all":
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出账号配置",
			DefaultName: "accounts_export.json",
			Filters:     []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
		})
		if ok {
			data, _ := json.MarshalIndent(map[string]interface{}{"accounts": a.accountMgr.GetRawAccounts()}, "", "  ")
			_ = os.WriteFile(filePath, data, 0644)
			a.AddLog("📥 [账号导出] 成功导出所有账号")
		}

	case "accounts:export-single":
		acc := a.accountMgr.GetAccountByID(getStringArg(0))
		if acc != nil {
			filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
				Title:       "导出单账号配置",
				DefaultName: fmt.Sprintf("account_%s.json", acc.Email),
				Filters:     []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
			})
			if ok {
				data, _ := json.MarshalIndent(map[string]interface{}{"accounts": []*account.Account{acc}}, "", "  ")
				_ = os.WriteFile(filePath, data, 0644)
				a.AddLog("📥 [账号导出] 成功导出账号: " + acc.Email)
			}
		}

	case "accounts:import":
		filePath, ok, _ := a.dialogSvc.Open(a.ctx, dialogs.OpenRequest{
			Title:   "导入账号配置",
			Filters: []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
		})
		if ok {
			if fileData, err := os.ReadFile(filePath); err == nil {
				var wrapper struct {
					Accounts []*account.Account `json:"accounts"`
				}
				if json.Unmarshal(fileData, &wrapper) == nil && len(wrapper.Accounts) > 0 {
					addedCount := a.accountMgr.ImportAccountsList(wrapper.Accounts)
					if addedCount > 0 {
						a.AddLog(fmt.Sprintf("📥 [账号导入] 成功导入 %d 个账号", addedCount))
					}
				}
			}
		}

	case "pool:toggle":
		a.accountMgr.SetPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Antigravity Load Balancing enabled. Distributing requests across accounts.")
		} else {
			a.AddLog("🔄 Antigravity Load Balancing disabled. Using a single active account.")
		}

	case "pool:toggle-project":
		a.accountMgr.SetProjectPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Project API Load Balancing enabled. Distributing requests across project accounts.")
		} else {
			a.AddLog("🔄 Project API Load Balancing disabled. Using a single active project account.")
		}

	/* case "pool:toggle-gemini-cli":
		a.accountMgr.SetGeminiCliPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Gemini CLI Load Balancing enabled. Distributing requests across Gemini CLI accounts.")
		} else {
			a.AddLog("🔄 Gemini CLI Load Balancing disabled. Using a single active Gemini CLI account.")
		} */

	case "channel:switch":
		a.accountMgr.SetActiveChannel(getStringArg(0))
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})
		a.AddLog("🔄 Switched active routing channel to: " + getStringArg(0))

	case "toggle":
		enable := getBoolArg(0)
		a.proxyEngine.SetMode(enable)
		_ = a.settingsMgr.SetIsInterceptMode(enable)
		if enable {
			a.AddLog("✅ Mode Switched: Intercept ON (Traffic buffering & retrying 503 errors)")
		} else {
			a.AddLog("✅ Mode Switched: Intercept OFF (Passthrough to Google directly)")
		}

	case "get-state":
		wailsRuntime.EventsEmit(a.ctx, "state", a.proxyEngine.IsInterceptMode())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.emitMemoryStats()
		a.logBufferMu.Lock()
		for _, log := range a.logBuffer {
			wailsRuntime.EventsEmit(a.ctx, "log", log)
		}
		a.logBufferMu.Unlock()
		{
			activeDir := a.settingsMgr.GetActiveDataDirectory()
			caPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
			wailsRuntime.EventsEmit(a.ctx, "cert-status-res", cert.CheckCertStatus(caPath))
		}

	case "settings:set-system-log-enabled":
		_ = a.settingsMgr.SetEnableSystemLog(getBoolArg(0))

	case "settings:set-packet-capture-enabled":
		_ = a.settingsMgr.SetEnablePacketCapture(getBoolArg(0))

	case "settings:set-auto-start":
		_ = a.settingsMgr.SetAutoStart(getBoolArg(0))

	case "settings:set-silent-start":
		_ = a.settingsMgr.SetSilentStart(getBoolArg(0))

	case "settings:set-max-retries":
		_ = a.settingsMgr.SetMaxRetries(getIntArg(0))

	case "cert-status":
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		caPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		wailsRuntime.EventsEmit(a.ctx, "cert-status-res", cert.CheckCertStatus(caPath))

	case "cert-install":
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		caPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		a.AddLog("⏳ Starting Root CA installation...")
		ok, errStr := cert.InstallCert(caPath)
		if ok {
			a.AddLog("🔒 Local Root CA successfully trusted in system store.")
		} else {
			a.AddLog("❌ Failed to trust Root CA: " + errStr)
		}
		wailsRuntime.EventsEmit(a.ctx, "cert-status-res", ok)

	case "cert-uninstall":
		a.AddLog("⏳ Removing Root CA certificate...")
		ok, errStr := cert.UninstallCert()
		if ok {
			a.AddLog("🔓 Local Root CA removed from system store.")
		} else {
			a.AddLog("❌ Failed to remove Root CA: " + errStr)
		}
		wailsRuntime.EventsEmit(a.ctx, "cert-status-res", !ok)

	case "get-pricing":
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())

	case "update-pricing":
		if idx := strings.Index(argsJSON, ","); idx != -1 {
			var rate pricing.ModelRate
			modelKey := getStringArg(0)
			if len(args) > 1 {
				if mapData, ok := args[1].(map[string]interface{}); ok {
					bytesData, _ := json.Marshal(mapData)
					_ = json.Unmarshal(bytesData, &rate)
				}
			}
			_ = a.pricingMgr.UpdateModelPricing(modelKey, rate)
			wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
			wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
			a.AddLog(fmt.Sprintf("💰 Model pricing updated for \"%s\": In: $%f/1M, Out: $%f/1M, Cache: $%f/1M", modelKey, rate.Input, rate.Output, rate.Cached))
		}

	case "delete-pricing":
		modelKey := getStringArg(0)
		a.pricingMgr.DeleteModelPricing(modelKey)
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog("🗑️ Model pricing deleted for \"" + modelKey + "\"")

	case "reset-pricing":
		_ = a.pricingMgr.ResetPricingToDefault()
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog("🔄 Model pricing reset to defaults")

	case "packet:clear":
		a.packetCap.ClearPackets()

	case "app:install-update":
		filePath := getStringArg(0)
		a.AddLog("⏳ 正在启动应用程序更新安装: " + filePath)
		err := a.updateMgr.InstallUpdate(filePath)
		if err != nil {
			a.AddLog("❌ 启动更新安装失败: " + err.Error())
			wailsRuntime.EventsEmit(a.ctx, "app:update-error", err.Error())
		} else {
			a.AddLog("👋 更新安装包已成功启动，正在退出当前进程以完成更新...")
			os.Exit(0)
		}

	case "settings:export-logs":
		logContent := getStringArg(0)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			DefaultName: fmt.Sprintf("system_logs_%s.txt", time.Now().Format("20060102150405")),
			Title:       "Export System Logs",
			Filters:     []dialogs.FileFilter{{DisplayName: "Text Files", Pattern: "*.txt"}},
		})
		if ok && filePath != "" {
			err := os.WriteFile(filePath, []byte(logContent), 0644)
			if err != nil {
				a.AddLog(fmt.Sprintf("❌ Failed to export system logs: %v", err))
			} else {
				a.AddLog(fmt.Sprintf("✅ System logs exported to: %s", filePath))
			}
		}

	case "settings:open-folder":
		a.OpenPath(getStringArg(0))
	}
}

// IPCInvoke routes Electron's invoke requests and returns JSON string results
func (a *App) IPCInvoke(channel string, argsJSON string) (string, error) {
	var args []interface{}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	if res, handled, err := a.handleSessionIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleRelayIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleTotpIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleAccountIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleAutoTriggerIPC(channel, args); handled {
		return res, err
	}

	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	case "auth:login":
		provider := "gemini-cli"
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				provider = s
			} else if m, ok := args[0].(map[string]interface{}); ok {
				if p, exists := m["provider"].(string); exists {
					provider = p
				}
			}
		}
		res, err := a.authMgr.StartLogin(provider, a.OpenPath)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ Login failed (%s): %v", provider, err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		// Save account
		projectId := ""
		if mapProj, ok := args[0].(map[string]interface{}); ok {
			if p, exists := mapProj["projectId"].(string); exists {
				projectId = p
			}
		}

		if res["access_token"] != nil {
			token := res["access_token"].(string)
			email := res["email"].(string)
			refresh := ""
			if res["refresh_token"] != nil {
				refresh = res["refresh_token"].(string)
			}

			a.accountMgr.AddAccount(&account.Account{
				Email:        email,
				AccessToken:  token,
				RefreshToken: refresh,
				Provider:     provider,
				ProjectID:    projectId,
				ProjectLabel: projectId,
				Enabled:      true,
			})
		}

		return marshalResponse(map[string]interface{}{"success": true, "email": res["email"]})

	case "auth:cancel-login":
		a.authMgr.CancelLogin()
		return marshalResponse(map[string]interface{}{"success": true})

	case "auth:get-manual-oauth-url":
		res := a.authMgr.GenerateManualOAuthURL()
		return marshalResponse(res)

	case "auth:exchange-manual-code":
		code := ""
		verifier := ""
		if len(args) > 0 {
			if mapData, ok := args[0].(map[string]interface{}); ok {
				if c, exists := mapData["code"].(string); exists {
					code = c
				}
				if v, exists := mapData["code_verifier"].(string); exists {
					verifier = v
				}
			}
		}

		tokenData, err := a.authMgr.ExchangeCodeForTokenManual(code, verifier)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		email, err := a.authMgr.GetUserEmail(tokenData.AccessToken, "project")
		if err != nil {
			email = "Unknown"
		}

		return marshalResponse(map[string]interface{}{
			"success":          true,
			"email":            email,
			"access_token":     tokenData.AccessToken,
			"refresh_token":    tokenData.RefreshToken,
			"activeProjectId":  "",
			"projects":         []interface{}{},
		})

	case "auth:add-manual-account":
		var payload struct {
			Email        string `json:"email"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ProjectID    string `json:"projectId"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		a.accountMgr.AddAccount(&account.Account{
			Email:        payload.Email,
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			Provider:     "project",
			ProjectID:    payload.ProjectID,
			ProjectLabel: payload.ProjectID,
			Enabled:      true,
		})
		return marshalResponse(map[string]interface{}{"success": true})

	case "pool:clear-sessions":
		cleared := a.sessionRouter.ClearAllAndSave()
		a.AddLog(fmt.Sprintf("🧹 [粘性路由] 手动清空所有会话绑定，共 %d 条。", cleared))
		return marshalResponse(map[string]interface{}{"success": true, "cleared": cleared})

	case "quota:fetch":
		accId := getStringArg(0)
		acc := a.accountMgr.GetAccountByID(accId)
		if acc == nil {
			a.AddLog("❌ [配额刷新] 无法刷新配额：未找到对应的账号")
			return marshalResponse(map[string]interface{}{"error": "Account not found", "buckets": []interface{}{}})
		}
		a.AddLog(fmt.Sprintf("🔄 [配额刷新] 开始刷新账号 %s 的配额...", acc.Email))
		res, err := a.accountMgr.FetchQuota(acc)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [配额刷新] 账号 %s 刷新配额失败: %v", acc.Email, err))
			return marshalResponse(map[string]interface{}{"error": err.Error(), "buckets": []interface{}{}})
		}
		if len(res.Buckets) > 0 {
			a.accountMgr.UpdateAccountCooldownFromQuota(accId, res.Buckets)
		}
		a.accountMgr.UpdateAccountTier(accId, res.Tier)
		if res.Credits != nil {
			a.accountMgr.UpdateAccountCredits(accId, *res.Credits)
		}
		a.AddLog(fmt.Sprintf("✅ [配额刷新] 账号 %s 配额及积分刷新成功！(Tier: %s)", acc.Email, res.Tier))
		return marshalResponse(res)

	case "settings:change-dir":
		targetDir, ok, _ := a.dialogSvc.OpenDir(a.ctx, dialogs.DirRequest{Title: "选择数据存储目录"})
		if !ok {
			return marshalResponse(map[string]interface{}{"success": false, "error": "用户取消选择"})
		}

		if filepath.Clean(targetDir) == filepath.Clean(a.settingsMgr.GetActiveDataDirectory()) {
			return marshalResponse(map[string]interface{}{"success": true, "activeDir": targetDir})
		}

		wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": "stop-proxy", "status": "正在停止代理服务器..."})
		a.proxyEngine.Stop()

		defaultUserData := a.settingsMgr.GetDefaultUserDataPath()

		errMigrate := a.settingsMgr.MigrateData(
			targetDir,
			func(step, status string) {
				wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": step, "status": status})
			},
			a.proxyEngine.Stop,
			func() {
				_ = a.proxyEngine.Start(targetDir)
			},
			func(caPemPath string) error {
				homeDir, _ := os.UserHomeDir()
				return patch.PatchAll(true, defaultUserData, homeDir, caPemPath, a.AddLog)
			},
			func(newDir string) {
				a.accountMgr.UpdatePath(newDir)
				a.statsTracker.UpdatePath(newDir)
				a.usageTracker.UpdatePath(newDir)
				a.errLogger.UpdatePath(newDir)
				a.pricingMgr.UpdatePath(newDir)
				a.packetCap.UpdatePath(newDir)
				a.sessionRouter.UpdatePath(newDir)
				a.quotaSvc.UpdatePath(newDir)
				if a.relayUserMgr != nil {
					a.relayUserMgr.UpdatePath(newDir)
				}
				if a.relayStatsMgr != nil {
					a.relayStatsMgr.UpdatePath(newDir)
				}
			},
		)

		if errMigrate != nil {
			wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": "error", "status": errMigrate.Error()})
			_ = a.proxyEngine.Start(a.settingsMgr.GetActiveDataDirectory())
			return marshalResponse(map[string]interface{}{"success": false, "error": errMigrate.Error()})
		}

		a.AddLog("📁 数据存储路径已成功更改并迁移至: " + targetDir)
		return marshalResponse(map[string]interface{}{"success": true, "activeDir": targetDir})

	case "app:check-for-updates":
		hasUpdate, release, err := a.updateMgr.CheckForUpdates()
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}

		if hasUpdate {
			wailsRuntime.EventsEmit(a.ctx, "app:update-available", map[string]interface{}{
				"currentVersion": appVersion,
				"latestVersion":  release.TagName,
				"releaseNotes":   release.Body,
				"downloadUrl":    release.HTMLURL,
				"assets":         release.Assets,
			})
		} else {
			wailsRuntime.EventsEmit(a.ctx, "app:update-not-available", map[string]interface{}{
				"currentVersion": appVersion,
			})
		}
		return marshalResponse(hasUpdate)

	case "app:start-download-update":
		var assets []update.ReleaseAsset
		if len(args) > 0 {
			bytesAssets, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesAssets, &assets)
		}

		destPath, err := a.updateMgr.DownloadUpdate(assets, func(percent int, downloaded, total int64) {
			wailsRuntime.EventsEmit(a.ctx, "app:download-progress", map[string]interface{}{
				"percent":    percent,
				"downloaded": downloaded,
				"total":      total,
			})
		})

		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "app:update-error", err.Error())
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}

		wailsRuntime.EventsEmit(a.ctx, "app:download-complete", destPath)
		return marshalResponse(destPath)

	case "app:get-version":
		return marshalResponse(appVersion)

	case "retry-error-logs:get":
		return marshalResponse(a.errLogger.GetLogs())

	case "retry-error-logs:clear":
		logType := getStringArg(0)
		a.errLogger.ClearLogs(logType)
		a.statsTracker.ClearRetriesOrErrors(logType)
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		return marshalResponse(true)

	case "retry-error-logs:export":
		logs := a.errLogger.GetLogs()
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出重试与报错日志",
			DefaultName: fmt.Sprintf("antigravity_retry_error_logs_%d.json", time.Now().Unix()),
			Filters: []dialogs.FileFilter{
				{DisplayName: "JSON Files", Pattern: "*.json"},
				{DisplayName: "CSV Files", Pattern: "*.csv"},
			},
		})
		if !ok {
			return marshalResponse(false)
		}

		var content []byte
		if strings.HasSuffix(filePath, ".csv") {
			var csv strings.Builder
			csv.WriteString("\uFEFF时间,类型,尝试/状态,账号,目标模型,接口路径,错误/异常详情\n")
			for _, log := range logs {
				logType := "最终失败"
				if log.Type == "RETRY" {
					logType = fmt.Sprintf("第 %d 次", log.Attempt)
				}
				csv.WriteString(fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n",
					log.Timestamp, log.Type, logType, log.Account, log.Model, log.Path, strings.ReplaceAll(log.Error, "\"", "\"\"")))
			}
			content = []byte(csv.String())
		} else {
			content, _ = json.MarshalIndent(logs, "", "  ")
		}

		_ = os.WriteFile(filePath, content, 0644)
		return marshalResponse(true)

	case "request-logs:export":
		logs, err := db.QueryAllRequestLogs()
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [请求日志导出] 查询数据库失败: %v", err))
			return marshalResponse(false)
		}
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出请求日志",
			DefaultName: fmt.Sprintf("antigravity_request_logs_%d.json", time.Now().Unix()),
			Filters: []dialogs.FileFilter{
				{DisplayName: "JSON Files", Pattern: "*.json"},
				{DisplayName: "CSV Files", Pattern: "*.csv"},
			},
		})
		if !ok {
			return marshalResponse(false)
		}

		var content []byte
		if strings.HasSuffix(filePath, ".csv") {
			var csv strings.Builder
			csv.WriteString("\uFEFF时间,模式,账号/用户,请求方式,域名,路径,模型,输入Token,输出Token,缓存Token,总成本,耗时(ms),状态码,会话ID\n")
			for _, log := range logs {
				formattedTime := log.Timestamp
				if t, err := time.Parse(time.RFC3339, log.Timestamp); err == nil {
					formattedTime = t.Local().Format("2006-01-02 15:04:05")
				}
				csv.WriteString(fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",%d,%d,%d,%f,%d,%d,\"%s\"\n",
					formattedTime, log.Mode, log.UserID, log.Method, log.Host, log.Path, log.ModelName,
					log.InTokens, log.OutTokens, log.CachedTokens, log.Cost, log.DurationMs, log.StatusCode, log.SessionID))
			}
			content = []byte(csv.String())
		} else {
			content, _ = json.MarshalIndent(logs, "", "  ")
		}

		_ = os.WriteFile(filePath, content, 0644)
		a.AddLog(fmt.Sprintf("📥 [请求日志导出] 成功导出请求日志到: %s", filePath))
		return marshalResponse(true)

	case "packet:get-all":
		return marshalResponse(a.packetCap.GetPackets())

	case "packet:analyze":
		accId := getStringArg(0)
		sourceType := getStringArg(1)
		if sourceType == "" {
			sourceType = "ALL"
		}
		markdown, err := a.packetCap.AnalyzePackets(accId, sourceType)
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}
		return marshalResponse(markdown)

	case "accounts:update-2fa":
		id := getStringArg(0)
		secret := getStringArg(1)

		if secret != "" {
			cleanSecret := strings.ReplaceAll(secret, " ", "")
			cleanSecret = strings.ToUpper(cleanSecret)
			if len(cleanSecret)%8 != 0 {
				cleanSecret += strings.Repeat("=", 8-(len(cleanSecret)%8))
			}
			_, err := base32.StdEncoding.DecodeString(cleanSecret)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": "无效的 Base32 格式，请检查密钥是否正确（支持包含空格）"})
			}
		}

		a.accountMgr.UpdateAccount2FASecret(id, secret)
		return marshalResponse(map[string]interface{}{"success": true})



	case "packet:download":
		markdown := getStringArg(0)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存 API 接口文档说明",
			DefaultName: "api_documentation.md",
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		return marshalResponse(true)

	case "packet:export-log":
		markdown := getStringArg(0)
		exportType := getStringArg(1)
		defaultName := fmt.Sprintf("api_packets_log_%s.md", strings.ToLower(exportType))
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存接口抓包日志",
			DefaultName: defaultName,
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		return marshalResponse(true)

	case "packet:export-single":
		markdown := getStringArg(0)
		method := getStringArg(1)
		pathStr := getStringArg(2)
		
		// Clean up pathStr to be a safe filename
		safePath := pathStr
		invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
		for _, char := range invalidChars {
			safePath = strings.ReplaceAll(safePath, char, "_")
		}
		safePath = strings.Trim(safePath, " _")
		if safePath == "" {
			safePath = "api"
		}
		
		defaultName := fmt.Sprintf("packet_%s_%s.md", strings.ToLower(method), safePath)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存单条接口抓包日志",
			DefaultName: defaultName,
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		return marshalResponse(true)
	}

	return `{"error":"Unknown channel"}`, nil
}

func (a *App) startMemoryMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	a.emitMemoryStats()

	trendCounter := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runtime.GC()
			debug.FreeOSMemory()
			if a.IsWindowVisibleAndActive() {
				a.emitMemoryStats()
			}

			trendCounter++
			if trendCounter >= 6 { // 6 * 10s = 60s
				trendCounter = 0
				if a.IsWindowVisibleAndActive() {
					wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
				}
			}
		}
	}
}

func (a *App) emitMemoryStats() {
	total, count, cpuPercent, err := stats.GetAppMemoryStats()
	if err != nil {
		return
	}

	// Read Go runtime actual heap allocation (actual in-use memory by Go objects)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	wailsRuntime.EventsEmit(a.ctx, "memory-stats-updated", map[string]interface{}{
		"total":        total,
		"processCount": count,
		"heapAlloc":    ms.HeapAlloc,
		"cpuUsage":     cpuPercent,
	})
}

// SetWindowVisible 线程安全地设置窗口可见状态
func (a *App) SetWindowVisible(v bool) {
	a.isWindowVisibleMu.Lock()
	a.isWindowVisible = v
	a.isWindowVisibleMu.Unlock()
}

// IsWindowVisibleAndActive 检查窗口是否在前台且可见（非最小化且未隐藏）
func (a *App) IsWindowVisibleAndActive() bool {
	if a.ctx == nil {
		return false
	}
	if wailsRuntime.WindowIsMinimised(a.ctx) {
		return false
	}
	a.isWindowVisibleMu.RLock()
	defer a.isWindowVisibleMu.RUnlock()
	return a.isWindowVisible
}

// getStatsPayload 获取隔离或原生的统计载荷快照
func (a *App) getStatsPayload(simplified bool) map[string]interface{} {
	usagePayload := a.usageTracker.GetPayload()
	if a.remoteRelay != nil && a.remoteRelay.GetConfig().Connected {
		cfg := a.remoteRelay.GetConfig()
		// No remote log syncing to local database anymore. All metrics are pre-aggregated and queried on-demand.

		if remoteStats, err := a.remoteRelay.FetchRemoteStats(); err == nil && remoteStats != nil {
			// 完全使用远端数据构建一套纯净的 GlobalStats
			statsObj := stats.GlobalStats{
				Models: make(map[string]*stats.ModelStats),
			}
			if tr, _ := remoteStats["totalRequests"].(float64); tr > 0 {
				statsObj.TotalRequests = int(tr)
			}
			if ti, _ := remoteStats["totalInputTokens"].(float64); ti > 0 {
				statsObj.TotalInputTokens = int(ti)
			}
			if to, _ := remoteStats["totalOutputTokens"].(float64); to > 0 {
				statsObj.TotalOutputTokens = int(to)
			}
			if tc, _ := remoteStats["totalCachedTokens"].(float64); tc > 0 {
				statsObj.TotalCachedTokens = int(tc)
			}
			if cost, _ := remoteStats["totalCost"].(float64); cost > 0 {
				statsObj.TotalCost = cost
			}

			if rmObj, ok := remoteStats["models"].(map[string]interface{}); ok {
				for k, vObj := range rmObj {
					if mObj, mok := vObj.(map[string]interface{}); mok {
						mStats := &stats.ModelStats{}
						if reqs, _ := mObj["requestCount"].(float64); reqs > 0 {
							mStats.Reqs = int(reqs)
						}
						if inT, _ := mObj["inputTokens"].(float64); inT > 0 {
							mStats.InTokens = int(inT)
						}
						if outT, _ := mObj["outputTokens"].(float64); outT > 0 {
							mStats.OutTokens = int(outT)
						}
						if cacheT, _ := mObj["cachedTokens"].(float64); cacheT > 0 {
							mStats.CachedTokens = int(cacheT)
						}
						if mc, _ := mObj["totalCost"].(float64); mc > 0 {
							mStats.Cost = mc
						}
						statsObj.Models[k] = mStats
					}
				}
			}

			// 恢复历史数据：从 SQLite 聚合出旧的 local trends（因为远端服务器升级前可能没有记录旧的历史）
			localTrends := db.QueryHourlyTrends(cfg.UserKey, "remote")
			
			trendMap := make(map[string]*stats.HourlyTrend)
			for _, dt := range localTrends {
				trendMap[dt.Time] = &stats.HourlyTrend{
					Time:       dt.Time,
					Input:      dt.Input,
					Output:     dt.Output,
					Cached:     dt.Cached,
					Requests:   dt.Requests,
					Cost:       dt.Cost,
					InputCost:  dt.InputCost,
					OutputCost: dt.OutputCost,
					CachedCost: dt.CachedCost,
				}
			}

			// Fetch hourly aggregated trends directly from the remote relay server
			if remoteTrends, err := a.remoteRelay.FetchRemoteTrends(); err == nil {
				for _, dt := range remoteTrends {
					// 远端数据优先级更高，覆盖本地（因为远端可能包含了其他设备共享的中继数据）
					trendMap[dt.Time] = &stats.HourlyTrend{
						Time:       dt.Time,
						Input:      dt.Input,
						Output:     dt.Output,
						Cached:     dt.Cached,
						Requests:   dt.Requests,
						Cost:       dt.Cost,
						InputCost:  dt.InputCost,
						OutputCost: dt.OutputCost,
						CachedCost: dt.CachedCost,
					}
				}
			}

			var trends []*stats.HourlyTrend
			var keys []string
			for k := range trendMap {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				trends = append(trends, trendMap[k])
			}

			if trends == nil {
				trends = []*stats.HourlyTrend{}
			}

			dbRequests := db.QueryRecentRequests(cfg.UserKey, "remote", 50)
			var requests []*stats.RequestLog
			for _, dr := range dbRequests {
				formattedTime := dr.Timestamp
				if t, err := time.Parse(time.RFC3339, dr.Timestamp); err == nil {
					formattedTime = t.Local().Format("01/02 15:04:05")
				}
				requests = append(requests, &stats.RequestLog{
					ID:           dr.ReqID,
					Timestamp:    formattedTime,
					Model:        dr.ModelName,
					InTokens:     dr.InTokens,
					OutTokens:    dr.OutTokens,
					CachedTokens: dr.CachedTokens,
					Cost:         dr.Cost,
					Account:      dr.UserID,
					DurationMs:   dr.DurationMs,
					StatusCode:   dr.StatusCode,
					Method:       dr.Method,
					Host:         dr.Host,
					Path:         dr.Path,
					SessionID:    dr.SessionID,
				})
			}
			if requests == nil {
				requests = []*stats.RequestLog{}
			}

			return map[string]interface{}{
				"stats":    statsObj,
				"trends":   trends,
				"requests": requests,
				"usage":    usagePayload,
			}
		}
	}

	// 本地模式或者远端获取失败时，保持完全的原生本地快照
	if simplified {
		return a.statsTracker.GetPayloadSimplified(usagePayload)
	}
	return a.statsTracker.GetPayload(usagePayload)
}

