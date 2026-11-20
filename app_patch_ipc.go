package main

import (
	"antigravity-proxy/internal/antigravitybg"
	"antigravity-proxy/internal/patch"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// app_patch_ipc.go: 处理「重新打补丁并自动重启 Antigravity 桌面端」IPC 通道。

func (a *App) handlePatchInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	switch channel {
	case "patch:repatch-and-restart":
		var customPath string
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				customPath = s
			}
		}

		var resolver patch.DesktopProcessController
		if a.antigravityBgMgr != nil {
			resolver = a.antigravityBgMgr.GetPathResolver()
		} else {
			resolver = antigravitybg.NewPathResolver()
		}

		homeDir, _ := os.UserHomeDir()
		activeDir := a.settingsMgr.GetActiveDataDirectory()
		defaultUserData := a.settingsMgr.GetDefaultUserDataPath()
		caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")

		svc := patch.NewRepatchService(resolver, patch.PatchAll)
		res := svc.RepatchAndRestart(patch.RepatchOptions{
			DefaultUserData: defaultUserData,
			HomeDir:         homeDir,
			CACertPath:      caCertPath,
			CustomAppPath:   customPath,
			PollInterval:    200 * time.Millisecond,
			MaxKillWait:     5 * time.Second,
			PostExitDelay:   300 * time.Millisecond,
		}, a.AddLog)

		b, err := json.Marshal(res)
		if err != nil {
			return "", true, err
		}
		return string(b), true, nil
	}

	return "", false, nil
}
