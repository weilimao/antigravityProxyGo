package main

import (
	"testing"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/stats"
)

// TestRelayRecordUsage_AccruesToTrends 验证远程中继请求通过 relayRecordUsage 处理后，
// 能实时累加到 statsTracker.trends 综合趋势桶、全局标量以及模型统计表中。
func TestRelayRecordUsage_AccruesToTrends(t *testing.T) {
	tempDir := t.TempDir()

	// 初始化独立沙箱 SQLite DB
	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	statsTracker.Init(tempDir)

	relayStatsMgr := relay.NewStatsTracker(pricingMgr)

	app := &App{
		statsTracker:  statsTracker,
		relayStatsMgr: relayStatsMgr,
	}

	// 记录调用前 trends 桶和全局请求数
	payloadBefore := statsTracker.GetPayload(nil)
	trendsBefore := payloadBefore["trends"].([]*stats.HourlyTrend)
	initialReqs := 0
	for _, tr := range trendsBefore {
		initialReqs += tr.Requests
	}

	// 模拟一笔远程中继请求
	inTokens := 50000
	outTokens := 2000
	cachedTokens := 30000
	model := "gemini-3.8-flash-high"
	reqID := "relay-test-req-001"

	app.relayRecordUsage(
		"test-acc-id",
		"user-1",
		"key-1",
		model,
		inTokens,
		outTokens,
		cachedTokens,
		"POST",
		"daily-cloudcode-pa.googleapis.com",
		"/v1internal:streamGenerateContent",
		"session-001",
		120,
		50,
		200,
		reqID,
	)

	// 验证 1: trends 桶内数据增长
	payloadAfter := statsTracker.GetPayload(nil)
	trendsAfter := payloadAfter["trends"].([]*stats.HourlyTrend)
	afterReqs := 0
	afterInTokens := 0
	afterOutTokens := 0
	afterCachedTokens := 0
	for _, tr := range trendsAfter {
		afterReqs += tr.Requests
		afterInTokens += tr.Input
		afterOutTokens += tr.Output
		afterCachedTokens += tr.Cached
	}

	if afterReqs != initialReqs+1 {
		t.Fatalf("expected trends requests to increase by 1, before=%d, after=%d", initialReqs, afterReqs)
	}
	if afterInTokens < inTokens {
		t.Fatalf("expected trends inTokens >= %d, got %d", inTokens, afterInTokens)
	}
	if afterOutTokens < outTokens {
		t.Fatalf("expected trends outTokens >= %d, got %d", outTokens, afterOutTokens)
	}
	if afterCachedTokens < cachedTokens {
		t.Fatalf("expected trends cachedTokens >= %d, got %d", cachedTokens, afterCachedTokens)
	}

	// 验证 2: 全局标量增长
	globalStats := payloadAfter["stats"].(stats.GlobalStats)
	if globalStats.TotalRequests < 1 {
		t.Fatalf("expected TotalRequests >= 1, got %v", globalStats.TotalRequests)
	}
	if globalStats.TotalInputTokens < inTokens {
		t.Fatalf("expected TotalInputTokens >= %d, got %d", inTokens, globalStats.TotalInputTokens)
	}

	// 验证 3: SQLite request_logs 表中写入 mode='remote_relay'
	var dbMode string
	var dbInTokens int
	row := db.GlobalDB.QueryRow("SELECT mode, in_tokens FROM request_logs WHERE req_id = ?", reqID)
	if err := row.Scan(&dbMode, &dbInTokens); err != nil {
		t.Fatalf("query request_logs failed: %v", err)
	}
	if dbMode != "remote_relay" {
		t.Fatalf("expected mode 'remote_relay', got %q", dbMode)
	}
	if dbInTokens != inTokens {
		t.Fatalf("expected in_tokens %d, got %d", inTokens, dbInTokens)
	}
}

// TestBackfillRelayTrendsFromDB_Idempotent 验证历史中继请求自愈回填逻辑的正确性与幂等性。
func TestBackfillRelayTrendsFromDB_Idempotent(t *testing.T) {
	tempDir := t.TempDir()

	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	// 预埋 2 条测试用的 remote_relay 记录到 request_logs
	now := time.Now()
	nowISO := now.Format(time.RFC3339)
	prevHourISO := now.Add(-1 * time.Hour).Format(time.RFC3339)

	log1 := &db.RequestLog{
		ReqID:        "backfill-1",
		Timestamp:    nowISO,
		Mode:         "remote_relay",
		UserID:       "u1",
		ModelName:    "gemini-3.8-flash-high",
		InTokens:     100000,
		OutTokens:    5000,
		CachedTokens: 80000,
		Cost:         1.25,
		StatusCode:   200,
	}
	log2 := &db.RequestLog{
		ReqID:        "backfill-2",
		Timestamp:    prevHourISO,
		Mode:         "remote_relay",
		UserID:       "u1",
		ModelName:    "gemini-3.8-flash-high",
		InTokens:     200000,
		OutTokens:    10000,
		CachedTokens: 150000,
		Cost:         2.50,
		StatusCode:   200,
	}

	if err := db.InsertRequestLog(log1); err != nil {
		t.Fatalf("InsertRequestLog 1 failed: %v", err)
	}
	if err := db.InsertRequestLog(log2); err != nil {
		t.Fatalf("InsertRequestLog 2 failed: %v", err)
	}

	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	statsTracker.Init(tempDir)

	// 首次回填
	statsTracker.Lock()
	statsTracker.BackfillRelayTrendsFromDBLocked()
	statsTracker.Unlock()

	// 验证首次回填结果
	payloadAfter1 := statsTracker.GetPayload(nil)
	trendsAfter1 := payloadAfter1["trends"].([]*stats.HourlyTrend)
	totalReqs1 := 0
	totalTokens1 := 0
	for _, tr := range trendsAfter1 {
		totalReqs1 += tr.Requests
		totalTokens1 += tr.Input + tr.Output
	}

	if totalReqs1 != 2 {
		t.Fatalf("expected 2 backfilled requests, got %d", totalReqs1)
	}
	expectedTokens := 100000 + 5000 + 200000 + 10000
	if totalTokens1 != expectedTokens {
		t.Fatalf("expected %d tokens, got %d", expectedTokens, totalTokens1)
	}

	// 内存中二次回填调用 (实例级幂等性测试: 内存标志守卫，不应重复累加)
	statsTracker.Lock()
	statsTracker.BackfillRelayTrendsFromDBLocked()
	statsTracker.Unlock()

	payloadAfter2 := statsTracker.GetPayload(nil)
	trendsAfter2 := payloadAfter2["trends"].([]*stats.HourlyTrend)
	totalReqs2 := 0
	totalTokens2 := 0
	for _, tr := range trendsAfter2 {
		totalReqs2 += tr.Requests
		totalTokens2 += tr.Input + tr.Output
	}

	if totalReqs2 != 2 {
		t.Fatalf("idempotency check failed: expected requests to remain 2, got %d", totalReqs2)
	}
	if totalTokens2 != expectedTokens {
		t.Fatalf("idempotency check failed: expected tokens to remain %d, got %d", expectedTokens, totalTokens2)
	}

	// 3. 落盘与跨进程重启测试 (跨进程重启幂等性: 验证 SaveToDisk 正确持久化 RelayTrendsBackfillDone，重启后不重复累加)
	statsTracker.SaveToDisk()

	statsTrackerRestarted := stats.NewTracker(pricingMgr)
	statsTrackerRestarted.Init(tempDir)

	payloadAfterRestart := statsTrackerRestarted.GetPayload(nil)
	trendsAfterRestart := payloadAfterRestart["trends"].([]*stats.HourlyTrend)
	totalReqsRestart := 0
	totalTokensRestart := 0
	for _, tr := range trendsAfterRestart {
		totalReqsRestart += tr.Requests
		totalTokensRestart += tr.Input + tr.Output
	}

	if totalReqsRestart != 2 {
		t.Fatalf("restart idempotency check failed: expected requests to remain 2, got %d", totalReqsRestart)
	}
	if totalTokensRestart != expectedTokens {
		t.Fatalf("restart idempotency check failed: expected tokens to remain %d, got %d", expectedTokens, totalTokensRestart)
	}
}

// TestRelayRecordUsage_DedupWhenAlreadyTracked 验证当请求已经由本地核心代理处理并计入 statsTracker 时，
// relayRecordUsage 不会发生二次统计与成对重复行追加，彻底防范双重记账。
func TestRelayRecordUsage_DedupWhenAlreadyTracked(t *testing.T) {
	tempDir := t.TempDir()

	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.CloseDB()

	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	statsTracker.Init(tempDir)

	relayStatsMgr := relay.NewStatsTracker(pricingMgr)

	app := &App{
		statsTracker:  statsTracker,
		relayStatsMgr: relayStatsMgr,
	}

	reqID := "relay-req-already-tracked-001"
	model := "gemini-3.8-flash-high"
	inTokens := 60000
	outTokens := 300
	cachedTokens := 50000

	// 1. 模拟核心代理层 (classifyResponse / helpers) 已经先处理并记录了该请求
	statsTracker.TrackRequest(model, inTokens, outTokens, cachedTokens)
	statsTracker.AddRequestLog(&stats.RequestLog{
		ID:           reqID,
		Model:        model,
		Path:         "/v1internal:streamGenerateContent",
		InTokens:     inTokens,
		OutTokens:    outTokens,
		CachedTokens: cachedTokens,
		CacheStatus:  "HIT",
		Account:      "core-acc@gmail.com",
		SessionID:    "auth:acc:initial",
		DurationMs:   40,
		FirstByteMs:  2000,
	})

	// 记录调用 relayRecordUsage 前的请求数与日志条数
	payloadBefore := statsTracker.GetPayload(nil)
	reqsBefore := payloadBefore["stats"].(stats.GlobalStats).TotalRequests
	trendsBefore := payloadBefore["trends"].([]*stats.HourlyTrend)
	trendsReqsBefore := 0
	for _, tr := range trendsBefore {
		trendsReqsBefore += tr.Requests
	}
	rawLogsBefore := payloadBefore["requests"].([]stats.RequestLogLite)
	if len(rawLogsBefore) != 1 {
		t.Fatalf("expected initial 1 log, got %d", len(rawLogsBefore))
	}

	// 2. 模拟请求完成后触发 relayRecordUsage
	app.relayRecordUsage(
		"core-acc@gmail.com",
		"user-1",
		"key-1",
		model,
		inTokens,
		outTokens,
		cachedTokens,
		"POST",
		"daily-cloudcode-pa.googleapis.com",
		"/v1internal:streamGenerateContent",
		"auth:acc:initial",
		40,
		2000,
		200,
		reqID,
	)

	// 3. 验证: 全局请求数、trends 桶请求数、内存日志条数均没有翻倍
	payloadAfter := statsTracker.GetPayload(nil)
	reqsAfter := payloadAfter["stats"].(stats.GlobalStats).TotalRequests
	if reqsAfter != reqsBefore {
		t.Fatalf("expected TotalRequests to remain %d, got %d (duplicate tracking occurred!)", reqsBefore, reqsAfter)
	}

	trendsAfter := payloadAfter["trends"].([]*stats.HourlyTrend)
	trendsReqsAfter := 0
	for _, tr := range trendsAfter {
		trendsReqsAfter += tr.Requests
	}
	if trendsReqsAfter != trendsReqsBefore {
		t.Fatalf("expected trends requests to remain %d, got %d (duplicate trends occurred!)", trendsReqsBefore, trendsReqsAfter)
	}

	rawLogsAfter := payloadAfter["requests"].([]stats.RequestLogLite)
	if len(rawLogsAfter) != 1 {
		t.Fatalf("expected request logs count to remain 1, got %d (duplicate row added!)", len(rawLogsAfter))
	}

	// 4. 验证: SQLite 中依然能完整记入 mode='remote_relay' 的中继审计记录
	var dbMode string
	row := db.GlobalDB.QueryRow("SELECT mode FROM request_logs WHERE req_id = ? AND mode = 'remote_relay'", reqID)
	if err := row.Scan(&dbMode); err != nil {
		t.Fatalf("query remote_relay audit log failed: %v", err)
	}
	if dbMode != "remote_relay" {
		t.Fatalf("expected mode 'remote_relay', got %q", dbMode)
	}
}

