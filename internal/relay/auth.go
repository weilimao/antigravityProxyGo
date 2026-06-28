package relay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type RelaySession struct {
	Token     string
	UserID    string
	UserKey   string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AuthManager struct {
	sync.RWMutex
	sessions map[string]*RelaySession
	userMgr  *UserManager
}

func NewAuthManager(userMgr *UserManager) *AuthManager {
	return &AuthManager{
		sessions: make(map[string]*RelaySession),
		userMgr:  userMgr,
	}
}

func (a *AuthManager) Login(key, password string) (*RelaySession, error) {
	user, err := a.userMgr.ValidateCredentials(key, password)
	if err != nil {
		return nil, err
	}

	token := generateToken()
	now := time.Now()
	session := &RelaySession{
		Token:     token,
		UserID:    user.ID,
		UserKey:   user.Key,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}

	a.Lock()
	a.sessions[token] = session
	a.Unlock()

	return session, nil
}

func (a *AuthManager) ValidateToken(token string) (*RelaySession, error) {
	a.RLock()
	session, exists := a.sessions[token]
	a.RUnlock()

	if !exists {
		// 虚拟 Key 防呆放行机制：如果客户端传过来的是标准的官方 Key 格式 (以 "sk-ant-" 或 "sk-" 开头)
		// 说明客户端（如 Claude Code / ccswitch）透传或硬编码了官方格式的密钥，我们在中继层予以直接放行，并将其动态绑定到系统内首个有效真实用户名下进行计费和会话路由
		if strings.HasPrefix(token, "sk-ant-") || strings.HasPrefix(token, "sk-") {
			var targetUserID string
			a.userMgr.RLock()
			for _, u := range a.userMgr.users {
				if u.Enabled {
					targetUserID = u.ID
					break
				}
			}
			a.userMgr.RUnlock()

			if targetUserID == "" {
				return nil, fmt.Errorf("no active user available in system for virtual key bypass")
			}

			return &RelaySession{
				UserID:    targetUserID,
				UserKey:   "virtual_client",
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
			}, nil
		}
		return nil, fmt.Errorf("invalid token")
	}
	if time.Now().After(session.ExpiresAt) {
		a.Lock()
		delete(a.sessions, token)
		a.Unlock()
		return nil, fmt.Errorf("token expired")
	}

	user := a.userMgr.GetUserByID(session.UserID)
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	if !user.Enabled {
		return nil, fmt.Errorf("user %q is disabled", user.Key)
	}

	return session, nil
}

func (a *AuthManager) ValidateProxyAuth(r *http.Request) (string, error) {
	header := r.Header.Get("Proxy-Authorization")
	if header == "" {
		return "", fmt.Errorf("missing Proxy-Authorization header")
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("invalid Proxy-Authorization format")
	}

	token := strings.TrimSpace(parts[1])
	session, err := a.ValidateToken(token)
	if err != nil {
		return "", err
	}
	return session.UserID, nil
}

func (a *AuthManager) Logout(token string) {
	a.Lock()
	delete(a.sessions, token)
	a.Unlock()
}

func (a *AuthManager) CleanExpired() {
	now := time.Now()
	a.Lock()
	defer a.Unlock()

	for token, session := range a.sessions {
		if now.After(session.ExpiresAt) {
			delete(a.sessions, token)
		}
	}
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate token: %v", err))
	}
	return hex.EncodeToString(b)
}
