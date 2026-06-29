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
			ModelName  string   `json:"modelName"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		if len(payload.AccountIDs) == 0 {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "没有选中的账号"})
			return data, true, err
		}
		if payload.ModelName == "" {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "请选择模型"})
			return data, true, err
		}

		type AccountResult struct {
			Email   string `json:"email"`
			Success bool   `json:"success"`
			Error   string `json:"error,omitempty"`
		}

		results := make([]AccountResult, len(payload.AccountIDs))
		var wg sync.WaitGroup

		a.AddLog(fmt.Sprintf("⚡ [测试回复] 开始批量对 %d 个账号触发最短回复...", len(payload.AccountIDs)))

		for i, id := range payload.AccountIDs {
			acc := a.accountMgr.GetAccountByID(id)
			if acc == nil {
				results[i] = AccountResult{
					Email:   id,
					Success: false,
					Error:   "账号未找到",
				}
				continue
			}

			wg.Add(1)
			go func(idx int, targetAcc *account.Account) {
				defer wg.Done()
				
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()

				err := account.TriggerTestResponse(
					ctx, 
					targetAcc, 
					payload.ModelName, 
					a.quotaSvc.GetStoredProject, 
					a.authMgr.RefreshToken,
				)

				if err != nil {
					a.AddLog(fmt.Sprintf("❌ [测试回复] 账号 %s 触发失败: %v", targetAcc.Email, err))
					results[idx] = AccountResult{
						Email:   targetAcc.Email,
						Success: false,
						Error:   err.Error(),
					}
				} else {
					a.AddLog(fmt.Sprintf("✅ [测试回复] 账号 %s 触发成功！模型: %s", targetAcc.Email, payload.ModelName))
					results[idx] = AccountResult{
						Email:   targetAcc.Email,
						Success: true,
					}
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
