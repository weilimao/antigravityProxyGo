package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
)

// handleAccountIPC 处理账号相关的自定义 IPC 呼叫，如批量触发测试回复。
func (a *App) handleAccountIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	case "accounts:trigger-test-response":
		var payload struct {
			AccountIDs []string `json:"accountIds"`
			ModelNames []string `json:"modelNames"`
			ModelName  string   `json:"modelName"`
			Prompt     string   `json:"prompt"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		if len(payload.AccountIDs) == 0 {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "没有选中的账号"})
			return data, true, err
		}

		models := payload.ModelNames
		if len(models) == 0 && payload.ModelName != "" {
			models = []string{payload.ModelName}
		}

		if len(models) == 0 {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "请选择模型"})
			return data, true, err
		}

		type ModelResult struct {
			Model    string `json:"model"`
			Success  bool   `json:"success"`
			Response string `json:"response,omitempty"`
			Error    string `json:"error,omitempty"`
		}

		type AccountResult struct {
			Email        string        `json:"email"`
			Success      bool          `json:"success"`
			ModelResults []ModelResult `json:"modelResults"`
		}

		results := make([]AccountResult, len(payload.AccountIDs))
		var wg sync.WaitGroup

		a.AddLog(fmt.Sprintf("⚡ [测试回复] 开始批量对 %d 个账号触发 %d 个模型的最短回复...", len(payload.AccountIDs), len(models)))

		for i, id := range payload.AccountIDs {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				results[i] = AccountResult{
					Email:   id,
					Success: false,
					ModelResults: []ModelResult{
						{Model: "all", Success: false, Error: "账号未找到"},
					},
				}
				continue
			}

			wg.Add(1)
			go func(idx int, targetAcc *account.Account) {
				defer wg.Done()

				modelResults := make([]ModelResult, len(models))
				successModels := 0
				for mIdx, model := range models {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					respText, err := account.TriggerTestResponse(
						ctx,
						targetAcc,
						model,
						payload.Prompt,
						a.quotaSvc.GetStoredProject,
						a.authMgr.RefreshToken,
					)
					cancel()

					if err != nil {
						a.AddLog(fmt.Sprintf("❌ [测试回复] 账号 %s 触发模型 %s 失败: %v", targetAcc.Email, model, err))
						modelResults[mIdx] = ModelResult{
							Model:   model,
							Success: false,
							Error:   err.Error(),
						}
					} else {
						a.AddLog(fmt.Sprintf("✅ [测试回复] 账号 %s 触发模型 %s 成功！响应: %s", targetAcc.Email, model, respText))
						modelResults[mIdx] = ModelResult{
							Model:    model,
							Success:  true,
							Response: respText,
						}
						successModels++
					}
				}

				results[idx] = AccountResult{
					Email:        targetAcc.Email,
					Success:      successModels > 0,
					ModelResults: modelResults,
				}
			}(i, acc)
		}

		wg.Wait()

		successCount := 0
		for _, r := range results {
			if r.Success {
				successCount++
			}
		}

		a.AddLog(fmt.Sprintf("🏁 [测试回复] 批量触发完成！成功: %d/%d", successCount, len(payload.AccountIDs)))
		data, err := marshalResponse(map[string]interface{}{
			"success":      true,
			"results":      results,
			"successCount": successCount,
			"totalCount":   len(payload.AccountIDs),
		})
		return data, true, err

	// ========== NVIDIA 号池 CRUD ==========

	case "nvidia:add":
		// args: [baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?]
		// 前端按顺序传参;label/模型字段可留空。
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
		in := account.NvidiaAccountInput{
			BaseURL:      strAt(0),
			APIKey:       strAt(1),
			Label:        strAt(2),
			DefaultModel: strAt(3),
			ModelSonnet:  strAt(4),
			ModelOpus:    strAt(5),
			ModelHaiku:   strAt(6),
			ModelFable:   strAt(7),
		}
		id, err := a.accountMgr.AddNvidiaAccount(in)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 添加账号失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"nvidiaPoolMode":    a.accountMgr.GetNvidiaPoolMode(),
			"nvidiaLBMode":      a.accountMgr.GetNvidiaLBMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})
		a.AddLog(fmt.Sprintf("✅ [NVIDIA] 添加账号成功: %s (id=%s)", in.BaseURL, id))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "id": id})
		return data, true, nil

	case "nvidia:update":
		// args: [accountId, baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?]
		// 与 nvidia:add 位置参数对齐;apiKey 留空表示保持不变。
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
		inU := account.NvidiaAccountInput{
			BaseURL:      strAtU(1),
			APIKey:       strAtU(2),
			Label:        strAtU(3),
			DefaultModel: strAtU(4),
			ModelSonnet:  strAtU(5),
			ModelOpus:    strAtU(6),
			ModelHaiku:   strAtU(7),
			ModelFable:   strAtU(8),
		}
		_, uerr := a.accountMgr.UpdateNvidiaAccount(idU, inU)
		if uerr != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 更新账号失败: %v", uerr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": uerr.Error()})
			return data, true, nil
		}
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"nvidiaPoolMode":    a.accountMgr.GetNvidiaPoolMode(),
			"nvidiaLBMode":      a.accountMgr.GetNvidiaLBMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})
		a.AddLog(fmt.Sprintf("✅ [NVIDIA] 更新账号成功 id=%s", idU))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "nvidia:remove":
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
		a.accountMgr.RemoveAccount(id)
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"nvidiaPoolMode":    a.accountMgr.GetNvidiaPoolMode(),
			"nvidiaLBMode":      a.accountMgr.GetNvidiaLBMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})
		a.AddLog(fmt.Sprintf("🗑️ [NVIDIA] 已移除账号 id=%s", id))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "nvidia:toggle-enabled":
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
		// 仅对 nvidia 账号生效，避免误操作其他 provider 账号
		acc := a.accountMgr.GetAccountByID(id)
		if acc == nil || acc.Provider != "nvidia" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 NVIDIA 类型"})
			return data, true, nil
		}
		a.accountMgr.UpdateAccountEnabled(id, enabled)
		wailsRuntime.EventsEmit(a.ctx, "accounts-res", map[string]interface{}{
			"accounts":          a.accountMgr.GetAccounts(),
			"poolMode":          a.accountMgr.GetPoolMode(),
			"projectPoolMode":   a.accountMgr.GetProjectPoolMode(),
			"geminiCliPoolMode": a.accountMgr.GetGeminiCliPoolMode(),
			"nvidiaPoolMode":    a.accountMgr.GetNvidiaPoolMode(),
			"nvidiaLBMode":      a.accountMgr.GetNvidiaLBMode(),
			"activeChannel":     a.accountMgr.GetActiveChannel(),
		})
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		a.AddLog(fmt.Sprintf("🔄 [NVIDIA] 账号 %s is now %s.", acc.Email, status))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "nvidia:fetch-models":
		// args: [baseURL, apiKey]
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
			baseURL = account.DefaultNvidiaBaseURL
		}

		models, ferr := fetchRemoteNvidiaModels(baseURL, apiKey)
		if ferr != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 拉取模型列表失败: %v", ferr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": ferr.Error()})
			return data, true, nil
		}
		a.AddLog(fmt.Sprintf("✅ [NVIDIA] 成功获取到 %d 个模型 (baseURL=%s)", len(models), baseURL))
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"models":  models,
		})
		return data, true, nil

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
		a.emitAccountsRes()
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

	case "account:reveal-key":
		// 明文查看 API Key(编辑号池账号时眼睛切明文)。
		// args: [accountId, provider] —— provider 限定可被查看明文的池类型(nvidia / other),
		// 对齐 other:remove/toggle 的安全守卫:账号必须存在且 Provider 匹配,否则拒绝下发。
		// 安全说明(用户已确认):这是唯一把明文 Key 下发给渲染层的通道,仅用于编辑态"看回 Key";
		// GetAccounts()/renderAccounts 仍保持脱敏,不扩散明文到列表/抓包展示。
		accID := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				accID = s
			}
		}
		provider := ""
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				provider = s
			}
		}
		provider = strings.ToLower(strings.TrimSpace(provider))
		if accID == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "缺少 accountId"})
			return data, true, nil
		}
		if provider != "nvidia" && provider != "other" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "provider 仅支持 nvidia/other"})
			return data, true, nil
		}
		acc := a.accountMgr.GetAccountByID(accID)
		if acc == nil || strings.ToLower(strings.TrimSpace(acc.Provider)) != provider {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "账号不存在或非 " + provider + " 类型"})
			return data, true, nil
		}
		plainKey := acc.GetAccessToken()
		if strings.TrimSpace(plainKey) == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "该账号未配置 API Key"})
			return data, true, nil
		}
		a.AddLog(fmt.Sprintf("🔍 [%s] 明文查看 Key 账号 id=%s group=%s", provider, accID, acc.GroupID))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "apiKey": plainKey})
		return data, true, nil
	}

	return "", false, nil
}

// fetchRemoteNvidiaModels 请求上游 NVIDIA (OpenAI 兼容) 模型列表端点 /v1/models,
// 兼容 {data:[{id}]} 与 {models:[{id}]} 两种响应形态,去重排序后返回。
// baseURL 留空时使用 account.DefaultNvidiaBaseURL;apiKey 可为空(部分上游匿名可列模型)。
// 抽自原 nvidia:fetch-models case,供账号级与全局专属模型清单两路复用。
func fetchRemoteNvidiaModels(baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		baseURL = account.DefaultNvidiaBaseURL
	}
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))

	endpoint := baseURL + "/v1/models"
	if strings.HasSuffix(baseURL, "/v1") {
		endpoint = baseURL + "/models"
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建模型获取请求失败: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var parseRes struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(bodyBytes, &parseRes); err != nil {
		return nil, fmt.Errorf("解析模型数据失败: %v", err)
	}

	modelSet := make(map[string]bool)
	for _, item := range parseRes.Data {
		if item.ID != "" {
			modelSet[item.ID] = true
		}
	}
	for _, item := range parseRes.Models {
		if item.ID != "" {
			modelSet[item.ID] = true
		}
	}
	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)
	return models, nil
}
