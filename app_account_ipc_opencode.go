package main

import (
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
)

// handleAccountIPCOpenCode 处理 OpenCode 号池 CRUD 相关的 IPC invoke 分支。
func (a *App) handleAccountIPCOpenCode(channel string, args []interface{}) (string, bool, error) {
	// 兼容支持通过 account:ipc 包装调用的场景: args[0] 为子通道 (如 "opencode:add"), args[1] 为参数切片或参数列表
	if channel == "account:ipc" && len(args) > 0 {
		if subChan, ok := args[0].(string); ok && strings.HasPrefix(subChan, "opencode:") {
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

	intAt := func(i int) int {
		if i < len(args) {
			switch v := args[i].(type) {
			case int:
				return v
			case int64:
				return int(v)
			case float64:
				return int(v)
			}
		}
		return 0
	}

	switch channel {
	// ========== OpenCode 号池 CRUD ==========

	case "opencode:add":
		// 支持两种传参:
		// 1) 对象形式: [{apiKey/accessToken, baseUrl, label, defaultModel}]
		// 2) 位置形式: [apiKey, label, defaultModel, baseUrl]
		var in account.OpenCodeAccountInput
		if len(args) > 0 {
			if m, ok := args[0].(map[string]interface{}); ok {
				if k, ok := m["accessToken"].(string); ok {
					in.AccessToken = k
				} else if k, ok := m["apiKey"].(string); ok {
					in.AccessToken = k
				}
				if b, ok := m["baseUrl"].(string); ok {
					in.BaseURL = b
				}
				if l, ok := m["label"].(string); ok {
					in.Label = l
				}
				if d, ok := m["defaultModel"].(string); ok {
					in.DefaultModel = d
				}
			} else {
				in.AccessToken = strAt(0)
				in.Label = strAt(1)
				in.DefaultModel = strAt(2)
				in.BaseURL = strAt(3)
			}
		}

		if strings.TrimSpace(in.AccessToken) == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 API Key (sk-...)"})
			return data, true, nil
		}

		id, err := a.accountMgr.AddOpenCodeAccount(in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [OpenCode] 添加账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("✅ [OpenCode] 添加账号成功: %s (id=%s)", in.Label, id))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "id": id})
		return data, true, nil

	case "opencode:batch-add":
		// 批量添加多个 Key:
		// args: [keysArrayOrMultilineString, defaultModel?]
		if len(args) == 0 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "未提供导入内容"})
			return data, true, nil
		}
		defaultModel := strAt(1)
		var rawKeys []string
		if list, ok := args[0].([]interface{}); ok {
			for _, item := range list {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					rawKeys = append(rawKeys, strings.TrimSpace(s))
				}
			}
		} else if text, ok := args[0].(string); ok {
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					rawKeys = append(rawKeys, line)
				}
			}
		}

		if len(rawKeys) == 0 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "没有解析到有效的 Key"})
			return data, true, nil
		}

		successCount := 0
		var failErrors []string
		for _, rawKey := range rawKeys {
			in := account.OpenCodeAccountInput{
				AccessToken:  rawKey,
				DefaultModel: defaultModel,
			}
			_, err := a.accountMgr.AddOpenCodeAccount(in)
			if err != nil {
				failErrors = append(failErrors, fmt.Sprintf("%s: %v", rawKey, err))
			} else {
				successCount++
			}
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("📦 [OpenCode] 批量导入完成: 成功 %d 个, 失败 %d 个", successCount, len(failErrors)))
		data, _ := marshalResponse(map[string]interface{}{
			"success":      true,
			"imported":     successCount,
			"failed":       len(failErrors),
			"failedErrors": failErrors,
		})
		return data, true, nil

	case "opencode:update":
		// 支持两种传参:
		// 1) 对象形式: [{id/accountId, apiKey/accessToken, label, defaultModel, baseUrl}]
		// 2) 位置形式: [accountId, apiKey/accessToken, label, defaultModel, baseUrl]
		var id string
		var in account.OpenCodeAccountInput
		if len(args) > 0 {
			if m, ok := args[0].(map[string]interface{}); ok {
				if v, ok := m["id"].(string); ok {
					id = v
				} else if v, ok := m["accountId"].(string); ok {
					id = v
				}
				if k, ok := m["accessToken"].(string); ok {
					in.AccessToken = k
				} else if k, ok := m["apiKey"].(string); ok {
					in.AccessToken = k
				}
				if b, ok := m["baseUrl"].(string); ok {
					in.BaseURL = b
				}
				if l, ok := m["label"].(string); ok {
					in.Label = l
				}
				if d, ok := m["defaultModel"].(string); ok {
					in.DefaultModel = d
				}
			} else {
				id = strAt(0)
				in.AccessToken = strAt(1)
				in.Label = strAt(2)
				in.DefaultModel = strAt(3)
				in.BaseURL = strAt(4)
			}
		}

		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}

		_, err := a.accountMgr.UpdateOpenCodeAccount(id, in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [OpenCode] 更新账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		if updatedAcc := a.accountMgr.GetAccountByID(id); updatedAcc != nil {
			if a.usageTracker != nil {
				a.usageTracker.RenameAccountByID(updatedAcc.ID, updatedAcc.Email)
			}
		}
		a.emitAccountsRes()
		if a.ctx != nil && a.usageTracker != nil {
			wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		}
		a.AddLog(fmt.Sprintf("✅ [OpenCode] 更新账号成功 id=%s", id))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "opencode:remove":
		id := strAt(0)
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "opencode" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 OpenCode 类型"})
			return data, true, nil
		}
		a.accountMgr.RemoveAccount(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🗑️ [OpenCode] 已移除账号 id=%s", id))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "opencode:toggle-enabled":
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
		if acc == nil || acc.Provider != "opencode" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 OpenCode 类型"})
			return data, true, nil
		}
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		a.emitAccountsRes()
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		a.AddLog(fmt.Sprintf("🔄 [OpenCode] 账号 %s is now %s.", acc.Email, status))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "opencode:clear-cooldown":
		id := strAt(0)
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "opencode" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 OpenCode 类型"})
			return data, true, nil
		}
		a.accountMgr.ClearAccountCooldown(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("❄️ [OpenCode] 已解除账号 %s 的冷静状态", acc.Email))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "opencode:import-local":
		// 一键导入本机当前 OpenCode CLI 登录配置
		customPath := strAt(0)
		acc, err := a.accountMgr.ImportOpenCodeLocalAccount(customPath)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [OpenCode] 一键导入本地账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🎉 [OpenCode] 成功从本机导入账号: %s", acc.Email))
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"account": map[string]interface{}{
				"id":           acc.ID,
				"email":        acc.Email,
				"defaultModel": acc.DefaultModel,
			},
		})
		return data, true, nil

	case "opencode:fetch-models":
		models := account.OpenCodeSupportedModels
		data, _ := marshalResponse(map[string]interface{}{"success": true, "models": models})
		return data, true, nil

	case "opencode:set-lb-mode":
		mode := strAt(0)
		a.accountMgr.SetOpenCodeLBMode(mode)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🔄 [OpenCode] LB Mode → %s", a.accountMgr.GetOpenCodeLBMode()))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "mode": a.accountMgr.GetOpenCodeLBMode()})
		return data, true, nil

	case "opencode:get-lb-mode":
		data, _ := marshalResponse(map[string]interface{}{"success": true, "mode": a.accountMgr.GetOpenCodeLBMode()})
		return data, true, nil

	case "opencode:set-max-concurrency":
		val := intAt(0)
		a.accountMgr.SetOpenCodeMaxConcurrency(val)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🔄 [OpenCode] Max Concurrency → %d", a.accountMgr.GetOpenCodeMaxConcurrency()))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "maxConcurrency": a.accountMgr.GetOpenCodeMaxConcurrency()})
		return data, true, nil

	case "opencode:get-max-concurrency":
		data, _ := marshalResponse(map[string]interface{}{"success": true, "maxConcurrency": a.accountMgr.GetOpenCodeMaxConcurrency()})
		return data, true, nil

	default:
		if strings.HasPrefix(channel, "opencode:") {
			data, _ := marshalResponse(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("未知的 OpenCode IPC 指令: %s", channel),
			})
			return data, true, nil
		}
		return "", false, nil
	}
}
