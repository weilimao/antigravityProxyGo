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
		baseURL = strings.TrimRight(baseURL, "/")

		endpoint := baseURL + "/v1/models"
		if strings.HasSuffix(baseURL, "/v1") {
			endpoint = baseURL + "/models"
		}

		req, err := http.NewRequestWithContext(context.Background(), "GET", endpoint, nil)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 创建模型获取请求失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}

		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		req.Header.Set("Accept", "application/json")

		client := &http.Client{
			Timeout: 15 * time.Second,
		}
		resp, err := client.Do(req)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 拉取模型列表失败: %v", err))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": fmt.Sprintf("网络请求失败: %v", err)})
			return data, true, nil
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "读取响应失败"})
			return data, true, nil
		}

		if resp.StatusCode != http.StatusOK {
			a.AddLog(fmt.Sprintf("❌ [NVIDIA] 拉取模型列表返回 HTTP %d: %s", resp.StatusCode, string(bodyBytes)))
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))})
			return data, true, nil
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
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "解析模型数据失败: " + err.Error()})
			return data, true, nil
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

		a.AddLog(fmt.Sprintf("✅ [NVIDIA] 成功从 %s 获取到 %d 个模型", endpoint, len(models)))
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"models":  models,
		})
		return data, true, nil
	}

	return "", false, nil
}
