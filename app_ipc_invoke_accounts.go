package main

import (
	"encoding/base32"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/dialogs"
)

// handleAccountsInvokeIPC 处理 IPCInvoke 的账号/号池/配额通道:accounts:export-all/
// export-single/import/pool:clear-sessions/quota:fetch/update-2fa。从 app_ipc.go
// IPCInvoke 尾部 switch 抽离(连续 6 case,含 export-all 嵌套 switch provider 的定位文件名
// 分支),2-tuple marshalResponse 改写为 3-tuple(子处理器返 (string, bool, error)),
// 未命中返回 ("", false, nil) fall-through。export-all 的内层 switch provider 逐字保留
// (antigravity/project/nvidia/default 四分支选 DefaultName+logMsg),brace-balance 已含嵌套。
// update-2fa 经 base32 校验 TOTP 密钥格式。逻辑逐行等价。
func (a *App) handleAccountsInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "accounts:export-all":
		provider := getStringArg(0)
		var defaultName string
		var logMsg string
		switch provider {
		case "antigravity":
			defaultName = "accounts_antigravity_export.json"
			logMsg = "📥 [账号导出] 成功导出 Antigravity 官方账号"
		case "project":
			defaultName = "accounts_project_export.json"
			logMsg = "📥 [账号导出] 成功导出 谷歌云项目 API 账号"
		case "nvidia":
			defaultName = "accounts_nvidia_export.json"
			logMsg = "📥 [账号导出] 成功导出 NVIDIA 号池账号"
		default:
			defaultName = "accounts_export.json"
			logMsg = "📥 [账号导出] 成功导出所有账号"
		}

		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出账号配置",
			DefaultName: defaultName,
			Filters:     []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		accounts := a.accountMgr.GetRawAccountsByProvider(provider)
		if accounts == nil {
			accounts = []*account.Account{}
		}
		data, _ := json.MarshalIndent(map[string]interface{}{"accounts": accounts}, "", "  ")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			a.AddLog(fmt.Sprintf("❌ [账号导出] 写盘失败: %v", err))
			return marshalResponse(false)
		}
		a.AddLog(logMsg)
		// 保存成功后自动打开文件夹并精确定位到该文件
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "accounts:export-single":
		acc := a.accountMgr.GetAccountByID(getStringArg(0))
		if acc == nil {
			return marshalResponse(false)
		}
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出单账号配置",
			DefaultName: fmt.Sprintf("account_%s.json", acc.Email),
			Filters:     []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		data, _ := json.MarshalIndent(map[string]interface{}{"accounts": []*account.Account{acc}}, "", "  ")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			a.AddLog(fmt.Sprintf("❌ [账号导出] 写盘失败: %v", err))
			return marshalResponse(false)
		}
		a.AddLog("📥 [账号导出] 成功导出账号: " + acc.Email)
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "accounts:import":
		filePath, ok, _ := a.dialogSvc.Open(a.ctx, dialogs.OpenRequest{
			Title:   "导入账号配置",
			Filters: []dialogs.FileFilter{{DisplayName: "JSON Files", Pattern: "*.json"}},
		})
		if !ok {
			// 用户取消,返回 success:true(空结果)避免前端误判为失败
			return marshalResponse(map[string]interface{}{"success": true, "added": 0, "dir": ""})
		}
		var addedCount int
		if fileData, err := os.ReadFile(filePath); err == nil {
			var wrapper struct {
				Accounts []*account.Account `json:"accounts"`
			}
			if json.Unmarshal(fileData, &wrapper) == nil && len(wrapper.Accounts) > 0 {
				addedCount = a.accountMgr.ImportAccountsList(wrapper.Accounts)
				if addedCount > 0 {
					a.AddLog(fmt.Sprintf("📥 [账号导入] 成功导入 %d 个账号", addedCount))
				}
			}
		}
		// 关键:导入(含新增)落库后必须广播 accounts-res,让前端刷新 state.currentAccountsList。
		// 否则前端只在页面打开时拉一次 accounts:get,导入后仍持旧快照,切到对应号池 Tab
		// 过滤 renderAccounts 时看不到新导入的账号(如 Grok 号池 5 个 xai 账号只显示 0 个)。
		// 与 nvidia:add / other:add / grok:add 等新增路径的 emitAccountsRes() 对齐。
		if addedCount > 0 {
			a.emitAccountsRes()
		}
		// 返回成功状态 + 导入文件所在目录,供前端据此"定位到之前选择的文件夹"
		return marshalResponse(map[string]interface{}{
			"success": true,
			"added":   addedCount,
			"dir":     filepath.Dir(filePath),
		})

	case "pool:clear-sessions":
		cleared := a.sessionRouter.ClearAllAndSave()
		a.AddLog(fmt.Sprintf("🧹 [粘性路由] 手动清空所有会话绑定，共 %d 条。", cleared))
		return marshalResponse(map[string]interface{}{"success": true, "cleared": cleared})

	case "quota:fetch":
		accId := getStringArg(0)
		acc := a.accountMgr.GetAccountByID(accId)
		if acc == nil {
			a.AddLog("❌ [配额刷新] 无法刷新配额：未找到对应的账号")
			return marshalResponse(map[string]interface{}{"error": "Account not found", "buckets": []interface{}{}})
		}
		if acc.Provider == "nvidia" {
			a.AddLog(fmt.Sprintf("🔄 [配额刷新] NVIDIA 账号 %s 正在请求上游验证可用性...", acc.Email))
			res, err := a.accountMgr.FetchQuota(acc)
			if err != nil {
				a.AddLog(fmt.Sprintf("❌ [配额刷新] NVIDIA 账号 %s 配额请求失败: %v", acc.Email, err))
				return marshalResponse(map[string]interface{}{"error": err.Error(), "buckets": []interface{}{}})
			}
			a.accountMgr.UpdateAccountQuota(accId, res)
			modelCount := 0
			if res.Credits != nil {
				modelCount = int(*res.Credits)
			}
			a.AddLog(fmt.Sprintf("✅ [配额刷新] NVIDIA 账号 %s 可用，可用模型数 %d 个", acc.Email, modelCount))
			return marshalResponse(res)
		}
		if acc.Provider == "workbuddy" {
			if account.IsWorkBuddyDomesticAccount(acc) {
				a.AddLog(fmt.Sprintf("ℹ️ [配额刷新] WorkBuddy 账号 %s 属于国内邮箱注册，无需请求配额积分（推理正常）", acc.Email))
				res := &account.QuotaResult{Tier: "Free", Buckets: []account.QuotaBucket{}}
				return marshalResponse(res)
			}
			a.AddLog(fmt.Sprintf("🔄 [配额刷新] 开始刷新 WorkBuddy 国外账号 %s 的官方配额积分...", acc.Email))
			res, err := a.accountMgr.FetchQuota(acc)
			if err != nil {
				a.AddLog(fmt.Sprintf("❌ [配额刷新] WorkBuddy 账号 %s 刷新配额失败: %v", acc.Email, err))
				return marshalResponse(map[string]interface{}{"error": err.Error(), "buckets": []interface{}{}})
			}
			if acc.NoQuota {
				a.accountMgr.UpdateAccountNoQuota(accId, true)
				a.AddLog(fmt.Sprintf("ℹ️ [配额刷新] WorkBuddy 账号 %s 上游未开通计量中心，已自动标记为免配额探测（推理正常）", acc.Email))
			} else {
				a.accountMgr.UpdateAccountQuota(accId, res)
				creditsDesc := "0"
				if res.Credits != nil {
					creditsDesc = fmt.Sprintf("%.0f", *res.Credits)
				}
				a.AddLog(fmt.Sprintf("✅ [配额刷新] WorkBuddy 国外账号 %s 官方积分刷新成功！当前积分余额: %s", acc.Email, creditsDesc))
			}
			return marshalResponse(res)
		}
		a.AddLog(fmt.Sprintf("🔄 [配额刷新] 开始刷新账号 %s 的配额...", acc.Email))
		res, err := a.accountMgr.FetchQuota(acc)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [配额刷新] 账号 %s 刷新配额失败: %v", acc.Email, err))
			return marshalResponse(map[string]interface{}{"error": err.Error(), "buckets": []interface{}{}})
		}
		a.accountMgr.UpdateAccountQuota(accId, res)
		a.AddLog(fmt.Sprintf("✅ [配额刷新] 账号 %s 配额及积分刷新成功！(Tier: %s)", acc.Email, res.Tier))
		return marshalResponse(res)

	case "accounts:update-2fa":
		id := getStringArg(0)
		secret := getStringArg(1)

		if secret != "" {
			cleanSecret := strings.ReplaceAll(secret, " ", "")
			cleanSecret = strings.ToUpper(cleanSecret)
			if len(cleanSecret)%8 != 0 {
				cleanSecret += strings.Repeat("=", 8-(len(cleanSecret)%8))
			}
			_, err := base32.StdEncoding.DecodeString(cleanSecret)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": "无效的 Base32 格式，请检查密钥是否正确（支持包含空格）"})
			}
		}

		a.accountMgr.UpdateAccount2FASecret(id, secret)
		return marshalResponse(map[string]interface{}{"success": true})
	}

	return "", false, nil
}
