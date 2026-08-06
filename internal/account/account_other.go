package account

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// account_other.go: Other 号池(自定义多上游组)的账号构造、校验、录入与组级管理。
//
// 设计:Other 号池是「一批自定义 OpenAI/Anthropic 兼容上游」的容器,内部按 GroupID 细分多个上游组
// (如 deepseek 组、openai 组、第三方中继组),每组可挂多个账号(BaseURL 相同、多 Key)做组内轮换。
// 与 NVIDIA 号池(NvidiaAccountInput 单账号一套档位)不同,Other 号池的维度是「组」而非「档位」,
// 故本文件提供组级管理 API(GetOtherGroups/AddOtherAccount 按组聚合)与组级 LB 策略(otherLBModes)。
//
// Provider 约定:Provider 固定 "other",ScopeType 固定 "other",GroupID/GroupName/Formats 承载组身份与协议能力。
// API Key 复用 AccessToken 字段(与 NVIDIA 同源),BaseURL 复用现有字段(同组各账号应共享同 BaseURL)。

// otherProvider / otherScopeType 是 Other 号池账号的固定 Provider/ScopeType 标识。
const (
	otherProvider = "other"
	otherScope    = "other"
)

// OtherAccountInput 是从前端/IPC 接收的 Other 号池账号录入参数(组内单账号)。
// 前端以单对象 JSON 透传(非位置参数),避免与 nvidia:add 的 8 位置参数风格耦合。
type OtherAccountInput struct {
	GroupID      string   // 组标识,如 "openai";前端校验仅小写字母数字下划线/连字符
	GroupName    string   // 组显示名,如 "OpenAI 上游组";留空则回退 GroupID
	BaseURL      string   // 组上游 URL(必须 https,同组共享)
	APIKey       string   // 该账号的 Key(组内多账号各填一个)
	Formats      []string // 该组原生协议,子集 ["openai","anthropic"],至少一项
	Label        string   // 可选展示名,留空回退"{GroupName}-{前6位Key}"
	DefaultModel string   // 可选默认模型(供档位透传兜底,首期不强制)
}

// OtherGroupInfo 是组级汇总信息,供前端「按组渲染模型获取按钮」与组选择器使用。
type OtherGroupInfo struct {
	GroupID      string   `json:"groupId"`
	GroupName    string   `json:"groupName"`
	BaseURL      string   `json:"baseUrl"`
	Formats      []string `json:"formats"`
	AccountCount int      `json:"accountCount"`
	EnabledCount int      `json:"enabledCount"`
	// LbMode 是该组当前 LB 算法(round-robin/sticky),供前端组 tab 下拉展示。
	LbMode string `json:"lbMode"`
}

// reserveOtherProviderGroupIDs 是禁止用作 GroupID 的保留值,避免与现有号池 Provider 冲突导致路由歧义。
var reserveOtherGroupIDs = map[string]bool{
	"antigravity": true, "project": true, "nvidia": true, "google": true, "gcp": true,
	"gemini-cli": true, "2fa": true, "": true, "all": true, "other": true,
}

// ValidateOtherAccountInput 校验 Other 账号录入参数(新增表单,key 必填)。
func ValidateOtherAccountInput(in OtherAccountInput) error {
	return validateOtherFields(in, true)
}

// validateOtherFields 校验 Other 账号字段。requireKey 为 false 时允许 APIKey 留空
// (编辑态留空表示保持原 Key 不变)。
// 安全红线:GroupID 规范化后仅小写字母/数字/下划线/连字符且非保留值;BaseURL 必须 https;Formats 至少一项且仅 openai/anthropic。
func validateOtherFields(in OtherAccountInput, requireKey bool) error {
	in.GroupID = strings.ToLower(strings.TrimSpace(in.GroupID))
	if in.GroupID == "" {
		return errors.New("groupId 不能为空")
	}
	if reserveOtherGroupIDs[in.GroupID] {
		return errors.New("groupId 与现有号池 Provider 冲突: " + in.GroupID)
	}
	for _, c := range in.GroupID {
		isAllowed := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !isAllowed {
			return errors.New("groupId 仅允许小写字母/数字/下划线/连字符: " + in.GroupID)
		}
	}
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.BaseURL == "" {
		return errors.New("baseUrl 不能为空")
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("baseUrl 必须是合法的 http/https 地址")
	}
	if requireKey && strings.TrimSpace(in.APIKey) == "" {
		return errors.New("apiKey 不能为空")
	}
	if len(in.Formats) == 0 {
		return errors.New("formats 至少勾选一项(openai/anthropic)")
	}
	for _, f := range in.Formats {
		fl := strings.ToLower(strings.TrimSpace(f))
		if fl != "openai" && fl != "anthropic" {
			return errors.New("formats 仅支持 openai/anthropic: " + f)
		}
	}
	return nil
}

// normalizeOtherFormats 去重并规范化 Formats 为小写排序后的稳定切片(便于持久化与比对)。
func normalizeOtherFormats(formats []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 2)
	for _, f := range formats {
		fl := strings.ToLower(strings.TrimSpace(f))
		if fl == "" || seen[fl] {
			continue
		}
		seen[fl] = true
		out = append(out, fl)
	}
	// 稳定排序:openai 在前 anthropic 在后(便于日志与展示一致)
	if len(out) == 2 && out[0] == "anthropic" {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

// NewOtherAccount 根据 input 构造一个未入库的 Other Account。
// 不会触碰 Manager 全局锁,调用方负责 AddAccount 落库。
func NewOtherAccount(in OtherAccountInput) *Account {
	groupID := strings.ToLower(strings.TrimSpace(in.GroupID))
	groupName := strings.TrimSpace(in.GroupName)
	if groupName == "" {
		groupName = groupID
	}
	baseURL := strings.TrimSpace(strings.TrimRight(in.BaseURL, "/"))
	label := strings.TrimSpace(in.Label)
	defaultModel := strings.TrimSpace(in.DefaultModel)
	if label == "" {
		// 用 {GroupName}-{Key前6位} 作展示名,便于组内多账号辨认
		keyTail := strings.TrimSpace(in.APIKey)
		if len(keyTail) > 6 {
			keyTail = keyTail[:6]
		}
		if defaultModel != "" {
			label = groupName + "-" + defaultModel + "-" + keyTail
		} else {
			label = groupName + "-" + keyTail
		}
	}
	return &Account{
		Email:        label,
		Provider:     otherProvider,
		ScopeType:    otherScope,
		GroupID:      groupID,
		GroupName:    groupName,
		Formats:      normalizeOtherFormats(in.Formats),
		AccessToken:  strings.TrimSpace(in.APIKey), // 复用 AccessToken 存 API Key,与 NVIDIA 同源
		BaseURL:      baseURL,
		DefaultModel: defaultModel,
		Enabled:      true,
		AddedAt:      time.Now().Format(time.RFC3339),
		Cooldowns:    make(map[string]int64),
	}
}

// AddOtherAccount 校验 + 构造 + 入库,返回新账号 ID 与 error。
// 组不存在时隐式建组(无独立「建组」动作,首个账号即建组);组内多账号按 (GroupID+APIKey) 去重。
func (m *Manager) AddOtherAccount(in OtherAccountInput) (string, error) {
	if err := ValidateOtherAccountInput(in); err != nil {
		return "", err
	}
	acc := NewOtherAccount(in)
	// 组内同 Key 去重:同 GroupID 下已存在相同 APIKey 的账号则拒绝,避免组内重复轮询同一 Key。
	m.RLock()
	for _, a := range m.accounts {
		if a != nil && a.Provider == otherProvider && a.GroupID == acc.GroupID &&
			strings.TrimSpace(a.GetAccessToken()) == strings.TrimSpace(acc.GetAccessToken()) {
			m.RUnlock()
			return "", errors.New("该组下已存在相同 APIKey 的账号")
		}
	}
	m.RUnlock()
	m.AddAccount(acc)
	return acc.ID, nil
}

// UpdateOtherAccount 就地更新已有 other 账号的可编辑字段(GroupID/GroupName/BaseURL/APIKey/Formats/展示名/默认模型)。
// APIKey 留空表示保持不变(前端编辑时若不改 Key 则传空),故跳过 key 必填校验。
// 返回更新后的账号,账号不存在或非 other 类型时报错。
func (m *Manager) UpdateOtherAccount(id string, in OtherAccountInput) (*Account, error) {
	if err := validateOtherFields(in, false); err != nil {
		return nil, err
	}

	m.Lock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id && a.Provider == otherProvider {
			target = a
			break
		}
	}
	if target == nil {
		m.Unlock()
		return nil, errors.New("账号不存在或非 Other 类型")
	}

	groupID := strings.ToLower(strings.TrimSpace(in.GroupID))
	groupName := strings.TrimSpace(in.GroupName)
	if groupName == "" {
		groupName = groupID
	}
	baseURL := strings.TrimSpace(strings.TrimRight(in.BaseURL, "/"))
	label := strings.TrimSpace(in.Label)
	defaultModel := strings.TrimSpace(in.DefaultModel)
	if label == "" {
		// 展示名留空回退为 {GroupName}-{Key前6位}(与 NewOtherAccount 一致)。
		keyTail := strings.TrimSpace(in.APIKey)
		if keyTail == "" {
			keyTail = target.GetAccessToken()
		}
		if len(keyTail) > 6 {
			keyTail = keyTail[:6]
		}
		if defaultModel != "" {
			label = groupName + "-" + defaultModel + "-" + keyTail
		} else {
			label = groupName + "-" + keyTail
		}
	}
	target.GroupID = groupID
	target.GroupName = groupName
	target.BaseURL = baseURL
	target.Formats = normalizeOtherFormats(in.Formats)
	target.DefaultModel = defaultModel
	target.Email = label
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

// GetOtherGroups 返回当前所有 Other 组的汇总信息(按 GroupID 聚合),前端按组渲染模型获取按钮用。
// 组内多账号取首个账号的 BaseURL/Formats 作为组级元数据(同组各账号应共享同 BaseURL/Formats,
// AddOtherAccount 校验链不强制同组追加账号时 BaseURL/Formats 一致,但前端录入时以组为单位填天然保证)。
func (m *Manager) GetOtherGroups() []OtherGroupInfo {
	m.RLock()
	defer m.RUnlock()
	groupMap := make(map[string]*OtherGroupInfo)
	order := make([]string, 0)
	for _, a := range m.accounts {
		if a == nil || a.Provider != otherProvider || strings.TrimSpace(a.GroupID) == "" {
			continue
		}
		gid := a.GroupID
		gi, ok := groupMap[gid]
		if !ok {
			gi = &OtherGroupInfo{
				GroupID:   gid,
				GroupName: a.GroupName,
				BaseURL:   a.BaseURL,
				Formats:   append([]string{}, a.Formats...),
				LbMode:    m.otherLBModes[strings.ToLower(strings.TrimSpace(gid))],
			}
			if gi.LbMode == "" {
				gi.LbMode = "round-robin"
			}
			groupMap[gid] = gi
			order = append(order, gid)
		}
		gi.AccountCount++
		if a.Enabled {
			gi.EnabledCount++
		}
	}
	out := make([]OtherGroupInfo, 0, len(order))
	for _, gid := range order {
		out = append(out, *groupMap[gid])
	}
	return out
}

// GetEnabledOtherAccounts 返回某组下所有已启用且配置有效的 Other 账号(不受 CooldownUntil 过滤)。
// 专供模型列表拉取等不消耗 Token 的只读探针接口使用,与 GetEnabledNvidiaAccounts 同口径。
func (m *Manager) GetEnabledOtherAccounts(groupID string) []*Account {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	m.RLock()
	defer m.RUnlock()
	var result []*Account
	for _, acc := range m.accounts {
		if acc != nil && acc.Provider == otherProvider && strings.TrimSpace(acc.GroupID) == gid &&
			acc.Enabled && acc.AccessToken != "" && acc.BaseURL != "" {
			result = append(result, acc)
		}
	}
	return result
}

// IsOtherAvailable 供负载均衡调用的 Other 账号可用谓词。
// 与 isPassthroughAccountUnavailable 的判定口径一致:Provider 匹配、启用、有 AccessToken 且配了 BaseURL。
func IsOtherAvailable(a *Account) bool {
	return a != nil && a.Provider == otherProvider && a.Enabled && a.AccessToken != "" && a.BaseURL != ""
}

// ============ 组级 LB 模式 ============

// GetOtherLBMode 返回某组 LB 算法,空组回退默认 round-robin。
func (m *Manager) GetOtherLBMode(groupID string) string {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	m.RLock()
	defer m.RUnlock()
	if m.otherLBModes == nil {
		return "round-robin"
	}
	if v, ok := m.otherLBModes[gid]; ok && v != "" {
		return v
	}
	return "round-robin"
}

// SetOtherLBMode 设置某组 LB 算法并持久化到 accounts.json(经 AccountsData.OtherLBModes)。
func (m *Manager) SetOtherLBMode(groupID, mode string) {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "round-robin"
	}
	m.Lock()
	if m.otherLBModes == nil {
		m.otherLBModes = make(map[string]string)
	}
	m.otherLBModes[gid] = mode
	m.Unlock()
	_ = m.SaveAccounts(true)
}

// GetOtherGroupFormats 返回某组首个启用账号的 Formats,供中继转发层决定上游端点与协议转译方向。
// 组内无可用账号时返回 nil。
func (m *Manager) GetOtherGroupFormats(groupID string) []string {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	m.RLock()
	defer m.RUnlock()
	for _, acc := range m.accounts {
		if acc != nil && acc.Provider == otherProvider && strings.TrimSpace(acc.GroupID) == gid &&
			acc.Enabled && len(acc.Formats) > 0 {
			return append([]string{}, acc.Formats...)
		}
	}
	return nil
}
