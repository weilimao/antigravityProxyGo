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
			if trendCounter >= 3 { // 3 * 10s = 30s
				trendCounter = 0
				if a.IsWindowVisibleAndActive() {
					a.emitEvent("stats-updated", a.getStatsPayload(false))
				}
				// 仅后台/挂机态修剪:窗口可见/前台使用时,TrimProcessWorkingSet(EmptyWorkingSet 全进程树)
				// 会把 Go 主进程及所有 WebView2 子进程的物理内存页驱逐回磁盘,下一次交互即触发缺页回盘风暴,
				// 这正是"每 30s 卡一下"的最强嫌疑源;故前台绝不修剪,只在后台托盘挂机时维持低内存水位。
				if !a.IsWindowVisibleAndActive() {
					stats.TrimProcessWorkingSet()
					debug.FreeOSMemory()
				}
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

	payload := map[string]interface{}{
		"total":        total,
		"processCount": count,
		"heapAlloc":    ms.HeapAlloc,
		"cpuUsage":     cpuPercent,
	}

	a.emitEvent("memory-stats-updated", payload)
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

		// 完全使用远端数据构建纯净的 GlobalStats，严禁回退使用本地单机历史数据
		statsObj := stats.GlobalStats{
			Models: make(map[string]*stats.ModelStats),
		}

		if remoteStats, err := a.remoteRelay.FetchRemoteStats(); err == nil && remoteStats != nil {
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
		}

		// 远端综合趋势：严格只展示远端中继服务产生的真实趋势数据，绝不读取或混入本地单机历史波浪线
		trendMap := make(map[string]*stats.HourlyTrend)
		if remoteTrends, err := a.remoteRelay.FetchRemoteTrends(); err == nil && remoteTrends != nil {
			for _, dt := range remoteTrends {
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

		// 远程请求日志：只展示当前远程连接所产生的请求记录
		dbRequests := db.QueryRecentRequests(cfg.UserKey, "remote", stats.MaxRequestLogs)
		var requests []*stats.RequestLog
		for _, dr := range dbRequests {
			formattedTime := dr.Timestamp
			if t, err := time.Parse(time.RFC3339, dr.Timestamp); err == nil {
				formattedTime = t.Local().Format("01/02 15:04:05")
			}
			requests = append(requests, &stats.RequestLog{
				ID:              dr.ReqID,
				Timestamp:       formattedTime,
				Model:           dr.ModelName,
				InTokens:        dr.InTokens,
				OutTokens:       dr.OutTokens,
				CachedTokens:    dr.CachedTokens,
				Cost:            dr.Cost,
				Account:         dr.UserID,
				DurationMs:      dr.DurationMs,
				StatusCode:      dr.StatusCode,
				Method:          dr.Method,
				Host:            dr.Host,
				Path:            dr.Path,
				SessionID:       dr.SessionID,
				Family:          dr.Family,
				ReasoningEffort: dr.ReasoningEffort,
			})
		}
		if requests == nil {
			requests = []*stats.RequestLog{}
		}

		return map[string]interface{}{
			"stats":        statsObj,
			"trends":       trends,
			"nvidiaTrends": []*stats.HourlyTrend{},
			"requests":     requests,
			"usage":        usagePayload,
		}
	}

	// 本地模式或者远端获取失败时，保持完全的原生本地快照
	if simplified {
		return a.statsTracker.GetPayloadSimplified(usagePayload)
	}
	return a.statsTracker.GetPayload(usagePayload)
}
