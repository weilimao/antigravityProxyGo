package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/db"
	"golang.org/x/crypto/bcrypt"
)

type ModelQuota struct {
	EnableFixed bool   `json:"enableFixed"`
	FixedTokens int64  `json:"fixedTokens"`
	ResetAt     string `json:"resetAt,omitempty"`

	EnableHourly bool    `json:"enableHourly"`
	HourlyHours  float64 `json:"hourlyHours"`
	HourlyTokens int64   `json:"hourlyTokens"`

	EnableDaily bool    `json:"enableDaily"`
	DailyDays   float64 `json:"dailyDays"`
	DailyTokens int64   `json:"dailyTokens"`
}

type UserQuotas struct {
	Gemini        ModelQuota `json:"gemini"`
	Claude        ModelQuota `json:"claude"`
	Nvidia        ModelQuota `json:"nvidia"`
	Grok          ModelQuota `json:"grok"`
	ValidDuration int        `json:"validDuration"`
	ValidUnit     string     `json:"validUnit"` // "days", "months", "years"
	ExpireAt      int64      `json:"expireAt"`
	RateLimit     int        `json:"rateLimit"` // 每分钟请求次数限制，0 表示默认 30
}

type UserAPIKey struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Key               string    `json:"key"`
	CreatedAt         time.Time `json:"createdAt"`
	LimitGeminiTokens int64     `json:"limitGeminiTokens"`
	LimitClaudeTokens int64     `json:"limitClaudeTokens"`
	LimitNvidiaTokens int64     `json:"limitNvidiaTokens"`
	LimitGrokTokens   int64     `json:"limitGrokTokens"`
	UsedGeminiTokens  int64     `json:"usedGeminiTokens"`
	UsedClaudeTokens  int64     `json:"usedClaudeTokens"`
	UsedNvidiaTokens  int64     `json:"usedNvidiaTokens"`
	UsedGrokTokens    int64     `json:"usedGrokTokens"`
	// AllowedModels 是该 API Key 授权可调用的模型白名单(精确匹配)。
	// 空/nil = 不限制(全部模型授权,兼容旧数据); 非空 = 仅允许列表中的模型名完全一致时调用。
	AllowedModels []string `json:"allowedModels,omitempty"`
}

// UserAutoConfig 定义中继用户私有的 Auto 并发竞速模型配置
type UserAutoConfig struct {
	// Enabled 标识该用户是否启用了私有 Auto 竞速配置。若为 false 则该配置处于关闭状态
	Enabled bool `json:"enabled"`
	// CandidateModels 是该用户专属的自定义候选模型清单
	CandidateModels []string `json:"candidateModels"`
	// UseBenchmarkPool 标识是否将控制台测速池融入竞速候选
	UseBenchmarkPool bool `json:"useBenchmarkPool"`
}

type RelayUser struct {
	ID           string          `json:"id"`
	Key          string          `json:"key"`
	PasswordHash string          `json:"passwordHash"`
	Enabled      bool            `json:"enabled"`
	CreatedAt    time.Time       `json:"createdAt"`
	Remark       string          `json:"remark,omitempty"`
	Role         string          `json:"role,omitempty"`
	IsAdmin      bool            `json:"isAdmin,omitempty"`
	Quotas       UserQuotas      `json:"quotas"`
	APIKeys      []UserAPIKey    `json:"apiKeys"`
	AutoConfig   *UserAutoConfig `json:"autoConfig,omitempty"`
}

// IsAdminUser 判断用户是否具备中继管理员权限
func (u *RelayUser) IsAdminUser() bool {
	if u == nil {
		return false
	}
	return u.IsAdmin || strings.EqualFold(u.Role, "admin")
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

// EnsureAdminUser 确保管理员账号存在并具备 admin 权限
func (m *UserManager) EnsureAdminUser(key, password, remark string) (*RelayUser, error) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.Key == key {
			u.IsAdmin = true
			u.Role = "admin"
			u.Enabled = true
			if password != "" {
				u.PasswordHash = hashPassword(password)
			}
			m.saveToDiskLocked()
			return u, nil
		}
	}

	adminUser := &RelayUser{
		ID:           generateID(),
		Key:          key,
		PasswordHash: hashPassword(password),
		Enabled:      true,
		CreatedAt:    time.Now(),
		Remark:       remark,
		Role:         "admin",
		IsAdmin:      true,
	}
	m.users = append(m.users, adminUser)
	m.saveToDiskLocked()
	return adminUser, nil
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

func (m *UserManager) UpdateUserQuota(id string, quotas UserQuotas, resetLimit bool) error {
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

			if resetLimit {
				resetTimeStr := time.Now().Format(time.RFC3339)
				quotas.Gemini.ResetAt = resetTimeStr
				quotas.Claude.ResetAt = resetTimeStr

				// Reset rolling windows in SQLite database
				_ = db.SetQuotaWindowStart(id, "gemini_hourly", resetTimeStr)
				_ = db.SetQuotaWindowStart(id, "gemini_daily", resetTimeStr)
				_ = db.SetQuotaWindowStart(id, "claude_hourly", resetTimeStr)
				_ = db.SetQuotaWindowStart(id, "claude_daily", resetTimeStr)
				// Grok 配额窗口与 gemini/claude 同口径重置(grok_quota.go 用的 quotaType)。
				// NVIDIA 历史上未纳入重置列表(其 ResetAt 不重置), 这里 Grok 与 gemini/claude 对齐,
				// 使前端「重置限额」按钮对 Grok 池同样生效。
				_ = db.SetQuotaWindowStart(id, "grok_hourly", resetTimeStr)
				_ = db.SetQuotaWindowStart(id, "grok_daily", resetTimeStr)
			} else {
				// Retain existing ResetAt values
				quotas.Gemini.ResetAt = u.Quotas.Gemini.ResetAt
				quotas.Claude.ResetAt = u.Quotas.Claude.ResetAt
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
				return nil, fmt.Errorf("invalid credentials")
			}
			if !u.Enabled {
				return nil, fmt.Errorf("user %q is disabled", key)
			}
			return u, nil
		}
	}
	return nil, fmt.Errorf("invalid credentials")
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

func (m *UserManager) UpdateAPIKeyQuota(userID string, keyID string, limitGemini, limitClaude int64, allowedModels []string) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == keyID {
					u.APIKeys[i].LimitGeminiTokens = limitGemini
					u.APIKeys[i].LimitClaudeTokens = limitClaude
					u.APIKeys[i].AllowedModels = allowedModels
					m.saveToDiskLocked()
					return nil
				}
			}
			return fmt.Errorf("api key not found")
		}
	}
	return fmt.Errorf("user not found")
}

// IsModelAuthorizedForAPIKey 校验某 API Key 是否授权了指定模型(精确匹配)。
// 语义: key.AllowedModels 为空→放行(全部允许,兼容旧数据); 非空→model 必须与列表
// 某项完全一致才放行。找不到 user 或 key(如 official_bypass/default_bypass 兜底分支
// 经 ValidateToken 赋的标记 APIKeyID, 无对应 UserAPIKey 实体)→放行,不阻断兜底链路。
func (m *UserManager) IsModelAuthorizedForAPIKey(userID, apiKeyID, model string) error {
	if apiKeyID == "" || model == "" {
		return nil
	}
	m.RLock()
	defer m.RUnlock()

	var user *RelayUser
	for _, u := range m.users {
		if u.ID == userID {
			user = u
			break
		}
	}
	if user == nil {
		return nil
	}

	var key *UserAPIKey
	for i := range user.APIKeys {
		if user.APIKeys[i].ID == apiKeyID {
			key = &user.APIKeys[i]
			break
		}
	}
	if key == nil {
		return nil
	}
	if len(key.AllowedModels) == 0 {
		return nil
	}
	for _, allowed := range key.AllowedModels {
		if allowed == model {
			return nil
		}
	}
	return fmt.Errorf("model %q is not authorized for this API key; allowed: %v", model, key.AllowedModels)
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

// RecordAPIKeyUsageForFamily 是 RecordAPIKeyUsage 的 family-aware 后继: 按 APIKeyFamily
// 四态(gemini/claude/nvidia/grok)累加到对应 Used* 桶。与方案 A 一致, 保持 RecordAPIKeyUsage
// 原签名与既有 7 处调用点零回归(NVIDIA/Other 历史落进 Gemini 桶的口径不动); Grok 链路
// (recordGrokUsage)改调本方法, 把 Grok 用量计入独立 UsedGrokTokens 桶, 使 APIKey 级限额
// 校验(app_lifecycle.go 的 LimitGrokTokens 分支)能正确命中。
//
// family==FamilyGrok → UsedGrokTokens; FamilyClaude → UsedClaudeTokens;
// FamilyNvidia → UsedNvidiaTokens; 其余/未识别(FamilyGemini) → UsedGeminiTokens 兜底
// (与 RecordAPIKeyUsage(false,...) 等价, 保持既有"非 claude 即 Gemini"兜底口径)。
func (m *UserManager) RecordAPIKeyUsageForFamily(userID string, apiKeyID string, family APIKeyFamily, tokens int64) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == apiKeyID {
					switch family {
					case FamilyClaude:
						u.APIKeys[i].UsedClaudeTokens += tokens
					case FamilyNvidia:
						u.APIKeys[i].UsedNvidiaTokens += tokens
					case FamilyGrok:
						u.APIKeys[i].UsedGrokTokens += tokens
					default: // FamilyGemini / 未识别
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
	if err := os.WriteFile(m.persistPath, data, 0600); err != nil {
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

// UpdateUserAutoConfig 更新指定用户的私有 Auto 竞速模型配置并实时落盘
func (m *UserManager) UpdateUserAutoConfig(id string, cfg UserAutoConfig) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == id {
			cfgCopy := cfg
			u.AutoConfig = &cfgCopy
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user %q not found", id)
}

// GetUserAutoConfig 获取指定用户的私有 Auto 竞速配置指针(若无则返回 nil)
func (m *UserManager) GetUserAutoConfig(id string) *UserAutoConfig {
	m.RLock()
	defer m.RUnlock()

	for _, u := range m.users {
		if u.ID == id {
			if u.AutoConfig == nil {
				return nil
			}
			cp := *u.AutoConfig
			return &cp
		}
	}
	return nil
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
