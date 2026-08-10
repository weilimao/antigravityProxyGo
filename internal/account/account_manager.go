package account

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		concurrency:   NewConcurrency(),
		idEpoch:       randEpochSeed(),
	}
}

// randEpochSeed 用 crypto/rand 生成一个随机基数,作为 generateAccountID 的「进程隔离因子」。
// 一次性生成,全进程共用;消除「同纳秒同取模 → 同 ID」的并发碰撞。
func randEpochSeed() uint64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand 极少失败(Windows 上可用性等同于系统 CSP);真失败时回退到纳秒基数,仅退化为旧碰撞语义,不阻断流程。
		return uint64(time.Now().UnixNano())
	}
	b := binary.BigEndian.Uint64(buf[:])
	if b == 0 {
		return uint64(time.Now().UnixNano())
	}
	return b
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

// generateAccountID 生成进程内唯一的账号 ID。
//
// 旧实现 impaired
// (两个 AddAccount 连调 / ImportAccountsList 批量导入同纳秒)下极具碰撞可能,
// 致使多账号最终共用同一 ID。后果:前端 renderAccounts 以 acc.id 为 DOM 主键
// (accountsRenderer.ts 的 querySelector(`[data-account-id="<id>"]`)),
// 同 ID 多账号仅命中首张卡片,后续账号被静默丢弃 —— 表现为「导入多个账号界面只显示一个」。
//
// 新实现:纳秒时间戳 ⊕ 进程随机 epoch(提升跨实例隔离) ⊕ 进程内单调递增序号(消除同纳秒碰撞),
// 用 atomic 自增序号,单增保证生成物在进程内严格两两不同,彻底消除碰撞。
//
// 调用方约定:需在持有 m.Lock()(写锁)的临界区内调用,以与经典同步模型一致;
// 以 m.Lock + atomic 序号组合保证 ID 在并发写入路径下不重复。
func (m *Manager) generateAccountID() string {
	seq := atomic.AddUint64(&m.idSeq, 1)
	return fmt.Sprintf("%d-%d-%d", time.Now().UnixNano(), seq, m.idEpoch)
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
	if parsed.OtherLBModes != nil {
		// 载入时统一规范化为小写 groupID,保证与 SetOtherLBMode 的 key 口径一致。
		m.otherLBModes = make(map[string]string, len(parsed.OtherLBModes))
		for gid, mode := range parsed.OtherLBModes {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" {
				continue
			}
			mode = strings.TrimSpace(mode)
			if mode != "round-robin" && mode != "sticky" {
				mode = "round-robin"
			}
			m.otherLBModes[lgid] = mode
		}
	}
	m.nvidiaLBMode = parsed.NvidiaLBMode
	m.grokLBMode = parsed.GrokLBMode
	// 单账号最大并发数限制载入(对齐 otherLBModes 范式;0/负数=未配置,Get 时回退默认 10)。
	// Other map 持久化键规范化为小写 groupID,与 SetOtherMaxConcurrency 的 key 口径一致。
	m.nvidiaMaxConcurrency = parsed.NvidiaMaxConcurrency
	m.grokMaxConcurrency = parsed.GrokMaxConcurrency
	// Grok 号池全局 CLI 客户端版本号载入(对仗 grokMaxConcurrency):空串=未配置,
	// GetGrokCliVersion 时回退默认 DefaultGrokCliVersion("1.0.0")。TrimSpace 防御空白输入。
	m.grokCliVersion = strings.TrimSpace(parsed.GrokCliVersion)
	// Grok 号池「额度超限后冷却时长」载入(对仗 grokCliVersion, 单位小时):0/负数=未配置,
	// GetGrokQuotaCooldownHours 回退默认 DefaultGrokQuotaCooldownHours(24)。负数非法钳 0(规整范式)。
	m.grokQuotaCooldownHours = parsed.GrokQuotaCooldownHours
	if m.grokQuotaCooldownHours < 0 {
		m.grokQuotaCooldownHours = 0
	}
	m.antigravityMaxConcurrency = parsed.AntigravityMaxConcurrency
	m.projectMaxConcurrency = parsed.ProjectMaxConcurrency
	if parsed.OtherMaxConcurrency != nil {
		m.otherMaxConcurrency = make(map[string]int, len(parsed.OtherMaxConcurrency))
		for gid, v := range parsed.OtherMaxConcurrency {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" {
				continue
			}
			if v < 0 {
				v = 0 // 负数非法,规整为 0(等同未配置,Get 回退 10)
			}
			m.otherMaxConcurrency[lgid] = v
		}
	}
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

	// 兜底修复：清除历史遗留的「重复 Account.ID」。
	// 背景:旧版 generateAccountID 在同纳秒下会生成相同 ID;一旦 accounts.json 中已落库多个共用同一 ID 的账号,
	// 前端 renderAccounts 以 acc.id 为 DOM 主键进行 querySelector 只会命中第一张,
	// 后续同 ID 账号被静默丢弃 —— 表现为「导入多个账号界面只显示一个」。
	// 此处遍历并给重复 ID 的账号重新分配唯一 ID(首遇保留),杜绝历史脏数据继续造成界面少号。
	// 2FA 列表同理处理,避免 twofa 与 active 池间、或 twofa 内部仍残留同 ID。
	m.ensureUniqueIDsLocked(m.accounts)
	m.ensureUniqueIDsLocked(m.twofaAccounts)
}

// ensureUniqueIDsLocked 在已持有 m.Lock 的前提下,为列表内「除首次出现外」的重复 ID 账号重新生成唯一 ID。
// 空 ID 的账号也在此一并由 generateAccountID 补全。调用方须已持有 m 写锁。
func (m *Manager) ensureUniqueIDsLocked(list []*Account) {
	seen := make(map[string]struct{}, len(list))
	for _, acc := range list {
		if acc == nil {
			continue
		}
		if acc.ID == "" {
			acc.ID = m.generateAccountID()
			seen[acc.ID] = struct{}{}
			continue
		}
		if _, dup := seen[acc.ID]; dup {
			// 历史遗留同 ID:重新生成一个不与已有任何 ID 冲突的唯一值。
			acc.ID = m.generateUniqueIDLocked(seen)
		}
		seen[acc.ID] = struct{}{}
	}
}

// generateUniqueIDLocked 在已持锁前提下,反复生成 ID 直到与 seen 集合(以及全局现有 ID 池)都不重复。
// 理论上限极低(纳秒+进程递增序号+随机 epoch),循环仅为防御性兜底。
func (m *Manager) generateUniqueIDLocked(seen map[string]struct{}) string {
	for i := 0; i < 32; i++ {
		id := m.generateAccountID()
		if _, dup := seen[id]; dup {
			continue
		}
		if m.idExistsLocked(id) {
			continue
		}
		return id
	}
	// 32 次仍碰撞(概率近乎 0):退化为纳秒+序号+随机 epoch 后再拼一个计数尾巴,强制唯一。
	return fmt.Sprintf("%d-%d-%d-x", time.Now().UnixNano(), atomic.AddUint64(&m.idSeq, 1), m.idEpoch)
}

// idExistsLocked 在已持锁前提下,检查某 ID 是否已存在于 active 池或 2FA 池。
func (m *Manager) idExistsLocked(id string) bool {
	for _, a := range m.accounts {
		if a != nil && a.ID == id {
			return true
		}
	}
	for _, a := range m.twofaAccounts {
		if a != nil && a.ID == id {
			return true
		}
	}
	return false
}

func (m *Manager) SaveAccounts(silent bool) error {
	m.RLock()
	data := AccountsData{
		Accounts:                  m.accounts,
		TwoFAAccounts:             m.twofaAccounts,
		PoolMode:                  m.poolMode,
		ProjectPoolMode:           m.projectPoolMode,
		GeminiCliPoolMode:         m.geminiCliPoolMode,
		ActiveChannel:             m.activeChannel,
		OtherLBModes:              m.otherLBModes,
		NvidiaLBMode:              m.nvidiaLBMode,
		GrokLBMode:                m.grokLBMode,
		NvidiaMaxConcurrency:      m.nvidiaMaxConcurrency,
		AntigravityMaxConcurrency: m.antigravityMaxConcurrency,
		ProjectMaxConcurrency:     m.projectMaxConcurrency,
		OtherMaxConcurrency:       m.otherMaxConcurrency,
		GrokMaxConcurrency:        m.grokMaxConcurrency,
		GrokCliVersion:            m.grokCliVersion,
		GrokQuotaCooldownHours:    m.grokQuotaCooldownHours,
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
	// ID 唯一性保证:为空或与池内已有 ID 撞号时重新分配,避免「同 ID → 前端只渲染一张卡片」。
	if acc.ID == "" || m.idExistsLocked(acc.ID) {
		acc.ID = m.generateUniqueIDLocked(map[string]struct{}{})
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
	// 本批次已分配 ID 集合,防止输入 JSON 自带非唯一 ID(或旧版残留)时多账号落入同一 ID。
	batchSeen := make(map[string]struct{}, len(accountsList))
	addedCount := 0
	for _, acc := range accountsList {
		if acc.Email == "" {
			acc.Email = "Unknown Account"
		}
		// ID 唯一性保证:传入 ID 为空、或与池内/批次内已有 ID 撞号时,重新分配唯一 ID。
		// 修复「多个不同邮箱账号共用同一 ID → 前端 querySelector 只命中首张卡片」的渲染丢号问题。
		if acc.ID == "" || m.idExistsLocked(acc.ID) {
			acc.ID = m.generateUniqueIDLocked(batchSeen)
		}
		if _, dup := batchSeen[acc.ID]; dup {
			acc.ID = m.generateUniqueIDLocked(batchSeen)
		}
		batchSeen[acc.ID] = struct{}{}
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
			MaskedKey:        maskedKeyForAccount(a),
			BaseURL:          a.BaseURL,
			TokenEndpoint:    a.TokenEndpoint,
			DefaultModel:     a.DefaultModel,
			ModelSonnet:      a.ModelSonnet,
			ModelOpus:        a.ModelOpus,
			ModelHaiku:       a.ModelHaiku,
			ModelFable:       a.ModelFable,
			GroupID:          a.GroupID,
			GroupName:        a.GroupName,
			Formats:          a.Formats,
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

// UpdateAccountRefreshToken 更新账号的 RefreshToken(走 token 锁,与 UpdateAccessToken 同范式)。
// 供 OAuth 刷新链路在 refresh_token 轮换时回写(xai 等刷新可能返回新 refresh_token)。
func (m *Manager) UpdateAccountRefreshToken(id, newRefresh string) {
	m.RLock()
	var target *Account
	for _, a := range m.accounts {
		if a.ID == id {
			target = a
			break
		}
	}
	m.RUnlock()

	if target != nil {
		target.SetRefreshToken(newRefresh)
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
		"other":       true,
		"grok":        true,
	}
	out := []string{"antigravity", "google", "gcp", "nvidia", "other", "grok"}

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
