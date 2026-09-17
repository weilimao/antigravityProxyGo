package main

import (
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_emit.go: 统一的 accounts-res 事件 emit 辅助,集中补充 Other 号池字段,
// 避免 app_ipc.go / app_account_ipc.go / app_lifecycle.go 多处 emit 点逐一手写字段导致漏补。

// emitAccountsRes 向前端广播账号池状态快照(包含 Other 号池的 poolMode/groups)。
// 所有 other:xxx / accounts:xxx / nvidia:xxx 操作后均应调用此函数,保证前端收到一致的状态视图。
func (a *App) emitAccountsRes() {
	if a == nil || a.ctx == nil {
		return
	}
	defer func() { _ = recover() }()
	wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
		"accounts":                  a.accountMgr.GetAccounts(),
		"poolMode":                  a.accountMgr.GetPoolMode(),
		"projectPoolMode":           a.accountMgr.GetProjectPoolMode(),
		"geminiCliPoolMode":         a.accountMgr.GetGeminiCliPoolMode(),
		"nvidiaPoolMode":            a.accountMgr.GetNvidiaPoolMode(),
		"nvidiaLBMode":              a.accountMgr.GetNvidiaLBMode(),
		"otherPoolMode":             a.accountMgr.GetOtherPoolMode(),
		"otherGroups":               a.accountMgr.GetOtherGroups(),
		"grokPoolMode":              a.accountMgr.GetGrokPoolMode(),
		"grokLBMode":                a.accountMgr.GetGrokLBMode(),
		"grokMaxConcurrency":        a.accountMgr.GetGrokMaxConcurrency(),
		"grokCliVersion":            a.accountMgr.GetGrokCliVersion(),
		"grokQuotaCooldownHours":    a.accountMgr.GetGrokQuotaCooldownHours(),
		"activeChannel":             a.accountMgr.GetActiveChannel(),
		"nvidiaMaxConcurrency":      a.accountMgr.GetNvidiaMaxConcurrency(),
		"antigravityMaxConcurrency": a.accountMgr.GetAntigravityMaxConcurrency(),
		"antigravityCliVersion":     a.accountMgr.GetAntigravityCliVersion(),
		"projectMaxConcurrency":     a.accountMgr.GetProjectMaxConcurrency(),
		"workbuddyLBMode":           a.accountMgr.GetWorkBuddyLBMode(),
		"workbuddyMaxConcurrency":   a.accountMgr.GetWorkBuddyMaxConcurrency(),
		// Grok / Antigravity 两池 Worker 代理出口配置(号池单值)同步给前端工具栏回填。
		// 与 grokCliVersion/antigravityCliVersion 同范式,字段名复用设置层 JSON key 风格。
		"grokWorkerProxyEnabled":        a.settingsMgr.IsGrokWorkerProxyEnabled(),
		"grokWorkerProxyUrl":            a.settingsMgr.GetGrokWorkerProxyURL(),
		"antigravityWorkerProxyEnabled": a.settingsMgr.IsAntigravityWorkerProxyEnabled(),
		"antigravityWorkerProxyUrl":     a.settingsMgr.GetAntigravityWorkerProxyURL(),
	})
}
