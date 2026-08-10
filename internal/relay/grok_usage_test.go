package relay

import (
	"encoding/json"
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

// grok_usage_test.go: 锁定 recordGrokUsage 五处落点的命中/降级语义,
// 与 nvidia_usage_test.go 对偶,仅替换族标识("grok")、池 key、grokLogCtx 结构与 grokCursor 选号器。
//
// 落点契约(recordGrokUsage):
//   - 落点1  relay StatsTracker.RecordUsage(model 带 "grok/" 前缀) + RecordAPIKeyUsageForFamily(FamilyGrok);
//   - 落点2  usageTracker.RecordUsage(去 "grok/" 前缀,CachedTokens 透传);
//   - 落点3  globalStatsTracker.TrackRequestForModel(顶部指标卡 + 模型表 + 综合趋势);
//   - 落点3b TrackRequestForPool("grok")(Pools["grok"] 子聚合,仅 poolAccount != nil 时);
//   - 落点4  AddRequestLogForFamily(family="grok",绕过 isRealModel 显式入库,CacheStatus cached>0→HIT else NONE)。
//
// 关键降级语义(回归保证):
//   - input==0 && output==0 → 整函数早退(不触发落点3/4);
//   - userSession==nil → 落点2 `return` 提前退出,跳过落点3/4(与 recordNvidiaUsage 同口径);
//   - globalStatsTracker==nil → 落点3/4 安全跳过,不影响落点1/2。

// newGrokTestHandler 构造一个注入 mock 账号池 + sessionRouter + usageTracker 的 handler,
// 供 recordGrokUsage / handleGrok 测试。与 newNvidiaTestHandler 同构,仅把池模式切到 Grok
// (SetGrokPoolMode(true) 同时置 activeChannel="grok")。statsTracker/全局 tracker
// 不在此装配(传 nil),由各用例按需 SetGlobalStatsTracker 注入(与 nvidia 测试同款)。
func newGrokTestHandler(t *testing.T, accounts []*account.Account) (*APICompatHandler, *account.Manager, *session.Router, *stats.UsageTracker) {
	t.Helper()
	accMgr := account.NewManager()
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}
	accMgr.SetGrokPoolMode(true)

	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	handler := NewAPICompatHandler(nil, accMgr, router, nil, ut, nil, nil)
	return handler, accMgr, router, ut
}

// mkGrokAccount 构造一个最小可用 Grok 账号,与 mkNvidiaAccount 同构,仅 Provider/ScopeType="grok"。
func mkGrokAccount(id, email, key, baseURL, model string) *account.Account {
	return &account.Account{
		ID:           id,
		Email:        email,
		Provider:     "grok",
		ScopeType:    "grok",
		AccessToken:  key,
		BaseURL:      baseURL,
		Enabled:      true,
		ModelSonnet:  model,
		DefaultModel: model,
		Cooldowns:    map[string]int64{},
	}
}

// grokLogCtxOf 是测试便利:用必填字段构造一个 grokLogCtx,其余零值。
func grokLogCtxOf(method, host, path, accountName string, start time.Time) grokLogCtx {
	return grokLogCtx{
		Method:    method,
		Host:      host,
		Path:      path,
		Account:   accountName,
		StatusCode: 200,
		StartTs:   start,
	}
}

// TestRecordGrokUsage_FiresLandings3And4_WhenTrackerInjected 验证: globalStatsTracker 注入时,
// recordGrokUsage 的
//   - 落点3 (TrackRequestForModel): 全局综合统计 TotalRequests +1;
//   - 落点4 (AddRequestLogForFamily): 内存请求日志 +1, family=grok 写入。
// 同时落点1(relay StatsTracker)因 h.statsTracker==nil 自动跳过;落点2(usageTracker)正常 record。
// userSession 非 nil 以穿过落点2 的 `userSession==nil → return` 早退。
func TestRecordGrokUsage_FiresLandings3And4_WhenTrackerInjected(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	userSession := &RelaySession{Token: "grok-tok-1", UserID: "u-grok-1", SessionKey: "auth:acc:grok123def4567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(5 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := grokLogCtx{
		Method:       "POST",
		Host:         "api.x.ai",
		Path:         "/grok/v1/chat/completions",
		SessionID:    "auth:acc:grok123def4567890",
		Account:      "u-grok-1",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordGrokUsage(userSession, "grok-4.3", 100, 50, 0, nil, logCtx)

	if got := gt.GetTotalRequests(); got != beforeReqs+1 {
		t.Errorf("落点3 not fired: TotalRequests = %d, want %d (delta +1)", got, beforeReqs+1)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点4 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}

	// TTFT 闭环:打点后落点4 请求日志 FirstByteMs 应 > 0。
	lastFirstByte := gt.GetRecentRequestFirstByteMs()
	if lastFirstByte <= 0 {
		t.Errorf("expected last request FirstByteMs > 0 after MarkFirstByte, got %d", lastFirstByte)
	}
}

// TestRecordGrokUsage_SkipsLandings3And4_WhenTrackerNil 验证 globalStatsTracker==nil 时,
// 落点3/4 安全跳过,不 panic(既有「降级跳过」语义在新增落点后仍成立)。
func TestRecordGrokUsage_SkipsLandings3And4_WhenTrackerNil(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	// 不调 SetGlobalStatsTracker → globalStatsTracker 保持 nil

	userSession := &RelaySession{Token: "grok-tok-2", UserID: "u-grok-2", SessionKey: "auth:acc:failgrok0123456"}
	logCtx := grokLogCtxOf("POST", "api.x.ai", "/grok/v1/chat/completions", "u-grok-2", time.Now())
	// 不应 panic
	handler.recordGrokUsage(userSession, "grok-4.3", 100, 50, 0, nil, logCtx)
}

// TestRecordGrokUsage_SkipsOnZeroUsage 验证 (input==0 && output==0) 整函数早退,
// 不触发落点3/4(与既有保护同口径,避免制造空桶/噪声日志)。
func TestRecordGrokUsage_SkipsOnZeroUsage(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	handler.recordGrokUsage(&RelaySession{UserID: "u-grok-3"}, "grok-4.3", 0, 0, 0, nil, grokLogCtxOf("POST", "api.x.ai", "/grok/v1/chat/completions", "u-grok-3", time.Now()))

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("zero-usage should not fire 落点3: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("zero-usage should not fire 落点4: log count = %d, want %d", got, beforeLogs)
	}
}

// TestRecordGrokUsage_CachedHitSetsHITStatus 验证 cached>0 透传链路:
//   - 落点4 请求日志 CacheStatus=="HIT"(而非旧硬coding "NONE"),CachedTokens=cached;
//   - 落点3 综合桶 TotalCachedTokens +cached(缓存命中率分子真实写入);
//   - 落点2 usageTracker Totals.CachedTokens == cached(poolAccount nil 时落 Totals 聚合)。
// xAI 上游可能支持 prompt caching,本用例用 cached>0 模拟该场景,保证cached透传链路真实闭环。
func TestRecordGrokUsage_CachedHitSetsHITStatus(t *testing.T) {
	handler, _, _, uTracker := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeCached := gt.GetTotalCachedTokens()
	beforeLogs := gt.GetRequestLogCount()

	userSession := &RelaySession{Token: "grok-tok-hit", UserID: "u-grok-hit", SessionKey: "auth:acc:grok123def4567890"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := grokLogCtx{
		Method:       "POST",
		Host:         "api.x.ai",
		Path:         "/grok/v1/messages",
		SessionID:    "auth:acc:grok123def4567890",
		Account:      "u-grok-hit",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	// cached=40000 模拟上游回报缓存命中。
	handler.recordGrokUsage(userSession, "grok-4.3", 53263, 108, 40000, nil, logCtx)

	// 落点4:请求日志 CacheStatus=="HIT"。
	if got := gt.GetRecentRequestCacheStatus(); got != "HIT" {
		t.Errorf("落点4 CacheStatus = %q, want \"HIT\" (cached>0 应映射 HIT)", got)
	}
	// 落点4:请求日志条数 +1。
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Errorf("落点4 not fired: request log count = %d, want %d (delta +1)", got, beforeLogs+1)
	}
	// 落点3:综合桶 TotalCachedTokens +40000。
	if got := gt.GetTotalCachedTokens(); got != beforeCached+40000 {
		t.Errorf("落点3 TotalCachedTokens = %d, want %d (delta +40000)", got, beforeCached+40000)
	}
	// 落点2:usageTracker Totals.CachedTokens 应为 40000(poolAccount nil → 落 Totals 聚合)。
	payload, ok := uTracker.GetPayload().(stats.UsageState)
	if !ok {
		t.Fatalf("落点2 GetPayload 类型断言失败, got %T", uTracker.GetPayload())
	}
	if got := payload.Totals.CachedTokens; got != 40000 {
		t.Errorf("落点2 usageTracker Totals.CachedTokens = %d, want 40000", got)
	}
}

// TestRecordGrokUsage_ZeroCachedStaysNONE 验证 cached==0 时 CacheStatus 仍为 "NONE"(旧行为无回归)。
func TestRecordGrokUsage_ZeroCachedStaysNONE(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "grok-tok-none", UserID: "u-grok-none", SessionKey: "auth:acc:0000000000000000"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := grokLogCtx{
		Method:       "POST",
		Host:         "api.x.ai",
		Path:         "/grok/v1/messages",
		SessionID:    "auth:acc:0000000000000000",
		Account:      "u-grok-none",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	handler.recordGrokUsage(userSession, "grok-4.3", 500, 10, 0, nil, logCtx)

	if got := gt.GetRecentRequestCacheStatus(); got != "NONE" {
		t.Errorf("cached==0 时 CacheStatus = %q, want \"NONE\" (旧行为无回归)", got)
	}
}

// TestRecordGrokUsage_SkipsLandings3And4_WhenUserSessionNil 锁定 recordGrokUsage 的落点2早退语义:
// 落点2 检查 `h.usageTracker == nil || userSession == nil → return`(与 recordNvidiaUsage 同口径),
// userSession==nil 时在落点2 提前 return,落点3/4 不触发。区别于落点1(跳过)与落点3/4(nil 判),
// 这里是把落点2 当作整函数中段早退闸门。
func TestRecordGrokUsage_SkipsLandings3And4_WhenUserSessionNil(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	beforeReqs := gt.GetTotalRequests()
	beforeLogs := gt.GetRequestLogCount()

	// userSession == nil → 落点2 提前 return,落点3/4 不触发。
	handler.recordGrokUsage(nil, "grok-4.3", 100, 50, 0, nil, grokLogCtxOf("POST", "api.x.ai", "/grok/v1/chat/completions", "acc", time.Now()))

	if got := gt.GetTotalRequests(); got != beforeReqs {
		t.Errorf("userSession==nil 不应触发落点3: TotalRequests = %d, want %d", got, beforeReqs)
	}
	if got := gt.GetRequestLogCount(); got != beforeLogs {
		t.Errorf("userSession==nil 不应触发落点4: log count = %d, want %d", got, beforeLogs)
	}
}

// TestRecordGrokUsage_BodyAndHeadersPersists 验证 Grok 号池直连链路入站请求体/请求头落库链路:
// recordGrokUsage 把 logCtx.ReqBody / logCtx.ReqHeaders 落到 stats.RequestLog.RequestBody /
// RequestHeaders, 使前端「请求参数详情」弹窗按需经 GetRequestDetails 拉取时能如实展示,
// 而非恒落入「无请求参数 / 无请求头数据」兜底。同时验证敏感头(Authorization)被脱敏为 "<redacted>"。
func TestRecordGrokUsage_BodyAndHeadersPersists(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "grok-tok-body", UserID: "u-grok-body", SessionKey: "auth:acc:bodygrok123456ab"}
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	rec.MarkFirstByte()
	logCtx := grokLogCtx{
		Method:       "POST",
		Host:         "api.x.ai",
		Path:         "/grok/v1/messages",
		SessionID:    "auth:acc:bodygrok123456ab",
		Account:      "u-grok-body",
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
		ReqBody:      parseInboundBodyForLog([]byte(`{"model":"grok-4.3","stream":true,"messages":[{"role":"user","content":"hi"}]}`)),
		ReqHeaders:   collectInboundHeadersForLog(http.Header{"Authorization": {"Bearer xai-secret"}, "Content-Type": {"application/json"}}),
	}
	handler.recordGrokUsage(userSession, "grok-4.3", 100, 50, 0, nil, logCtx)

	body := gt.GetRecentRequestBody()
	if body == nil {
		t.Fatalf("RequestBody = nil, want 入站结构化 body; 详情弹窗将恒落「无请求参数」兜底")
	}
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		t.Fatalf("RequestBody 类型 = %T, want map[string]interface{}", body)
	}
	if got := bodyMap["model"]; got != "grok-4.3" {
		t.Errorf("RequestBody.model = %v, want grok-4.3", got)
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
		t.Errorf("RequestHeaders[Authorization] = %v, want \"<redacted>\"(敏感凭证脱敏)", got)
	}
	if got := headersMap["Content-Type"]; got != "application/json" {
		t.Errorf("RequestHeaders[Content-Type] = %v, want application/json(非敏感头原样保留)", got)
	}
}

// TestRecordGrokUsage_EmptyBodyAndHeadersStayNil 验证入站请求体/请求头为空时落库为 nil:
// 不强行注入,前端 formatRequestBody / formatRequestHeaders 仍走「无请求参数 / 无请求头数据」兜底(无回归)。
func TestRecordGrokUsage_EmptyBodyAndHeadersStayNil(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "grok-tok-empty", UserID: "u-grok-empty", SessionKey: "auth:acc:emptygrok000000"}
	logCtx := grokLogCtx{
		Method:     "POST",
		Host:       "api.x.ai",
		Path:       "/grok/v1/chat/completions",
		StartTs:    time.Now(),
		ReqBody:    parseInboundBodyForLog(nil), // 空 body → nil
		ReqHeaders: collectInboundHeadersForLog(nil),
	}
	handler.recordGrokUsage(userSession, "grok-4.3", 100, 50, 0, nil, logCtx)

	if got := gt.GetRecentRequestBody(); got != nil {
		t.Errorf("空入站 body 期望 RequestBody=nil, 实际=%v", got)
	}
	if got := gt.GetRecentRequestHeaders(); got != nil {
		t.Errorf("空入站 header 期望 RequestHeaders=nil, 实际=%v", got)
	}
}

// TestRecordGrokUsage_PoolAccountFiresLanding3b 锁定落点3b(TrackRequestForPool):
// poolAccount 非 nil 时,recordGrokUsage 把同一笔写入 Pools["grok"] 子聚合。
// 用包含 Pools 聚合计数的 getter 断言该落点真实触发(而非仅落入全局标量)。
func TestRecordGrokUsage_PoolAccountFiresLanding3b(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)

	userSession := &RelaySession{Token: "grok-tok-pool", UserID: "u-grok-pool", SessionKey: "auth:acc:poolgrok123456ab"}
	acc := mkGrokAccount("grok-pool-acc", "pool@x.ai", "xai-key", "https://api.x.ai/v1", "grok-4.3")
	start := time.Now()
	rec := stats.NewFirstByteRecorder(start)
	time.Sleep(2 * time.Millisecond)
	rec.MarkFirstByte()
	logCtx := grokLogCtx{
		Method:       "POST",
		Host:         "api.x.ai",
		Path:         "/grok/v1/chat/completions",
		SessionID:    "auth:acc:poolgrok123456ab",
		Account:      acc.Email,
		StatusCode:   200,
		StartTs:      start,
		FirstByteRec: rec,
	}
	// 显式传 poolAccount → 触发落点3b TrackRequestForPool("grok")。
	handler.recordGrokUsage(userSession, "grok-4.3", 1000, 200, 0, acc, logCtx)

	// 落点3/4 仍触发(globalStatsTracker 已注入)。
	if got := gt.GetRequestLogCount(); got < 1 {
		t.Fatalf("落点4 应触发(请求日志 +1), got count=%d", got)
	}
	// 落点3b:Pool 子聚合 "grok" 应记录该笔(通过 GetPoolStatsCopy 读 Pools["grok"] 深拷贝断言)。
	pools := gt.GetPoolStatsCopy()
	poolStats, ok := pools["grok"]
	if !ok || poolStats == nil {
		t.Fatalf("落点3b not fired: Pools[\"grok\"] 不存在 (poolAccount 非 nil 应触发 TrackRequestForPool)")
	}
	// 本笔 input=1000 + output=200,落点3b 仅写 Pools 子聚合(不动全局标量)。
	if poolStats.InTokens < 1000 {
		t.Errorf("落点3b Pools[\"grok\"].InTokens = %d, want >= 1000(含本笔)", poolStats.InTokens)
	}
	if poolStats.OutTokens < 200 {
		t.Errorf("落点3b Pools[\"grok\"].OutTokens = %d, want >= 200(含本笔)", poolStats.OutTokens)
	}
	if poolStats.Requests < 1 {
		t.Errorf("落点3b Pools[\"grok\"].Requests = %d, want >= 1(含本笔)", poolStats.Requests)
	}
}

// TestPickGrokAccount_RoundRobinRotation 锁定 round-robin 模式下 pickGrokAccount 的游标轮询:
// 3 个账号连续选号应按 grokCursor 取模被均匀覆盖(每个至少 1 次),单调递增打破共振。
// 单账号场景退化为恒取唯一号。
func TestPickGrokAccount_RoundRobinRotation(t *testing.T) {
	handler, _, _, _ := newGrokTestHandler(t, nil)
	accs := []*account.Account{
		mkGrokAccount("g1", "a@x.ai", "k1", "https://api.x.ai/v1", "grok-4.3"),
		mkGrokAccount("g2", "b@x.ai", "k2", "https://api.x.ai/v1", "grok-4.3"),
		mkGrokAccount("g3", "c@x.ai", "k3", "https://api.x.ai/v1", "grok-4.3"),
	}
	// round-robin 模式 + 空 sessionKey(不依赖 sticky)。
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		picked := handler.pickGrokAccount("round-robin", "", accs)
		if picked == nil {
			t.Fatalf("iter %d: pickGrokAccount returned nil", i)
		}
		seen[picked.ID]++
	}
	if len(seen) != 3 {
		t.Fatalf("round-robin 9 次选号应覆盖全部 3 个账号, got seen=%v", seen)
	}
	for id, n := range seen {
		if n != 3 {
			t.Errorf("round-robin 账号 %s 被选 %d 次, want 3(均匀取模轮询)", id, n)
		}
	}
	// 单账号场景退化为恒取唯一号。
	single := []*account.Account{mkGrokAccount("gs", "s@x.ai", "ks", "https://api.x.ai/v1", "grok-4.3")}
	for i := 0; i < 5; i++ {
		if got := handler.pickGrokAccount("round-robin", "", single); got == nil || got.ID != "gs" {
			t.Errorf("单账号应恒取唯一号, iter %d got %v", i, got)
		}
	}
	// 空账号列表 → nil。
	if got := handler.pickGrokAccount("round-robin", "", nil); got != nil {
		t.Errorf("空账号列表应返回 nil, got %v", got)
	}
}

// TestGrokHostFromBaseURL 验证上游账号 BaseURL 到裸 host 的提取,与 nvidiaHostFromBaseURL 同构,
// 仅回退占位用 "grok"(而非 "nvidia")。覆盖含路径、空串、非法 URL 兜底分支。
func TestGrokHostFromBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.x.ai/v1", "api.x.ai"},
		{"https://api.x.ai", "api.x.ai"},
		{"http://localhost:8080/v1", "localhost:8080"},
		{"api.x.ai/v1", "api.x.ai"}, // 无协议 — 兜底分支
		{"", "grok"},                // 空串 — 回退 grok 占位(区别于 nvidia 的 "nvidia")
	}
	for _, c := range cases {
		got := grokHostFromBaseURL(c.in)
		if got != c.want {
			t.Errorf("grokHostFromBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// nvidiaHostFromBaseURL 的回退占位是 "nvidia",grok 包装层必须把它改写为 "grok"。
	if got := grokHostFromBaseURL(""); got != "grok" {
		t.Errorf("空 BaseURL 应回退 grok 占位(非 nvidia), got %q", got)
	}
}

// TestHandleGrok_NonStreamAnthropic 端到端实证 Grok 号池非流式 Anthropic 完整链路
// (handleGrok → pickGrokAccount 选号 → 上游 chat/completions → 回译 → recordGrokUsage 落请求日志)。
// 锁定:上游端点固定 /v1/chat/completions,Authorization Bearer 透传,落点4 请求日志 +1 且 family=grok。
func TestHandleGrok_NonStreamAnthropic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("unexpected upstream path: %s (Grok 固定 chat/completions)", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xai-key" {
			t.Errorf("missing/invalid auth header: %s", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req OpenAIChatRequest
		_ = json.Unmarshal(body, &req)
		if req.Model != "grok-4.3" {
			t.Errorf("model not mapped to grok id: %s", req.Model)
		}
		// Grok 绝不注入 chat_template_kwargs(与 NIM 链路物理隔离)。
		if req.ChatTemplateKwargs != nil {
			t.Errorf("ChatTemplateKwargs must be nil for grok upstream, got %v", req.ChatTemplateKwargs)
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-grok-1", Model: "grok-4.3",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "Hello from Grok"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkGrokAccount("grok-1", "grok-1@x.ai", "xai-key", upstream.URL, "grok-4.3")
	handler, _, _, _ := newGrokTestHandler(t, []*account.Account{acc})
	gt := makeInjectedGlobalTracker(t)
	handler.SetGlobalStatsTracker(gt)
	beforeLogs := gt.GetRequestLogCount()

	anthReq := &AnthropicRequest{
		Model:    "grok-4.3",
		Stream:   false,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/grok/v1/messages", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	handler.handleGrok(rr, req, &RelaySession{UserID: "u-grok-e2e"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	// 落点4 应 +1(family=grok 入库)。
	if got := gt.GetRequestLogCount(); got != beforeLogs+1 {
		t.Fatalf("落点4 request log count = %d, want %d (delta +1, family=grok)", got, beforeLogs+1)
	}
}
