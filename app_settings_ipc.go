package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/relay"
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

	case "settings:get-ocr-model":
		wailsRuntime.EventsEmit(a.ctx, "settings:ocr-model-res", a.settingsMgr.GetOcrModel())
		return true

	case "settings:set-ocr-model":
		_ = a.settingsMgr.SetOcrModel(getStringArg(0))
		a.AddLog(fmt.Sprintf("⚙️ OCR 图片分析模型已更新: %s", getStringArg(0)))
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

	// ===== NVIDIA 上游蓄流重试耗尽后的兜底出站代理(独立于专属 SOCKS5) =====
	case "settings:set-fallback-proxy-address":
		_ = a.settingsMgr.SetFallbackProxyAddress(getStringArg(0))
		a.AddLog(fmt.Sprintf("⚙️ 兜底代理地址已更新: %s", getStringArg(0)))
		return true

	case "settings:set-fallback-proxy-enabled":
		_ = a.settingsMgr.SetFallbackProxyEnabled(getBoolArg(0))
		status := "禁用"
		if getBoolArg(0) {
			status = "启用"
		}
		a.AddLog(fmt.Sprintf("⚙️ 兜底代理状态已更新: %s", status))
		return true

	case "settings:set-fallback-proxy-username":
		_ = a.settingsMgr.SetFallbackProxyUsername(getStringArg(0))
		a.AddLog("⚙️ 兜底代理用户名已更新")
		return true

	case "settings:set-fallback-proxy-password":
		_ = a.settingsMgr.SetFallbackProxyPassword(getStringArg(0))
		a.AddLog("⚙️ 兜底代理密码已更新")
		return true

	case "settings:set-nvidia-preferred-models":
		// args: [models: []interface{<string>}]
		var models []string
		if len(args) > 0 {
			if arr, ok := args[0].([]interface{}); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok {
						s = strings.TrimSpace(s)
						if s != "" {
							models = append(models, s)
						}
					}
				}
			}
		}
		_ = a.settingsMgr.SetNvidiaPreferredModels(models)
		savedCount := len(models)
		a.AddLog(fmt.Sprintf("⚙️ NVIDIA 专属模型清单已更新: %d 个", savedCount))
		wailsRuntime.EventsEmit(a.ctx, "settings:nvidia-preferred-models-res", map[string]interface{}{
			"success": true,
			"count":   savedCount,
			"models":  models,
		})
		return true

	case "settings:get-network-status":
		fallbackURL := ""
		if u := netutil.GetCachedLocalProxy(); u != nil {
			fallbackURL = u.String()
		}
		wailsRuntime.EventsEmit(a.ctx, "settings:network-status-res", map[string]interface{}{
			"customSocks5Address":  a.settingsMgr.GetCustomSocks5Address(),
			"customSocks5Enabled":  a.settingsMgr.GetCustomSocks5Enabled(),
			"fallbackProxyAddress": a.settingsMgr.GetFallbackProxyAddress(),
			"fallbackProxyEnabled": a.settingsMgr.GetFallbackProxyEnabled(),
			"cachedLocalProxy":      fallbackURL,
		})
		return true

	case "settings:get-network-logs":
		wailsRuntime.EventsEmit(a.ctx, "settings:network-logs-res", netutil.GetNetworkLogs())
		return true

	case "settings:set-prompt-prefix":
		_ = a.settingsMgr.SetPromptPrefix(getStringArg(0))
		a.AddLog("⚙️ 自定义提示词前缀已更新")
		return true

	case "settings:set-custom-model-override-enabled":
		enabled := getBoolArg(0)
		_ = a.settingsMgr.SetCustomModelOverrideEnabled(enabled)
		status := "禁用"
		if enabled {
			status = "启用"
		}
		a.AddLog(fmt.Sprintf("⚙️ 自定义模型覆盖状态已更新: %s", status))
		return true

	case "settings:set-custom-model-override-id":
		_ = a.settingsMgr.SetCustomModelOverrideID(getStringArg(0))
		a.AddLog("⚙️ 自定义模型覆盖 ID 已更新")
		return true

	case "settings:set-custom-thinking-override-enabled":
		enabled := getBoolArg(0)
		_ = a.settingsMgr.SetCustomThinkingOverrideEnabled(enabled)
		status := "禁用"
		if enabled {
			status = "启用"
		}
		a.AddLog(fmt.Sprintf("⚙️ 自定义思维链覆盖状态已更新: %s", status))
		return true

	case "settings:set-custom-thinking-supports":
		supports := getBoolArg(0)
		_ = a.settingsMgr.SetCustomThinkingSupports(supports)
		status := "否"
		if supports {
			status = "是"
		}
		a.AddLog(fmt.Sprintf("⚙️ 自定义思维链声明支持状态已更新: %s", status))
		return true

	case "settings:set-custom-thinking-budget":
		budget := getIntArg(0)
		_ = a.settingsMgr.SetCustomThinkingBudget(budget)
		a.AddLog(fmt.Sprintf("⚙️ 自定义思维链预算已更新: %d", budget))
		return true

	case "settings:set-custom-thinking-min-budget":
		minBudget := getIntArg(0)
		_ = a.settingsMgr.SetCustomThinkingMinBudget(minBudget)
		a.AddLog(fmt.Sprintf("⚙️ 自定义思维链最小预算已更新: %d", minBudget))
		return true

	case "settings:set-custom-max-output-tokens":
		maxTokens := getIntArg(0)
		_ = a.settingsMgr.SetCustomMaxOutputTokens(maxTokens)
		a.AddLog(fmt.Sprintf("⚙️ 自定义最大输出 Token 已更新: %d", maxTokens))
		return true

	case "settings:set-reasoning-as-text":
		enabled := getBoolArg(0)
		_ = a.settingsMgr.SetReasoningAsText(enabled)
		relay.SetGlobalReasoningAsText(enabled)
		status := "关闭"
		if enabled {
			status = "开启"
		}
		a.AddLog(fmt.Sprintf("⚙️ 思考过程直吐正文模式: %s", status))
		return true

	case "settings:set-enable-thinking-mode":
		enabled := getBoolArg(0)
		_ = a.settingsMgr.SetEnableThinkingMode(enabled)
		relay.SetGlobalEnableThinkingMode(enabled)
		status := "关闭"
		if enabled {
			status = "开启"
		}
		a.AddLog(fmt.Sprintf("⚙️ 思考模式已: %s", status))
		return true

	case "settings:language-changed":
		lang := getStringArg(0)
		if lang != "" {
			_ = a.settingsMgr.SetLanguage(lang)
			a.AddLog(fmt.Sprintf("⚙️ 系统语言已更改为: %s", lang))
		}
		return true

	case "settings:get-debugger-mode":
		wailsRuntime.EventsEmit(a.ctx, "settings:debugger-mode-res", map[string]interface{}{
			"enabled":      a.settingsMgr.GetEnableDebuggerMode(),
			"path":         a.settingsMgr.GetDebuggerLogPath(),
			"resolvedPath": a.settingsMgr.GetResolvedDebuggerLogPath(),
		})
		return true

	case "settings:set-debugger-mode":
		enabled := getBoolArg(0)
		_ = a.settingsMgr.SetEnableDebuggerMode(enabled)
		status := "禁用"
		if enabled {
			status = "启用"
		}
		a.AddLog(fmt.Sprintf("⚙️ Debugger 调试模式已更新: %s", status))
		return true

	case "settings:set-debugger-log-path":
		pathVal := getStringArg(0)
		_ = a.settingsMgr.SetDebuggerLogPath(pathVal)
		a.AddLog(fmt.Sprintf("⚙️ Debugger 调试日志存储路径已更新: %s", pathVal))
		return true

	case "settings:select-debugger-log-dir":
		dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
			Title: "选择 Debugger 调试日志保存目录",
		})
		if err == nil && dir != "" {
			_ = a.settingsMgr.SetDebuggerLogPath(dir)
			wailsRuntime.EventsEmit(a.ctx, "settings:debugger-log-path-res", dir)
			a.AddLog(fmt.Sprintf("⚙️ Debugger 调试日志目录已选择: %s", dir))
		}
		return true
	}
	return false
}
