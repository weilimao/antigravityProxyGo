package account

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// account_grok.go: Grok(x.ai) 号池的账号构造、校验、录入与档位解析。
//
// 设计:Grok 号池是「一批带 API Key 的 xAI OpenAI Chat 兼容上游账号」的容器,
// 上游固定走 chat/completions 协议端点(POST {BaseURL}/v1/chat/completions),
// 思考字段为官方顶层 reasoning_effort(取值 none/low/medium/high,由 grok_thinking.go 注入,
// 与 NVIDIA NIM 的 chat_template_kwargs / Other 池的顶层 reasoning_effort 省略语义均不同:
// Grok 要「显式传 none」表达关闭,绝不归一空串省略)。
//
// Provider 约定:Provider 固定 "grok",ScopeType 固定 "grok"。
// API Key 复用 AccessToken 字段(与 NVIDIA/Other 同源),BaseURL 复用现有字段(默认 https://api.x.ai/v1)。
// 与 NVIDIA 同构:单账号一套档位(sonnet/opus/haiku/fable/default),供前端录入与档位透传。

// grokProvider / grokScope 是 Grok 号池账号的固定 Provider/ScopeType 标识。
const (
	grokProvider = "grok"
	grokScope    = "grok"
)

// Grok 默认上游与默认档位回退值,xAI 官方 API Key 路径开箱即用。
const (
	DefaultGrokModel   = "grok-4.3"
	DefaultGrokBaseURL = "https://api.x.ai/v1"
	// DefaultGrokCliVersion 是 Grok 号池全局 CLI 客户端版本号的默认值, 用于发往 cli-chat-proxy.grok.com
	// 上游的 x-grok-client-version 身份头(规避 426 "Grok CLI version (none) outdated. Please update to
	// version 0.1.202 or later" 版本闸门)。上游下限 0.1.202, 1.0.0 形式上更高可放行。
	// 用户可在 Grok 号池「负载均衡」区配置覆盖。对齐 CLIProxyAPI2/internal/runtime/executor/xai_executor.go:69
	// 的 xaiClientVersionValue 常量(该项目硬编码 0.2.93; 本项目号池全局可配, 默认 1.0.0)。
	DefaultGrokCliVersion = "1.0.0"
)

// GrokModelField 是账号级四档位映射字段名(与 NVIDIA 同源,供前端录入与 ResolveGrokModel 解析)。
type GrokModelField string

const (
	GrokModelSonnet  GrokModelField = "sonnet"
	GrokModelOpus    GrokModelField = "opus"
	GrokModelHaiku   GrokModelField = "haiku"
	GrokModelFable   GrokModelField = "fable"
	GrokModelDefault GrokModelField = "default"
)

// String 仅用于日志/调试,不参与序列化。
func (f GrokModelField) String() string { return string(f) }

// GrokAccountInput 是从前端/IPC 接收的 Grok 账号录入参数(与 NvidiaAccountInput 同构)。
type GrokAccountInput struct {
	BaseURL      string
	APIKey       string
	Label        string // 可选展示名(写入 Email 字段)
	DefaultModel string
	ModelSonnet  string
	ModelOpus    string
	ModelHaiku   string
	ModelFable   string
}

// ValidateGrokAccountInput 校验 Grok 账号录入参数(新增表单,key 必填)。
func ValidateGrokAccountInput(in GrokAccountInput) error {
	return validateGrokFields(in, true)
}

// validateGrokFields 校验 Grok 账号字段。requireKey 为 false 时允许 APIKey 留空
// (编辑态留空表示保持原 Key 不变,与 NVIDIA 同口径)。
// 安全红线:BaseURL 必须 http/https;新增态要求 APIKey 非空。
func validateGrokFields(in GrokAccountInput, requireKey bool) error {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultGrokBaseURL
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("base_url 必须是合法的 http/https 地址")
	}
	if requireKey && strings.TrimSpace(in.APIKey) == "" {
		return errors.New("api_key 不能为空")
	}
	return nil
}

// NewGrokAccount 根据 input 构造一个未入库的 Grok Account。
// 不会触碰 Manager 全局锁,调用方负责 AddAccount 落库。
func NewGrokAccount(in GrokAccountInput) *Account {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultGrokBaseURL
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		// 用 base_url 的 host 当展示名,便于在号池中辨认(与 NVIDIA 同范式)。
		if u, err := url.Parse(baseURL); err == nil {
			label = u.Host
		} else {
			label = "Grok 账号"
		}
	}
	return &Account{
		Email:        label,
		Provider:     grokProvider,
		ScopeType:    grokScope,
		AccessToken:  strings.TrimSpace(in.APIKey), // 复用 AccessToken 存 API Key,与 NVIDIA/Other 同源
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

// AddGrokAccount 校验 + 构造 + 入库,返回新账号 ID 与 error。
// 全部模型字段留空时给一个开箱即用的默认档位(与 NVIDIA 同口径)。
func (m *Manager) AddGrokAccount(in GrokAccountInput) (string, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultGrokBaseURL
	}
	if err := ValidateGrokAccountInput(in); err != nil {
		return "", err
	}
	acc := NewGrokAccount(in)
	if acc.DefaultModel == "" && acc.ModelSonnet == "" && acc.ModelOpus == "" && acc.ModelHaiku == "" && acc.ModelFable == "" {
		// 全部模型字段留空时给一个开箱即用的默认值
		acc.DefaultModel = DefaultGrokModel
	}
	m.AddAccount(acc)
	return acc.ID, nil
}

// UpdateGrokAccount 就地更新已有 Grok 账号的可编辑字段(BaseURL/APIKey/展示名/各档位模型)。
// APIKey 留空表示保持不变(前端编辑时若不改 Key 则传空),故跳过 key 必填校验。
// 返回更新后的账号,账号不存在或非 grok 类型时报错。
func (m *Manager) UpdateGrokAccount(id string, in GrokAccountInput) (*Account, error) {
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		in.BaseURL = DefaultGrokBaseURL
	}
	if err := validateGrokFields(in, false); err != nil {
		return nil, err
	}

	m.Lock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id && a.Provider == grokProvider {
			target = a
			break
		}
	}
	if target == nil {
		m.Unlock()
		return nil, errors.New("账号不存在或非 Grok 类型")
	}

	label := strings.TrimSpace(in.Label)
	if label == "" {
		// 展示名留空时回退为 host(与 NVIDIA 同口径)。
		if u, err := url.Parse(in.BaseURL); err == nil && u.Host != "" {
			label = u.Host
		} else {
			label = "Grok 账号"
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

// IsGrokAvailable 供负载均衡调用的 Grok 账号可用谓词。
// 与 GetAvailableAccountsForChannel("grok", ...) 的判定口径一致(与 IsNvidiaAvailable 同构)。
func IsGrokAvailable(a *Account) bool {
	return a != nil && a.Provider == grokProvider && a.Enabled && a.AccessToken != "" && a.BaseURL != ""
}

// ResolveGrokModel 按入站模型名档位解析成应发给上游的 Grok 模型 id。
// 语义与 ResolveNvidiaModel 完全对齐:剥离 [1M] 后缀;命中档位取账号对应字段,
// 缺省回退 DefaultModel,再缺省回退默认;客户端显式具名上游模型(含 /)优先透传。
func ResolveGrokModel(inModel string, acc *Account) string {
	if acc == nil {
		return DefaultGrokModel
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
		// 客户端显式指定了具名上游模型,优先直接透传
		return name
	}
	if acc.DefaultModel != "" {
		return acc.DefaultModel
	}
	if name != "" {
		// 用户未配档位且非默认值时,透传原始模型名
		return name
	}
	return DefaultGrokModel
}
