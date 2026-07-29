package stats

import (
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

// newTestTracker 构造一个不落盘的 Tracker, 供本文件用例复用。
// persistPath="" 使 SaveToDisk/scheduleSave 走 no-op 分支, 测试不碰磁盘。
// 单测环境不带 db.GlobalDB, AddRequestLogForFamily 的落库 goroutine 会安静吞 error,
// 不 panic; 本文件只断言内存 requests 与 stats/trends/nvidiaTrends 口径。
func newTestTracker() *Tracker {
	tracker := NewTracker(pricing.NewManager())
	tracker.persistPath = ""
	return tracker
}

// TestTrackRequestForModel_AccumulatesCorrectly 验证落点4把 NVIDIA 用量正确累加到
// 全局综合统计: 顶部指标卡(Total*) + 模型表(stats.Models) + 综合趋势桶(trends)。
func TestTrackRequestForModel_AccumulatesCorrectly(t *testing.T) {
	tracker := newTestTracker()

	tracker.TrackRequestForModel("z-ai/glm-5.2", 1000, 500, 0)
	tracker.TrackRequestForModel("z-ai/glm-5.2", 200, 80, 0)

	tracker.RLock()
	defer tracker.RUnlock()

	if tracker.stats.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", tracker.stats.TotalRequests)
	}
	if tracker.stats.TotalInputTokens != 1200 {
		t.Errorf("TotalInputTokens = %d, want 1200", tracker.stats.TotalInputTokens)
	}
	if tracker.stats.TotalOutputTokens != 580 {
		t.Errorf("TotalOutputTokens = %d, want 580", tracker.stats.TotalOutputTokens)
	}
	// NVIDIA 上游无 cache, 全程 CachedTokens 应为 0。
	if tracker.stats.TotalCachedTokens != 0 {
		t.Errorf("TotalCachedTokens = %d, want 0 (NVIDIA no cache)", tracker.stats.TotalCachedTokens)
	}

	m, ok := tracker.stats.Models["z-ai/glm-5.2"]
	if !ok {
		t.Fatalf("model z-ai/glm-5.2 missing from stats.Models")
	}
	if m.Reqs != 2 || m.InTokens != 1200 || m.OutTokens != 580 || m.CachedTokens != 0 {
		t.Errorf("model stats mismatch: %+v", m)
	}

	// 综合趋势桶应收到两次请求, 同小时桶累加。
	if len(tracker.trends) != 1 {
		t.Fatalf("expected 1 global trends bin, got %d", len(tracker.trends))
	}
	g := tracker.trends[0]
	if g.Requests != 2 || g.Input != 1200 || g.Output != 580 || g.Cached != 0 {
		t.Errorf("global bin mismatch: reqs=%d in=%d out=%d cached=%d", g.Requests, g.Input, g.Output, g.Cached)
	}
}

// TestTrackRequestForModel_DoesNotPolluteNvidiaTrends 验证落点4只写综合桶, 不污染
// nvidiaTrends 物理隔离桶——这是「综合趋势」(含NVIDIA) 与「NVIDIA Tab」(纯NVIDIA) 双视图
// 不发生错误双重计数的不变式基石。
func TestTrackRequestForModel_DoesNotPolluteNvidiaTrends(t *testing.T) {
	tracker := newTestTracker()

	tracker.TrackRequestForModel("z-ai/glm-5.2", 100, 50, 0)

	tracker.RLock()
	defer tracker.RUnlock()

	// nvidiaTrends 应保持空: 落点4不写它, 只有后端 recordNvidiaUsage 内的 TrackNvidiaRequest 才写。
	if len(tracker.nvidiaTrends) != 0 {
		t.Errorf("nvidiaTrends should stay empty, got %d bins (落点4 must not pollute nvidiaTrends)", len(tracker.nvidiaTrends))
	}
	// 综合桶应收到这次。
	if len(tracker.trends) != 1 {
		t.Fatalf("global trends should have 1 bin, got %d", len(tracker.trends))
	}
}

// TestTrackRequestForModel_WithTrackNvidiaRequest_NoDoubleCounting 模拟后端 recordNvidiaUsage
// 的真实调用形态: TrackNvidiaRequest(落点3, 写 nvidiaTrends) + TrackRequestForModel(落点4, 写综合桶)
// 被同一笔请求先后调用。验证两桶各自累加 1 次而非任一桶被累加 2 次——综合视图与 NVIDIA 视图口径不同,
// 不构成错误重复, 但同一桶不应被同一笔请求累加两次。
func TestTrackRequestForModel_WithTrackNvidiaRequest_NoDoubleCounting(t *testing.T) {
	tracker := newTestTracker()

	tracker.TrackNvidiaRequest("z-ai/glm-5.2", 100, 50)        // 落点3: 仅 nvidiaTrends
	tracker.TrackRequestForModel("z-ai/glm-5.2", 100, 50, 0) // 落点4: 仅综合桶

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.nvidiaTrends) != 1 {
		t.Fatalf("nvidiaTrends should have exactly 1 bin, got %d", len(tracker.nvidiaTrends))
	}
	if tracker.nvidiaTrends[0].Requests != 1 {
		t.Errorf("nvidiaTrends bin Requests = %d, want 1 (落点3 累加一次)", tracker.nvidiaTrends[0].Requests)
	}
	if len(tracker.trends) != 1 {
		t.Fatalf("global trends should have exactly 1 bin, got %d", len(tracker.trends))
	}
	if tracker.trends[0].Requests != 1 {
		t.Errorf("global trends bin Requests = %d, want 1 (落点4 累加一次)", tracker.trends[0].Requests)
	}
}

// TestAddRequestLogForFamily_BypassesIsRealModelFilter 验证落点5绕过既有 AddRequestLog 的
// isRealModel 过滤(要求 Path 含 generatecontent/predict): NVIDIA 走 /v1/chat/completions,
// 若按旧过滤会被全量丢弃。本方法应把不含关键词的 NVIDIA 路径日志保留入 requests。
func TestAddRequestLogForFamily_BypassesIsRealModelFilter(t *testing.T) {
	tracker := newTestTracker()

	rl := &RequestLog{
		ID:        "nv-log-1",
		Timestamp: time.Now().Format("01/02 15:04:05"),
		Method:    "POST",
		Host:      "integrate.api.nvidia.com",
		Path:      "/nvidia/v1/chat/completions", // 不含 generatecontent/predict
		Model:     "z-ai/glm-5.2",
		InTokens:   100,
		OutTokens: 30,
		CacheStatus: "NONE",
		StatusCode: 200,
		Account:    "u-1",
		Family:     "nvidia",
		DurationMs: 120,
	}
	tracker.AddRequestLogForFamily(rl)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 1 {
		t.Fatalf("expected 1 request kept (filter bypassed), got %d", len(tracker.requests))
	}
	got := tracker.requests[0]
	if got.ID != "nv-log-1" {
		t.Errorf("ID = %s, want nv-log-1", got.ID)
	}
	if got.Family != "nvidia" {
		t.Errorf("Family = %s, want nvidia", got.Family)
	}
	if got.Model != "z-ai/glm-5.2" {
		t.Errorf("Model = %s, want z-ai/glm-5.2", got.Model)
	}
}

// TestAddRequestLogForFamily_SkipsEmptyModel 验证落点5 仍保留 "Model==""||unknown" 跳过,
// 避免写入无模型名的噪声日志(与既有 AddRequestLog 同口径, 仅放过 isRealModel 过滤)。
func TestAddRequestLogForFamily_SkipsEmptyModel(t *testing.T) {
	tracker := newTestTracker()

	for _, m := range []string{"", "unknown"} {
		tracker.AddRequestLogForFamily(&RequestLog{ID: "x", Model: m, Path: "/v1/chat/completions", Family: "nvidia"})
	}

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 0 {
		t.Errorf("empty/unknown model logs should be skipped, got %d", len(tracker.requests))
	}
}

// TestAddRequestLogForFamily_TruncatesTo50 验证 requests 内存上限 50 条(FIFO),
// 与既有 AddRequestLog 同构——避免在高频 NVIDIA 流量下内存列表无界增长。
func TestAddRequestLogForFamily_TruncatesTo50(t *testing.T) {
	tracker := newTestTracker()

	for i := 0; i < 60; i++ {
		tracker.AddRequestLogForFamily(&RequestLog{
			ID:         "nv-" + time.Now().Format("150405.000000") + "-" + itoa(i),
			Timestamp:  time.Now().Format("01/02 15:04:05"),
			Method:     "POST",
			Host:       "integrate.api.nvidia.com",
			Path:       "/nvidia/v1/chat/completions",
			Model:      "z-ai/glm-5.2",
			InTokens:    1,
			OutTokens:   1,
			CacheStatus: "NONE",
			StatusCode:  200,
			Family:      "nvidia",
		})
	}

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 50 {
		t.Errorf("requests should be capped at 50, got %d", len(tracker.requests))
	}
	// 最新的应排在最前(newest first, AddRequestLog 同构的 prepend 语义)。
	if tracker.requests[0].Family != "nvidia" {
		t.Errorf("newest request Family = %s, want nvidia", tracker.requests[0].Family)
	}
}

// TestRequestLogLite_FamilyProjection 验证 RequestLogLite 轻量投影带上下行 Family,
// 供前端按族渲染 NVIDIA badge。这是落点5 日志经 GetPayload → IPC 热路径到达前端的关键字段。
func TestRequestLogLite_FamilyProjection(t *testing.T) {
	rl := &RequestLog{
		ID:        "nv-proj",
		Model:     "z-ai/glm-5.2",
		Family:    "nvidia",
		Path:      "/nvidia/v1/chat/completions",
	}
	lite := toRequestLogLite(rl)
	if lite.Family != "nvidia" {
		t.Errorf("RequestLogLite.Family = %s, want nvidia", lite.Family)
	}
	if lite.Model != "z-ai/glm-5.2" {
		t.Errorf("RequestLogLite.Model = %s, want z-ai/glm-5.2", lite.Model)
	}
}

// itoa 是避免引入 strconv 的小工具(保持文件零新依赖)。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
