package main

import (
	"encoding/base32"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/totp"
)

// handleTotpIPC 处理与 2FA/TOTP 相关的 IPC 呼叫
// 如果返回的 handled 为 true，表示该 channel 已被此模块处理，外层 IPCInvoke 可直接返回对应结果和 err。
func (a *App) handleTotpIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
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
	case "totp:get-codes":
		type OTPInfo struct {
			AccountID string `json:"accountId"`
			Email     string `json:"email"`
			HasSecret bool   `json:"hasSecret"`
			Secret    string `json:"secret,omitempty"`
			Code      string `json:"code"`
			Remaining int    `json:"remaining"`
			Error     string `json:"error,omitempty"`
		}

		accs := a.accountMgr.GetTwoFAAccounts()
		results := make([]OTPInfo, 0, len(accs))

		for _, acc := range accs {
			info := OTPInfo{
				AccountID: acc.ID,
				Email:     acc.Email,
				HasSecret: acc.TwoFASecret != "",
			}

			if acc.TwoFASecret != "" {
				info.Secret = acc.TwoFASecret
				code, remaining, err := totp.GenerateTOTP(acc.TwoFASecret)
				if err != nil {
					info.Error = err.Error()
				} else {
					info.Code = code
					info.Remaining = remaining
				}
			}

			results = append(results, info)
		}
		data, err := marshalResponse(results)
		return data, true, err

	case "totp:add-account":
		email := getStringArg(0)
		secret := getStringArg(1)

		if email == "" {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "邮箱/账号名称不能为空"})
			return data, true, err
		}

		if secret != "" {
			cleanSecret := strings.ReplaceAll(secret, " ", "")
			cleanSecret = strings.ToUpper(cleanSecret)
			if len(cleanSecret)%8 != 0 {
				cleanSecret += strings.Repeat("=", 8-(len(cleanSecret)%8))
			}
			_, err := base32.StdEncoding.DecodeString(cleanSecret)
			if err != nil {
				data, err := marshalResponse(map[string]interface{}{"success": false, "error": "无效的 Base32 格式，请检查密钥是否正确（支持包含空格）"})
				return data, true, err
			}
		}

		a.accountMgr.AddTwoFAAccount(email, secret)
		data, err := marshalResponse(map[string]interface{}{"success": true})
		return data, true, err

	case "totp:generate-code":
		secret := getStringArg(0)
		if secret == "" {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "密钥不能为空"})
			return data, true, err
		}

		cleanSecret := strings.ReplaceAll(secret, " ", "")
		cleanSecret = strings.ToUpper(cleanSecret)
		if len(cleanSecret)%8 != 0 {
			cleanSecret += strings.Repeat("=", 8-(len(cleanSecret)%8))
		}
		_, err := base32.StdEncoding.DecodeString(cleanSecret)
		if err != nil {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": "无效的 Base32 格式，请检查密钥是否正确（支持包含空格）"})
			return data, true, err
		}

		code, remaining, err := totp.GenerateTOTP(secret)
		if err != nil {
			data, err := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, err
		}

		data, err := marshalResponse(map[string]interface{}{
			"success":   true,
			"code":      code,
			"remaining": remaining,
		})
		return data, true, err

	case "totp:export":
		if a.dialogSvc == nil {
			data, err := marshalResponse(false)
			return data, true, err
		}

		accs := a.accountMgr.GetTwoFAAccounts()
		if len(accs) == 0 {
			data, err := marshalResponse(false)
			return data, true, err
		}

		nowStr := time.Now().Format("20060102_150405")
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出 2FA 账号",
			DefaultName: fmt.Sprintf("2fa_accounts_%s.txt", nowStr),
			Filters: []dialogs.FileFilter{
				{DisplayName: "TXT Files (*.txt)", Pattern: "*.txt"},
				{DisplayName: "CSV Files (*.csv)", Pattern: "*.csv"},
				{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
			},
		})
		if !ok {
			data, err := marshalResponse(false)
			return data, true, err
		}

		var content []byte
		if strings.HasSuffix(strings.ToLower(filePath), ".csv") {
			var csv strings.Builder
			csv.WriteString("\uFEFF邮箱,2FA密钥\n")
			for _, acc := range accs {
				csv.WriteString(fmt.Sprintf("\"%s\",\"%s\"\n",
					strings.ReplaceAll(acc.Email, "\"", "\"\""),
					strings.ReplaceAll(acc.TwoFASecret, "\"", "\"\"")))
			}
			content = []byte(csv.String())
		} else if strings.HasSuffix(strings.ToLower(filePath), ".json") {
			type ExportItem struct {
				Email  string `json:"email"`
				Secret string `json:"secret"`
			}
			items := make([]ExportItem, 0, len(accs))
			for _, acc := range accs {
				items = append(items, ExportItem{
					Email:  acc.Email,
					Secret: acc.TwoFASecret,
				})
			}
			data, err := json.MarshalIndent(items, "", "  ")
			if err != nil {
				data, mErr := marshalResponse(false)
				return data, true, mErr
			}
			content = data
		} else {
			var txt strings.Builder
			for _, acc := range accs {
				if acc.TwoFASecret != "" {
					txt.WriteString(fmt.Sprintf("%s----%s\n", acc.Email, acc.TwoFASecret))
				} else {
					txt.WriteString(fmt.Sprintf("%s\n", acc.Email))
				}
			}
			content = []byte(txt.String())
		}

		if err := os.WriteFile(filePath, content, 0644); err != nil {
			a.AddLog(fmt.Sprintf("❌ [2FA导出] 写盘失败: %v", err))
			data, mErr := marshalResponse(false)
			return data, true, mErr
		}

		a.AddLog(fmt.Sprintf("📥 [2FA导出] 成功导出 %d 个 2FA 账号到: %s", len(accs), filePath))
		a.dialogSvc.RevealFile(filePath)
		data, err := marshalResponse(true)
		return data, true, err
	}

	return "", false, nil
}
