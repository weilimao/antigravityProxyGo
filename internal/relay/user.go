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
	"gorm.io/gorm"
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
	LimitTokens       int64     `json:"limitTokens"`       // 统一/全模型总限额（0 表示无总额限制或跟随分桶）
	UsedTokens        int64     `json:"usedTokens"`        // 累计全部已用 Token 总量
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
	usageStore  UserUsageStore
	gormDB      *gorm.DB
}

func NewUserManager() *UserManager {
	return &UserManager{
		users: make([]*RelayUser, 0),
	}
}

func (m *UserManager) Init(dataDir string) {
	m.Lock()
	m.persistPath = filepath.Join(dataDir, "relay_users.json")
	if m.usageStore == nil {
		m.usageStore = NewUserUsageStore(m.gormDB, func() {
			m.SaveToDisk()
		})
	}
	m.Unlock()

	m.LoadFromDisk()
	if m.gormDB != nil {
		m.SyncFromDB()
	}
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

// UpdateUserExpireAt 更新指定用户的套餐到期时间戳(Unix秒，0表示永久有效)，实时刷新内存并持久化
func (m *UserManager) UpdateUserExpireAt(userIdentifier string, expireAt int64) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userIdentifier || u.Key == userIdentifier {
			u.Quotas.ExpireAt = expireAt
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("user %q not found", userIdentifier)
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

func (m *UserManager) GetUserByKey(key string) *RelayUser {
	m.RLock()
	defer m.RUnlock()
	for _, u := range m.users {
		if u.Key == key {
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

// SyncOrAddUser 供 Web 平台同步用户：若用户已存在则启用，若不存在则创建
func (m *UserManager) SyncOrAddUser(key, password, remark string) (*RelayUser, error) {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.Key == key {
			u.Enabled = true
			if password != "" {
				u.PasswordHash = hashPassword(password)
			}
			if remark != "" {
				u.Remark = remark
			}
			m.saveToDiskLocked()
			return u, nil
		}
	}

	user := &RelayUser{
		ID:           generateID(),
		Key:          key,
		PasswordHash: hashPassword(password),
		Enabled:      true,
		CreatedAt:    time.Now(),
		Remark:       remark,
		Role:         "user",
	}
	m.users = append(m.users, user)
	m.saveToDiskLocked()
	return user, nil
}

// CreateAPIKeyWithOptions 为用户创建 API Key，支持指定密钥串、授权模型白名单与额度（支持可选的统一总限额 limitTokens）
func (m *UserManager) CreateAPIKeyWithOptions(userIdentifier, name, customKey string, allowedModels []string, limitGemini, limitClaude int64, limitTokens ...int64) (*UserAPIKey, error) {
	m.Lock()
	defer m.Unlock()

	var totalLimit int64
	if len(limitTokens) > 0 && limitTokens[0] > 0 {
		totalLimit = limitTokens[0]
	}

	for _, u := range m.users {
		if u.ID == userIdentifier || u.Key == userIdentifier {
			keyStr := strings.TrimSpace(customKey)
			if keyStr == "" {
				keyStr = "sk-ant-" + generateID()
			}

			// 若指定的 key 已存在，则执行幂等就地更新并返回，避免产生重复 Key 记录
			for i := range u.APIKeys {
				if u.APIKeys[i].Key == keyStr {
					if name != "" {
						u.APIKeys[i].Name = name
					}
					u.APIKeys[i].AllowedModels = allowedModels
					if totalLimit > 0 {
						u.APIKeys[i].LimitTokens = totalLimit
					}
					if limitGemini > 0 {
						u.APIKeys[i].LimitGeminiTokens = limitGemini
					}
					if limitClaude > 0 {
						u.APIKeys[i].LimitClaudeTokens = limitClaude
					}
					m.saveToDiskLocked()
					return &u.APIKeys[i], nil
				}
			}

			newKey := UserAPIKey{
				ID:                generateID(),
				Name:              name,
				Key:               keyStr,
				CreatedAt:         time.Now(),
				AllowedModels:     allowedModels,
				LimitTokens:       totalLimit,
				LimitGeminiTokens: limitGemini,
				LimitClaudeTokens: limitClaude,
			}
			u.APIKeys = append(u.APIKeys, newKey)
			m.saveToDiskLocked()
			return &newKey, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

// DeleteAPIKeyByKey 根据 key 密钥内容或 ID 物理删除对应 API Key
func (m *UserManager) DeleteAPIKeyByKey(userIdentifier string, keyOrID string) error {
	m.Lock()
	defer m.Unlock()

	for _, u := range m.users {
		if u.ID == userIdentifier || u.Key == userIdentifier {
			for i, k := range u.APIKeys {
				if k.ID == keyOrID || k.Key == keyOrID {
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
	for _, u := range m.users {
		if !u.Enabled {
			continue
		}
		for i, k := range u.APIKeys {
			if k.Key == token {
				userPtr := u
				keyPtr := &u.APIKeys[i]
				m.RUnlock()
				return userPtr, keyPtr, nil
			}
		}
	}
	m.RUnlock()

	// 若开启了 DB 模式且内存未命中，尝试从数据库定向回表补齐缓存 (防止 Web 平台刚建 Key 时缓存未刷新)
	if m.gormDB != nil {
		if u, k, err := m.findAndCacheKeyFromDB(token); err == nil && u != nil && k != nil {
			return u, k, nil
		}
	}

	return nil, nil, fmt.Errorf("invalid api key")
}

func (m *UserManager) UpdateAPIKeyQuota(userID string, keyID string, limitGemini, limitClaude int64, allowedModels []string, limitTokens ...int64) error {
	m.Lock()
	defer m.Unlock()

	var totalLimit int64
	if len(limitTokens) > 0 && limitTokens[0] > 0 {
		totalLimit = limitTokens[0]
	}

	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == keyID {
					u.APIKeys[i].LimitGeminiTokens = limitGemini
					u.APIKeys[i].LimitClaudeTokens = limitClaude
					u.APIKeys[i].AllowedModels = allowedModels
					if totalLimit > 0 {
						u.APIKeys[i].LimitTokens = totalLimit
					}
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

// CheckAPIKeyQuota 校验某 API Key 在调用指定模型时是否已耗尽额度。
// 规则：
// 1. 若为 official_bypass / default_bypass / 空 KeyID 等免鉴权白名单会话，直接放行。
// 2. 检查账户有效期限(ExpireAt)。
// 3. 检查 API Key 统一总配额(LimitTokens)：当 LimitTokens > 0 时，若全模型累计用量(Max(UsedTokens, 分桶之和))已达到或超过 LimitTokens，直接拦截。
// 4. 若未命中总配额限制或 LimitTokens 未设定，兼容检查分模型家族配额(LimitClaudeTokens/LimitGeminiTokens/LimitNvidiaTokens/LimitGrokTokens)。
func (m *UserManager) CheckAPIKeyQuota(userID, apiKeyID, model string) error {
	if apiKeyID == "" || apiKeyID == APIKeyIDOfficialBypass || apiKeyID == APIKeyIDDefaultBypass {
		return nil
	}
	m.RLock()
	defer m.RUnlock()

	var user *RelayUser
	for _, u := range m.users {
		if u.ID == userID || u.Key == userID {
			user = u
			break
		}
	}
	if user == nil {
		return nil
	}
	if user.Quotas.ExpireAt > 0 && time.Now().Unix() > user.Quotas.ExpireAt {
		return fmt.Errorf("subscription expired: your plan expired at %s, please renew to continue", time.Unix(user.Quotas.ExpireAt, 0).Format("2006-01-02 15:04:05"))
	}

	var key *UserAPIKey
	for i := range user.APIKeys {
		if user.APIKeys[i].ID == apiKeyID || user.APIKeys[i].Key == apiKeyID {
			key = &user.APIKeys[i]
			break
		}
	}
	if key == nil {
		return nil
	}

	// 计算当前 Key 的全量累计使用量(防止单项计数漂移，取 UsedTokens 与分桶之和的最大值)
	totalUsed := key.UsedTokens
	subTotal := key.UsedGeminiTokens + key.UsedClaudeTokens + key.UsedNvidiaTokens + key.UsedGrokTokens
	if subTotal > totalUsed {
		totalUsed = subTotal
	}

	// 1. 统一总配额校验(优先)：商业平台与用户统一限额核心防线
	if key.LimitTokens > 0 && totalUsed >= key.LimitTokens {
		return fmt.Errorf("API Key token limit exceeded (used %d / limit %d)", totalUsed, key.LimitTokens)
	}

	// 2. 分模型家族配额校验(兼容旧版细粒度配置)
	family := DetectAPIKeyFamily(model)
	switch family {
	case FamilyClaude:
		if key.LimitClaudeTokens > 0 && key.UsedClaudeTokens >= key.LimitClaudeTokens {
			return fmt.Errorf("API Key Claude token limit exceeded (%d / %d)", key.UsedClaudeTokens, key.LimitClaudeTokens)
		}
	case FamilyNvidia:
		if key.LimitNvidiaTokens > 0 && key.UsedNvidiaTokens >= key.LimitNvidiaTokens {
			return fmt.Errorf("API Key NVIDIA token limit exceeded (%d / %d)", key.UsedNvidiaTokens, key.LimitNvidiaTokens)
		}
	case FamilyGrok:
		if key.LimitGrokTokens > 0 && key.UsedGrokTokens >= key.LimitGrokTokens {
			return fmt.Errorf("API Key Grok token limit exceeded (%d / %d)", key.UsedGrokTokens, key.LimitGrokTokens)
		}
	default:
		if key.LimitGeminiTokens > 0 && key.UsedGeminiTokens >= key.LimitGeminiTokens {
			return fmt.Errorf("API Key Gemini token limit exceeded (%d / %d)", key.UsedGeminiTokens, key.LimitGeminiTokens)
		}
	}

	return nil
}

func (m *UserManager) RecordAPIKeyUsage(userID string, apiKeyID string, isClaude bool, tokens int64) {
	family := FamilyGemini
	if isClaude {
		family = FamilyClaude
	}
	m.RecordAPIKeyUsageForFamily(userID, apiKeyID, family, tokens)
}

// RecordAPIKeyUsageForFamily 是 RecordAPIKeyUsage 的 family-aware 后继: 按 APIKeyFamily
// 四态(gemini/claude/nvidia/grok)累加到对应 Used* 桶。
// 核心优化: 彻底移除请求级全量 json.MarshalIndent 与同步写盘，转由 UserUsageStore 异步批处理落库或防抖落盘，
// 保证接口零等待，CPU 极低开销。
func (m *UserManager) RecordAPIKeyUsageForFamily(userID string, apiKeyID string, family APIKeyFamily, tokens int64) {
	if tokens <= 0 {
		return
	}

	var keyStr string
	var store UserUsageStore

	m.Lock()
	for _, u := range m.users {
		if u.ID == userID {
			for i, k := range u.APIKeys {
				if k.ID == apiKeyID || k.Key == apiKeyID {
					keyStr = k.Key
					u.APIKeys[i].UsedTokens += tokens
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
					break
				}
			}
			break
		}
	}
	store = m.usageStore
	m.Unlock()

	// 异步解耦落库，零阻塞返回
	if store != nil {
		store.RecordUsage(UsageRecord{
			UserID:    userID,
			APIKeyID:  apiKeyID,
			KeyStr:    keyStr,
			Family:    family,
			Tokens:    tokens,
			Timestamp: time.Now(),
		})
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
