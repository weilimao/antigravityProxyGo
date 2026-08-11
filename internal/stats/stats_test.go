package stats

import (
	"math"
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

func TestAddRequestLogInMemoryOnly(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = "" // Prevent file serialization during testing

	log := &RequestLog{
		ID:           "test_req_1",
		Timestamp:    time.Now().Format("01/02 15:04:05"),
		Method:       "POST",
		Host:         "api.openai.com",
		Path:         "/v1/chat/completions/generateContent", // satisfying isRealModel (contains generatecontent)
		Model:        "gemini-3.5-flash",
		Account:      "user_123",
		InTokens:     100,
		OutTokens:    50,
		CachedTokens: 20,
		StatusCode:   200,
		DurationMs:   250,
	}

	tracker.AddRequestLogInMemoryOnly(log)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 1 {
		t.Fatalf("expected 1 request in memory, got %d", len(tracker.requests))
	}

	saved := tracker.requests[0]
	if saved.ID != "test_req_1" {
		t.Errorf("expected ID 'test_req_1', got '%s'", saved.ID)
	}
	if saved.Account != "user_123" {
		t.Errorf("expected Account 'user_123', got '%s'", saved.Account)
	}
	if saved.InTokens != 100 || saved.OutTokens != 50 || saved.CachedTokens != 20 {
		t.Errorf("tokens mismatch")
	}
}

// TestRequestLogLite_FirstByteProjection 验证 RequestLog.FirstByteMs 经 toRequestLogLite
// 端到端投影到 RequestLogLite.FirstByteMs, 供仪表盘热路径读出。
// 这条不变式是「响应时间」列不再恒为「-」的最后一公里保证:后端打点 → 内存结构 → IPC 投影。
func TestRequestLogLite_FirstByteProjection(t *testing.T) {
	start := time.Now().Add(-50 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	rec.MarkFirstByte()

	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = "" // Prevent file serialization during testing

	durationMs := int64(80)
	log := &RequestLog{
		ID:          "test_req_ttft",
		Timestamp:   time.Now().Format("01/02 15:04:05"),
		Method:      "POST",
		Host:        "integrate.api.nvidia.com",
		Path:        "/nvidia/v1/chat/completions/generateContent",
		Model:       "z-ai/glm-5.2",
		Account:     "nv-pool",
		InTokens:    100,
		OutTokens:   50,
		StatusCode:  200,
		DurationMs:  durationMs,
		FirstByteMs: rec.FirstByteMs(durationMs),
		Family:      "nvidia",
	}

	tracker.AddRequestLogInMemoryOnly(log)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 1 {
		t.Fatalf("expected 1 request in memory, got %d", len(tracker.requests))
	}
	saved := tracker.requests[0]
	if saved.FirstByteMs <= 0 {
		t.Fatalf("expected FirstByteMs > 0 after MarkFirstByte, got %d", saved.FirstByteMs)
	}
	if saved.FirstByteMs > durationMs {
		t.Fatalf("expected FirstByteMs ≤ durationMs(%d), got %d", durationMs, saved.FirstByteMs)
	}

	// 投影到 Lite 后字段仍应保持。
	lite := toRequestLogLite(saved)
	if lite.FirstByteMs != saved.FirstByteMs {
		t.Fatalf("toRequestLogLite lost FirstByteMs: lite=%d saved=%d", lite.FirstByteMs, saved.FirstByteMs)
	}

	// 未打点(default) 兜底为 durationMs: 复用既有 TestAddRequestLogInMemoryOnly 的日志(FirstByteMs=0)
	// 不在此处重复构造, 仅断言「打点后非 0」这一关键链路。
}

// TestRequestLogLite_ReasoningEffortProjection 验证 RequestLog.ReasoningEffort 经 toRequestLogLite
// 端到端投影到 RequestLogLite.ReasoningEffort, 供仪表盘热路径读出、前端「模型」列追加 (档) 后缀。
// 这条不变式是后端打点 → 内存结构 → IPC 投影链路不丢命中思考等级的保证。
func TestRequestLogLite_ReasoningEffortProjection(t *testing.T) {
	rl := &RequestLog{
		ID:              "test_req_effort",
		Timestamp:       time.Now().Format("01/02 15:04:05"),
		Model:           "z-ai/glm-5.2",
		Family:          "nvidia",
		ReasoningEffort: "max",
	}
	lite := toRequestLogLite(rl)
	if lite.ReasoningEffort != "max" {
		t.Fatalf("toRequestLogLite lost ReasoningEffort: lite=%q saved=%q", lite.ReasoningEffort, rl.ReasoningEffort)
	}
	if lite.Model != rl.Model {
		t.Errorf("toRequestLogLite lost Model: lite=%q saved=%q", lite.Model, rl.Model)
	}
	// 空串(客户端未开思考)投影也需正确保留, 前端凭空串判定不渲染后缀。
	rlEmpty := &RequestLog{ID: "test_req_effort_empty", Model: "grok-4"}
	liteEmpty := toRequestLogLite(rlEmpty)
	if liteEmpty.ReasoningEffort != "" {
		t.Errorf("toRequestLogLite empty ReasoningEffort: lite=%q want empty", liteEmpty.ReasoningEffort)
	}
}

// TestTrackNvidiaRequest_IndependentBucket 验证 NVIDIA 专用趋势桶与综合全局桶物理隔离:
//  - TrackNvidiaRequest 仅累加 nvidiaTrends, 不进 trends, 也不动全局 stats;
//  - 同时走一次 TrackRequest 验证综合桶独立累加, 两个桶各自计数、互不污染。
// 这条不变式是前端「综合趋势 / NVIDIA」双 Tab 切换语义正确的基石:
// 综合视图数值口径必须与改动前完全一致 (零回归), NVIDIA 视图只反映号池用量。
func TestTrackNvidiaRequest_IndependentBucket(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = "" // Prevent file serialization during testing

	// 一次 NVIDIA 号池请求 + 一次综合链路请求, 都落当前小时桶。
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50, 0)
	tracker.TrackRequest("gemini-3.5-flash", 200, 100, 10)

	tracker.RLock()
	defer tracker.RUnlock()

	// 全局 stats 不应被 NVIDIA 请求计入 (TrackNvidiaRequest 不动 stats)。
	// 此处 TotalRequests 应只反映 TrackRequest 那一次综合请求。
	if tracker.stats.TotalRequests != 1 {
		t.Errorf("global TotalRequests should remain 1 (only TrackRequest), got %d", tracker.stats.TotalRequests)
	}
	if tracker.stats.TotalInputTokens != 200 {
		t.Errorf("global TotalInputTokens should be 200 (only TrackRequest), got %d", tracker.stats.TotalInputTokens)
	}

	// 综合趋势桶 trends: 只应含 TrackRequest 那一次的 200/100/10 + 1 request。
	if len(tracker.trends) != 1 {
		t.Fatalf("expected 1 global trends bin, got %d", len(tracker.trends))
	}
	gBin := tracker.trends[0]
	if gBin.Requests != 1 || gBin.Input != 200 || gBin.Output != 100 || gBin.Cached != 10 {
		t.Errorf("global bin mismatch: reqs=%d in=%d out=%d cached=%d", gBin.Requests, gBin.Input, gBin.Output, gBin.Cached)
	}

	// NVIDIA 趋势桶 nvidiaTrends: 只应含 TrackNvidiaRequest 那一次 100/50 + 1 request, cached=0(本用例传 0)。
	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("expected 1 nvidia trends bin, got %d", len(tracker.nvidiaTrends))
	}
	nBin := tracker.nvidiaTrends[0]
	if nBin.Requests != 1 || nBin.Input != 100 || nBin.Output != 50 || nBin.Cached != 0 {
		t.Errorf("nvidia bin mismatch: reqs=%d in=%d out=%d cached=%d", nBin.Requests, nBin.Input, nBin.Output, nBin.Cached)
	}
	// NVIDIA 成本应非负 (rate 回退到 unknown 也应非负); cached=0 → cachedCost 必为 0。
	if nBin.Cost < 0 || nBin.InputCost < 0 || nBin.OutputCost < 0 || nBin.CachedCost != 0 {
		t.Errorf("nvidia cost should be non-negative with zero cached cost: cost=%v in=%v out=%v cached=%v",
			nBin.Cost, nBin.InputCost, nBin.OutputCost, nBin.CachedCost)
	}
}

// TestTrackNvidiaRequest_AccruesCached 验证 TrackNvidiaRequest 透传 cached 后, nvidiaTrends 桶
// 的 Cached/CachedCost 被如实累加(修复「日志显示命中但 NVIDIA Tab 趋势/卡片为 0」的口径断层),
// 同时确认全局 stats 标量(TotalCachedTokens)仍不被 nvidiaTrends 桶污染(物理隔离不变式)。
func TestTrackNvidiaRequest_AccruesCached(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	// 一次 NVIDIA 号池请求, 上游末帧 cached_tokens=600(模拟 DeepSeek/兼容上游回报缓存命中)。
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 1000, 50, 600)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("expected 1 nvidia trends bin, got %d", len(tracker.nvidiaTrends))
	}
	nBin := tracker.nvidiaTrends[0]
	if nBin.Cached != 600 {
		t.Errorf("nvidia bin Cached = %d, want 600 (cached 未透传到 nvidiaTrends 桶)", nBin.Cached)
	}
	// CachedCost 按 rate.Cached 计价后非负; cached>0 且 rate.Cached>0 时应 > 0。
	// z-ai/glm-5.2 在 pricing 表未显式登记 → 回退 unknown 的 Cached=0.25, 故 CachedCost 应 > 0。
	if nBin.CachedCost <= 0 {
		t.Errorf("nvidia bin CachedCost = %v, want > 0 (cached=600 × rate.Cached 应产生缓存命中成本)", nBin.CachedCost)
	}

	// 物理隔离不变式: nvidiaTrends 桶累加 cached 不应污染全局 stats 标量。
	// TotalCachedTokens 仍由落点4 TrackRequestForModel 单独累加, 本用例未调它, 故必为 0。
	if tracker.stats.TotalCachedTokens != 0 {
		t.Errorf("global TotalCachedTokens = %d, want 0 (nvidiaTrends 桶累加 cached 不得污染全局标量)",
			tracker.stats.TotalCachedTokens)
	}
	// 综合桶摩擦不变式: nvidiaTrends 写入不进 trends。
	if len(tracker.trends) != 0 {
		t.Errorf("global trends should stay empty, got %d bins (nvidiaTrends 不得污染综合桶)", len(tracker.trends))
	}

	// === InputCost 口径回归(2026-08-11 修复) ===
	// 历史缺陷: TrackNvidiaRequest 的 inputCost 曾用全量 inTokens(含 cached) × rate.Input,
	// 致缓存命中那部分在 InputCost 按 rate.Input 重算一次, 违反
	// 「Cost = InputCost + OutputCost + CachedCost」恒等式, 前端 NVIDIA Tab 出现
	// 「输入总成本 > 总成本」反向数值。修复后 inputCost 应基于 nonCachedIn(=inTokens-cached)。
	rate := pm.GetPricingForModel("z-ai/glm-5.2")
	nonCachedIn := 1000 - 600 // 400
	wantInputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	wantCachedCost := math.Round((float64(600)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

	if math.Abs(nBin.InputCost-wantInputCost) > 1e-9 {
		t.Errorf("nvidia bin InputCost = %v, want %v (应基于 nonCachedIn=%d 扣除 cached, 不得用全量 inTokens×rate.Input)",
			nBin.InputCost, wantInputCost, nonCachedIn)
	}
	// 恒等式: 三项之和 == Cost(允许 1e-6 round 累积误差)。
	sumThree := math.Round((nBin.InputCost+nBin.OutputCost+nBin.CachedCost)*1000000.0) / 1000000.0
	if math.Abs(sumThree-nBin.Cost) > 1e-6 {
		t.Errorf("nvidia cost identity broken: InputCost(%v)+OutputCost(%v)+CachedCost(%v)=%v != Cost=%v "+
			"(应满足 Cost = InputCost + OutputCost + CachedCost)", nBin.InputCost, nBin.OutputCost, nBin.CachedCost, sumThree, nBin.Cost)
	}
	// 反向不变式: InputCost 不得超过 Cost(rate.Cached < rate.Input 时, 含 cached 的请求 InputCost 应小于 Cost)。
	if nBin.InputCost > nBin.Cost+1e-9 {
		t.Errorf("nvidia bin InputCost(%v) > Cost(%v): 输入总成本不应超过总成本 (cached 段被误并入 InputCost 是旧缺陷特征)",
			nBin.InputCost, nBin.Cost)
	}
	// wantXxx 自洽性自检(防 pricing 表漂移导致断言失真, 纯保护性)。
	if wantCachedCost <= 0 {
		t.Errorf("test fixture invalid: wantCachedCost=%v (rate.Cached 异常, 断言基准失效)", wantCachedCost)
	}
}

// TestTrackNvidiaRequest_InputCostExcludesCached 是 TestTrackNvidiaRequest_AccruesCached 的
// 独立镜像用例, 专注校验「InputCost 不吞 cached 段」这一条不变式, 意图明确便于回归定位。
// 背景:2026-08-11 修复 TrackNvidiaRequest 的 inputCost 口径(由 inTokens×rate.Input 改为
// nonCachedIn×rate.Input), 与 TrackRequest/TrackRequestForModel 同构。本用例用极简数据复现
// 修复前后的差异:in=1000/cached=800 时, 修复前 InputCost≈1000×Input(虚高), 修复后≈200×Input。
func TestTrackNvidiaRequest_InputCostExcludesCached(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 1000, 100, 800)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("expected 1 nvidia trends bin, got %d", len(tracker.nvidiaTrends))
	}
	nBin := tracker.nvidiaTrends[0]

	rate := pm.GetPricingForModel("z-ai/glm-5.2")
	nonCachedIn := 200 // 1000 - 800
	wantInputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	wantCachedCost := math.Round((float64(800)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

	if math.Abs(nBin.InputCost-wantInputCost) > 1e-9 {
		t.Errorf("InputCost = %v, want %v (nonCachedIn=%d × rate.Input; 修复前会算成 1000 × rate.Input 虚高 5x)",
			nBin.InputCost, wantInputCost, nonCachedIn)
	}
	if math.Abs(nBin.CachedCost-wantCachedCost) > 1e-9 {
		t.Errorf("CachedCost = %v, want %v (800 × rate.Cached)", nBin.CachedCost, wantCachedCost)
	}
	// 恒等式必须成立。
	sumThree := math.Round((nBin.InputCost+nBin.OutputCost+nBin.CachedCost)*1000000.0) / 1000000.0
	if math.Abs(sumThree-nBin.Cost) > 1e-6 {
		t.Errorf("cost identity broken: %v != Cost %v", sumThree, nBin.Cost)
	}
}

// TestTrackNvidiaRequest_SameHourAccumulation 验证同一小时多次 NVIDIA 请求会累加到
// 同一 nvidiaTrends 桶 (Requests 递增、Cost 精度 1e6 round), 而不会各自新建桶。
func TestTrackNvidiaRequest_SameHourAccumulation(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50, 0)
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 30, 70, 0)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("expected 1 nvidia bin after 2 same-hour calls, got %d", len(tracker.nvidiaTrends))
	}
	bin := tracker.nvidiaTrends[0]
	if bin.Requests != 2 {
		t.Errorf("expected 2 requests accumulated, got %d", bin.Requests)
	}
	if bin.Input != 130 || bin.Output != 120 {
		t.Errorf("expected cumulated in=130 out=120, got in=%d out=%d", bin.Input, bin.Output)
	}
	// 全局桶与全局 stats 全程不应被触及。
	if len(tracker.trends) != 0 {
		t.Errorf("global trends should stay empty, got %d bins", len(tracker.trends))
	}
	if tracker.stats.TotalRequests != 0 {
		t.Errorf("global TotalRequests should stay 0, got %d", tracker.stats.TotalRequests)
	}
}

// TestGetPayload_IncludesNvidiaTrends 验证 GetPayload 下发 nvidiaTrends 字段且与 trends 隔离,
// 前端据此切换 Tab 数据源。
func TestGetPayload_IncludesNvidiaTrends(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	tracker.TrackRequest("gemini-3.5-flash", 200, 100, 10)
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50, 0)

	payload := tracker.GetPayload(nil)

	nvRaw, ok := payload["nvidiaTrends"]
	if !ok {
		t.Fatal("GetPayload missing nvidiaTrends key")
	}
	nv, ok := nvRaw.([]*HourlyTrend)
	if !ok {
		t.Fatalf("nvidiaTrends wrong type: %T", nvRaw)
	}
	if len(nv) != 1 || nv[0].Requests != 1 || nv[0].Input != 100 || nv[0].Output != 50 {
		t.Errorf("nvidiaTrends payload wrong: %+v", nv)
	}

	trRaw, ok := payload["trends"]
	if !ok {
		t.Fatal("GetPayload missing trends key")
	}
	tr, ok := trRaw.([]*HourlyTrend)
	if !ok {
		t.Fatalf("trends wrong type: %T", trRaw)
	}
	// 综合桶只含 TrackRequest 那次, GPU 请求不应混入。
	if len(tr) != 1 || tr[0].Requests != 1 || tr[0].Input != 200 {
		t.Errorf("trends payload should exclude nvidia usage: %+v", tr)
	}
}

// TestCacheEligibleInputTokens_GeminiOnlyNvidiaExcluded 验证缓存命中率分母专用累加器
// TotalCacheEligibleInputTokens 的口径: 仅 TrackRequest(gemini/claude 直连) 累加其 input,
// TrackRequestForModel(NVIDIA 号池链路) 与 TrackNvidiaRequest(nvidiaTrends 专用桶) 均不累积。
// 这是前端「缓存命中率」不被 NVIDIA input 永久稀释的关键不变式——NVIDIA 上游 OpenAI Chat
// 协议无 cache, cachedTokens 恒 0, 若其 input 计入分母会导致命中率虚假偏低。
func TestCacheEligibleInputTokens_GeminiOnlyNvidiaExcluded(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = "" // Prevent file serialization during testing

	// gemini 直连: input=200, cached=10 → 分母应累加 200
	tracker.TrackRequest("gemini-3.5-flash", 200, 100, 10)
	// NVIDIA 号池(经 TrackRequestForModel): input=500, cached=0 → 分母不应累加
	tracker.TrackRequestForModel("z-ai/glm-5.2", 500, 250, 0)
	// NVIDIA 趋势桶(经 TrackNvidiaRequest): 仅写 nvidiaTrends, 不动全局 stats
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 300, 150, 0)

	tracker.RLock()
	defer tracker.RUnlock()

	// 分母: 只含 gemini 那次 200, NVIDIA 完全排除。
	if tracker.stats.TotalCacheEligibleInputTokens != 200 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 200 (only gemini, NVIDIA excluded)",
			tracker.stats.TotalCacheEligibleInputTokens)
	}
	// 总输入口径不变(含 NVIDIA): 200 + 500 = 700; TrackNvidiaRequest 不写全局 stats。
	if tracker.stats.TotalInputTokens != 700 {
		t.Errorf("TotalInputTokens = %d, want 700 (gemini 200 + nvidia 500)", tracker.stats.TotalInputTokens)
	}
	// 命中 Token 分子: 只 gemini 贡献 10。
	if tracker.stats.TotalCachedTokens != 10 {
		t.Errorf("TotalCachedTokens = %d, want 10", tracker.stats.TotalCachedTokens)
	}

	// 期望命中率 = 10 / 200 = 5%。若分母误含 NVIDIA(700), 命中率会被稀释为 1.43%。
	wantHitRate := 5.0
	gotHitRate := 0.0
	if tracker.stats.TotalCacheEligibleInputTokens > 0 {
		gotHitRate = float64(tracker.stats.TotalCachedTokens) / float64(tracker.stats.TotalCacheEligibleInputTokens) * 100.0
	}
	if gotHitRate != wantHitRate {
		t.Errorf("hit rate = %.2f%%, want %.2f%% (got diluted by NVIDIA?)", gotHitRate, wantHitRate)
	}
}

// TestCacheEligibleInputTokens_GetPayloadProjection 验证新增字段经 GetPayload 深拷贝下行,
// 供前端「缓存命中率」作分母; 与既有 TotalInputTokens 字段并存, 二者各司其职。
func TestCacheEligibleInputTokens_GetPayloadProjection(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	tracker.TrackRequest("gemini-3.5-flash", 200, 100, 10)
	tracker.TrackRequestForModel("z-ai/glm-5.2", 500, 250, 0)

	payload := tracker.GetPayload(nil)
	statsObj, ok := payload["stats"].(GlobalStats)
	if !ok {
		t.Fatalf("payload stats wrong type: %T", payload["stats"])
	}
	if statsObj.TotalCacheEligibleInputTokens != 200 {
		t.Errorf("payload TotalCacheEligibleInputTokens = %d, want 200", statsObj.TotalCacheEligibleInputTokens)
	}
	if statsObj.TotalInputTokens != 700 {
		t.Errorf("payload TotalInputTokens = %d, want 700", statsObj.TotalInputTokens)
	}
}

