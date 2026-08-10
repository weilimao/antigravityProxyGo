package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

// xai_oauth.go: xAI (Grok) OAuth 设备授权码流(RFC 8628)客户端。
//
// 对齐 CLIProxyAPI2 internal/auth/xai/xai.go 的端点、scope、client_id、轮询语义:
//   - OIDC 发现取 device_authorization_endpoint + token_endpoint;
//   - 设备码流:起流 POST device_authorization_endpoint → 用户浏览器打开 verification_uri_complete 授权
//     → 后台轮询 token_endpoint(grant_type=device_code),处理 authorization_pending/slow_down/
//     expired_token/access_denied;
//   - 刷新:grant_type=refresh_token + client_id(无 client_secret)。
//
// 产出 TokenData 含短效 JWT access_token + refresh_token + id_token,email/sub 从 id_token JWT 解析。

// xAI OAuth 常量(与 CLIProxyAPI2 一致)。
const (
	xaiCliProxyBaseURL = "https://cli-chat-proxy.grok.com/v1"
	xaiAPIGatewayURL   = "https://api.x.ai/v1"
	xaiDiscoveryURL    = "https://auth.x.ai/.well-known/openid-configuration"
	xaiDefaultTokenEP  = "https://auth.x.ai/oauth2/token"
	xaiClientID        = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope           = "openid profile email offline_access grok-cli:access api:access"
	xaiDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	xaiPollInterval    = 5 * time.Second
	xaiMaxPollDuration = 30 * time.Minute
	xaiHTTPTimeout     = 30 * time.Second
)

// XAIEndpoints 由 xAI OIDC 发现解析出的授权端点。
type XAIEndpoints struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

// XAIDeviceCode 是 xAI 设备授权端点返回的设备码与授权信息。
type XAIDeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	TokenEndpoint           string `json:"-"`
}

// XAITokenResult 是 xAI 设备码/刷新换取到的 token 数据(含从 id_token 解析的 email/sub)。
type XAITokenResult struct {
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token,omitempty"`
	TokenType     string `json:"token_type,omitempty"`
	ExpiresIn     int    `json:"expires_in,omitempty"`
	Expire        string `json:"expired,omitempty"`
	Email         string `json:"email,omitempty"`
	Subject       string `json:"sub,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
}

// XAIAuth 封装 xAI OAuth 设备码流的 HTTP 客户端。
type XAIAuth struct {
	httpClient *http.Client
	// minPollInterval 是轮询最小间隔(默认 xaiPollInterval=5s),拆出供测试注入更小值避免空等。
	minPollInterval time.Duration
}

// NewXAIAuth 创建 xAI OAuth 助手,出站走 app 统一 netutil client。
func NewXAIAuth() *XAIAuth {
	return &XAIAuth{httpClient: netutil.NewClient(xaiHTTPTimeout), minPollInterval: xaiPollInterval}
}

// newXAIAuthForTest 供单测注入毫秒级轮询间隔,避免 authorization_pending 空等 5s。
func newXAIAuthForTest(minInterval time.Duration) *XAIAuth {
	return &XAIAuth{httpClient: netutil.NewClient(xaiHTTPTimeout), minPollInterval: minInterval}
}

// Discover 通过 OIDC 发现解析 xAI 授权端点,并校验 host 必须落在 x.ai 域且用 https。
func (a *XAIAuth) Discover(ctx context.Context) (*XAIEndpoints, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, xaiDiscoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai discovery: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai discovery failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("xai discovery: parse response: %w", err)
	}
	deviceEP, err := validateXAIEndpoint(payload.DeviceAuthorizationEndpoint)
	if err != nil {
		return nil, err
	}
	tokenEP, err := validateXAIEndpoint(payload.TokenEndpoint)
	if err != nil {
		return nil, err
	}
	return &XAIEndpoints{
		DeviceAuthorizationEndpoint: deviceEP,
		TokenEndpoint:               tokenEP,
	}, nil
}

// validateXAIEndpoint 校验端点必须 https 且 host 属于 x.ai 域。
func validateXAIEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("xai discovery endpoint is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("xai discovery endpoint invalid: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("xai discovery endpoint must use https: %q", raw)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", fmt.Errorf("xai discovery endpoint host %q is not on x.ai", host)
	}
	return raw, nil
}

// StartDeviceFlow 请求 xAI 设备授权码并返回授权信息。
func (a *XAIAuth) StartDeviceFlow(ctx context.Context) (*XAIDeviceCode, *XAIEndpoints, error) {
	ep, err := a.Discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	dc, err := a.RequestDeviceCode(ctx, ep.DeviceAuthorizationEndpoint, ep.TokenEndpoint)
	if err != nil {
		return nil, nil, err
	}
	return dc, ep, nil
}

// RequestDeviceCode 向给定 device 端点请求设备授权码(拆出以支持测试,对齐 CLIProxyAPI2)。
func (a *XAIAuth) RequestDeviceCode(ctx context.Context, deviceAuthorizationEndpoint, tokenEndpoint string) (*XAIDeviceCode, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deviceAuthorizationEndpoint = strings.TrimSpace(deviceAuthorizationEndpoint)
	if deviceAuthorizationEndpoint == "" {
		return nil, errors.New("xai device code: device authorization endpoint is required")
	}
	form := url.Values{
		"client_id": {xaiClientID},
		"scope":     {xaiScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xai device code: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai device code request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai device code: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai device code request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dc XAIDeviceCode
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("xai device code: parse response: %w", err)
	}
	if strings.TrimSpace(dc.DeviceCode) == "" {
		return nil, errors.New("xai device code: response missing device_code")
	}
	if strings.TrimSpace(dc.UserCode) == "" {
		return nil, errors.New("xai device code: response missing user_code")
	}
	if strings.TrimSpace(dc.VerificationURI) == "" && strings.TrimSpace(dc.VerificationURIComplete) == "" {
		return nil, errors.New("xai device code: response missing verification URI")
	}
	dc.TokenEndpoint = strings.TrimSpace(tokenEndpoint)
	return &dc, nil
}

// PollForToken 轮询 token_endpoint 直到用户授权完成或设备码过期。
func (a *XAIAuth) PollForToken(ctx context.Context, dc *XAIDeviceCode) (*XAITokenResult, error) {
	if dc == nil {
		return nil, errors.New("xai device code: response is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tokenEndpoint := strings.TrimSpace(dc.TokenEndpoint)
	if tokenEndpoint == "" {
		tokenEndpoint = xaiDefaultTokenEP
	}

	interval := time.Duration(dc.Interval) * time.Second
	if interval < a.minPollInterval {
		interval = a.minPollInterval
	}
	deadline := time.Now().Add(xaiMaxPollDuration)
	if dc.ExpiresIn > 0 {
		codeDeadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
		if codeDeadline.Before(deadline) {
			deadline = codeDeadline
		}
	}

	first := true
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("xai device code: context cancelled: %w", ctx.Err())
		case <-timer.C:
			if !first && time.Now().After(deadline) {
				return nil, errors.New("xai device code expired")
			}
			first = false
			token, pollErr, nextInterval, cont := a.exchangeDeviceCode(ctx, tokenEndpoint, dc.DeviceCode, interval)
			if token != nil {
				token.TokenEndpoint = tokenEndpoint
				return token, nil
			}
			if !cont {
				return nil, pollErr
			}
			interval = nextInterval
			timer.Reset(interval)
		}
	}
}

// exchangeDeviceCode 尝试用设备码换取 token。返回 (token, err, nextInterval, shouldContinue)。
func (a *XAIAuth) exchangeDeviceCode(ctx context.Context, tokenEndpoint, deviceCode string, interval time.Duration) (*XAITokenResult, error, time.Duration, bool) {
	form := url.Values{
		"grant_type":  {xaiDeviceGrantType},
		"device_code": {strings.TrimSpace(deviceCode)},
		"client_id":   {xaiClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(tokenEndpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xai device token: create request: %w", err), interval, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai device token request failed: %w", err), interval, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai device token: read response: %w", err), interval, false
	}
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("xai device token: parse response: %w", err), interval, false
	}
	if payload.Error != "" {
		switch payload.Error {
		case "authorization_pending":
			return nil, nil, interval, true
		case "slow_down":
			return nil, nil, interval + a.minPollInterval, true
		case "expired_token":
			return nil, errors.New("xai device code expired"), interval, false
		case "access_denied":
			return nil, errors.New("xai device authorization denied"), interval, false
		default:
			desc := strings.TrimSpace(payload.ErrorDescription)
			if desc != "" {
				return nil, fmt.Errorf("xai device token error: %s: %s", payload.Error, desc), interval, false
			}
			return nil, fmt.Errorf("xai device token error: %s", payload.Error), interval, false
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai device token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))), interval, false
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, errors.New("xai device token response missing access_token"), interval, false
	}
	email, sub := parseJWTIdentity(payload.IDToken)
	return buildTokenResult(payload.AccessToken, payload.RefreshToken, payload.IDToken, payload.TokenType, payload.ExpiresIn, email, sub), nil, interval, false
}

// RefreshTokens 用 refresh_token 刷新 xAI access token(无 client_secret)。
func (a *XAIAuth) RefreshTokens(ctx context.Context, refreshToken, tokenEndpoint string) (*XAITokenResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("xai token refresh: refresh token is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tokenEndpoint = strings.TrimSpace(tokenEndpoint)
	if tokenEndpoint == "" {
		tokenEndpoint = xaiDefaultTokenEP
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {xaiClientID},
		"refresh_token": {strings.TrimSpace(refreshToken)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("xai token request: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xai token response: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("xai token response: parse body: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, errors.New("xai token response missing access_token")
	}
	email, sub := parseJWTIdentity(payload.IDToken)
	res := buildTokenResult(payload.AccessToken, payload.RefreshToken, payload.IDToken, payload.TokenType, payload.ExpiresIn, email, sub)
	res.TokenEndpoint = tokenEndpoint
	return res, nil
}

// buildTokenResult 组装 XAITokenResult,并按 expires_in 推导过期时间。
func buildTokenResult(accessToken, refreshToken, idToken, tokenType string, expiresIn int, email, sub string) *XAITokenResult {
	res := &XAITokenResult{
		AccessToken:  strings.TrimSpace(accessToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		IDToken:      strings.TrimSpace(idToken),
		TokenType:    strings.TrimSpace(tokenType),
		ExpiresIn:    expiresIn,
		Email:        email,
		Subject:      sub,
	}
	if expiresIn > 0 {
		res.Expire = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return res
}

// parseJWTIdentity 从 id_token JWT 的中段 payload 解析 email 与 sub。
func parseJWTIdentity(token string) (email string, subject string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload := parts[1]
	payload += strings.Repeat("=", (4-len(payload)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return "", ""
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", ""
	}
	if v, ok := claims["email"].(string); ok {
		email = strings.TrimSpace(v)
	}
	if v, ok := claims["sub"].(string); ok {
		subject = strings.TrimSpace(v)
	}
	return email, subject
}