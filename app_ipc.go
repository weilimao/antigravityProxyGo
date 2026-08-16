package main

import (
	"antigravity-proxy/internal/pricing"
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_ipc.go: 前端 IPC 路由 — IPCSend(单向事件下发)/IPCInvoke(双向调用分发)。
// 从 app.go 按职责拆分而出,同 main 包内共享 App 结构体与全局符号,物理搬移,逻辑逐行等价,零回归。

// IPCSend routes Electron's send requests
func (a *App) IPCSend(channel string, argsJSON string) {
	var args []interface{}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// 模块化分流：交给设置 IPC 处理器，防止 app.go 持续膨胀
	if a.handleSettingsIPCSend(channel, args) {
		return
	}
	if a.handleExternalConfigIPCSend(channel, args) {
		return
	}
	if a.handleMiscSendIPC(channel, args) {
		return
	}
	if a.handleAccountsSendIPC(channel, args) {
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

	switch channel {
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
	if res, handled, err := a.handlePricingInvokeIPC(channel, args); handled {
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
	if res, handled, err := a.handleAppInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handlePacketInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleIOInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleAuthInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleSettingsInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleExternalConfigInvokeIPC(channel, args); handled {
		return res, err
	}
	if res, handled, err := a.handleAccountsInvokeIPC(channel, args); handled {
		return res, err
	}

	return `{"error":"Unknown channel"}`, nil
}
