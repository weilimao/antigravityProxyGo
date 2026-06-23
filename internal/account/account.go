package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Account struct {
	// tokenMu protects concurrent reads/writes of AccessToken and RefreshToken
	tokenMu sync.RWMutex `json:"-"`

	ID             string           `json:"id"`
	Email          string           `json:"email"`
	AccessToken    string           `json:"access_token"`
	RefreshToken   string           `json:"refresh_token"`
	Provider       string           `json:"provider"`
	ProjectID      string           `json:"projectId"`
	ProjectLabel   string           `json:"projectLabel"`
	ScopeType      string           `json:"scopeType"`
	AddedAt        string           `json:"addedAt"`
	Tier           string           `json:"tier"`
	Enabled        bool             `json:"enabled"`
	EnableOverages bool             `json:"enableOverages"`
	Credits        *float64         `json:"credits"`
	Cooldowns      map[string]int64 `json:"cooldowns"`     // category -> untilTimeMs
	CooldownUntil  int64            `json:"cooldownUntil"` // max(cooldowns)
	TwoFASecret    string           `json:"twofa_secret,omitempty"`
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
	a.tokenMu.Unlock()
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
	Accounts        []*Account `json:"accounts"`
	PoolMode        bool       `json:"poolMode"`
	ProjectPoolMode bool       `json:"projectPoolMode"`
	ActiveChannel   string     `json:"activeChannel"`
}

type Manager struct {
	sync.RWMutex
	userDataPath     string
	accountsFilePath string
	accounts         []*Account
	poolMode         bool
	projectPoolMode  bool
	activeChannel    string
	currentIndex     int
	errorCounts      map[string]int // accountId -> error count
	cooldownTicker   *time.Ticker
	cooldownStop     chan struct{}

	// 解耦回调函数
	OnAccountsUpdated        func(accounts []*Account)
	OnAccountDisabled        func(accountId string)
	OnAccountCooldownUpdated func(accountId string, category string, untilTimeMs int64)
	FetchQuota               func(account *Account) (*QuotaResult, error)
	RefreshToken             func(account *Account) (string, error)
}

func NewManager() *Manager {
	return &Manager{
		accounts:      make([]*Account, 0),
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

func (m *Manager) LoadAccounts() {
	m.Lock()
	defer m.Unlock()

	if _, err := os.Stat(m.accountsFilePath); os.IsNotExist(err) {
		m.accounts = make([]*Account, 0)
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
	m.activeChannel = parsed.ActiveChannel
	m.accounts = parsed.Accounts

	// 补全和规范化字段
	for _, acc := range m.accounts {
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
			if acc.Provider == "antigravity" {
				acc.ScopeType = "account"
			} else {
				acc.ScopeType = "project"
			}
		}
		if acc.Cooldowns == nil {
			acc.Cooldowns = make(map[string]int64)
		}
		// 恢复时，如果包含启用属性，保留之，否则默认为启用
		// 在 JSON 中如果不存在，默认是 false 的话这里补充
	}
}

func (m *Manager) SaveAccounts(silent bool) error {
	m.RLock()
	data := AccountsData{
		Accounts:        m.accounts,
		PoolMode:        m.poolMode,
		ProjectPoolMode: m.projectPoolMode,
		ActiveChannel:   m.activeChannel,
	}
	m.RUnlock()

	bytesData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	m.Lock()
	err = os.WriteFile(m.accountsFilePath, bytesData, 0644)
	m.Unlock()

	if err != nil {
		return err
	}

	if !silent && m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return nil
}

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

	// 如果开启的是单账号模式，自动禁用同类型的其他账号
	if acc.Enabled {
		isAntigravity := acc.Provider == "antigravity"
		if isAntigravity && !m.poolMode {
			for _, a := range m.accounts {
				if a.Provider == "antigravity" && a.ID != acc.ID {
					a.Enabled = false
				}
			}
		} else if !isAntigravity && !m.projectPoolMode {
			for _, a := range m.accounts {
				if a.Provider != "antigravity" && a.ID != acc.ID {
					a.Enabled = false
				}
			}
		}
	}
	m.Unlock()

	_ = m.SaveAccounts(false)
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
			ID:             a.ID,
			Email:          a.Email,
			Provider:       a.Provider,
			ProjectID:      a.ProjectID,
			ProjectLabel:   a.ProjectLabel,
			ScopeType:      a.ScopeType,
			AddedAt:        a.AddedAt,
			Tier:           a.Tier,
			Enabled:        a.Enabled,
			EnableOverages: a.EnableOverages,
			Credits:        creditsCopy,
			Cooldowns:      cooldownsCopy,
			CooldownUntil:  a.CooldownUntil,
			TwoFASecret:    a.TwoFASecret,
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

				// 限制单账号启用规则
				if enabled {
					isAntigravity := a.Provider == "antigravity"
					if isAntigravity && !m.poolMode {
						for _, other := range m.accounts {
							if other.Provider == "antigravity" && other.ID != id {
								other.Enabled = false
							}
						}
					} else if !isAntigravity && !m.projectPoolMode {
						for _, other := range m.accounts {
							if other.Provider != "antigravity" && other.ID != id {
								other.Enabled = false
							}
						}
					}
				}
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

func (m *Manager) UpdateAccount2FASecret(id string, secret string) {
	m.Lock()
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.TwoFASecret != secret {
				a.TwoFASecret = secret
				changed = true
			}
			break
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

func (m *Manager) SetPoolMode(enabled bool) {
	m.Lock()
	m.poolMode = enabled
	if enabled {
		m.projectPoolMode = false
		m.activeChannel = "antigravity"
	}
	m.Unlock()
	_ = m.SaveAccounts(false)
}

func (m *Manager) GetPoolMode() bool {
	m.RLock()
	defer m.RUnlock()
	return m.poolMode
}

func (m *Manager) SetProjectPoolMode(enabled bool) {
	m.Lock()
	m.projectPoolMode = enabled
	if enabled {
		m.poolMode = false
		m.activeChannel = "project"
	}
	m.Unlock()
	_ = m.SaveAccounts(false)
}

func (m *Manager) GetProjectPoolMode() bool {
	m.RLock()
	defer m.RUnlock()
	return m.projectPoolMode
}

func (m *Manager) SetActiveChannel(channel string) {
	m.Lock()
	if channel == "antigravity" || channel == "project" {
		if channel == "project" && m.poolMode {
			m.Unlock()
			return
		}
		if channel == "antigravity" && m.projectPoolMode {
			m.Unlock()
			return
		}
		m.activeChannel = channel
	}
	m.Unlock()
	_ = m.SaveAccounts(false)
}

func (m *Manager) GetActiveChannel() string {
	m.RLock()
	defer m.RUnlock()
	return m.activeChannel
}

func (m *Manager) GetModelCategory(modelName string) string {
	name := strings.ToLower(modelName)
	if strings.Contains(name, "claude") {
		return "claude"
	}
	return "gemini"
}

func (m *Manager) GetNextAccount(modelName string) *Account {
	m.Lock()
	defer m.Unlock()

	if len(m.accounts) == 0 {
		return nil
	}

	currentChannel := m.activeChannel
	isPool := m.poolMode
	if currentChannel == "project" {
		isPool = m.projectPoolMode
	}

	// 筛选出当前通道所有启用的账号
	var activeAccounts []*Account
	for _, a := range m.accounts {
		accountChannel := "antigravity"
		if a.Provider != "antigravity" {
			accountChannel = "project"
		}
		if accountChannel == currentChannel && a.Enabled {
			activeAccounts = append(activeAccounts, a)
		}
	}

	if len(activeAccounts) == 0 {
		return nil
	}

	category := m.GetModelCategory(modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)

	isAvailable := func(acc *Account) bool {
		cooldownUntil := acc.CooldownUntil
		if acc.Cooldowns != nil {
			if v, ok := acc.Cooldowns[category]; ok {
				cooldownUntil = v
			}
		}
		hasOverages := acc.EnableOverages && acc.Credits != nil && *acc.Credits > 0
		return cooldownUntil == 0 || now >= cooldownUntil || hasOverages
	}

	if !isPool {
		// 单账号模式，返回通道中的第一个（如果可用）
		acc := activeAccounts[0]
		if isAvailable(acc) {
			// 清除已过期的冷静期
			cooldownUntil := acc.CooldownUntil
			if acc.Cooldowns != nil {
				if v, ok := acc.Cooldowns[category]; ok {
					cooldownUntil = v
				}
			}
			if cooldownUntil > 0 && now >= cooldownUntil {
				delete(acc.Cooldowns, category)
				acc.CooldownUntil = 0
				for _, v := range acc.Cooldowns {
					if v > acc.CooldownUntil {
						acc.CooldownUntil = v
					}
				}
				go func() { _ = m.SaveAccounts(true) }()
			}
			return acc
		}
		return nil
	}

	// 轮询策略
	attempts := 0
	for attempts < len(activeAccounts) {
		m.currentIndex = m.currentIndex % len(activeAccounts)
		acc := activeAccounts[m.currentIndex]
		m.currentIndex = (m.currentIndex + 1) % len(activeAccounts)

		if isAvailable(acc) {
			cooldownUntil := acc.CooldownUntil
			if acc.Cooldowns != nil {
				if v, ok := acc.Cooldowns[category]; ok {
					cooldownUntil = v
				}
			}
			if cooldownUntil > 0 && now >= cooldownUntil {
				delete(acc.Cooldowns, category)
				acc.CooldownUntil = 0
				for _, v := range acc.Cooldowns {
					if v > acc.CooldownUntil {
						acc.CooldownUntil = v
					}
				}
				go func() { _ = m.SaveAccounts(true) }()
			}
			return acc
		}
		attempts++
	}

	return nil
}

func (m *Manager) SetAccountCooldown(id string, untilTimeMs int64, modelName string) {
	m.Lock()
	category := m.GetModelCategory(modelName)
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			if a.Cooldowns == nil {
				a.Cooldowns = make(map[string]int64)
			}
			a.Cooldowns[category] = untilTimeMs

			var maxCooldown int64 = 0
			for _, v := range a.Cooldowns {
				if v > maxCooldown {
					maxCooldown = v
				}
			}
			a.CooldownUntil = maxCooldown
			changed = true
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccounts(true)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}

		if m.OnAccountCooldownUpdated != nil {
			m.OnAccountCooldownUpdated(id, category, untilTimeMs)
		}
	}
}

func (m *Manager) GetAvailableAccounts(modelName string) []*Account {
	m.RLock()
	defer m.RUnlock()

	if len(m.accounts) == 0 {
		return nil
	}

	currentChannel := m.activeChannel
	category := m.GetModelCategory(modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)

	var list []*Account
	for _, a := range m.accounts {
		accountChannel := "antigravity"
		if a.Provider != "antigravity" {
			accountChannel = "project"
		}
		if accountChannel != currentChannel || !a.Enabled {
			continue
		}

		cooldownUntil := a.CooldownUntil
		if a.Cooldowns != nil {
			if v, ok := a.Cooldowns[category]; ok {
				cooldownUntil = v
			}
		}
		hasOverages := a.EnableOverages && a.Credits != nil && *a.Credits > 0
		if cooldownUntil == 0 || now >= cooldownUntil || hasOverages {
			list = append(list, a)
		}
	}
	return list
}

func (m *Manager) UpdateAccountCooldownFromQuota(id string, buckets []QuotaBucket) bool {
	acc := m.GetAccountByID(id)
	if acc == nil || len(buckets) == 0 {
		return false
	}

	m.ResetAccountError(id)

	var geminiExhausted, claudeExhausted bool
	var geminiResetTime, claudeResetTime int64

	nowMs := time.Now().UnixNano() / int64(time.Millisecond)

	parseTime := func(timeStr string) int64 {
		if timeStr == "" {
			return 0
		}
		t, err := time.Parse(time.RFC3339, timeStr)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04:05Z", timeStr)
			if err != nil {
				return 0
			}
		}
		return t.UnixNano() / int64(time.Millisecond)
	}

	for _, b := range buckets {
		isClaude := strings.Contains(strings.ToLower(b.Group), "claude") || strings.Contains(strings.ToLower(b.ModelID), "claude")
		category := "gemini"
		if isClaude {
			category = "claude"
		}

		isExhausted := b.RemainingFraction == 0 || b.RemainPercent == 0
		if isExhausted {
			resetMs := parseTime(b.ResetTime)
			if category == "claude" {
				claudeExhausted = true
				if resetMs > claudeResetTime {
					claudeResetTime = resetMs
				}
			} else {
				geminiExhausted = true
				if resetMs > geminiResetTime {
					geminiResetTime = resetMs
				}
			}
		}
	}

	m.Lock()
	defer m.Unlock()

	// 重新获取防止脏写
	acc = nil
	for _, a := range m.accounts {
		if a.ID == id {
			acc = a
			break
		}
	}
	if acc == nil {
		return false
	}

	if acc.Cooldowns == nil {
		acc.Cooldowns = make(map[string]int64)
	}

	changed := false

	updateCatCooldown := func(cat string, exhausted bool, resetTime int64) {
		if exhausted {
			targetTime := resetTime
			if targetTime == 0 {
				targetTime = nowMs + 10*60*1000 // 默认冷静10分钟
			}
			if acc.Cooldowns[cat] != targetTime {
				acc.Cooldowns[cat] = targetTime
				changed = true
			}
		} else {
			if _, ok := acc.Cooldowns[cat]; ok {
				delete(acc.Cooldowns, cat)
				changed = true
			}
		}
	}

	updateCatCooldown("gemini", geminiExhausted, geminiResetTime)
	updateCatCooldown("claude", claudeExhausted, claudeResetTime)

	var maxCooldown int64 = 0
	for _, v := range acc.Cooldowns {
		if v > maxCooldown {
			maxCooldown = v
		}
	}
	newCooldownUntil := maxCooldown
	if maxCooldown == 0 {
		newCooldownUntil = 0
	}

	if acc.CooldownUntil != newCooldownUntil {
		acc.CooldownUntil = newCooldownUntil
		changed = true
	}

	if changed {
		go func() {
			_ = m.SaveAccounts(true)
			if m.OnAccountsUpdated != nil {
				m.OnAccountsUpdated(m.accounts)
			}
		}()

		if m.OnAccountCooldownUpdated != nil {
			// 触发事件通知
			go m.OnAccountCooldownUpdated(id, "all", newCooldownUntil)
		}
	}

	return changed
}

func (m *Manager) StartCooldownMonitor() {
	m.Lock()
	if m.cooldownTicker != nil {
		m.Unlock()
		return
	}
	m.cooldownTicker = time.NewTicker(2 * time.Minute)
	m.cooldownStop = make(chan struct{})
	m.Unlock()

	go func() {
		for {
			select {
			case <-m.cooldownTicker.C:
				m.RLock()
				var cooldownAccounts []*Account
				now := time.Now().UnixNano() / int64(time.Millisecond)
				for _, a := range m.accounts {
					if a.CooldownUntil > 0 && now >= a.CooldownUntil {
						cooldownAccounts = append(cooldownAccounts, a)
					}
				}
				m.RUnlock()

				if len(cooldownAccounts) == 0 {
					continue
				}

				if m.FetchQuota == nil {
					// 如果未注册配额拉取回调，直接解除冷静状态
					m.Lock()
					for _, acc := range cooldownAccounts {
						acc.CooldownUntil = 0
						acc.Cooldowns = make(map[string]int64)
					}
					m.Unlock()
					_ = m.SaveAccounts(false)
					continue
				}

				for _, acc := range cooldownAccounts {
					// 异步刷新验证
					go func(a *Account) {
						fmt.Printf("[CooldownMonitor] Verifying quota for cooled account: %s\n", a.Email)
						res, err := m.FetchQuota(a)
						if err != nil {
							// 刷新失败，冷静期往后延长 5 分钟
							m.Lock()
							targetAcc := m.GetAccountByID(a.ID)
							if targetAcc != nil {
								nextCooldown := time.Now().UnixNano()/int64(time.Millisecond) + 5*60*1000
								targetAcc.CooldownUntil = nextCooldown
								if targetAcc.Cooldowns != nil {
									for k := range targetAcc.Cooldowns {
										targetAcc.Cooldowns[k] = nextCooldown
									}
								}
							}
							m.Unlock()
							_ = m.SaveAccounts(true)
							return
						}

						if res != nil && len(res.Buckets) > 0 {
							m.UpdateAccountCooldownFromQuota(a.ID, res.Buckets)
						}
						if res != nil && res.Tier != "" {
							m.UpdateAccountTier(a.ID, res.Tier)
						}
						if res != nil && res.Credits != nil {
							m.UpdateAccountCredits(a.ID, *res.Credits)
						}
					}(acc)
				}
			case <-m.cooldownStop:
				return
			}
		}
	}()
}

func (m *Manager) StopCooldownMonitor() {
	m.Lock()
	defer m.Unlock()
	if m.cooldownTicker != nil {
		m.cooldownTicker.Stop()
		m.cooldownTicker = nil
		close(m.cooldownStop)
	}
}

func (m *Manager) RecordAccountError(id string, statusCode int, modelName string, logFn func(string)) {
	if statusCode != 503 && statusCode != 429 {
		return
	}

	category := m.GetModelCategory(modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)

	m.Lock()
	var acc *Account
	for _, a := range m.accounts {
		if a.ID == id {
			acc = a
			break
		}
	}
	if acc == nil {
		m.Unlock()
		return
	}

	cooldownUntil := acc.CooldownUntil
	if acc.Cooldowns != nil {
		if v, ok := acc.Cooldowns[category]; ok {
			cooldownUntil = v
		}
	}
	hasQuota := cooldownUntil == 0 || now >= cooldownUntil

	if !hasQuota {
		m.Unlock()
		return
	}

	currentCount := m.errorCounts[id] + 1
	m.errorCounts[id] = currentCount
	email := acc.Email

	// If threshold reached, clear error count atomically before releasing lock
	shouldFetch := currentCount >= 5
	if shouldFetch {
		delete(m.errorCounts, id) // 清除计数，防止并发多次触发刷新
	}
	m.Unlock()

	if logFn != nil {
		logFn(fmt.Sprintf("⚠️ [负载均衡] 账号 %s 遇到 %d 报错，连续报错次数: %d/5", email, statusCode, currentCount))
	}

	if shouldFetch {
		if logFn != nil {
			logFn(fmt.Sprintf("🔄 [负载均衡] 账号 %s 连续遇到 503/429 达到 5 次，触发自动刷新配额以修正冷静状态...", email))
		}
		if m.FetchQuota != nil {
			go func(a *Account) {
				res, err := m.FetchQuota(a)
				if err == nil && res != nil {
					m.UpdateAccountCooldownFromQuota(a.ID, res.Buckets)
					if res.Tier != "" {
						m.UpdateAccountTier(a.ID, res.Tier)
					}
					if res.Credits != nil {
						m.UpdateAccountCredits(a.ID, *res.Credits)
					}
				} else if err != nil && logFn != nil {
					logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 自动刷新配额失败: %v", a.Email, err))
				}
			}(acc)
		}
	}
}

func (m *Manager) ResetAccountError(id string) {
	m.Lock()
	defer m.Unlock()
	delete(m.errorCounts, id)
}

func (m *Manager) RefreshAccountTokenSync(id string) (string, error) {
	m.RLock()
	acc := m.GetAccountByID(id)
	m.RUnlock()
	if acc == nil {
		return "", errors.New("账号未找到")
	}

	if m.RefreshToken == nil {
		return "", errors.New("Token 刷新服务未注册")
	}

	newToken, err := m.RefreshToken(acc)
	if err != nil {
		return "", err
	}

	m.UpdateAccessToken(id, newToken)
	return newToken, nil
}
