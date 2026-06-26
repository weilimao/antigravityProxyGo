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
		ExpiresAt: now.Add(24 * time.Hour),
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
		return nil, fmt.Errorf("invalid token")
	}
	if time.Now().After(session.ExpiresAt) {
		a.Lock()
		delete(a.sessions, token)
		a.Unlock()
		return nil, fmt.Errorf("token expired")
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
