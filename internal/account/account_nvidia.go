package account

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// NVIDIA 等第三方 OpenAI 兼容上游的默认模型档位回退值与默认 BaseURL。
// 与 cc-switch 的 NVIDIA 预设保持一致，开箱即用。
const (
	DefaultNvidiaModel   = "moonshotai/kimi-k2.5"
	DefaultNvidiaBaseURL = "https://integrate.api.nvidia.com/v1"
)

// NvidiaModelField 是账号级四档位映射字段名。
type NvidiaModelField string

const (
	NvidiaModelSonnet  NvidiaModelField = "sonnet"
	NvidiaModelOpus    NvidiaModelField = "opus"
	NvidiaModelHaiku   NvidiaModelField = "haiku"
	NvidiaModelFable   NvidiaModelField = "fable"
	NvidiaModelDefault NvidiaModelField = "default"
)

// NvidiaAccountInput 是从前端/IPC 接收的 nvidia 账号录入参数。
type NvidiaAccountInput struct {
	BaseURL      string
	APIKey       string
	Label        string // 可选展示名（写入 Email 字段）
	DefaultModel string
	ModelSonnet  string
	ModelOpus    string
	ModelHaiku   string
	ModelFable   string
}

// ValidateNvidiaAccountInput 校验 nvidia 账号录入参数(新增表单,key 必填)。
func ValidateNvidiaAccountInput(in NvidiaAccountInput) error {
	return validateNvidiaFields(in, true)
}

// validateNvidiaFields 校验 nvidia 账号字段。requireKey 为 false 时允许 APIKey 留空
// (编辑态留空表示保持原 Key 不变)。
func validateNvidiaFields(in NvidiaAccountInput, requireKey bool) error {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultNvidiaBaseURL
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("base_url 必须是合法的 https 地址")
	}
	if requireKey && strings.TrimSpace(in.APIKey) == "" {
		return errors.New("api_key 不能为空")
	}
	return nil
}

// NewNvidiaAccount 根据 input 构造一个未入库的 nvidia Account。
// 不会触碰 Manager 全局锁，调用方负责 AddAccount 落库。
func NewNvidiaAccount(in NvidiaAccountInput) *Account {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultNvidiaBaseURL
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		// 用 base_url 的 host 当展示名，便于在号池中辨认
		if u, err := url.Parse(baseURL); err == nil {
			label = u.Host
		} else {
			label = "NVIDIA 账号"
		}
	}
	return &Account{
		Email:        label,
		Provider:     "nvidia",
		ScopeType:    "nvidia",
		AccessToken:  strings.TrimSpace(in.APIKey), // 复用 AccessToken 存 API Key
		BaseURL:      baseURL,
		DefaultModel: strings.TrimSpace(in.DefaultModel),
		ModelSonnet:  strings.TrimSpace(in.ModelSonnet),
		ModelOpus:    strings.TrimSpace(in.ModelOpus),
		ModelHaiku:   strings.TrimSpace(in.ModelHaiku),
		ModelFable:   strings.TrimSpace(in.ModelFable),
		Enabled:      true,
		AddedAt:      time.Now().Format(time.RFC3339),
		Cooldowns:    make(map[string]int64),
	}
}

// AddNvidiaAccount 校验 + 构造 + 入库，返回新账号 ID 与 error。
func (m *Manager) AddNvidiaAccount(in NvidiaAccountInput) (string, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultNvidiaBaseURL
	}
	if err := ValidateNvidiaAccountInput(in); err != nil {
		return "", err
	}
	acc := NewNvidiaAccount(in)
	if acc.DefaultModel == "" && acc.ModelSonnet == "" && acc.ModelOpus == "" && acc.ModelHaiku == "" && acc.ModelFable == "" {
		// 全部模型字段留空时给一个开箱即用的默认值
		acc.DefaultModel = DefaultNvidiaModel
	}
	m.AddAccount(acc)
	return acc.ID, nil
}

// UpdateNvidiaAccount 就地更新已有 nvidia 账号的可编辑字段(BaseURL/APIKey/展示名/各档位模型)。
// APIKey 留空表示保持不变(前端编辑时若不改 Key 则传空),故跳过 key 必填校验。
// 返回更新后的账号,账号不存在或非 nvidia 类型时报错。
func (m *Manager) UpdateNvidiaAccount(id string, in NvidiaAccountInput) (*Account, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultNvidiaBaseURL
	}
	if err := validateNvidiaFields(in, false); err != nil {
		return nil, err
	}

	m.Lock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id && a.Provider == "nvidia" {
			target = a
			break
		}
	}
	if target == nil {
		m.Unlock()
		return nil, errors.New("账号不存在或非 NVIDIA 类型")
	}

	label := strings.TrimSpace(in.Label)
	if label == "" {
		// 展示名留空时回退为 host
		if u, err := url.Parse(in.BaseURL); err == nil && u.Host != "" {
			label = u.Host
		} else {
			label = "NVIDIA 账号"
		}
	}
	target.BaseURL = in.BaseURL
	target.Email = label
	target.DefaultModel = strings.TrimSpace(in.DefaultModel)
	target.ModelSonnet = strings.TrimSpace(in.ModelSonnet)
	target.ModelOpus = strings.TrimSpace(in.ModelOpus)
	target.ModelHaiku = strings.TrimSpace(in.ModelHaiku)
	target.ModelFable = strings.TrimSpace(in.ModelFable)
	// APIKey 留空表示保持不变;否则覆盖 AccessToken(复用 SetAccessToken 走 token 锁)。
	if strings.TrimSpace(in.APIKey) != "" {
		target.SetAccessToken(strings.TrimSpace(in.APIKey))
	}

	// 先释放写锁再 SaveAccounts(内部会 RLock;写锁持有时不可再 RLock,否则自死锁)。
	m.Unlock()

	_ = m.SaveAccounts(true)

	if m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return target, nil
}

// IsNvidiaAvailable 供负载均衡调用的 nvidia 账号可用谓词。
// 与 GetAvailableAccountsForChannel("nvidia", ...) 的判定一致。
func IsNvidiaAvailable(a *Account) bool {
	return a != nil && a.Provider == "nvidia" && a.Enabled && a.AccessToken != "" && a.BaseURL != ""
}

// maskAPIKey 对 API Key 做掩码脱敏，仅保留前 4 / 后 4 位用于前端辨认。
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	// 只对明显的 API Key 做掩码；OAuth token 通常很长也一并掩码，保留首尾各 4
	if len(key) <= 10 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// ResolveNvidiaModel 按入站模型名档位解析成应发给上游的 NVIDIA 模型 id。
// 剥离 [1M] 后缀；命中档位取账号对应字段，缺省回退 DefaultModel，再缺省回退默认。
func ResolveNvidiaModel(inModel string, acc *Account) string {
	if acc == nil {
		return DefaultNvidiaModel
	}
	name := strings.TrimSpace(inModel)
	// 剥离 [1M] 等显式上下文窗口后缀
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "sonnet"):
		if acc.ModelSonnet != "" {
			return acc.ModelSonnet
		}
	case strings.Contains(lower, "opus"):
		if acc.ModelOpus != "" {
			return acc.ModelOpus
		}
	case strings.Contains(lower, "haiku"):
		if acc.ModelHaiku != "" {
			return acc.ModelHaiku
		}
	case strings.Contains(lower, "fable"):
		if acc.ModelFable != "" {
			return acc.ModelFable
		}
	}
	if strings.Contains(name, "/") {
		// 客户端显式指定了具名上游模型（如 meta/llama-3.3-70b-instruct），优先直接透传
		return name
	}
	if acc.DefaultModel != "" {
		return acc.DefaultModel
	}
	if name != "" {
		// 用户未配档位且非默认值时，透传原始模型名
		return name
	}
	return DefaultNvidiaModel
}

// MaskAPIKey 对 API Key 做掩码脱敏，供全工程及测试调用。
func MaskAPIKey(key string) string {
	return maskAPIKey(key)
}

// maskedKeyForAccount 生成账号的脱敏 Key(供 GetAccounts 深拷贝填充 maskedKey)。
// 仅对 API Key 型 provider(nvidia/other)填掩码;OAuth 型(antigravity/project)不填,
// 避免把长 OAuth token 也当 Key 展示。无 Key 或未知 provider 返回空。
func maskedKeyForAccount(a *Account) string {
	if a == nil {
		return ""
	}
	switch strings.ToLower(a.Provider) {
	case "nvidia", "other":
		return maskAPIKey(a.GetAccessToken())
	}
	return ""
}

// String 仅用于日志/调试，不参与序列化。
func (f NvidiaModelField) String() string { return string(f) }
