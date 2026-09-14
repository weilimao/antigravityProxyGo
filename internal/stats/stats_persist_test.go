package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
)

// stats_persist_test.go: 锁定「请求日志持久化迁移到 SQLite」后的三个不变式:
//  1) SaveToDisk 落盘文件不再携带 requests(12MB+ 报文膨胀根因切除);
//  2) 老 stats.json 的存量 requests 经 LoadFromDisk 一次性迁入 request_logs(按 req_id 幂等),
//     且内存环改由 DB 回填;
//  3) totals/trends 等既有统计口径在迁移 round-trip 后零回归。

// TestSaveToDisk_OmitsRequests 锁定: 内存环有日志时, 落盘文件不含 requests 键,
// 统计与趋势字段完整保持。
func TestSaveToDisk_OmitsRequests(t *testing.T) {
	dir := t.TempDir()
	// 纯文件路径场景确保 DB 不影响本用例: 显式置空(防同包其他用例残留 GlobalDB)。
	db.GlobalDB = nil

	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	tr.TrackRequest("test-model", 100, 10, 5)
	tr.requests = []*RequestLog{{
		ID:           "req-1",
		Timestamp:    "08/29 13:00:01",
		Model:        "test-model",
		InTokens:     100,
		OutTokens:    10,
		CachedTokens: 5,
		RequestBody:  map[string]interface{}{"hello": "world"},
	}}

	tr.SaveToDisk()

	raw, err := os.ReadFile(filepath.Join(dir, "stats.json"))
	if err != nil {
		t.Fatalf("read stats.json: %v", err)
	}
	// 注意: 不能用 `"requests"` 裸匹配——HourlyTrend 桶里也有 requests 字段。
	// 顶层请求日志数组的判空口径: 报文字段名 requestBody 不得出现 + 解析后顶层 Requests 为 nil。
	if strings.Contains(string(raw), "requestBody") {
		t.Fatalf("落盘文件不应再携带请求报文内容: %.200s", string(raw))
	}

	// 统计口径回读校验
	var parsed StatsData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Requests != nil {
		t.Fatalf("落盘文件顶层 requests 应为空, 实际 %d 条", len(parsed.Requests))
	}
	if parsed.Stats.TotalRequests != 1 || parsed.Stats.TotalInputTokens != 100 {
		t.Fatalf("统计口径漂移: reqs=%d in=%d", parsed.Stats.TotalRequests, parsed.Stats.TotalInputTokens)
	}
	if len(parsed.Trends) != 1 {
		t.Fatalf("趋势桶丢失: %d", len(parsed.Trends))
	}
}

// TestLoadFromDisk_MigratesLegacyRequestsToDB 锁定: 老 stats.json 的 requests 一次性迁入 DB
// (报文随行), 内存环由 DB 回填(DB 为权威源), legacy 时间戳折算为合法 RFC3339。
func TestLoadFromDisk_MigratesLegacyRequestsToDB(t *testing.T) {
	dir := t.TempDir()

	// 预备 legacy stats.json: 1 条带报文请求日志(显示格式时间戳, 与老文件口径一致)
	legacy := StatsData{
		Stats: GlobalStats{
			TotalRequests:    7,
			TotalInputTokens: 700,
			Models:           map[string]*ModelStats{},
			Pools:            map[string]*PoolStats{},
		},
		Requests: []*RequestLog{{
			ID:             "legacy-req-1",
			Timestamp:      "08/29 13:00:01",
			Model:          "moonshotai/kimi-k3",
			InTokens:       1000,
			OutTokens:      20,
			CachedTokens:   900,
			CacheStatus:    "HIT",
			StatusCode:     200,
			Cost:           0.5,
			Account:        "acc@example.com",
			SessionID:      "s-1",
			DurationMs:     2400,
			FirstByteMs:    1500,
			Family:         "nvidia",
			RequestBody:    map[string]interface{}{"prompt": "你好"},
			RequestHeaders: map[string]interface{}{"Authorization": "Bearer ***"},
		}},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), raw, 0644); err != nil {
		t.Fatalf("write legacy stats.json: %v", err)
	}

	if err := db.InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		db.CloseDB()
		db.GlobalDB = nil
	}()

	tr := NewTracker(pricing.NewManager())
	tr.Init(dir) // 内部触发 LoadFromDisk → 迁移 + DB 回填

	// 1) DB 侧: 报文与全字段迁入
	rows := db.QueryRecentLocalRequests(10)
	if len(rows) != 1 {
		t.Fatalf("迁移后 DB 应有 1 条, 实际 %d", len(rows))
	}
	row := rows[0]
	if row.ReqID != "legacy-req-1" || row.ModelName != "moonshotai/kimi-k3" {
		t.Fatalf("迁移行主键/模型不符: %+v", row)
	}
	if !strings.Contains(row.RequestBody, "prompt") || !strings.Contains(row.RequestBody, "你好") {
		t.Fatalf("报文未随行迁移: %q", row.RequestBody)
	}
	if row.CacheStatus != "HIT" || row.FirstByteMs != 1500 || row.DurationMs != 2400 {
		t.Fatalf("标量迁移失真: cache=%q ttfb=%d dur=%d", row.CacheStatus, row.FirstByteMs, row.DurationMs)
	}
	if len(row.Timestamp) < 19 || !strings.Contains(row.Timestamp, "T") {
		t.Fatalf("迁移时间戳未折算为 RFC3339: %q", row.Timestamp)
	}

	// 2) 内存环: 由 DB 回填, 详情弹窗字段(interface{} 形态)可用
	if len(tr.requests) != 1 {
		t.Fatalf("内存环应回填 1 条, 实际 %d", len(tr.requests))
	}
	ring := tr.requests[0]
	if ring.ID != "legacy-req-1" || ring.Timestamp != "08/29 13:00:01" {
		t.Fatalf("回填行标识/时间戳不符: %+v", ring)
	}
	bodyMap, ok := ring.RequestBody.(map[string]interface{})
	if !ok || bodyMap["prompt"] != "你好" {
		t.Fatalf("回填报文应反序列化为对象供详情弹窗使用: %#v", ring.RequestBody)
	}
	if ring.CacheStatus != "HIT" || ring.Account != "acc@example.com" {
		t.Fatalf("回填标量失真: %+v", ring)
	}

	// 3) 迁移幂等: 再跑一遍 Init(LoadFromDisk) 不产生重复行
	tr2 := NewTracker(pricing.NewManager())
	tr2.Init(dir)
	var cnt int
	if err := db.GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE req_id = 'legacy-req-1'`).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("重复迁移产生重复行: %d", cnt)
	}

	// 4) 迁移后 SaveToDisk 不再回写 requests(判空口径同 TestSaveToDisk_OmitsRequests:
	// 不能裸匹配 "requests"——HourlyTrend 桶也有该字段)。
	tr.SaveToDisk()
	saved, err := os.ReadFile(filepath.Join(dir, "stats.json"))
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if strings.Contains(string(saved), "requestBody") {
		t.Fatalf("迁移后落盘仍携带请求日志报文")
	}
}

// TestLoadFromDisk_KeepsShortTrendHistory 锁定: 老 stats.json 仅有少量趋势桶(≤6)时,
// LoadFromDisk 不得再按旧门槛清空——「综合趋势空 / NVIDIA 有数据」事故的直接回归锁。
func TestLoadFromDisk_KeepsShortTrendHistory(t *testing.T) {
	dir := t.TempDir()
	db.GlobalDB = nil

	legacy := StatsData{
		Stats: GlobalStats{Models: map[string]*ModelStats{}, Pools: map[string]*PoolStats{}},
		Trends: []*HourlyTrend{
			{Time: "08/29 13:00", Requests: 2, Input: 100, Output: 10, Cached: 50, Cost: 0.1},
			{Time: "08/29 14:00", Requests: 3, Input: 200, Output: 20, Cached: 100, Cost: 0.2},
			{Time: "08/29 15:00", Requests: 1, Input: 300, Output: 30, Cached: 150, Cost: 0.3},
		},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	if len(tr.trends) != 3 {
		t.Fatalf("3 个存量趋势桶被清空(旧 ≤6 门槛回归), 实际 %d", len(tr.trends))
	}
	if tr.trends[0].Time != "08/29 13:00" || tr.trends[2].Requests != 1 {
		t.Fatalf("趋势桶内容错位: %+v", tr.trends[0])
	}
}

// 内存环兜底沿用文件内 requests, 行为与迁移前等价(兼容零回归)。
func TestLoadFromDisk_FallbackWithoutDB(t *testing.T) {
	dir := t.TempDir()
	db.GlobalDB = nil

	legacy := StatsData{
		Stats: GlobalStats{Models: map[string]*ModelStats{}, Pools: map[string]*PoolStats{}},
		Requests: []*RequestLog{{
			ID:        "file-req-1",
			Timestamp: "08/29 13:00:01",
			Model:     "m",
			RequestBody: map[string]interface{}{
				"short": "ok",
			},
		}},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), raw, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	if len(tr.requests) != 1 || tr.requests[0].ID != "file-req-1" {
		t.Fatalf("无 DB 时应兜底沿用文件 requests: %+v", tr.requests)
	}
}

// TestSaveToDisk_PreservesMigrationFlags 锁定: 所有一次性迁移与回填标志在 SaveToDisk
// 后落盘进 stats.json，并在后续 NewTracker + LoadFromDisk 重新加载后完整保持为 true，
// 杜绝手写列举遗漏标志位导致重启重复回填。
func TestSaveToDisk_PreservesMigrationFlags(t *testing.T) {
	dir := t.TempDir()
	db.GlobalDB = nil

	pricingMgr := pricing.NewManager()
	tr1 := NewTracker(pricingMgr)
	tr1.Init(dir)

	tr1.Lock()
	tr1.stats.RelayTrendsBackfillDone = true
	tr1.stats.BackfillForcedDone = true
	tr1.stats.NvidiaUsageBackfillDone = true
	tr1.stats.TabExcludedFromEligible = true
	tr1.stats.TotalRequests = 100
	tr1.stats.TotalInputTokens = 5000
	tr1.Unlock()

	tr1.SaveToDisk()

	// 重新构造 tracker 模拟应用重启
	tr2 := NewTracker(pricingMgr)
	tr2.Init(dir)

	tr2.RLock()
	defer tr2.RUnlock()

	if !tr2.stats.RelayTrendsBackfillDone {
		t.Fatalf("expected RelayTrendsBackfillDone to be true after reload from disk, got false")
	}
	if !tr2.stats.BackfillForcedDone {
		t.Fatalf("expected BackfillForcedDone to be true after reload from disk, got false")
	}
	if !tr2.stats.NvidiaUsageBackfillDone {
		t.Fatalf("expected NvidiaUsageBackfillDone to be true after reload from disk, got false")
	}
	if !tr2.stats.TabExcludedFromEligible {
		t.Fatalf("expected TabExcludedFromEligible to be true after reload from disk, got false")
	}
	if tr2.stats.TotalRequests != 100 || tr2.stats.TotalInputTokens != 5000 {
		t.Fatalf("expected TotalRequests=100, TotalInputTokens=5000, got reqs=%d, in=%d",
			tr2.stats.TotalRequests, tr2.stats.TotalInputTokens)
	}
}

