package quota

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type UserInfo struct {
	Email string `json:"email"`
}

type activeLogin struct {
	cancel context.CancelFunc
}

type AuthManager struct {
	sync.Mutex
	refreshPromises map[string]*refreshPromise // accountId -> active refresh
	accountMgr      *account.Manager
	activeLogin     *activeLogin
	// xaiLoginStates 保存进行中的 xAI 设备码登录状态(state -> result),供前端轮询。
	xaiLoginStates map[string]*xaiLoginState
}

type refreshPromise struct {
	wg    sync.WaitGroup
	token string
	err   error
}

func NewAuthManager(accountMgr *account.Manager) *AuthManager {
	return &AuthManager{
		refreshPromises: make(map[string]*refreshPromise),
		accountMgr:      accountMgr,
		xaiLoginStates:  make(map[string]*xaiLoginState),
	}
}

// Helper: reverse string decode
func decodeSecret(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func getCredentials(provider string) (string, string) {
	if provider == "antigravity" {
		return decodeSecret("moc.tnetnocresuelgoog.sppa.pe304g4hjolotv532ercl12h2nisshmt-1950606001701"),
			decodeSecret("fADq6z4CXs8BLm1JLdL684RWF85K-XPSCOG")
	} else if provider == "project" {
		// Project provider client credentials
		return decodeSecret("moc.tnetnocresuelgoog.sppa.hlb5c862doc6vo23caiugt3bjj1crt63-250919453488"),
			decodeSecret("XstZ0RwMKxY-jdTQ0CDWR7FpWQY9-XPSCOG")
	} else if provider == "grok" {
		// xAI (Grok) OAuth 设备码流:公开 client_id,无 client_secret。
		return xaiClientID, ""
	}
	// Default: gemini-cli
	return decodeSecret("moc.tnetnocresuelgoog.sppa.j531bidmh3va6fqa3e9pnrdrpo2tf8oo-593908552186"),
		decodeSecret("lxsFXlc5uC6Veg-kS7o1-mPMgHu4-XPSCOG")
}

func (am *AuthManager) RefreshToken(acc *account.Account) (string, error) {
	am.Lock()
	accountId := acc.ID
	if accountId == "" {
		accountId = acc.Email
	}

	// SingleFlight merging of concurrent refreshes for the same account
	if promise, exists := am.refreshPromises[accountId]; exists {
		am.Unlock()
		promise.wg.Wait()
		return promise.token, promise.err
	}

	promise := &refreshPromise{}
	promise.wg.Add(1)
	am.refreshPromises[accountId] = promise
	am.Unlock()

	defer func() {
		am.Lock()
		delete(am.refreshPromises, accountId)
		am.Unlock()
		promise.wg.Done()
	}()

	clientID, clientSecret := getCredentials(acc.Provider)

	// WorkBuddy 账号使用独立 Keycloak 授权体系，不支持 Google OAuth 刷新流，防御性拦截
	if acc.Provider == "workbuddy" {
		err := errors.New("workbuddy does not support Google OAuth refresh flow")
		promise.err = err
		return "", err
	}

	// xAI (Grok) OAuth 刷新:走 xai 设备码流 RefreshTokens(无 client_secret),
	// token_endpoint 取账号自带字段,回退默认 auth.x.ai 端点;其余 provider 走原 Google 路径。
	if acc.Provider == "grok" {
		tokenData, refErr := am.refreshXaiToken(acc)
		if refErr != nil {
			promise.err = refErr
			return "", refErr
		}
		promise.token = tokenData.AccessToken
		if am.accountMgr != nil {
			am.accountMgr.UpdateAccessToken(acc.ID, promise.token)
			// xai 刷新可能轮换 refresh_token,回写避免下次用旧 token 刷新失败。
			if strings.TrimSpace(tokenData.RefreshToken) != "" && strings.TrimSpace(tokenData.RefreshToken) != strings.TrimSpace(acc.RefreshToken) {
				am.accountMgr.UpdateAccountRefreshToken(acc.ID, tokenData.RefreshToken)
			}
		}
		return promise.token, nil
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", acc.RefreshToken)

	client := netutil.NewClient(15 * time.Second)
	resp, err := client.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		promise.err = err
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		promise.err = err
		return "", err
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		promise.err = err
		return "", err
	}

	if resp.StatusCode != 200 || tokenResp.AccessToken == "" {
		errMsg := tokenResp.ErrorDescription
		if errMsg == "" {
			errMsg = tokenResp.Error
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		promise.err = errors.New(errMsg)

		// Check if permanent refresh failure (e.g. invalid grant), and disable account automatically.
		//
		// 关键词判定收紧:invalid_client/unauthorized_client 才是永久失败;invalid_grant
		// 宽松对待(可能只是临时风控,且下文 xAI 路径已转为「连续 2 次」判定)。
		// invalid_request/bad request 这类瞬时错误历史曾误停用整批账号,故此处不再触发停用。
		// 仅 Google 分支保留「单次永久失败即停用」语义——Google 端 invalid_grant 确实是 refresh_token
		// 真失效,而本监控 5 分钟一 tick 触发频率低、单次可信度高。
		// xAI(Grok)分支见 refreshXaiToken:走连续 2 次累计判定,不再走 isPermanent。
		errLower := strings.ToLower(errMsg)
		isPermanent := strings.Contains(errLower, "invalid_grant") ||
			strings.Contains(errLower, "invalid client") ||
			strings.Contains(errLower, "unauthorized_client")

		if isPermanent && am.accountMgr != nil {
			fmt.Printf("[AuthManager] Permanent token refresh failure for %s, disabling account.\n", acc.Email)
			am.accountMgr.UpdateAccountEnabled(acc.ID, false)
		}

		return "", promise.err
	}

	promise.token = tokenResp.AccessToken
	if am.accountMgr != nil {
		am.accountMgr.UpdateAccessToken(acc.ID, promise.token)
	}
	return promise.token, nil
}

func (am *AuthManager) GetUserEmail(accessToken, provider string) (string, error) {
	req, err := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if provider == "antigravity" {
		req.Header.Set("User-Agent", "Code-Assist/1.22.4 (JetBrains; Windows 11 10.0; x86_64) cloudaicompanion/1.22.4")
	}

	client := netutil.NewClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var info UserInfo
	if err := json.Unmarshal(bodyBytes, &info); err != nil {
		return "", err
	}

	if info.Email == "" {
		return "Unknown", nil
	}
	return info.Email, nil
}

type ManualOAuthResult struct {
	URL          string `json:"url"`
	CodeVerifier string `json:"code_verifier"`
}

func (am *AuthManager) GenerateManualOAuthURL() ManualOAuthResult {
	randGen := rand.New(rand.NewSource(time.Now().UnixNano()))
	verifierBytes := make([]byte, 32)
	randGen.Read(verifierBytes)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	hasher := sha256.New()
	hasher.Write([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hasher.Sum(nil))

	stateBytes := make([]byte, 16)
	randGen.Read(stateBytes)
	state := fmt.Sprintf("%x", stateBytes)

	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
		"openid",
	}

	officialClientID := decodeSecret("moc.tnetnocresuelgoog.sppa.hlb5c862doc6vo23caiugt3bjj1crt63-250919453488")
	officialRedirectURI := "https://antigravity.google/oauth-callback"

	scopeParam := strings.Join(scopes, " ")
	authUrl := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?access_type=offline&client_id=%s&code_challenge=%s&code_challenge_method=S256&prompt=consent&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		officialClientID, challenge, url.QueryEscape(officialRedirectURI), url.QueryEscape(scopeParam), state)

	return ManualOAuthResult{
		URL:          authUrl,
		CodeVerifier: verifier,
	}
}

func (am *AuthManager) ExchangeCodeForTokenManual(code, verifier string) (*TokenResponse, error) {
	officialClientID := decodeSecret("moc.tnetnocresuelgoog.sppa.hlb5c862doc6vo23caiugt3bjj1crt63-250919453488")
	officialClientSecret := decodeSecret("XstZ0RwMKxY-jdTQ0CDWR7FpWQY9-XPSCOG")
	officialRedirectURI := "https://antigravity.google/oauth-callback"

	form := url.Values{}
	form.Set("client_id", officialClientID)
	form.Set("client_secret", officialClientSecret)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", officialRedirectURI)

	client := netutil.NewClient(15 * time.Second)
	resp, err := client.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 || tokenResp.AccessToken == "" {
		errMsg := tokenResp.ErrorDescription
		if errMsg == "" {
			errMsg = tokenResp.Error
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP Status %d", resp.StatusCode)
		}
		return nil, errors.New(errMsg)
	}

	return &tokenResp, nil
}

func (am *AuthManager) StartLogin(provider string, openBrowser func(string)) (map[string]interface{}, error) {
	am.Lock()
	if am.activeLogin != nil {
		am.activeLogin.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	currentActive := &activeLogin{cancel: cancel}
	am.activeLogin = currentActive
	am.Unlock()

	defer func() {
		am.Lock()
		if am.activeLogin == currentActive {
			am.activeLogin = nil
		}
		am.Unlock()
		cancel()
	}()

	var port = 0
	if provider == "antigravity" {
		port = 38121
	}

	// Bind temporary local listener
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("无法绑定 OAuth 回调接口: %v", err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	redirectUri := fmt.Sprintf("http://127.0.0.1:%d/", actualPort)
	if provider == "antigravity" {
		redirectUri = fmt.Sprintf("http://127.0.0.1:%d/oauth-callback", actualPort)
	}

	clientID, clientSecret := getCredentials(provider)

	scopes := []string{
		"openid",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cloud-platform",
	}
	if provider == "antigravity" {
		scopes = append(scopes, "https://www.googleapis.com/auth/cclog", "https://www.googleapis.com/auth/experimentsandconfigs")
	}

	scopeParam := strings.Join(scopes, " ")
	authUrl := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&response_type=code&scope=%s&redirect_uri=%s&access_type=offline&prompt=consent",
		clientID, url.QueryEscape(scopeParam), url.QueryEscape(redirectUri))

	openBrowser(authUrl)

	type loginResult struct {
		email        string
		accessToken  string
		refreshToken string
		err          error
	}

	resultChan := make(chan loginResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("<html><body><h2>登录失败：未收到授权码。</h2></body></html>"))
			resultChan <- loginResult{err: errors.New("no auth code received")}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body><h2>登录成功！您可以关闭此页面并返回 Antigravity Proxy。</h2><script>window.close()</script></body></html>"))

		// Exchange code
		form := url.Values{}
		form.Set("client_id", clientID)
		form.Set("client_secret", clientSecret)
		form.Set("code", code)
		form.Set("grant_type", "authorization_code")
		form.Set("redirect_uri", redirectUri)

		client := netutil.NewClient(15 * time.Second)
		resp, err := client.PostForm("https://oauth2.googleapis.com/token", form)
		if err != nil {
			resultChan <- loginResult{err: err}
			return
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			resultChan <- loginResult{err: err}
			return
		}

		var tokenResp TokenResponse
		if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
			resultChan <- loginResult{err: err}
			return
		}

		if resp.StatusCode != 200 || tokenResp.AccessToken == "" {
			errMsg := tokenResp.ErrorDescription
			if errMsg == "" {
				errMsg = tokenResp.Error
			}
			resultChan <- loginResult{err: fmt.Errorf("OAuth exchange failed: %s", errMsg)}
			return
		}

		email, err := am.GetUserEmail(tokenResp.AccessToken, provider)
		if err != nil {
			email = "Unknown"
		}

		resultChan <- loginResult{
			email:        email,
			accessToken:  tokenResp.AccessToken,
			refreshToken: tokenResp.RefreshToken,
		}
	})

	server := &http.Server{
		Handler: mux,
	}

	go server.Serve(listener)

	// Wait with a 5 minutes timeout or context cancellation
	var loginRes loginResult
	select {
	case loginRes = <-resultChan:
	case <-ctx.Done():
		loginRes = loginResult{err: errors.New("登录已取消")}
	case <-time.After(5 * time.Minute):
		loginRes = loginResult{err: errors.New("登录超时（5分钟）")}
	}

	// Clean up server
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	server.Shutdown(ctxShutdown)
	listener.Close()

	if loginRes.err != nil {
		return nil, loginRes.err
	}

	return map[string]interface{}{
		"email":         loginRes.email,
		"access_token":  loginRes.accessToken,
		"refresh_token": loginRes.refreshToken,
		"provider":      provider,
	}, nil
}

func (am *AuthManager) CancelLogin() {
	am.Lock()
	defer am.Unlock()
	if am.activeLogin != nil {
		am.activeLogin.cancel()
		am.activeLogin = nil
	}
	// xai 设备码轮询的 ctx 复用 activeLogin,取消后 PollForToken 会退出;
	// 同时清空状态条目,避免前端轮询到残留的旧会话。
	for k := range am.xaiLoginStates {
		delete(am.xaiLoginStates, k)
	}
}

// ============ xAI (Grok) OAuth 设备码登录 ============

// xaiLoginState 是进行中 xAI 设备码登录的状态条目。
type xaiLoginState struct {
	status  string // "pending" | "success" | "error"
	error   string
	data    *xaiLoginData
	cancel  context.CancelFunc
}

// xaiLoginData 是登录成功后的凭证数据,供 IPC 落库。
type xaiLoginData struct {
	Email         string
	AccessToken   string
	RefreshToken  string
	BaseURL       string
	TokenEndpoint string
}

// refreshXaiToken 用 xAI 设备码流刷新 access token(grok provider 专用)。
//
// 永久失败判定与处置:命中 invalid_grant/unauthorized_client/invalid_client/
// access_denied 等凭证作废类永久失败时,自动将账号标记为停用(Enabled=false),
// 绝不直接 RemoveAccount 物理删除,保留账号卡片及所有凭证,防止网络或上游抖动导致账号丢失。
//
// 抖动防护:
//   - RefreshToken 入口的 refreshPromises(SingleFlight)已合并同账号并发刷新,同一 refresh_token
//     不会被并发重复打到 auth.x.ai,从根上消除「并发刷同一 token 临时返 invalid_grant」的抖动源;
//   - 定时/手动触发链路(CheckAndPurgeGrokAuth、RefreshAccountTokenSync)均遵守「只刷临近/已过期的」
//     口径(JWT exp 距当前 <= grokAuthRefreshSkewSec 才刷),不主动刷仍有效的 token,进一步压低抖动概率;
//   - invalid_request/bad request 这类瞬时协议/风控错误不视作永久失败,不停用(仅返回 err 让上层重试)。
func (am *AuthManager) refreshXaiToken(acc *account.Account) (*XAITokenResult, error) {
	if acc == nil {
		return nil, errors.New("xai token refresh: account is nil")
	}
	tokenEndpoint := strings.TrimSpace(acc.TokenEndpoint)
	if tokenEndpoint == "" {
		tokenEndpoint = xaiDefaultTokenEP
	}
	auth := NewXAIAuth()
	res, err := auth.RefreshTokens(context.Background(), acc.RefreshToken, tokenEndpoint)
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		// 关键词判定:命中凭证作废类永久失败时自动停用,不停留物理删除风险。
		isPermanent := strings.Contains(errMsg, "invalid_grant") ||
			strings.Contains(errMsg, "unauthorized_client") ||
			strings.Contains(errMsg, "invalid_client") ||
			strings.Contains(errMsg, "access_denied")

		if isPermanent && am.accountMgr != nil {
			// 永久失败自动停用账号(对齐 Google 官方账号策略,保留账号卡片供用户重新授权)。
			fmt.Printf("[AuthManager] Permanent xai token refresh failure for %s (id=%s), disabling account: %s\n",
				acc.Email, acc.ID, errMsg)
			am.accountMgr.UpdateAccountEnabled(acc.ID, false)
			if cb := am.accountMgr.OnAccountsUpdated; cb != nil {
				go cb(am.accountMgr.GetRawAccounts())
			}
		} else if am.accountMgr != nil {
			// 非永久失败(网络抖动/5xx/超时/invalid_request 瞬时):不停用,仅告警,留给下一个 tick 重试。
			fmt.Printf("[AuthManager] Transient xai token refresh failure for %s (id=%s), will retry next tick: %s\n",
				acc.Email, acc.ID, errMsg)
		}
		return nil, err
	}

	return res, nil
}

// StartXaiLogin 启动 xAI 设备码授权登录:请求设备码、打开浏览器、后台轮询,立即返回状态句柄。
// 复用 activeLogin 单槽取消基建:发起新登录前先取消旧登录。
func (am *AuthManager) StartXaiLogin(openBrowser func(string)) (map[string]interface{}, error) {
	am.Lock()
	if am.activeLogin != nil {
		am.activeLogin.cancel()
	}
	// 清空历史的 xai 登录状态,避免脏 state 残留。
	for k := range am.xaiLoginStates {
		delete(am.xaiLoginStates, k)
	}
	ctx, cancel := context.WithCancel(context.Background())
	currentActive := &activeLogin{cancel: cancel}
	am.activeLogin = currentActive
	am.Unlock()

	xaiauth := NewXAIAuth()
	dc, _, err := xaiauth.StartDeviceFlow(ctx)
	if err != nil {
		am.Lock()
		if am.activeLogin == currentActive {
			am.activeLogin = nil
		}
		am.Unlock()
		cancel()
		return nil, fmt.Errorf("启动 xAI 授权失败: %w", err)
	}

	state := fmt.Sprintf("xai-%d", time.Now().UnixNano())

	am.Lock()
	am.xaiLoginStates[state] = &xaiLoginState{
		status: "pending",
		cancel: cancel,
	}
	am.Unlock()

	// 授权地址(优先 verification_uri_complete,自动带 user_code),下发给前端展示与复制。
	// 不再在此自动调起系统浏览器:默认浏览器会复用已登录 xAI 的会话/Cookie,
	// 导致直接授权成"当前账号"而非用户期望的新账号。
	// 改由前端"复制授权链接"按钮,用户自行粘贴到无痕窗口/目标账号浏览器完成授权。
	authURL := strings.TrimSpace(dc.VerificationURIComplete)
	if authURL == "" {
		authURL = strings.TrimSpace(dc.VerificationURI)
	}

	// 后台轮询 token_endpoint,结果写入状态条目。
	// 防旧登录覆盖:新登录开始时会清空 xaiLoginStates 并 cancel 旧登录的 ctx,
	// 旧 goroutine 退出时按自己的 state 查 map 得 entry==nil,自然跳过写入。
	go func() {
		res, pollErr := xaiauth.PollForToken(ctx, dc)
		am.Lock()
		entry := am.xaiLoginStates[state]
		if pollErr == nil && res != nil {
			if entry != nil {
				entry.status = "success"
				entry.data = &xaiLoginData{
					Email:         res.Email,
					AccessToken:   res.AccessToken,
					RefreshToken:  res.RefreshToken,
					BaseURL:       xaiCliProxyBaseURL,
					TokenEndpoint: res.TokenEndpoint,
				}
			}
		} else {
			if entry != nil && entry.status == "pending" {
				entry.status = "error"
				if pollErr != nil {
					entry.error = pollErr.Error()
				} else {
					entry.error = "授权失败"
				}
			}
		}
		am.Unlock()
	}()

	return map[string]interface{}{
		"state":           state,
		"user_code":         dc.UserCode,
		"verification_url": authURL,
		"expires_in":       dc.ExpiresIn,
	}, nil
}

// GetXaiLoginStatus 查询进行中 xAI 登录的状态(供前端轮询)。
func (am *AuthManager) GetXaiLoginStatus(state string) (map[string]interface{}, error) {
	am.Lock()
	defer am.Unlock()
	entry, ok := am.xaiLoginStates[state]
	if !ok {
		return nil, errors.New("未知或已过期的授权会话")
	}
	switch entry.status {
	case "pending":
		return map[string]interface{}{"status": "pending"}, nil
	case "success":
		if entry.data == nil {
			return nil, errors.New("授权成功但缺少凭证数据")
		}
		return map[string]interface{}{
			"status":         "success",
			"email":          entry.data.Email,
			"access_token":   entry.data.AccessToken,
			"refresh_token":  entry.data.RefreshToken,
			"base_url":       entry.data.BaseURL,
			"token_endpoint": entry.data.TokenEndpoint,
		}, nil
	case "error":
		return map[string]interface{}{"status": "error", "error": entry.error}, nil
	default:
		return map[string]interface{}{"status": "pending"}, nil
	}
}
