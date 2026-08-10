package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

// google_usage_test.go: 覆盖 handleV1Internal 直连出站成功路径的 recordGoogleUsage 五落点。
//
// 断言重点:
//   1. globalStatsTracker 注入时, 落点3(TrackRequestForModel)/落点4(TrackRequestForPool →
//      Pools["antigravity"])/落点5(AddRequestLogForFamily, family=antigravity)全部触发;
//   2. cached>0 → CacheStatus="HIT" + Pools["antigravity"].CachedTokens 累加(缓存命中率分子);
//   3. globalStatsTracker==nil 时安全降级(不 panic), 与 recordNvidiaUsage/recordOtherUsage 同语义;
//   4. 零 usage 早退(input==0 && output==0 不产生任何落点);
//   5. googleTailWriter 环形缓冲: 超大响应只保留尾部、Bytes() 还原时序、usage 在尾部仍能解析;
//   6. 端到端 handleV1Internal 直连出站成功(Provider=antigravity)→ recordGoogleUsage 全部落点闭环。

// mkGoogleAccount 构造一个 Provider=antigravity 的号池账号(直连出站目标 daily-cloudcode-pa)。
func mkGoogleAccount(id, email string) *account.Account {
	return &account.Account{
		ID:          id,
		Email:       email,
		Provider:    "antigravity",
		ScopeType:   "cloudcode",
		AccessToken: "gaia-token",
		Enabled:     true,
		Cooldowns:   map[string]int64{},
	}
}

// TestGoogleTailWriter_KeepsTail 验证 googleTailWriter 环形缓冲语义:
// 超过容量时丢弃最旧字节、保留最近 N 字节、Bytes() 返回真实写入时序(非环形地址序)。
func TestGoogleTailWriter_KeepsTail(t *testing.T) {
	tw := newGoogleTailWriter(8)
	tw.Write([]byte("abcdefgh")) // 恰好填满
	tw.Write([]byte("ij"))      // 溢出丢弃 "ab", 保留 cdefghij
	if got := string(tw.Bytes()); got != "cdefghij" {
		t.Errorf("Bytes() = %q, want %q", got, "cdefghij")
	}

	// 超大响应只保留尾部: 头部 padding 被丢弃, 尾部 usage 仍能解析(流式末帧语义)。
	tail := []byte(`"promptTokenCount":222,"candidatesTokenCount":4`)
	tw3 := newGoogleTailWriter(300)
	tw3.Write(bytes.Repeat([]byte("x"), 100)) // 100×'x' padding
	tw3.Write(tail)
	if got := tw3.Bytes(); len(got) != 100+len(tail) {
		t.Errorf("Bytes len = %d, want %d", len(got), 100+len(tail))
	}
	if in, out, cached := googleUsageFromRaw(tw3.Bytes()); in != 222 || out != 4 || cached != 0 {
		t.Errorf("tail parse = (%d,%d,%d), want (222,4,0)", in, out, cached)
	}
}

// TestGoogleTailWriter_TruncatesHugeResponse 验证超过容量上限时只保留最近 N 字节
// (恒占内存 ≤ maxLen, 超大非流式 JSON 也不 OOM), 且尾部 usage 仍在保留窗口内可解析。
func TestGoogleTailWriter_TruncatesHugeResponse(t *testing.T) {
	tail := []byte(`"promptTokenCount":333,"candidatesTokenCount":9`)
	tw := newGoogleTailWriter(64)
	tw.Write(bytes.Repeat([]byte("y"), 1000)) // 远超容量, 只保留最近 64 字节
	tw.Write(tail)
	got := tw.Bytes()
	if len(got) != 64 {
		t.Errorf("truncated len = %d, want 64 (maxLen)", len(got))
	}
	// 尾部 usage 应在保留窗口的最末段, 仍能被正则拾取。
	if in, out, cached := googleUsageFromRaw(got); in != 333 || out != 9 || cached != 0 {
		t.Errorf("trunced tail parse = (%d,%d,%d), want (333,9,0)", in, out, cached)
	}
}

// TestGoogleUsageFromRaw_LastFrameWins 验证流式末帧权威语义:
// 多个 usageMetadata 时取最后一个匹配(帧间递增, 末帧为累计权威值)。
func TestGoogleUsageFromRaw_LastFrameWins(t *testing.T) {
	raw := []byte(`data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":1,"cachedContentTokenCount":50}}
data: {"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":3,"cachedContentTokenCount":80}}
data: {"usageMetadata":{"promptTokenCount":140,"candidatesTokenCount":5,"cachedContentTokenCount":96}}`)
	in, out, cached := googleUsageFromRaw(raw)
	if in != 140 || out != 5 || cached != 96 {
		t.Errorf("last-frame parse = (%d,%d,%d), want (140,5,96)", in, out, cached)
	}
}

// TestRecordGoogleUsage_FiresAllLandings_WhenTrackerInjected 验证五落点对账完整触发:
//   - 落点3: TotalRequests +1, TotalCachedTokens +40000;
//   - 落点4: Pools["antigravity"] 的 reqs/in/out/cached 真实累加(PoolKeyForProvider 归 antigravity);
//   - 落点5: 请求日志 +1, family=antigravity, CacheStatus=HIT。
func TestRecordGoogleUsage_FiresAllLandings_WhenTrackerInjected(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeCached := gt.GetTotalCachedTokens()
	beforeLogs := gt.GetRequestLogCount()

	userSession := &RelaySession{Token: "tok-ag-1", UserID: "u-ag-1", SessionKey: "auth:acc:google1"}
	acc := mkGoogleAccount("ag-1", "ag-1@example.com")
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(5 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := googleLogCtx{
		Method:       http.MethodPost,
		Host:         "daily-cloudcode-pa.googleapis.com",
		Path:         "/v1internal:streamGenerateContent",
		SessionID:    "auth:acc:google1",
		Account:      "ag-1",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordGoogleUsage(userSession, "gemini-3-flash-agent", 100, 30, 40000, acc, logCtx)

	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点3 not fired: TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	if got := gt.GetTotalCachedTokens(); got != beforeCached+40000 {
		t.Errorf("落点3 cached = %d, want %d (delta +40000)", got, beforeCached+40000)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点5 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}

	pools := gt.GetPoolStatsCopy()
	ag := pools["antigravity"]
	if ag == nil {
		t.Fatal("Pools[\"antigravity\"] should exist after recordGoogleUsage")
	}
	if ag.Requests != 1 || ag.InTokens != 100 || ag.OutTokens != 30 || ag.CachedTokens != 40000 {
		t.Errorf("落点4 antigravity pool bucket = %+v, want reqs=1 in=100 out=30 cached=40000", ag)
	}
	if ag.CacheEligibleInputTokens == 0 {
		t.Errorf("落点4 cacheEligibleInputTokens should be > 0, got 0(命中率分母缺累计)")
	}
	if got := gt.GetRecentRequestCacheStatus(); got != "HIT" {
		t.Errorf("落点5 CacheStatus = %q, want \"HIT\" (cached=40000)", got)
	}
	if last := gt.GetRecentRequestFirstByteMs(); last <= 0 {
		t.Errorf("落点5 FirstByteMs = %d, want > 0 (TTFT 链路未闭环)", last)
	}
}

// TestRecordGoogleUsage_SkipsAll_WhenTrackerNil 验证 globalStatsTracker==nil 时安全降级不 panic。
func TestRecordGoogleUsage_SkipsAll_WhenTrackerNil(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)

	userSession := &RelaySession{Token: "tok-ag-2", UserID: "u-ag-2", SessionKey: "auth:acc:google2"}
	acc := mkGoogleAccount("ag-2", "ag-2@example.com")
	logCtx := googleLogCtx{
		Method:     http.MethodPost,
		Host:       "daily-cloudcode-pa.googleapis.com",
		Path:       "/v1internal:generateContent",
		SessionID:  "auth:acc:google2",
		Account:    "ag-2",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	// 不应 panic (recordGoogleUsage 各落点 nil-safe 降级)
	handler.recordGoogleUsage(userSession, "gemini-3-flash-agent", 100, 30, 0, acc, logCtx)
}

// TestRecordGoogleUsage_SkipsOnZeroUsage 验证 input==0 && output==0 整函数早退。
func TestRecordGoogleUsage_SkipsOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordGoogleUsage(&RelaySession{UserID: "u-ag-3"}, "gemini-3-flash-agent", 0, 0, 0, nil, googleLogCtx{StartTs: time.Now()})

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("zero-usage should not fire 落点3: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("zero-usage should not fire 落点5: log count = %d, want %d", got, beforeLogs)
	}
	if pools := gt.GetPoolStatsCopy(); pools["antigravity"] != nil {
		t.Errorf("zero-usage should not create pool bucket, got %+v", pools["antigravity"])
	}
}

// TestRecordGoogleUsage_CachedZeroStaysNone 验证 cached==0 时 CacheStatus="NONE" 无回归。
func TestRecordGoogleUsage_CachedZeroStaysNone(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "tok-ag-none", UserID: "u-ag-none", SessionKey: "auth:acc:googlenone"}
	acc := mkGoogleAccount("ag-none", "ag-none@example.com")
	logCtx := googleLogCtx{
		Method:     http.MethodPost,
		Host:       "daily-cloudcode-pa.googleapis.com",
		Path:       "/v1internal:generateContent",
		SessionID:  "auth:acc:googlenone",
		Account:    "ag-none",
		StatusCode: 200,
		StartTs:    time.Now(),
	}
	handler.recordGoogleUsage(userSession, "gemini-3-flash-agent", 500, 10, 0, acc, logCtx)

	if got := gt.GetRecentRequestCacheStatus(); got != "NONE" {
		t.Errorf("cached==0 期望 CacheStatus=NONE, 实际=%q", got)
	}
}

// TestHandleV1Internal_DirectSuccess_RecordsUsage 端到端实证 handleV1Internal 直连出站
// (Provider=antigravity) 成功 200 后, recordGoogleUsage 五落点全量闭环: Pools["antigravity"]
// 桶入桶、全局指标卡、请求日志 family=antigravity、CacheStatus 按 usage 推导。
//
// 真实出站目标是写死的 daily-cloudcode-pa.googleapis.com(compat_v1internal.go:195),
// 单测无法注入网络地址, 故用「劫持直连出站前账」的双层接缝:
//  1. 把 finalRequester 临时替换为「返回伪造 200 响应的假上游」闭包(不入网);
//  2. finalRequester 返回的 body 流: 带 usageMetadata 的末尾 JSON → 经 googleTailWriter
//     尾部捕获 → googleUsageFromRaw 解析 → recordGoogleUsage 五落点。
//
// finalRequester 在记录完用量后仍由本函数驱动走完拷贝循环, piece 写入 recorder。
// 断言 Pools["antigravity"] 桶 + 请求日志 family + CacheStatus, 覆盖真实直连出站记账入口闭环。
func TestHandleV1Internal_DirectSuccess_RecordsUsage(t *testing.T) {
	accMgr := account.NewManager()
	accMgr.AddAccount(mkGoogleAccount("ag-1", "ag-1@example.com"))
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)
	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	handler := NewAPICompatHandler(nil, accMgr, router, nil, ut, nil, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	origRequester := handler.finalRequester
	handler.finalRequester = func(u *account.Account, method, url string, body []byte) (*http.Response, error) {
		if u.Provider != "antigravity" {
			t.Fatalf("expected antigravity pool account, got %s", u.Provider)
		}
		if !strings.Contains(url, "daily-cloudcode-pa.googleapis.com") {
			t.Errorf("unexpected target url: %s", url)
		}
		// 合法 200: 末尾带 usageMetadata(cached 命中), 无任何报错/权限头。
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":222,"candidatesTokenCount":4,"cachedContentTokenCount":150}}`)),
		}, nil
	}
	defer func() { handler.finalRequester = origRequester }()

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	body := `{"request":{"model":"gemini-2.5-flash","contents":[{"role":"user","parts":[{"text":"hi"}]}]},"project":"fav-syn-001","requestId":"chat/1-1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1internal:generateContent", strings.NewReader(body))
	// 入站请求头(含鉴权凭证, 验证 recordGoogleUsage 经 collectInboundHeadersForLog 脱敏后落库)。
	req.Header.Set("Authorization", "Bearer gaia-secret-e2e")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.handleV1Internal(rr, req, &RelaySession{Token: "tok-e2e", UserID: "u-e2e"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// 账面: 全局 +1 请求。
	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点3 TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	// 落点4: antigravity 池桶入桶。
	pools := gt.GetPoolStatsCopy()
	ag := pools["antigravity"]
	if ag == nil {
		t.Fatal("Pools[\"antigravity\"] should exist after handleV1Internal direct success")
	}
	if ag.Requests != 1 || ag.InTokens != 222 || ag.OutTokens != 4 || ag.CachedTokens != 150 {
		t.Errorf("antigravity pool bucket = %+v, want reqs=1 in=222 out=4 cached=150", ag)
	}
	// 落点5: 请求日志 +1, family=antigravity, CacheStatus=HIT。
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点5 request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}
	if last := gt.GetRecentRequestCacheStatus(); last != "HIT" {
		t.Errorf("端到端 CacheStatus = %q, want \"HIT\" (cached=150)", last)
	}
	// 入站请求头/请求体端到端落库:handleV1Internal 装配 logCtx 时注入 ReqBody/ReqHeaders,
	// 经 recordGoogleUsage 落到 stats.RequestLog, 使前端「请求参数详情」弹窗能展示入站
	// 请求体/请求头而非「无请求参数 / 无请求头数据」兜底(截图现象)。鉴权头 Authorization
	// 需脱敏为 "<redacted>"(避免凭证写进 SQLite), 非敏感头 Content-Type 原样保留。
	if gotBody := gt.GetRecentRequestBody(); gotBody == nil {
		t.Errorf("端到端 RequestBody = nil, want 入站结构化 body; 详情弹窗恒落「无请求参数」(截图现象未修)")
	} else if bodyMap, ok := gotBody.(map[string]interface{}); !ok || bodyMap["project"] != "fav-syn-001" {
		t.Errorf("端到端 RequestBody.project 未透传, got %v", gotBody)
	}
	gotHeaders := gt.GetRecentRequestHeaders()
	if gotHeaders == nil {
		t.Fatalf("端到端 RequestHeaders = nil, want 非空映射; 详情弹窗恒落「无请求头数据」(截图现象未修)")
	}
	headersMap, ok := gotHeaders.(map[string]interface{})
	if !ok {
		t.Fatalf("端到端 RequestHeaders 类型 = %T, want map[string]interface{}", gotHeaders)
	}
	if got := headersMap["Authorization"]; got != "<redacted>" {
		t.Errorf("端到端 Authorization = %v, want \"<redacted>\"(鉴权凭证脱敏)", got)
	}
}