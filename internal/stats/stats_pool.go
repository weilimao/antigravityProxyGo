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
	if !IsTabModel(modelName) {
		ps.CacheEligibleInputTokens += inTokens // 各池/组分母累积，剔除 TAB 补全模型
	}
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

// BackfillPoolFromModels 一次性迁移式回填 Pools 子聚合(幂等, 仅首次生效)。
// 公开方法: 自身上锁, 供外部(测试/运维链路)在未持锁上下文调用。
// 内部调用方(如 LoadFromDisk 已持有 t.Lock)必须改调 backfillPoolFromModelsLocked,
// 避免同一 goroutine 对 sync.Mutex 二次加锁自死锁(Go 的 Mutex 不可重入)。
//
// 设计目的: TrackRequestForPool 是 8/8 才随按池筛选引入, 老 stats.json 的 Pools 只有零散增量,
// antigravity 号池真实全量历史(自 6/12 的 gemini/claude/v1internal 直连全部累计)只躺在
// Models/trends/全局标量, 从未录入池桶。回填一次性把 Google 号池族模型反推归并进
// Pools["antigravity"], 使按池卡片不再显示残缺快照(截图 76.9%/$0.49/1.57M 仅 8/8 增量)。
//
// 幂等守卫: 桶已有真实增量(reqs>0 || inTokens>0)即跳过。该守卫在「8/8 上线前无 Pools」的空白
// 起点上正确; 但若老 stats.json 已带 8/8 上线后零散的 Pools["antigravity"](reqs=121 而非 Models 全量),
// 守卫会错误地把它当成「回填已完成」而跳过, 导致存量 Models 全量(几千 M cached)永不入桶 → 池命中率
// 用 121 reqs 的残缺快照作分子, 直到该进程再次 scheduleSave 落盘覆盖旧值。BackfillPoolFromModelsForce
// 专用于修正这种「桶非空但全量未并入」的历史脏态。
func (t *Tracker) BackfillPoolFromModels() {
	t.Lock()
	defer t.Unlock()
	t.backfillPoolFromModelsLocked()
}

// BackfillPoolFromModelsForce 强制重算 Pools["antigravity"] 为 Models 表的 Google 号池族全量,
// 忽略幂等守卫(已有 reqs/inTokens 也照算), 专用于修正 8/8 上线后老 stats.json 已带零散池桶、
// 但 Models 全量历史从未并入的脏态。幂等保证: 重复调用结果一致(每次都用 Models 全量重算并覆盖,
// 桶的真实增量在 Models 表里也有对应行, 用 Models 全量覆盖不会丢真实增量, 只补齐历史缺口)。
//
// 一次性迁移标志 BackfillForcedDone 由 LoadFromDisk 在首次本地迁移后置 true 落盘, 再次启动
// 即跳过该段重算, 避免每次启动都从 Models 重算覆盖正在增长的 Pools["antigravity"] 真实增量。
func (t *Tracker) BackfillPoolFromModelsForce() {
	t.Lock()
	defer t.Unlock()
	t.backfillPoolFromModelsForceLocked()
}

// Reset
func (t *Tracker) backfillPoolFromModelsForceLocked() {
	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}

	var agReqs, agIn, agOut, agCached, agEligibleIn int
	var agCost float64
	found := false
	for mKey, m := range t.stats.Models {
		if m == nil {
			continue
		}
		if !modelIsGooglePoolFamily(mKey) {
			continue
		}
		found = true
		agReqs += m.Reqs
		agIn += m.InTokens
		agOut += m.OutTokens
		agCached += m.CachedTokens
		if !IsTabModel(mKey) {
			agEligibleIn += m.InTokens
		}
		agCost += m.Cost
	}
	if !found {
		// 存量里没有 Google 族模型累积(全新启动/极早版本), 不制造空桶。
		return
	}

	ag := t.stats.Pools["antigravity"]
	if ag == nil {
		ag = &PoolStats{}
		t.stats.Pools["antigravity"] = ag
	}
	// 强制覆盖式: 用 Models 表 Google 族全量重设桶标量, 不受「桶已有零散增量」幂等守卫拦截。
	// 这与普通 backfill 的「叠加 +=」语义不同(普通版假设桶为空白起, 叠加等价于覆盖);
	// force 版针对脏态桶非空但未含全量的场景, 必须覆盖而非叠加, 否则零散增量会与全量重复计数。
	ag.Requests = agReqs
	ag.InTokens = agIn
	ag.OutTokens = agOut
	ag.CachedTokens = agCached
	ag.CacheEligibleInputTokens = agEligibleIn
	ag.Cost = math.Round(agCost*1000000.0) / 1000000.0
}

// backfillPoolFromModelsLocked 是实际回填逻辑, 调用方必须已持有 t.Lock。
// 见 BackfillPoolFromModels 的语义注释。仅用于空白起点的幂等首次回填; 历史脏态(桶非空但含不全)
// 走 backfillPoolFromModelsForceLocked 修正。
func (t *Tracker) backfillPoolFromModelsLocked() {
	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}
	ag := t.stats.Pools["antigravity"]
	if ag != nil && (ag.Requests > 0 || ag.InTokens > 0) {
		// antigravity 桶已有真实增量(存量回填已完成), 直接跳过, 幂等。
		return
	}

	var agReqs, agIn, agOut, agCached, agEligibleIn int
	var agCost float64
	found := false
	for mKey, m := range t.stats.Models {
		if m == nil {
			continue
		}
		if !modelIsGooglePoolFamily(mKey) {
			continue
		}
		found = true
		agReqs += m.Reqs
		agIn += m.InTokens
		agOut += m.OutTokens
		agCached += m.CachedTokens
		if !IsTabModel(mKey) {
			agEligibleIn += m.InTokens
		}
		agCost += m.Cost
	}
	if !found {
		// 存量里没有 Google 族模型累积(全新启动/极早版本), 不制造空桶。
		return
	}

	if ag == nil {
		ag = &PoolStats{}
		t.stats.Pools["antigravity"] = ag
	}
	// 只叠加一次: 若桶已被真实增量(少数 generatecontent-200 路径)累积, 存量仍合并进同一桶。
	ag.Requests += agReqs
	ag.InTokens += agIn
	ag.OutTokens += agOut
	ag.CachedTokens += agCached
	ag.CacheEligibleInputTokens += agEligibleIn
	ag.Cost = math.Round((ag.Cost+agCost)*1000000.0) / 1000000.0
}

// IsTabModel 判定模型名称是否属于 TAB 代码补全模型 (如 tab_flash_lite_preview, tab_jump_flash_lite_preview 等)。
// TAB 补全模型高频短小且绝大部分 cachedTokens 为 0, 不累加至“缓存命中率”分母 (TotalCacheEligibleInputTokens),
// 避免稀释控制台展示的真实上下文缓存命中率。
func IsTabModel(modelName string) bool {
	m := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(modelName, "models/")))
	return strings.HasPrefix(m, "tab")
}

// RecalculateCacheEligibleTokensLocked 依据 Models 模型表重新求和非 TAB 模型的输入 Token,
// 一次性修正全局 TotalCacheEligibleInputTokens,
// 消除历史保存的 stats.json 中被 TAB 补全请求稀释的分母。调用方必须已持有 t.Lock。
//
// 分母口径隔离(关键约束, bug 修复): 本方法只重算「全局」TotalCacheEligibleInputTokens,
// 刻意不再覆盖 Pools["antigravity"].CacheEligibleInputTokens。
//   - 全局分母 = Models 表里所有 Google 族且非 TAB 模型的 inTokens 之和
//     (历史上 NVIDIA/Other 池也含在 Google 族里——这是历史口径, 不动);
//   - 池分母 = 各池各自累加, 与全局分母是两套独立口径(详见 stats_pool.go 顶部注释)。
//
// 旧实现把全局分母直接塞进 antigravity 池桶, 等于把 NVIDIA 池的 inTokens 也算进 antigravity
// 的命中率分母 → antigravity 池命中率 = antigravityCached / (antigravityIn + nvidiaIn + otherIn),
// 分母被严重放大, 命中率暴跌到接近 0(截图 0.0% 的两大根因之一)。
// 修复后池分母只由 TrackRequestForPool 按各自池累加, 本方法只管全局分母, 二者物理隔离。
func (t *Tracker) RecalculateCacheEligibleTokensLocked() {
	var eligibleSum int
	for mKey, m := range t.stats.Models {
		if m == nil {
			continue
		}
		if modelIsGooglePoolFamily(mKey) && !IsTabModel(mKey) {
			eligibleSum += m.InTokens
		}
	}

	t.stats.TotalCacheEligibleInputTokens = eligibleSum

	// 刻意不再覆盖 Pools["antigravity"].CacheEligibleInputTokens:
	// 池分母由 TrackRequestForPool 与 backfillPoolFromModelsForce 各自按池累加,
	// 全局分母与池分母是两套独立口径, 在此串扰会让 antigravity 池命中率被全量分母稀释到 0。
}

// modelIsGooglePoolFamily 判定模型名主键是否属于 antigravity 号池族。
// Google 号池族包括 gemini 全系、claude 全系(经 daily-cloudcode-pa 重译为 Vertex Anthropic)、
// agent 模型、tab 补全(tab_flash/tab_jump)、antigravity-core 及空/unknown 兜底名。
// 显式排除第三方号池族: "nvidia/" 前缀、以及 openai/deepseek/qwen/moonshot/kimi/z-ai/glm/gpt
// 等 OpenAI 兼容上游(这些经 recordNvidiaUsage/recordOtherUsage 各自记账, 不该并入 antigravity)。
// 仅用于 BackfillPoolFromModels 一次性存量回填的归并口径, 不影响新请求的 PoolKeyForProvider。
func modelIsGooglePoolFamily(modelKey string) bool {
	m := strings.ToLower(modelKey)
	if strings.HasPrefix(m, "nvidia/") {
		return false
	}
	thirdParty := []string{"openai/", "deepseek", "qwen", "moonshot", "kimi", "z-ai/", "glm", "gpt-", "o1-", "o3-", "o4-", "o5-"}
	for _, p := range thirdParty {
		if strings.Contains(m, p) {
			return false
		}
	}
	if m == "" || m == "unknown" || m == "antigravity-core" {
		return true
	}
	for _, p := range []string{"gemini", "claude", "tab_f", "tab-jump", "grok", "gpt-oss", "mistral", "llama", "phi", "command-r"} {
		if strings.Contains(m, p) {
			return true
		}
	}
	return false
}
