package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 持久化簇：scheduleSave / SaveToDisk / LoadFromDisk / seedEmptyTrends。


func (t *Tracker) scheduleSave() {
	t.saveTimeoutLock.Lock()
	defer t.saveTimeoutLock.Unlock()

	if t.saveTimeout != nil {
		return
	}

	t.saveTimeout = time.AfterFunc(3*time.Second, func() {
		t.SaveToDisk()
		t.saveTimeoutLock.Lock()
		t.saveTimeout = nil
		t.saveTimeoutLock.Unlock()

		t.RLock()
		callback := t.onPayloadUpdate
		t.RUnlock()
		if callback != nil {
			callback()
		}
	})
}

func (t *Tracker) SaveToDisk() {
	t.RLock()
	path := t.persistPath
	if path == "" {
		t.RUnlock()
		return
	}

	// Deep-copy all mutable slices while holding the read lock so that
	// json.Marshal (which uses reflection) never races with concurrent writes.
	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        make(map[string]*ModelStats, len(t.stats.Models)),
		Pools:                         copyPools(t.stats.Pools),
	}
	for k, v := range t.stats.Models {
		ms := *v // value copy, not pointer
		statsCopy.Models[k] = &ms
	}

	trendsCopy := make([]*HourlyTrend, len(t.trends))
	for i, tr := range t.trends {
		cp := *tr // value copy
		trendsCopy[i] = &cp
	}

	// nvidiaTrendsCopy: 英伟达号池专用趋势桶序列化拷贝, 与 trends 同样做值拷贝避免
	// json.Marshal 反射与并发写竞争; 落盘进 stats.json 的 nvidiaTrends 字段, 重启回填。
	nvidiaTrendsCopy := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, tr := range t.nvidiaTrends {
		cp := *tr
		nvidiaTrendsCopy[i] = &cp
	}

	reqsCopy := make([]*RequestLog, len(t.requests))
	for i, req := range t.requests {
		cp := *req // value copy
		reqsCopy[i] = &cp
	}
	t.RUnlock()

	// Marshal from fully-owned copies – no shared pointers, no data race.
	data := StatsData{
		Stats:        statsCopy,
		Trends:       trendsCopy,
		NvidiaTrends: nvidiaTrendsCopy,
		Requests:     reqsCopy,
	}

	bytesData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to marshal stats: %v\n", err)
		return
	}

	err = os.WriteFile(path, bytesData, 0644)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to write stats: %v\n", err)
	}
}

func (t *Tracker) LoadFromDisk() {
	t.Lock()
	defer t.Unlock()

	if t.persistPath == "" {
		t.seedEmptyTrends()
		return
	}

	if _, err := os.Stat(t.persistPath); os.IsNotExist(err) {
		t.seedEmptyTrends()
		return
	}

	data, err := os.ReadFile(t.persistPath)
	if err != nil {
		t.seedEmptyTrends()
		return
	}

	var parsed StatsData
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.seedEmptyTrends()
		return
	}

	t.stats = parsed.Stats
	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}
	// Pools 回填: 旧 stats.json 无本字段时为 nil, 兜底为空 map 保持 NewTracker 口径一致
	// (与 Models 兜底同构), 避免 TrackRequestForPool 的 getOrCreatePoolLocked 对 nil map 写入 panic。
	// 历史 Pools 缺失 → 重启后"按池/组口径"从 0 累加(全局口径从旧标量正常读回, 不受影响)。
	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}
	t.trends = parsed.Trends
	// nvidiaTrends 回填: 老 stats.json 无此字段时为 nil, 兜底为空切片保持 NewTracker 口径一致,
	// 避免 appendTrendBucket 对 nil 切片 append 时虽合法但与「始终非 nil」的约定不符。
	t.nvidiaTrends = parsed.NvidiaTrends
	if t.nvidiaTrends == nil {
		t.nvidiaTrends = make([]*HourlyTrend, 0)
	}
	t.requests = parsed.Requests

	for _, req := range t.requests {
		req.RequestBody = TruncateRequestBody(req.RequestBody)
	}

	// 存量按池口径回填: 老 stats.json 的 Pools 只有 TrackRequestForPool 上线后的零散增量,
	// antigravity 号池真实历史(自 6/12 的 gemini/claude/v1internal 直连与 daily-cloudcode-pa
	// 翻译链)一直只累计在 Models/trends/全局标量。此处用完 Models 表一次性反推归并进
	// Pools["antigravity"], 命中率卡片按池筛选才能反映用户真实的几千 M 缓存, 否则与
	// NVIDIA/Other 桶(各自有完整中继记账)口径严重不对齐。只写 Pools 子聚合, 全局标量 / Models /
	// trends 零回归。
	//
	// 双轨回填(关键, 适配两种历史状态):
	//   - 空白起点(Pools["antigravity"] 不存在或 reqs=0): backfillPoolFromModelsLocked 叠加合并, 幂等。
	//   - 脏态起点(Pools["antigravity"] 已有 8/8 上线后零散增量 reqs=121 但 Models 全量未并入):
	//     backfillPoolFromModelsLocked 的幂等守卫(reqs>0 跳过)会错误拦截, 导致全量永不入桶。
	//     一次性迁移标志 BackfillForcedDone 缺失时, 改走 backfillPoolFromModelsForceLocked 用
	//     Models 全量覆盖式重算桶标量, 完成后置 BackfillForcedDone=true 落盘, 下次启动不再重算。
	t.backfillPoolFromModelsLocked()
	if !t.stats.BackfillForcedDone {
		t.backfillPoolFromModelsForceLocked()
		t.stats.BackfillForcedDone = true
		t.scheduleSave()
	}

	// 历史 TAB 分母一次性重算修复: 若从未执行过 TAB 剔除迁移, 依据 Models 模型表重新精算出非 TAB 分母, 仅触发 1 次。
	// 仅重算「全局」TotalCacheEligibleInputTokens, 刻意不覆盖 Pools["antigravity"].CacheEligibleInputTokens
	// (池分母由 TrackRequestForPool/Force 各自按池累加, 全局与池分母两套独立口径, 串扰会让池命中率暴跌到 0)。
	if !t.stats.TabExcludedFromEligible {
		t.RecalculateCacheEligibleTokensLocked()
		t.stats.TabExcludedFromEligible = true
		t.scheduleSave()
	}

	// NVIDIA 号池历史差量自愈合并: recordNvidiaUsage 的第 4 落点(TrackRequestForModel)上线晚于
	// usage.json 记账, 历史 NVIDIA 模型量只躺在 usage.json 未进 stats.Models 模型表/全局标量/
	// Pools["nvidia"]。此处从同目录 usage.json 聚合 nvidia 账号差量一次性合并, 幂等
	// (NvidiaUsageBackfillDone 标志 + diff 恒收敛), 与上方两个一次性迁移范式同构。
	// 仅在本地持久化模式编排; 无 persistPath(纯内存/测试)时跳过, 不产生副作用。
	if t.persistPath != "" {
		agg := LoadNvidiaModelAggregatesFromDisk(filepath.Dir(t.persistPath))
		t.backfillNvidiaModelsFromUsageLocked(agg)
	}

	if len(t.trends) <= 6 {
		t.seedEmptyTrends()
	}
}

func (t *Tracker) seedEmptyTrends() {
	t.trends = make([]*HourlyTrend, 0)
}
