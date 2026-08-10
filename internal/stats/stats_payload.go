package stats

// Payload 投影簇：GetPayload / GetPayloadSimplified / GetRequestDetails。


func (t *Tracker) GetPayload(usagePayload interface{}) map[string]interface{} {
	t.RLock()
	defer t.RUnlock()

	// deep copy map/arrays for thread safety when returning payload
	modelsCopy := make(map[string]*ModelStats)
	for k, v := range t.stats.Models {
		modelsCopy[k] = &ModelStats{
			Reqs:         v.Reqs,
			InTokens:     v.InTokens,
			OutTokens:    v.OutTokens,
			CachedTokens: v.CachedTokens,
			Cost:         v.Cost,
		}
	}

	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        modelsCopy,
		Pools:                         copyPools(t.stats.Pools),
	}

	trendsCopy := make([]*HourlyTrend, len(t.trends))
	for i, trend := range t.trends {
		trendsCopy[i] = &HourlyTrend{
			Time:       trend.Time,
			Input:      trend.Input,
			Output:     trend.Output,
			Cached:     trend.Cached,
			Requests:   trend.Requests,
			Cost:       trend.Cost,
			InputCost:  trend.InputCost,
			OutputCost: trend.OutputCost,
			CachedCost: trend.CachedCost,
		}
	}

	// nvidiaTrendsCopy: 英伟达号池专用趋势桶深拷贝, 供前端「NVIDIA」Tab 消费;
	// 与综合趋势 trendsCopy 物理隔离, 二者在前端按 scope 切换, 互不污染。
	nvidiaTrendsCopy := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, trend := range t.nvidiaTrends {
		nvidiaTrendsCopy[i] = &HourlyTrend{
			Time:       trend.Time,
			Input:      trend.Input,
			Output:     trend.Output,
			Cached:     trend.Cached,
			Requests:   trend.Requests,
			Cost:       trend.Cost,
			InputCost:  trend.InputCost,
			OutputCost: trend.OutputCost,
			CachedCost: trend.CachedCost,
		}
	}

	// Lite projection: only scalar fields. requestBody / requestHeaders stay
	// in t.requests and are fetched on demand via GetRequestDetails.
	requestsCopy := make([]RequestLogLite, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = toRequestLogLite(req)
	}

	return map[string]interface{}{
		"stats":        statsCopy,
		"trends":       trendsCopy,
		"nvidiaTrends": nvidiaTrendsCopy,
		"requests":     requestsCopy,
		"usage":        usagePayload,
	}
}

func (t *Tracker) GetPayloadSimplified(usagePayload interface{}) map[string]interface{} {
	t.RLock()
	defer t.RUnlock()

	// deep copy map/arrays for thread safety when returning payload
	modelsCopy := make(map[string]*ModelStats)
	for k, v := range t.stats.Models {
		modelsCopy[k] = &ModelStats{
			Reqs:         v.Reqs,
			InTokens:     v.InTokens,
			OutTokens:    v.OutTokens,
			CachedTokens: v.CachedTokens,
			Cost:         v.Cost,
		}
	}

	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        modelsCopy,
		// Pools 随 stats-updated 一并下发供前端命中率卡片按池筛选;
		// 与 GetPayload 同口径深拷贝, 避免返回内部 map 别名导致的并发写竞争。
		Pools: copyPools(t.stats.Pools),
	}

	requestsCopy := make([]RequestLogLite, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = toRequestLogLite(req)
	}

	return map[string]interface{}{
		"stats":    statsCopy,
		"trends":   nil, // Omit trends to optimize memory/IPC overhead
		"requests": requestsCopy,
		"usage":    usagePayload,
	}
}

// GetRequestDetails returns the (truncated) requestBody and requestHeaders for
// a given request ID from the in-memory recent log buffer. Used by the frontend
// details modal to fetch heavy payload on demand instead of carrying it on
// every stats-updated tick. Returns nil/nil when the id is no longer in the
// 50-entry recent window (or in remote mode, where bodies aren't stored).
func (t *Tracker) GetRequestDetails(id string) (interface{}, interface{}) {
	t.RLock()
	defer t.RUnlock()
	for _, r := range t.requests {
		if r.ID == id {
			return r.RequestBody, r.RequestHeaders
		}
	}
	return nil, nil
}
