package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// account_opencode.go: OpenCode Zen 聚合网关号池的账号构造、校验、本地导入与模型解析。
//
// 设计:
//   - Provider 固定 "opencode", ScopeType 固定 "opencode"。
//   - 上游 API 端点固定为 POST {BaseURL}/chat/completions (默认 https://opencode.ai/zen/v1)。
//   - 认证使用 Bearer Token，格式形如 sk-...，由 https://opencode.ai/zen 签发。
//   - 支持一键从本地 ~/.local/share/opencode/auth.json 读取凭证。

const (
	opencodeProvider = "opencode"
	opencodeScope    = "opencode"

	DefaultOpenCodeBaseURL = "https://opencode.ai/zen/v1"
	DefaultOpenCodeModel   = "claude-sonnet-4-6"
)

// OpenCodeSupportedModels 列出 OpenCode Zen 原生支持的核心主流模型。
var OpenCodeSupportedModels = []string{
	"claude-sonnet-4-6",
	"gpt-5",
	"gpt-5-codex",
	"gpt-5.1-codex",
	"gpt-5.1-codex-mini",
	"gpt-5-nano",
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	"glm-5",
	"glm-5.3",
	"minimax-m3",
	"kimi-k3",
	"qwen3.6-plus",
	"big-pickle",
	"deepseek-v4-flash-free",
	"mimo-v2.5-free",
	"nemotron-3-ultra-free",
}

// OpenCodeAccountInput 是从前端/IPC 接收的 OpenCode 账号录入参数。
type OpenCodeAccountInput struct {
	BaseURL      string `json:"baseUrl"`
	AccessToken  string `json:"accessToken"`
	Label        string `json:"label"`
	DefaultModel string `json:"defaultModel"`
}

// ValidateOpenCodeAccountInput 校验新增 OpenCode 账号参数。
func ValidateOpenCodeAccountInput(in OpenCodeAccountInput) error {
	return validateOpenCodeFields(in, true)
}

func validateOpenCodeFields(in OpenCodeAccountInput, requireKey bool) error {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultOpenCodeBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("base_url 必须是合法的 http/https 地址")
	}
	if requireKey && strings.TrimSpace(in.AccessToken) == "" {
		return errors.New("access_token (API Key) 不能为空")
	}
	return nil
}

// NewOpenCodeAccount 根据 input 构造未入库的 OpenCode Account。
func NewOpenCodeAccount(in OpenCodeAccountInput) *Account {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultOpenCodeBaseURL
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		key := strings.TrimSpace(in.AccessToken)
		if len(key) > 8 {
			label = "OpenCode-" + key[len(key)-6:]
		} else {
			label = "OpenCode 账号"
		}
	}
	defModel := strings.TrimSpace(in.DefaultModel)
	if defModel == "" {
		defModel = DefaultOpenCodeModel
	}

	return &Account{
		Email:        label,
		Provider:     opencodeProvider,
		ScopeType:    opencodeScope,
		AccessToken:  strings.TrimSpace(in.AccessToken),
		BaseURL:      baseURL,
		DefaultModel: defModel,
		Enabled:      true,
		AddedAt:      time.Now().Format(time.RFC3339),
		Cooldowns:    make(map[string]int64),
	}
}

// AddOpenCodeAccount 校验 + 构造 + 入库，返回新账号 ID。
func (m *Manager) AddOpenCodeAccount(in OpenCodeAccountInput) (string, error) {
	if in.BaseURL == "" {
		in.BaseURL = DefaultOpenCodeBaseURL
	}
	if err := ValidateOpenCodeAccountInput(in); err != nil {
		return "", err
	}
	acc := NewOpenCodeAccount(in)
	m.AddAccount(acc)
	return acc.ID, nil
}

// UpdateOpenCodeAccount 就地更新已有 OpenCode 账号字段。
func (m *Manager) UpdateOpenCodeAccount(id string, in OpenCodeAccountInput) (*Account, error) {
	if in.BaseURL == "" {
		in.BaseURL = DefaultOpenCodeBaseURL
	}
	if err := validateOpenCodeFields(in, false); err != nil {
		return nil, err
	}

	m.Lock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id && a.Provider == opencodeProvider {
			target = a
			break
		}
	}
	if target == nil {
		m.Unlock()
		return nil, errors.New("账号不存在或非 OpenCode 类型")
	}

	label := strings.TrimSpace(in.Label)
	if label != "" {
		target.Email = label
	}

	target.BaseURL = in.BaseURL
	if strings.TrimSpace(in.DefaultModel) != "" {
		target.DefaultModel = strings.TrimSpace(in.DefaultModel)
	}
	if strings.TrimSpace(in.AccessToken) != "" {
		target.SetAccessToken(strings.TrimSpace(in.AccessToken))
	}

	m.Unlock()

	_ = m.SaveAccountsFor(true, opencodeProvider)

	if m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return target, nil
}

// IsOpenCodeAvailable 判定 OpenCode 账号是否处于可用状态。
func IsOpenCodeAvailable(a *Account) bool {
	return a != nil && a.Provider == opencodeProvider && a.Enabled && a.AccessToken != "" && a.BaseURL != ""
}

// ResolveOpenCodeModel 解析入站模型名。
func ResolveOpenCodeModel(inModel string, acc *Account) string {
	name := strings.TrimSpace(inModel)
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	if strings.HasPrefix(name, "opencode/") {
		name = strings.TrimPrefix(name, "opencode/")
	}
	if strings.HasPrefix(name, "oc/") {
		name = strings.TrimPrefix(name, "oc/")
	}
	if name != "" {
		return name
	}
	if acc != nil && strings.TrimSpace(acc.DefaultModel) != "" {
		return strings.TrimSpace(acc.DefaultModel)
	}
	return DefaultOpenCodeModel
}

// GetDefaultOpenCodeAuthPath 返回本机 OpenCode 凭证存储文件的标准路径。
func GetDefaultOpenCodeAuthPath() string {
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		path := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")
		if fi, sErr := os.Stat(path); sErr == nil && !fi.IsDir() {
			return path
		}
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		path := filepath.Join(localAppData, "opencode", "auth.json")
		if fi, sErr := os.Stat(path); sErr == nil && !fi.IsDir() {
			return path
		}
	}
	if homeDir != "" {
		return filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")
	}
	return ""
}

// ParseOpenCodeAuthInfo 从 auth.json 字节流中解析 OpenCode Zen API Key 凭证。
func ParseOpenCodeAuthInfo(content []byte) (*OpenCodeAccountInput, error) {
	var rawMap map[string]interface{}
	if err := json.Unmarshal(content, &rawMap); err != nil {
		return nil, fmt.Errorf("解析 OpenCode 凭证 JSON 失败: %w", err)
	}

	extractKey := func(val interface{}) string {
		if val == nil {
			return ""
		}
		if s, ok := val.(string); ok {
			return strings.TrimSpace(s)
		}
		if m, ok := val.(map[string]interface{}); ok {
			if k, ok := m["key"].(string); ok && strings.TrimSpace(k) != "" {
				return strings.TrimSpace(k)
			}
			if k, ok := m["accessToken"].(string); ok && strings.TrimSpace(k) != "" {
				return strings.TrimSpace(k)
			}
			if k, ok := m["token"].(string); ok && strings.TrimSpace(k) != "" {
				return strings.TrimSpace(k)
			}
		}
		return ""
	}

	key := extractKey(rawMap["opencode"])
	if key == "" {
		key = extractKey(rawMap["zen"])
	}
	if key == "" {
		key = extractKey(rawMap["key"])
	}
	if key == "" {
		key = extractKey(rawMap["accessToken"])
	}

	if key == "" {
		return nil, errors.New("OpenCode 凭证文件中未找到有效 opencode / zen API Key")
	}

	label := "OpenCode-Local"
	if len(key) > 8 {
		label = fmt.Sprintf("OpenCode-%s", key[len(key)-6:])
	}

	return &OpenCodeAccountInput{
		BaseURL:      DefaultOpenCodeBaseURL,
		AccessToken:  key,
		Label:        label,
		DefaultModel: DefaultOpenCodeModel,
	}, nil
}

// ImportOpenCodeLocalAccount 自动探测并一键导入本机当前配置的 OpenCode Zen API Key 账号。
func (m *Manager) ImportOpenCodeLocalAccount(customPath ...string) (*Account, error) {
	path := ""
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		path = customPath[0]
	} else {
		path = GetDefaultOpenCodeAuthPath()
	}

	if path == "" {
		return nil, errors.New("无法定位本地 OpenCode 凭证存储路径")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取本地 OpenCode 凭证文件失败 (%s): %w", path, err)
	}

	input, err := ParseOpenCodeAuthInfo(data)
	if err != nil {
		return nil, err
	}

	m.RLock()
	var existingID string
	for _, a := range m.accounts {
		if a.Provider == opencodeProvider && a.AccessToken == input.AccessToken {
			existingID = a.ID
			break
		}
	}
	m.RUnlock()

	if existingID != "" {
		return m.UpdateOpenCodeAccount(existingID, *input)
	}

	id, err := m.AddOpenCodeAccount(*input)
	if err != nil {
		return nil, err
	}

	return m.GetAccountByID(id), nil
}
