package main

import (
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
)

// handleAccountIPCWorkBuddy 处理 WorkBuddy 号池 CRUD 相关的 IPC invoke 分支。
func (a *App) handleAccountIPCWorkBuddy(channel string, args []interface{}) (string, bool, error) {
	// 兼容支持通过 account:ipc 包装调用的场景: args[0] 为子通道 (如 "workbuddy:oauth-start"), args[1] 为参数切片或参数列表
	if channel == "account:ipc" && len(args) > 0 {
		if subChan, ok := args[0].(string); ok && strings.HasPrefix(subChan, "workbuddy:") {
			channel = subChan
			if len(args) > 1 {
				if subArgs, ok := args[1].([]interface{}); ok {
					args = subArgs
				} else {
					args = args[1:]
				}
			} else {
				args = nil
			}
		}
	}

	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	strAt := func(i int) string {
		if i < len(args) {
			if s, ok := args[i].(string); ok {
				return s
			}
		}
		return ""
	}

	boolAt := func(i int) bool {
		if i < len(args) {
			if b, ok := args[i].(bool); ok {
				return b
			}
		}
		return false
	}

	switch channel {
	// ========== WorkBuddy 号池 CRUD ==========

	case "workbuddy:add":
		// args: [baseURL, accessToken, label?, defaultModel?]
		if len(args) < 2 {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "参数不足:至少需要 baseURL 与 accessToken"})
			return data, true, err
		}
		in := account.WorkBuddyAccountInput{
			BaseURL:      strAt(0),
			AccessToken:  strAt(1),
			Label:        strAt(2),
			DefaultModel: strAt(3),
		}
		id, err := a.accountMgr.AddWorkBuddyAccount(in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [WorkBuddy] 添加账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("✅ [WorkBuddy] 添加账号成功: %s (id=%s)", in.Label, id))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "id": id})
		return data, true, nil

	case "workbuddy:update":
		// args: [accountId, baseURL, accessToken, label?, defaultModel?]
		if len(args) < 2 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "参数不足:至少需要 accountId、baseURL"})
			return data, true, nil
		}
		idU := strAt(0)
		if idU == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		inU := account.WorkBuddyAccountInput{
			BaseURL:      strAt(1),
			AccessToken:  strAt(2),
			Label:        strAt(3),
			DefaultModel: strAt(4),
		}
		_, uerr := a.accountMgr.UpdateWorkBuddyAccount(idU, inU)
		if uerr != nil {
			a.AddLog(fmt.Sprintf("❌ [WorkBuddy] 更新账号失败: %v", uerr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": uerr.Error()})
			return data, true, nil
		}
		if updatedAcc := a.accountMgr.GetAccountByID(idU); updatedAcc != nil {
			a.usageTracker.RenameAccountByID(updatedAcc.ID, updatedAcc.Email)
		}
		a.emitAccountsRes()
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog(fmt.Sprintf("✅ [WorkBuddy] 更新账号成功 id=%s", idU))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "workbuddy:remove":
		id := strAt(0)
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "workbuddy" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 WorkBuddy 类型"})
			return data, true, nil
		}
		a.accountMgr.RemoveAccount(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🗑️ [WorkBuddy] 已移除账号 id=%s", id))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "workbuddy:toggle-enabled":
		id := strAt(0)
		enabled := false
		if len(args) > 1 {
			if b, ok := args[1].(bool); ok {
				enabled = b
			}
		}
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "workbuddy" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 WorkBuddy 类型"})
			return data, true, nil
		}
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		a.emitAccountsRes()
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		a.AddLog(fmt.Sprintf("🔄 [WorkBuddy] 账号 %s is now %s.", acc.Email, status))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "workbuddy:clear-cooldown":
		id := strAt(0)
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "workbuddy" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 WorkBuddy 类型"})
			return data, true, nil
		}
		a.accountMgr.ClearAccountCooldown(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("❄️ [WorkBuddy] 已解除账号 %s 的冷静状态", acc.Email))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "workbuddy:import-local":
		// 一键导入本机当前 WorkBuddy AI 登录账号
		customPath := strAt(0)
		acc, err := a.accountMgr.ImportWorkBuddyLocalAccount(customPath)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [WorkBuddy] 一键导入本地账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🎉 [WorkBuddy] 成功从本机导入账号: %s (UID: %s)", acc.Email, acc.ProjectID))
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"account": map[string]interface{}{
				"id":           acc.ID,
				"email":        acc.Email,
				"uid":          acc.ProjectID,
				"defaultModel": acc.DefaultModel,
			},
		})
		return data, true, nil

	case "workbuddy:oauth-start":
		mgr := a.getOrInitWorkBuddyOAuthMgr()
		version := strAt(0)
		if version == "" {
			version = account.DefaultWorkBuddyAuthVersion
		}
		noOpen := boolAt(1)
		sess, err := mgr.StartLogin(a.ctx, version)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [WorkBuddy] 发起网页授权登录失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		if !noOpen {
			// 自动拉起系统默认浏览器
			wailsRuntime.BrowserOpenURL(a.ctx, sess.BrowserURL)
			a.AddLog(fmt.Sprintf("🌐 [WorkBuddy] 发起网页授权登录，已拉起系统浏览器: %s", sess.BrowserURL))
		} else {
			a.AddLog(fmt.Sprintf("📋 [WorkBuddy] 发起网页授权登录（已生成链接并启动本地监听）: %s", sess.BrowserURL))
		}
		data, _ := marshalResponse(map[string]interface{}{
			"success":    true,
			"state":      sess.State,
			"browserUrl": sess.BrowserURL,
			"authUrl":    sess.AuthURL,
		})
		return data, true, nil

	case "workbuddy:oauth-status":
		mgr := a.getOrInitWorkBuddyOAuthMgr()
		state := strAt(0)
		if state == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 state 参数"})
			return data, true, nil
		}
		sess := mgr.GetSession(state)
		if sess == nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "未找到对应的登录会话"})
			return data, true, nil
		}
		resp := map[string]interface{}{
			"success":      true,
			"state":        sess.State,
			"status":       sess.Status,
			"errorMessage": sess.ErrorMessage,
		}
		if sess.Account != nil {
			resp["account"] = map[string]interface{}{
				"id":           sess.Account.ID,
				"email":        sess.Account.Email,
				"uid":          sess.Account.ProjectID,
				"defaultModel": sess.Account.DefaultModel,
			}
		}
		data, _ := marshalResponse(resp)
		return data, true, nil

	case "workbuddy:oauth-cancel":
		mgr := a.getOrInitWorkBuddyOAuthMgr()
		state := strAt(0)
		if state != "" {
			mgr.CancelLogin(state)
			a.AddLog(fmt.Sprintf("⏹️ [WorkBuddy] 取消网页登录流程 (state=%s)", state))
		}
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "workbuddy:fetch-models":
		models := account.WorkBuddySupportedModels
		data, _ := marshalResponse(map[string]interface{}{"success": true, "models": models})
		return data, true, nil

	default:
		if strings.HasPrefix(channel, "workbuddy:") {
			data, _ := marshalResponse(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("未知的 WorkBuddy IPC 指令: %s", channel),
			})
			return data, true, nil
		}
		return "", false, nil
	}
}

func (a *App) getOrInitWorkBuddyOAuthMgr() *account.WorkBuddyOAuthManager {
	if a.workbuddyOAuthMgr != nil {
		return a.workbuddyOAuthMgr
	}
	a.workbuddyOAuthMgr = account.NewWorkBuddyOAuthManager(a.accountMgr)
	a.workbuddyOAuthMgr.SetOnSuccess(func(acc *account.Account) {
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🎉 [WorkBuddy] 官方网页授权登录成功: %s (UID: %s)", acc.Email, acc.ProjectID))
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "workbuddy:oauth-success", map[string]interface{}{
				"id":    acc.ID,
				"email": acc.Email,
				"uid":   acc.ProjectID,
			})
		}
	})
	return a.workbuddyOAuthMgr
}
