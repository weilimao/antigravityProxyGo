package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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
	}

	return "", false, nil
}
