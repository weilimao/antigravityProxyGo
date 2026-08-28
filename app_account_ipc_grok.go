package main

import (
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/modelfetch"
)

// handleAccountIPCGrok 处理 Grok 号池 CRUD 相关的 IPC invoke 分支。
// 从 handleAccountIPC 按 provider 切面拆分而出:承接 grok:* 全部 8 个 case
// (add/update/remove/toggle-enabled/clear-cooldown/fetch-models/check-auth)。
// marshalResponse 保持局部闭包风格,物理搬移,逻辑逐行等价,零回归。
func (a *App) handleAccountIPCGrok(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	// ========== Grok 号池 CRUD(xAI OpenAI Chat 兼容上游, 单池无组) ==========

	case "grok:add":
		// args: [baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?]
		// 前端按顺序传参(与 nvidia:add 位置参数对齐);label/模型字段可留空。
		if len(args) < 2 {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "参数不足:至少需要 baseURL 与 apiKey"})
			return data, true, err
		}
		strAt := func(i int) string {
			if i < len(args) {
				if s, ok := args[i].(string); ok {
					return s
				}
			}
			return ""
		}
		in := account.GrokAccountInput{
			BaseURL:      strAt(0),
			APIKey:       strAt(1),
			Label:        strAt(2),
			DefaultModel: strAt(3),
			ModelSonnet:  strAt(4),
			ModelOpus:    strAt(5),
			ModelHaiku:   strAt(6),
			ModelFable:   strAt(7),
		}
		id, err := a.accountMgr.AddGrokAccount(in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [Grok] 添加账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("✅ [Grok] 添加账号成功: %s (id=%s)", in.BaseURL, id))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "id": id})
		return data, true, nil

	case "grok:update":
		// args: [accountId, baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?]
		// 与 grok:add 位置参数对齐;apiKey 留空表示保持不变(与 nvidia:update 同口径)。
		if len(args) < 2 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "参数不足:至少需要 accountId、baseURL"})
			return data, true, nil
		}
		strAtU := func(i int) string {
			if i < len(args) {
				if s, ok := args[i].(string); ok {
					return s
				}
			}
			return ""
		}
		idU := strAtU(0)
		if idU == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		inU := account.GrokAccountInput{
			BaseURL:      strAtU(1),
			APIKey:       strAtU(2),
			Label:        strAtU(3),
			DefaultModel: strAtU(4),
			ModelSonnet:  strAtU(5),
			ModelOpus:    strAtU(6),
			ModelHaiku:   strAtU(7),
			ModelFable:   strAtU(8),
		}
		_, uerr := a.accountMgr.UpdateGrokAccount(idU, inU)
		if uerr != nil {
			a.AddLog(fmt.Sprintf("❌ [Grok] 更新账号失败: %v", uerr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": uerr.Error()})
			return data, true, nil
		}
		// 改名联动使用详情:同步该账号桶展示名(只改展示名副本,不动 Token/成本数值)。
		if updatedAcc := a.accountMgr.GetAccountByID(idU); updatedAcc != nil {
			a.usageTracker.RenameAccountByID(updatedAcc.ID, updatedAcc.Email)
		}
		a.emitAccountsRes()
		// 主动下发一次含 usage 的完整统计载荷,让前端「使用详情」即时重渲染新名。
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog(fmt.Sprintf("✅ [Grok] 更新账号成功 id=%s", idU))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "grok:remove":
		// args: [accountId]
		// 与 other:remove 同口径: 精确校验 Provider=="grok" 后删除, 避免误删其它池账号。
		id := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				id = s
			}
		}
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "grok" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 Grok 类型"})
			return data, true, nil
		}
		a.accountMgr.RemoveAccount(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🗑️ [Grok] 已移除账号 id=%s", id))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "grok:toggle-enabled":
		// args: [accountId, enabled]
		id := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				id = s
			}
		}
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
		// 仅对 grok 账号生效,避免误操作其他 provider 账号(与 nvidia:toggle-enabled 同口径)。
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "grok" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 Grok 类型"})
			return data, true, nil
		}
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		a.emitAccountsRes()
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		a.AddLog(fmt.Sprintf("🔄 [Grok] 账号 %s is now %s.", acc.Email, status))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "grok:clear-cooldown":
		// 手动解除单个 Grok 账号的冷却态(前端「单账号解冻」按钮入口)。
		// args: [accountId]。与 grok:remove/toggle-enabled 同口径:精确校验 Provider=="grok" 后清冷却,
		// 避免误清其它号池账号。ClearAccountCooldown 无论冷却是否到期都立即解除,触发 OnQuotaRestored 回调。
		// 成功后 emitAccountsRes 广播,前端卡片冷却徽标即时翻绿。
		id := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				id = s
			}
		}
		if id == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "grok" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 Grok 类型"})
			return data, true, nil
		}
		ok := a.accountMgr.ClearAccountCooldown(id)
		if ok {
			a.emitAccountsRes()
			a.AddLog(fmt.Sprintf("🧊 [Grok] 已手动解冻账号 %s (id=%s)", acc.Email, id))
		}
		data, _ := marshalResponse(map[string]interface{}{"success": true, "cleared": ok})
		return data, true, nil

	case "grok:fetch-models":
		// args: [baseURL, apiKey]
		// 与 nvidia:fetch-models 同构: 复用通用 OpenAI list 探活(modelfetch.FetchModels 经 fetchRemoteGrokModels)。
		// baseURL 留空时回退 account.DefaultGrokBaseURL(https://api.x.ai/v1), 与 AddGrokAccount 同口径。
		baseURL := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				baseURL = strings.TrimSpace(s)
			}
		}
		apiKey := ""
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				apiKey = strings.TrimSpace(s)
			}
		}
		if baseURL == "" {
			baseURL = account.DefaultGrokBaseURL
		}

		models, ferr := fetchRemoteGrokModels(baseURL, apiKey)
		if ferr != nil {
			a.AddLog(fmt.Sprintf("❌ [Grok] 拉取模型列表失败: %v", ferr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": ferr.Error(), "allowManualInput": true})
			return data, true, nil
		}
		if len(models) == 0 {
			a.AddLog(fmt.Sprintf("⚠️ [Grok] 上游 [%s] 返回的模型列表为空,可手动填写模型名", baseURL))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "上游返回的模型列表为空,请手动填写模型名", "allowManualInput": true})
			return data, true, nil
		}
		a.AddLog(fmt.Sprintf("✅ [Grok] 成功获取到 %d 个模型 (baseURL=%s)", len(models), baseURL))
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"models":  models,
		})
		return data, true, nil

	case "grok:check-auth":
		// 手动触发一次「Grok 号池授权过期检查→刷新→失效停用」,复用后端 CheckAndPurgeGrokAuth
		// (与 1h 定时 tick 同一逻辑)。无参:检查全部 grok OAuth 账号。
		// 判定主走 access_token(JWT)exp:仍有效 skipped / 临近过期刷新成功 refreshed /
		// 刷新命中永久失败(invalid_grant 等)已 UpdateAccountEnabled(false) disabled / 瞬时失败 failed。
		// 停用已发生在 CheckAndPurgeGrokAuth→RefreshAccountTokenSync→refreshXaiToken 链路内,
		// 此处汇总计数 + AddLog + emitAccountsRes 广播(账号状态变更,前端需即时刷新)。
		results := a.accountMgr.CheckAndPurgeGrokAuth()
		var disabled, refreshed, skipped, failed int
		for _, r := range results {
			switch r.Outcome {
			case "disabled", "removed":
				disabled++
			case "refreshed":
				refreshed++
			case "skipped":
				skipped++
			case "failed":
				failed++
			}
		}
		total := len(results)
		a.AddLog(fmt.Sprintf("🔍 [Grok 检查授权] 完成,共 %d 个 OAuth 账号:刷新 %d / 停用 %d / 跳过 %d / 失败 %d",
			total, refreshed, disabled, skipped, failed))
		// 广播一次让前端卡片即时刷新状态
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{
			"success":   true,
			"total":     total,
			"disabled":  disabled,
			"removed":   disabled, // 兼容前端历史解构
			"refreshed": refreshed,
			"skipped":   skipped,
			"failed":    failed,
			"details":   results,
		})
		return data, true, nil
	}

	return "", false, nil
}

// fetchRemoteGrokModels 请求 Grok(xAI) 上游 /v1/models 模型列表端点,供 grok:fetch-models IPC 复用。
// 与 fetchRemoteNvidiaModels 同构, 均委托 internal/modelfetch.FetchModels 兼容 {data:[{id}]} /
// {models:[{id}]} 两种响应形态; baseURL 留空回退 account.DefaultGrokBaseURL(https://api.x.ai/v1),
// 与 AddGrokAccount / fetchGrokQuota 同口径。xAI 官方端点即标准 OpenAI list 形态, 候选端点续试
// 逻辑对 xAI 同样生效(未来若 xAI 改挂兼容子路径可自动兜底)。
func fetchRemoteGrokModels(baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		baseURL = account.DefaultGrokBaseURL
	}
	return modelfetch.FetchModels(baseURL, apiKey)
}
