package account

import (
	"encoding/base64"
	"encoding/json"
	"strings"
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
	// MaskedKey 是 AccessToken(API Key) 的脱敏展示版(仅首尾保留,如 sk-****abcd),
	// 仅在 GetAccounts 深拷贝时填充,供前端编辑态辨认"已配置 Key"且绝不下发明文。
	MaskedKey string `json:"maskedKey,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	// EgressIP 是账号绑定的专属出口伪装 IP(如 104.28.19.82),经 Worker 代理出口时通过 X-Egress-IP 请求头透传。
	EgressIP string `json:"egressIp,omitempty"`
	// TokenEndpoint 是 OAuth 账号的 token 刷新端点(如 Grok 的 https://auth.x.ai/oauth2/token)。
	// 仅后端刷新用;GetAccounts 深拷贝透出(非敏感,token 本身不清空),前端展示/编辑态可见。
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	DefaultModel  string `json:"defaultModel,omitempty"`
	ModelSonnet   string `json:"modelSonnet,omitempty"`
	ModelOpus     string `json:"modelOpus,omitempty"`
	ModelHaiku    string `json:"modelHaiku,omitempty"`
	ModelFable    string `json:"modelFable,omitempty"`
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

// SetRefreshToken safely updates the refresh token under write lock.
func (a *Account) SetRefreshToken(token string) {
	a.tokenMu.Lock()
	a.RefreshToken = token
	a.tokenMu.Unlock()
}

// GetTokenRefreshedAt safely reads the token refreshed timestamp under read lock.
func (a *Account) GetTokenRefreshedAt() int64 {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.TokenRefreshedAt
}

// AccessTokenExp 解析 access_token(JWT,OAuth 账号)的 exp claim,返回 Unix 秒;非 JWT/无 exp/解析失败返回 0。
// 供 Token 刷新监控判断「access_token 是否仍有效」:未过期则不必刷新,避免对刚导入的有效凭证
// 无谓地打 token_endpoint 触发上游风控(详见 CheckAndRefreshTokens)。
// 仅在 tokenMu 读锁内取 token 后释放再解析,避免持锁做 base64/json 解析。
func (a *Account) AccessTokenExp() int64 {
	a.tokenMu.RLock()
	tok := a.AccessToken
	a.tokenMu.RUnlock()
	return jwtExpClaim(tok)
}

// jwtExpClaim 解析 JWT(如 xAI/Google OAuth access_token)中段 payload 的 "exp" claim。
// 非标准 JWT(无 3 段/非 base64url/无 exp) 统一返回 0,语义「无法判定过期,按原逻辑处理」。
// 与 internal/quota/xai_oauth.go 的 parseJWTIdentity 同源思路,但不依赖 id_token——access_token
// 同样是 JWT 且带 exp。
func jwtExpClaim(token string) int64 {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload := parts[1]
	// base64url padding 补齐。
	payload += strings.Repeat("=", (4-len(payload)%4)%4)
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return 0
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return 0
	}
	v, ok := claims["exp"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return 0
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
	// OtherLBModes 按 GroupID 持久化 Other 号池各组独立 LB 算法(round-robin/sticky)。
	OtherLBModes map[string]string `json:"otherLbModes,omitempty"`
	// NvidiaLBMode 持久化 NVIDIA 号池 LB 算法
	NvidiaLBMode string `json:"nvidiaLbMode,omitempty"`
	// GrokLBMode 持久化 Grok 号池 LB 算法(与 NvidiaLBMode 同构单池单值)。
	GrokLBMode string `json:"grokLbMode,omitempty"`
	// 单账号最大并发数限制:请求打到某账号起算占 1 槽,本次请求结束释放;超过上限即换号。
	// 0/负数视作「未配置」,Get 时回退默认 10(对齐 NvidiaLBMode 空串回退范式)。
	// 三池单值 + Other 按 GroupID map(与 OtherLBModes 同范式,持久化键小写规范化)。
	NvidiaMaxConcurrency      int            `json:"nvidiaMaxConcurrency,omitempty"`
	AntigravityMaxConcurrency int            `json:"antigravityMaxConcurrency,omitempty"`
	ProjectMaxConcurrency     int            `json:"projectMaxConcurrency,omitempty"`
	OtherMaxConcurrency       map[string]int `json:"otherMaxConcurrency,omitempty"`
	// GrokMaxConcurrency 持久化 Grok 号池单账号在途并发上限(单池单值,与 NvidiaMaxConcurrency 同口径)。
	GrokMaxConcurrency int `json:"grokMaxConcurrency,omitempty"`
	// GrokCliVersion 持久化 Grok 号池全局 CLI 客户端版本号(号池单值,对仗 GrokMaxConcurrency)。
	// 用于发往 cli-chat-proxy.grok.com 上游的 x-grok-client-version 身份头(规避 426 版本闸门)。
	// 空串=未配置, GetGrokCliVersion 回退默认 DefaultGrokCliVersion("1.0.0")。
	GrokCliVersion string `json:"grokCliVersion,omitempty"`
	// GrokQuotaCooldownHours 持久化 Grok 号池「额度超限后冷却时长」(单池单值, 对仗 GrokCliVersion,
	// 单位小时)。仅在单账号 429/403 原地等 5s 重试 1 次仍失败时挂该冷却(默认 24h=1 天)。
	// 0/负数=未配置, GetGrokQuotaCooldownHours 回退默认 DefaultGrokQuotaCooldownHours(24)。
	GrokQuotaCooldownHours int `json:"grokQuotaCooldownHours,omitempty"`
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
	// grokPoolMode 是 Grok(x.ai) 号池的负载均衡总开关,与 nvidiaPoolMode 同构互斥。
	grokPoolMode bool
	// grokLBMode 持久化 Grok 号池 LB 算法(round-robin/sticky),单池单值(与 nvidiaLBMode 同范式)。
	grokLBMode string
	// otherPoolMode 是 Other 号池(自定义多上游组)的负载均衡总开关,与 poolMode/projectPoolMode/nvidiaPoolMode 同构互斥。
	otherPoolMode bool
	// otherLBModes 按 GroupID 维度保存各组独立的 LB 算法(round-robin/sticky),与 nvidiaLBMode(单池单值)不同,
	// 因 Other 号池内可有多个独立上游组,每组应有自己的轮询策略。
	otherLBModes map[string]string
	// 单账号最大并发数限制(在途并发上限):对应 AccountsData 的三个单值 + Other map。
	// 0/负数=未配置,Get 时回退默认 10。Set 负数置 0(等同回退);Other map 键小写规范化(与 otherLBModes 同范式)。
	// relay 与 proxy 选号链路经 FilterByConcurrency 过滤超限账号、超限换号;全满则 LeastLoaded 超额降级。
	nvidiaMaxConcurrency      int
	antigravityMaxConcurrency int
	projectMaxConcurrency     int
	otherMaxConcurrency       map[string]int
	// grokMaxConcurrency 是 Grok 号池单账号在途并发上限(单池单值,与 nvidiaMaxConcurrency 同口径);
	// 0/负数=未配置,Get 时回退默认 10。
	grokMaxConcurrency int
	// grokCliVersion 持久化 Grok 号池全局 CLI 客户端版本号(单池单值,对仗 grokMaxConcurrency);
	// 空串=未配置, GetGrokCliVersion 回退默认 DefaultGrokCliVersion("1.0.0")。用于发往
	// cli-chat-proxy.grok.com 上游的 x-grok-client-version 身份头(规避 426 版本闸门)。
	grokCliVersion string
	// grokQuotaCooldownHours 持久化 Grok 号池「额度超限后冷却时长」(单池单值, 对仗 grokCliVersion,
	// 单位小时)。仅在单账号 429/403 原地等 5s 重试 1 次仍失败时挂该冷却(默认 24h=1 天)。
	// 0/负数=未配置, GetGrokQuotaCooldownHours 回退默认 DefaultGrokQuotaCooldownHours(24)。
	grokQuotaCooldownHours int
	// concurrency 是单账号在途并发计数器(纯内存易失),由 Manager 单实例持有,
	// relay 的 APICompatHandler.accountMgr 与 proxy 的 ProxyHandler.accountMgr 同一引用,
	// 天然共享同一份在途计数。NewManager 初始化非 nil。详见 concurrency.go。
	concurrency        *Concurrency
	activeChannel      string
	currentIndex       int
	errorCounts        map[string]int // accountId -> error count
	refreshLocks       sync.Map       // accountId -> *sync.Mutex (Double-Checked Locking)
	cooldownTicker     *time.Ticker
	cooldownStop       chan struct{}
	tokenRefreshTicker *time.Ticker
	tokenRefreshStop   chan struct{}
	// grokAuthTicker/Stop 是 Grok 号池专用的「授权过期检查→刷新→失效移除」1 小时定时器,
	// 与全局 tokenRefreshTicker 并列但职责正交:grok 脱离 CheckAndRefreshTokens(见其 grok 排除
	// 分支),专走 CheckAndPurgeGrokAuth 的「JWT exp 临近过期才刷 + 永久失败移除」语义。
	grokAuthTicker *time.Ticker
	grokAuthStop   chan struct{}

	// idEpoch 是 generateAccountID 的进程内随机基数,NewManager 时用 crypto/rand 一次性生成。
	// 作用:消除「同纳秒同取模 → 同 ID」的并发碰撞(Windows/高频导入下尤其明显),
	// 即便同一秒内连发多次 generateAccountID,因 epoch 不同也不会撞号。
	// idSeq 由 generateAccountID 在调用方的临界区内自增,保证同一进程内每次生成 ID 严格递增不重复。
	idEpoch uint64
	idSeq   uint64

	// 解耦回调函数
	OnAccountsUpdated        func(accounts []*Account)
	OnAccountDisabled        func(accountId string)
	OnAccountCooldownUpdated func(accountId string, category string, untilTimeMs int64)
	OnQuotaRestored          func(accountId string, categories []string)
	FetchQuota               func(account *Account) (*QuotaResult, error)
	RefreshToken             func(account *Account) (string, error)
	OnQuotaUpdated           func(accountId string, result *QuotaResult)
}
