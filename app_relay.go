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
	"antigravity-proxy/internal/settings"

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

	case "relay:get-security-config":
		return marshalResponse(map[string]interface{}{
			"relaySSRFBlock":       a.settingsMgr.GetRelaySSRFBlock(),
			"relayPortBlock":       a.settingsMgr.GetRelayPortBlock(),
			"relayDomainFilter":    a.settingsMgr.GetRelayDomainFilter(),
			"relayDomainWhitelist": a.settingsMgr.GetRelayDomainWhitelist(),
		})

	case "relay:set-security-config":
		var config struct {
			SSRFBlock       bool     `json:"relaySSRFBlock"`
			PortBlock       bool     `json:"relayPortBlock"`
			DomainFilter    bool     `json:"relayDomainFilter"`
			DomainWhitelist []string `json:"relayDomainWhitelist"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &config)
		}

		_ = a.settingsMgr.SetRelaySSRFBlock(config.SSRFBlock)
		_ = a.settingsMgr.SetRelayPortBlock(config.PortBlock)
		_ = a.settingsMgr.SetRelayDomainFilter(config.DomainFilter)
		_ = a.settingsMgr.SetRelayDomainWhitelist(config.DomainWhitelist)

		a.proxyEngine.UpdateSecurityRules(
			config.SSRFBlock,
			config.PortBlock,
			config.DomainFilter,
			config.DomainWhitelist,
		)

		a.AddLog("🛡️ 中继服务网络安全规则已保存并热加载")
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-model-mapping":
		return marshalResponse(a.settingsMgr.GetRelayModelMapping())

	case "relay:set-model-mapping":
		var mapping []settings.ModelMappingEntry
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &mapping)
		}
		err := a.settingsMgr.SetRelayModelMapping(mapping)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog("🔄 中继大模型映射配置已保存")
		return marshalResponse(map[string]interface{}{"success": true})

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
			return marshalResponse(map[string]interface{}{
				"users": []interface{}{},
				"total": 0,
				"page":  1,
			})
		}

		var req struct {
			Page       int    `json:"page"`
			PageSize   int    `json:"pageSize"`
			Search     string `json:"search"`
			PackageTag string `json:"packageTag"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &req)
		}

		if req.Page <= 0 {
			req.Page = 1
		}
		if req.PageSize <= 0 {
			req.PageSize = 10
		}

		allUsers := a.relayUserMgr.GetUsers()
		var pkgs []*relay.RelayPackageTemplate
		if a.relayPackageMgr != nil {
			pkgs = a.relayPackageMgr.GetPackages()
		}

		var filtered []*relay.RelayUser
		for _, u := range allUsers {
			// 1. Search by account name (case-insensitive)
			if req.Search != "" {
				if !strings.Contains(strings.ToLower(u.Key), strings.ToLower(req.Search)) {
					continue
				}
			}

			// 2. Filter by package type
			if req.PackageTag != "" && req.PackageTag != "all" {
				pkgName := matchUserPackage(u.Quotas, pkgs)
				if req.PackageTag == "custom" {
					if pkgName != "custom" {
						continue
					}
				} else if req.PackageTag == "unlimited" {
					if pkgName != "unlimited" {
						continue
					}
				} else {
					if pkgName != req.PackageTag {
						continue
					}
				}
			}

			filtered = append(filtered, u)
		}

		total := len(filtered)
		start := (req.Page - 1) * req.PageSize
		end := start + req.PageSize
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}

		paginatedUsers := filtered[start:end]
		return marshalResponse(map[string]interface{}{
			"users": paginatedUsers,
			"total": total,
			"page":  req.Page,
		})

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
					claudeLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
				} else {
					geminiLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
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

	case "remote:get-keys":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		keys, err := a.remoteRelay.FetchRemoteKeys()
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "keys": keys})

	case "remote:create-key":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		name := getStringArg(0)
		key, err := a.remoteRelay.CreateRemoteKey(name)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "key": key})

	case "remote:delete-key":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		id := getStringArg(0)
		err := a.remoteRelay.DeleteRemoteKey(id)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})
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
	if a.relayStatsMgr != nil {
		a.relayStatsMgr.Close()
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
	a.relayAPIMgr = relay.NewAPIHandler(a.relayAuthMgr, a.relayStatsMgr, a.relayPackageMgr, a.AddLog, caCertPath)

	a.relayCompatAPIMgr = relay.NewAPICompatHandler(
		a.relayAuthMgr,
		a.accountMgr,
		a.sessionRouter,
		a.relayStatsMgr,
		a.settingsMgr,
		a.AddLog,
	)

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

	wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	return nil
}

// disconnectRemote 核心断开远程中继并恢复本地证书逻辑
func (a *App) disconnectRemote() {
	if a.remoteRelay != nil {
		a.remoteRelay.Disconnect()
		a.proxyEngine.SetRemoteRelay(nil)

		// 重新加载本地证书到内存中，使代理引擎能够继续正常解密签名
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
		"token":               remoteConfig.Token,
	}
}

// emitRemoteState broadcasts the complete remote state to the frontend
func (a *App) emitRemoteState() {
	wailsRuntime.EventsEmit(a.ctx, "remote-state", a.getRemoteStatusPayload())
}

// matchUserPackage returns the package name if user quotas match a template,
// otherwise returns "custom" or "unlimited".
func matchUserPackage(q relay.UserQuotas, pkgs []*relay.RelayPackageTemplate) string {
	for _, pkg := range pkgs {
		if checkQuotaEqual(q.Gemini, pkg.Quotas.Gemini) &&
			checkQuotaEqual(q.Claude, pkg.Quotas.Claude) &&
			q.ValidDuration == pkg.Quotas.ValidDuration &&
			q.ValidUnit == pkg.Quotas.ValidUnit {
			return pkg.Name
		}
	}
	if q.Gemini.EnableFixed || q.Gemini.EnableHourly || q.Gemini.EnableDaily ||
		q.Claude.EnableFixed || q.Claude.EnableHourly || q.Claude.EnableDaily {
		return "custom"
	}
	return "unlimited"
}

func checkQuotaEqual(q1, q2 relay.ModelQuota) bool {
	return q1.EnableFixed == q2.EnableFixed &&
		q1.FixedTokens == q2.FixedTokens &&
		q1.EnableHourly == q2.EnableHourly &&
		q1.HourlyHours == q2.HourlyHours &&
		q1.HourlyTokens == q2.HourlyTokens &&
		q1.EnableDaily == q2.EnableDaily &&
		q1.DailyDays == q2.DailyDays &&
		q1.DailyTokens == q2.DailyTokens
}
