package relay

import (
	"testing"
	"time"

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
	logCtx := nvidiaLogCtx{
		Method:     "POST",
		Host:       "integrate.api.nvidia.com",
		Path:       "/nvidia/v1/chat/completions",
		SessionID:  "auth:acc:abc123def4567890",
		Account:    "u-1",
		StatusCode: 200,
		StartTs:    start,
	}
	handler.recordNvidiaUsage(userSession, "nvidia/z-ai/glm-5.2", 100, 50, nil, logCtx)

	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点4 not fired: TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点5 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
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
	handler.recordNvidiaUsage(userSession, "z-ai/glm-5.2", 100, 50, nil, logCtx)
}

// TestRecordNvidiaUsage_SkipsOnZeroUsage 验证 (input==0 && output==0) 时整函数早退,
// 既不触发落点4也不触发落点5 (与既有保护同口径, 避免制造空桶/噪声日志)。
func TestRecordNvidiaUsage_SkipsOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordNvidiaUsage(&RelaySession{UserID: "u-3"}, "z-ai/glm-5.2", 0, 0, nil, nvidiaLogCtx{StartTs: time.Now()})

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("zero-usage should not fire 落点4: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("zero-usage should not fire 落点5: log count = %d, want %d", got, beforeLogs)
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
