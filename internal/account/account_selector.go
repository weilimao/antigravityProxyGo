package account

import (
	"strings"
	"time"
)

// account_selector.go 收纳账号轮询选择、冷却写入、可用性过滤与号池模式开关。
// 拆分自原 account.go:此处只保留「选号 + 冷却 + 通道模式 + 模型类别」逻辑;
// Manager 容器/CRUD 见 account_manager.go,定时监控/Token 刷新见 account_monitor.go。
//
// conc:
//   - GetNextAccount/SetAccountCooldown 持 Manager 写锁操作账号冷却字段;
//   - 冷却到期清除在持锁内执行,异步 SaveAccounts 避免持锁写文件死锁。

// ============ 模型类别(claude/gemini/nvidia 三族解耦) ============

func (m *Manager) GetModelCategory(modelName string) string {
	name := strings.ToLower(modelName)
	if strings.Contains(name, "claude") {
		return "claude"
	}
	return "gemini"
}

// GetModelCategoryByProvider 根据账号 provider 与模型名共同确定冷却类别。
// NVIDIA 号池走独立的 "nvidia" 冷却类别，与 gemini/claude 解耦：
// 避免把满额 NVIDIA 配额误判为 "gemini 配额恢复"，进而在 OnQuotaRestored
// 回调里刷屏打印无关的自动触发日志。其余 provider 维持原 gemini/claude 二分。
func (m *Manager) GetModelCategoryByProvider(provider, modelName string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "nvidia" {
		return "nvidia"
	}
	if p == "other" {
		return "other"
	}
	return m.GetModelCategory(modelName)
}

// ============ 号池模式开关(同构互斥:开一个关其余) ============

func (m *Manager) SetPoolMode(enabled bool) {
	m.Lock()
	m.poolMode = enabled
	if enabled {
		m.projectPoolMode = false
		m.nvidiaPoolMode = false
		m.geminiCliPoolMode = false
		m.otherPoolMode = false
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
		m.nvidiaPoolMode = false
		m.geminiCliPoolMode = false
		m.otherPoolMode = false
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

func (m *Manager) SetGeminiCliPoolMode(enabled bool) {
}

func (m *Manager) GetGeminiCliPoolMode() bool {
	return false
}

func (m *Manager) IsPoolModeForActiveChannel() bool {
	m.RLock()
	defer m.RUnlock()
	switch m.activeChannel {
	case "antigravity":
		return m.poolMode
	case "project":
		return m.projectPoolMode
	case "nvidia":
		return m.nvidiaPoolMode
	case "other":
		return m.otherPoolMode
	}
	return false
}

func (m *Manager) SetActiveChannel(channel string) {
	m.Lock()
	if channel == "antigravity" || channel == "project" || channel == "nvidia" || channel == "other" {
		if channel == "project" && m.poolMode {
			m.Unlock()
			return
		}
		if channel == "antigravity" && m.projectPoolMode {
			m.Unlock()
			return
		}
		if channel == "nvidia" && (m.poolMode || m.projectPoolMode) {
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

// ============ 账号轮询选择(GetNextAccount) ============

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
	} /* else if currentChannel == "gemini-cli" {
		isPool = m.geminiCliPoolMode
	} */

	// 筛选出当前通道所有启用的账号 (且已授权登录)
	var activeAccounts []*Account
	for _, a := range m.accounts {
		accountChannel := a.Provider
		if accountChannel == currentChannel && a.Enabled {
			if a.AccessToken != "" || a.RefreshToken != "" {
				activeAccounts = append(activeAccounts, a)
			}
		}
	}

	if len(activeAccounts) == 0 {
		return nil
	}

	category := m.GetModelCategory(modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)

	isAvailable := func(acc *Account) bool {
		cooldownUntil := int64(0)
		if acc.Cooldowns != nil {
			if v, ok := acc.Cooldowns[category]; ok {
				cooldownUntil = v
			}
		} else {
			cooldownUntil = acc.CooldownUntil
		}
		hasOverages := acc.EnableOverages && acc.Credits != nil && *acc.Credits > 0
		return cooldownUntil == 0 || now >= cooldownUntil || hasOverages
	}

	if !isPool {
		// 单账号模式，返回通道中的第一个（如果可用）
		acc := activeAccounts[0]
		if isAvailable(acc) {
			// 清除已过期的冷静期
			cooldownUntil := int64(0)
			if acc.Cooldowns != nil {
				if v, ok := acc.Cooldowns[category]; ok {
					cooldownUntil = v
				}
			} else {
				cooldownUntil = acc.CooldownUntil
			}
			if cooldownUntil > 0 && now >= cooldownUntil {
				delete(acc.Cooldowns, category)
				acc.CooldownUntil = 0
				for _, v := range acc.Cooldowns {
					if acc.CooldownUntil == 0 || v < acc.CooldownUntil {
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
			cooldownUntil := int64(0)
			if acc.Cooldowns != nil {
				if v, ok := acc.Cooldowns[category]; ok {
					cooldownUntil = v
				}
			} else {
				cooldownUntil = acc.CooldownUntil
			}
			if cooldownUntil > 0 && now >= cooldownUntil {
				delete(acc.Cooldowns, category)
				acc.CooldownUntil = 0
				for _, v := range acc.Cooldowns {
					if acc.CooldownUntil == 0 || v < acc.CooldownUntil {
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

// ============ 冷却写入(SetAccountCooldown / ForChannel) ============

func (m *Manager) SetAccountCooldown(id string, untilTimeMs int64, modelName string) {
	m.Lock()
	category := ""
	changed := false
	for _, a := range m.accounts {
		if a.ID == id {
			category = m.GetModelCategoryByProvider(a.Provider, modelName)
			if a.Cooldowns == nil {
				a.Cooldowns = make(map[string]int64)
			}
			a.Cooldowns[category] = untilTimeMs

			var minCooldown int64 = 0
			for _, v := range a.Cooldowns {
				if minCooldown == 0 || v < minCooldown {
					minCooldown = v
				}
			}
			a.CooldownUntil = minCooldown
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

func (m *Manager) SetAccountCooldownForChannel(id string, cooldownUntil int64, channel string, model string) {
	m.SetAccountCooldown(id, cooldownUntil, model)
}

// ============ 可用账号过滤(只读) ============

func (m *Manager) GetAvailableAccountsForChannel(channel string, modelName string) []*Account {
	return m.GetAvailableAccountsForChannelAndGroup(channel, "", modelName)
}

// GetAvailableAccountsForChannelAndGroup 是 GetAvailableAccountsForChannel 的组维度扩展:
// 在 Provider 过滤基础上叠加 GroupID 过滤(Other 号池按组选号)。groupID 为空时退化为原行为,
// 保持对既有 NVIDIA/Google 族调用方零影响。冷却与可用性口径与 GetAvailableAccountsForChannel 完全一致。
func (m *Manager) GetAvailableAccountsForChannelAndGroup(channel, groupID string, modelName string) []*Account {
	m.RLock()
	defer m.RUnlock()

	if len(m.accounts) == 0 {
		return nil
	}

	category := m.GetModelCategoryByProvider(channel, modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)
	gid := strings.ToLower(strings.TrimSpace(groupID))

	var list []*Account
	for _, a := range m.accounts {
		accountChannel := a.Provider
		if accountChannel != channel || !a.Enabled {
			continue
		}
		// Other 号池按 GroupID 细分:groupID 非空时仅选该组账号。groupID 为空时不过滤(向后兼容)。
		if gid != "" && strings.TrimSpace(a.GroupID) != gid {
			continue
		}
		if a.AccessToken == "" && a.RefreshToken == "" {
			continue
		}

		cooldownUntil := int64(0)
		if a.Cooldowns != nil {
			if v, ok := a.Cooldowns[category]; ok {
				cooldownUntil = v
			}
		} else {
			cooldownUntil = a.CooldownUntil
		}
		hasOverages := a.EnableOverages && a.Credits != nil && *a.Credits > 0
		if cooldownUntil == 0 || now >= cooldownUntil || hasOverages {
			list = append(list, a)
		}
	}
	return list
}

func (m *Manager) GetAvailableAccounts(modelName string) []*Account {
	m.RLock()
	channel := m.activeChannel
	m.RUnlock()
	return m.GetAvailableAccountsForChannel(channel, modelName)
}

// UpdateAccountCooldownFromQuota 根据配额桶把「已耗尽/已恢复」翻译成账号冷却写入。
// 冷却类别与 gemini/claude/nvidia 三族解耦:
func (m *Manager) UpdateAccountCooldownFromQuota(id string, buckets []QuotaBucket) bool {
	acc := m.GetAccountByID(id)
	if acc == nil || len(buckets) == 0 {
		return false
	}

	m.ResetAccountError(id)

	// 冷却类别与 gemini/claude/nvidia 三族解耦：
	// NVIDIA 号池走独立 "nvidia" 冷却键，避免其满额配额被误判为 gemini 恢复。
	isNvidiaAcc := strings.EqualFold(acc.Provider, "nvidia")

	var geminiExhausted, claudeExhausted, nvidiaExhausted bool
	var geminiResetTime, claudeResetTime, nvidiaResetTime int64

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

	// classifyBucket 按 bucket 归属与账号 provider 判定冷却类别。
	// NVIDIA bucket 含 "nvidia" 标识；其余按 claude/gemini 二分。
	classifyBucket := func(b QuotaBucket) string {
		lowerGroup := strings.ToLower(b.Group)
		lowerModelID := strings.ToLower(b.ModelID)
		if strings.Contains(lowerGroup, "nvidia") || strings.Contains(lowerModelID, "nvidia") {
			return "nvidia"
		}
		if strings.Contains(lowerGroup, "claude") || strings.Contains(lowerModelID, "claude") {
			return "claude"
		}
		// NVIDIA 账号的 bucket 在缺少显式标识时也归 nvidia，保持与冷却写入端一致
		if isNvidiaAcc {
			return "nvidia"
		}
		return "gemini"
	}

	for _, b := range buckets {
		category := classifyBucket(b)
		isExhausted := b.RemainingFraction == 0 || b.RemainPercent == 0
		if !isExhausted {
			continue
		}
		resetMs := parseTime(b.ResetTime)
		switch category {
		case "claude":
			claudeExhausted = true
			if resetMs > claudeResetTime {
				claudeResetTime = resetMs
			}
		case "nvidia":
			nvidiaExhausted = true
			if resetMs > nvidiaResetTime {
				nvidiaResetTime = resetMs
			}
		default:
			geminiExhausted = true
			if resetMs > geminiResetTime {
				geminiResetTime = resetMs
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
	var restoredCategories []string

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
				restoredCategories = append(restoredCategories, cat)
			}
		}
	}

	updateCatCooldown("gemini", geminiExhausted, geminiResetTime)
	updateCatCooldown("claude", claudeExhausted, claudeResetTime)

	// NVIDIA 号池走独立的 "nvidia" 冷却类别，与 gemini/claude 解耦：
	// 仅当本次刷新观察到"nvidia 冷却键实际存在且本次配额已满额恢复"时，
	// updateCatCooldown 才会记入 restoredCategories 并触发 OnQuotaRestored；
	// 无前置冷却的常态满额刷新不再误触发自动刷新日志。
	if isNvidiaAcc {
		updateCatCooldown("nvidia", nvidiaExhausted, nvidiaResetTime)
	}

	var minCooldown int64 = 0
	for _, v := range acc.Cooldowns {
		if minCooldown == 0 || v < minCooldown {
			minCooldown = v
		}
	}
	newCooldownUntil := minCooldown

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

		if len(restoredCategories) > 0 && m.OnQuotaRestored != nil {
			go m.OnQuotaRestored(id, restoredCategories)
		}
	}

	return changed
}

// ============ NVIDIA 专属(LB 模式 + 号池模式 + 只读探针) ============

func (m *Manager) GetNvidiaLBMode() string {
	m.RLock()
	defer m.RUnlock()
	if m.nvidiaLBMode == "" {
		return "round-robin"
	}
	return m.nvidiaLBMode
}

func (m *Manager) SetNvidiaLBMode(mode string) {
	m.Lock()
	m.nvidiaLBMode = mode
	m.Unlock()
	_ = m.SaveAccounts(false)
}

// ============ 单账号最大并发数限制(四池 Get/Set + 计数器转发) ============
//
// 语义约定(对齐 LB 模式 Get/Set 范式):
//   - Get:v<=0(0/负数)= 未配置,回退默认 10;返回正数即单调并发上限。
//   - Set:负数置 0(等同回退,持久化为 0 经 omitempty 不落盘但语义清晰);Other 按 groupID 小写键。
//   - 默认值 10 是产品约定(见 accounts.json 配置项),既有 accounts.json 无该字段时自动回退,零迁移。
// 选号链路使用:Get 后把上限作为 limit 传 FilterByConcurrency 过滤超限账号,全满则 LeastLoaded 超额降级。
// 见 concurrency.go 与各 relay/proxy 选号接入点。

// defaultMaxConcurrency 是各号池单账号并发上限的「未配置」回退值:既有号池默认按 10 并发限流,
// 与产品基线一致。0/负数视作未配置(序列化经 omitempty 不落盘),Get 时回退此值。
const defaultMaxConcurrency = 10

func (m *Manager) GetNvidiaMaxConcurrency() int {
	m.RLock()
	v := m.nvidiaMaxConcurrency
	m.RUnlock()
	if v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

func (m *Manager) SetNvidiaMaxConcurrency(v int) {
	if v < 0 {
		v = 0
	}
	m.Lock()
	m.nvidiaMaxConcurrency = v
	m.Unlock()
	_ = m.SaveAccounts(false)
}

func (m *Manager) GetAntigravityMaxConcurrency() int {
	m.RLock()
	v := m.antigravityMaxConcurrency
	m.RUnlock()
	if v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

func (m *Manager) SetAntigravityMaxConcurrency(v int) {
	if v < 0 {
		v = 0
	}
	m.Lock()
	m.antigravityMaxConcurrency = v
	m.Unlock()
	_ = m.SaveAccounts(false)
}

func (m *Manager) GetProjectMaxConcurrency() int {
	m.RLock()
	v := m.projectMaxConcurrency
	m.RUnlock()
	if v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

func (m *Manager) SetProjectMaxConcurrency(v int) {
	if v < 0 {
		v = 0
	}
	m.Lock()
	m.projectMaxConcurrency = v
	m.Unlock()
	_ = m.SaveAccounts(false)
}

// GetOtherMaxConcurrency 返回某组单账号并发上限,未知组回退默认 10。
func (m *Manager) GetOtherMaxConcurrency(groupID string) int {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	m.RLock()
	if m.otherMaxConcurrency == nil {
		m.RUnlock()
		return defaultMaxConcurrency
	}
	v, ok := m.otherMaxConcurrency[gid]
	m.RUnlock()
	if !ok || v <= 0 {
		return defaultMaxConcurrency
	}
	return v
}

// SetOtherMaxConcurrency 设置某组单账号并发上限并持久化到 accounts.json(经 AccountsData.OtherMaxConcurrency)。
// groupID 经小写规范化与 OtherLBModes 同口径,保证 GetOtherGroups 回显与选号查询键一致。负数置 0。
func (m *Manager) SetOtherMaxConcurrency(groupID string, v int) {
	gid := strings.ToLower(strings.TrimSpace(groupID))
	if gid == "" {
		return
	}
	if v < 0 {
		v = 0
	}
	m.Lock()
	if m.otherMaxConcurrency == nil {
		m.otherMaxConcurrency = make(map[string]int)
	}
	m.otherMaxConcurrency[gid] = v
	m.Unlock()
	_ = m.SaveAccounts(true)
}

// ============ 在途并发计数器转发(选号热路径用) ============

// AcquireAccount 为该账号占用一个在途并发槽,由选号链路在确认选定账号后调用。
// Manager.concurrency 非空(NewManager 初始化);nil 防御保测试桩与未注入场景。
func (m *Manager) AcquireAccount(id string) {
	if m == nil || m.concurrency == nil {
		return
	}
	m.concurrency.Acquire(id)
}

// ReleaseAccount 释放该账号一个在途并发槽,由请求结束(流 EOF/响应回写完成/失败取消)处调用。
// floor 0 兜底防负,见 concurrency.go。
func (m *Manager) ReleaseAccount(id string) {
	if m == nil || m.concurrency == nil {
		return
	}
	m.concurrency.Release(id)
}

// FilterByConcurrency 返回 candidates 中在途并发数 < limit 的子集(保持原序)。
// limit<=0 视作不限(原样返回全部);typically 由调用方传 Get*MaxConcurrency() 的正数。
func (m *Manager) FilterByConcurrency(candidates []*Account, limit int) []*Account {
	if m == nil || m.concurrency == nil {
		return candidates
	}
	return m.concurrency.PickUnderLimit(candidates, limit)
}

// LeastLoadedAccount 返回 candidates 中当前在途并发数最小者(并列取首个);
// 全满降级时用:挑并发最少的号允许超额并日志标注,绝不硬拒 503。
func (m *Manager) LeastLoadedAccount(candidates []*Account) *Account {
	if m == nil || m.concurrency == nil {
		if len(candidates) == 0 {
			return nil
		}
		return candidates[0]
	}
	return m.concurrency.LeastLoaded(candidates)
}

// AccountInFlightCount 返回某账号当前在途并发数(只读),供调试/日志/测试断言使用。
func (m *Manager) AccountInFlightCount(id string) int {
	if m == nil || m.concurrency == nil {
		return 0
	}
	return m.concurrency.Count(id)
}

func (m *Manager) GetNvidiaPoolMode() bool {
	m.RLock()
	defer m.RUnlock()
	return m.nvidiaPoolMode
}

func (m *Manager) SetNvidiaPoolMode(enabled bool) {
	m.Lock()
	m.nvidiaPoolMode = enabled
	if enabled {
		m.poolMode = false
		m.projectPoolMode = false
		m.geminiCliPoolMode = false
		m.otherPoolMode = false
		m.activeChannel = "nvidia"
	}
	m.Unlock()
	_ = m.SaveAccounts(false)
}

// GetOtherPoolMode / SetOtherPoolMode 是 Other 号池的负载均衡总开关,与 NVIDIA 同构互斥。
// Other 号池内可有多个独立上游组,但总开关只有一个:开启后所有 Other 组账号统一参与轮询,
// 关闭则仅用每组首个可用账号(单账号模式)。组内具体 LB 算法见 otherLBModes(按 GroupID 维度)。
func (m *Manager) GetOtherPoolMode() bool {
	m.RLock()
	defer m.RUnlock()
	return m.otherPoolMode
}

func (m *Manager) SetOtherPoolMode(enabled bool) {
	m.Lock()
	m.otherPoolMode = enabled
	if enabled {
		m.poolMode = false
		m.projectPoolMode = false
		m.nvidiaPoolMode = false
		m.geminiCliPoolMode = false
		m.activeChannel = "other"
	}
	m.Unlock()
	_ = m.SaveAccounts(false)
}

// GetEnabledNvidiaAccounts 返回所有已启用且配置有效的 NVIDIA 账号（不受 CooldownUntil 过滤影响）。
// 专供模型列表拉取等不消耗 Token 的只读探针接口使用。
func (m *Manager) GetEnabledNvidiaAccounts() []*Account {
	m.RLock()
	defer m.RUnlock()
	var result []*Account
	for _, acc := range m.accounts {
		if acc != nil && acc.Provider == "nvidia" && acc.Enabled && acc.AccessToken != "" && acc.BaseURL != "" {
			result = append(result, acc)
		}
	}
	return result
}

// GetRawAccountsByProvider 按 provider 过滤返回账号(空/"all" 返回全量副本)。
func (m *Manager) GetRawAccountsByProvider(provider string) []*Account {
	m.RLock()
	defer m.RUnlock()
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" || p == "all" {
		res := make([]*Account, len(m.accounts))
		copy(res, m.accounts)
		return res
	}
	var res []*Account
	for _, acc := range m.accounts {
		if strings.ToLower(acc.Provider) == p {
			res = append(res, acc)
		}
	}
	return res
}
