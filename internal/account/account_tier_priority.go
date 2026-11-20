package account

import (
	"strconv"
	"strings"
)

// account_tier_priority.go: 基于“检测可用模型”持久化结果实现的同配额族低阶账号优先调度。
// 当 PreferLowTierAccount 开关开启（默认 false）且号池在同配额族存在模型层级差异（例如部分账号仅支持
// gemini-3.8-flash 而无 gemini-4-hydrogen 权限）时，优先将低阶模型请求路由至不含高阶差异模型的账号，
// 保留高阶账号的配额给高阶模型；当低阶账号均处于冷静期/禁用/满并发/本次请求已失败排除时，自动平滑回退高阶账号。

// GetPreferLowTierAccount 返回是否启用“优先使用低阶模型账号”开关（默认 false）。
func (m *Manager) GetPreferLowTierAccount() bool {
	if m == nil {
		return false
	}
	m.RLock()
	defer m.RUnlock()
	return m.preferLowTierAccount
}

// SetPreferLowTierAccount 设置“优先使用低阶模型账号”开关并立即持久化至 pool 分区配置。
func (m *Manager) SetPreferLowTierAccount(enabled bool) error {
	if m == nil {
		return nil
	}
	m.Lock()
	m.preferLowTierAccount = enabled
	m.Unlock()
	return m.SaveAccountsFor(false, poolPartKind)
}

// SelectPriorityTierAccounts 在请求已过滤出可用候选（含冷却、权限、排除、并发过滤）之后，
// 根据 PreferLowTierAccount 开关收敛本轮应交付负载均衡/会话粘连的优先级子集：
//   - 开关关闭：原样返回 candidates；
//   - 开关开启且 candidates 中存在已确认低阶账号（不支持同配额族更高阶差异模型）：仅返回该 Tier 1 子集；
//   - 若 Tier 1 在当前可用候选中已全部不可用/满并发：回退返回其余可用候选（如高阶/未探测账号）。
func (m *Manager) SelectPriorityTierAccounts(candidates []*Account, channel, modelName string) []*Account {
	if m == nil || len(candidates) <= 1 || strings.TrimSpace(modelName) == "" {
		return candidates
	}
	m.RLock()
	defer m.RUnlock()
	if !m.preferLowTierAccount {
		return candidates
	}
	return selectPriorityTierAccountsLocked(m.accounts, candidates, channel, modelName, true)
}

// orderAccountsByLowTierLocked 在持有 Manager 锁（读锁或写锁）时稳定前置 Tier 1 低阶账号，
// 同时保留全部可用账号用于后续并发过滤与失败降级，供 GetAvailableAccountsForChannelAndGroup / GetNextAccount 复用。
func (m *Manager) orderAccountsByLowTierLocked(candidates []*Account, channel, modelName string) []*Account {
	if m == nil || !m.preferLowTierAccount || len(candidates) <= 1 || strings.TrimSpace(modelName) == "" {
		return candidates
	}
	return selectPriorityTierAccountsLocked(m.accounts, candidates, channel, modelName, false)
}

func selectPriorityTierAccountsLocked(
	poolAccounts []*Account,
	candidates []*Account,
	channel, modelName string,
	filterToPriority bool,
) []*Account {
	normTarget := normalizeCapabilityModel(modelName)
	if normTarget == "" {
		return candidates
	}
	targetQuotaGroup := modelQuotaGroup(normTarget)
	if targetQuotaGroup == "" {
		return candidates
	}
	targetFamily := extractTierFamilyKey(normTarget)

	higherDiffFamilies := computeHigherTierDiffFamilies(poolAccounts, channel, targetQuotaGroup, targetFamily, normTarget)
	if len(higherDiffFamilies) == 0 {
		return candidates
	}

	type candTierScore struct {
		acc         *Account
		probed      bool
		supportsReq bool
		higherCount int
	}

	scored := make([]candTierScore, len(candidates))
	var tierZero []*Account
	hasUnprobed := false
	minProbedHigher := -1
	maxProbedHigher := -1

	for i, acc := range candidates {
		item := candTierScore{acc: acc}
		if acc != nil && len(acc.SupportedModels) > 0 {
			item.probed = true
			_, item.supportsReq = acc.ExplicitlySupportsModel(modelName)
			item.higherCount = countMatchingFamilies(acc.SupportedModels, targetQuotaGroup, higherDiffFamilies)
			if item.supportsReq {
				if item.higherCount == 0 {
					tierZero = append(tierZero, acc)
				}
				if minProbedHigher == -1 || item.higherCount < minProbedHigher {
					minProbedHigher = item.higherCount
				}
				if item.higherCount > maxProbedHigher {
					maxProbedHigher = item.higherCount
				}
			}
		} else {
			hasUnprobed = true
		}
		scored[i] = item
	}

	if filterToPriority {
		if len(tierZero) > 0 {
			return tierZero
		}
		// 若 Tier 0 全部被排除，但剩余候选全部已探测且仍存在阶梯差，优先保护更高阶号；若混有未探测号则全量兜底避免误挡。
		if !hasUnprobed && minProbedHigher >= 0 && minProbedHigher < maxProbedHigher {
			var minTier []*Account
			for _, item := range scored {
				if item.probed && item.supportsReq && item.higherCount == minProbedHigher {
					minTier = append(minTier, item.acc)
				}
			}
			if len(minTier) > 0 {
				return minTier
			}
		}
		return candidates
	}

	// 稳定排序重排：已探测低阶确认组(tierZero) -> 其余已探测按 higherCount 升序 -> 未探测与不匹配兜底。
	ordered := make([]*Account, 0, len(candidates))
	ordered = append(ordered, tierZero...)

	// 收集 remaining 已探测但 higherCount > 0 的账号并按 higherCount 分桶稳定追加
	if maxProbedHigher > 0 {
		for level := 1; level <= maxProbedHigher; level++ {
			for _, item := range scored {
				if item.probed && item.supportsReq && item.higherCount == level {
					ordered = append(ordered, item.acc)
				}
			}
		}
	}
	for _, item := range scored {
		if item.probed && item.supportsReq {
			continue
		}
		ordered = append(ordered, item.acc)
	}
	return ordered
}

// computeHigherTierDiffFamilies 统计同渠道、同配额族中所有“已探测账号之间存在差异且高于目标模型”的家族集合。
func computeHigherTierDiffFamilies(
	poolAccounts []*Account,
	channel, targetQuotaGroup, targetFamily, normTarget string,
) map[string]bool {
	probedCount := 0
	familySupportCount := make(map[string]int)

	for _, acc := range poolAccounts {
		if acc == nil || !acc.Enabled || len(acc.SupportedModels) == 0 {
			continue
		}
		if channel != "" && acc.Provider != channel {
			continue
		}
		probedCount++
		seenFam := make(map[string]bool)
		for _, rawModel := range acc.SupportedModels {
			norm := normalizeCapabilityModel(rawModel)
			if norm == "" || modelQuotaGroup(norm) != targetQuotaGroup {
				continue
			}
			fam := extractTierFamilyKey(norm)
			if fam == "" || seenFam[fam] {
				continue
			}
			seenFam[fam] = true
			familySupportCount[fam]++
		}
	}

	if probedCount < 2 {
		return nil
	}

	result := make(map[string]bool)
	for fam, cnt := range familySupportCount {
		if fam == targetFamily {
			continue
		}
		// 只有部分已探测账号拥有、另一部分缺失的模型家族才构成“阶梯差异”。
		if cnt <= 0 || cnt >= probedCount {
			continue
		}
		if isHigherTierFamily(fam, normTarget) {
			result[fam] = true
		}
	}
	return result
}

func countMatchingFamilies(supportedModels []string, targetQuotaGroup string, higherDiffFamilies map[string]bool) int {
	if len(higherDiffFamilies) == 0 {
		return 0
	}
	seen := make(map[string]bool)
	for _, rawModel := range supportedModels {
		norm := normalizeCapabilityModel(rawModel)
		if norm == "" || modelQuotaGroup(norm) != targetQuotaGroup {
			continue
		}
		fam := extractTierFamilyKey(norm)
		if higherDiffFamilies[fam] {
			seen[fam] = true
		}
	}
	return len(seen)
}

// modelQuotaGroup 将模型划分到独立的配额计数桶（Gemini 常规 / Gemini 4 Argon / Claude+GPT）。
// 不同配额桶之间的模型权限差异互不消耗彼此额度，不应相互限流。
func modelQuotaGroup(normModel string) string {
	switch {
	case strings.Contains(normModel, "argon"):
		return "argon"
	case strings.Contains(normModel, "gemini"):
		return "gemini"
	case strings.Contains(normModel, "claude"),
		strings.Contains(normModel, "opus"),
		strings.Contains(normModel, "sonnet"),
		strings.Contains(normModel, "haiku"),
		strings.Contains(normModel, "gpt"):
		return "claude"
	default:
		return ""
	}
}

func extractTierFamilyKey(normModel string) string {
	key := normModel
	for _, suffix := range []string{"-xhigh", "-high", "-medium", "-low", "-tiered", "-thinking"} {
		if strings.HasSuffix(key, suffix) && len(key) > len(suffix) {
			key = strings.TrimSuffix(key, suffix)
			break
		}
	}
	return key
}

// isHigherTierFamily 判断候选模型家族 candFam 是否严格高于请求模型 normTarget。
func isHigherTierFamily(candFam, normTarget string) bool {
	candGated := isHighTierGatedModel(candFam)
	targetGated := isHighTierGatedModel(normTarget)
	if candGated && !targetGated {
		return true
	}

	candMaj, candMin, candOK := parseModelVersion(candFam)
	tgtMaj, tgtMin, tgtOK := parseModelVersion(normTarget)
	if candOK && tgtOK {
		if candMaj != tgtMaj {
			return candMaj > tgtMaj
		}
		if candMin != tgtMin {
			return candMin > tgtMin
		}
		return modelTierRank(candFam) > modelTierRank(normTarget)
	}
	if !candOK && !tgtOK {
		return modelTierRank(candFam) > modelTierRank(normTarget)
	}
	return false
}

func parseModelVersion(normModel string) (int, int, bool) {
	parts := strings.Split(normModel, "-")
	for i := 0; i < len(parts); i++ {
		tok := parts[i]
		if dotIdx := strings.IndexByte(tok, '.'); dotIdx > 0 {
			maj, ok1 := parseShortInt(tok[:dotIdx])
			min, ok2 := parseShortInt(tok[dotIdx+1:])
			if ok1 && ok2 {
				return maj, min, true
			}
		}
		if maj, ok := parseShortInt(tok); ok {
			if i+1 < len(parts) {
				if min, ok2 := parseShortInt(parts[i+1]); ok2 {
					return maj, min, true
				}
			}
			return maj, 0, true
		}
	}
	return 0, 0, false
}

func parseShortInt(s string) (int, bool) {
	if s == "" || len(s) > 3 {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v >= 100 {
		return 0, false
	}
	return v, true
}

func modelTierRank(normModel string) int {
	switch {
	case strings.Contains(normModel, "argon"):
		return 4
	case strings.Contains(normModel, "hydrogen"),
		strings.Contains(normModel, "ultra"),
		strings.Contains(normModel, "opus"),
		strings.Contains(normModel, "max"):
		return 3
	case strings.Contains(normModel, "pro"),
		strings.Contains(normModel, "sonnet"):
		return 2
	case strings.Contains(normModel, "flash"),
		strings.Contains(normModel, "lite"),
		strings.Contains(normModel, "mini"),
		strings.Contains(normModel, "haiku"):
		return 1
	default:
		return 2
	}
}
