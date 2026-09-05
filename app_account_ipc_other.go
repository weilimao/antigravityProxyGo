package main

import (
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
)

// handleAccountIPCOther 处理 Other 号池(自定义多上游组)相关的 IPC invoke 分支。
// 从 handleAccountIPC 按 provider 切面拆分而出:承接 other:* 全部 10 个 case。
// parseOtherInputFromArgs / parseOtherFetchArgs 定义于 app_other_ipc.go(包级),
// 本处直接复用。marshalResponse 保持局部闭包风格,物理搬移,逻辑逐行等价,零回归。
func (a *App) handleAccountIPCOther(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	// ========== Other 号池 CRUD(自定义多上游组) ==========

	case "other:add":
		// args: [jsonInputString] 或 [groupID, baseURL, apiKey, groupName?, formatsJSON?, label?, defaultModel?]
		// 推荐前端传单对象 JSON(第 0 参为 JSON 字符串),与 nvidia:add 的位置参数风格解耦。
		in, perr := parseOtherInputFromArgs(args)
		if perr != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": perr.Error()})
			return data, true, nil
		}
		id, err := a.accountMgr.AddOtherAccount(in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [Other] 添加账号失败 (group=%s): %v", in.GroupID, err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("✅ [Other] 添加账号成功 group=%s baseURL=%s (id=%s)", in.GroupID, in.BaseURL, id))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "id": id})
		return data, true, nil

	case "other:remove":
		// args: [accountId]
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
		if acc == nil || acc.Provider != "other" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 Other 类型"})
			return data, true, nil
		}
		a.accountMgr.RemoveAccount(id)
		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("🗑️ [Other] 已移除账号 id=%s group=%s", id, acc.GroupID))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:toggle-enabled":
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
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "other" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 Other 类型"})
			return data, true, nil
		}
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		a.emitAccountsRes()
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		a.AddLog(fmt.Sprintf("🔄 [Other] 账号 %s (group %s) is now %s.", acc.Email, acc.GroupID, status))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:update":
		// args: [jsonInputString] —— 单对象 JSON 中以 accountId 定位账号,其余字段为可编辑项。
		// 与 other:add 的 JSON 形态一致,额外支持 accountId;APIKey 留空表示保持不变。
		if len(args) < 1 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "参数不足:至少需要 accountId 与账号字段"})
			return data, true, nil
		}
		rawU, okU := args[0].(string)
		if !okU || strings.TrimSpace(rawU) == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "参数必须为 JSON 对象字符串"})
			return data, true, nil
		}
		var upObj struct {
			AccountID    string   `json:"accountId"`
			GroupID      string   `json:"groupId"`
			GroupName    string   `json:"groupName"`
			BaseURL      string   `json:"baseUrl"`
			APIKey       string   `json:"apiKey"`
			Formats      []string `json:"formats"`
			Label        string   `json:"label"`
			DefaultModel string   `json:"defaultModel"`
		}
		if err := json.Unmarshal([]byte(rawU), &upObj); err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "解析 Other 更新 JSON 失败: " + err.Error()})
			return data, true, nil
		}
		if strings.TrimSpace(upObj.AccountID) == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		inU := account.OtherAccountInput{
			GroupID:      upObj.GroupID,
			GroupName:    upObj.GroupName,
			BaseURL:      upObj.BaseURL,
			APIKey:       upObj.APIKey,
			Formats:      upObj.Formats,
			Label:        upObj.Label,
			DefaultModel: upObj.DefaultModel,
		}
		_, uerr := a.accountMgr.UpdateOtherAccount(upObj.AccountID, inU)
		if uerr != nil {
			a.AddLog(fmt.Sprintf("❌ [Other] 更新账号失败 (group=%s): %v", inU.GroupID, uerr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": uerr.Error()})
			return data, true, nil
		}
		// 改名联动使用详情:同步该账号桶展示名(只改展示名副本,不动 Token/成本数值)。
		if updatedAcc := a.accountMgr.GetAccountByID(upObj.AccountID); updatedAcc != nil {
			a.usageTracker.RenameAccountByID(updatedAcc.ID, updatedAcc.Email)
		}
		a.emitAccountsRes()
		// 主动下发一次含 usage 的完整统计载荷,让前端「使用详情」即时重渲染新名。
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		a.AddLog(fmt.Sprintf("✅ [Other] 更新账号成功 id=%s group=%s", upObj.AccountID, inU.GroupID))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:set-lb-mode":
		// args: [groupID, mode]
		groupID := ""
		mode := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				groupID = s
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				mode = s
			}
		}
		a.accountMgr.SetOtherLBMode(groupID, mode)
		a.AddLog(fmt.Sprintf("🔄 [Other] group %s LB mode → %s", groupID, a.accountMgr.GetOtherLBMode(groupID)))
		// 关键:保存后广播 accounts-res,让前端 state.lastBackendData.otherGroups[].lbMode 同步刷新,
		// 否则切换 tab 时 renderOtherLBMode 会用陈旧本地值把下拉还原为旧模式(与 nvidia:set-lb-mode 对齐)。
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:set-max-concurrency":
		// args: [groupID, value]。与 other:set-lb-mode 同走 invoke 双通道(前端组操作既有分布)。
		// 0=未配置回退默认 10;超过自动换号(超额降级最少并发号)。
		groupID := ""
		var v int
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				groupID = s
			}
		}
		if len(args) > 1 {
			if f, ok := args[1].(float64); ok {
				v = int(f)
			} else if i, ok := args[1].(int); ok {
				v = i
			}
		}
		a.accountMgr.SetOtherMaxConcurrency(groupID, v)
		a.AddLog(fmt.Sprintf("🔄 [Other] group %s max concurrency → %d", groupID, a.accountMgr.GetOtherMaxConcurrency(groupID)))
		// 与 other:set-lb-mode 同:广播让前端切 tab 时 renderOtherLBMode 用最新值回填,避免还原陈旧值。
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:set-worker-proxy-url":
		// args: [groupID, url]。与 other:set-lb-mode 同走 invoke 双通道。
		groupID := ""
		url := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				groupID = s
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				url = s
			}
		}
		_ = a.accountMgr.SetOtherWorkerProxyURL(groupID, url)
		a.AddLog(fmt.Sprintf("⚙️ [Other] group %s Cloudflare Worker 代理 URL: %s", groupID, a.accountMgr.GetOtherWorkerProxyURL(groupID)))
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:set-worker-proxy-enabled":
		// args: [groupID, enabled]。启用 Worker 代理出口时,以 Worker URL 覆盖组内各账号的 BaseURL。
		groupID := ""
		var enabled bool
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				groupID = s
			}
		}
		if len(args) > 1 {
			if b, ok := args[1].(bool); ok {
				enabled = b
			}
		}
		_ = a.accountMgr.SetOtherWorkerProxyEnabled(groupID, enabled)
		a.AddLog(fmt.Sprintf("⚙️ [Other] group %s Cloudflare Worker 代理出口启用: %v", groupID, a.accountMgr.IsOtherWorkerProxyEnabled(groupID)))
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "other:set-cooldown-rule":
		// args: [groupID, ruleJSON]。组级自定义冷却策略(状态码→冷却时长+模型过滤),见
		// account.OtherCooldownRule。ruleJSON 为 {enabled,statusCodes[],cooldownSecs,models[]};
		// 全字段为空等价清除该组规则。与 other:set-lb-mode 同走 invoke 双通道并广播 accounts-res 回显。
		groupID := ""
		ruleJSON := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				groupID = s
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				ruleJSON = s
			}
		}
		var rule account.OtherCooldownRule
		if strings.TrimSpace(ruleJSON) != "" {
			if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
				data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "解析冷却规则 JSON 失败: " + err.Error()})
				return data, true, nil
			}
		}
		saved := a.accountMgr.SetOtherCooldownRule(groupID, rule)
		if saved.Enabled && len(saved.StatusCodes) > 0 {
			a.AddLog(fmt.Sprintf("🧊 [Other] group %s 自定义冷却已启用: 状态码 %v → 冷却 %ds, 模型 %v", groupID, saved.StatusCodes, saved.CooldownSecs, saved.Models))
		} else {
			a.AddLog(fmt.Sprintf("🧊 [Other] group %s 自定义冷却已关闭/清除", groupID))
		}
		a.emitAccountsRes()
		data, _ := marshalResponse(map[string]interface{}{"success": true, "cooldown": saved})
		return data, true, nil

	case "other:list-groups":
		groups := a.accountMgr.GetOtherGroups()
		data, _ := marshalResponse(map[string]interface{}{"success": true, "groups": groups})
		return data, true, nil

	case "other:fetch-models":
		// args: [groupID] 按组拉模型:统一打 {BaseURL}/v1/models(含 Anthropic-only 组也尝试上游)。
		// Anthropic 官方上游无公开 /v1/models 端点时返回错误,前端改为手动填写模型名。
		// 首个参数既支持纯 groupID 字符串,也支持单对象 JSON(含 groupID + baseURL + apiKey 透传,便于未入库时预拉)。
		groupID, directBaseURL, directAPIKey, directFormats, parseErr := parseOtherFetchArgs(args)
		if parseErr != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": parseErr.Error()})
			return data, true, nil
		}
		_ = directFormats // formats 当前不参与分支(统一打上游),保留入参兼容前端。

		// 优先用直接透传的 baseURL/apiKey(未入库预拉场景);否则查号池该组首个可用账号。
		baseURL := directBaseURL
		apiKey := directAPIKey
		if baseURL == "" {
			probeAcc := a.accountMgr.GetEnabledOtherAccounts(groupID)
			if len(probeAcc) > 0 {
				baseURL = probeAcc[0].BaseURL
				if apiKey == "" {
					apiKey = probeAcc[0].GetAccessToken()
				}
			}
		} else if apiKey == "" {
			// 编辑态:前端 Key 框留空(留空表示保持不变),但这里需要真实 Key 才能探测上游。
			// 故按组查号池首个可用账号回退其 Key,避免 401 Token not provided。
			probeAcc := a.accountMgr.GetEnabledOtherAccounts(groupID)
			if len(probeAcc) > 0 {
				apiKey = probeAcc[0].GetAccessToken()
			}
		}

		if baseURL == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": fmt.Sprintf("组 [%s] 下暂无已启用账号或未提供 baseURL", groupID)})
			return data, true, nil
		}

		// 统一打上游 /v1/models;上游不支持模型列表端点时返回错误,前端手填兜底。
		models, ferr := fetchRemoteNvidiaModels(baseURL, apiKey)
		if ferr != nil {
			a.AddLog(fmt.Sprintf("⚠️ [Other] 拉取模型列表失败 (group=%s baseURL=%s): %v(可改为手动填写模型名)", groupID, baseURL, ferr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": ferr.Error(), "allowManualInput": true})
			return data, true, nil
		}
		if len(models) == 0 {
			a.AddLog(fmt.Sprintf("⚠️ [Other] 上游 [%s] 返回的模型列表为空 (group=%s),可手动填写模型名", baseURL, groupID))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "上游返回的模型列表为空,请手动填写模型名", "allowManualInput": true})
			return data, true, nil
		}
		a.AddLog(fmt.Sprintf("✅ [Other] 成功获取到 %d 个模型 (group=%s baseURL=%s)", len(models), groupID, baseURL))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "models": models})
		return data, true, nil
	}

	return "", false, nil
}
