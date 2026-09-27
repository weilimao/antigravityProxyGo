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
//   - 上游 API 端点固定为 POST {BaseURL}/v2/chat/completions (默认 https://www.workbuddy.ai)。
//   - 认证使用 Bearer Token，从 WorkBuddy 客户端生成的 workbuddy-desktop-ai.info 中提取。
//   - 支持 0 积分免费调用模型：deepseek-v4.1-flash, hy4-preview-f, hy3。

const (
	workbuddyProvider = "workbuddy"
	workbuddyScope    = "workbuddy"

	DefaultWorkBuddyBaseURL         = "https://www.workbuddy.ai"
	DefaultWorkBuddyDomesticBaseURL = "https://copilot.tencent.com"
	DefaultWorkBuddyModel           = "deepseek-v4.1-flash"
)

// WorkBuddySupportedModels 列出 WorkBuddy 原生支持的免费模型集合。
var WorkBuddySupportedModels = []string{
	"deepseek-v4.1-flash",
	"hy4-preview-f",
	"hy3",
	"hunyuan-2.0-instruct",
	"glm-5.1",
	"minimax-m2.5",
	"gpt-5.5",
	"gemini-3.5-flash",
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
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
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
	if in.BaseURL == "" || strings.Contains(in.BaseURL, "codebuddy.ai") {
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
	if in.BaseURL == "" || strings.Contains(in.BaseURL, "codebuddy.ai") {
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

// GetWorkBuddyModelAffinity 返回模型的版本亲和性：
// - "domestic": 仅国内版（腾讯云网关）支持的专属模型（如腾讯混元、国产接入大模型、腾讯特有别名）；
// - "international": 仅国际版（海外网关）支持的专属模型（如 GPT-5、Gemini、海外特有别名）；
// - "any": 双版本均支持或通用的模型（如 deepseek-v4.1-flash, deepseek-v4-flash, hy3, auto 等）。
func GetWorkBuddyModelAffinity(modelName string) string {
	m := strings.ToLower(strings.TrimSpace(modelName))
	if i := strings.Index(m, "["); i > 0 {
		m = strings.TrimSpace(m[:i])
	}

	// 1. 显式前缀判定
	if strings.HasPrefix(m, "workbuddy/domestic/") || strings.HasPrefix(m, "workbuddy/cn/") {
		return "domestic"
	}
	if strings.HasPrefix(m, "workbuddy/intl/") || strings.HasPrefix(m, "workbuddy/overseas/") {
		return "international"
	}
	if strings.HasPrefix(m, "workbuddy/") {
		m = strings.TrimPrefix(m, "workbuddy/")
	}

	// 2. 国内专属模型前缀与特征
	if strings.HasPrefix(m, "hunyuan") ||
		strings.HasPrefix(m, "default-1.") ||
		strings.HasPrefix(m, "codewise") ||
		strings.HasPrefix(m, "minimax") ||
		strings.HasPrefix(m, "glm-4") ||
		strings.HasPrefix(m, "glm-5.0") ||
		strings.HasPrefix(m, "glm-5.1") ||
		strings.HasPrefix(m, "deepseek-v3") ||
		strings.HasPrefix(m, "deepseek-r1") {
		return "domestic"
	}

	// 3. 国际专属模型前缀与特征
	if strings.HasPrefix(m, "gpt-") ||
		strings.HasPrefix(m, "gemini-") ||
		strings.HasPrefix(m, "kimi-k3") ||
		strings.HasPrefix(m, "kimi-k2.8") ||
		strings.HasPrefix(m, "hy4-preview") ||
		m == "fast-model" ||
		m == "balanced-model" ||
		m == "primary-model" ||
		m == "deep-model" {
		return "international"
	}

	// 4. 双版本通用模型 (deepseek-v4.1-flash, deepseek-v4-flash, hy3, auto, default-model, kimi-k2.6 等)
	return "any"
}

// ResolveWorkBuddyModel 解析入站模型名，自动剥离渠道与版本前缀。
func ResolveWorkBuddyModel(inModel string, acc *Account) string {
	name := strings.TrimSpace(inModel)
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	for _, pfx := range []string{
		"workbuddy/domestic/",
		"workbuddy/cn/",
		"workbuddy/intl/",
		"workbuddy/overseas/",
		"workbuddy/",
	} {
		if strings.HasPrefix(strings.ToLower(name), pfx) {
			name = name[len(pfx):]
			break
		}
	}
	if name != "" {
		return name
	}
	if acc != nil && strings.TrimSpace(acc.DefaultModel) != "" {
		return strings.TrimSpace(acc.DefaultModel)
	}
	return DefaultWorkBuddyModel
}

// ScanAllWorkBuddyLocalInfoPaths 扫描本机所有的 WorkBuddy / CodeBuddy 本地凭证文件路径（按修改时间倒序）。
// 兼容国内版 (workbuddy-desktop*.info) 与国际版 (workbuddy-desktop-ai*.info)。
func ScanAllWorkBuddyLocalInfoPaths() []string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil
	}
	authDir := filepath.Join(localAppData, "CodeBuddyExtension", "Data", "Public", "auth")
	pattern := filepath.Join(authDir, "workbuddy-desktop*.info")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}

	type fileItem struct {
		path    string
		modTime time.Time
	}
	var items []fileItem
	for _, f := range matches {
		if strings.HasSuffix(f, ".logged-out") {
			continue
		}
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			items = append(items, fileItem{path: f, modTime: fi.ModTime()})
		}
	}

	// 按修改时间倒序排序
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].modTime.After(items[i].modTime) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	var res []string
	for _, it := range items {
		res = append(res, it.path)
	}
	return res
}

// GetDefaultWorkBuddyLocalInfoPath 返回本机 WorkBuddy 凭证存储文件的路径。
// 优先返回最新修改的有效 .info 凭证文件；若无则返回兜底默认路径。
func GetDefaultWorkBuddyLocalInfoPath() string {
	paths := ScanAllWorkBuddyLocalInfoPaths()
	if len(paths) > 0 {
		return paths[0]
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info")
}

// workBuddyAuthFileSchema 对应 workbuddy-desktop*.info 的 JSON 结构。
type workBuddyAuthFileSchema struct {
	Account struct {
		UID         string `json:"uid"`
		Nickname    string `json:"nickname"`
		PhoneNumber string `json:"phoneNumber"`
	} `json:"account"`
	Auth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	} `json:"auth"`
}

// ParseWorkBuddyAuthInfo 从 JSON 字节流中解析 WorkBuddy 凭证。
// 能够根据 auth.domain（如包含 workbuddy.cn）或特定标识自动推断国内版网关 (copilot.tencent.com)。
func ParseWorkBuddyAuthInfo(content []byte) (*WorkBuddyAccountInput, error) {
	var info workBuddyAuthFileSchema
	if err := json.Unmarshal(content, &info); err != nil {
		return nil, fmt.Errorf("解析 WorkBuddy 凭证 JSON 失败: %w", err)
	}

	token := strings.TrimSpace(info.Auth.AccessToken)
	if token == "" {
		return nil, errors.New("WorkBuddy 凭证文件中未找到有效 accessToken")
	}

	baseURL := DefaultWorkBuddyBaseURL
	domain := strings.ToLower(strings.TrimSpace(info.Auth.Domain))
	isDomestic := strings.Contains(domain, "workbuddy.cn") || strings.Contains(domain, "tencent.com")
	if isDomestic {
		baseURL = DefaultWorkBuddyDomesticBaseURL
	}

	nickname := strings.TrimSpace(info.Account.Nickname)
	label := nickname
	if label == "" {
		if info.Account.PhoneNumber != "" {
			label = info.Account.PhoneNumber
		} else if info.Account.UID != "" {
			label = "WB-" + info.Account.UID
		} else {
			label = "WorkBuddy 账号"
		}
	}
	if isDomestic && !strings.Contains(label, "国内版") {
		label = fmt.Sprintf("%s (国内版)", label)
	}

	return &WorkBuddyAccountInput{
		BaseURL:      baseURL,
		AccessToken:  token,
		RefreshToken: strings.TrimSpace(info.Auth.RefreshToken),
		UID:          strings.TrimSpace(info.Account.UID),
		Nickname:     nickname,
		Label:        label,
		DefaultModel: DefaultWorkBuddyModel,
	}, nil
}

// BuildWorkBuddyHeaders 根据账号版本（国内版 vs 国际版）构造上游请求所需的请求头集合。
func BuildWorkBuddyHeaders(acc *Account) map[string]string {
	headers := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
	if acc == nil {
		return headers
	}
	if tok := acc.GetAccessToken(); tok != "" {
		headers["Authorization"] = "Bearer " + tok
	}
	if acc.ProjectID != "" {
		headers["X-User-Id"] = acc.ProjectID
	}

	if acc.IsWorkBuddyDomestic() {
		headers["X-IDE-Type"] = "WorkBuddy"
		headers["X-IDE-Name"] = "WorkBuddy"
		headers["X-IDE-Version"] = "5.5.6"
		headers["X-Product"] = "WorkBuddy"
		headers["User-Agent"] = "WorkBuddy/5.5.6"
		headers["X-Domain"] = "www.workbuddy.cn"
	} else {
		headers["X-IDE-Type"] = "CodeBuddy"
		headers["X-IDE-Name"] = "WorkBuddy"
		headers["X-IDE-Version"] = "5.5.2"
		headers["X-Product"] = "WorkBuddy"
		headers["User-Agent"] = "WorkBuddy/5.5.2"
	}
	return headers
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

// ImportAllWorkBuddyLocalAccounts 扫描本机所有有效的 WorkBuddy 凭证并批量导入/更新。
func (m *Manager) ImportAllWorkBuddyLocalAccounts() ([]*Account, error) {
	paths := ScanAllWorkBuddyLocalInfoPaths()
	if len(paths) == 0 {
		return nil, errors.New("未检测到任何本地 WorkBuddy 登录凭证文件")
	}

	var imported []*Account
	seenUIDs := make(map[string]bool)
	for _, p := range paths {
		acc, err := m.ImportWorkBuddyLocalAccount(p)
		if err == nil && acc != nil {
			key := acc.ProjectID
			if key == "" {
				key = acc.ID
			}
			if !seenUIDs[key] {
				seenUIDs[key] = true
				imported = append(imported, acc)
			}
		}
	}
	return imported, nil
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

