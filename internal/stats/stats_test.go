package stats

import (
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
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50)
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

	// NVIDIA 趋势桶 nvidiaTrends: 只应含 TrackNvidiaRequest 那一次 100/50 + 1 request, cached=0。
	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("expected 1 nvidia trends bin, got %d", len(tracker.nvidiaTrends))
	}
	nBin := tracker.nvidiaTrends[0]
	if nBin.Requests != 1 || nBin.Input != 100 || nBin.Output != 50 || nBin.Cached != 0 {
		t.Errorf("nvidia bin mismatch: reqs=%d in=%d out=%d cached=%d", nBin.Requests, nBin.Input, nBin.Output, nBin.Cached)
	}
	// NVIDIA 成本应非负 (rate 回退到 unknown 也应非负)。
	if nBin.Cost < 0 || nBin.InputCost < 0 || nBin.OutputCost < 0 || nBin.CachedCost != 0 {
		t.Errorf("nvidia cost should be non-negative with zero cached cost: cost=%v in=%v out=%v cached=%v",
			nBin.Cost, nBin.InputCost, nBin.OutputCost, nBin.CachedCost)
	}
}

// TestTrackNvidiaRequest_SameHourAccumulation 验证同一小时多次 NVIDIA 请求会累加到
// 同一 nvidiaTrends 桶 (Requests 递增、Cost 精度 1e6 round), 而不会各自新建桶。
func TestTrackNvidiaRequest_SameHourAccumulation(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = ""

	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50)
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 30, 70)

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
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50)

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
	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 300, 150)

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

