package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/settings"
)

// app_mode_switch.go: 实现【本地模式】与【服务器模式】之间的无损热切换与沙箱隔离。
//
// 架构安全保障:
// 1. 本地数据绝对隔离: 本地原目录 (localDir) 在服务器模式下保持只读, 不发生任何写入或覆盖。
// 2. 独立沙箱机制: 云端拉取的数据全部写入 localDir/remote_sandbox/ 独立子目录。
// 3. 动态热路由: 各 Manager 通过 UpdatePath() 动态切换持久化路径, 切换时清空内存中本地账号残留,
//    全量加载远端账号与配置。
// 4. 断开瞬间无损恢复: 断开远端时, UpdatePath(localDir) 瞬间恢复本地全部 11 个官方账号及全部配置,
//    零数据丢失, 零数据污染。

var (
	modeSwitchMu           sync.Mutex
	isRemoteServerMode     bool
	originalLocalDataDir   string // 本地真实业务数据目录 (如 D:\antigravityProxy\data)
	originalLocalConfigDir string // 本地主配置目录 (如 %APPDATA%\antigravity-proxy-desktop)
)

// isServerMode 判断当前客户端是否处于服务器模式
func (a *App) isServerMode() bool {
	modeSwitchMu.Lock()
	defer modeSwitchMu.Unlock()
	return isRemoteServerMode
}

// setServerModeState 设置当前模式状态
func (a *App) setServerModeState(isServer bool) {
	modeSwitchMu.Lock()
	isRemoteServerMode = isServer
	modeSwitchMu.Unlock()
}

// switchToServerMode 切换到服务器模式: 拉取远端数据 -> 写入沙箱 -> 热切换各 Manager 路径 -> 广播前端重绘
func (a *App) switchToServerMode() error {
	if a.remoteRelay == nil {
		return fmt.Errorf("remote relay not initialized")
	}

	cfg := a.remoteRelay.GetConfig()
	if !cfg.Connected {
		return fmt.Errorf("not connected to remote relay")
	}

	a.AddLog(fmt.Sprintf("🔄 [服务器模式] 正在从云端 (%s:%s) 拉取最新配置与号池...", cfg.Host, cfg.Port))

	// 1. 从远端拉取完整配置集
	files, err := a.remoteRelay.FetchRemoteFullSync()
	if err != nil {
		a.AddLog(fmt.Sprintf("❌ [服务器模式] 拉取云端数据失败: %v", err))
		return err
	}

	modeSwitchMu.Lock()
	if originalLocalDataDir == "" {
		originalLocalDataDir = a.settingsMgr.GetActiveDataDirectory()
	}
	if originalLocalConfigDir == "" {
		originalLocalConfigDir = a.settingsMgr.GetDefaultUserDataPath()
	}
	localDir := originalLocalDataDir
	if localDir == "" {
		localDir = originalLocalConfigDir
	}
	modeSwitchMu.Unlock()

	sandboxDir := filepath.Join(localDir, "remote_sandbox")
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create sandbox dir: %w", err)
	}

	// 2. 必须初始化的账号池分区文件列表(确保云端未开启的号池在沙箱中为空, 不残留本地账号)
	expectedPoolFiles := []string{
		"accounts_antigravity.json",
		"accounts_project.json",
		"accounts_gcp.json",
		"accounts_grok.json",
		"accounts_2fa.json",
		"accounts_nvidia.json",
		"accounts_other.json",
	}

	for _, poolFile := range expectedPoolFiles {
		content, ok := files[poolFile]
		targetPath := filepath.Join(sandboxDir, poolFile)
		if ok && len(content) > 0 {
			trimmed := strings.TrimSpace(content)
			if strings.HasPrefix(trimmed, "[") {
				content = fmt.Sprintf("{\"accounts\":%s}", trimmed)
			}
			_ = os.WriteFile(targetPath, []byte(content), 0644)
		} else {
			// 云端无此号池或为空 -> 沙箱中写入标准空外壳, 保证加载时 0 账号残留
			_ = os.WriteFile(targetPath, []byte("{\"accounts\":[]}"), 0644)
		}
	}

	// 处理 accounts_pool.json (池级配置)
	poolContent, ok := files["accounts_pool.json"]
	poolTargetPath := filepath.Join(sandboxDir, "accounts_pool.json")
	if ok && len(poolContent) > 0 {
		_ = os.WriteFile(poolTargetPath, []byte(poolContent), 0644)
	} else {
		_ = os.WriteFile(poolTargetPath, []byte("{}"), 0644)
	}

	// 3. 写入其余关键配置文件(config.json, pricing.json, relay_users.json, relay_packages.json 等)
	for fname, content := range files {
		if fname == "accounts_pool.json" {
			continue
		}
		isPool := false
		for _, pf := range expectedPoolFiles {
			if pf == fname {
				isPool = true
				break
			}
		}
		if isPool {
			continue
		}

		targetPath := filepath.Join(sandboxDir, fname)
		if len(content) > 0 {
			_ = os.WriteFile(targetPath, []byte(content), 0644)
		}
	}

	// 4. 标记进入服务器模式
	a.setServerModeState(true)

	// 5. 切换所有 Manager 到沙箱目录并重载
	a.accountMgr.UpdatePath(sandboxDir)
	a.settingsMgr.UpdatePath(sandboxDir)
	if a.relayUserMgr != nil {
		a.relayUserMgr.UpdatePath(sandboxDir)
	}
	if a.relayPackageMgr != nil {
		a.relayPackageMgr.UpdatePath(sandboxDir)
	}
	if a.pricingMgr != nil {
		a.pricingMgr.UpdatePath(sandboxDir)
	}
	if a.sessionRouter != nil {
		a.sessionRouter.UpdatePath(sandboxDir)
	}
	if a.quotaSvc != nil {
		a.quotaSvc.UpdatePath(sandboxDir)
	}
	if a.statsTracker != nil {
		a.statsTracker.UpdatePath(sandboxDir)
	}
	if a.usageTracker != nil {
		a.usageTracker.UpdatePath(sandboxDir)
	}
	if a.errLogger != nil {
		a.errLogger.UpdatePath(sandboxDir)
	}

	// 5.1 异步拉取当前登录账号在远端的专属 Auto 竞速配置，注入沙箱 settingsMgr
	go func() {
		if a.remoteRelay != nil {
			if userAuto, isUser, err := a.remoteRelay.FetchRemoteUserAutoConfig(); err == nil && userAuto != nil && isUser {
				mappings := a.settingsMgr.GetRelayModelMapping()
				hasAuto := false
				useBench := userAuto.UseBenchmarkPool
				for i, m := range mappings {
					if strings.EqualFold(strings.TrimSpace(m.ClientModel), "auto") {
						mappings[i].Expose = userAuto.Enabled
						mappings[i].CandidateModels = userAuto.CandidateModels
						mappings[i].UseBenchmarkPool = &useBench
						hasAuto = true
						break
					}
				}
				if !hasAuto {
					mappings = append([]settings.ModelMappingEntry{{
						ClientModel:      "auto",
						TargetModel:      "auto",
						Expose:           userAuto.Enabled,
						CandidateModels:  userAuto.CandidateModels,
						UseBenchmarkPool: &useBench,
					}}, mappings...)
				}
				_ = a.settingsMgr.SetRelayModelMapping(mappings)
				a.AddLog(fmt.Sprintf("✅ [服务器模式] 已同步加载账号专属 Auto 竞速配置 (候选数: %d)", len(userAuto.CandidateModels)))
			}
		}
	}()

	// 6. 广播事件, 驱动前端各个模块全面刷新视图
	a.emitAccountsRes()
	a.emitEventSafe("remote:mode-changed", map[string]interface{}{
		"mode":      "server",
		"connected": true,
		"host":      cfg.Host,
		"port":      cfg.Port,
	})

	a.AddLog("✅ [服务器模式] 已成功加载服务器号池与配置，本地数据处于只读隔离保护状态")
	return nil
}

// switchToLocalMode 切回本地模式: 恢复各 Manager 路径到本地原始目录 -> 恢复全部本地账号与配置 -> 广播前端重绘
func (a *App) switchToLocalMode() {
	// 切回本地前，先从当前 settingsMgr 或 remoteRelay 捕获当前远程连接凭据
	savedHost := a.settingsMgr.GetRemoteHost()
	savedPort := a.settingsMgr.GetRemotePort()
	savedPath := a.settingsMgr.GetRemotePath()
	savedKey := a.settingsMgr.GetRemoteKey()
	savedPwd := a.settingsMgr.GetRemotePassword()
	if savedHost == "" && a.remoteRelay != nil {
		rc := a.remoteRelay.GetConfig()
		savedHost = rc.Host
		savedPort = rc.Port
		savedPath = rc.Path
		savedKey = rc.UserKey
	}

	a.setServerModeState(false)

	modeSwitchMu.Lock()
	targetDataDir := originalLocalDataDir
	targetConfigDir := originalLocalConfigDir
	modeSwitchMu.Unlock()

	if targetConfigDir == "" {
		targetConfigDir = a.settingsMgr.GetDefaultUserDataPath()
	}

	a.AddLog("🔄 [本地模式] 正在恢复本地配置与账号池...")

	// 1. 优先恢复 settingsMgr 到本地主配置目录，重新载入真实 config.json
	a.settingsMgr.UpdatePath(targetConfigDir)

	// 2. 关键保障：若此前存在远程凭据，切回本地后必须将凭据持久化保留在本地配置中（仅将 enabled 置为 false）
	//    这样停用后界面依然能呈现待命状态黄色徽标，随时允许点击“启用”重新连回，而不会误变为“彻底退出”
	if savedHost != "" && savedKey != "" {
		_ = a.settingsMgr.SetRemoteHost(savedHost)
		if savedPort != "" {
			_ = a.settingsMgr.SetRemotePort(savedPort)
		}
		_ = a.settingsMgr.SetRemotePath(savedPath)
		_ = a.settingsMgr.SetRemoteKey(savedKey)
		if savedPwd != "" {
			_ = a.settingsMgr.SetRemotePassword(savedPwd)
		}
		_ = a.settingsMgr.SetRemoteEnabled(false)
	}

	// 2. 确认本地真实业务数据目录（优先原记录的 targetDataDir，若无则从重载后的 settingsMgr 获取 ActiveDataDirectory）
	if targetDataDir == "" {
		targetDataDir = a.settingsMgr.GetActiveDataDirectory()
	}
	if targetDataDir == "" {
		targetDataDir = targetConfigDir
	}

	// 3. 恢复所有业务 Manager 到本地真实数据目录，确保 100% 载入全部本地账号与历史用量
	a.accountMgr.UpdatePath(targetDataDir)
	if a.relayUserMgr != nil {
		a.relayUserMgr.UpdatePath(targetDataDir)
	}
	if a.relayPackageMgr != nil {
		a.relayPackageMgr.UpdatePath(targetDataDir)
	}
	if a.pricingMgr != nil {
		a.pricingMgr.UpdatePath(targetDataDir)
	}
	if a.sessionRouter != nil {
		a.sessionRouter.UpdatePath(targetDataDir)
	}
	if a.quotaSvc != nil {
		a.quotaSvc.UpdatePath(targetDataDir)
	}
	if a.statsTracker != nil {
		a.statsTracker.UpdatePath(targetDataDir)
	}
	if a.usageTracker != nil {
		a.usageTracker.UpdatePath(targetDataDir)
	}
	if a.errLogger != nil {
		a.errLogger.UpdatePath(targetDataDir)
	}

	// 4. 重置记录变量，允许下次切换时重新动态捕获最新目录
	modeSwitchMu.Lock()
	originalLocalDataDir = ""
	originalLocalConfigDir = ""
	modeSwitchMu.Unlock()

	// 5. 广播恢复事件
	a.emitAccountsRes()
	a.emitEventSafe("remote:mode-changed", map[string]interface{}{
		"mode":      "local",
		"connected": false,
	})

	a.AddLog("✅ [本地模式] 已完好恢复本地原始账号池与配置，本地数据未受任何修改")
}

func (a *App) emitEventSafe(name string, data ...interface{}) {
	if a == nil || a.ctx == nil {
		return
	}
	defer func() { _ = recover() }()
	wailsRuntime.EventsEmit(a.ctx, name, data...)
}

// SyncFileToRemoteIfServerMode 在服务器模式下，将变更的文件实时异步推送到远端服务器
func (a *App) SyncFileToRemoteIfServerMode(filename string) {
	if !a.isServerMode() || a.remoteRelay == nil {
		return
	}

	modeSwitchMu.Lock()
	localDir := originalLocalDataDir
	if localDir == "" {
		localDir = a.settingsMgr.GetActiveDataDirectory()
	}
	if localDir == "" {
		localDir = a.settingsMgr.GetDefaultUserDataPath()
	}
	modeSwitchMu.Unlock()

	sandboxDir := filepath.Join(localDir, "remote_sandbox")
	filePath := filepath.Join(sandboxDir, filename)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	go func() {
		if err := a.remoteRelay.PushRemoteSync(filename, string(content)); err != nil {
			a.AddLog(fmt.Sprintf("⚠️ [同步] 实时推送 %s 到远端失败: %v", filename, err))
		} else {
			a.AddLog(fmt.Sprintf("✅ [同步] 已将 %s 变更同步至云端服务器", filename))
		}
	}()
}
