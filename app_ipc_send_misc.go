package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"antigravity-proxy/internal/cert"
	"antigravity-proxy/internal/pricing"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleMiscSendIPC 处理 IPCSend 的杂项通道:证书安装/状态、定价表查询-删除-重置、
// 抓包清空、更新安装启动、打开本地目录。从 app_ipc.go IPCSend 抽离,返回 bool 表已处理。
// 未命中返回 false 由 IPCSend fall-through 下一处理器。update-pricing 因引用 argsJSON 留 hub。
// 注:原 IPCSend case 体无 return(void),迁入 bool 子处理器后每 case 体末补 return true。
func (a *App) handleMiscSendIPC(channel string, args []interface{}) bool {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "cert-status":
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		caPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
		wailsRuntime.EventsEmit(a.ctx, "cert-status-res", cert.CheckCertStatus(caPath))
		return true

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
		return true

	case "cert-uninstall":
		a.AddLog("⏳ Removing Root CA certificate...")
		ok, errStr := cert.UninstallCert()
		if ok {
			a.AddLog("🔓 Local Root CA removed from system store.")
		} else {
			a.AddLog("❌ Failed to remove Root CA: " + errStr)
		}
		wailsRuntime.EventsEmit(a.ctx, "cert-status-res", !ok)
		return true

	case "get-pricing":
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
		return true

	case "delete-pricing":
		modelKey := getStringArg(0)
		a.pricingMgr.DeleteModelPricing(modelKey)
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog("🗑️ Model pricing deleted for \"" + modelKey + "\"")
		return true

	case "reset-pricing":
		_ = a.pricingMgr.ResetPricingToDefault()
		wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog("🔄 Model pricing reset to defaults")
		return true

	case "update-pricing-batch":
		// 前端「AI 一键生成计费」确认后批量提交 {模型名: {input,output,cached}}。
		// 一次落盘 + 一次 get-pricing-res + 一次 stats-updated, 比 N 次单条 update-pricing
		// 更省 IO 且减少 N 次热路径 getStatsPayload 重算。
		if len(args) > 0 {
			if mapData, ok := args[0].(map[string]interface{}); ok {
				b, _ := json.Marshal(mapData)
				var rates map[string]pricing.ModelRate
				if json.Unmarshal(b, &rates) == nil {
					_ = a.pricingMgr.UpdatePricingBatch(rates)
					wailsRuntime.EventsEmit(a.ctx, "get-pricing-res", a.pricingMgr.GetAllPricing())
					wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
					a.AddLog(fmt.Sprintf("✨ AI 生成计费已批量写入 %d 条模型单价", len(rates)))
				}
			}
		}
		return true

	case "packet:clear":
		a.packetCap.ClearPackets()
		return true

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
		return true

	case "settings:open-folder":
		// 打开本地目录/文件(URL 走浏览器,本地路径走文件管理器精确定位)。
		// 供导入后打开目录、下载定位文件等场景。
		pathVal := getStringArg(0)
		if pathVal == "" {
			return true
		}
		if strings.HasPrefix(pathVal, "http://") || strings.HasPrefix(pathVal, "https://") {
			a.OpenPath(pathVal)
		} else {
			a.OpenFolderInExplorer(pathVal)
		}
		return true

	}

	return false
}
