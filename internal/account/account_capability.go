package account

import (
	"sort"
	"strings"
	"time"
)

// account_capability.go: 账号模型能力画像（HasArgonBucket / SupportedModels / UnsupportedModels）
// 与选号前置过滤逻辑。用于解决号池中部分账号无 Gemini 4 Argon 或 claude-opus-5-5 系列模型权限时，
// 默认盲目命中无权限账号触发 404 再换号导致的风控风险。

const defaultUnsupportedModelTTL = 24 * time.Hour

// normalizeCapabilityModel 规整模型名称（去除 models/ 前缀、去空白、转小写）。
func normalizeCapabilityModel(modelName string) string {
	m := strings.ToLower(strings.TrimSpace(modelName))
	m = strings.TrimPrefix(m, "models/")
	return m
}

// isHighTierGatedModel 判断目标模型是否属于灰度/高权限受限模型（如 Gemini 4 Argon、Claude 5.5 系列）。
func isHighTierGatedModel(normModel string) bool {
	if normModel == "" {
		return false
	}
	return strings.Contains(normModel, "argon") ||
		strings.Contains(normModel, "gemini-4") ||
		strings.Contains(normModel, "opus-5") ||
		strings.Contains(normModel, "sonnet-5") ||
		strings.Contains(normModel, "claude-5") ||
		strings.Contains(normModel, "5-5") ||
		strings.Contains(normModel, "5.5")
}

// IsModelMarkedUnsupported 检查该账号是否已在 404 负向缓存中被标记为不支持指定模型（且尚未过期）。
// 调用方需持有 Manager 读锁或写锁。
func (a *Account) IsModelMarkedUnsupported(modelName string, nowMs int64) bool {
	if a == nil || len(a.UnsupportedModels) == 0 {
		return false
	}
	norm := normalizeCapabilityModel(modelName)
	if norm == "" {
		return false
	}
	untilMs, ok := a.UnsupportedModels[norm]
	if !ok || untilMs <= 0 {
		return false
	}
	return nowMs < untilMs
}

// ExplicitlySupportsModel 基于已采样的 SupportedModels 判断账号是否支持指定模型。
// 返回值 (known, supported)：
//   - known=false: 该账号尚未采样过 fetchAvailableModels 模型列表；
//   - known=true, supported=true: 已采样且明确包含该模型；
//   - known=true, supported=false: 已采样且明确不包含该模型。
// 调用方需持有 Manager 读锁或写锁。
func (a *Account) ExplicitlySupportsModel(modelName string) (bool, bool) {
	if a == nil {
		return false, false
	}
	norm := normalizeCapabilityModel(modelName)
	if norm == "" {
		return false, false
	}
	// 若请求的是 Argon 系列模型，且配额桶已明确探测出该账号无 Gemini 4 Argon 配额桶，直接判定为不支持
	if strings.Contains(norm, "argon") && a.HasArgonBucket != nil && !*a.HasArgonBucket {
		return true, false
	}
	if len(a.SupportedModels) == 0 {
		return false, false
	}
	for _, m := range a.SupportedModels {
		if normalizeCapabilityModel(m) == norm {
			return true, true
		}
	}
	// 兼容不带档位后缀或带 -thinking 后缀的同代模型别名（如 gemini-4-hydrogen / claude-opus-5-5-thinking -> *-high）
	baseAlias := strings.TrimSuffix(norm, "-thinking")
	if baseAlias != "" {
		for _, m := range a.SupportedModels {
			cm := normalizeCapabilityModel(m)
			for _, tierSuffix := range []string{"-xhigh", "-high", "-medium", "-low"} {
				if cm == baseAlias+tierSuffix {
					return true, true
				}
			}
		}
	}
	return true, false
}

// SetAccountSupportedModels 更新账号通过 fetchAvailableModels 探测到的可用模型列表，
// 并自动清除这些模型在 UnsupportedModels 中的历史 404 负向标记。
func (m *Manager) SetAccountSupportedModels(id string, models []string) {
	if m == nil || id == "" || len(models) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, raw := range models {
		norm := normalizeCapabilityModel(raw)
		if norm == "" {
			continue
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		normalized = append(normalized, norm)
	}
	if len(normalized) == 0 {
		return
	}
	sort.Strings(normalized)

	m.Lock()
	acc := m.getAccountByIDLocked(id)
	if acc == nil {
		m.Unlock()
		return
	}
	provider := acc.Provider
	changed := len(acc.SupportedModels) != len(normalized)
	if !changed {
		for i := range normalized {
			if acc.SupportedModels[i] != normalized[i] {
				changed = true
				break
			}
		}
	}
	acc.SupportedModels = normalized
	if len(acc.UnsupportedModels) > 0 {
		for _, sup := range normalized {
			if _, exists := acc.UnsupportedModels[sup]; exists {
				delete(acc.UnsupportedModels, sup)
				changed = true
			}
		}
	}
	m.Unlock()

	if changed && provider != "" {
		_ = m.SaveAccountsFor(true, provider)
	}
}

// MarkAccountModelUnsupported 在账号遇到上游 HTTP 404 (Model not found) 时，
// 将该模型记入该账号的 UnsupportedModels 负向缓存（仅屏蔽该模型，不影响该账号服务其他模型）。
func (m *Manager) MarkAccountModelUnsupported(id, modelName string, ttl time.Duration) {
	if m == nil || id == "" {
		return
	}
	norm := normalizeCapabilityModel(modelName)
	if norm == "" {
		return
	}
	if ttl <= 0 {
		ttl = defaultUnsupportedModelTTL
	}
	untilMs := time.Now().UnixNano()/int64(time.Millisecond) + ttl.Milliseconds()

	m.Lock()
	acc := m.getAccountByIDLocked(id)
	if acc == nil {
		m.Unlock()
		return
	}
	provider := acc.Provider
	if acc.UnsupportedModels == nil {
		acc.UnsupportedModels = make(map[string]int64)
	}
	acc.UnsupportedModels[norm] = untilMs

	// 若 SupportedModels 曾包含该模型，同步移除以保持能力画像自洽
	if len(acc.SupportedModels) > 0 {
		filtered := acc.SupportedModels[:0]
		for _, s := range acc.SupportedModels {
			if normalizeCapabilityModel(s) != norm {
				filtered = append(filtered, s)
			}
		}
		acc.SupportedModels = filtered
	}
	m.Unlock()

	if provider != "" {
		_ = m.SaveAccountsFor(true, provider)
	}
}

// anyPoolAccountExplicitlySupports 检查全池同一渠道启用账号（含冷却中）是否至少有一个已明确支持该模型。
// 调用方需持有 m.RLock() 或 m.Lock()。
func anyPoolAccountExplicitlySupports(accounts []*Account, channel, modelName string) bool {
	norm := normalizeCapabilityModel(modelName)
	if norm == "" {
		return false
	}
	for _, a := range accounts {
		if a == nil || !a.Enabled || a.IsAuthExpired() || a.Provider != channel {
			continue
		}
		if a.AccessToken == "" && a.RefreshToken == "" {
			continue
		}
		if known, supported := a.ExplicitlySupportsModel(norm); known && supported {
			return true
		}
	}
	return false
}

// filterAccountsByModelCapability 对已通过基础启用/冷却校验的候选账号列表执行模型能力过滤。
// 调用方需已持有 m.RLock() 或 m.Lock()。
func filterAccountsByModelCapability(candidates []*Account, channel, modelName string, nowMs int64) []*Account {
	return filterAccountsByModelCapabilityWithPool(candidates, channel, modelName, nowMs, false)
}

// filterAccountsByModelCapabilityWithPool 在 filterAccountsByModelCapability 基础上引入全池支持感知：
// 当池内（含冷却中）已知存在支持该模型的账号时，严禁回退到已明确不支持该模型的账号。
func filterAccountsByModelCapabilityWithPool(candidates []*Account, channel, modelName string, nowMs int64, poolHasExplicitSupport bool) []*Account {
	if len(candidates) == 0 {
		return candidates
	}
	normModel := normalizeCapabilityModel(modelName)
	if normModel == "" {
		return candidates
	}
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch != "" && ch != "antigravity" && ch != "project" && ch != "google" && ch != "gcp" && ch != "gemini-cli" {
		return candidates
	}

	// 1. 第一道防线（硬过滤）：剔除在 404 负向缓存期内的账号。
	// 若全池账号均对该模型返回过 404，返回空切片以快速终止无意义的 21 次重试空转。
	notUnsupported := make([]*Account, 0, len(candidates))
	for _, acc := range candidates {
		if !acc.IsModelMarkedUnsupported(normModel, nowMs) {
			notUnsupported = append(notUnsupported, acc)
		}
	}
	if len(notUnsupported) == 0 {
		return nil
	}
	candidates = notUnsupported

	// 2. 第二道防线：基于 SupportedModels 白名单与 HasArgonBucket 配额桶画像优选。
	isArgon := strings.Contains(normModel, "argon")
	isGated := isHighTierGatedModel(normModel)
	hasExplicitlyUnsupported := false

	var confirmedSupported []*Account
	var unprobedWithArgon []*Account
	var unprobedPaid []*Account
	var unprobedOther []*Account

	for _, acc := range candidates {
		known, supported := acc.ExplicitlySupportsModel(normModel)
		if known {
			if supported {
				confirmedSupported = append(confirmedSupported, acc)
			} else {
				// known && !supported: 明确无该模型权限，跳过不放入优选集
				hasExplicitlyUnsupported = true
			}
			continue
		}

		// 尚未采样 SupportedModels 的账号：检查 HasArgonBucket 与 Tier 画像
		if isArgon && acc.HasArgonBucket != nil && !*acc.HasArgonBucket {
			// 明确无 Gemini 4 Argon 配额桶的账号，不放入 Argon 优选集
			continue
		}

		if acc.HasArgonBucket != nil && *acc.HasArgonBucket {
			unprobedWithArgon = append(unprobedWithArgon, acc)
		} else if isPaidTier(acc.Tier) {
			unprobedPaid = append(unprobedPaid, acc)
		} else {
			unprobedOther = append(unprobedOther, acc)
		}
	}

	// 动态差异模型（号池中存在明确不支持的账号）或高权限受限模型：
	// 按「白名单明确支持 -> 拥有 Argon 配额桶 -> Pro/Ultra 付费层级 -> 其余未探测账号」梯队收敛，
	// 确保只要池内存在已确认支持的可用账号，负载均衡与粘性哈希绝不落入无权限或未确认账号。
	if isGated || hasExplicitlyUnsupported {
		if len(confirmedSupported) > 0 {
			return confirmedSupported
		}
		if len(unprobedWithArgon) > 0 {
			return unprobedWithArgon
		}
		if !isArgon && len(unprobedPaid) > 0 {
			return unprobedPaid
		}
		if len(unprobedOther) > 0 {
			return unprobedOther
		}
		// 若全池已知存在支持该模型的账号（只是当前在冷却），绝不回退到明确不支持的账号
		if poolHasExplicitSupport {
			return nil
		}
		// 保底：若全池均未收录该模型（如上游刚上新且本地尚未重探），回退非 404 候选集防死锁
		return candidates
	}

	// 针对全员通用的常规模型：
	// 合并所有明确支持与未采样账号参与均衡轮询。
	regularAllowed := make([]*Account, 0, len(confirmedSupported)+len(unprobedWithArgon)+len(unprobedPaid)+len(unprobedOther))
	regularAllowed = append(regularAllowed, confirmedSupported...)
	regularAllowed = append(regularAllowed, unprobedWithArgon...)
	regularAllowed = append(regularAllowed, unprobedPaid...)
	regularAllowed = append(regularAllowed, unprobedOther...)
	if len(regularAllowed) > 0 {
		return regularAllowed
	}
	if poolHasExplicitSupport {
		return nil
	}
	return candidates
}

func isPaidTier(tier string) bool {
	t := strings.ToLower(strings.TrimSpace(tier))
	return strings.Contains(t, "ultra") || strings.Contains(t, "pro") || strings.Contains(t, "enterprise")
}
