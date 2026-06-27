package main

import (
	"encoding/base32"
	"encoding/json"
	"strings"

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
	}

	return "", false, nil
}
