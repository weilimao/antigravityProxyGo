package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/patch"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleSettingsInvokeIPC 处理 IPCInvoke 的设置通道:settings:export-logs/
// get-nvidia-preferred-models/change-dir。从 app_ipc.go IPCInvoke 尾部 switch 抽离(段A
// export-logs + 段BC get-nvidia-preferred-models/change-dir),2-tuple marshalResponse 改写
// 为 3-tuple(子处理器返 (string, bool, error)),未命中返回 ("", false, nil) fall-through。
// get-nvidia-preferred-models 复用第一个启用 NVIDIA 账号经 fetchRemoteNvidiaModels(包级
// 自由函数,同 package main 跨文件)拉远端 /v1/models;change-dir 经 settingsMgr.MigrateData
// 整体迁移数据目录并回写各子系统 UpdatePath。逻辑逐行等价。
func (a *App) handleSettingsInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
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
	case "settings:export-logs":
		logContent := getStringArg(0)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			DefaultName: fmt.Sprintf("system_logs_%s.txt", time.Now().Format("20060102150405")),
			Title:       "Export System Logs",
			Filters:     []dialogs.FileFilter{{DisplayName: "Text Files", Pattern: "*.txt"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		if err := os.WriteFile(filePath, []byte(logContent), 0644); err != nil {
			a.AddLog(fmt.Sprintf("❌ Failed to export system logs: %v", err))
			return marshalResponse(false)
		}
		a.AddLog(fmt.Sprintf("✅ System logs exported to: %s", filePath))
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "settings:get-nvidia-preferred-models":
		// 全局级"NVIDIA 专属模型清单"获取:
		// 清单已配置 → 直接返回清单(不请求远端);清单为空 → 复用号池第一个启用 NVIDIA 账号请求远端 /v1/models。
		a.AddLog("🔍 [NVIDIA 专属模型] 收到获取请求,开始执行")
		// force 来源参数(可选):"remote"=强制跳过 cache 直接打上游;不传/"local"=沿用"cache 优先,空则回退远端"。
		// 前端来源切换条用此区分:本地清单(fore=undefined) vs NVIDIA远端(force="remote")。
		forceRemote := false
		if len(args) > 0 {
			if m, ok := args[0].(map[string]interface{}); ok {
				if v, ok := m["force"].(string); ok && v == "remote" {
					forceRemote = true
				}
			}
		}
		cached := a.settingsMgr.GetNvidiaPreferredModels()
		if len(cached) > 0 && !forceRemote {
			a.AddLog(fmt.Sprintf("🔍 [NVIDIA 专属模型] 命中缓存清单,%d 个模型,直接返回", len(cached)))
			return marshalResponse(map[string]interface{}{
				"success": true,
				"source":  "cache",
				"models":  cached,
			})
		}
		if forceRemote {
			a.AddLog("🔍 [NVIDIA 专属模型] 强制远端模式,跳过缓存清单直接请求上游")
		}
		// 清单为空 → 取号池第一个启用 NVIDIA 账号
		// 重要:必须用 GetRawAccounts() —— 前端展示用的 GetAccounts() 刻意不拷贝 BaseURL/AccessToken
		// (避免向前端泄露 token),用它会拿到全空 BaseURL/AccessToken,误判所有账号不可用。
		// GetRawAccounts 返回内部指针,这里只读不写,线程安全(RLock 保护)。
		rawAccounts := a.accountMgr.GetRawAccounts()
		var firstAcc *account.Account
		nvidiaCount := 0
		enabledCount := 0
		baseURLOkCount := 0
		for _, acc := range rawAccounts {
			if acc == nil || acc.Provider != "nvidia" {
				continue
			}
			nvidiaCount++
			if !acc.Enabled {
				continue
			}
			enabledCount++
			if strings.TrimSpace(acc.BaseURL) == "" {
				continue
			}
			baseURLOkCount++
			if firstAcc == nil {
				firstAcc = acc
			}
		}
		if firstAcc == nil {
			// 诊断:列出前若干个 nvidia 账号的关键字段,一次性看清为何全部不可用
			detail := ""
			shown := 0
			for _, acc := range rawAccounts {
				if acc == nil || acc.Provider != "nvidia" || shown >= 5 {
					continue
				}
				keyMasked := ""
				if acc.AccessToken != "" {
					keyMasked = account.MaskAPIKey(acc.AccessToken)
				}
				detail += fmt.Sprintf("\n  - [email=%s] enabled=%v baseURL=%q accessTokenLen=%d keyMask=%s", acc.Email, acc.Enabled, acc.BaseURL, len(acc.AccessToken), keyMasked)
				shown++
			}
			a.AddLog(fmt.Sprintf("❌ [NVIDIA 专属模型] 号池无可用 NVIDIA 账号(nvidia=%d,enabled=%d,baseURL就绪=%d)%s", nvidiaCount, enabledCount, baseURLOkCount, detail))
			return marshalResponse(map[string]interface{}{
				"success": false,
				"source":  "remote",
				"error":   "号池中暂无可用 NVIDIA 账号(需 Provider=nvidia 且启用 且已配置 Base URL)。请在 NVIDIA 号池添加账号时填写 Base URL(如 https://integrate.api.nvidia.com/v1)与 API Key。",
			})
		}
		a.AddLog(fmt.Sprintf("🔍 [NVIDIA 专属模型] 清单为空,用账号 %s 请求远端 %s", firstAcc.Email, firstAcc.BaseURL))
		remoteModels, ferr := fetchRemoteNvidiaModels(firstAcc.BaseURL, firstAcc.AccessToken)
		if ferr != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA 专属模型] 拉取远端模型失败: %v", ferr))
			return marshalResponse(map[string]interface{}{
				"success": false,
				"source":  "remote",
				"error":   ferr.Error(),
			})
		}
		a.AddLog(fmt.Sprintf("✅ [NVIDIA 专属模型] 复用账号 %s 拉取到远端 %d 个候选模型", firstAcc.Email, len(remoteModels)))
		return marshalResponse(map[string]interface{}{
			"success": true,
			"source":  "remote",
			"models":  remoteModels,
		})

	case "settings:change-dir":
		targetDir, ok, _ := a.dialogSvc.OpenDir(a.ctx, dialogs.DirRequest{Title: "选择数据存储目录"})
		if !ok {
			return marshalResponse(map[string]interface{}{"success": false, "error": "用户取消选择"})
		}
		// OpenDir 成功后内部已将该目录写入目录记忆(MemDataDir)，后续导入/导出
		// 对话框默认定位到最近选择的目录。迁移成功后数据目录本身即承载数据。

		if filepath.Clean(targetDir) == filepath.Clean(a.settingsMgr.GetActiveDataDirectory()) {
			return marshalResponse(map[string]interface{}{"success": true, "activeDir": targetDir})
		}

		wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": "stop-proxy", "status": "正在停止代理服务器..."})
		a.proxyEngine.Stop()

		defaultUserData := a.settingsMgr.GetDefaultUserDataPath()

		errMigrate := a.settingsMgr.MigrateData(
			targetDir,
			func(step, status string) {
				wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": step, "status": status})
			},
			a.proxyEngine.Stop,
			func() {
				_ = a.proxyEngine.Start(targetDir)
			},
			func(caPemPath string) error {
				homeDir, _ := os.UserHomeDir()
				return patch.PatchAll(true, defaultUserData, homeDir, caPemPath, a.AddLog)
			},
			func(newDir string) {
				a.accountMgr.UpdatePath(newDir)
				a.statsTracker.UpdatePath(newDir)
				a.usageTracker.UpdatePath(newDir)
				a.errLogger.UpdatePath(newDir)
				a.pricingMgr.UpdatePath(newDir)
				a.packetCap.UpdatePath(newDir)
				a.sessionRouter.UpdatePath(newDir)
				a.quotaSvc.UpdatePath(newDir)
				if a.relayUserMgr != nil {
					a.relayUserMgr.UpdatePath(newDir)
				}
				if a.relayStatsMgr != nil {
					a.relayStatsMgr.UpdatePath(newDir)
				}
			},
		)

		if errMigrate != nil {
			wailsRuntime.EventsEmit(a.ctx, "settings:migration-progress", map[string]string{"step": "error", "status": errMigrate.Error()})
			_ = a.proxyEngine.Start(a.settingsMgr.GetActiveDataDirectory())
			return marshalResponse(map[string]interface{}{"success": false, "error": errMigrate.Error()})
		}

		a.AddLog("📁 数据存储路径已成功更改并迁移至: " + targetDir)
		return marshalResponse(map[string]interface{}{"success": true, "activeDir": targetDir})
	}

	return "", false, nil
}
