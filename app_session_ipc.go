package main

import (
	"encoding/json"
	"fmt"
)

// handleSessionIPC 处理会话管理相关的 IPC 呼叫
// 如果返回的 handled 为 true，表示该 channel 已被此模块处理，外层 IPCInvoke 可直接返回对应结果和 err。
func (a *App) handleSessionIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
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
	case "sessions:get":
		type FrontendSessionInfo struct {
			SessionKey   string `json:"sessionKey"`
			AccountID    string `json:"accountId"`
			AccountEmail string `json:"accountEmail"`
			LastActive   int64  `json:"lastActive"`
		}
		bindings := a.sessionRouter.GetBindings()
		res := make([]FrontendSessionInfo, 0, len(bindings))
		for _, b := range bindings {
			email := "未知账号"
			acc := a.accountMgr.GetAccountByID(b.AccountID)
			if acc != nil {
				email = acc.Email
			}
			res = append(res, FrontendSessionInfo{
				SessionKey:   b.SessionKey,
				AccountID:    b.AccountID,
				AccountEmail: email,
				LastActive:   b.LastActive,
			})
		}
		data, err := marshalResponse(res)
		return data, true, err

	case "sessions:unbind":
		sessionKey := getStringArg(0)
		success := a.sessionRouter.UnbindSession(sessionKey)
		a.AddLog(fmt.Sprintf("🗑️ [会话路由] 手动解绑会话 %s, 结果: %v", sessionKey, success))
		data, err := marshalResponse(map[string]interface{}{"success": success})
		return data, true, err
	}

	return "", false, nil
}
