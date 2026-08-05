package account

import (
	"sync"
	"time"
)

// account.go(精简后): 仅保留 Account 结构体定义与 Token 并发访问器。
// Manager 结构、构造、加载保存、CRUD 见 account_manager.go;
// 选号/冷却/号池模式/可用过滤见 account_selector.go;
// 定时监控/Token 刷新/错误计数/配额回写见 account_monitor.go。

type Account struct {
	// tokenMu protects concurrent reads/writes of AccessToken and RefreshToken
	tokenMu sync.RWMutex `json:"-"`

	ID               string           `json:"id"`
	Email            string           `json:"email"`
	AccessToken      string           `json:"access_token"`
	RefreshToken     string           `json:"refresh_token"`
	Provider         string           `json:"provider"`
	ProjectID        string           `json:"projectId"`
	ProjectLabel     string           `json:"projectLabel"`
	ScopeType        string           `json:"scopeType"`
	AddedAt          string           `json:"addedAt"`
	Tier             string           `json:"tier"`
	Enabled          bool             `json:"enabled"`
	EnableOverages   bool             `json:"enableOverages"`
	Credits          *float64         `json:"credits"`
	Cooldowns        map[string]int64 `json:"cooldowns"`     // category -> untilTimeMs
	CooldownUntil    int64            `json:"cooldownUntil"` // min(cooldowns)
	TwoFASecret      string           `json:"twofa_secret,omitempty"`
	TokenRefreshedAt int64            `json:"token_refreshed_at"`
	BaseURL          string           `json:"baseUrl,omitempty"`
	DefaultModel     string           `json:"defaultModel,omitempty"`
	ModelSonnet      string           `json:"modelSonnet,omitempty"`
	ModelOpus        string           `json:"modelOpus,omitempty"`
	ModelHaiku       string           `json:"modelHaiku,omitempty"`
	ModelFable       string           `json:"modelFable,omitempty"`
	// GroupID 是 Other 号池内的组标识(如 "openai"/"deepseek"),与 Provider="other" 配合使用。
	// 非 Other 号池账号该字段空。同 GroupID 下可挂多条账号(BaseURL 相同、多 Key)做组内轮换。
	GroupID string `json:"groupId,omitempty"`
	// GroupName 是 Other 号池组的显示名(如 "OpenAI 上游组"),前端 Tab/卡片用它辨认组。
	GroupName string `json:"groupName,omitempty"`
	// Formats 是该 Other 上游组原生支持的协议集合,子集 ["openai","anthropic"]。
	// 中继转发层据此决定上游端点(/v1/chat/completions 或 /v1/messages)与协议转译方向。
	// 非 Other 号池账号该字段空。
	Formats []string `json:"formats,omitempty"`
}

// GetAccessToken safely reads the access token under read lock.
func (a *Account) GetAccessToken() string {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.AccessToken
}

// SetAccessToken safely updates the access token under write lock.
func (a *Account) SetAccessToken(token string) {
	a.tokenMu.Lock()
	a.AccessToken = token
	if token != "" {
		a.TokenRefreshedAt = time.Now().Unix()
	}
	a.tokenMu.Unlock()
}

// GetTokenRefreshedAt safely reads the token refreshed timestamp under read lock.
func (a *Account) GetTokenRefreshedAt() int64 {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.TokenRefreshedAt
}

type QuotaBucket struct {
	ModelID           string  `json:"modelId"`
	Group             string  `json:"group"`
	RemainingFraction float64 `json:"remainingFraction"`
	RemainPercent     int     `json:"remainPercent"`
	ResetTime         string  `json:"resetTime"`
}

type QuotaResult struct {
	Buckets []QuotaBucket `json:"buckets"`
	Tier    string        `json:"tier"`
	Credits *float64      `json:"credits,omitempty"`
	Error   string        `json:"error,omitempty"`
}

type AccountsData struct {
	Accounts          []*Account `json:"accounts"`
	TwoFAAccounts     []*Account `json:"twofa_accounts,omitempty"`
	PoolMode          bool       `json:"poolMode"`
	ProjectPoolMode   bool       `json:"projectPoolMode"`
	GeminiCliPoolMode bool       `json:"geminiCliPoolMode"`
	ActiveChannel     string     `json:"activeChannel"`
}

type Manager struct {
	sync.RWMutex
	fileLock          sync.Mutex
	userDataPath      string
	accountsFilePath  string
	accounts          []*Account
	twofaAccounts     []*Account
	poolMode          bool
	projectPoolMode   bool
	geminiCliPoolMode bool
	nvidiaPoolMode    bool
	nvidiaLBMode      string
	// otherPoolMode 是 Other 号池(自定义多上游组)的负载均衡总开关,与 poolMode/projectPoolMode/nvidiaPoolMode 同构互斥。
	otherPoolMode bool
	// otherLBModes 按 GroupID 维度保存各组独立的 LB 算法(round-robin/sticky),与 nvidiaLBMode(单池单值)不同,
	// 因 Other 号池内可有多个独立上游组,每组应有自己的轮询策略。
	otherLBModes       map[string]string
	activeChannel      string
	currentIndex       int
	errorCounts        map[string]int // accountId -> error count
	refreshLocks       sync.Map       // accountId -> *sync.Mutex (Double-Checked Locking)
	cooldownTicker     *time.Ticker
	cooldownStop       chan struct{}
	tokenRefreshTicker *time.Ticker
	tokenRefreshStop   chan struct{}

	// 解耦回调函数
	OnAccountsUpdated        func(accounts []*Account)
	OnAccountDisabled        func(accountId string)
	OnAccountCooldownUpdated func(accountId string, category string, untilTimeMs int64)
	OnQuotaRestored          func(accountId string, categories []string)
	FetchQuota               func(account *Account) (*QuotaResult, error)
	RefreshToken             func(account *Account) (string, error)
	OnQuotaUpdated           func(accountId string, result *QuotaResult)
}
