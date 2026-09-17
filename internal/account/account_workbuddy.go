package account

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// account_workbuddy.go: 腾讯 WorkBuddy 免费模型号池的账号构造、校验、本地导入与模型解析。
//
// 设计:
//   - Provider 固定 "workbuddy", ScopeType 固定 "workbuddy"。
//   - 上游 API 端点固定为 POST {BaseURL}/v2/chat/completions (默认 https://www.codebuddy.ai)。
//   - 认证使用 Bearer Token，从 WorkBuddy 客户端生成的 workbuddy-desktop-ai.info 中提取。
//   - 支持 0 积分免费调用模型：deepseek-v4.1-flash, hy4-preview-f, hy3。

const (
	workbuddyProvider = "workbuddy"
	workbuddyScope    = "workbuddy"

	DefaultWorkBuddyBaseURL = "https://www.codebuddy.ai"
	DefaultWorkBuddyModel   = "deepseek-v4.1-flash"
)

// WorkBuddySupportedModels 列出 WorkBuddy 原生支持的免费模型集合。
var WorkBuddySupportedModels = []string{
	"deepseek-v4.1-flash",
	"hy4-preview-f",
	"hy3",
}

// WorkBuddyAccountInput 是从前端/IPC 接收的 WorkBuddy 账号录入参数。
type WorkBuddyAccountInput struct {
	BaseURL      string `json:"baseUrl"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	UID          string `json:"uid"`
	Nickname     string `json:"nickname"`
	Label        string `json:"label"`
	DefaultModel string `json:"defaultModel"`
}

// ValidateWorkBuddyAccountInput 校验新增 WorkBuddy 账号参数。
func ValidateWorkBuddyAccountInput(in WorkBuddyAccountInput) error {
	return validateWorkBuddyFields(in, true)
}

func validateWorkBuddyFields(in WorkBuddyAccountInput, requireKey bool) error {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultWorkBuddyBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("base_url 必须是合法的 http/https 地址")
	}
	if requireKey && strings.TrimSpace(in.AccessToken) == "" {
		return errors.New("access_token 不能为空")
	}
	return nil
}

// NewWorkBuddyAccount 根据 input 构造未入库的 WorkBuddy Account。
func NewWorkBuddyAccount(in WorkBuddyAccountInput) *Account {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		baseURL = DefaultWorkBuddyBaseURL
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		if strings.TrimSpace(in.Nickname) != "" {
			label = strings.TrimSpace(in.Nickname)
		} else if strings.TrimSpace(in.UID) != "" {
			label = "WB-" + strings.TrimSpace(in.UID)
		} else {
			label = "WorkBuddy 账号"
		}
	}
	defModel := strings.TrimSpace(in.DefaultModel)
	if defModel == "" {
		defModel = DefaultWorkBuddyModel
	}

	return &Account{
		Email:        label,
		Provider:     workbuddyProvider,
		ScopeType:    workbuddyScope,
		AccessToken:  strings.TrimSpace(in.AccessToken),
		RefreshToken: strings.TrimSpace(in.RefreshToken),
		ProjectID:    strings.TrimSpace(in.UID),
		BaseURL:      baseURL,
		DefaultModel: defModel,
		Enabled:      true,
		AddedAt:      time.Now().Format(time.RFC3339),
		Cooldowns:    make(map[string]int64),
	}
}

// AddWorkBuddyAccount 校验 + 构造 + 入库，返回新账号 ID。
func (m *Manager) AddWorkBuddyAccount(in WorkBuddyAccountInput) (string, error) {
	if in.BaseURL == "" {
		in.BaseURL = DefaultWorkBuddyBaseURL
	}
	if err := ValidateWorkBuddyAccountInput(in); err != nil {
		return "", err
	}
	acc := NewWorkBuddyAccount(in)
	m.AddAccount(acc)
	return acc.ID, nil
}

// UpdateWorkBuddyAccount 就地更新已有 WorkBuddy 账号字段。
func (m *Manager) UpdateWorkBuddyAccount(id string, in WorkBuddyAccountInput) (*Account, error) {
	if in.BaseURL == "" {
		in.BaseURL = DefaultWorkBuddyBaseURL
	}
	if err := validateWorkBuddyFields(in, false); err != nil {
		return nil, err
	}

	m.Lock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id && a.Provider == workbuddyProvider {
			target = a
			break
		}
	}
	if target == nil {
		m.Unlock()
		return nil, errors.New("账号不存在或非 WorkBuddy 类型")
	}

	label := strings.TrimSpace(in.Label)
	if label != "" {
		target.Email = label
	} else if strings.TrimSpace(in.Nickname) != "" {
		target.Email = strings.TrimSpace(in.Nickname)
	}

	target.BaseURL = in.BaseURL
	if strings.TrimSpace(in.DefaultModel) != "" {
		target.DefaultModel = strings.TrimSpace(in.DefaultModel)
	}
	if strings.TrimSpace(in.AccessToken) != "" {
		target.SetAccessToken(strings.TrimSpace(in.AccessToken))
	}
	if strings.TrimSpace(in.RefreshToken) != "" {
		target.SetRefreshToken(strings.TrimSpace(in.RefreshToken))
	}
	if strings.TrimSpace(in.UID) != "" {
		target.ProjectID = strings.TrimSpace(in.UID)
	}

	m.Unlock()

	_ = m.SaveAccountsFor(true, workbuddyProvider)

	if m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return target, nil
}

// IsWorkBuddyAvailable 判定 WorkBuddy 账号是否处于可用状态。
func IsWorkBuddyAvailable(a *Account) bool {
	return a != nil && a.Provider == workbuddyProvider && a.Enabled && a.AccessToken != "" && a.BaseURL != ""
}

// ResolveWorkBuddyModel 解析入站模型名。
func ResolveWorkBuddyModel(inModel string, acc *Account) string {
	name := strings.TrimSpace(inModel)
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	if strings.HasPrefix(name, "workbuddy/") {
		name = strings.TrimPrefix(name, "workbuddy/")
	}
	if name != "" {
		return name
	}
	if acc != nil && strings.TrimSpace(acc.DefaultModel) != "" {
		return strings.TrimSpace(acc.DefaultModel)
	}
	return DefaultWorkBuddyModel
}

// GetDefaultWorkBuddyLocalInfoPath 返回本机 WorkBuddy 凭证存储文件的路径。
// 优先返回固定的 workbuddy-desktop-ai.info；若不存在，则动态扫描该目录中最新的 workbuddy-desktop-ai*.info 文件。
func GetDefaultWorkBuddyLocalInfoPath() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	authDir := filepath.Join(localAppData, "CodeBuddyExtension", "Data", "Public", "auth")
	defaultPath := filepath.Join(authDir, "workbuddy-desktop-ai.info")
	if fi, err := os.Stat(defaultPath); err == nil && !fi.IsDir() {
		return defaultPath
	}

	// 动态扫描最新修改的 .info 凭证文件（排除 .logged-out 等退出状态文件）
	pattern := filepath.Join(authDir, "workbuddy-desktop-ai*.info")
	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		var latestFile string
		var latestModTime time.Time
		for _, file := range matches {
			if strings.HasSuffix(file, ".logged-out") {
				continue
			}
			if fi, err := os.Stat(file); err == nil && !fi.IsDir() {
				if fi.ModTime().After(latestModTime) || (fi.ModTime().Equal(latestModTime) && file > latestFile) {
					latestModTime = fi.ModTime()
					latestFile = file
				}
			}
		}
		if latestFile != "" {
			return latestFile
		}
	}

	return defaultPath
}

// workBuddyAuthFileSchema 对应 workbuddy-desktop-ai.info 的 JSON 结构。
type workBuddyAuthFileSchema struct {
	Account struct {
		UID      string `json:"uid"`
		Nickname string `json:"nickname"`
	} `json:"account"`
	Auth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
	} `json:"auth"`
}

// ParseWorkBuddyAuthInfo 从 JSON 字节流中解析 WorkBuddy 凭证。
func ParseWorkBuddyAuthInfo(content []byte) (*WorkBuddyAccountInput, error) {
	var info workBuddyAuthFileSchema
	if err := json.Unmarshal(content, &info); err != nil {
		return nil, fmt.Errorf("解析 WorkBuddy 凭证 JSON 失败: %w", err)
	}

	token := strings.TrimSpace(info.Auth.AccessToken)
	if token == "" {
		return nil, errors.New("WorkBuddy 凭证文件中未找到有效 accessToken")
	}

	return &WorkBuddyAccountInput{
		BaseURL:      DefaultWorkBuddyBaseURL,
		AccessToken:  token,
		RefreshToken: strings.TrimSpace(info.Auth.RefreshToken),
		UID:          strings.TrimSpace(info.Account.UID),
		Nickname:     strings.TrimSpace(info.Account.Nickname),
		Label:        strings.TrimSpace(info.Account.Nickname),
		DefaultModel: DefaultWorkBuddyModel,
	}, nil
}

// ImportWorkBuddyLocalAccount 自动探测并一键导入本机当前登录的 WorkBuddy 账号。
func (m *Manager) ImportWorkBuddyLocalAccount(customPath ...string) (*Account, error) {
	path := ""
	if len(customPath) > 0 && strings.TrimSpace(customPath[0]) != "" {
		path = customPath[0]
	} else {
		path = GetDefaultWorkBuddyLocalInfoPath()
	}

	if path == "" {
		return nil, errors.New("无法定位本地 LOCALAPPDATA 环境变量")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取本地 WorkBuddy 凭证文件失败 (%s): %w", path, err)
	}

	input, err := ParseWorkBuddyAuthInfo(data)
	if err != nil {
		return nil, err
	}

	// 检查是否已有同 UID 或同 Token 的账号，有则就地更新，无则新增
	m.RLock()
	var existingID string
	for _, a := range m.accounts {
		if a.Provider == workbuddyProvider {
			if (input.UID != "" && a.ProjectID == input.UID) || a.AccessToken == input.AccessToken {
				existingID = a.ID
				break
			}
		}
	}
	m.RUnlock()

	if existingID != "" {
		return m.UpdateWorkBuddyAccount(existingID, *input)
	}

	id, err := m.AddWorkBuddyAccount(*input)
	if err != nil {
		return nil, err
	}

	return m.GetAccountByID(id), nil
}

// IsDomesticEmail 判定邮箱是否属于国内注册邮箱（QQ/163/126/Sina/Aliyun 等主流国内服务商或 .cn 后缀）
func IsDomesticEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	domain := parts[1]
	if strings.HasSuffix(domain, ".cn") {
		return true
	}
	domesticDomains := map[string]bool{
		"qq.com":      true,
		"vip.qq.com":  true,
		"foxmail.com": true,
		"163.com":     true,
		"126.com":     true,
		"yeah.net":    true,
		"sina.com":    true,
		"sina.cn":     true,
		"sohu.com":    true,
		"aliyun.com":  true,
		"139.com":     true,
		"189.com":     true,
		"wo.cn":       true,
		"tom.com":     true,
	}
	return domesticDomains[domain]
}

// GetWorkBuddyEffectiveEmail 获取账号的有效注册邮箱。优先使用 Email 字段，若非邮箱则从 AccessToken(JWT) 解析 email claim。
func GetWorkBuddyEffectiveEmail(acc *Account) string {
	if acc == nil {
		return ""
	}
	if strings.Contains(acc.Email, "@") {
		return strings.TrimSpace(acc.Email)
	}
	token := acc.GetAccessToken()
	parts := strings.Split(token, ".")
	if len(parts) >= 2 {
		payload := parts[1]
		payload += strings.Repeat("=", (4-len(payload)%4)%4)
		if raw, err := base64.URLEncoding.DecodeString(payload); err == nil {
			var claims map[string]any
			if err := json.Unmarshal(raw, &claims); err == nil {
				if email, ok := claims["email"].(string); ok && strings.Contains(email, "@") {
					return strings.TrimSpace(email)
				}
			}
		}
	}
	return acc.Email
}

// IsWorkBuddyDomesticAccount 判定 WorkBuddy 账号是否是由国内邮箱注册的账号。
// 国内邮箱账号在腾讯云官方缺少 CAM 海外计量策略，因此不请求也不显示积分配额。
func IsWorkBuddyDomesticAccount(acc *Account) bool {
	if acc == nil || acc.Provider != workbuddyProvider {
		return false
	}
	if acc.NoQuota {
		return true
	}
	effectiveEmail := GetWorkBuddyEffectiveEmail(acc)
	return IsDomesticEmail(effectiveEmail)
}

