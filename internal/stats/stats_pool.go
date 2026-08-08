package stats

// stats_pool.go: 按号池/按组维度的缓存命中率子统计。
//
// 本文件提供与 GlobalStats 全局标量并行的"第四个累加点" TrackRequestForPool,
// 使前端「缓存命中率」卡片可按号池(antigravity / nvidia / other:<groupId>)筛选显示。
//
// 设计要点(详见 plan 与 stats.go:262-264 注释意图):
//   - 不改动既有 TrackRequest / TrackRequestForModel / TrackNvidiaRequest 三个累加点签名,
//     全局口径静止不变,既有单测零回归;
//   - 本文件的方法是平行第四点,只写 GlobalStats.Pools 子聚合,不触碰全局标量;
//   - internal/stats 不 import internal/account(底层包, 避免循环依赖),
//     poolKey 由调用方经纯标量 helper PoolKeyForProvider(provider, groupID) 算好传入。
//
// key 命名规范:
//   "antigravity"          —— Provider ∈ {antigravity, project, google, gcp, gemini-cli, ""} 的直连请求;
//   "nvidia"               —— Provider == "nvidia"(NIM 上游 OpenAI Chat 协议无 cache, cached 恒 0);
//   "other:<groupIdLowerTrim>" —— Provider == "other", 拼 groupId; 缺失兜底 "other:__unknown__"。
//
// 各池/组分子分母独立累加, 互不串扰, 与原全局第一档口径一致(剔除恒 0 缓存的池时各池独立剔除)。

import (
	"math"
	"strings"
)

// PoolStats 是按号池/按组维度的命中率子统计,与 GlobalStats 全局标量两套独立口径并行:
//   - 全局原口径 = TrackRequest(gemini/claude 直连) + TrackRequestForModel(NVIDIA+Other) 合流;
//   - 新池口径 = TrackRequestForPool 各自仅取所属池/组的累积, 互不串扰。
// 两个口径的分子分母不共用, 旧全局不变式(stats.go 现有单测)零回归。
type PoolStats struct {
	Requests int `json:"reqs"`
	InTokens int `json:"inTokens"`
	OutTokens int `json:"outTokens"`
	// CachedTokens 是本池/组的缓存命中 token 累计(命中率分子)。
	// antigravity: gemini/claude 直连回报的真实 cachedTokens;
	// nvidia: NIM 上游无 cache, 恒 0;
	// other:<gid>: Other 上游(OpenAI/Anthropic 兼容端点)真实回报的 cached 透传。
	CachedTokens int `json:"cachedTokens"`
	// CacheEligibleInputTokens 是"缓存命中率分母"按本池/组聚合的同口径累加,
	// 与 GlobalStats.TotalCacheEligibleInputTokens 原全局第一档口径一致:
	// 各池/组每次请求的 inTokens 计入分母; NVIDIA 虽 cached 恒 0 但分母仍累加 input
	// (其命中率注定 0%, 接受不强行隐藏)。
	CacheEligibleInputTokens int     `json:"cacheEligibleInputTokens"`
	Cost                    float64 `json:"cost"`
}

// otherUnknownGroupKey 是 Other 账号缺失 GroupID 时的兜底池 key, 避免该异常账号 token 丢失,
// 同时不与任何真实组串扰。前端下拉里它是"Other · 未知组"兜底项。
const otherUnknownGroupKey = "other:__unknown__"

// otherKeyPrefix 是 Other 池 key 的前缀, 前端据此从 Pools map 拆出组列表。
const otherKeyPrefix = "other:"

// PoolKeyForProvider 把 account.Provider(见 internal/account/account.go:21)映射到命中率筛选池 key。
//
// 映射规则:
//   - "nvidia" → "nvidia"
//   - "other"  → "other:<groupIDLowerTrim>", groupID 空(经规整)兜底 "other:__unknown__"
//   - 其余("antigravity"/"project"/"google"/"gcp"/"gemini-cli"/"") → "antigravity"
//     (直连链路口径等价于官方账号; 直连无 poolAccount 时空 provider 亦归此, 即默认链路)
//
// 纯标量签名刻意不依赖 account 包, 由调用点(relay/proxy, 本就 import account)算好 key 传入,
// 规避 internal/stats → internal/account 的循环依赖(stats 是底层包)。
// groupID 规整(ToLower+TrimSpace)与 internal/account/account_other.go 的 OtherLBModes 键口径一致。
func PoolKeyForProvider(provider, groupID string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "nvidia":
		return "nvidia"
	case "other":
		gid := strings.ToLower(strings.TrimSpace(groupID))
		if gid == "" {
			return otherUnknownGroupKey
		}
		return otherKeyPrefix + gid
	default:
		return "antigravity"
	}
}

// IsOtherPoolKey 判定一个 Pools key 是否属于 Other 池(含 "other:__unknown__")。
// 前端与单测据此从 Pools 拆出 Other 组列表; antigravity/nvidia 返回 false。
func IsOtherPoolKey(key string) bool {
	return strings.HasPrefix(key, otherKeyPrefix)
}

// OtherGroupIDFromKey 从 "other:<groupId>" 风格的 key 提取 groupId 部分;
// 非 Other key 返回空串。供前端/排错用, 不依赖 account 包。
func OtherGroupIDFromKey(key string) string {
	if !IsOtherPoolKey(key) {
		return ""
	}
	return strings.TrimPrefix(key, otherKeyPrefix)
}

// TrackRequestForPool 为单笔请求写入按池/组维度的累计(命中率分子分母 / token / 成本),
// 与 TrackRequest / TrackRequestForModel 并列的第四个独立累加点, 不动全局标量, 故全局口径零回归。
//
// poolKey 由调用方经 PoolKeyForProvider 预先算好传入; 空串兜底视为 "antigravity"(与默认链路一致)。
// cached: antigravity 链路为真实 cachedTokens; nvidia 链路恒 0; other 链路透传上游真实回报。
// 与并存调用关系: 各调用点在同一事务里先调原 Track*/TrackRequestForModel, 再追加调本方法,
// 两次写互不干扰(一个写全局标量+trends, 一个写 Pools 子聚合)。
//
// 注: cost 单独按本池累积, 不入全局 TotalCost(后者由原 Track* 负责), 两套口径并行。
func (t *Tracker) TrackRequestForPool(modelName string, inTokens, outTokens, cachedTokens int, poolKey string) {
	if inTokens < 0 {
		inTokens = 0
	}
	if outTokens < 0 {
		outTokens = 0
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}

	poolKey = strings.TrimSpace(poolKey)
	if poolKey == "" {
		poolKey = "antigravity"
	}

	t.Lock()
	defer t.Unlock()

	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}
	ps := t.getOrCreatePoolLocked(poolKey)
	ps.Requests++
	ps.InTokens += inTokens
	ps.OutTokens += outTokens
	ps.CachedTokens += cachedTokens
	ps.CacheEligibleInputTokens += inTokens // 各池/组第一档分母累加, 与原全局口径一致
	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, cachedTokens)
	ps.Cost = math.Round((ps.Cost+cost)*1000000.0) / 1000000.0

	t.scheduleSave()
}

// getOrCreatePoolLocked 取或建池桶, 调用方必须已持有 t.Lock(本方法不加锁)。
// pools map 在 NewTracker 起即非 nil, 但防御性兜底以应对 LoadFromDisk 后/反序列化异常路径。
func (t *Tracker) getOrCreatePoolLocked(poolKey string) *PoolStats {
	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}
	ps, ok := t.stats.Pools[poolKey]
	if !ok {
		ps = &PoolStats{}
		t.stats.Pools[poolKey] = ps
	}
	return ps
}

// copyPools 返回 Pools map 的值深拷贝, 供 SaveToDisk / GetPayload / GetPayloadSimplified
// 在读锁内构造快照, 避免 json.Marshal 反射或前端 IPC 与并发写竞争(与 modelsCopy/nvidiaTrendsCopy 同口径)。
// src 为 nil 时返回空非 nil map, 保持 NewTracker 的"始终非 nil"约定。
func copyPools(src map[string]*PoolStats) map[string]*PoolStats {
	if src == nil {
		return make(map[string]*PoolStats)
	}
	out := make(map[string]*PoolStats, len(src))
	for k, v := range src {
		if v == nil {
			continue
		}
		cp := *v // 值拷贝, 不共享指针
		out[k] = &cp
	}
	return out
}

// GetPoolStatsCopy 轻量级读取 Pools 的深拷贝, 供外部/单测断言各池累加结果,
// 不暴露内部 map 别名(线程安全, 与 GetNvidiaTrends 同口径, 读锁内值拷贝)。
func (t *Tracker) GetPoolStatsCopy() map[string]*PoolStats {
	t.RLock()
	defer t.RUnlock()
	return copyPools(t.stats.Pools)
}
