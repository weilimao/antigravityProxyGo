package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

// WorkBuddyTokenRefreshResult 包含通过 RefreshToken 换取到的新 Token 集合。
type WorkBuddyTokenRefreshResult struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresIn        int64  `json:"expiresIn"`
	RefreshExpiresIn int64  `json:"refreshExpiresIn"`
}

// RefreshWorkBuddyToken 向官方 /v2/plugin/auth/token/refresh 发起 POST 请求，
// 通过 X-Refresh-Token 请求头携带旧的 RefreshToken 换取最新的 AccessToken 与 RefreshToken。
func RefreshWorkBuddyToken(acc *Account) (*WorkBuddyTokenRefreshResult, error) {
	if acc == nil {
		return nil, errors.New("账号为空")
	}
	rfToken := strings.TrimSpace(acc.GetRefreshToken())
	if rfToken == "" {
		return nil, errors.New("账号未配置 refreshToken，无法执行换取")
	}

	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
		if acc.IsWorkBuddyDomestic() {
			baseURL = DefaultWorkBuddyDomesticBaseURL
		} else {
			baseURL = DefaultWorkBuddyBaseURL
		}
	}

	refreshURL := strings.TrimRight(baseURL, "/") + "/v2/plugin/auth/token/refresh"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, refreshURL, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("创建刷新 Token 请求失败: %w", err)
	}

	// 构造官方请求头并附带 X-Refresh-Token
	headers := BuildWorkBuddyHeaders(acc)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Refresh-Token", rfToken)

	client := &http.Client{Timeout: 15 * time.Second, Transport: netutil.NewTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("换取 Token 网络请求失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("凭证已失效（HTTP %d），需要重新登录导入: %s", resp.StatusCode, bodyStr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("换取 Token 失败 (HTTP %d): %s", resp.StatusCode, bodyStr)
	}

	var respData struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken      string `json:"accessToken"`
			RefreshToken     string `json:"refreshToken"`
			ExpiresIn        int64  `json:"expiresIn"`
			RefreshExpiresIn int64  `json:"refreshExpiresIn"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &respData); err != nil {
		return nil, fmt.Errorf("解析 Token 刷新响应失败: %w", err)
	}

	if respData.Code != 0 || strings.TrimSpace(respData.Data.AccessToken) == "" {
		return nil, fmt.Errorf("换取 Token 返回业务错误 (code=%d): %s", respData.Code, respData.Msg)
	}

	result := &WorkBuddyTokenRefreshResult{
		AccessToken:      strings.TrimSpace(respData.Data.AccessToken),
		RefreshToken:     strings.TrimSpace(respData.Data.RefreshToken),
		ExpiresIn:        respData.Data.ExpiresIn,
		RefreshExpiresIn: respData.Data.RefreshExpiresIn,
	}

	// 立即安全更新账号内存态
	acc.SetAccessToken(result.AccessToken)
	if result.RefreshToken != "" {
		acc.SetRefreshToken(result.RefreshToken)
	}

	return result, nil
}
