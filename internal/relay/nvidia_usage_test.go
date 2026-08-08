package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/stats"
)

// makeInjectedGlobalTracker 构造一个真实 stats.Tracker 并以临时目录 Init, 避免落盘污染
// 工作目录(relay 包无法直设私有 persistPath="", 故用 t.TempDir 通过公开 Init 让其安全写盘)。
// 落点5 的 DB 落库 goroutine 在单测环境因 db.GlobalDB==nil 走 error 吞分支, 不 panic;
// 内存 requests 段由 AddRequestLogForFamily 同步更新, 用 GetRequestLogCount 公开 getter 断言。
func makeInjectedGlobalTracker(t *testing.T) *stats.Tracker {
	t.Helper()
	gt := stats.NewTracker(pricing.NewManager())
	gt.Init(t.TempDir())
	return gt
}

// TestRecordNvidiaUsage_FiresLandings4And5_WhenTrackerInjected 验证: globalStatsTracker 注入时,
// recordNvidiaUsage 的
//   - 落点4 (TrackRequestForFamily): 全局综合统计 TotalRequests +1, Model 走去前缀展示名;
//   - 落点5 (AddRequestLogForFamily): 内存请求日志 +1, family=nvidia 写入 (经 GetRequestLogCount 断言);
// 同时 recordNvidiaUsage 的既有落点 (relay/usage/nvidiaTrends) 因 userSession==nil 在本用例被安全跳过,
// 不影响对落点4/5 的聚焦断言。
func TestRecordNvidiaUsage_FiresLandings4And5_WhenTrackerInjected(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	// userSession 必须非 nil: recordNvidiaUsage 落点2(usageTracker)有 `userSession==nil → return`,
	// 若传 nil 会提前 return 而跳过落点3/4/5。本用例落点1(relay StatsTracker)因 newNvidiaTestHandler
	// 把 h.statsTracker 装成 nil 而自动跳过, 不影响对落点4/5 的聚焦断言。
	// SessionKey 注入模拟正式生产口径:handleNvidia 入口经 ExtractSessionKey + auth:acc: 前缀算出后
	// 注入,recordNvidiaUsage 经 ocrSessionDisplay 取它填 logCtx.SessionID → 请求日志会话 ID 列。
	userSession := &RelaySession{Token: "tok-1", UserID: "u-1", SessionKey: "auth:acc:abc123def4567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	// 等待 > 1ms 再打点,避免 start 与 MarkFirstByte 同毫秒导致 Milliseconds() 截断为 0。
	time.Sleep(5 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := nvidiaLogCtx{
		Method:       "POST",
		Host:         "integrate.api.nvidia.com",
		Path:         "/nvidia/v1/chat/completions",
		SessionID:    "auth:acc:abc123def4567890",
		Account:      "u-1",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordNvidiaUsage(userSession, "nvidia/z-ai/glm-5.2", 100, 50, 0, nil, logCtx)

	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点4 not fired: TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点5 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}

	// 端到端断言:打点后落点5 的请求日志 FirstByteMs 应 > 0 且 ≤ durationMs,
	// 验证 FirstByteRecorder → nvidiaLogCtx.FirstByteRec → stats.RequestLog.FirstByteMs
	// 这条 TTFT 链路在 NVIDIA 号池入口真实闭环(不再恒为 0)。
	lastFirstByte := gt.GetRecentRequestFirstByteMs()
	if lastFirstByte <= 0 {
		t.Errorf("expected last request FirstByteMs > 0 after MarkFirstByte, got %d", lastFirstByte)
	}
}

// TestRecordNvidiaUsage_SkipsLandings4And5_WhenTrackerNil 验证 globalStatsTracker==nil (relay 单测默认装配、
// 以及未注入场景) 时, recordNvidiaUsage 的落点3/4/5 全安全跳过, 不 panic; 这是既有"降级跳过"语义
// 在新增落点4/5 后仍成立的回归保证。
func TestRecordNvidiaUsage_SkipsLandings4And5_WhenTrackerNil(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	// 不调 SetGlobalStatsTracker → globalStatsTracker 保持 nil

	userSession := &RelaySession{Token: "tok-2", UserID: "u-2", SessionKey: "auth:acc:failbeef01234567"}
	logCtx := nvidiaLogCtx{
		Method:     "POST",
		Host:       "integrate.api.nvidia.com",
		Path:       "/nvidia/v1/chat/completions",
		SessionID:  "auth:acc:failbeef01234567",
		Account:    "u-2",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	// 不应 panic
	handler.recordNvidiaUsage(userSession, "z-ai/glm-5.2", 100, 50, 0, nil, logCtx)
}

// TestRecordNvidiaUsage_SkipsOnZeroUsage 验证 (input==0 && output==0) 时整函数早退,
// 既不触发落点4也不触发落点5 (与既有保护同口径, 避免制造空桶/噪声日志)。
func TestRecordNvidiaUsage_SkipsOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordNvidiaUsage(&RelaySession{UserID: "u-3"}, "z-ai/glm-5.2", 0, 0, 0, nil, nvidiaLogCtx{StartTs: time.Now()})

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("zero-usage should not fire 落点4: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("zero-usage should not fire 落点5: log count = %d, want %d", got, beforeLogs)
	}
}

// TestRecordNvidiaUsage_CachedHitSetsHITStatus 验证 cached>0 透传链路:
// recordNvidiaUsage 收到 cached=40000 时,落点5 请求日志的 CacheStatus 应为 "HIT"(而非旧硬编码 "NONE"),
// CachedTokens 应为 40000;落点4 综合桶 TotalCachedTokens 应 +40000(缓存命中率分子真实写入);
// 落点2 号池账号维度 usageTracker 的 CachedTokens 也应为 40000。
// 当前 NVIDIA 官方 NIM 不回报 cache,cached 恒 0,本用例用 cached>0 模拟"未来/兼容上游回报 cache"场景,
// 保证 cached 透传链路(recordNvidiaUsage 新签名 → 落点2/4/5)真实闭环而非恒 0/"NONE"。
func TestRecordNvidiaUsage_CachedHitSetsHITStatus(t *testing.T) {
	handler, _, _, uTracker := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeCached := gt.GetTotalCachedTokens()
	beforeLogs := gt.GetRequestLogCount()

	// userSession 非 nil 以穿过落点2(usageTracker)的 userSession==nil 早退。
	userSession := &RelaySession{Token: "tok-hit", UserID: "u-hit", SessionKey: "auth:acc:abc123def4567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := nvidiaLogCtx{
		Method:       "POST",
		Host:         "integrate.api.nvidia.com",
		Path:         "/nvidia/v1/messages",
		SessionID:    "auth:acc:abc123def4567890",
		Account:      "u-hit",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	// cached=40000 模拟上游回报缓存命中(NVIDIA 官方 NIM 当前不回报,此处验证透传链路而非上游行为)。
	handler.recordNvidiaUsage(userSession, "z-ai/glm-5.2", 53263, 108, 40000, nil, logCtx)

	// 落点5:请求日志 CacheStatus=="HIT"。
	if got := gt.GetRecentRequestCacheStatus(); got != "HIT" {
		t.Errorf("落点5 CacheStatus = %q, want \"HIT\" (cached>0 应映射 HIT 而非旧硬编码 NONE)", got)
	}
	// 落点5:请求日志条数 +1。
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点5 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}
	// 落点4:综合桶 TotalCachedTokens +40000(缓存命中率分子真实写入)。
	if got := gt.GetTotalCachedTokens(); got != beforeCached+40000 {
		t.Errorf("落点4 TotalCachedTokens = %d, want %d (delta +40000)", got, beforeCached+40000)
	}
	// 落点2:usageTracker 号池账号维度聚合 Totals.CachedTokens 应为 40000。
	// 经 GetPayload 取 UsageState(无 poolAccount 时 AccountMeta 为 nil,RecordUsage 仍把 cached
	// 累加到 state.Totails 聚合,见 stats/usage.go RecordUsage 的 Totals 分支)。
	payload, ok := uTracker.GetPayload().(stats.UsageState)
	if !ok {
		t.Fatalf("落点2 GetPayload 类型断言失败, got %T", uTracker.GetPayload())
	}
	if got := payload.Totals.CachedTokens; got != 40000 {
		t.Errorf("落点2 usageTracker Totals.CachedTokens = %d, want 40000", got)
	}
}

// TestRecordNvidiaUsage_ZeroCachedStaysNONE 验证 cached==0 时 CacheStatus 仍为 "NONE",
// 即旧行为(前端紫色 NONE badge)无回归。当前 NVIDIA 官方 NIM 不回报 cache,真实链路恒走此分支。
func TestRecordNvidiaUsage_ZeroCachedStaysNONE(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-none", UserID: "u-none", SessionKey: "auth:acc:0000000000000000"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := nvidiaLogCtx{
		Method:       "POST",
		Host:         "integrate.api.nvidia.com",
		Path:         "/nvidia/v1/messages",
		SessionID:    "auth:acc:0000000000000000",
		Account:      "u-none",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordNvidiaUsage(userSession, "z-ai/glm-5.2", 500, 10, 0, nil, logCtx)

	if got := gt.GetRecentRequestCacheStatus(); got != "NONE" {
		t.Errorf("cached==0 时 CacheStatus = %q, want \"NONE\" (旧行为无回归)", got)
	}
}

// TestNvidiaHostFromBaseURL 验证上游账号 BaseURL 到裸 host 的提取, 与 gemini/claude 直连链路
// RequestLog.Host 只存裸 host 的口径一致。覆盖含/不含路径、带端口、空串、非法 URL 兜底分支。
func TestNvidiaHostFromBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://integrate.api.nvidia.com/v1", "integrate.api.nvidia.com"},
		{"https://integrate.api.nvidia.com", "integrate.api.nvidia.com"},
		{"http://localhost:8080/v1", "localhost:8080"},
		{"integrate.api.nvidia.com/v1", "integrate.api.nvidia.com"}, // 无协议 — 兜底分支
		{"", "nvidia"},                                            // 空串 — 回退占位
		{"   https://api.x.com/v1  ", "api.x.com"},                // 前后空白
		{"://bad-url", ""}, // url.Parse 解析不出 Host → 回退去前缀; 检查不 panic 即可
	}
	for _, c := range cases {
		got := nvidiaHostFromBaseURL(c.in)
		// 对非法 ://bad-url 仅要求不 panic 且非空(兜底返回非空 host 或原样), 其余精确匹配
		if c.want != "" && got != c.want {
			t.Errorf("nvidiaHostFromBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 单独确认非法输入兜底仍非空且不 panic
	if h := nvidiaHostFromBaseURL("://bad-url"); h == "" {
		t.Error("nvidiaHostFromBaseURL fallback should return non-empty for malformed input")
	}
}

// TestHandleNvidiaStream_TTFTReflectsFirstFrame 端到端实证 NVIDIA 号池流式 Anthropic 完整链路
// (handleNvidia → 选号 → 上游 SSE → 回译 → recordNvidiaUsage 落请求日志)的 TTFT 打点。
//
// 背景:handleNvidia 在用 bufio.Peek(1024) 嗅探首帧是否含上游 error 时(见 nvidia.go:511),
// 若上游首帧 <1024 字节(短回答/思考分隔等常见 LLM 输出形态),Peek 会阻塞等待足够的字节累积,
// 导致 writeNvidiaAnthropicStream 的 TTFT 打点(firstUpstreamByteHook)被推迟到「上游累积吐够
// 1024 字节」之后 → 落库 FirstByteMs 兜底≈DurationMs → 前端「响应时间==耗时」异常(截图现象)。
//
// 本用例用小首帧(<1024 字节)复现该 Bug:修复(把 TTFT 打点提前到上游响应头到达,即 writeNvidiaResponse
// 入口,绕开 Peek(1024) 阻塞)后,FirstByteMs 应反映上游响应头到达时刻(≈firstDelay),且显著小于
// DurationMs(请求结束时刻 = firstDelay + 尾帧间隔 gap)。修复前 Peek 阻塞把打点推迟到请求末尾,
// FirstByteMs 兜底≈DurationMs,本用例 FAIL,精确复现截图「响应时间==耗时」异常。
func TestHandleNvidiaStream_TTFTReflectsFirstFrame(t *testing.T) {
	// 上游首字延迟:响应头到达前空等时长。
	firstDelay := 400 * time.Millisecond
	// 首帧之后到尾帧的额外间隔:让 DurationMs 明显大于 TTFT,便于区分「打点生效」与「打点失效兜底」。
	gap := 300 * time.Millisecond
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		f := w.(http.Flusher)
		// 先空等 firstDelay 再吐首帧(模拟上游慢首字——响应头也在此刻才到达)。
		time.Sleep(firstDelay)
		// 首帧小(<1024 字节),复现 Peek(1024) 阻塞场景。
		_, _ = w.Write([]byte(`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}` + "\n\n"))
		f.Flush()
		// 首帧后gap 再吐尾帧,使 DurationMs 明显大于 TTFT。
		time.Sleep(gap)
		_, _ = w.Write([]byte(`data: {"id":"1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}` + "\n\n"))
		f.Flush()
		_, _ = w.Write([]byte(`data: [DONE]` + "\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-ttft", "ttft@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)
	beforeLogs := gt.GetRequestLogCount()

	anthReq := &AnthropicRequest{
		Model:    "z-ai/glm-5.2",
		Stream:   true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-ttft"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// 请求日志应新增一条
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Fatalf("request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}
	last := gt.GetRecentRequestFirstByteMs()
	if last < 0 {
		t.Fatalf("FirstByteMs = %d, 期望 ≥ 0", last)
	}
	// 决定性断言:TTFT 应反映上游响应头到达时刻(≈firstDelay),而非被推迟到请求末尾。
	// 端到端总耗时 ≈ firstDelay+gap=700ms;若 TTFT 打点失效兜底为端到端(700ms),会 ≥ 阈值 580ms,
	// 说明「响应时间==耗时」异常复现。修复后 TTFT≈400ms << 580ms,打点生效。
	// (注:改造后 DurationMs 为「第一帧→流结束」的流式耗时,本场景下 TTFT=400ms 可 > 流式耗时=300ms,
	// 属正常边界——首帧慢、生成快,故此处不直接把 TTFT 与 DurationMs 比较,而用 fixed 阈值判定打点失效。)
	if int64(last) >= int64(firstDelay.Milliseconds())+int64(gap.Milliseconds())*6/10 {
		t.Errorf("FirstByteMs = %dms, 期望 ≈ 上游首字延迟 %dms: TTFT 打点被 Peek(1024) 阻塞推迟,兜底成端到端耗时,前端「响应时间==耗时」异常复现", last, firstDelay.Milliseconds())
	}
}

// TestHandleNvidiaStream_CachedHitPropagatesEndToEnd 端到端实证 NVIDIA 号池流式 Anthropic 完整链路
// (handleNvidia → 选号 → 上游 SSE 末帧带 cached → 回译 → recordNvidiaUsage 落请求日志)的 cached 透传。
//
// 背景:旧实现 recordNvidiaUsage 硬编码 cached=0/CacheStatus="NONE",即使上游某天在末帧 usage
// 里回报 prompt_tokens_details.cached_tokens(或 prompt_cache_hit_tokens),代理也会把它压成 0,
// 体现在前端就是「缓存命中率 0.0% / 直通 (NONE)」永不变化。
//
// 本用例构造一个 mock 上游,其末帧 usage 带 prompt_tokens_details.cached_tokens=600,验证:
//   - 上游末帧 usage 的 cached 字段经 openAIChatSSEToAnthropicSSEInto 解析(nvidia_translate_sse.go:126);
//   - 经 pullAnthropicStreamWithRetry 的 cached 返回值透传(nvidia_stream.go);
//   - 经 writeNvidiaAnthropicStream 传给 recordNvidiaUsage(nvidia_stream.go:192);
//   - 落点5 请求日志 CacheStatus=="HIT"、落点4 综合桶 TotalCachedTokens +600。
//
// 当前 NVIDIA 官方 NIM 不回报 cache,本用例用 mock 上游模拟"未来/兼容上游回报 cache"场景,
// 保证整条 cached 透传链路真实闭环。真实 NIM 调用走 cached==0 分支(见 TestRecordNvidiaUsage_ZeroCachedStaysNONE)。
func TestHandleNvidiaStream_CachedHitPropagatesEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		f := w.(http.Flusher)
		// 首帧带正文 delta。
		_, _ = w.Write([]byte(`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}` + "\n\n"))
		f.Flush()
		// 末帧带 usage + cached_tokens(模拟上游回报缓存命中)。
		_, _ = w.Write([]byte(`data: {"id":"1","choices":[],"usage":{"prompt_tokens":800,"completion_tokens":2,"total_tokens":802,"prompt_tokens_details":{"cached_tokens":600}}}` + "\n\n"))
		f.Flush()
		_, _ = w.Write([]byte(`data: [DONE]` + "\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-cache", "cache@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)
	beforeCached := gt.GetTotalCachedTokens()
	beforeLogs := gt.GetRequestLogCount()

	anthReq := &AnthropicRequest{
		Model:    "z-ai/glm-5.2",
		Stream:   true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-cache"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// 落点5:请求日志 +1,CacheStatus=="HIT"。
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Fatalf("request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}
	if got := gt.GetRecentRequestCacheStatus(); got != "HIT" {
		t.Errorf("端到端 CacheStatus = %q, want \"HIT\" (上游末帧 cached_tokens=600 未透传到落点5)", got)
	}
	// 落点4:综合桶 TotalCachedTokens +600(缓存命中率分子真实写入)。
	if got := gt.GetTotalCachedTokens(); got != beforeCached+600 {
		t.Errorf("端到端 TotalCachedTokens = %d, want %d (delta +600, 上游末帧 cached 未透传到落点4)", got, beforeCached+600)
	}
}
