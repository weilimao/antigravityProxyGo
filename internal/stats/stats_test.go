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

