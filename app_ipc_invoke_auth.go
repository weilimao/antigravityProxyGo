package main

import (
	"encoding/json"
	"fmt"

	"antigravity-proxy/internal/account"
)

// handleAuthInvokeIPC 处理 IPCInvoke 的登录/OAuth 通道:auth:login/
// cancel-login/get-manual-oauth-url/exchange-manual-code/add-manual-account。从
// app_ipc.go IPCInvoke 尾部 switch 抽离(连续 5 case),2-tuple marshalResponse 改写为
// 3-tuple(子处理器返 (string, bool, error)),未命中返回 ("", false, nil) fall-through。
// auth:login 经 a.authMgr.StartLogin 拉起 OAuth,成功后 a.accountMgr.AddAccount 落库;
// add-manual-account 经 json 反序列化 payload 后 AddAccount。逻辑逐行等价。
func (a *App) handleAuthInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {
	case "auth:login":
		provider := "gemini-cli"
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				provider = s
			} else if m, ok := args[0].(map[string]interface{}); ok {
				if p, exists := m["provider"].(string); exists {
					provider = p
				}
			}
		}
		res, err := a.authMgr.StartLogin(provider, a.OpenPath)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ Login failed (%s): %v", provider, err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		// Save account
		projectId := ""
		if mapProj, ok := args[0].(map[string]interface{}); ok {
			if p, exists := mapProj["projectId"].(string); exists {
				projectId = p
			}
		}

		if res["access_token"] != nil {
			token := res["access_token"].(string)
			email := res["email"].(string)
			refresh := ""
			if res["refresh_token"] != nil {
				refresh = res["refresh_token"].(string)
			}

			a.accountMgr.AddAccount(&account.Account{
				Email:        email,
				AccessToken:  token,
				RefreshToken: refresh,
				Provider:     provider,
				ProjectID:    projectId,
				ProjectLabel: projectId,
				Enabled:      true,
			})
		}

		return marshalResponse(map[string]interface{}{"success": true, "email": res["email"]})

	case "auth:cancel-login":
		a.authMgr.CancelLogin()
		return marshalResponse(map[string]interface{}{"success": true})

	case "auth:xai-login":
		res, err := a.authMgr.StartXaiLogin(a.OpenPath)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ Grok 授权登录启动失败: %v", err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		// 成功返回需带 success:true,否则前端 `!res.success` 会误判为失败。
		res["success"] = true
		return marshalResponse(res)

	case "auth:xai-status":
		state := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				state = s
			} else if m, ok := args[0].(map[string]interface{}); ok {
				if s, exists := m["state"].(string); exists {
					state = s
				}
			}
		}
		if state == "" {
			return marshalResponse(map[string]interface{}{"success": false, "error": "缺少授权会话状态"})
		}
		status, err := a.authMgr.GetXaiLoginStatus(state)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		if status["status"] == "success" {
			email := ""
			if v, ok := status["email"].(string); ok {
				email = v
			}
			at := ""
			if v, ok := status["access_token"].(string); ok {
				at = v
			}
			rt := ""
			if v, ok := status["refresh_token"].(string); ok {
				rt = v
			}
			baseURL := ""
			if v, ok := status["base_url"].(string); ok {
				baseURL = v
			}
			tokenEP := ""
			if v, ok := status["token_endpoint"].(string); ok {
				tokenEP = v
			}
			if baseURL == "" {
				baseURL = "https://cli-chat-proxy.grok.com/v1"
			}
			a.accountMgr.AddAccount(&account.Account{
				Email:         email,
				AccessToken:   at,
				RefreshToken:  rt,
				Provider:      "grok",
				ScopeType:     "grok",
				BaseURL:       baseURL,
				TokenEndpoint: tokenEP,
				// 不写 DefaultModel:OAuth 授权登录后不显示默认模型,客户端传什么模型就
				// 路由到中继模型映射配置的模型(见 ResolveGrokModel 客户端模型优先透传)。
				Enabled: true,
			})
			a.AddLog(fmt.Sprintf("✅ Grok 授权登录成功: %s", email))
			a.emitAccountsRes()
			// 前端轮询按 status 字段判定成功/失败,必须携带 status:"success",
			// 否则弹窗轮询线程永远不命中,持续挂起"授权中"。
			return marshalResponse(map[string]interface{}{"success": true, "status": "success", "email": email})
		}
		return marshalResponse(status)

	case "auth:get-manual-oauth-url":
		res := a.authMgr.GenerateManualOAuthURL()
		return marshalResponse(res)

	case "auth:exchange-manual-code":
		code := ""
		verifier := ""
		if len(args) > 0 {
			if mapData, ok := args[0].(map[string]interface{}); ok {
				if c, exists := mapData["code"].(string); exists {
					code = c
				}
				if v, exists := mapData["code_verifier"].(string); exists {
					verifier = v
				}
			}
		}

		tokenData, err := a.authMgr.ExchangeCodeForTokenManual(code, verifier)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}

		email, err := a.authMgr.GetUserEmail(tokenData.AccessToken, "project")
		if err != nil {
			email = "Unknown"
		}

		return marshalResponse(map[string]interface{}{
			"success":         true,
			"email":           email,
			"access_token":    tokenData.AccessToken,
			"refresh_token":   tokenData.RefreshToken,
			"activeProjectId": "",
			"projects":        []interface{}{},
		})

	case "auth:add-manual-account":
		var payload struct {
			Email        string `json:"email"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ProjectID    string `json:"projectId"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		a.accountMgr.AddAccount(&account.Account{
			Email:        payload.Email,
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			Provider:     "project",
			ProjectID:    payload.ProjectID,
			ProjectLabel: payload.ProjectID,
			Enabled:      true,
		})
		return marshalResponse(map[string]interface{}{"success": true})
	}

	return "", false, nil
}
