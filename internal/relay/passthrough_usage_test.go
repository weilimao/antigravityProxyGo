package relay

import (
	"net/http"
	"testing"
	"time"

	"antigravity-proxy/internal/stats"
)

// TestRecordOtherUsage_FiresLandings4_WhenTrackerInjected 验证: globalStatsTracker 注入?
// recordOtherUsage ?
//   - 落点3 (TrackRequestForModel): 全局综合统计 TotalRequests +1, Model 走展示名;
//   - 落点4 (AddRequestLogForFamily): 内存请求日志 +1, family=other 写入 (?GetRequestLogCount 断言)?
//
// ?recordNvidiaUsage 对偶, ?Other 号池请求现在能进仪表盘「请求日志?「模型统计?「综合趋势」?
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

	// 端到端断言: 打点后落? 请求日志 FirstByteMs ?> 0, 验证 TTFT 链路 (FirstByteRecorder ?RequestLog.FirstByteMs)?
	lastFirstByte := gt.GetRecentRequestFirstByteMs()
	if lastFirstByte <= 0 {
		t.Errorf("expected last request FirstByteMs > 0 after MarkFirstByte, got %d", lastFirstByte)
	}
}

// TestRecordOtherUsage_ReasoningEffortPropagates 验证命中上游思考等级落库链路(Other 号池):
// logCtx.ReasoningEffort 经 recordOtherUsage → stats.RequestLog.ReasoningEffort 真实闭环,
// 供前端「模型」列追加 (档) 后缀展示。high, max(Other 走官方 OpenAI 取值集, max 1:1 透传)与空串三态覆盖。
func TestRecordOtherUsage_ReasoningEffortPropagates(t *testing.T) {
	// 每个子用例独立注入 fresh tracker。AddRequestLogForFamily 以 prepend 语义落库
	// (新日志=requests[0]), GetRecentRequestReasoningEffort 现亦读 requests[0] 对齐
	// (历史误读 [len-1] 已修, 见 stats_getters.go 口径说明)。此隔离使每子用例仅 1 条记录,
	// 断言不依赖"后落即最新"隐含时序, 兼作子用例间卫生隔离 (与 NVIDIA/Grok 同款)。
	userSession := &RelaySession{Token: "tok-other-eff", UserID: "u-other-eff", SessionKey: "auth:acc:other1234567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()

	t.Run("high", func(t *testing.T) {
		handler, _, _, _ := newNvidiaTestHandler(t, nil)
		gt := makeInjectedGlobalTracker(t)
		handler.SetGlobalStatsTracker(gt)
		logCtx := passthroughLogCtx{
			Method:          "POST",
			Host:            "token-plan.cn-beijing.maas.aliyuncs.com",
			Path:            "/route/v1/chat/completions",
			SessionID:       "auth:acc:other1234567890",
			Account:         "u-other-eff",
			StatusCode:      200,
			StartTs:         start,
			FirstByteRec:    rec,
			ReasoningEffort: "high",
		}
		handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 100, 50, 0, nil, logCtx)
		if got := gt.GetRecentRequestReasoningEffort(); got != "high" {
			t.Errorf("high 落库: ReasoningEffort = %q, want %q", got, "high")
		}
	})
	t.Run("empty", func(t *testing.T) {
		handler, _, _, _ := newNvidiaTestHandler(t, nil)
		gt := makeInjectedGlobalTracker(t)
		handler.SetGlobalStatsTracker(gt)
		logCtx := passthroughLogCtx{
			Method:       "POST",
			Host:         "token-plan.cn-beijing.maas.aliyuncs.com",
			Path:         "/route/v1/chat/completions",
			SessionID:    "auth:acc:other1234567890",
			Account:      "u-other-eff",
			StatusCode:   200,
			StartTs:      start,
			FirstByteRec: rec,
		}
		handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 100, 50, 0, nil, logCtx)
		if got := gt.GetRecentRequestReasoningEffort(); got != "" {
			t.Errorf("空串落库: ReasoningEffort = %q, want empty", got)
		}
	})
}

// TestRecordOtherUsage_SkipsLanding34_WhenTrackerNil 验证 globalStatsTracker==nil ?recordOtherUsage
// 的落?/4 全安全跳? ?panic (?recordNvidiaUsage 降级语义一??
func TestRecordOtherUsage_SkipsLanding34_WhenTrackerNil(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	// 不调 SetGlobalStatsTracker ?globalStatsTracker 保持 nil

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

// TestRecordOtherUsage_CacheHit_PropagatesCached 验证 cached>0 ?
//   - 落点3 TrackRequestForModel ?cached 透传 (缓存命中率分?分子口径);
//   - 落点4 RequestLog.CachedTokens 写入 + CacheStatus="HIT" (而非?"NONE")?
//
// 这是缓存命中率修复的核心回归: 之前 recordOtherUsage 硬编?CachedTokens:0/CacheStatus:"NONE",
// 导致 Other 号池 (?AliYun DeepSeek 返回 prompt_cache_hit_tokens) 命中率恒 0%?
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

	// 落点3 cached 透传口径: TotalCachedTokens 累加 = 2000 (?TrackRequestForModel ???
	if got := gt.GetTotalCachedTokens(); got != 2000 {
		t.Errorf("cached>0 期望全局缓存 token=2000, 实际=%d", got)
	}
}

// TestRecordOtherUsage_SkipsOnZeroUsage 验证 (input==0 && output==0) 时整函数早退,
// 不触发落?/4 (?recordNvidiaUsage 保护同口? 避免空桶/噪声日志)?
func TestRecordOtherUsage_FiresLogOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordOtherUsage(&RelaySession{UserID: "u-other-3"}, "deepseek-v4-flash-0731", 0, 0, 0, nil, passthroughLogCtx{StartTs: time.Now()})

	// Fix: zero-usage (upstream omitted usage) must still record points 3/4 so the
	// completed 200 request appears in the request log. Only points 1/2 (token accounting)
	// are skipped on zero-usage; points 3/4 fire unconditionally.
	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("zero-usage should still fire point3: TotalRequests = %d, want %d", got, beforeReqs+1)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("zero-usage should still fire point4: log count = %d, want %d", got, beforeLogs+1)
	}
}

// TestRecordOtherUsage_PersistsBodyAndHeaders 验证 Other 号池链路入站请求?请求头落?
// recordOtherUsage ?logCtx.ReqBody / logCtx.ReqHeaders 落到 stats.RequestLog.RequestBody /
// RequestHeaders, 使前端「请求参数详情」弹窗能展示入站请求?请求头而非兜底文案。敏感头脱敏?
func TestRecordOtherUsage_PersistsBodyAndHeaders(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-other-bh", UserID: "u-other-bh", SessionKey: "auth:acc:otherbh000000001"}
	logCtx := passthroughLogCtx{
		Method:       "POST",
		Host:         "token-plan.cn-beijing.maas.aliyuncs.com",
		Path:         "/route/v1/chat/completions",
		SessionID:    "auth:acc:otherbh000000001",
		Account:      "u-other-bh",
		StatusCode:   200,
		StartTs:      time.Now(),
		FirstByteRec: stats.NewFirstByteRecorder(time.Now()),
		ReqBody:      parseInboundBodyForLog([]byte(`{"model":"deepseek-v4-flash-0731","messages":[{"role":"user","content":"hi"}]}`)),
		ReqHeaders:   collectInboundHeadersForLog(http.Header{"Authorization": {"Bearer ds-secret"}, "Content-Type": {"application/json"}}),
	}
	handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 100, 50, 0, nil, logCtx)

	body := gt.GetRecentRequestBody()
	if body == nil {
		t.Fatalf("RequestBody = nil, want 入站结构化 body; 详情弹窗将恒落「无请求参数」兜底")
	}
	if bodyMap, ok := body.(map[string]interface{}); !ok || bodyMap["model"] != "deepseek-v4-flash-0731" {
		t.Errorf("RequestBody.model 未透传, got %v", body)
	}
	headers := gt.GetRecentRequestHeaders()
	if headers == nil {
		t.Fatalf("RequestHeaders = nil, want 非空映射; 详情弹窗将恒落「无请求头数据」兜底")
	}
	headersMap, ok := headers.(map[string]interface{})
	if !ok {
		t.Fatalf("RequestHeaders 类型 = %T, want map[string]interface{}", headers)
	}
	if got := headersMap["Authorization"]; got != "<redacted>" {
		t.Errorf("RequestHeaders[Authorization] = %v, want \"<redacted>\"", got)
	}
	if got := headersMap["Content-Type"]; got != "application/json" {
		t.Errorf("RequestHeaders[Content-Type] = %v, want application/json", got)
	}
}

// TestRecordGoogleUsage_PersistsBodyAndHeaders 验证 Antigravity 直连链路入站请求?请求头落?
// (对偶 TestRecordOtherUsage_PersistsBodyAndHeaders), 鉴权头脱敏、协议头原样?
func TestRecordGoogleUsage_PersistsBodyAndHeaders(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-ag-bh", UserID: "u-ag-bh", SessionKey: "auth:acc:googlebh"}
	acc := mkGoogleAccount("ag-bh", "ag-bh@example.com")
	logCtx := googleLogCtx{
		Method:       http.MethodPost,
		Host:         "daily-cloudcode-pa.googleapis.com",
		Path:         "/v1internal:generateContent",
		SessionID:    "auth:acc:googlebh",
		Account:      "ag-bh",
		StatusCode:   200,
		StartTs:      time.Now(),
		FirstByteRec: stats.NewFirstByteRecorder(time.Now()),
		ReqBody:      parseInboundBodyForLog([]byte(`{"project":"fav-syn-001","contents":[{"role":"user"}]}`)),
		ReqHeaders:   collectInboundHeadersForLog(http.Header{"Authorization": {"Bearer gaia-secret"}, "X-Goog-Api-Key": {"goog-key"}, "Content-Type": {"application/json"}}),
	}
	handler.recordGoogleUsage(userSession, "gemini-3-flash-agent", 100, 30, 0, acc, logCtx)

	body := gt.GetRecentRequestBody()
	if body == nil {
		t.Fatalf("RequestBody = nil, want 入站结构化 body; 详情弹窗将恒落「无请求参数」兜底")
	}
	if bodyMap, ok := body.(map[string]interface{}); !ok || bodyMap["project"] != "fav-syn-001" {
		t.Errorf("RequestBody.project 未透传, got %v", body)
	}
	headers := gt.GetRecentRequestHeaders()
	if headers == nil {
		t.Fatalf("RequestHeaders = nil, want 非空映射; 详情弹窗将恒落「无请求头数据」兜底")
	}
	headersMap, ok := headers.(map[string]interface{})
	if !ok {
		t.Fatalf("RequestHeaders 类型 = %T, want map[string]interface{}", headers)
	}
	if got := headersMap["Authorization"]; got != "<redacted>" {
		t.Errorf("RequestHeaders[Authorization] = %v, want \"<redacted>\"", got)
	}
	if got := headersMap["X-Goog-Api-Key"]; got != "<redacted>" {
		t.Errorf("RequestHeaders[X-Goog-Api-Key] = %v, want \"<redacted>\"", got)
	}
	if got := headersMap["Content-Type"]; got != "application/json" {
		t.Errorf("RequestHeaders[Content-Type] = %v, want application/json", got)
	}
}

// TestPassthroughHostFromBaseURL 验证上游账号 BaseURL 到裸 host 的提? 回退占位 "other"?
func TestPassthroughHostFromBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://token-plan.cn-beijing.maas.aliyuncs.com/v1": "token-plan.cn-beijing.maas.aliyuncs.com",
		"https://api.deepseek.com":                           "api.deepseek.com",
		"":                                                   "other",
	}
	for in, want := range cases {
		if got := passthroughHostFromBaseURL(in); got != want {
			t.Errorf("passthroughHostFromBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	// 非法 URL 兜底也须非空且不 panic(?nvidiaHostFromBaseURL 相同容忍??
	if h := passthroughHostFromBaseURL("://bad-url"); h == "" {
		t.Error("passthroughHostFromBaseURL fallback should return non-empty for malformed input")
	}
}

// TestRecordOtherUsage_CachedZeroFallsBackToNone 验证 cached==0 ?CacheStatus="NONE"
// (前端 badge 渲染「直?(NONE)」的口径), ?cached>0 ?HIT 互补, 锁定开/关边界?
// 这是缓存命中率修复后 "缺值不报错" 的回归保? 上游未命中缓存时仍正常落? 命中?0?
func TestRecordOtherUsage_CachedZeroFallsBackToNone(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-other-none", UserID: "u-other-none", SessionKey: "auth:acc:othernonzero000001"}
	logCtx := passthroughLogCtx{
		Method:     "POST",
		Host:       "token-plan.cn-beijing.maas.aliyuncs.com",
		Path:       "/route/v1/messages",
		SessionID:  "auth:acc:othernonzero000001",
		Account:    "u-other-none",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	// cached=0 未命中缓?上游?cache_read/prompt_cache_hit 字段)
	handler.recordOtherUsage(userSession, "deepseek-v4-flash-0731", 53263, 108, 0, nil, logCtx)

	if got := gt.GetRecentRequestCacheStatus(); got != "NONE" {
		t.Errorf("cached==0 期望 CacheStatus=NONE, 实际=%q", got)
	}
}
