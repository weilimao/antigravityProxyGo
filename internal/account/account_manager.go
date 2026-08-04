package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// account_manager.go 收纳 Manager 的核心结构、构造、初始化与账号持久化/CRUD。
// 拆分自原 account.go:此处只保留「账号池容器 + 磁盘读写 + 增删改查」基础设施;
// 账号轮询选择/冷却逻辑见 account_selector.go,定时监控/Token 刷新见 account_monitor.go。
//
// conc:
//   - fileLock 串行化磁盘写,与 SaveAccounts 的 RLock 解耦,避免「持读锁写文件」死锁;
//   - refreshLocks 用 sync.Map 为每账号提供独立互斥锁(见 getAccountRefreshLock)。

// ============ Manager 结构体 + 构造 + 初始化 ============

func (m *Manager) getAccountRefreshLock(id string) *sync.Mutex {
	v, _ := m.refreshLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func NewManager() *Manager {
	return &Manager{
		accounts:      make([]*Account, 0),
		twofaAccounts: make([]*Account, 0),
		activeChannel: "antigravity",
		errorCounts:   make(map[string]int),
	}
}

func (m *Manager) Init(userDataPath string) {
	m.Lock()
	m.userDataPath = userDataPath
	m.accountsFilePath = filepath.Join(userDataPath, "accounts.json")
	m.Unlock()

	m.LoadAccounts()
	m.StartCooldownMonitor()
	m.StartTokenRefreshMonitor()
}

func (m *Manager) UpdatePath(newPath string) {
	m.Lock()
	m.userDataPath = newPath
	m.accountsFilePath = filepath.Join(newPath, "accounts.json")
	m.Unlock()

	m.LoadAccounts()
}

func (m *Manager) generateAccountID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().UnixNano()%100000)
}

// ============ 磁盘加载/保存 ============

func (m *Manager) LoadAccounts() {
	m.Lock()
	defer m.Unlock()

	if _, err := os.Stat(m.accountsFilePath); os.IsNotExist(err) {
		m.accounts = make([]*Account, 0)
		m.twofaAccounts = make([]*Account, 0)
		return
	}

	data, err := os.ReadFile(m.accountsFilePath)
	if err != nil {
		fmt.Printf("[AccountManager] Failed to read accounts.json: %v\n", err)
		return
	}

	var parsed AccountsData
	if err := json.Unmarshal(data, &parsed); err != nil {
		fmt.Printf("[AccountManager] Failed to parse accounts.json: %v\n", err)
		return
	}

	m.poolMode = parsed.PoolMode
	m.projectPoolMode = parsed.ProjectPoolMode
	m.geminiCliPoolMode = parsed.GeminiCliPoolMode
	m.activeChannel = parsed.ActiveChannel
	if m.activeChannel == "gemini-cli" {
		m.activeChannel = "antigravity"
	}
	m.accounts = parsed.Accounts
	m.twofaAccounts = parsed.TwoFAAccounts
	if m.twofaAccounts == nil {
		m.twofaAccounts = make([]*Account, 0)
	}

	// 补全独立 2FA 列表的字段
	for _, acc := range m.twofaAccounts {
		if acc.ID == "" {
			acc.ID = m.generateAccountID()
		}
		acc.Provider = "2fa"
		acc.ScopeType = "2fa"
	}

	// 核心迁移逻辑：自动将原有 accounts 列表中没有 Token 的 2FA 账号分离出来
	var activePoolAccounts []*Account
	for _, acc := range m.accounts {
		isTwoFAOnly := acc.TwoFASecret != "" && acc.AccessToken == "" && acc.RefreshToken == "" && acc.Provider == "antigravity"
		if isTwoFAOnly {
			// 自动迁移入独立的 2FA 列表中
			acc.Provider = "2fa"
			acc.ScopeType = "2fa"
			acc.Enabled = false

			alreadyExists := false
			for _, t := range m.twofaAccounts {
				if t.Email == acc.Email {
					t.TwoFASecret = acc.TwoFASecret
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				m.twofaAccounts = append(m.twofaAccounts, acc)
			}
		} else {
			if acc.ID == "" {
				acc.ID = m.generateAccountID()
			}
			if acc.Provider == "" {
				if acc.ProjectID != "" {
					acc.Provider = "project"
				} else {
					acc.Provider = "antigravity"
				}
			}
			if acc.ScopeType == "" {
				if acc.Provider == "antigravity" || acc.Provider == "gemini-cli" {
					acc.ScopeType = "account"
				} else {
					acc.ScopeType = "project"
				}
			}
			if acc.Cooldowns == nil {
				acc.Cooldowns = make(map[string]int64)
			}
			activePoolAccounts = append(activePoolAccounts, acc)
		}
	}
	m.accounts = activePoolAccounts

	// 兜底修复：为从旧版 JSON 加载的 NVIDIA 账号补全缺失的 BaseURL 字段
	for _, acc := range m.accounts {
		if acc.Provider == "nvidia" && acc.BaseURL == "" {
			acc.BaseURL = DefaultNvidiaBaseURL
		}
	}
}

func (m *Manager) SaveAccounts(silent bool) error {
	m.RLock()
	data := AccountsData{
		Accounts:          m.accounts,
		TwoFAAccounts:     m.twofaAccounts,
		PoolMode:          m.poolMode,
		ProjectPoolMode:   m.projectPoolMode,
		GeminiCliPoolMode: m.geminiCliPoolMode,
		ActiveChannel:     m.activeChannel,
	}
	m.RUnlock()

	bytesData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	m.fileLock.Lock()
	err = os.WriteFile(m.accountsFilePath, bytesData, 0644)
	m.fileLock.Unlock()

	if err != nil {
		return err
	}

	if !silent && m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return nil
}

// ============ 账号增删改查 CRUD ============

func (m *Manager) AddAccount(acc *Account) {
	m.Lock()
	if acc.Email == "" {
		acc.Email = "Unknown Account"
	}
	if acc.ID == "" {
		acc.ID = m.generateAccountID()
	}
	if acc.Cooldowns == nil {
		acc.Cooldowns = make(map[string]int64)
	}
	if acc.AddedAt == "" {
		acc.AddedAt = time.Now().Format(time.RFC3339)
	}

	// 排重：删除相同 Email、Provider 和 ProjectID 的账号
	var newAccounts []*Account
	for _, a := range m.accounts {
		if a.Email == acc.Email && a.Provider == acc.Provider && a.ProjectID == acc.ProjectID {
			continue
		}
		newAccounts = append(newAccounts, a)
	}
	newAccounts = append(newAccounts, acc)
	m.accounts = newAccounts

	m.Unlock()

	_ = m.SaveAccounts(false)

	// 自动为新添加的账号拉取配额和级别信息，以完成初始数据的填充
	if m.FetchQuota != nil {
		go func() {
			res, err := m.FetchQuota(acc)
			if err == nil && res != nil {
				m.UpdateAccountQuota(acc.ID, res)
			}
		}()
	}
}

func (m *Manager) ImportAccountsList(accountsList []*Account) int {
	m.Lock()
	addedCount := 0
	for _, acc := range accountsList {
		if acc.Email == "" {
			acc.Email = "Unknown Account"
		}
		if acc.ID == "" {
			acc.ID = m.generateAccountID()
		}
		if acc.Cooldowns == nil {
			acc.Cooldowns = make(map[string]int64)
		}

		// 排重
		var newAccounts []*Account
		for _, a := range m.accounts {
			if a.Email == acc.Email && a.Provider == acc.Provider && a.ProjectID == acc.ProjectID {
				continue
			}
			newAccounts = append(newAccounts, a)
		}
		newAccounts = append(newAccounts, acc)
		m.accounts = newAccounts
		addedCount++
	}
	m.Unlock()

	if addedCount > 0 {
		_ = m.SaveAccounts(false)
	}
	return addedCount
}

func (m *Manager) RemoveAccount(id string) {
	m.Lock()
	var newAccounts []*Account
	for _, a := range m.accounts {
		if a.ID == id {
			continue
		}
		newAccounts = append(newAccounts, a)
	}
	m.accounts = newAccounts
	if m.currentIndex >= len(m.accounts) {
		m.currentIndex = 0
	}
	m.Unlock()

	_ = m.SaveAccounts(false)
}

// ============ 账号读取(展示用深拷贝/原始引用) ============

func (m *Manager) GetAccounts() []*Account {
	m.RLock()
	defer m.RUnlock()

	// 不泄露 access_token 和 refresh_token，深拷贝用于前端展示
	var list []*Account
	for _, a := range m.accounts {
		creditsCopy := a.Credits
		cooldownsCopy := make(map[string]int64)
		for k, v := range a.Cooldowns {
			cooldownsCopy[k] = v
		}

		list = append(list, &Account{
			ID:               a.ID,
			Email:            a.Email,
			Provider:         a.Provider,
			ProjectID:        a.ProjectID,
			ProjectLabel:     a.ProjectLabel,
			ScopeType:        a.ScopeType,
			AddedAt:          a.AddedAt,
			Tier:             a.Tier,
			Enabled:          a.Enabled,
			EnableOverages:   a.EnableOverages,
			Credits:          creditsCopy,
			Cooldowns:        cooldownsCopy,
			CooldownUntil:    a.CooldownUntil,
			TwoFASecret:      a.TwoFASecret,
			TokenRefreshedAt: a.GetTokenRefreshedAt(),
		})
	}
	return list
}

func (m *Manager) GetRawAccounts() []*Account {
	m.RLock()
	defer m.RUnlock()
	return m.accounts
}

func (m *Manager) GetAccountByID(id string) *Account {
	m.RLock()
	defer m.RUnlock()
	for _, a := range m.accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// ============ 账号字段更新(Token/Credits/Overages/Enabled/Tier/2FA) ============

func (m *Manager) UpdateAccessToken(id, newToken string) {
	m.RLock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id {
			target = a
			break
		}
	}
	m.RUnlock()

	// Use per-account token lock to update safely without holding the global Manager write lock
	if target != nil {
		target.SetAccessToken(newToken)
		_ = m.SaveAccounts(true)
	}
}

func (m *Manager) UpdateAccountCredits(id string, credits float64) {
	m.Lock()
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.Credits == nil || *a.Credits != credits {
				a.Credits = &credits
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(true)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

func (m *Manager) UpdateAccountOverages(id string, enabled bool) {
	m.Lock()
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.EnableOverages != enabled {
				a.EnableOverages = enabled
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(true)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

func (m *Manager) UpdateAccountEnabled(id string, enabled bool) {
	m.Lock()
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.Enabled != enabled {
				a.Enabled = enabled
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(true)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}

		if !enabled && m.OnAccountDisabled != nil {
			m.OnAccountDisabled(id)
		}
	}
}

func (m *Manager) AddTwoFAAccount(email, secret string) {
	m.Lock()
	if email == "" {
		m.Unlock()
		return
	}

	foundInPool := false
	for _, a := range m.accounts {
		if a.Email == email {
			a.TwoFASecret = secret
			foundInPool = true
		}
	}

	if !foundInPool {
		foundIn2FA := false
		for _, a := range m.twofaAccounts {
			if a.Email == email {
				a.TwoFASecret = secret
				foundIn2FA = true
				break
			}
		}
		if !foundIn2FA {
			m.twofaAccounts = append(m.twofaAccounts, &Account{
				ID:          m.generateAccountID(),
				Email:       email,
				TwoFASecret: secret,
				Provider:    "2fa",
				ScopeType:   "2fa",
				Enabled:     false,
				AddedAt:     time.Now().Format(time.RFC3339),
			})
		}
	}
	m.Unlock()

	_ = m.SaveAccounts(false)
}

func (m *Manager) GetTwoFAAccounts() []*Account {
	m.RLock()
	defer m.RUnlock()

	var list []*Account
	seenEmails := make(map[string]bool)

	for _, a := range m.accounts {
		if a.TwoFASecret != "" {
			if !seenEmails[a.Email] {
				list = append(list, a)
				seenEmails[a.Email] = true
			}
		}
	}
	for _, a := range m.twofaAccounts {
		if !seenEmails[a.Email] {
			list = append(list, a)
			seenEmails[a.Email] = true
		}
	}
	return list
}

func (m *Manager) UpdateAccount2FASecret(id string, secret string) {
	m.Lock()
	changed := false

	// 先通过 ID 找到对应的 Email
	var targetEmail string
	for _, a := range m.accounts {
		if a.ID == id {
			targetEmail = a.Email
			break
		}
	}
	if targetEmail == "" {
		for _, a := range m.twofaAccounts {
			if a.ID == id {
				targetEmail = a.Email
				break
			}
		}
	}

	// 如果找到了对应的 Email，则全量更新
	if targetEmail != "" {
		// 1. 更新主账号池中的所有匹配记录
		for _, a := range m.accounts {
			if a.Email == targetEmail {
				if a.TwoFASecret != secret {
					a.TwoFASecret = secret
					changed = true
				}
			}
		}

		// 2. 更新独立的 2FA 账号列表中的记录
		var new2FAAccounts []*Account
		for _, a := range m.twofaAccounts {
			if a.Email == targetEmail {
				if secret != "" {
					if a.TwoFASecret != secret {
						a.TwoFASecret = secret
						changed = true
					}
					new2FAAccounts = append(new2FAAccounts, a)
				} else {
					// 密钥被清空且是 2FA-only 账号，直接将其从独立列表中删除
					changed = true
				}
			} else {
				new2FAAccounts = append(new2FAAccounts, a)
			}
		}

		// 只有在独立 2FA 列表中有变动，或者原本列表需要过滤时，才替换 slice
		// 实际上由于全量遍历了，直接覆盖是最安全的做法
		if changed || len(new2FAAccounts) != len(m.twofaAccounts) {
			m.twofaAccounts = new2FAAccounts
			changed = true
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(false)
	}
}

func (m *Manager) UpdateAccountTier(id, tier string) {
	m.Lock()
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.Tier != tier {
				a.Tier = tier
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(true)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

// ============ 账号派生映射(Email/Provider) ============

func (m *Manager) GetAccountEmailMap() map[string]string {
	m.RLock()
	defer m.RUnlock()
	res := make(map[string]string)
	for _, acc := range m.accounts {
		if acc != nil {
			res[acc.ID] = acc.Email
		}
	}
	return res
}

// GetAccountProviderMap 返回 AccountID -> Provider 的映射。
// 用于会话绑定/会话路由等需要按账号所属号池类型(provider)进行筛选或归类的场景，
// 复用与 GetAccountEmailMap 相同的只读遍历方式，不引入额外的并发风险。
func (m *Manager) GetAccountProviderMap() map[string]string {
	m.RLock()
	defer m.RUnlock()
	res := make(map[string]string)
	for _, acc := range m.accounts {
		if acc != nil {
			res[acc.ID] = acc.Provider
		}
	}
	return res
}

// GetAllChannels 返回当前账号池中包含的所有去重 Provider/Channel 列表，
// 默认包含预设号池 ["antigravity", "google", "gcp", "nvidia"] 及已存在账号中的任意第三方 Provider。
func (m *Manager) GetAllChannels() []string {
	m.RLock()
	defer m.RUnlock()

	seen := map[string]bool{
		"antigravity": true,
		"google":      true,
		"gcp":         true,
		"nvidia":      true,
	}
	out := []string{"antigravity", "google", "gcp", "nvidia"}

	for _, acc := range m.accounts {
		if acc != nil && acc.Provider != "" {
			p := acc.Provider
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

