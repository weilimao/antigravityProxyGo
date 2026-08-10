package main

import (
	"fmt"
	"path/filepath"

	"antigravity-proxy/internal/cert"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleAccountsSendIPC 处理 IPCSend 的账号/号池/通道/状态通道:账号同步移除启用\协同负载均衡\各号池
// 并发上限\通道切换\拦截开关\全局状态广播。从 app_ipc.go IPCSend 抽离,返回 bool 表已处理。
// 未命中返回 false 由 IPCSend fall-through 下一处理器。update-pricing 因引用 argsJSON 留 hub。
// 注:原 IPCSend case 体无 return(void),迁入 bool 子处理器后每 case 体末补 return true。
func (a *App) handleAccountsSendIPC(channel string, args []interface{}) bool {
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
		a.emitAccountsRes()
		return true

	case "accounts:remove", "nvidia:remove", "other:remove":
		id := getStringArg(0)
		if id != "" {
			a.accountMgr.RemoveAccount(id)
			a.AddLog(fmt.Sprintf("🗑️ [账号移除] 已成功移除账号 id=%s", id))
			a.emitAccountsRes()
		}
		return true

	case "accounts:toggle-enabled", "nvidia:toggle-enabled", "other:toggle-enabled":
		id := getStringArg(0)
		enabled := getBoolArg(1)
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		acc := a.accountMgr.GetAccountByID(id)
		if acc != nil {
			statusStr := "disabled"
			if enabled {
				statusStr = "enabled"
			}
			a.AddLog(fmt.Sprintf("🔄 Account %s is now %s in the pool.", acc.Email, statusStr))
		}
		a.emitAccountsRes()
		return true

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
		return true

	case "pool:toggle":
		a.accountMgr.SetPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Antigravity Load Balancing enabled. Distributing requests across accounts.")
		} else {
			a.AddLog("🔄 Antigravity Load Balancing disabled. Using a single active account.")
		}
		return true

	case "pool:toggle-project":
		a.accountMgr.SetProjectPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Project API Load Balancing enabled. Distributing requests across project accounts.")
		} else {
			a.AddLog("🔄 Project API Load Balancing disabled. Using a single active project account.")
		}
		return true

	case "pool:toggle-nvidia":
		a.accountMgr.SetNvidiaPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 NVIDIA Pool Load Balancing enabled. Distributing requests across NVIDIA accounts.")
		} else {
			a.AddLog("🔄 NVIDIA Pool Load Balancing disabled. Using a single active NVIDIA account.")
		}
		return true

	case "pool:toggle-other":
		// Other 号池(自定义多上游组)负载均衡总开关,与 nvidia 同构互斥。
		// 组内具体 LB 算法按 GroupID 维度配置(见 other:set-lb-mode)。
		a.accountMgr.SetOtherPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Other Pool Load Balancing enabled. Distributing requests across Other group accounts.")
		} else {
			a.AddLog("🔄 Other Pool Load Balancing disabled. Using a single active account per group.")
		}
		return true

	case "pool:toggle-grok":
		// Grok(x.ai) 号池负载均衡总开关,与 nvidia / other 同构互斥。
		// 池内具体 LB 算法(粘性/轮询)经 grok:set-lb-mode 配置。
		a.accountMgr.SetGrokPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Grok Pool Load Balancing enabled. Distributing requests across Grok accounts.")
		} else {
			a.AddLog("🔄 Grok Pool Load Balancing disabled. Using a single active Grok account.")
		}
		return true

	case "nvidia:set-lb-mode":
		mode := getStringArg(0)
		a.accountMgr.SetNvidiaLBMode(mode)
		a.AddLog("🔄 NVIDIA Load Balancing Algorithm switched to: " + a.accountMgr.GetNvidiaLBMode())
		a.emitAccountsRes()
		return true

	case "other:set-lb-mode":
		// args: [groupID, mode]。前端其他:下拉 ipcRenderer.send 走 IPCSend(非 invoke),
		// 必须在此处理,否则 SetOtherLBMode 永不被调用、粘性无法持久化、切回号池回退轮询。
		groupID := getStringArg(0)
		mode := getStringArg(1)
		a.accountMgr.SetOtherLBMode(groupID, mode)
		a.AddLog(fmt.Sprintf("🔄 [Other] group %s LB mode → %s", groupID, a.accountMgr.GetOtherLBMode(groupID)))
		a.emitAccountsRes()
		return true

	case "nvidia:set-max-concurrency":
		// 单账号在途并发上限:0=未配置回退默认 10;超过自动换号(超额降级最少并发号)。
		v := getIntArg(0)
		a.accountMgr.SetNvidiaMaxConcurrency(v)
		a.AddLog(fmt.Sprintf("🔄 NVIDIA Max Concurrency → %d", a.accountMgr.GetNvidiaMaxConcurrency()))
		a.emitAccountsRes()
		return true

	case "antigravity:set-max-concurrency":
		v := getIntArg(0)
		a.accountMgr.SetAntigravityMaxConcurrency(v)
		a.AddLog(fmt.Sprintf("🔄 Antigravity Max Concurrency → %d", a.accountMgr.GetAntigravityMaxConcurrency()))
		a.emitAccountsRes()
		return true

	case "project:set-max-concurrency":
		v := getIntArg(0)
		a.accountMgr.SetProjectMaxConcurrency(v)
		a.AddLog(fmt.Sprintf("🔄 Project Max Concurrency → %d", a.accountMgr.GetProjectMaxConcurrency()))
		a.emitAccountsRes()
		return true

	case "other:set-max-concurrency":
		// args: [groupID, value]。与 other:set-lb-mode 同走 IPCSend(前端组操作既有分布)。
		groupID := getStringArg(0)
		v := getIntArg(1)
		a.accountMgr.SetOtherMaxConcurrency(groupID, v)
		a.AddLog(fmt.Sprintf("🔄 [Other] group %s max concurrency → %d", groupID, a.accountMgr.GetOtherMaxConcurrency(groupID)))
		a.emitAccountsRes()
		return true

	case "grok:set-lb-mode":
		// args: [mode]。Grok 单池单值 LB 算法(与 nvidia:set-lb-mode 同构, 无组维度)。
		// 广播 accounts-res 让前端 grok tab 的 LB 下拉用最新值回填, 避免切 tab 还原陈旧值。
		mode := getStringArg(0)
		a.accountMgr.SetGrokLBMode(mode)
		a.AddLog("🔄 Grok Load Balancing Algorithm switched to: " + a.accountMgr.GetGrokLBMode())
		a.emitAccountsRes()
		return true

	case "grok:set-max-concurrency":
		// 单账号在途并发上限:0=未配置回退默认 10;超过自动换号(超额降级最少并发号)。
		v := getIntArg(0)
		a.accountMgr.SetGrokMaxConcurrency(v)
		a.AddLog(fmt.Sprintf("🔄 Grok Max Concurrency → %d", a.accountMgr.GetGrokMaxConcurrency()))
		a.emitAccountsRes()
		return true

	case "grok:set-cli-version":
		// Grok 号池全局 CLI 客户端版本号(单池单值,对仗 grok:set-max-concurrency)。
		// 用于发往 cli-chat-proxy.grok.com 上游的 x-grok-client-version 身份头(规避 426 版本闸门)。
		// 空串=未配置,GetGrokCliVersion 回退默认 DefaultGrokCliVersion("1.0.0")。
		v := getStringArg(0)
		a.accountMgr.SetGrokCliVersion(v)
		a.AddLog("🔄 Grok CLI Version → " + a.accountMgr.GetGrokCliVersion())
		a.emitAccountsRes()

		/* case "pool:toggle-gemini-cli":
		a.accountMgr.SetGeminiCliPoolMode(getBoolArg(0))
		if getBoolArg(0) {
			a.AddLog("🔄 Gemini CLI Load Balancing enabled. Distributing requests across Gemini CLI accounts.")
		} else {
			a.AddLog("🔄 Gemini CLI Load Balancing disabled. Using a single active Gemini CLI account.")
		} */
		return true

	case "channel:switch":
		a.accountMgr.SetActiveChannel(getStringArg(0))
		a.emitAccountsRes()
		a.AddLog("🔄 Switched active routing channel to: " + getStringArg(0))
		return true

	case "toggle":
		enable := getBoolArg(0)
		a.proxyEngine.SetMode(enable)
		_ = a.settingsMgr.SetIsInterceptMode(enable)
		if enable {
			a.AddLog("✅ Mode Switched: Intercept ON (Traffic buffering & retrying 503 errors)")
		} else {
			a.AddLog("✅ Mode Switched: Intercept OFF (Passthrough to Google directly)")
		}
		return true

	case "get-state":
		wailsRuntime.EventsEmit(a.ctx, "state", a.proxyEngine.IsInterceptMode())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.emitMemoryStats()
		a.logBufferMu.Lock()
		if len(a.logBuffer) > 0 {
			logsCopy := make([]string, len(a.logBuffer))
			copy(logsCopy, a.logBuffer)
			wailsRuntime.EventsEmit(a.ctx, "logs:batch", logsCopy)
		}
		a.logBufferMu.Unlock()
		{
			activeDir := a.settingsMgr.GetActiveDataDirectory()
			caPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
			wailsRuntime.EventsEmit(a.ctx, "cert-status-res", cert.CheckCertStatus(caPath))
		}
		return true

	}

	return false
}
