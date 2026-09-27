package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// account_workbuddy_oauth.go: WorkBuddy 官方网页授权登录状态机与轮询调度。
//
// 官方协议（国内版与国际版通用）：
// 1. POST {Endpoint}/v2/plugin/auth/state?platform=workbuddy-ai
//    获取 state (UUID) 及 authUrl。
//    - 国内版 Endpoint: https://copilot.tencent.com (支持微信扫码、手机号验证码、腾讯云账号登录)
//    - 国际版 Endpoint: https://www.workbuddy.ai (支持 Google / GitHub / X 登录)
// 2. 拼接 &version={version} 后在系统默认浏览器中打开 authUrl。
// 3. 后台 1s 轮询 GET {Endpoint}/v2/plugin/auth/token?state={state}：
//    - 未完成返回 code: 11217 (11217:login ing...)，保持等待。
//    - 成功返回 code: 0 及 Token (accessToken, refreshToken, expiresIn)。
// 4. 成功后调用 GET {Endpoint}/v2/plugin/login/account?state={state}
//    携带 Bearer Token 获取当前用户信息 (uid, nickname)。
// 5. 自动构造并入库至 Manager 号池中。

const (
	DefaultWorkBuddyAuthURL         = "https://www.workbuddy.ai"
	DefaultWorkBuddyDomesticAuthURL = "https://copilot.tencent.com"
	DefaultWorkBuddyAuthVersion     = "5.5.2"
	DefaultWorkBuddyDomesticVersion = "5.5.6"
	WorkBuddyRetryCode              = 11217
	DefaultWorkBuddyTimeout         = 300 * time.Second
	DefaultWorkBuddyPollPeriod      = 1 * time.Second
)

// WorkBuddyOAuthSession 代表一个活跃的官方网页登录会话。
type WorkBuddyOAuthSession struct {
	State        string             `json:"state"`
	AuthURL      string             `json:"authUrl"`
	BrowserURL   string             `json:"browserUrl"`
	Status       string             `json:"status"` // "pending", "success", "failed", "timeout", "canceled"
	ErrorMessage string             `json:"errorMessage,omitempty"`
	CreatedAt    time.Time          `json:"createdAt"`
	Account      *Account           `json:"account,omitempty"`
	Edition      string             `json:"edition,omitempty"` // "domestic" 或 "international"
	CancelFunc   context.CancelFunc `json:"-"`
}

// WorkBuddyOAuthManager 统一管理所有登录会话。
type WorkBuddyOAuthManager struct {
	mu                  sync.RWMutex
	sessions            map[string]*WorkBuddyOAuthSession
	accountMgr          *Manager
	client              *http.Client
	authBaseURL         string
	domesticAuthBaseURL string
	pollInterval        time.Duration
	timeout             time.Duration
	onSuccess           func(acc *Account)
}

// NewWorkBuddyOAuthManager 实例化 WorkBuddyOAuthManager。
func NewWorkBuddyOAuthManager(mgr *Manager, opts ...func(*WorkBuddyOAuthManager)) *WorkBuddyOAuthManager {
	m := &WorkBuddyOAuthManager{
		sessions:            make(map[string]*WorkBuddyOAuthSession),
		accountMgr:          mgr,
		client:              &http.Client{Timeout: 10 * time.Second},
		authBaseURL:         DefaultWorkBuddyAuthURL,
		domesticAuthBaseURL: DefaultWorkBuddyDomesticAuthURL,
		pollInterval:        DefaultWorkBuddyPollPeriod,
		timeout:             DefaultWorkBuddyTimeout,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// SetOnSuccess 注册账号登录成功后的回调钩子。
func (m *WorkBuddyOAuthManager) SetOnSuccess(fn func(acc *Account)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSuccess = fn
}

// SetAuthBaseURL 设置认证基础 URL（主要用于单元测试与环境隔离）。
func (m *WorkBuddyOAuthManager) SetAuthBaseURL(urlStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authBaseURL = strings.TrimRight(urlStr, "/")
	if m.domesticAuthBaseURL == "" || m.domesticAuthBaseURL == DefaultWorkBuddyDomesticAuthURL {
		m.domesticAuthBaseURL = m.authBaseURL
	}
}

// SetDomesticAuthBaseURL 单独设置国内版认证基础 URL。
func (m *WorkBuddyOAuthManager) SetDomesticAuthBaseURL(urlStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.domesticAuthBaseURL = strings.TrimRight(urlStr, "/")
}

// SetPollInterval 设置轮询频率（主要用于单测加速）。
func (m *WorkBuddyOAuthManager) SetPollInterval(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollInterval = d
}

// SetTimeout 设置登录总超时时间。
func (m *WorkBuddyOAuthManager) SetTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout = d
}

type authStateResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	RequestId string `json:"requestId"`
	Data      struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	} `json:"data"`
}

type authTokenResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	RequestId string `json:"requestId"`
	Data      struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresIn        int64  `json:"expiresIn"`
		RefreshExpiresIn int64  `json:"refreshExpiresIn"`
		TokenType        string `json:"tokenType"`
	} `json:"data"`
}

type authAccountResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	RequestId string `json:"requestId"`
	Data      struct {
		UID       string `json:"uid"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatarUrl"`
		Email     string `json:"email"`
	} `json:"data"`
}

// StartLogin 发起新的 WorkBuddy 国际版网页登录（保持历史签名向后兼容）。
func (m *WorkBuddyOAuthManager) StartLogin(parentCtx context.Context, version string) (*WorkBuddyOAuthSession, error) {
	edition := "international"
	if strings.EqualFold(version, "domestic") || strings.EqualFold(version, "cn") {
		edition = "domestic"
		version = ""
	}
	return m.StartLoginWithEdition(parentCtx, edition, version)
}

// StartLoginWithEdition 发起指定版本（domestic 国内版 / international 国际版）的 WorkBuddy 官方网页登录。
func (m *WorkBuddyOAuthManager) StartLoginWithEdition(parentCtx context.Context, edition, version string) (*WorkBuddyOAuthSession, error) {
	edition = strings.ToLower(strings.TrimSpace(edition))
	isDomestic := edition == "domestic" || edition == "cn" || edition == "tencent" || edition == "zh"
	normEdition := "international"
	if isDomestic {
		normEdition = "domestic"
	}

	m.mu.Lock()
	// 取消之前仍在 pending 状态的所有历史会话，避免重复轮询
	for _, old := range m.sessions {
		if old.Status == "pending" && old.CancelFunc != nil {
			old.CancelFunc()
			old.Status = "canceled"
		}
	}

	var baseURL string
	if isDomestic {
		if m.domesticAuthBaseURL != "" {
			baseURL = m.domesticAuthBaseURL
		} else {
			baseURL = DefaultWorkBuddyDomesticAuthURL
		}
	} else {
		if m.authBaseURL != "" {
			baseURL = m.authBaseURL
		} else {
			baseURL = DefaultWorkBuddyAuthURL
		}
	}

	timeoutDur := m.timeout
	pollDur := m.pollInterval
	m.mu.Unlock()

	if version == "" {
		if isDomestic {
			version = DefaultWorkBuddyDomesticVersion
		} else {
			version = DefaultWorkBuddyAuthVersion
		}
	}

	stateURL := fmt.Sprintf("%s/v2/plugin/auth/state?platform=workbuddy-ai", baseURL)
	req, err := http.NewRequestWithContext(parentCtx, http.MethodPost, stateURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("创建 state 请求失败: %w", err)
	}

	m.applyOfficialHeaders(req, version, isDomestic)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 WorkBuddy auth state 失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 auth state 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth state 响应异常 (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var stateResp authStateResponse
	if err := json.Unmarshal(bodyBytes, &stateResp); err != nil {
		return nil, fmt.Errorf("解析 auth state JSON 失败: %w", err)
	}

	if stateResp.Code != 0 || stateResp.Data.State == "" || stateResp.Data.AuthURL == "" {
		return nil, fmt.Errorf("获取 auth state 错误 (code=%d): %s", stateResp.Code, stateResp.Msg)
	}

	// 拼接版本号至 Browser 打开 URL
	browserURL := stateResp.Data.AuthURL
	u, err := url.Parse(browserURL)
	if err == nil {
		q := u.Query()
		q.Set("version", version)
		u.RawQuery = q.Encode()
		browserURL = u.String()
	} else {
		if strings.Contains(browserURL, "?") {
			browserURL += "&version=" + url.QueryEscape(version)
		} else {
			browserURL += "?version=" + url.QueryEscape(version)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)

	session := &WorkBuddyOAuthSession{
		State:      stateResp.Data.State,
		AuthURL:    stateResp.Data.AuthURL,
		BrowserURL: browserURL,
		Status:     "pending",
		Edition:    normEdition,
		CreatedAt:  time.Now(),
		CancelFunc: cancel,
	}

	m.mu.Lock()
	m.sessions[session.State] = session
	m.mu.Unlock()

	// 启动后台轮询 Goroutine
	go m.pollLoop(ctx, session, baseURL, version, isDomestic, pollDur)

	return session, nil
}

// CancelLogin 主动取消指定 State 的登录流程。
func (m *WorkBuddyOAuthManager) CancelLogin(state string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[state]
	if !exists {
		return false
	}
	if sess.CancelFunc != nil {
		sess.CancelFunc()
	}
	sess.Status = "canceled"
	return true
}

// GetSession 获取指定 State 的会话当前快照。
func (m *WorkBuddyOAuthManager) GetSession(state string) *WorkBuddyOAuthSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[state]
}

// applyOfficialHeaders 附加 WorkBuddy 官方客户端协议头。
func (m *WorkBuddyOAuthManager) applyOfficialHeaders(req *http.Request, version string, isDomestic bool) {
	req.Header.Set("User-Agent", "WorkBuddy/"+version)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-No-Authorization", "true")
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	if isDomestic {
		req.Header.Set("X-Domain", "www.workbuddy.cn")
		req.Header.Set("X-IDE-Type", "WorkBuddy")
	}
}

// pollLoop 轮询 Token 与用户信息，并在成功后入库号池。
func (m *WorkBuddyOAuthManager) pollLoop(ctx context.Context, sess *WorkBuddyOAuthSession, baseURL, version string, isDomestic bool, pollInterval time.Duration) {
	tokenURL := fmt.Sprintf("%s/v2/plugin/auth/token?state=%s", baseURL, url.QueryEscape(sess.State))
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			if sess.Status == "pending" {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					sess.Status = "timeout"
					sess.ErrorMessage = "登录超时，请重试"
				} else {
					sess.Status = "canceled"
					sess.ErrorMessage = "登录已取消"
				}
			}
			m.mu.Unlock()
			return

		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
			if err != nil {
				continue
			}
			m.applyOfficialHeaders(req, version, isDomestic)

			resp, err := m.client.Do(req)
			if err != nil {
				continue
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue
			}

			var tokenResp authTokenResponse
			if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
				continue
			}

			// 状态 11217: 登录中，继续轮询
			if tokenResp.Code == WorkBuddyRetryCode {
				continue
			}

			// 状态 0: 登录成功，拿到了 Token！
			if tokenResp.Code == 0 && tokenResp.Data.AccessToken != "" {
				accessToken := tokenResp.Data.AccessToken
				refreshToken := tokenResp.Data.RefreshToken

				// 尝试拉取用户信息
				uid, nickname := m.fetchAccountDetails(ctx, baseURL, version, sess.State, accessToken, isDomestic)
				if uid == "" || nickname == "" {
					jwtUID, jwtNick := extractUserFromJWT(accessToken)
					if uid == "" {
						uid = jwtUID
					}
					if nickname == "" {
						nickname = jwtNick
					}
				}

				accBaseURL := DefaultWorkBuddyBaseURL
				if isDomestic {
					accBaseURL = DefaultWorkBuddyDomesticBaseURL
				}

				label := nickname
				if label == "" {
					if uid != "" {
						label = "WB-" + uid
					} else {
						label = "WorkBuddy 账号"
					}
				}
				if isDomestic && !strings.Contains(label, "国内版") {
					label = label + " (国内版)"
				}

				in := WorkBuddyAccountInput{
					BaseURL:      accBaseURL,
					AccessToken:  accessToken,
					RefreshToken: refreshToken,
					UID:          uid,
					Nickname:     nickname,
					Label:        label,
					DefaultModel: DefaultWorkBuddyModel,
				}

				var savedAcc *Account
				if m.accountMgr != nil {
					// 查重：若已有同 UID 或同 Token，更新；否则新增
					m.accountMgr.RLock()
					var existingID string
					for _, a := range m.accountMgr.accounts {
						if a.Provider == workbuddyProvider {
							if (in.UID != "" && a.ProjectID == in.UID) || a.AccessToken == in.AccessToken {
								existingID = a.ID
								break
							}
						}
					}
					m.accountMgr.RUnlock()

					if existingID != "" {
						savedAcc, _ = m.accountMgr.UpdateWorkBuddyAccount(existingID, in)
					} else {
						newID, err := m.accountMgr.AddWorkBuddyAccount(in)
						if err == nil {
							savedAcc = m.accountMgr.GetAccountByID(newID)
						}
					}
				} else {
					savedAcc = NewWorkBuddyAccount(in)
				}

				m.mu.Lock()
				sess.Status = "success"
				sess.Account = savedAcc
				callback := m.onSuccess
				m.mu.Unlock()

				if callback != nil && savedAcc != nil {
					callback(savedAcc)
				}
				return
			}

			// 非 0 且非 11217 则视为致命失败
			if tokenResp.Code != 0 {
				m.mu.Lock()
				sess.Status = "failed"
				sess.ErrorMessage = fmt.Sprintf("登录失败 (code=%d): %s", tokenResp.Code, tokenResp.Msg)
				m.mu.Unlock()
				return
			}
		}
	}
}

// fetchAccountDetails 携带 Bearer Token 获取用户的 UID 与昵称。
func (m *WorkBuddyOAuthManager) fetchAccountDetails(ctx context.Context, baseURL, version, state, accessToken string, isDomestic bool) (string, string) {
	accURL := fmt.Sprintf("%s/v2/plugin/login/account?state=%s", baseURL, url.QueryEscape(state))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, accURL, nil)
	if err != nil {
		return "", ""
	}

	req.Header.Set("User-Agent", "WorkBuddy/"+version)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	if isDomestic {
		req.Header.Set("X-Domain", "www.workbuddy.cn")
		req.Header.Set("X-IDE-Type", "WorkBuddy")
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", ""
	}

	var accResp authAccountResponse
	if err := json.Unmarshal(bodyBytes, &accResp); err != nil || accResp.Code != 0 {
		return "", ""
	}

	return strings.TrimSpace(accResp.Data.UID), strings.TrimSpace(accResp.Data.Nickname)
}

// extractUserFromJWT 尝试从 JWT Payload 中解析 uid 和 nickname/uin
func extractUserFromJWT(tokenStr string) (string, string) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payloadSegment := parts[1]
	if l := len(payloadSegment) % 4; l > 0 {
		payloadSegment += strings.Repeat("=", 4-l)
	}
	data, err := base64.URLEncoding.DecodeString(payloadSegment)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(payloadSegment)
		if err != nil {
			return "", ""
		}
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", ""
	}

	var uid, nickname string
	if v, ok := claims["uid"].(string); ok && v != "" {
		uid = v
	} else if v, ok := claims["uin"].(string); ok && v != "" {
		uid = v
	} else if v, ok := claims["sub"].(string); ok && v != "" {
		uid = v
	}
	if v, ok := claims["nickname"].(string); ok && v != "" {
		nickname = v
	} else if v, ok := claims["name"].(string); ok && v != "" {
		nickname = v
	} else if v, ok := claims["phone"].(string); ok && v != "" {
		nickname = v
	} else if v, ok := claims["email"].(string); ok && v != "" {
		nickname = v
	}
	return uid, nickname
}
