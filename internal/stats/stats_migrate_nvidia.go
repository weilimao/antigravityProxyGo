package stats

// stats_migrate_nvidia.go: NVIDIA 号池历史差量自愈合并迁移模块。
//
// 背景: recordNvidiaUsage 的第 4 落点(TrackRequestForModel, 写 stats.json 的 Models 表 +
// 全局标量)上线晚于 usage.json 记账。usage.json(账号维度, 58 个 provider=nvidia 账号的
// per-model 累计)自 recordNvidiaUsage 上线即连续记录, 是 NVIDIA 历史全量权威来源;
// stats.json Models 表只有第4落点上线后的增量 —— 历史差量只躺在 usage.json。
//
// 本模块把该差量一次性合并回 stats.json(Models + 全局标量 + Pools["nvidia"]),
// 幂等可重复执行, 与既有 BackfillForcedDone / TabExcludedFromEligible 迁移范式同构。
//
// 设计要点:
//   - 幂等: 差量公式 diff = usage 权威全量 − stats 现有值, 只取正值; usage 是固定上界,
//     重复执行 diff 恒收敛到 0, 不会重复累加。
//   - 不误并 OTHER 池: 聚合来源天然只有 provider=nvidia 账号(GetNvidiaModelAggregates /
//     aggregateNvidiaAccounts); 外加"模型名必须含 / 前缀"守卫, 防御裸名(deepseek-v4-flash 等)
//     与 OTHER 池共享键的极端碰撞——碰撞时 diff = nvidia全量 − OTHER存量 ≤ 0 被跳过, 双重保险。
//   - 不在热路径: 仅在 LoadFromDisk 一次性编排(NvidiaUsageBackfillDone 标志), 新请求
//     落点4 已全量覆盖, 日常运行零开销。
//   - Mutex 不可重入: 公开方法自身上锁; 内部 Locked 实现由持锁调用方调用。

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// ModelAggregate 是 usage.json 里按模型的累计聚合值(合并 58 个 nvidia 账号)。
type ModelAggregate struct {
	Model        string
	Reqs         int
	InTokens     int
	OutTokens    int
	CachedTokens int
	Cost         float64
}

// aggregateNvidiaAccounts 从账号桶聚合 provider=="nvidia" 的 per-model 累计。
// 非 nvidia 账号一律排除; 同模型跨账号合并 sum。空/unknown 模型名忽略。
// 供 UsageTracker.GetNvidiaModelAggregates 与 LoadNvidiaModelAggregatesFromDisk 共用。
func aggregateNvidiaAccounts(accounts map[string]*AccountUsage) []ModelAggregate {
	agg := make(map[string]*ModelAggregate)
	for _, acc := range accounts {
		if acc == nil || !strings.EqualFold(strings.TrimSpace(acc.Provider), "nvidia") {
			continue
		}
		for mk, mu := range acc.Models {
			if mu == nil || (mu.RequestCount <= 0 && mu.InputTokens <= 0) {
				continue
			}
			key := strings.TrimSpace(mk)
			if key == "" || key == "unknown" {
				continue
			}
			b, exists := agg[key]
			if !exists {
				b = &ModelAggregate{Model: key}
				agg[key] = b
			}
			b.Reqs += mu.RequestCount
			b.InTokens += mu.InputTokens
			b.OutTokens += mu.OutputTokens
			b.CachedTokens += mu.CachedTokens
			b.Cost = roundCost(b.Cost + mu.TotalCost)
		}
	}

	out := make([]ModelAggregate, 0, len(agg))
	for _, b := range agg {
		out = append(out, *b)
	}
	return out
}

// LoadNvidiaModelAggregatesFromDisk 直接读取 dataDir/usage.json(与 stats.json 同目录)并聚合
// NVIDIA 账号 per-model 累计, 供 stats.LoadFromDisk 编排使用(stats 包内不持有 UsageTracker 实例,
// 且 LoadFromDisk 已持锁, 无法经 tracker 方法读)。解析失败/文件缺失返回 nil。
func LoadNvidiaModelAggregatesFromDisk(dataDir string) []ModelAggregate {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "usage.json"))
	if err != nil {
		return nil
	}

	var parsed struct {
		Usage *UsageState `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Usage == nil {
		return nil
	}
	return aggregateNvidiaAccounts(parsed.Usage.Accounts)
}

// FindModelBackfills 是纯函数: 计算 diff = 权威 usage 全量 − stats 现有 Models 值,
// 只返回 diff > 0 的模型(差量回填目标)。usage ≤ stats 现值(已同步或倒挂)不进返回集。
// 幂等性基石: 同一 usage 输入重复调用返回相同结果(收敛目标), 第二次应用时 diff=0 不再产生条目。
//
// stats 里不存在的键按 usage 全量全额回填(如 minimaxai/minimax-m3 全新条目)。
func FindModelBackfills(models map[string]*ModelStats, usage []ModelAggregate) []ModelAggregate {
	var out []ModelAggregate
	for _, ag := range usage {
		if ag.Reqs <= 0 && ag.InTokens <= 0 {
			continue
		}
		ex, exists := models[ag.Model]
		diffReqs := ag.Reqs
		diffIn := ag.InTokens
		diffOut := ag.OutTokens
		diffCached := ag.CachedTokens
		diffCost := ag.Cost
		if exists && ex != nil {
			diffReqs -= ex.Reqs
			diffIn -= ex.InTokens
			diffOut -= ex.OutTokens
			diffCached -= ex.CachedTokens
			// cost 逐位口径相同(百万位圆整), 直接减现条 cost。
			diffCost -= ex.Cost
		}
		// 全量已同步(diff≤0)或波形为零, 不制造回填条目。
		if diffReqs <= 0 && diffIn <= 0 && diffOut <= 0 && diffCached <= 0 && diffCost <= 0 {
			continue
		}
		out = append(out, ModelAggregate{
			Model:        ag.Model,
			Reqs:         diffReqs,
			InTokens:     diffIn,
			OutTokens:    diffOut,
			CachedTokens: diffCached,
			Cost:         diffCost,
		})
	}
	return out
}

// ApplyModelBackfillsLocked 把差量回填合并进 t.stats:
//
//	1) Models[key] += diff(新键按 diff 创建, 与 FindModelBackfills 差量约定对应)
//	2) 全局标量 TotalRequests/Input/Output/Cached += diff; 非 TAB 模型的 inTokens 进
//	   TotalCacheEligibleInputTokens(命中率分母口径与现有 TrackRequestForModel 一致, 不因
//	   差量回填改变口径); TotalCost 四舍六入五成双到 6 位小数。
//	3) Pools["nvidia"] 同步回填: reqs/in/out/cached/eligible 按 diff 累计(cost 单独百万位圆整)。
//
// 调用方必须已持有 t.Lock(t.RLock 不行)。cached 差量对 NVIDIA 历史上恒 0, 但保留通路
// 与上游未来回报 cache 的自愈能力一致。
func (t *Tracker) ApplyModelBackfillsLocked(backfills []ModelAggregate) {
	if len(backfills) == 0 {
		return
	}

	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}

	nvidiaPool := t.stats.Pools["nvidia"]

	for _, b := range backfills {
		// ---- 1) Models 表 ----
		m, exists := t.stats.Models[b.Model]
		if !exists {
			m = &ModelStats{}
			t.stats.Models[b.Model] = m
		}
		m.Reqs += b.Reqs
		m.InTokens += b.InTokens
		m.OutTokens += b.OutTokens
		m.CachedTokens += b.CachedTokens
		m.Cost = roundCost(m.Cost + b.Cost)

		// ---- 2) 全局标量 ----
		t.stats.TotalRequests += b.Reqs
		t.stats.TotalInputTokens += b.InTokens
		t.stats.TotalOutputTokens += b.OutTokens
		t.stats.TotalCachedTokens += b.CachedTokens
		if !IsTabModel(b.Model) {
			// 命中率分母: 与 TrackRequestForModel 同口径 —— NVIDIA 历史上 cached 恒 0,
			// 差量 inTokens 进分母会稀释命中率, 但现状全局分母含 NVIDIA(历史口径), 故此保持。
			t.stats.TotalCacheEligibleInputTokens += b.InTokens
		}
		t.stats.TotalCost = roundCost(t.stats.TotalCost + b.Cost)
	}

	// ---- 3) Pools["nvidia"] 同步回填(幂等: diff 恒收敛, 重复执行不重复计数) ----
	if nvidiaPool == nil {
		nvidiaPool = &PoolStats{}
		t.stats.Pools["nvidia"] = nvidiaPool
	}
	var poolReqs, poolIn, poolOut, poolCached, poolEligible int
	var poolCost float64
	for _, b := range backfills {
		poolReqs += b.Reqs
		poolIn += b.InTokens
		poolOut += b.OutTokens
		poolCached += b.CachedTokens
		if !IsTabModel(b.Model) {
			poolEligible += b.InTokens
		}
		poolCost += b.Cost
	}
	nvidiaPool.Requests += poolReqs
	nvidiaPool.InTokens += poolIn
	nvidiaPool.OutTokens += poolOut
	nvidiaPool.CachedTokens += poolCached
	nvidiaPool.CacheEligibleInputTokens += poolEligible
	nvidiaPool.Cost = roundCost(nvidiaPool.Cost + poolCost)
}

// isNvidiaPrefixedModel 判定模型名是否属于 NVIDIA 命名空间(带厂商前缀的具名模型)。
// 裸名(deepseek-v4-flash 等)可能是 OTHER 池共享键, 一律不并入 NVIDIA 差量;
// 前缀名(deepseek-ai/ z-ai/ minimaxai/ qwen/ meta/ nvidia/ 等)是 NVIDIA 号池记账形态。
// 显式排除 "models/" 前缀 —— 那是 Google 直连链路的模型键形态(models/gemini-...),
// 与 NVIDIA 厂商命名空间无关, 防御性避免与直连链路的同名键错并。
func isNvidiaPrefixedModel(modelName string) bool {
	name := strings.TrimSpace(modelName)
	slash := strings.Index(name, "/")
	if slash <= 0 {
		return false
	}
	// 前缀必须是字母/短横线/点组成的厂商命名空间(如 deepseek-ai、minimaxai),
	// 排除 "models/gemini..." 这类路径式前缀。
	prefix := name[:slash]
	if strings.EqualFold(prefix, "models") {
		return false
	}
	for _, r := range prefix {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

// BackfillNvidiaModelsFromUsage 是公开迁移入口: 自身上锁, 从 usage 聚合差量回填
// stats.Models + 全局标量 + Pools["nvidia"]。幂等守卫: 一次性标志已置或差量为空则 no-op。
//
// usage 应仅含 provider=nvidia 账号的聚合(调用方用 GetNvidiaModelAggregates /
// LoadNvidiaModelAggregatesFromDisk 获取); 模型名不含 / 前缀的裸名(可能为 OTHER 池共享键)
// 一律不并入, 防御串池。
func (t *Tracker) BackfillNvidiaModelsFromUsage(usage []ModelAggregate) {
	t.Lock()
	defer t.Unlock()
	t.backfillNvidiaModelsFromUsageLocked(usage)
}

// backfillNvidiaModelsFromUsageLocked 是实际逻辑, 调用方必须已持有 t.Lock。
func (t *Tracker) backfillNvidiaModelsFromUsageLocked(usage []ModelAggregate) {
	if t.stats.NvidiaUsageBackfillDone {
		return
	}

	var candidates []ModelAggregate
	for _, ag := range usage {
		if !isNvidiaPrefixedModel(ag.Model) {
			continue // 裸名可能为 OTHER 池共享键, 绝不合入
		}
		candidates = append(candidates, ag)
	}
	backfills := FindModelBackfills(t.stats.Models, candidates)
	if len(backfills) == 0 {
		// 无实际差量(usage 与 stats 已同步或 usage 为空), 仅落一次性标志, 避免每次启动重扫。
		t.stats.NvidiaUsageBackfillDone = true
		return
	}

	t.ApplyModelBackfillsLocked(backfills)
	t.stats.NvidiaUsageBackfillDone = true
	t.scheduleSave()
}

func roundCost(v float64) float64 {
	return math.Round(v*1000000.0) / 1000000.0
}