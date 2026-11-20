package quota

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/netutil"
)

const (
	antigravityOAuthAuthURL          = "https://accounts.google.com/o/oauth2/auth"
	defaultGoogleTokenURL            = "https://oauth2.googleapis.com/token"
	defaultGoogleUserInfoURL         = "https://www.googleapis.com/oauth2/v2/userinfo"
	defaultAntigravityAuthSuccessURL = "https://antigravity.google/auth-success?app=antigravity"
	antigravityCallbackPath          = "/auth/callback"
)

// antigravityOAuthScopes 与官方 Antigravity 客户端 v2.32.1 保持一致（不含 openid，包含 aicode）。
var antigravityOAuthScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
	"https://www.googleapis.com/auth/aicode",
}

// oauthEndpoints 允许在单元测试中注入本地 httptest 服务地址，零值时自动回退官方端点。
type oauthEndpoints struct {
	authURL        string
	tokenURL       string
	userInfoURL    string
	authSuccessURL string
}

type oauthLoginResult struct {
	email        string
	accessToken  string
	refreshToken string
	err          error
}

func (am *AuthManager) resolveOAuthEndpoints() oauthEndpoints {
	ep := oauthEndpoints{
		authURL:        antigravityOAuthAuthURL,
		tokenURL:       defaultGoogleTokenURL,
		userInfoURL:    defaultGoogleUserInfoURL,
		authSuccessURL: defaultAntigravityAuthSuccessURL,
	}
	if am == nil {
		return ep
	}
	if strings.TrimSpace(am.endpoints.authURL) != "" {
		ep.authURL = strings.TrimSpace(am.endpoints.authURL)
	}
	if strings.TrimSpace(am.endpoints.tokenURL) != "" {
		ep.tokenURL = strings.TrimSpace(am.endpoints.tokenURL)
	}
	if strings.TrimSpace(am.endpoints.userInfoURL) != "" {
		ep.userInfoURL = strings.TrimSpace(am.endpoints.userInfoURL)
	}
	if strings.TrimSpace(am.endpoints.authSuccessURL) != "" {
		ep.authSuccessURL = strings.TrimSpace(am.endpoints.authSuccessURL)
	}
	return ep
}

// generatePKCEPair 生成 RFC 7636 S256 PKCE code_verifier 与 code_challenge。
func generatePKCEPair() (verifier string, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// generateOAuthState 生成 CSPRNG 高熵随机 state 用于防 CSRF。
func generateOAuthState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// buildAntigravityAuthURL 构造与官方 golang.org/x/oauth2 AuthCodeURL 一致的参数与排序格式。
func buildAntigravityAuthURL(baseAuthURL, clientID, redirectURI, state, codeChallenge string) (string, error) {
	parsed, err := url.Parse(baseAuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid OAuth auth URL: %w", err)
	}
	q := parsed.Query()
	q.Set("access_type", "offline")
	q.Set("client_id", clientID)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "consent")
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(antigravityOAuthScopes, " "))
	q.Set("state", state)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// listenLoopbackDualStack 监听随机空闲端口的 127.0.0.1 与 [::1] 双栈回环地址。
// 若当前系统未启用 IPv6 回环，则自动降级为仅 IPv4 监听，保证稳定性。
func listenLoopbackDualStack() ([]net.Listener, int, error) {
	v4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("无法绑定 OAuth 回调接口: %w", err)
	}
	port := v4.Addr().(*net.TCPAddr).Port
	listeners := []net.Listener{v4}

	if v6, err6 := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port)); err6 == nil {
		listeners = append(listeners, v6)
	}
	return listeners, port, nil
}

func closeListeners(listeners []net.Listener) {
	for _, ln := range listeners {
		if ln != nil {
			_ = ln.Close()
		}
	}
}

func constantTimeStateMatch(expected, actual string) bool {
	if expected == "" || len(expected) != len(actual) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func (am *AuthManager) startAntigravityLogin(ctx context.Context, openBrowser func(string)) (map[string]interface{}, error) {
	listeners, actualPort, err := listenLoopbackDualStack()
	if err != nil {
		return nil, err
	}
	defer closeListeners(listeners)

	redirectURI := fmt.Sprintf("http://localhost:%d%s", actualPort, antigravityCallbackPath)
	clientID, clientSecret := getCredentials("antigravity")

	codeVerifier, codeChallenge, err := generatePKCEPair()
	if err != nil {
		return nil, err
	}
	expectedState, err := generateOAuthState()
	if err != nil {
		return nil, err
	}

	ep := am.resolveOAuthEndpoints()
	authURL, err := buildAntigravityAuthURL(ep.authURL, clientID, redirectURI, expectedState, codeChallenge)
	if err != nil {
		return nil, err
	}

	resultChan := make(chan oauthLoginResult, 1)
	var deliverOnce sync.Once

	mux := http.NewServeMux()
	mux.HandleFunc(antigravityCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if !constantTimeStateMatch(expectedState, query.Get("state")) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html><body><h2>登录校验失败：无效的 state 参数。</h2></body></html>"))
			return
		}

		if errCode := query.Get("error"); errCode != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<html><body><h2>授权失败：%s</h2></body></html>", html.EscapeString(errCode))
			deliverOnce.Do(func() {
				resultChan <- oauthLoginResult{err: fmt.Errorf("OAuth authorization failed: %s", errCode)}
			})
			return
		}

		code := query.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html><body><h2>登录失败：未收到授权码。</h2></body></html>"))
			deliverOnce.Do(func() {
				resultChan <- oauthLoginResult{err: errors.New("no auth code received")}
			})
			return
		}

		// 命中合法授权码后立即跳转官方成功页，与原生客户端行为保持一致
		http.Redirect(w, r, ep.authSuccessURL, http.StatusFound)

		deliverOnce.Do(func() {
			go func() {
				resultChan <- am.completeAntigravityCodeExchange(ctx, clientID, clientSecret, code, redirectURI, codeVerifier)
			}()
		})
	})

	// 显式拒绝 /favicon.ico 等非回调请求，避免浏览器并发请求触发竞态误报
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	server := &http.Server{Handler: mux}
	for _, ln := range listeners {
		go server.Serve(ln)
	}

	if openBrowser != nil {
		openBrowser(authURL)
	}

	var loginRes oauthLoginResult
	select {
	case loginRes = <-resultChan:
	case <-ctx.Done():
		loginRes = oauthLoginResult{err: errors.New("登录已取消")}
	case <-time.After(5 * time.Minute):
		loginRes = oauthLoginResult{err: errors.New("登录超时（5分钟）")}
	}

	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	_ = server.Shutdown(ctxShutdown)

	if loginRes.err != nil {
		return nil, loginRes.err
	}

	return map[string]interface{}{
		"email":         loginRes.email,
		"access_token":  loginRes.accessToken,
		"refresh_token": loginRes.refreshToken,
		"provider":      "antigravity",
	}, nil
}

func (am *AuthManager) completeAntigravityCodeExchange(ctx context.Context, clientID, clientSecret, code, redirectURI, codeVerifier string) oauthLoginResult {
	tempKey := "oauth-login:" + code
	defer netutil.CloseAccountTransport(tempKey)

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)

	tokenResp, err := am.doGoogleTokenRequest(ctx, form, tempKey, 15*time.Second)
	if err != nil {
		return oauthLoginResult{err: fmt.Errorf("OAuth exchange failed: %w", err)}
	}

	email, err := am.getUserEmailWithTransportKey(ctx, tokenResp.AccessToken, tempKey)
	if err != nil || email == "" {
		email = "Unknown"
	}

	return oauthLoginResult{
		email:        email,
		accessToken:  tokenResp.AccessToken,
		refreshToken: tokenResp.RefreshToken,
	}
}
