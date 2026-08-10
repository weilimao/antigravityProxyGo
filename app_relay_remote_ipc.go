package main

import (
	"encoding/json"
	"fmt"
	"time"

	"antigravity-proxy/internal/proxy"
)

// handleRelayRemoteIPC 处理 remote:* (远程中继客户端模式) IPC 通道。
// 从 app_relay.go handleRelayIPC 抽离:remote:login/disconnect/disable/enable/get-status/
// test/sync-stats/get-keys/create-key/delete-key/update-key-quota。自包含本地闭包,
// 未命中返回 ("", false, nil) 由 handleRelayIPC fall-through 下一处理器(逻辑逐行等价)。
func (a *App) handleRelayRemoteIPC(channel string, args []interface{}) (string, bool, error) {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	getInt64Arg := func(idx int) int64 {
		if idx < len(args) {
			switch v := args[idx].(type) {
			case float64:
				return int64(v)
			case int64:
				return v
			case int:
				return int64(v)
			}
		}
		return 0
	}

	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {
	// ========== Remote Connection (Client Mode) ==========

	case "remote:login":
		host := getStringArg(0)
		port := getStringArg(1)
		key := getStringArg(2)
		password := getStringArg(3)
		path := getStringArg(4)

		portArg := port

		if err := a.connectRemote(host, port, path, key, password); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		// Save config to settings
		_ = a.settingsMgr.SetRemoteHost(host)
		_ = a.settingsMgr.SetRemotePort(portArg)
		_ = a.settingsMgr.SetRemotePath(path)
		_ = a.settingsMgr.SetRemoteKey(key)
		_ = a.settingsMgr.SetRemotePassword(password)
		_ = a.settingsMgr.SetRemoteEnabled(true)

		a.AddLog(fmt.Sprintf("🌐 Remote connected to %s:%s%s as %s", host, port, path, key))
		a.emitRemoteState()

		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:disconnect":
		a.disconnectRemote()
		_ = a.settingsMgr.SetRemoteHost("")
		_ = a.settingsMgr.SetRemotePort("")
		_ = a.settingsMgr.SetRemotePath("")
		_ = a.settingsMgr.SetRemoteKey("")
		_ = a.settingsMgr.SetRemotePassword("")
		_ = a.settingsMgr.SetRemoteEnabled(false)
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:disable":
		a.disconnectRemote()
		_ = a.settingsMgr.SetRemoteEnabled(false)
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:enable":
		host := a.settingsMgr.GetRemoteHost()
		port := a.settingsMgr.GetRemotePort()
		path := a.settingsMgr.GetRemotePath()
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host == "" || key == "" {
			return marshalResponse(map[string]interface{}{"success": false, "error": "没有已保存的远程连接凭据"})
		}
		if err := a.connectRemote(host, port, path, key, pwd); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		_ = a.settingsMgr.SetRemoteEnabled(true)
		a.AddLog(fmt.Sprintf("🌐 Remote re-connected to %s:%s%s as %s", host, port, path, key))
		a.emitRemoteState()
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:get-status":
		return marshalResponse(a.getRemoteStatusPayload())

	case "remote:test":
		host := getStringArg(0)
		port := getStringArg(1)
		path := getStringArg(2)

		testRelay := proxy.NewRemoteRelay(nil)
		start := time.Now()
		if err := testRelay.TestConnection(host, port, path); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		latency := time.Since(start).Milliseconds()
		return marshalResponse(map[string]interface{}{"success": true, "latencyMs": latency})

	case "remote:sync-stats":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(nil)
		}
		stats, err := a.remoteRelay.FetchRemoteStats()
		if err != nil {
			a.AddLog(fmt.Sprintf("⚠️ Remote stats sync failed: %v", err))
			return marshalResponse(nil)
		}
		return marshalResponse(stats)

	case "remote:get-keys":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		keys, err := a.remoteRelay.FetchRemoteKeys()
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "keys": keys})

	case "remote:create-key":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		name := getStringArg(0)
		key, err := a.remoteRelay.CreateRemoteKey(name)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "key": key})

	case "remote:delete-key":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		id := getStringArg(0)
		err := a.remoteRelay.DeleteRemoteKey(id)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "remote:update-key-quota":
		if a.remoteRelay == nil || !a.remoteRelay.IsConnected() {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not connected"})
		}
		id := getStringArg(0)
		limitGemini := getInt64Arg(1)
		limitClaude := getInt64Arg(2)
		err := a.remoteRelay.UpdateRemoteKeyQuota(id, limitGemini, limitClaude)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})
	}

	return "", false, nil
}
