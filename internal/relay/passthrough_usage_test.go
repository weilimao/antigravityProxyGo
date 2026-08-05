package relay

import (
	"testing"
	"time"

	"antigravity-proxy/internal/stats"
)

// TestRecordOtherUsage_FiresLandings4_WhenTrackerInjected 验证: globalStatsTracker 注入时,
// recordOtherUsage 的
//   - 落点3 (TrackRequestForModel): 全局综合统计 TotalRequests +1, Model 走展示名;
//   - 落点4 (AddRequestLogForFamily): 内存请求日志 +1, family=other 写入 (经 GetRequestLogCount 断言)。
//
// 与 recordNvidiaUsage 对偶, 证 Other 号池请求现在能进仪表盘「请求日志」+「模型统计」+「综合趋势」。
func TestRecordOtherUsage_FiresLandings34_WhenTrackerInjected(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	userSession := &RelaySession{Token: "tok-other-1", UserID: "u-other-1", SessionKey: "auth:acc:other1234567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(5 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := passthroughLogCtx{
		Method:       "POST",
		Host:         "token-plan.cn-beijing.maas.aliyuncs.com",
		Path:         "/route/v1/messages",
		SessionID:    "auth:acc:other1234567890",
		Account:      "u-other-1",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 100, 50, 0, nil, logCtx)

	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点3 not fired: TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点4 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}

	// 端到端断言: 打点后落点4 请求日志 FirstByteMs 应 > 0, 验证 TTFT 链路 (FirstByteRecorder → RequestLog.FirstByteMs)。
	lastFirstByte := gt.GetRecentRequestFirstByteMs()
	if lastFirstByte <= 0 {
		t.Errorf("expected last request FirstByteMs > 0 after MarkFirstByte, got %d", lastFirstByte)
	}
}

// TestRecordOtherUsage_SkipsLanding34_WhenTrackerNil 验证 globalStatsTracker==nil 时 recordOtherUsage
// 的落点3/4 全安全跳过, 不 panic (与 recordNvidiaUsage 降级语义一致)。
func TestRecordOtherUsage_SkipsLanding34_WhenTrackerNil(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	// 不调 SetGlobalStatsTracker → globalStatsTracker 保持 nil

	userSession := &RelaySession{Token: "tok-other-2", UserID: "u-other-2", SessionKey: "auth:acc:otherfailbeef0123"}
	logCtx := passthroughLogCtx{
		Method:     "POST",
		Host:       "token-plan.cn-beijing.maas.aliyuncs.com",
		Path:       "/route/v1/messages",
		SessionID:  "auth:acc:otherfailbeef0123",
		Account:    "u-other-2",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	// 不应 panic
	handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 100, 50, 0, nil, logCtx)
}

// TestRecordOtherUsage_CacheHit_PropagatesCached 验证 cached>0 时:
//   - 落点3 TrackRequestForModel 的 cached 透传 (缓存命中率分母/分子口径);
//   - 落点4 RequestLog.CachedTokens 写入 + CacheStatus="HIT" (而非恒 "NONE")。
//
// 这是缓存命中率修复的核心回归: 之前 recordOtherUsage 硬编码 CachedTokens:0/CacheStatus:"NONE",
// 导致 Other 号池 (如 AliYun DeepSeek 返回 prompt_cache_hit_tokens) 命中率恒 0%。
func TestRecordOtherUsage_CacheHit_PropagatesCached(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-other-cache", UserID: "u-other-cache", SessionKey: "auth:acc:othercache0000001"}
	logCtx := passthroughLogCtx{
		Method:     "POST",
		Host:       "token-plan.cn-beijing.maas.aliyuncs.com",
		Path:       "/route/v1/messages",
		SessionID:  "auth:acc:othercache0000001",
		Account:    "u-other-cache",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	// cached=2000 命中缓存
	handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 5000, 80, 2000, nil, logCtx)

	if got := gt.GetRecentRequestCacheStatus(); got != "HIT" {
		t.Errorf("cached>0 期望 CacheStatus=HIT, 实际=%q", got)
	}

	// 落点3 cached 透传口径: TotalCachedTokens 累加 = 2000 (经 TrackRequestForModel 第4参)。
	if got := gt.GetTotalCachedTokens(); got != 2000 {
		t.Errorf("cached>0 期望全局缓存 token=2000, 实际=%d", got)
	}
}

// TestRecordOtherUsage_SkipsOnZeroUsage 验证 (input==0 && output==0) 时整函数早退,
// 不触发落点3/4 (与 recordNvidiaUsage 保护同口径, 避免空桶/噪声日志)。
func TestRecordOtherUsage_SkipsOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordOtherUsage(&RelaySession{UserID: "u-other-3"}, "deepseek-v4-flash-0731", 0, 0, 0, nil, passthroughLogCtx{StartTs: time.Now()})

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("zero-usage should not fire 落点3: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("zero-usage should not fire 落点4: log count = %d, want %d", got, beforeLogs)
	}
}

// TestPassthroughHostFromBaseURL 验证上游账号 BaseURL 到裸 host 的提取, 回退占位 "other"。
func TestPassthroughHostFromBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://token-plan.cn-beijing.maas.aliyuncs.com/v1": "token-plan.cn-beijing.maas.aliyuncs.com",
		"https://api.deepseek.com":                            "api.deepseek.com",
		"":                                                    "other",
	}
	for in, want := range cases {
		if got := passthroughHostFromBaseURL(in); got != want {
			t.Errorf("passthroughHostFromBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	// 非法 URL 兜底也须非空且不 panic(与 nvidiaHostFromBaseURL 相同容忍度)。
	if h := passthroughHostFromBaseURL("://bad-url"); h == "" {
		t.Error("passthroughHostFromBaseURL fallback should return non-empty for malformed input")
	}
}