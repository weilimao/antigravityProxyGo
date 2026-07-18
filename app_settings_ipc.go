package main

import (
	"encoding/json"
	"fmt"

	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/settings"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) handleSettingsIPCSend(channel string, args []interface{}) bool {
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

	switch channel {
	case "settings:get-session-optimization":
		wailsRuntime.EventsEmit(a.ctx, "settings:session-optimization-res", a.settingsMgr.GetSessionOptimization())
		return true

	case "settings:set-session-optimization":
		var cfg settings.SessionOptimizationConfig
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &cfg)
		}
		_ = a.settingsMgr.SetSessionOptimization(cfg)
		a.AddLog("⚙️ 自定义会话压缩与优化配置已更新")
		return true

	case "settings:set-fallback-proxy-ports":
		_ = a.settingsMgr.SetFallbackProxyPorts(getStringArg(0))
		a.AddLog(fmt.Sprintf("⚙️ 自定义 Fallback 扫描端口已更新: %s", getStringArg(0)))
		return true

	case "settings:set-custom-socks5-address":
		_ = a.settingsMgr.SetCustomSocks5Address(getStringArg(0))
		a.AddLog(fmt.Sprintf("⚙️ 专属 SOCKS5 代理地址已更新: %s", getStringArg(0)))
		return true

	case "settings:set-custom-socks5-enabled":
		_ = a.settingsMgr.SetCustomSocks5Enabled(getBoolArg(0))
		status := "禁用"
		if getBoolArg(0) {
			status = "启用"
		}
		a.AddLog(fmt.Sprintf("⚙️ 专属 SOCKS5 代理状态已更新: %s", status))
		return true

	case "settings:set-custom-socks5-username":
		_ = a.settingsMgr.SetCustomSocks5Username(getStringArg(0))
		a.AddLog("⚙️ 专属 SOCKS5 用户名已更新")
		return true

	case "settings:set-custom-socks5-password":
		_ = a.settingsMgr.SetCustomSocks5Password(getStringArg(0))
		a.AddLog("⚙️ 专属 SOCKS5 密码已更新")
		return true

	case "settings:get-fallback-proxy-ports":
		wailsRuntime.EventsEmit(a.ctx, "settings:fallback-proxy-ports-res", a.settingsMgr.GetFallbackProxyPorts())
		return true

	case "settings:get-custom-socks5":
		wailsRuntime.EventsEmit(a.ctx, "settings:custom-socks5-res", map[string]interface{}{
			"address":  a.settingsMgr.GetCustomSocks5Address(),
			"enabled":  a.settingsMgr.GetCustomSocks5Enabled(),
			"username": a.settingsMgr.GetCustomSocks5Username(),
			"password": a.settingsMgr.GetCustomSocks5Password(),
		})
		return true

	case "settings:get-custom-socks5-username":
		wailsRuntime.EventsEmit(a.ctx, "settings:custom-socks5-username-res", a.settingsMgr.GetCustomSocks5Username())
		return true

	case "settings:get-custom-socks5-password":
		wailsRuntime.EventsEmit(a.ctx, "settings:custom-socks5-password-res", a.settingsMgr.GetCustomSocks5Password())
		return true

	case "settings:get-network-status":
		fallbackURL := ""
		if u := netutil.GetCachedLocalProxy(); u != nil {
			fallbackURL = u.String()
		}
		wailsRuntime.EventsEmit(a.ctx, "settings:network-status-res", map[string]interface{}{
			"customSocks5Address": a.settingsMgr.GetCustomSocks5Address(),
			"customSocks5Enabled": a.settingsMgr.GetCustomSocks5Enabled(),
			"cachedLocalProxy":    fallbackURL,
		})
		return true

	case "settings:get-network-logs":
		wailsRuntime.EventsEmit(a.ctx, "settings:network-logs-res", netutil.GetNetworkLogs())
		return true

	case "settings:set-prompt-prefix":
		_ = a.settingsMgr.SetPromptPrefix(getStringArg(0))
		a.AddLog("⚙️ 自定义提示词前缀已更新")
		return true

	case "settings:language-changed":
		lang := getStringArg(0)
		if lang != "" {
			_ = a.settingsMgr.SetLanguage(lang)
			a.AddLog(fmt.Sprintf("⚙️ 系统语言已更改为: %s", lang))
		}
		return true
	}
	return false
}
