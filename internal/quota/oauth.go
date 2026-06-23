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

		// Check if permanent refresh failure (e.g. invalid grant), and disable account automatically
		errLower := strings.ToLower(errMsg)
		isPermanent := strings.Contains(errLower, "invalid_grant") ||
			strings.Contains(errLower, "invalid client") ||
			strings.Contains(errLower, "unauthorized_client") ||
			strings.Contains(errLower, "invalid_request") ||
			strings.Contains(errLower, "bad request")

		if isPermanent && am.accountMgr != nil {
			fmt.Printf("[AuthManager] Permanent token refresh failure for %s, disabling account.\n", acc.Email)
			am.accountMgr.UpdateAccountEnabled(acc.ID, false)
		}

		return "", promise.err
	}

	promise.token = tokenResp.AccessToken
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
}
