package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"time"

	"antigravity-proxy/internal/antigravitybg"
	"antigravity-proxy/internal/stats"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_antigravity_bg_ipc.go: Antigravity 桌面端壁纸与外观调谐管理 IPC。
// 支持跨平台 (macOS 与 Windows) 状态获取、本地图片选择、补丁注入、备份还原与拉起启动。

func (a *App) handleAntigravityBgIPCSend(channel string, args []interface{}) bool {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "antigravitybg:get-status":
		customPath := getStringArg(0)
		status := a.antigravityBgMgr.GetStatus(customPath)
		wailsRuntime.EventsEmit(a.ctx, "antigravitybg:status-res", status)
		return true

	case "antigravitybg:trim-memory":
		go func() {
			time.Sleep(100 * time.Millisecond)
			stats.TrimProcessWorkingSet()
		}()
		return true
	}

	return false
}

func (a *App) handleAntigravityBgIPCInvoke(channel string, args []interface{}) (string, bool, error) {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "antigravitybg:trim-memory":
		go func() {
			time.Sleep(100 * time.Millisecond)
			stats.TrimProcessWorkingSet()
		}()
		return `{"success":true}`, true, nil

	case "antigravitybg:get-status":
		customPath := getStringArg(0)
		status := a.antigravityBgMgr.GetStatus(customPath)
		b, _ := json.Marshal(status)
		return string(b), true, nil

	case "antigravitybg:apply":
		var req struct {
			CustomPath string                         `json:"customPath"`
			Config     antigravitybg.WallpaperConfig `json:"config"`
		}
		if len(args) > 0 {
			rawJSON, _ := json.Marshal(args[0])
			_ = json.Unmarshal(rawJSON, &req)
		}
		res := a.antigravityBgMgr.ApplyWallpaper(req.CustomPath, req.Config)
		if res.Success {
			a.AddLog("🎨 [Antigravity壁纸] 成功应用壁纸与外观调谐配置")
		} else {
			a.AddLog(fmt.Sprintf("❌ [Antigravity壁纸] 应用配置失败: %s", res.Error))
		}
		b, _ := json.Marshal(res)
		return string(b), true, nil

	case "antigravitybg:restore":
		customPath := getStringArg(0)
		res := a.antigravityBgMgr.RestoreDefault(customPath)
		if res.Success {
			a.AddLog("🔄 [Antigravity壁纸] 已恢复 Antigravity 出厂默认界面")
		} else {
			a.AddLog(fmt.Sprintf("❌ [Antigravity壁纸] 恢复默认失败: %s", res.Error))
		}
		b, _ := json.Marshal(res)
		return string(b), true, nil

	case "antigravitybg:launch":
		customPath := getStringArg(0)
		err := a.antigravityBgMgr.LaunchApp(customPath)
		res := map[string]interface{}{
			"success": err == nil,
		}
		if err != nil {
			res["error"] = err.Error()
			a.AddLog(fmt.Sprintf("⚠️ [Antigravity壁纸] 启动 Antigravity 失败: %v", err))
		} else {
			a.AddLog("🚀 [Antigravity壁纸] 已拉起/启动 Antigravity 桌面端")
		}
		b, _ := json.Marshal(res)
		return string(b), true, nil

	case "antigravitybg:select-image-dialog":
		filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
			Title: "选择壁纸图片或动态视频",
			Filters: []wailsRuntime.FileFilter{
				{
					DisplayName: "所有支持的壁纸媒体 (*.png;*.jpg;*.webp;*.gif;*.mp4;*.webm;*.mov)",
					Pattern:     "*.png;*.jpg;*.jpeg;*.webp;*.bmp;*.gif;*.mp4;*.webm;*.mov;*.mkv",
				},
				{
					DisplayName: "动态视频 (*.mp4;*.webm;*.mov)",
					Pattern:     "*.mp4;*.webm;*.mov;*.mkv",
				},
				{
					DisplayName: "静态/动效图片 (*.png;*.jpg;*.jpeg;*.webp;*.gif)",
					Pattern:     "*.png;*.jpg;*.jpeg;*.webp;*.bmp;*.gif",
				},
			},
		})
		if err != nil || filePath == "" {
			b, _ := json.Marshal(map[string]interface{}{
				"success":  false,
				"canceled": true,
			})
			return string(b), true, nil
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		mediaType := "image"

		switch ext {
		case ".mp4", ".webm", ".mov", ".mkv":
			mediaType = "video"
		case ".gif":
			mediaType = "gif"
		}

		// 使用本地流式 HTTP 路径，内存 0 拷贝
		mediaUrl := fmt.Sprintf("/local-media?path=%s", filePath)
		b, _ := json.Marshal(map[string]interface{}{
			"success":   true,
			"filePath":  filePath,
			"fileName":  filepath.Base(filePath),
			"mediaType": mediaType,
			"mediaUrl":  mediaUrl,
		})
		return string(b), true, nil

	case "antigravitybg:get-gallery":
		gallery := a.antigravityBgMgr.GetGallery()
		b, _ := json.Marshal(map[string]interface{}{
			"success": true,
			"gallery": gallery,
		})
		return string(b), true, nil

	case "antigravitybg:get-wallpaper-data":
		id := getStringArg(0)
		data, err := a.antigravityBgMgr.GetWallpaperData(id)
		res := map[string]interface{}{
			"success": err == nil,
			"id":      id,
			"data":    data,
		}
		if err != nil {
			res["error"] = err.Error()
		}
		b, _ := json.Marshal(res)
		return string(b), true, nil

	case "antigravitybg:save-gallery":
		var items []antigravitybg.SavedWallpaper
		if len(args) > 0 {
			rawJSON, _ := json.Marshal(args[0])
			_ = json.Unmarshal(rawJSON, &items)
		}
		_ = a.antigravityBgMgr.SaveGallery(items)
		b, _ := json.Marshal(map[string]interface{}{
			"success": true,
			"gallery": items,
		})
		return string(b), true, nil

	case "antigravitybg:add-wallpaper":
		var item antigravitybg.SavedWallpaper
		if len(args) > 0 {
			rawJSON, _ := json.Marshal(args[0])
			_ = json.Unmarshal(rawJSON, &item)
		}
		gallery := a.antigravityBgMgr.AddWallpaperToGallery(item)
		b, _ := json.Marshal(map[string]interface{}{
			"success": true,
			"gallery": gallery,
		})
		return string(b), true, nil

	case "antigravitybg:remove-wallpaper":
		id := getStringArg(0)
		gallery := a.antigravityBgMgr.RemoveWallpaperFromGallery(id)
		b, _ := json.Marshal(map[string]interface{}{
			"success": true,
			"gallery": gallery,
		})
		return string(b), true, nil
	}

	return "", false, nil
}
