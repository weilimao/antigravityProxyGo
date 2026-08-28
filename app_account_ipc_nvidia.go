package main

import (
	"encoding/json"
	"fmt"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/modelfetch"
)

// handleAccountIPCNvidia 处理 NVIDIA 号池 CRUD 相关的 IPC invoke 分支。
// 从 handleAccountIPC 按 provider 切面拆分而出:原 34 个 case 的巨函数按号池归属下沉,
// 本方法承接 nvidia:* 全部 8 个 case。marshalResponse 保持局部闭包风格(与 app_autotrigger_ipc.go
// / app_externalconfig_ipc.go 一致),物理搬移,逻辑逐行等价,零回归。
func (a *App) handleAccountIPCNvidia(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
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
			EgressIP:     strAt(8),
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
		// args: [accountId, baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?, egressIp?]
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
			EgressIP:     strAtU(9),
		}
		_, uerr := a.accountMgr.UpdateNvidiaAccount(idU, inU)
		if uerr != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 更新账号失败: %v", uerr))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": uerr.Error()})
			return data, true, nil
		}
		// 改名联动使用详情:把 usage.json 中该账号桶缓存的展示名同步为新名,
		// 使「使用详情」页无需等该账号再次出请求即可即时刷新(只改展示名副本,不动 Token/成本数值)。
		if updatedAcc := a.accountMgr.GetAccountByID(idU); updatedAcc != nil {
			a.usageTracker.RenameAccountByID(updatedAcc.ID, updatedAcc.Email)
		}
		// 与 grok:update 同走 emitAccountsRes(广播完整号池快照,含 Other/Grok 字段),
		// 替代此前缺失这些字段的局部 accounts-res,避免改名后切号池 tab 时状态丢失。
		a.emitAccountsRes()
		// 主动下发一次含 usage 的完整统计载荷,让前端「使用详情」即时重渲染新名。
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
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

	case "nvidia:get-residential-subnets":
		subnets := account.GetResidentialSubnets()
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"subnets": subnets,
		})
		return data, true, nil

	case "nvidia:generate-subnet-ip":
		subnetID := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				subnetID = strings.TrimSpace(s)
			}
		}
		ip, err := account.GenerateRandomIPFromSubnet(subnetID)
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		data, _ := marshalResponse(map[string]interface{}{"success": true, "ip": ip})
		return data, true, nil

	case "nvidia:batch-assign-egress-ip":
		opts := account.ParseBatchAssignIPOptions(args)
		updatedCount, err := a.accountMgr.BatchAssignNvidiaEgressIP(opts)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 批量分配出口 IP 失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}

		a.emitAccountsRes()
		a.AddLog(fmt.Sprintf("✅ [NVIDIA] 成功为 %d 个账号分配出口 IP", updatedCount))
		data, _ := marshalResponse(map[string]interface{}{
			"success":      true,
			"updatedCount": updatedCount,
		})
		return data, true, nil
	}

	return "", false, nil
}

// fetchRemoteNvidiaModels 请求上游 NVIDIA (OpenAI 兼容) 模型列表端点 /v1/models,
// 兼容 {data:[{id}]} 与 {models:[{id}]} 两种响应形态,去重排序后返回。
// baseURL 留空时使用 account.DefaultNvidiaBaseURL;apiKey 可为空(部分上游匿名可列模型)。
// 抽自原 nvidia:fetch-models case,供账号级与全局专属模型清单两路复用。
//
// 实际探测委托 internal/modelfetch:对 Base URL 生成候选端点列表并按序尝试,
// 命中已知「Anthropic 协议兼容子路径」(如 /anthropic、/api/coding)时剥离后缀兜底到
// 根域 /v1/models,404/405 续试直至命中。使 DeepSeek/Kimi/智谱等把 Anthropic 挂在
// 兼容子路径上的官方供应商也能自动取到模型,而非直接 404 落入手填兜底。
func fetchRemoteNvidiaModels(baseURL, apiKey string) ([]string, error) {
	if baseURL == "" {
		baseURL = account.DefaultNvidiaBaseURL
	}
	return modelfetch.FetchModels(baseURL, apiKey)
}
