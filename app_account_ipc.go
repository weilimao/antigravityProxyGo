package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
)

// handleAccountIPC 处理账号相关的自定义 IPC invoke 呼叫(批量触发测试回复 / 批量删除 / 明文查看 Key),
// 并按号池切面委派 nvidia / other / grok 三个子 handler。
// 原 1017 行巨函数(34 个 case)已按 provider 拆分:本文件保留跨号池的通用账号操作,
// nvidia/other/grok 号池 CRUD 分别下沉至 app_account_ipc_nvidia.go / app_account_ipc_other.go /
// app_account_ipc_grok.go。委派链保持「命中即返回」语义,与拆分前分发顺序一致。
func (a *App) handleAccountIPC(channel string, args []interface{}) (string, bool, error) {
	// 先委派号池专属分支;若命中则直接返回,不再进入通用 switch。
	if res, handled, err := a.handleAccountIPCNvidia(channel, args); handled {
		return res, handled, err
	}
	if res, handled, err := a.handleAccountIPCOther(channel, args); handled {
		return res, handled, err
	}
	if res, handled, err := a.handleAccountIPCGrok(channel, args); handled {
		return res, handled, err
	}
	if res, handled, err := a.handleAccountIPCWorkBuddy(channel, args); handled {
		return res, handled, err
	}
	if res, handled, err := a.handleAccountIPCOpenCode(channel, args); handled {
		return res, handled, err
	}

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

	case "accounts:batch-remove":
		var ids []string
		if len(args) > 0 {
			if slice, ok := args[0].([]interface{}); ok {
				for _, item := range slice {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						ids = append(ids, strings.TrimSpace(s))
					}
				}
			} else if sliceStr, ok := args[0].([]string); ok {
				ids = sliceStr
			}
		}
		if len(ids) == 0 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "没有可删除的账号 ID"})
			return data, true, nil
		}

		removedCount := 0
		for _, id := range ids {
			if acc := a.accountMgr.GetAccountByID(id); acc != nil {
				a.accountMgr.RemoveAccount(id)
				removedCount++
			}
		}

		if removedCount > 0 {
			a.emitAccountsRes()
			a.AddLog(fmt.Sprintf("🗑️ [批量删除] 成功批量删除 %d 个账号 (请求共 %d 个)", removedCount, len(ids)))
		}

		data, _ := marshalResponse(map[string]interface{}{
			"success":      true,
			"removedCount": removedCount,
			"totalCount":   len(ids),
		})
		return data, true, nil

	case "account:reveal-key":
		// 明文查看 API Key(编辑号池账号时眼睛切明文)。
		// args: [accountId, provider] —— provider 限定可被查看明文的池类型(nvidia / other / grok),
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
		if provider != "nvidia" && provider != "other" && provider != "grok" && provider != "opencode" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "provider 仅支持 nvidia/other/grok/opencode"})
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
