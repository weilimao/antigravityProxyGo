package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"antigravity-proxy/internal/cert"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/patch"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/relay"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleRelayIPC handles relay server and remote connection IPC channels.
// Returns (response, handled, error).
func (a *App) handleRelayIPC(channel string, args []interface{}) (string, bool, error) {
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

	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {

	// ========== Relay Server Management ==========

	case "relay:get-config":
		return marshalResponse(map[string]interface{}{
			"enabled": a.settingsMgr.GetRelayEnabled(),
			"port":    a.settingsMgr.GetRelayPort(),
		})

	case "relay:set-config":
		var config struct {
			Enabled bool   `json:"enabled"`
			Port    string `json:"port"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &config)
		}

		if config.Port == "" {
			config.Port = "18444"
		}
		_ = a.settingsMgr.SetRelayEnabled(config.Enabled)
		_ = a.settingsMgr.SetRelayPort(config.Port)

		if config.Enabled {
			if err := a.startRelayServer(config.Port); err != nil {
				a.AddLog(fmt.Sprintf("❌ Failed to start relay server: %v", err))
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		} else {
			a.stopRelayServer()
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-users":
		a.ensureRelayInitialized()
		if a.relayUserMgr == nil {
			return marshalResponse([]interface{}{})
		}
		return marshalResponse(a.relayUserMgr.GetUsers())

	case "relay:add-user":
		key := getStringArg(0)
		password := getStringArg(1)
		remark := getStringArg(2)

		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		user, err := a.relayUserMgr.AddUser(key, password, remark)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("🔑 Relay user added: %s", key))
		return marshalResponse(map[string]interface{}{"success": true, "user": user})

	case "relay:remove-user":
		userId := getStringArg(0)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		if err := a.relayUserMgr.RemoveUser(userId); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("🗑️ Relay user removed: %s", userId))
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:toggle-user":
		userId := getStringArg(0)
		enabled := getBoolArg(1)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		a.relayUserMgr.UpdateUserEnabled(userId, enabled)
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:update-user-quota":
		userId := getStringArg(0)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}
		var quotas relay.UserQuotas
		if len(args) > 1 {
			b, _ := json.Marshal(args[1])
			_ = json.Unmarshal(b, &quotas)
		}
		err := a.relayUserMgr.UpdateUserQuota(userId, quotas)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-packages":
		a.ensureRelayInitialized()
		if a.relayPackageMgr == nil {
			return marshalResponse([]interface{}{})
		}
		return marshalResponse(a.relayPackageMgr.GetPackages())
		
	case "relay:save-package":
		if a.relayPackageMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not initialized"})
		}
		var pkg relay.RelayPackageTemplate
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &pkg)
		}
		if pkg.ID == "" {
			_, err := a.relayPackageMgr.AddPackage(pkg.Name, pkg.Quotas)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		} else {
			err := a.relayPackageMgr.UpdatePackage(pkg.ID, pkg.Name, pkg.Quotas)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		}
		return marshalResponse(map[string]interface{}{"success": true})
		
	case "relay:delete-package":
		if a.relayPackageMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not initialized"})
		}
		id := getStringArg(0)
		err := a.relayPackageMgr.DeletePackage(id)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-user-stats":
		userId := getStringArg(0)
		a.ensureRelayInitialized()
		if a.relayStatsMgr == nil {
			return marshalResponse(nil)
		}
		stats := a.relayStatsMgr.GetUserStats(userId)
		var geminiLifetime, claudeLifetime int64
		if stats != nil {
			for mName, mStats := range stats.Models {
				if strings.Contains(strings.ToLower(mName), "claude") {
					claudeLifetime += int64(mStats.InputTokens + mStats.OutputTokens + mStats.CachedTokens)
				} else {
					geminiLifetime += int64(mStats.InputTokens + mStats.OutputTokens + mStats.CachedTokens)
				}
			}
		}

		var geminiHourlyUsed, geminiDailyUsed int64
		var claudeHourlyUsed, claudeDailyUsed int64
		var geminiHourlyResetAt, claudeHourlyResetAt string
		var geminiDailyResetAt, claudeDailyResetAt string
		user := a.relayUserMgr.GetUserByID(userId)
		if user != nil {
			if user.Quotas.Gemini.EnableHourly && user.Quotas.Gemini.HourlyHours > 0 {
				since := time.Now().Add(-time.Duration(user.Quotas.Gemini.HourlyHours) * time.Hour).Format(time.RFC3339)
				geminiHourlyUsed, _ = db.GetTokensForUserModelFamilySince(userId, "gemini", since)
				if geminiHourlyUsed > 0 {
					if firstTs, err := db.GetOldestRequestTimestampSince(userId, "gemini", since); err == nil && firstTs != "" {
						if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
							geminiHourlyResetAt = parsed.Add(time.Duration(user.Quotas.Gemini.HourlyHours) * time.Hour).Format(time.RFC3339)
						}
					}
				}
			}
			if user.Quotas.Gemini.EnableDaily && user.Quotas.Gemini.DailyDays > 0 {
				since := time.Now().Add(-time.Duration(user.Quotas.Gemini.DailyDays*24) * time.Hour).Format(time.RFC3339)
				geminiDailyUsed, _ = db.GetTokensForUserModelFamilySince(userId, "gemini", since)
				if geminiDailyUsed > 0 {
					if firstTs, err := db.GetOldestRequestTimestampSince(userId, "gemini", since); err == nil && firstTs != "" {
						if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
							geminiDailyResetAt = parsed.Add(time.Duration(user.Quotas.Gemini.DailyDays*24) * time.Hour).Format(time.RFC3339)
						}
					}
				}
			}

			if user.Quotas.Claude.EnableHourly && user.Quotas.Claude.HourlyHours > 0 {
				since := time.Now().Add(-time.Duration(user.Quotas.Claude.HourlyHours) * time.Hour).Format(time.RFC3339)
				claudeHourlyUsed, _ = db.GetTokensForUserModelFamilySince(userId, "claude", since)
				if claudeHourlyUsed > 0 {
					if firstTs, err := db.GetOldestRequestTimestampSince(userId, "claude", since); err == nil && firstTs != "" {
						if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
							claudeHourlyResetAt = parsed.Add(time.Duration(user.Quotas.Claude.HourlyHours) * time.Hour).Format(time.RFC3339)
						}
					}
				}
			}
			if user.Quotas.Claude.EnableDaily && user.Quotas.Claude.DailyDays > 0 {
				since := time.Now().Add(-time.Duration(user.Quotas.Claude.DailyDays*24) * time.Hour).Format(time.RFC3339)
				claudeDailyUsed, _ = db.GetTokensForUserModelFamilySince(userId, "claude", since)
				if claudeDailyUsed > 0 {
					if firstTs, err := db.GetOldestRequestTimestampSince(userId, "claude", since); err == nil && firstTs != "" {
						if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
							claudeDailyResetAt = parsed.Add(time.Duration(user.Quotas.Claude.DailyDays*24) * time.Hour).Format(time.RFC3339)
						}
					}
				}
			}
		}

		return marshalResponse(map[string]interface{}{
			"stats":               stats,
			"user":                user,
			"geminiLifetime":      geminiLifetime,
			"geminiHourlyUsed":    geminiHourlyUsed,
			"geminiDailyUsed":     geminiDailyUsed,
			"claudeLifetime":      claudeLifetime,
			"claudeHourlyUsed":    claudeHourlyUsed,
			"claudeDailyUsed":     claudeDailyUsed,
			"geminiHourlyResetAt": geminiHourlyResetAt,
			"claudeHourlyResetAt": claudeHourlyResetAt,
			"geminiDailyResetAt":  geminiDailyResetAt,
			"claudeDailyResetAt":  claudeDailyResetAt,
		})

	// ========== Remote Connection (Client Mode) ==========

	case "remote:login":
		host := getStringArg(0)
		port := getStringArg(1)
		key := getStringArg(2)
		password := getStringArg(3)

		if port == "" {
			port = "18444"
		}

		if err := a.connectRemote(host, port, key, password); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		// Save config to settings
		_ = a.settingsMgr.SetRemoteHost(host)
		_ = a.settingsMgr.SetRemotePort(port)
		_ = a.settingsMgr.SetRemoteKey(key)
		_ = a.settingsMgr.SetRemotePassword(password)
		_ = a.settingsMgr.SetRemoteEnabled(true)

		a.AddLog(fmt.Sprintf("🌐 Remote connected to %s:%s as %s", host, port, key))
		a.emitRemoteState()

		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:disconnect":
		a.disconnectRemote()
		_ = a.settingsMgr.SetRemoteHost("")
		_ = a.settingsMgr.SetRemotePort("")
		_ = a.settingsMgr.SetRemoteKey("")
		_ = a.settingsMgr.SetRemotePassword("")
		_ = a.settingsMgr.SetRemoteEnabled(false)
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:disable":
		a.disconnectRemote()
		_ = a.settingsMgr.SetRemoteEnabled(false)
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:enable":
		host := a.settingsMgr.GetRemoteHost()
		port := a.settingsMgr.GetRemotePort()
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host == "" || key == "" {
			return marshalResponse(map[string]interface{}{"success": false, "error": "没有已保存的远程连接凭据"})
		}
		if err := a.connectRemote(host, port, key, pwd); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		_ = a.settingsMgr.SetRemoteEnabled(true)
		a.AddLog(fmt.Sprintf("🌐 Remote re-connected to %s:%s as %s", host, port, key))
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:get-status":
		return marshalResponse(a.getRemoteStatusPayload())

	case "remote:test":
		host := getStringArg(0)
		port := getStringArg(1)
		if port == "" {
			port = "18444"
		}

		testRelay := proxy.NewRemoteRelay(nil)
		start := time.Now()
		if err := testRelay.TestConnection(host, port); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		latency := time.Since(start).Milliseconds()
		return marshalResponse(map[string]interface{}{"success": true, "latencyMs": latency})

	case "remote:sync-stats":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(nil)
		}
		stats, err := a.remoteRelay.FetchRemoteStats()
		if err != nil {
			a.AddLog(fmt.Sprintf("⚠️ Remote stats sync failed: %v", err))
			return marshalResponse(nil)
		}
		return marshalResponse(stats)
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
		a.AddLog,
		proxy.RelayUserCtxKey,
	)

	if err := a.relayServer.Start(port); err != nil {
		return err
	}

	a.AddLog(fmt.Sprintf("🚀 Relay server started on port %s", port))
	return nil
}

// stopRelayServer stops the relay server if it's running.
func (a *App) stopRelayServer() {
	if a.relayServer != nil && a.relayServer.IsRunning() {
		a.relayServer.Stop()
		a.AddLog("🛑 Relay server stopped")
	}
}

// ensureRelayInitialized initializes relay components if not already done.
func (a *App) ensureRelayInitialized() {
	if a.relayUserMgr != nil || a.settingsMgr == nil {
		return
	}

	activeDir := a.settingsMgr.GetActiveDataDirectory()

	a.relayUserMgr = relay.NewUserManager()
	a.relayUserMgr.Init(activeDir)

	a.relayPackageMgr = relay.NewPackageManager()
	a.relayPackageMgr.Init(activeDir)

	a.relayAuthMgr = relay.NewAuthManager(a.relayUserMgr)

	a.relayStatsMgr = relay.NewStatsTracker(a.pricingMgr)
	a.relayStatsMgr.Init(activeDir)

	caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
	a.relayAPIMgr = relay.NewAPIHandler(a.relayAuthMgr, a.relayStatsMgr, a.AddLog, caCertPath)

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
func (a *App) connectRemote(host, port, key, password string) error {
	if a.remoteRelay == nil {
		a.remoteRelay = proxy.NewRemoteRelay(a.AddLog)
	}

	if err := a.remoteRelay.Login(host, port, key, password); err != nil {
		return err
	}

	// Set remote relay on proxy engine
	a.proxyEngine.SetRemoteRelay(a.remoteRelay)

	// Download remote server's CA cert for trust chain
	activeDir := a.settingsMgr.GetActiveDataDirectory()
	remoteCACertPath := filepath.Join(activeDir, "certs", "certs", "remote_ca.pem")
	if err := a.remoteRelay.DownloadCACert(remoteCACertPath); err != nil {
		a.AddLog(fmt.Sprintf("⚠️ Failed to download remote CA cert: %v", err))
	} else {
		// Overwrite local ca.pem with remote one for MITM trust
		localCACertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		if remoteData, readErr := os.ReadFile(remoteCACertPath); readErr == nil {
			_ = os.WriteFile(localCACertPath, remoteData, 0644)
			a.AddLog("📜 Remote CA cert installed as local CA cert")

			// 重新注入 CA 证书到系统/IDE运行环境
			a.AddLog("🚀 正在将远程 CA 证书重载并注入至 IDE 及系统环境...")
			homeDir, _ := os.UserHomeDir()
			defaultUserData := a.settingsMgr.GetDefaultUserDataPath()
			go func() {
				_ = patch.PatchAll(true, defaultUserData, homeDir, localCACertPath, a.AddLog)
				if !cert.CheckCertStatus(localCACertPath) {
					a.AddLog("🛡️ 正在向操作系统信任库自动导入远端中继根证书...")
					_, errStr := cert.InstallCert(localCACertPath)
					if errStr != "" {
						a.AddLog(fmt.Sprintf("⚠️ 自动导入系统证书库提示: %s", errStr))
					} else {
						a.AddLog("✅ 远端中继根证书已成功导入系统受信任存储区")
					}
				}
			}()
		}
	}

	wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	return nil
}

// disconnectRemote 核心断开远程中继并恢复本地证书逻辑
func (a *App) disconnectRemote() {
	if a.remoteRelay != nil {
		a.remoteRelay.Disconnect()
		a.proxyEngine.SetRemoteRelay(nil)

		// 还原重载本地 CA 证书到系统/IDE运行环境
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		localCACertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		a.AddLog("🔄 正在恢复本地 CA 证书信任链及系统环境...")
		homeDir, _ := os.UserHomeDir()
		defaultUserData := a.settingsMgr.GetDefaultUserDataPath()
		go func() {
			_ = patch.PatchAll(true, defaultUserData, homeDir, localCACertPath, a.AddLog)
			if !cert.CheckCertStatus(localCACertPath) {
				a.AddLog("🛡️ 正在向操作系统信任库还原导入本地根证书...")
				_, errStr := cert.InstallCert(localCACertPath)
				if errStr != "" {
					a.AddLog(fmt.Sprintf("⚠️ 还原系统证书库提示: %s", errStr))
				} else {
					a.AddLog("✅ 本地根证书已成功导入系统受信任存储区")
				}
			}
		}()

		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	}
}

// getRemoteStatusPayload returns the current remote config and status dictionary
func (a *App) getRemoteStatusPayload() map[string]interface{} {
	hasSaved := a.settingsMgr.GetRemoteHost() != "" && a.settingsMgr.GetRemoteKey() != ""
	connected := false
	var remoteConfig proxy.RemoteConfig
	if a.remoteRelay != nil {
		remoteConfig = a.remoteRelay.GetConfig()
		connected = remoteConfig.Connected
	}
	return map[string]interface{}{
		"connected":           connected,
		"hasSavedCredentials": hasSaved,
		"savedHost":           a.settingsMgr.GetRemoteHost(),
		"savedPort":           a.settingsMgr.GetRemotePort(),
		"savedKey":            a.settingsMgr.GetRemoteKey(),
		"remoteEnabled":       a.settingsMgr.GetRemoteEnabled(),
		"host":                remoteConfig.Host,
		"port":                remoteConfig.Port,
		"userKey":             remoteConfig.UserKey,
	}
}

// emitRemoteState broadcasts the complete remote state to the frontend
func (a *App) emitRemoteState() {
	wailsRuntime.EventsEmit(a.ctx, "remote-state", a.getRemoteStatusPayload())
}
