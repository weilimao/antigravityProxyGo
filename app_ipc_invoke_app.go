package main

import (
	"encoding/json"

	"antigravity-proxy/internal/update"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleAppInvokeIPC 处理 IPCInvoke 的应用更新/版本通道:app:check-for-updates/
// start-download-update/get-version。从 app_ipc.go IPCInvoke 尾部 switch 抽离,2-tuple marshalResponse
// 改写为 3-tuple(对应子处理器 (string, bool, error)),未命中返回 ("", false, nil) fall-through。
// appVersion 为包级全局(version.go),同 package main 跨文件解析。逻辑逐行等价。
func (a *App) handleAppInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {
	case "app:check-for-updates":
		hasUpdate, release, err := a.updateMgr.CheckForUpdates()
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}

		if hasUpdate {
			wailsRuntime.EventsEmit(a.ctx, "app:update-available", map[string]interface{}{
				"currentVersion": appVersion,
				"latestVersion":  release.TagName,
				"releaseNotes":   release.Body,
				"downloadUrl":    release.HTMLURL,
				"assets":         release.Assets,
			})
		} else {
			wailsRuntime.EventsEmit(a.ctx, "app:update-not-available", map[string]interface{}{
				"currentVersion": appVersion,
			})
		}
		return marshalResponse(hasUpdate)

	case "app:start-download-update":
		var assets []update.ReleaseAsset
		if len(args) > 0 {
			bytesAssets, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesAssets, &assets)
		}

		destPath, err := a.updateMgr.DownloadUpdate(assets, func(percent int, downloaded, total int64) {
			wailsRuntime.EventsEmit(a.ctx, "app:download-progress", map[string]interface{}{
				"percent":    percent,
				"downloaded": downloaded,
				"total":      total,
			})
		})

		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "app:update-error", err.Error())
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}

		wailsRuntime.EventsEmit(a.ctx, "app:download-complete", destPath)
		return marshalResponse(destPath)

	case "app:get-version":
		return marshalResponse(appVersion)
	}

	return "", false, nil
}
