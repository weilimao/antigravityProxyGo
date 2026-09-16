package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"antigravity-proxy/internal/cert"
	"antigravity-proxy/internal/patch"
	platformapi "antigravity-proxy/internal/platform/api"
	platformdb "antigravity-proxy/internal/platform/db"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/relay"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleRelayIPC handles relay server and remote connection IPC channels.
// Returns (response, handled, error).
func (a *App) handleRelayIPC(channel string, args []interface{}) (string, bool, error) {
	if res, handled, err := a.handleRelayConfigIPC(channel, args); handled {
		return res, true, err
	}
	if res, handled, err := a.handleRelayUsersIPC(channel, args); handled {
		return res, true, err
	}
	if res, handled, err := a.handleRelayRemoteIPC(channel, args); handled {
		return res, true, err
	}

	return "", false, nil
}

// startRelayServer initializes and starts the relay server on the specified port.
func (a *App) startRelayServer(port string) error {
	a.ensureRelayInitialized()

	if a.relayServer != nil && a.relayServer.IsRunning() {
		a.relayServer.Stop()
	}

	a.relayServer = relay.NewRelayServer(
		a.proxyEngine,
		a.relayAuthMgr,
		a.relayAPIMgr,
		a.relayCompatAPIMgr,
		a.AddLog,
		proxy.RelayUserCtxKey,
		proxy.RelayAPIKeyCtxKey,
	)

	// Pass empty strings for CA cert/key paths to default to HTTP.
	// TLS support is implemented in the server, but should only be enabled via a future UI toggle
	// rather than automatically hijacking the MITM proxy's CA certificates.
	if err := a.relayServer.Start(port, "", ""); err != nil {
		return err
	}

	scheme := "http"
	if a.relayServer.IsTLS() {
		scheme = "https"
	}
	a.AddLog(fmt.Sprintf("🚀 Relay server started on %s://0.0.0.0:%s", scheme, port))
	return nil
}

// stopRelayServer stops the relay server if it's running.
func (a *App) stopRelayServer() {
	if a.relayServer != nil && a.relayServer.IsRunning() {
		a.relayServer.Stop()
		a.AddLog("🛑 Relay server stopped")
	}
	if a.relayStatsMgr != nil {
		a.relayStatsMgr.Close()
	}
	platformdb.CloseDB()
}

// ensureRelayInitialized initializes relay components if not already done.
func (a *App) ensureRelayInitialized() {
	if a.relayUserMgr != nil || a.settingsMgr == nil {
		return
	}

	activeDir := a.settingsMgr.GetActiveDataDirectory()

	// Initialize platform database (dual-mode: SQLite / Remote MySQL)
	// We use the same host as RemoteHost if PlatformMySQLMode is true, 
	// with the specific port and credentials for the platform MySQL instance.
	go func() {
		err := platformdb.InitDB(platformdb.Config{
			DataDir:        activeDir,
			RemoteEnabled:  a.settingsMgr.GetPlatformMySQLMode(),
			RemoteHost:     a.settingsMgr.GetRemoteHost(),
			RemotePort:     "39306",
			RemoteUser:     "root",
			RemotePassword: "ProxySub2026SecDbPass99",
			RemoteDBName:   "antigravity_platform",
		})
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ Failed to initialize platform database: %v", err))
		} else {
			a.AddLog("✅ Platform database initialized successfully")
			if a.relayUserMgr != nil && platformdb.GlobalDB != nil {
				a.relayUserMgr.SetDB(platformdb.GlobalDB)
			}
		}
	}()

	a.relayUserMgr = relay.NewUserManager()
	a.relayUserMgr.Init(activeDir)
	if platformdb.GlobalDB != nil {
		a.relayUserMgr.SetDB(platformdb.GlobalDB)
	}

	a.relayPackageMgr = relay.NewPackageManager()
	a.relayPackageMgr.Init(activeDir)

	a.relayAuthMgr = relay.NewAuthManager(a.relayUserMgr)

	a.relayStatsMgr = relay.NewStatsTracker(a.pricingMgr)
	a.relayStatsMgr.Init(activeDir)

	caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
	a.relayAPIMgr = relay.NewAPIHandler(a.relayAuthMgr, a.relayStatsMgr, a.relayPackageMgr, a.AddLog, caCertPath, a.settingsMgr, a.accountMgr)
	a.relayAPIMgr.SetGlobalStatsTracker(a.statsTracker)
	
	// Inject platform API Gin router
	a.relayAPIMgr.SetPlatformRouter(platformapi.SetupRouter())

	a.relayCompatAPIMgr = relay.NewAPICompatHandler(
		a.relayAuthMgr,
		a.accountMgr,
		a.sessionRouter,
		a.relayStatsMgr,
		a.usageTracker,
		a.settingsMgr,
		a.AddLog,
	)
	// 注入全局 stats.Tracker, 使 NVIDIA 中继链路能将号池用量计入
	// 「使用趋势-NVIDIA」专用桶 (TrackNvidiaRequest), 与综合趋势桶隔离。
	a.relayCompatAPIMgr.SetGlobalStatsTracker(a.statsTracker)

	// 绑定 OCR 引擎的跨号池路由解析:使 OCR 模型下拉选中带前缀的非 Google 模型
	// (如 nvidia/xxx、other/openai/xxx)时,OCR 出站能按模型映射路由到对应号池,
	// 而不是死绑定 Google 家族(18443)。依赖 relayCompatAPIMgr.resolveRoutedTarget,
	// 故必须在 NewAPICompatHandler 之后调用。
	a.relayCompatAPIMgr.WireOcrRouteResolver()

	// Start session cleanup timer
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.relayAuthMgr.CleanExpired()
			}
		}
	}()
}

// connectRemote 核心远程中继登录连接逻辑
func (a *App) connectRemote(host, port, path, key, password string) error {
	if a.remoteRelay == nil {
		a.remoteRelay = proxy.NewRemoteRelay(a.AddLog)
	}

	if err := a.remoteRelay.Login(host, port, path, key, password); err != nil {
		return err
	}

	// Register auto-relogin callback for token expiry
	a.remoteRelay.SetOnTokenExpired(func() {
		a.AddLog("🔄 Token expired, attempting auto-relogin...")
		savedKey := a.settingsMgr.GetRemoteKey()
		savedPwd := a.settingsMgr.GetRemotePassword()
		savedHost := a.settingsMgr.GetRemoteHost()
		savedPort := a.settingsMgr.GetRemotePort()
		savedPath := a.settingsMgr.GetRemotePath()
		if savedKey == "" || savedPwd == "" || savedHost == "" {
			a.AddLog("⚠️ Cannot auto-relogin: no saved credentials")
			return
		}
		if err := a.remoteRelay.Login(savedHost, savedPort, savedPath, savedKey, savedPwd); err != nil {
			a.AddLog(fmt.Sprintf("❌ Auto-relogin failed: %v", err))
		} else {
			a.AddLog("✅ Auto-relogin successful")
		}
	})

	// 注入自动重连回调:健康检查探测到链路恢复、或外部网络监听器
	// 触发重连时调用。复用保存凭据,经 reconnectRemoteSafely 串行化
	// 避免与网络恢复回调并发重复 Login。uses the saved credentials to re-login.
	a.remoteRelay.SetAutoReconnectFn(func() {
		savedHost := a.settingsMgr.GetRemoteHost()
		savedPort := a.settingsMgr.GetRemotePort()
		savedPath := a.settingsMgr.GetRemotePath()
		savedKey := a.settingsMgr.GetRemoteKey()
		savedPwd := a.settingsMgr.GetRemotePassword()
		if savedHost == "" || savedKey == "" {
			a.AddLog("⚠️ [自动重连] 无保存的远程中继凭据,跳过自动重连")
			return
		}
		if err := a.reconnectRemoteSafely(savedHost, savedPort, savedPath, savedKey, savedPwd); err != nil {
			a.AddLog(fmt.Sprintf("❌ [自动重连] 远程中继重连失败: %v", err))
		} else {
			a.AddLog("✅ [自动重连] 远程中继重连成功")
		}
	})

	// Set remote relay on proxy engine
	a.proxyEngine.SetRemoteRelay(a.remoteRelay)

	// Download remote server's CA cert for trust chain
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	remoteCACertPath := filepath.Join(activeDir, "certs", "certs", "remote_ca.pem")
	localCACertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")

	if err := a.remoteRelay.DownloadCACert(remoteCACertPath); err == nil {
		a.AddLog("✅ 成功下载远端 CA 证书")

		// 1. 合并证书逻辑：读取本地 ca.pem 和下载 of remote_ca.pem
		localData, errReadLocal := os.ReadFile(localCACertPath)
		remoteData, errReadRemote := os.ReadFile(remoteCACertPath)

		if errReadLocal == nil && errReadRemote == nil {
			// 检查 localData 中是否已经包含 remoteData
			if !strings.Contains(string(localData), string(remoteData)) {
				// 将 remoteData 追加到 localData 中
				combined := append(localData, []byte("\n")...)
				combined = append(combined, remoteData...)
				if errWrite := os.WriteFile(localCACertPath, combined, 0644); errWrite == nil {
					a.AddLog("💾 已将远端中继 CA 证书合并至本地 ca.pem")
				} else {
					a.AddLog(fmt.Sprintf("⚠️ 合并远端证书失败: %v", errWrite))
				}
			} else {
				a.AddLog("ℹ️ 本地 ca.pem 已包含远端 CA 证书，无需重复合并")
			}
		}

		// 2. 重载本地代理证书并注入 IDE 环境
		homeDir, _ := os.UserHomeDir()
		defaultUserData := a.settingsMgr.GetDefaultUserDataPath()
		go func() {
			// 重新将合并后的 ca.pem 注入到 IDE 的运行环境
			_ = patch.PatchAll(true, defaultUserData, homeDir, localCACertPath, a.AddLog)

			// 检查远端证书系统信任状态并异步导入
			if !cert.CheckCertStatus(remoteCACertPath) {
				a.AddLog("🛡️ 正在向操作系统信任库导入远端中继根证书...")
				_, errStr := cert.InstallCert(remoteCACertPath)
				if errStr != "" {
					a.AddLog(fmt.Sprintf("⚠️ 自动导入系统证书库提示: %s", errStr))
				} else {
					a.AddLog("✅ 远端中继根证书已成功导入系统受信任存储区")
				}
			} else {
				a.AddLog("✅ 远端中继根证书已处于系统受信任状态，无需重复导入")
			}
		}()

		// 3. 重载代理引擎的证书
		if errReload := a.proxyEngine.ReloadCertificates(activeDir); errReload != nil {
			a.AddLog(fmt.Sprintf("⚠️ 代理引擎重载证书失败: %v", errReload))
		}
	} else {
		a.AddLog(fmt.Sprintf("⚠️ 下载远端 CA 证书失败: %v，将跳过远端证书的合并与系统导入", err))
	}

	// 切换至服务器模式：拉取目标服务实例数据并接管配置（独立沙箱隔离保护本地数据）
	if errSync := a.switchToServerMode(); errSync != nil {
		a.AddLog(fmt.Sprintf("⚠️ [服务器模式] 数据同步失败: %v", errSync))
	} else {
		a.AddLog("✅ 已成功切换至服务器模式")
	}

	wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	return nil
}

// disconnectRemote 核心断开远程中继并恢复本地证书逻辑
func (a *App) disconnectRemote() {
	if a.remoteRelay != nil {
		a.remoteRelay.Disconnect()
		a.proxyEngine.SetRemoteRelay(nil)
		a.proxyEngine.ResetRemoteClient()

		// 1. 优先切回本地模式：全面恢复本地原本全部数据、配置与各 Manager 路径
		a.switchToLocalMode()

		// 2. 从已彻底恢复的本地真实数据目录中重新加载本地证书到内存中，使代理引擎继续正常解密签名
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		if errReload := a.proxyEngine.ReloadCertificates(activeDir); errReload != nil {
			a.AddLog(fmt.Sprintf("⚠️ 重新加载本地 CA 证书失败: %v", errReload))
		} else {
			a.AddLog("✅ 代理引擎已成功重载本地 CA 证书")
		}

		a.AddLog("🔄 已断开远程中继并切换至本地代理模式")
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	}
}

// getRemoteStatusPayload returns the current remote config and status dictionary
func (a *App) getRemoteStatusPayload() map[string]interface{} {
	connected := false
	var remoteConfig proxy.RemoteConfig
	if a.remoteRelay != nil {
		remoteConfig = a.remoteRelay.GetConfig()
		connected = remoteConfig.Connected
	}

	savedHost := a.settingsMgr.GetRemoteHost()
	savedPort := a.settingsMgr.GetRemotePort()
	savedPath := a.settingsMgr.GetRemotePath()
	savedKey := a.settingsMgr.GetRemoteKey()
	if savedHost == "" && remoteConfig.Host != "" {
		savedHost = remoteConfig.Host
	}
	if savedPort == "" && remoteConfig.Port != "" {
		savedPort = remoteConfig.Port
	}
	if savedPath == "" && remoteConfig.Path != "" {
		savedPath = remoteConfig.Path
	}
	if savedKey == "" && remoteConfig.UserKey != "" {
		savedKey = remoteConfig.UserKey
	}
	hasSaved := savedHost != "" && savedKey != ""

	return map[string]interface{}{
		"connected":           connected,
		"hasSavedCredentials": hasSaved,
		"savedHost":           savedHost,
		"savedPort":           savedPort,
		"savedPath":           savedPath,
		"savedKey":            savedKey,
		"remoteEnabled":       a.settingsMgr.GetRemoteEnabled(),
		"host":                remoteConfig.Host,
		"port":                remoteConfig.Port,
		"path":                remoteConfig.Path,
		"userKey":             remoteConfig.UserKey,
	}
}

// emitRemoteState broadcasts the complete remote state to the frontend
func (a *App) emitRemoteState() {
	wailsRuntime.EventsEmit(a.ctx, "remote-state", a.getRemoteStatusPayload())
}
