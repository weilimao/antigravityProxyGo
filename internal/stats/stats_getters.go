package stats

// 只读 getter 簇：从 stats.go 抽离（同包）。


// GetTotalRetries 轻量级读取，避免 GetPayload 的全量深拷贝
func (t *Tracker) GetTotalRetries() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalRetries
}

// GetTotalRequests 轻量级读取全局总请求数, 供外部(含单测)不做全量 payload 深拷贝即能校验
// 落点4(TrackRequestForFamily)是否被触发。与 GetTotalRetries 同口径, 读锁内返回标量。
func (t *Tracker) GetTotalRequests() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalRequests
}

// GetTotalCachedTokens 轻量级读取全局累计缓存命中 token(TotalCachedTokens), 供单测断言
// TrackRequestForModel 的 cached 透传口径(缓存命中率分子)真实写入而非恒 0。
func (t *Tracker) GetTotalCachedTokens() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalCachedTokens
}

// GetRequestLogCount 轻量级读取内存请求日志条数, 供外部(含单测)校验落点5
// (AddRequestLogForFamily)是否把日志写入了内存 requests 快照。读锁内返回长度, 不回切片别名。
func (t *Tracker) GetRequestLogCount() int {
	t.RLock()
	defer t.RUnlock()
	return len(t.requests)
}

// GetRecentRequestFirstByteMs 轻量级读取最近一条内存请求日志的 FirstByteMs, 供单测端到端
// 断言 TTFT 打点链路(FirstByteRecorder → RequestLog.FirstByteMs)真实闭环而非恒 0。
// 无日志时返回 -1。读锁内取值, 不回切片别名。
//
// 口径说明: AddRequestLog/TrackRequestForModel/AddRequestLogForFamily 均以 prepend 语义把
// 新日志插在 requests[0] (见 stats_requestlog.go), 故"最近一条"= requests[0], 而非
// requests[len-1] (后者是最旧的一条)。历史上此处误读 [len-1], 单日志场景下 [0]==[len-1]
// 未暴露; 多日志断言时 getter 读到的其实是首条, 与"最近"语义错位。今统一改为读 [0] 对齐
// prepend 语义, 单测相应以"每子测试独立 handler/tracker (仅一记录)"隔离避免跨用例污染。
func (t *Tracker) GetRecentRequestFirstByteMs() int64 {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return -1
	}
	return t.requests[0].FirstByteMs
}

// GetRecentRequestCacheStatus 轻量级读取最近一条内存请求日志的 CacheStatus, 供单测端到端
// 断言缓存命中链路(record*Usage 的 cached>0 → CacheStatus="HIT")真实闭环而非恒 "NONE"。
// 无日志时返回 ""。读锁内取值, 不回切片别名。
// 口径同 GetRecentRequestFirstByteMs: 最近一条 = requests[0] (prepend 语义)。
func (t *Tracker) GetRecentRequestCacheStatus() string {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return ""
	}
	return t.requests[0].CacheStatus
}

// GetRecentRequestBody 轻量级读取最近一条内存请求日志的 RequestBody, 供单测端到端断言
// 号池直连链路(record*Usage)入站请求体落库链路(logCtx.ReqBody → stats.RequestLog.RequestBody)
// 真实闭环而非恒 nil(前端「请求参数详情」弹窗恒落「无请求参数」兜底)。
// 无日志时返回 nil。读锁内取值, 不回切片别名。
// 口径同 GetRecentRequestFirstByteMs: 最近一条 = requests[0] (prepend 语义)。
func (t *Tracker) GetRecentRequestBody() interface{} {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return nil
	}
	return t.requests[0].RequestBody
}

// GetRecentRequestHeaders 轻量级读取最近一条内存请求日志的 RequestHeaders, 供单测端到端断言
// 号池直连链路(record*Usage)入站请求头落库链路(logCtx.ReqHeaders → stats.RequestLog.RequestHeaders)
// 真实闭环而非恒 nil(前端「请求参数详情」弹窗恒落「无请求头数据」兜底)。
// 无日志时返回 nil。读锁内取值, 不回切片别名。
// 口径同 GetRecentRequestFirstByteMs: 最近一条 = requests[0] (prepend 语义)。
func (t *Tracker) GetRecentRequestHeaders() interface{} {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return nil
	}
	return t.requests[0].RequestHeaders
}

// GetRecentRequestReasoningEffort 轻量级读取最近一条内存请求日志的 ReasoningEffort, 供单测端到端
// 断言命中思考等级落库链路(logCtx.ReasoningEffort → stats.RequestLog.ReasoningEffort)
// 真实闭环而非恒 ""(前端「模型」列命中思考等级后缀渲染的口径)。
// 无日志时返回 ""。读锁内取值, 不回切片别名。
// 口径同 GetRecentRequestFirstByteMs: 最近一条 = requests[0] (prepend 语义)。
func (t *Tracker) GetRecentRequestReasoningEffort() string {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return ""
	}
	return t.requests[0].ReasoningEffort
}

// GetNvidiaTrends 轻量级读取 NVIDIA 号池专用趋势桶的深拷贝, 供 app.go 远程中继分支
// 在手工组装 stats-updated payload 时携带本地 nvidiaTrends (该分支走 remote query 不
// 调 GetPayload, 故需单独取)。线程安全: 读锁内值拷贝每个 HourlyTrend, 与 GetPayload 的
// trendsCopy 同口径, 避免返回内部切片别名导致的并发写竞争。
func (t *Tracker) GetNvidiaTrends() []*HourlyTrend {
	t.RLock()
	defer t.RUnlock()
	copyOut := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, tr := range t.nvidiaTrends {
		copyOut[i] = &HourlyTrend{
			Time:       tr.Time,
			Input:      tr.Input,
			Output:     tr.Output,
			Cached:     tr.Cached,
			Requests:   tr.Requests,
			Cost:       tr.Cost,
			InputCost:  tr.InputCost,
			OutputCost: tr.OutputCost,
			CachedCost: tr.CachedCost,
		}
	}
	return copyOut
}
