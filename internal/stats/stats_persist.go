package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/fileutil"
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

	// 深拷贝完成后立即释放读锁(关键: 任何后续序列化/IO/DB 操作都不得在持锁状态下进行,
	// 否则首次落盘即泄漏读锁, 后续 TrackRequest*(写锁)与 GetPayload*(读锁)全量死等,
	// 对外表现为"请求进不来/日志与首帧与趋势全部停更")。
	t.RUnlock()

	// 请求日志不再随 stats.json 持久化: 150 条完整报文曾让落盘文件膨胀到 12MB+,
	// 且流量期间每 3s 一次的全量序列化+写盘是常驻 IO/GC 抖动源。
	// 报文持久化已收敛到 SQLite request_logs(增量单行写入), requests 字段写出为空、
	// 仅保留 JSON 键位兼容旧读侧; LoadFromDisk 读到存量时一次性迁入 DB 后不再回读。
	data := StatsData{
		Stats:        statsCopy,
		Trends:       trendsCopy,
		NvidiaTrends: nvidiaTrendsCopy,
	}

	bytesData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to marshal stats: %v\n", err)
		return
	}

	err = fileutil.WriteFileAtomic(path, bytesData, 0644)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to write stats: %v\n", err)
		return
	}

	// 落盘成功节拍上顺带修剪 DB 超窗报文(仅最新 MaxRequestLogs 条保留报文, 防 request_logs
	// 随报文无限膨胀); 挂载现有防抖节拍不新增定时器, DB 未初始化时函数内直接返回。
	_ = db.PruneLocalRequestBodies(MaxRequestLogs)
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

	// ===== 请求日志环形缓冲回填(双轨) =====
	// 1) 存量迁移: 老 stats.json 的 requests(含完整报文) 一次性幂等迁入 SQLite request_logs,
	//    此后 stats.json 不再携带该数组(SaveToDisk 已停写), 文件体积回归 KB 级。
	t.migrateLegacyRequestsToDB(parsed.Requests)
	// 2) 内存环种子: DB 可用时从 request_logs 回填最新 MaxRequestLogs 条(重启后请求列表与
	//    「查看详情」报文仍在, 优于旧行为); 仅在 DB 不可用/无数据时兜底沿用文件内 requests
	//    (兼容无库的单测/纯内存运行环境, 行为与迁移前等价)。
	if seeded := t.recentRequestRingFromDB(); len(seeded) > 0 {
		t.requests = seeded
	} else {
		t.requests = parsed.Requests
		for _, req := range t.requests {
			req.RequestBody = TruncateRequestBody(req.RequestBody)
		}
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

	// 综合趋势桶以盘上数据为准, 不再设"桶数过少则清空"的门槛:
	// 旧实现 len(t.trends) <= 6 时强制 seedEmptyTrends(), 在 stats.json 被重建/截断后
	// 会进入死循环式清空——重启即归零、永远涨不过 6 桶, 而 nvidiaTrends 无此门槛,
	// 直接造成「综合趋势空 / NVIDIA Tab 有数据」的割裂现象。
	// 此处仅把 nil 规范化为空切片(保持「始终非 nil」约定), 不丢盘上任何既有桶。
	if t.trends == nil {
		t.trends = make([]*HourlyTrend, 0)
	}
}

func (t *Tracker) seedEmptyTrends() {
	t.trends = make([]*HourlyTrend, 0)
}

// migrateLegacyRequestsToDB 将 stats.json 存量请求日志一次性迁入 request_logs 表。
// Upsert 按 req_id 去重(双写时代 DB 里可能已有同 req_id 标量行 → 仅补报文/缓存标记;
// DB 里缺失(如库被重建过) → 整行插入)。重复启动重复执行亦幂等无副作用。
// DB 未初始化(纯内存运行/单测环境)时整体跳过, 不产生副作用。
func (t *Tracker) migrateLegacyRequestsToDB(legacy []*RequestLog) {
	if len(legacy) == 0 || db.GlobalDB == nil {
		return
	}
	migrated := 0
	for _, r := range legacy {
		if r == nil || r.ID == "" {
			continue
		}
		item := &db.RequestLog{
			ReqID:           r.ID,
			Timestamp:       legacyTimestampToRFC3339(r.Timestamp),
			Mode:            "local",
			UserID:          r.Account,
			ModelName:       r.Model,
			InTokens:        r.InTokens,
			OutTokens:       r.OutTokens,
			CachedTokens:    r.CachedTokens,
			Cost:            r.Cost,
			DurationMs:      r.DurationMs,
			FirstByteMs:     r.FirstByteMs,
			StatusCode:      r.StatusCode,
			Method:          r.Method,
			Host:            r.Host,
			Path:            r.Path,
			SessionID:       r.SessionID,
			Family:          r.Family,
			ReasoningEffort: r.ReasoningEffort,
			RequestBody:     requestBodyToDBString(r.RequestBody),
			RequestHeaders:  requestBodyToDBString(r.RequestHeaders),
			CacheStatus:     r.CacheStatus,
		}
		if _, err := db.UpsertLocalRequestLog(item); err == nil {
			migrated++
		}
	}
	if migrated > 0 {
		fmt.Printf("[StatsTracker] Migrated %d legacy request logs from stats.json into database\n", migrated)
	}
}

// recentRequestRingFromDB 从 SQLite request_logs 回填内存环(最新 MaxRequestLogs 条, DESC 序与环
// 「首元素最新」口径一致)。DB 不可用或无本地日志时返回 nil, 调用方自行走文件兜底分支。
// RFC3339 时间戳回转为环内展示口径 "01/02 15:04:05"(与 app_monitor.go 远端分支转换同构)。
// Input/Output/Cached 成本分量列不入环: 内存环不消费, 从 DB 读回后由前端按单价口径展示 Cost 总量。
func (t *Tracker) recentRequestRingFromDB() []*RequestLog {
	if db.GlobalDB == nil {
		return nil
	}
	rows := db.QueryRecentLocalRequests(MaxRequestLogs)
	if len(rows) == 0 {
		return nil
	}
	out := make([]*RequestLog, 0, len(rows))
	for _, r := range rows {
		ts := r.Timestamp
		if parsed, err := time.Parse(time.RFC3339, r.Timestamp); err == nil {
			ts = parsed.Local().Format("01/02 15:04:05")
		}
		out = append(out, &RequestLog{
			ID:              r.ReqID,
			Timestamp:       ts,
			Method:          r.Method,
			Host:            r.Host,
			Path:            r.Path,
			Model:           r.ModelName,
			InTokens:        r.InTokens,
			OutTokens:       r.OutTokens,
			CachedTokens:    r.CachedTokens,
			CacheStatus:     r.CacheStatus,
			StatusCode:      r.StatusCode,
			Cost:            r.Cost,
			Account:         r.UserID,
			RequestBody:     requestBodyFromDBString(r.RequestBody),
			RequestHeaders:  requestBodyFromDBString(r.RequestHeaders),
			SessionID:       r.SessionID,
			DurationMs:      r.DurationMs,
			FirstByteMs:     r.FirstByteMs,
			Family:          r.Family,
			ReasoningEffort: r.ReasoningEffort,
		})
	}
	return out
}

// legacyTimestampToRFC3339 把 stats.json 的 "01/02 15:04:05" 显示时间折算为 RFC3339。
// 显示格式不含年份: 先按当前年重构, 若结果晚于当前时刻(跨年边界: 12 月的日志在 1 月启动)则回退一年。
// 解析失败(历史脏数据)兜底当前时刻, 保证落库 timestamp 恒为 ≥19 字符 RFC3339
// (hourly trends 重建迁移依赖 substr 长度判定)。
func legacyTimestampToRFC3339(ts string) string {
	now := time.Now()
	parsed, err := time.ParseInLocation("01/02 15:04:05", ts, time.Local)
	if err != nil {
		return now.Format(time.RFC3339)
	}
	full := time.Date(now.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.Local)
	if full.After(now) {
		full = full.AddDate(-1, 0, 0)
	}
	return full.Format(time.RFC3339)
}
