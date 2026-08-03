package main

import (
	"antigravity-proxy/internal/corelog"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/stats"
	"context"
	"runtime"
	"runtime/debug"
	"sort"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_monitor.go: 后台监控 — startMemoryMonitor 内存心跳 / emitMemoryStats 上报 / getStatsPayload 统计面板载荷。
// 从 app.go 按职责拆分而出,同 main 包内共享 App 结构体与全局符号,物理搬移,逻辑逐行等价,零回归。

func (a *App) startMemoryMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	a.emitMemoryStats()

	trendCounter := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.IsWindowVisibleAndActive() {
				a.emitMemoryStats()
			}

			trendCounter++
			if trendCounter >= 6 { // 6 * 10s = 60s
				trendCounter = 0
				if a.IsWindowVisibleAndActive() {
					wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
				}
				// Periodically force the Go runtime to release unused heap
				// memory back to the OS. Under heavy concurrent traffic the
				// runtime may retain freed pages; this caps RSS growth.
				debug.FreeOSMemory()
			}
		}
	}
}

func (a *App) emitMemoryStats() {
	total, count, cpuPercent, err := stats.GetAppMemoryStats()
	if err != nil {
		return
	}

	// Read Go runtime actual heap allocation (actual in-use memory by Go objects)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// 使用 corelog 异步输出，避免后台监控协程 fmt.Printf 写 stdout 被下游背压阻塞，
	// 导致内存监控协程卡死后连带拖垮其它 handler。
	corelog.Printf("[DEBUG MEMORY] Total RSS: %.2f MB | Go Heap: %.2f MB | Go Sys: %.2f MB | Proc Count: %d | CPU: %.1f%%\n",
		float64(total)/(1024*1024), float64(ms.HeapAlloc)/(1024*1024), float64(ms.Sys)/(1024*1024), count, cpuPercent)

	wailsRuntime.EventsEmit(a.ctx, "memory-stats-updated", map[string]interface{}{
		"total":        total,
		"processCount": count,
		"heapAlloc":    ms.HeapAlloc,
		"cpuUsage":     cpuPercent,
	})
}

// getStatsPayload 获取隔离或原生的统计载荷快照
func (a *App) getStatsPayload(simplified bool) map[string]interface{} {
	// Skip the heavy usage deep-copy on the 1s hot path; usage data is only
	// sent on the 60s heartbeat (simplified=false) and explicit get-state.
	var usagePayload interface{}
	if !simplified {
		usagePayload = a.usageTracker.GetPayload()
	}
	if a.remoteRelay != nil && a.remoteRelay.GetConfig().Connected {
		cfg := a.remoteRelay.GetConfig()
		// No remote log syncing to local database anymore. All metrics are pre-aggregated and queried on-demand.

		if remoteStats, err := a.remoteRelay.FetchRemoteStats(); err == nil && remoteStats != nil {
			// 完全使用远端数据构建一套纯净的 GlobalStats
			statsObj := stats.GlobalStats{
				Models: make(map[string]*stats.ModelStats),
			}
			if tr, _ := remoteStats["totalRequests"].(float64); tr > 0 {
				statsObj.TotalRequests = int(tr)
			}
			if ti, _ := remoteStats["totalInputTokens"].(float64); ti > 0 {
				statsObj.TotalInputTokens = int(ti)
			}
			if to, _ := remoteStats["totalOutputTokens"].(float64); to > 0 {
				statsObj.TotalOutputTokens = int(to)
			}
			if tc, _ := remoteStats["totalCachedTokens"].(float64); tc > 0 {
				statsObj.TotalCachedTokens = int(tc)
			}
			// 远端命中率分母: 旧版中继服务器无此字段时为 0, 前端兜底回退 totalInputTokens。
			// 仅新版中继服务器(internal/relay StatsTracker.RecordUsage 已按 nvidia/ 前缀排除累加)
			// 部署后下发精确值, 使远端模式命中率口径与本地一致。
			if cei, _ := remoteStats["totalCacheEligibleInputTokens"].(float64); cei > 0 {
				statsObj.TotalCacheEligibleInputTokens = int(cei)
			}
			if cost, _ := remoteStats["totalCost"].(float64); cost > 0 {
				statsObj.TotalCost = cost
			}

			if rmObj, ok := remoteStats["models"].(map[string]interface{}); ok {
				for k, vObj := range rmObj {
					if mObj, mok := vObj.(map[string]interface{}); mok {
						mStats := &stats.ModelStats{}
						if reqs, _ := mObj["requestCount"].(float64); reqs > 0 {
							mStats.Reqs = int(reqs)
						}
						if inT, _ := mObj["inputTokens"].(float64); inT > 0 {
							mStats.InTokens = int(inT)
						}
						if outT, _ := mObj["outputTokens"].(float64); outT > 0 {
							mStats.OutTokens = int(outT)
						}
						if cacheT, _ := mObj["cachedTokens"].(float64); cacheT > 0 {
							mStats.CachedTokens = int(cacheT)
						}
						if mc, _ := mObj["totalCost"].(float64); mc > 0 {
							mStats.Cost = mc
						}
						statsObj.Models[k] = mStats
					}
				}
			}

			// 恢复历史数据：从 SQLite 聚合出旧的 local trends（因为远端服务器升级前可能没有记录旧的历史）
			localTrends := db.QueryHourlyTrends(cfg.UserKey, "remote")

			trendMap := make(map[string]*stats.HourlyTrend)
			for _, dt := range localTrends {
				trendMap[dt.Time] = &stats.HourlyTrend{
					Time:       dt.Time,
					Input:      dt.Input,
					Output:     dt.Output,
					Cached:     dt.Cached,
					Requests:   dt.Requests,
					Cost:       dt.Cost,
					InputCost:  dt.InputCost,
					OutputCost: dt.OutputCost,
					CachedCost: dt.CachedCost,
				}
			}

			// Fetch hourly aggregated trends directly from the remote relay server
			if remoteTrends, err := a.remoteRelay.FetchRemoteTrends(); err == nil {
				for _, dt := range remoteTrends {
					// 远端数据优先级更高，覆盖本地（因为远端可能包含了其他设备共享的中继数据）
					trendMap[dt.Time] = &stats.HourlyTrend{
						Time:       dt.Time,
						Input:      dt.Input,
						Output:     dt.Output,
						Cached:     dt.Cached,
						Requests:   dt.Requests,
						Cost:       dt.Cost,
						InputCost:  dt.InputCost,
						OutputCost: dt.OutputCost,
						CachedCost: dt.CachedCost,
					}
				}
			}

			var trends []*stats.HourlyTrend
			var keys []string
			for k := range trendMap {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				trends = append(trends, trendMap[k])
			}

			if trends == nil {
				trends = []*stats.HourlyTrend{}
			}

			dbRequests := db.QueryRecentRequests(cfg.UserKey, "remote", 50)
			var requests []*stats.RequestLog
			for _, dr := range dbRequests {
				formattedTime := dr.Timestamp
				if t, err := time.Parse(time.RFC3339, dr.Timestamp); err == nil {
					formattedTime = t.Local().Format("01/02 15:04:05")
				}
				requests = append(requests, &stats.RequestLog{
					ID:           dr.ReqID,
					Timestamp:    formattedTime,
					Model:        dr.ModelName,
					InTokens:     dr.InTokens,
					OutTokens:    dr.OutTokens,
					CachedTokens: dr.CachedTokens,
					Cost:         dr.Cost,
					Account:      dr.UserID,
					DurationMs:   dr.DurationMs,
					StatusCode:   dr.StatusCode,
					Method:       dr.Method,
					Host:         dr.Host,
					Path:         dr.Path,
					SessionID:    dr.SessionID,
					Family:       dr.Family,
				})
			}
			if requests == nil {
				requests = []*stats.RequestLog{}
			}

			return map[string]interface{}{
				"stats":        statsObj,
				"trends":       trends,
				"nvidiaTrends": a.statsTracker.GetNvidiaTrends(),
				"requests":     requests,
				"usage":        usagePayload,
			}
		}
	}

	// 本地模式或者远端获取失败时，保持完全的原生本地快照
	if simplified {
		return a.statsTracker.GetPayloadSimplified(usagePayload)
	}
	return a.statsTracker.GetPayload(usagePayload)
}
