package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type ModelQuota struct {
	EnableFixed  bool  `json:"enableFixed"`
	FixedTokens  int64 `json:"fixedTokens"`

	EnableHourly bool    `json:"enableHourly"`
	HourlyHours  float64 `json:"hourlyHours"`
	HourlyTokens int64   `json:"hourlyTokens"`

	EnableDaily  bool    `json:"enableDaily"`
	DailyDays    float64 `json:"dailyDays"`
	DailyTokens  int64   `json:"dailyTokens"`
}

type UserQuotas struct {
	Gemini ModelQuota `json:"gemini"`
	Claude ModelQuota `json:"claude"`
	ValidDuration int `json:"validDuration"`
	ValidUnit     string `json:"validUnit"` // "days", "months", "years"
	ExpireAt      int64  `json:"expireAt"`
	RateLimit     int    `json:"rateLimit"` // 每分钟请求次数限制，0 表示默认 30
}

type UserAPIKey struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Key               string    `json:"key"`
	CreatedAt         time.Time `json:"createdAt"`
	LimitGeminiTokens int64     `json:"limitGeminiTokens"`
	LimitClaudeTokens int64     `json:"limitClaudeTokens"`
	UsedGeminiTokens   int64     `json:"usedGeminiTokens"`
	UsedClaudeTokens   int64     `json:"usedClaudeTokens"`
}

type RelayUser struct {
	ID           string       `json:"id"`
	Key          string       `json:"key"`
	PasswordHash string       `json:"passwordHash"`
	Enabled      bool         `json:"enabled"`
	CreatedAt    time.Time    `json:"createdAt"`
	Remark       string       `json:"remark,omitempty"`
	Quotas       UserQuotas   `json:"quotas"`
	APIKeys      []UserAPIKey `json:"apiKeys"`
}

type UserManager struct {
	sync.RWMutex
	users       []*RelayUser
	persistPath string
}

func NewUserManager() *UserManager {
	return &UserManager{
		users: make([]*RelayUser, 0),
	}
}

func (m *UserManager) Init(dataDir string) {
	m.Lock()
	m.persistPath = filepath.Join(dataDir, "relay_users.json")
	m.Unlock()

	m.LoadFromDisk()
}

func (m *UserManager) AddUser(key, password, remark string) (*RelayUser, error) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.Key == key {
			return nil, fmt.Errorf("user key %q already exists", key)
		}
	}

	user := &RelayUser{
		ID:           generateID(),
		Key:          key,
		PasswordHash: hashPassword(password),
		Enabled:      true,
		CreatedAt:    time.Now(),
		Remark:       remark,
	}
	m.users = append(m.users, user)
	m.saveToDiskLocked()
	return user, nil
}

func (m *UserManager) RemoveUser(id string) error {
	m.Lock()
	defer m.Unlock()

	for i, u := range m.users {
		if u.ID == id {
			m.users = append(m.users[:i], m.users[i+1:]...)
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user %q not found", id)
}

func (m *UserManager) UpdateUserEnabled(id string, enabled bool) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == id {
			u.Enabled = enabled
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *UserManager) UpdateUserQuota(id string, quotas UserQuotas) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == id {
			if quotas.ValidDuration > 0 {
				now := time.Now()
				if quotas.ValidUnit == "months" {
					now = now.AddDate(0, quotas.ValidDuration, 0)
				} else if quotas.ValidUnit == "years" {
					now = now.AddDate(quotas.ValidDuration, 0, 0)
				} else { // default to days
					now = now.AddDate(0, 0, quotas.ValidDuration)
				}
				quotas.ExpireAt = now.Unix()
			} else {
				quotas.ExpireAt = 0 // permanent
			}
			u.Quotas = quotas
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user not found")
}

func (m *UserManager) GetUsers() []*RelayUser {
	m.RLock()
	defer m.RUnlock()

	// Return a copy
	out := make([]*RelayUser, len(m.users))
	for i, u := range m.users {
		// Create a shallow copy so we don't return the original reference
		uc := *u
		uc.PasswordHash = "***"
		out[i] = &uc
	}
	return out
}

func (m *UserManager) GetUserByID(id string) *RelayUser {
	m.RLock()
	defer m.RUnlock()
	for _, u := range m.users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (m *UserManager) ValidateCredentials(key, password string) (*RelayUser, error) {
	m.RLock()
	defer m.RUnlock()

	for _, u := range m.users {
		if u.Key == key {
			if !checkPassword(u.PasswordHash, password) {
				return nil, fmt.Errorf("invalid password")
			}
			if !u.Enabled {
				return nil, fmt.Errorf("user %q is disabled", key)
			}
			return u, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", key)
}

func (m *UserManager) CreateAPIKey(userID string, name string) (*UserAPIKey, error) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			newKey := UserAPIKey{
				ID:        generateID(),
				Name:      name,
				Key:       "sk-ant-" + generateID(),
				CreatedAt: time.Now(),
			}
			u.APIKeys = append(u.APIKeys, newKey)
			m.saveToDiskLocked()
			return &newKey, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (m *UserManager) DeleteAPIKey(userID string, keyID string) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == keyID {
					u.APIKeys = append(u.APIKeys[:i], u.APIKeys[i+1:]...)
					m.saveToDiskLocked()
					return nil
				}
			}
			return fmt.Errorf("api key not found")
		}
	}
	return fmt.Errorf("user not found")
}

func (m *UserManager) ValidateAPIKey(token string) (*RelayUser, *UserAPIKey, error) {
	m.RLock()
	defer m.RUnlock()

	for _, u := range m.users {
		if !u.Enabled {
			continue
		}
		for i, k := range u.APIKeys {
			if k.Key == token {
				return u, &u.APIKeys[i], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("invalid api key")
}

func (m *UserManager) UpdateAPIKeyQuota(userID string, keyID string, limitGemini, limitClaude int64) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == keyID {
					u.APIKeys[i].LimitGeminiTokens = limitGemini
					u.APIKeys[i].LimitClaudeTokens = limitClaude
					m.saveToDiskLocked()
					return nil
				}
			}
			return fmt.Errorf("api key not found")
		}
	}
	return fmt.Errorf("user not found")
}

func (m *UserManager) RecordAPIKeyUsage(userID string, apiKeyID string, isClaude bool, tokens int64) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == apiKeyID {
					if isClaude {
						u.APIKeys[i].UsedClaudeTokens += tokens
					} else {
						u.APIKeys[i].UsedGeminiTokens += tokens
					}
					m.saveToDiskLocked()
					return
				}
			}
			return
		}
	}
}

func (m *UserManager) saveToDiskLocked() {
	if m.persistPath == "" {
		return
	}
	data, err := json.MarshalIndent(m.users, "", "  ")
	if err != nil {
		fmt.Printf("[UserManager] Failed to marshal users: %v\n", err)
		return
	}
	if err := os.WriteFile(m.persistPath, data, 0644); err != nil {
		fmt.Printf("[UserManager] Failed to write users: %v\n", err)
	}
}

func (m *UserManager) SaveToDisk() {
	m.RLock()
	defer m.RUnlock()
	m.saveToDiskLocked()
}

func (m *UserManager) LoadFromDisk() {
	m.Lock()
	defer m.Unlock()

	if m.persistPath == "" {
		m.users = make([]*RelayUser, 0)
		return
	}

	if _, err := os.Stat(m.persistPath); os.IsNotExist(err) {
		m.users = make([]*RelayUser, 0)
		return
	}

	raw, err := os.ReadFile(m.persistPath)
	if err != nil {
		m.users = make([]*RelayUser, 0)
		return
	}

	var loaded []*RelayUser
	if err := json.Unmarshal(raw, &loaded); err != nil {
		m.users = make([]*RelayUser, 0)
		return
	}
	m.users = loaded
}

func (m *UserManager) UpdatePath(newDir string) {
	m.SaveToDisk()

	m.Lock()
	m.persistPath = filepath.Join(newDir, "relay_users.json")
	m.Unlock()

	m.LoadFromDisk()
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random ID: %v", err))
	}
	return hex.EncodeToString(b)
}

func hashPassword(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("failed to hash password: %v", err))
	}
	return string(hash)
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
