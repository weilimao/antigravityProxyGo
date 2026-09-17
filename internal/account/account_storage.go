package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"antigravity-proxy/internal/fileutil"
)

// account_storage.go: 账号池分区化磁盘读写层。
//
// 设计动机:旧实现把全部号池 tab(antigravity/project/nvidia/grok/other)与 2FA 列表、
// 全部池级配置(PoolMode/LBMode/MaxConcurrency/...)塞进单一 accounts.json。账号数 N
// 增长后,每次单点编辑(改一条 Token / 切一个 LB)都要全量 json.MarshalIndent + 整文件
// os.WriteFile,磁盘 IO 与 fileLock 串行成本随 N 线性放大,前端体感「点一下卡一片」。
//
// 本层把磁盘按 provider 维度切片成 7 个独立文件:
//   - accounts_antigravity.json / accounts_project.json / accounts_nvidia.json /
//     accounts_grok.json / accounts_other.json  → 各 provider 账号数组(外壳 {"accounts":[...]})
//   - accounts_2fa.json                          → 独立 2FA-only 列表(外壳 {"accounts":[...]})
//   - accounts_pool.json                          → 池级配置(17 个非数组字段,无 accounts 数组)
//
// 内存模型不变:m.accounts 仍是单一聚合数组,所有读路径零改动;只在磁盘读写边界做切片。
// 落盘入口 SaveAccountsFor(silent, providers...) 支持定向落盘,SaveAccounts(silent) 退化为全量。
//
// conc:
//   - 一次性迁移由存在性判定(旧 accounts.json 存在 & 无任何 accounts_*.json 分区文件)触发,
//     迁移后把 accounts.json 重命名为 accounts.json.bak 而非删除,保留手动回滚入口;
//   - 所有写盘共用 m.fileLock 串行化(与旧 SaveAccounts 同范式),分区后单次写盘体积骤降,
//     串行成本可忽略,无需引入 per-partition 锁即能消除「编辑一卡一片」的主因;
//   - 取数走 m.RLock(读锁),释锁后再取 fileLock 写盘,避免「持读锁写文件」自死锁。

// ============ 分区枚举与文件命名 ============

// partitionKind 标识分区类型:provider 分区 + "2fa" + "pool"。
// provider 分区的 kind 即 provider 名(antigravity/project/nvidia/grok/other);
// "2fa" 对应独立 2FA 列表;"pool" 对应池级配置。
const (
	poolPartKind = "pool"
	twoFAPartKind = "2fa"
)

// knownProviderPartitions 是按 provider 切片的分区白名单(用于全量加载/全量写盘的确定性遍历顺序)。
// 顺序固定便于测试断言与迁移幂等;与前端 tab 顺序对齐(antigravity/project/nvidia/other/grok/workbuddy)。
var knownProviderPartitions = []string{"antigravity", "project", "nvidia", "other", "grok", "workbuddy"}

// allPartitionKinds 返回全部分区 kind(5 provider + 2fa + pool),用于全量写盘与存在性探测。
func allPartitionKinds() []string {
	out := make([]string, 0, len(knownProviderPartitions)+2)
	out = append(out, knownProviderPartitions...)
	out = append(out, twoFAPartKind, poolPartKind)
	return out
}

// partitionFileName 按 kind 拼磁盘文件名(userDataPath 下)。
// provider 与 2FA 分区统一 accounts_<kind>.json;pool 分区用 accounts_pool.json。
func partitionFileName(kind string) string {
	if kind == poolPartKind {
		return "accounts_pool.json"
	}
	return "accounts_" + kind + ".json"
}

// partitionFilePath 拼分区文件绝对路径。
func (m *Manager) partitionFilePath(kind string) string {
	if m.userDataPath == "" {
		return ""
	}
	return filepath.Join(m.userDataPath, partitionFileName(kind))
}


// legacyAccountsFilePath 返回旧单文件路径(仅迁移判定与重命名用)。
// 复用 m.accountsFilePath(由 Init/UpdatePath 设为 userDataPath/accounts.json),避免冗余字段。
func (m *Manager) legacyAccountsFilePath() string {
	return m.accountsFilePath
}

// ============ 分区文件外壳 ============

// accountsFileShell 是账号分区文件(含 provider 分区与 2FA 分区)的统一磁盘外壳。
// 与现行 accounts:export-all / accounts:export-single 的导出形态逐字节一致(仅最外层 "accounts"),
// 便于导入回路与人工检视兼容。池配置分区不走此壳(见 poolConfigOnDisk)。
type accountsFileShell struct {
	Accounts []*Account `json:"accounts"`
}

// poolConfigOnDisk 是 accounts_pool.json 的磁盘形态:仅承载池级配置 17 字段,不含账号数组。
// 字段名与 AccountsData 完全一致,确保人工检视与未来回退至单文件时的可读性。
//
// 注意:此处刻意复用 AccountsData 类型会导致 omitempty 串(如 twofa_accounts,omitempty)
// 序列化时丢字段;故单独定义一份「无账号数组」的池配置投影,字段 tag 与 AccountsData 对齐。
type poolConfigOnDisk struct {
	PoolMode                  bool            `json:"poolMode"`
	ProjectPoolMode           bool            `json:"projectPoolMode"`
	GeminiCliPoolMode          bool            `json:"geminiCliPoolMode"`
	ActiveChannel             string          `json:"activeChannel"`
	OtherLBModes              map[string]string `json:"otherLbModes,omitempty"`
	NvidiaLBMode              string          `json:"nvidiaLbMode,omitempty"`
	GrokLBMode                string          `json:"grokLbMode,omitempty"`
	NvidiaMaxConcurrency      int             `json:"nvidiaMaxConcurrency,omitempty"`
	AntigravityMaxConcurrency int             `json:"antigravityMaxConcurrency,omitempty"`
	AntigravityCliVersion     string          `json:"antigravityCliVersion,omitempty"`
	ProjectMaxConcurrency     int             `json:"projectMaxConcurrency,omitempty"`
	OtherMaxConcurrency       map[string]int  `json:"otherMaxConcurrency,omitempty"`
	OtherWorkerProxyURLs    map[string]string `json:"otherWorkerProxyUrls,omitempty"`
	OtherWorkerProxyEnabled map[string]bool   `json:"otherWorkerProxyEnabled,omitempty"`
	// OtherCooldownRules 按 GroupID 持久化 Other 号池各组自定义冷却策略(状态码→冷却时长+模型过滤)。
	OtherCooldownRules map[string]*OtherCooldownRule `json:"otherCooldownRules,omitempty"`
	GrokMaxConcurrency        int             `json:"grokMaxConcurrency,omitempty"`
	GrokCliVersion            string          `json:"grokCliVersion,omitempty"`
	GrokQuotaCooldownHours   int             `json:"grokQuotaCooldownHours,omitempty"`
	WorkBuddyLBMode           string          `json:"workbuddyLbMode,omitempty"`
	WorkBuddyMaxConcurrency   int             `json:"workbuddyMaxConcurrency,omitempty"`
}

// ============ 写盘:定向/全量 ============

// SaveAccountsFor 定向落盘指定分区。providers 为空时退化为全量写盘(等价旧 SaveAccounts)。
// kind 取值:provider 名(antigravity/project/nvidia/grok/other)、"2fa"、"pool"。
//
// 调用方语义约定:
//   - 单账号字段更新(UpdateAccountEnabled/Credits/...):先解析 id→provider,再 SaveAccountsFor(true, provider)
//   - 池配置变更(SetNvidiaPoolMode/SetNvidiaLBMode/...17 处):SaveAccountsFor(false, "pool")
//   - 2FA 联动(UpdateAccount2FASecret 同时改 2FA 列表与 active 账号字段):SaveAccountsFor(false, "2fa", provider)
//   - ImportAccountsList 跨多 provider:按受影响 provider 集合批量落盘
//
// silent=true 时跳过 OnAccountsUpdated 回调(与旧 SaveAccounts 同口径,供 token 刷新等静默路径用)。
// 返回首个写盘错误(遇错即返回,但已落盘的分区不回滚——单分区失败不应阻塞其它分区持久化)。
func (m *Manager) SaveAccountsFor(silent bool, kinds ...string) error {
	if len(kinds) == 0 {
		// 全量:5 provider + 2fa + pool
		kinds = allPartitionKinds()
	}

	for _, kind := range kinds {
		if err := m.saveOnePartition(kind); err != nil {
			return err
		}
	}

	if !silent && m.OnAccountsUpdated != nil {
		go m.OnAccountsUpdated(m.accounts)
	}
	return nil
}

// saveOnePartition 落单一分区。按 kind 分发到 provider / 2fa / pool 三路。
func (m *Manager) saveOnePartition(kind string) error {
	path := m.partitionFilePath(kind)
	if path == "" {
		return fmt.Errorf("partition %q: userDataPath not initialized", kind)
	}

	var (
		data []byte
		err  error
	)

	switch kind {
	case poolPartKind:
		data, err = m.marshalPoolConfig()
	case twoFAPartKind:
		data, err = m.marshalAccountsShell(m.collect2FAAccounts())
	default:
		// provider 分区:从聚合数组按 provider 过滤
		data, err = m.marshalAccountsShell(m.collectProviderAccounts(kind))
	}
	if err != nil {
		return fmt.Errorf("marshal partition %q: %w", kind, err)
	}

	m.fileLock.Lock()
	// 原子写 + .bak 快照:磁盘写满时 tmp 写失败,原分区文件分毫未动(治本,防截断丢数据)。
	err = fileutil.WriteFileAtomicWithBak(path, data, 0644)
	m.fileLock.Unlock()
	if err != nil {
		return fmt.Errorf("write partition %q: %w", kind, err)
	}
	return nil
}

// marshalAccountsShell 取数(读锁)→ 释锁 → 序列化账号分区文件。
// 读锁不持有到序列化完成,避免「持读锁做重活」;序列化是纯内存操作,释锁后无并发风险
// (Account 指针已拷贝出切片副本,切片内元素仍是共享指针,但落盘期间不会被增删改且无并发写盘竞态)。
func (m *Manager) marshalAccountsShell(accounts []*Account) ([]byte, error) {
	if accounts == nil {
		accounts = []*Account{}
	}
	shell := accountsFileShell{Accounts: accounts}
	return json.MarshalIndent(shell, "", "  ")
}

// collectProviderAccounts 在读锁内按 provider 过滤出该分区账号(返回切片,元素为原指针)。
// provider 分区文件只承载该 provider 的账号,故跨 provider 的编辑不误伤其它分区。
func (m *Manager) collectProviderAccounts(provider string) []*Account {
	m.RLock()
	defer m.RUnlock()
	var out []*Account
	for _, a := range m.accounts {
		if a != nil && strings.EqualFold(a.Provider, provider) {
			out = append(out, a)
		}
	}
	return out
}

// collect2FAAccounts 在读锁内返回独立 2FA 列表副本(元素为原指针)。
func (m *Manager) collect2FAAccounts() []*Account {
	m.RLock()
	defer m.RUnlock()
	if len(m.twofaAccounts) == 0 {
		return []*Account{}
	}
	out := make([]*Account, len(m.twofaAccounts))
	copy(out, m.twofaAccounts)
	return out
}

// marshalPoolConfig 取池级配置(读锁)→ 释锁 → 序列化 pool 分区文件。
// 池配置字段全部在 Manager 上,不涉及账号数组,落盘体积恒定(与账号数 N 无关)。
func (m *Manager) marshalPoolConfig() ([]byte, error) {
	m.RLock()
	cfg := poolConfigOnDisk{
		PoolMode:                  m.poolMode,
		ProjectPoolMode:           m.projectPoolMode,
		GeminiCliPoolMode:         m.geminiCliPoolMode,
		ActiveChannel:             m.activeChannel,
		OtherLBModes:              m.otherLBModes,
		NvidiaLBMode:              m.nvidiaLBMode,
		GrokLBMode:                m.grokLBMode,
		NvidiaMaxConcurrency:      m.nvidiaMaxConcurrency,
		AntigravityMaxConcurrency: m.antigravityMaxConcurrency,
		AntigravityCliVersion:     m.antigravityCliVersion,
		ProjectMaxConcurrency:     m.projectMaxConcurrency,
		OtherMaxConcurrency:       m.otherMaxConcurrency,
		OtherWorkerProxyURLs:    m.otherWorkerProxyURLs,
		OtherWorkerProxyEnabled: m.otherWorkerProxyEnabled,
		OtherCooldownRules:      m.otherCooldownRules,
		GrokMaxConcurrency:        m.grokMaxConcurrency,
		GrokCliVersion:            m.grokCliVersion,
		GrokQuotaCooldownHours:   m.grokQuotaCooldownHours,
		WorkBuddyLBMode:           m.workbuddyLBMode,
		WorkBuddyMaxConcurrency:   m.workbuddyMaxConcurrency,
	}
	m.RUnlock()
	return json.MarshalIndent(cfg, "", "  ")
}

// ============ 加载:分区读 + 一次性迁移 ============

// loadFromPartitions 逐文件读 7 个分区并合并进内存。须在持有 m.Lock(写锁)的临界区内调用
// (由 LoadAccounts 统一持锁,与旧实现一致)。文件缺失视为该分区空,静默跳过。
func (m *Manager) loadFromPartitions() {
	// 1. 账号聚合数组:逐 provider 分区合并,保留 knownProviderPartitions 顺序作为稳定插入序;
	//    同时兜底读入任何未知 provider 的分区文件(若未来扩展 provider 时旧文件已落盘)。
	m.accounts = nil
	for _, kind := range knownProviderPartitions {
		accs := m.readAccountsPartition(kind)
		m.accounts = append(m.accounts, accs...)
	}
	// 2FA 列表
	m.twofaAccounts = m.readAccountsPartition(twoFAPartKind)
	if m.twofaAccounts == nil {
		m.twofaAccounts = make([]*Account, 0)
	}
	// 池配置
	m.loadPoolConfigIntoMemory()
}

// readAccountsPartition 读单个账号分区文件(provider 或 2fa)。文件缺失返回 nil;
// 主文件损坏自动回退 .bak 快照,双毁时主文件被旁移为 .corrupt(保留字节),返回 nil。
func (m *Manager) readAccountsPartition(kind string) []*Account {
	path := m.partitionFilePath(kind)
	if path == "" {
		return nil
	}
	var shell accountsFileShell
	fromBak, err := fileutil.ReadJSONWithBak(path, &shell)
	if err != nil {
		// 全新安装(分区文件尚不存在)静默;真损坏才告警。
		if !os.IsNotExist(err) {
			fmt.Printf("[AccountManager] ⚠️ 分区 %s 加载失败: %v\n", partitionFileName(kind), err)
		}
		return nil
	}
	if fromBak {
		fmt.Printf("[AccountManager] ⚠️ 分区 %s 主文件损坏,已从 .bak 快照恢复\n", partitionFileName(kind))
	}
	return shell.Accounts
}

// loadPoolConfigIntoMemory 读 accounts_pool.json 并把 17 个池字段灌进 Manager 内存字段。
// 与旧 LoadAccounts 中解析 AccountsData 的字段规整逻辑同口径(小写规范化、负数钳 0 等)。
func (m *Manager) loadPoolConfigIntoMemory() {
	path := m.partitionFilePath(poolPartKind)
	if path == "" {
		return
	}
	var cfg poolConfigOnDisk
	fromBak, err := fileutil.ReadJSONWithBak(path, &cfg)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Printf("[AccountManager] ⚠️ 加载 %s 失败: %v\n", partitionFileName(poolPartKind), err)
		}
		return
	}
	if fromBak {
		fmt.Printf("[AccountManager] ⚠️ %s 主文件损坏,已从 .bak 快照恢复\n", partitionFileName(poolPartKind))
	}

	m.poolMode = cfg.PoolMode
	m.projectPoolMode = cfg.ProjectPoolMode
	m.geminiCliPoolMode = cfg.GeminiCliPoolMode
	m.activeChannel = cfg.ActiveChannel
	if cfg.OtherLBModes != nil {
		// 载入时统一规范化为小写 groupID,保证与 SetOtherLBMode 的 key 口径一致。
		m.otherLBModes = make(map[string]string, len(cfg.OtherLBModes))
		for gid, mode := range cfg.OtherLBModes {
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
	m.nvidiaLBMode = cfg.NvidiaLBMode
	m.grokLBMode = cfg.GrokLBMode
	m.workbuddyLBMode = cfg.WorkBuddyLBMode
	m.nvidiaMaxConcurrency = cfg.NvidiaMaxConcurrency
	m.grokMaxConcurrency = cfg.GrokMaxConcurrency
	m.workbuddyMaxConcurrency = cfg.WorkBuddyMaxConcurrency
	m.grokCliVersion = strings.TrimSpace(cfg.GrokCliVersion)
	m.grokQuotaCooldownHours = cfg.GrokQuotaCooldownHours
	if m.grokQuotaCooldownHours < 0 {
		m.grokQuotaCooldownHours = 0
	}
	m.antigravityMaxConcurrency = cfg.AntigravityMaxConcurrency
	m.antigravityCliVersion = strings.TrimSpace(cfg.AntigravityCliVersion)
	m.projectMaxConcurrency = cfg.ProjectMaxConcurrency
	if cfg.OtherMaxConcurrency != nil {
		m.otherMaxConcurrency = make(map[string]int, len(cfg.OtherMaxConcurrency))
		for gid, v := range cfg.OtherMaxConcurrency {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" {
				continue
			}
			if v < 0 {
				v = 0
			}
			m.otherMaxConcurrency[lgid] = v
		}
	}
	if cfg.OtherWorkerProxyURLs != nil {
		m.otherWorkerProxyURLs = make(map[string]string, len(cfg.OtherWorkerProxyURLs))
		for gid, u := range cfg.OtherWorkerProxyURLs {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" {
				continue
			}
			m.otherWorkerProxyURLs[lgid] = strings.TrimSpace(u)
		}
	}
	if cfg.OtherWorkerProxyEnabled != nil {
		m.otherWorkerProxyEnabled = make(map[string]bool, len(cfg.OtherWorkerProxyEnabled))
		for gid, en := range cfg.OtherWorkerProxyEnabled {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" {
				continue
			}
			m.otherWorkerProxyEnabled[lgid] = en
		}
	}
	if cfg.OtherCooldownRules != nil {
		// 载入时逐组规整(小写 groupID + normalizeOtherCooldownRule),与 SetOtherCooldownRule 落盘口径一致,
		// 兜底手工编辑 accounts_pool.json 写入的脏数据(非法状态码/越界时长)。
		m.otherCooldownRules = make(map[string]*OtherCooldownRule, len(cfg.OtherCooldownRules))
		for gid, r := range cfg.OtherCooldownRules {
			lgid := strings.ToLower(strings.TrimSpace(gid))
			if lgid == "" || r == nil {
				continue
			}
			nr := normalizeOtherCooldownRule(r)
			if nr == nil || (!nr.Enabled && len(nr.StatusCodes) == 0 && len(nr.Models) == 0) {
				continue
			}
			m.otherCooldownRules[lgid] = nr
		}
	}
	if m.activeChannel == "gemini-cli" {
		m.activeChannel = "antigravity"
	}
}

// ============ 一次性迁移:旧单文件 → 7 分区 ============

// shouldMigrateLegacy 判定是否需要迁移:旧 accounts.json 存在且无任何 accounts_*.json 分区文件。
// 两个条件同时满足才迁,避免「已分区化但用户又手工放进旧 accounts.json」时误删分区。
func (m *Manager) shouldMigrateLegacy() bool {
	legacyPath := m.legacyAccountsFilePath()
	if legacyPath == "" {
		return false
	}
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		return false // 旧文件不存在,无迁移来源
	}
	// 任一分区文件存在即视为已分区化,不迁移
	for _, kind := range allPartitionKinds() {
		if _, err := os.Stat(m.partitionFilePath(kind)); err == nil {
			return false
		}
	}
	return true
}

// migrateLegacyFile 读旧 accounts.json(全量 AccountsData),按 provider 拆分落盘 7 个分区,
// 随后把 accounts.json 重命名为 accounts.json.bak(保留不删,可手动回滚)。
// 须在持有 m.Lock(写锁)的临界区内调用,且此时内存尚未填充(由调用方在迁移后 loadFromPartitions)。
func (m *Manager) migrateLegacyFile() error {
	legacyPath := m.legacyAccountsFilePath()
	if legacyPath == "" {
		return fmt.Errorf("userDataPath not initialized")
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return fmt.Errorf("read legacy accounts.json: %w", err)
	}
	var parsed AccountsData
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse legacy accounts.json: %w", err)
	}

	// 把旧全量账号按 provider 分桶 + 2FA 分桶。桶内元素为原指针,落盘后回滚不影响内存。
	providerBuckets := make(map[string][]*Account, len(knownProviderPartitions))
	for _, p := range knownProviderPartitions {
		providerBuckets[p] = nil
	}
	for _, a := range parsed.Accounts {
		if a == nil {
			continue
		}
		matched := false
		for _, p := range knownProviderPartitions {
			if strings.EqualFold(a.Provider, p) {
				providerBuckets[p] = append(providerBuckets[p], a)
				matched = true
				break
			}
		}
		if !matched {
			// 未知 provider(如历史脏数据 "2fa" 落在 accounts 数组):塞进 antigravity 兜底分区,
			// 由后续 LoadAccounts 的内存层迁移逻辑(2FA-only 分离、provider 兜底推断)正规化。
			// 这是迁移路径下的防御性兜底,正常账号池不会触发。
			providerBuckets["antigravity"] = append(providerBuckets["antigravity"], a)
		}
	}
	// 2FA 分桶:旧 AccountsData.TwoFAAccounts 直进 2FA 分区。
	twoFABucket := parsed.TwoFAAccounts
	if twoFABucket == nil {
		twoFABucket = []*Account{}
	}

	// 逐 partition 写盘。沿用 m.fileLock 串行化(此时仍持 m.Lock 写锁,fileLock 是另一把专用锁,
	// 与写锁无嵌套死锁风险——fileLock 旧 SaveAccounts 同范式)。
	for _, p := range knownProviderPartitions {
		shell := accountsFileShell{Accounts: providerBuckets[p]}
		if shell.Accounts == nil {
			shell.Accounts = []*Account{}
		}
		bytesData, mErr := json.MarshalIndent(shell, "", "  ")
		if mErr != nil {
			return fmt.Errorf("marshal partition %s: %w", p, mErr)
		}
		m.fileLock.Lock()
		wErr := fileutil.WriteFileAtomicWithBak(m.partitionFilePath(p), bytesData, 0644)
		m.fileLock.Unlock()
		if wErr != nil {
			return fmt.Errorf("write partition %s: %w", p, wErr)
		}
	}
	// 2FA 分区
	shell := accountsFileShell{Accounts: twoFABucket}
	bytesData, mErr := json.MarshalIndent(shell, "", "  ")
	if mErr != nil {
		return fmt.Errorf("marshal partition 2fa: %w", mErr)
	}
	m.fileLock.Lock()
	wErr := fileutil.WriteFileAtomicWithBak(m.partitionFilePath(twoFAPartKind), bytesData, 0644)
	m.fileLock.Unlock()
	if wErr != nil {
		return fmt.Errorf("write partition 2fa: %w", wErr)
	}
	// pool 分区:复用 marshalPoolConfig 的字段投影。此时内存字段尚未灌,需用 parsed 的池配置直接建 cfg。
	cfg := poolConfigOnDisk{
		PoolMode:                  parsed.PoolMode,
		ProjectPoolMode:           parsed.ProjectPoolMode,
		GeminiCliPoolMode:         parsed.GeminiCliPoolMode,
		ActiveChannel:             parsed.ActiveChannel,
		OtherLBModes:              parsed.OtherLBModes,
		NvidiaLBMode:              parsed.NvidiaLBMode,
		GrokLBMode:                parsed.GrokLBMode,
		NvidiaMaxConcurrency:      parsed.NvidiaMaxConcurrency,
		AntigravityMaxConcurrency: parsed.AntigravityMaxConcurrency,
		AntigravityCliVersion:     parsed.AntigravityCliVersion,
		ProjectMaxConcurrency:     parsed.ProjectMaxConcurrency,
		OtherMaxConcurrency:       parsed.OtherMaxConcurrency,
		GrokMaxConcurrency:        parsed.GrokMaxConcurrency,
		GrokCliVersion:            parsed.GrokCliVersion,
		GrokQuotaCooldownHours:   parsed.GrokQuotaCooldownHours,
		WorkBuddyLBMode:           parsed.WorkBuddyLBMode,
		WorkBuddyMaxConcurrency:   parsed.WorkBuddyMaxConcurrency,
	}
	poolBytes, pErr := json.MarshalIndent(cfg, "", "  ")
	if pErr != nil {
		return fmt.Errorf("marshal partition pool: %w", pErr)
	}
	m.fileLock.Lock()
	pWriteErr := fileutil.WriteFileAtomicWithBak(m.partitionFilePath(poolPartKind), poolBytes, 0644)
	m.fileLock.Unlock()
	if pWriteErr != nil {
		return fmt.Errorf("write partition pool: %w", pWriteErr)
	}

	// 把旧 accounts.json 重命名为 accounts.json.bak。若 .bak 已存在则覆盖(迁移幂等)。
	bakPath := legacyPath + ".bak"
	_ = os.Remove(bakPath) // 清掉旧 .bak 避免 Rename 失败
	if rErr := os.Rename(legacyPath, bakPath); rErr != nil {
		// 重命名失败极罕见(Windows 下旧文件被占用)。不阻断迁移:分区文件已落盘,
		// 旧 accounts.json 留在原地,下次启动会再次触发 shouldMigrateLegacy(false,因分区已存在),
		// 故不会重复迁移,也不会丢失数据。仅记日志。
		fmt.Printf("[AccountManager] migrate: rename legacy → .bak failed (partitioned files already written, legacy kept in place): %v\n", rErr)
	} else {
		fmt.Printf("[AccountManager] migrate: split accounts.json → 7 partition files, legacy moved to accounts.json.bak\n")
	}
	return nil
}
