package account

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"path/filepath"
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
	m.StartGrokAuthMonitor()
}

func (m *Manager) UpdatePath(newPath string) {
	m.Lock()
	m.userDataPath = newPath
	m.accountsFilePath = filepath.Join(newPath, "accounts.json")
	m.Unlock()

	m.LoadAccounts()
}// generateAccountID 生成进程内唯一的账号 ID。
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

	// 磁盘层:首次启动若检测到旧 accounts.json 且无任何 accounts_*.json 分区文件,则一次性
	// 拆分为 7 个分区文件并把旧文件重命名为 accounts.json.bak;随后逐分区加载到内存。
	// 迁移失败不阻断:走分区加载(分区文件可能已部分落盘),内存层迁移逻辑会兜底正规化,
	// 旧 accounts.json 原地保留供下次启动重试。
	if m.shouldMigrateLegacy() {
		if err := m.migrateLegacyFile(); err != nil {
			fmt.Printf("[AccountManager] migrate legacy accounts.json failed: %v\n", err)
		}
	}
	m.loadFromPartitions()

	// ===== 以下为内存层正规化(与磁盘分区无关,原样保留自旧实现) =====

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

	// 兜底修复：清洗非 Antigravity / NVIDIA 账号上误入的 gemini/claude/nvidia 冷却脏数据
	for _, acc := range m.accounts {
		if acc.Provider != "antigravity" && acc.Provider != "nvidia" {
			if acc.Cooldowns != nil {
				delete(acc.Cooldowns, "gemini")
				delete(acc.Cooldowns, "claude")
				delete(acc.Cooldowns, "nvidia")
			}
			channelCat := m.GetModelCategoryByProvider(acc.Provider, "")
			if acc.Cooldowns == nil || acc.Cooldowns[channelCat] == 0 {
				acc.CooldownUntil = 0
			}
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

// SaveAccounts 落盘全部分区(5 provider + 2fa + pool)。保留作全量写入口:
//   - Init 后的兜底全量重建、ImportAccountsList 跨多 provider 落盘、
//   - 旧调用点未显式指定 provider 时的兼容回退。
//
// 单点编辑/池配置请改用 SaveAccountsFor(silent, kinds...) 定向落盘,避免重写无关分区。
// silent=true 时跳过 OnAccountsUpdated 回调(与旧实现同口径,供 token 刷新等静默路径用)。
func (m *Manager) SaveAccounts(silent bool) error {
	return m.SaveAccountsFor(silent)
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

	provider := acc.Provider
	m.Unlock()

	// 定向落盘:只重写新增账号所属 provider 分区(排重可能波及同 provider 既有账号,故落该整分区)。
	_ = m.SaveAccountsFor(false, provider)

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
	// 受影响 provider 集合:批量导入可能跨多 provider,落盘时按集合逐分区写,不误伤未涉及分区。
	touchedProviders := make(map[string]struct{}, len(accountsList))
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
		if acc.Provider != "" {
			touchedProviders[acc.Provider] = struct{}{}
		}
		addedCount++
	}
	m.Unlock()

	if addedCount > 0 {
		// 定向落盘:只重写被本批次触及的 provider 分区,未涉及的号池分区不重写。
		kinds := make([]string, 0, len(touchedProviders))
		for p := range touchedProviders {
			kinds = append(kinds, p)
		}
		_ = m.SaveAccountsFor(false, kinds...)
	}
	return addedCount
}

func (m *Manager) RemoveAccount(id string) {
	m.Lock()
	var newAccounts []*Account
	removedProvider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			removedProvider = a.Provider
			continue
		}
		newAccounts = append(newAccounts, a)
	}
	m.accounts = newAccounts
	if m.currentIndex >= len(m.accounts) {
		m.currentIndex = 0
	}
	m.Unlock()

	// 定向落盘:只重写被删账号所属 provider 分区。空 provider(理论上不会发生)兜底走全量。
	if removedProvider != "" {
		_ = m.SaveAccountsFor(false, removedProvider)
	} else {
		_ = m.SaveAccounts(false)
	}
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
			NoQuota:          a.NoQuota,
			MaskedKey:        maskedKeyForAccount(a),
			BaseURL:          a.BaseURL,
			EgressIP:         a.EgressIP,
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

// getAccountByIDLocked 在已持有 m.Lock 或 m.RLock 的临界区内按 ID 查找账号。
// 严禁在此方法内再次调用 m.Lock/RLock，以避免 RWMutex 不可重入导致自死锁。
func (m *Manager) getAccountByIDLocked(id string) *Account {
	for _, a := range m.accounts {
		if a != nil && a.ID == id {
			return a
		}
	}
	return nil
}

func (m *Manager) GetAccountByID(id string) *Account {
	m.RLock()
	defer m.RUnlock()
	return m.getAccountByIDLocked(id)
}

// ============ 账号字段更新(Token/Credits/Overages/Enabled/Tier/2FA) ============

// accountProviderOfLocked 在已持锁前提下返回某 ID 账号的 provider(key)。
// 找不到时返回空串。供定向落盘路径解析 id→provider 用,避免每次 Update* 都全量重写。
// 调用方须已持有 m.Lock 或 m.RLock(读路径亦可,因只读 Provider 字段)。
func (m *Manager) accountProviderOfLocked(id string) string {
	for _, a := range m.accounts {
		if a != nil && a.ID == id {
			return a.Provider
		}
	}
	return ""
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
	provider := ""
	if target != nil {
		provider = target.Provider
	}
	m.RUnlock()

	// Use per-account token lock to update safely without holding the global Manager write lock
	if target != nil {
		target.SetAccessToken(newToken)
		// 定向落盘:只重写该账号所属 provider 分区,不触碰其它号池的大文件。
		_ = m.SaveAccountsFor(true, provider)
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
	provider := ""
	if target != nil {
		provider = target.Provider
	}
	m.RUnlock()

	if target != nil {
		target.SetRefreshToken(newRefresh)
		_ = m.SaveAccountsFor(true, provider)
	}
}

func (m *Manager) UpdateAccountCredits(id string, credits float64) {
	m.Lock()
	changed := false
	provider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			provider = a.Provider
			if a.Credits == nil || *a.Credits != credits {
				a.Credits = &credits
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccountsFor(true, provider)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

func (m *Manager) UpdateAccountOverages(id string, enabled bool) {
	m.Lock()
	changed := false
	provider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			provider = a.Provider
			if a.EnableOverages != enabled {
				a.EnableOverages = enabled
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccountsFor(true, provider)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

func (m *Manager) UpdateAccountEnabled(id string, enabled bool) {
	m.Lock()
	changed := false
	provider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			provider = a.Provider
			if a.Enabled != enabled {
				a.Enabled = enabled
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccountsFor(true, provider)
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
	touchedProviders := map[string]struct{}{}
	for _, a := range m.accounts {
		if a.Email == email {
			a.TwoFASecret = secret
			foundInPool = true
			if a.Provider != "" {
				touchedProviders[a.Provider] = struct{}{}
			}
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

	// 双写:2FA 列表分区 + 被 TwoFASecret 联动改动的 active 账号 provider 分区。
	kinds := make([]string, 0, len(touchedProviders)+1)
	kinds = append(kinds, twoFAPartKind)
	for p := range touchedProviders {
		kinds = append(kinds, p)
	}
	_ = m.SaveAccountsFor(false, kinds...)
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
	var touchedProviders = map[string]struct{}{}
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
				if a.Provider != "" {
					touchedProviders[a.Provider] = struct{}{}
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
		// 双写:2FA 列表分区 + 被 TwoFASecret 字段联动改动的 active 账号 provider 分区。
		// touchedProviders 收集所有邮箱匹配到的 provider(同一 2FA 可能绑定多 provider 账号)。
		kinds := make([]string, 0, len(touchedProviders)+1)
		kinds = append(kinds, twoFAPartKind)
		for p := range touchedProviders {
			kinds = append(kinds, p)
		}
		_ = m.SaveAccountsFor(false, kinds...)
	}
}

func (m *Manager) UpdateAccountTier(id, tier string) {
	m.Lock()
	changed := false
	provider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			provider = a.Provider
			if a.Tier != tier {
				a.Tier = tier
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccountsFor(true, provider)
		if m.OnAccountsUpdated != nil {
			go m.OnAccountsUpdated(m.accounts)
		}
	}
}

func (m *Manager) UpdateAccountNoQuota(id string, noQuota bool) {
	m.Lock()
	changed := false
	provider := ""
	for _, a := range m.accounts {
		if a.ID == id {
			provider = a.Provider
			if a.NoQuota != noQuota {
				a.NoQuota = noQuota
				changed = true
			}
			break
		}
	}
	m.Unlock()

	if changed {
		_ = m.SaveAccountsFor(true, provider)
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
		"workbuddy":   true,
	}
	out := []string{"antigravity", "google", "gcp", "nvidia", "other", "grok", "workbuddy"}

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
