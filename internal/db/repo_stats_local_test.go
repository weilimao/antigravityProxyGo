package db

import (
	"fmt"
	"testing"
	"time"
)

// repo_stats_local_test.go: 锁定「本地请求日志携报文入库 + 启动回填 + 报文窗口修剪」三件套。
//
// 背景: stats.json 曾持久化 150 条完整报文(12MB+ 膨胀, 每 3s 全量重写),
// 现持久化职责收敛到 SQLite request_logs(request_body/request_headers/cache_status 三列),
// 内存环保留热路径, DB 作跨重启的权威副本。

// newLocalLog 构造一条本地模式测试日志; body/headers 为已截断 JSON 文本形态。
func newLocalLog(reqID string, body string) *RequestLog {
	return &RequestLog{
		ReqID:           reqID,
		Timestamp:       time.Now().Format(time.RFC3339),
		Mode:            "local",
		UserID:          "acc@example.com",
		ModelName:       "moonshotai/kimi-k3",
		InTokens:        100,
		OutTokens:       10,
		CachedTokens:    80,
		Cost:            0.001,
		DurationMs:      1234,
		FirstByteMs:     456,
		StatusCode:      200,
		Method:          "POST",
		Host:            "integrate.api.nvidia.com",
		Path:            "/route/v1/messages",
		SessionID:       "sess-1",
		Family:          "nvidia",
		ReasoningEffort: "max",
		RequestBody:     body,
		RequestHeaders:  `{"Authorization":"Bearer ***"}`,
		CacheStatus:     "HIT",
	}
}

// TestQueryRecentLocalRequests_RoundTripWithBody 锁定: 携报文插入后, 回填查询按 id DESC 返回
// 且 request_body/request_headers/cache_status/first_byte_ms 各列无损往返。
func TestQueryRecentLocalRequests_RoundTripWithBody(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	for i := 1; i <= 3; i++ {
		if err := InsertRequestLog(newLocalLog(fmt.Sprintf("req-%d", i), fmt.Sprintf(`{"seq":%d}`, i))); err != nil {
			t.Fatalf("InsertRequestLog(%d): %v", i, err)
		}
	}

	// 非 local 行不得混入回填结果
	remote := newLocalLog("req-remote", `{"seq":99}`)
	remote.Mode = "remote"
	if err := InsertRequestLog(remote); err != nil {
		t.Fatalf("InsertRequestLog(remote): %v", err)
	}

	rows := QueryRecentLocalRequests(2)
	if len(rows) != 2 {
		t.Fatalf("期望回填 2 条, 实际 %d", len(rows))
	}
	// DESC: 最新(req-3)在前
	if rows[0].ReqID != "req-3" || rows[1].ReqID != "req-2" {
		t.Fatalf("回填序不符 DESC 口径: got %s, %s", rows[0].ReqID, rows[1].ReqID)
	}
	if rows[0].RequestBody != `{"seq":3}` {
		t.Fatalf("request_body 往返失真: %q", rows[0].RequestBody)
	}
	if rows[0].RequestHeaders == "" || rows[0].CacheStatus != "HIT" {
		t.Fatalf("headers/cache_status 往返失真: headers=%q cache=%q", rows[0].RequestHeaders, rows[0].CacheStatus)
	}
	if rows[0].FirstByteMs != 456 {
		t.Fatalf("first_byte_ms 往返失真: %d", rows[0].FirstByteMs)
	}
}

// TestPruneLocalRequestBodies 锁定: 仅最新 keep 条本地行保留报文, 更老行报文/头置空,
// 且标量列与 remote 行(即便带报文)不被触碰。
func TestPruneLocalRequestBodies(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	for i := 1; i <= 5; i++ {
		if err := InsertRequestLog(newLocalLog(fmt.Sprintf("req-%d", i), fmt.Sprintf(`{"seq":%d}`, i))); err != nil {
			t.Fatalf("InsertRequestLog(%d): %v", i, err)
		}
	}
	// remote 行带报文, 验证修剪不误伤远端模式数据
	remote := newLocalLog("req-remote", `{"remote":true}`)
	remote.Mode = "remote"
	if err := InsertRequestLog(remote); err != nil {
		t.Fatalf("InsertRequestLog(remote): %v", err)
	}

	if err := PruneLocalRequestBodies(2); err != nil {
		t.Fatalf("PruneLocalRequestBodies: %v", err)
	}

	// 最新 2 条(req-4, req-5)报文保留
	rows := QueryRecentLocalRequests(5)
	if len(rows) != 5 {
		t.Fatalf("回填条数不符: %d", len(rows))
	}
	kept := map[string]string{}
	for _, r := range rows {
		kept[r.ReqID] = r.RequestBody
	}
	for _, id := range []string{"req-4", "req-5"} {
		if kept[id] == "" {
			t.Fatalf("最新窗口 %s 的报文被误清", id)
		}
	}
	for _, id := range []string{"req-1", "req-2", "req-3"} {
		if kept[id] != "" {
			t.Fatalf("超窗 %s 的报文未被置空: %q", id, kept[id])
		}
	}
	// 标量仍可读(修剪不清行)
	if kept["req-1"] == "" && rows[len(rows)-1].InTokens != 100 {
		t.Fatalf("修剪伤到了标量列")
	}

	// remote 行报文未被修剪(查询 remote 侧)
	var remoteBody string
	if err := GlobalDB.QueryRow(`SELECT request_body FROM request_logs WHERE req_id = 'req-remote'`).Scan(&remoteBody); err != nil {
		t.Fatalf("查询 remote 行: %v", err)
	}
	if remoteBody != `{"remote":true}` {
		t.Fatalf("remote 行报文被误修剪: %q", remoteBody)
	}

	// 幂等: 再跑一次不报错
	if err := PruneLocalRequestBodies(2); err != nil {
		t.Fatalf("二次修剪应幂等: %v", err)
	}
}

// TestUpsertLocalRequestLog 锁定迁移语义: req_id 已存在 → 仅补报文/标记不插新行;
// 不存在 → 整行插入。重复执行幂等。
func TestUpsertLocalRequestLog(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	// 预置一条旧双写时代的纯标量行(无报文), 模拟存量 DB
	if err := InsertRequestLog(newLocalLog("dup-1", "")); err != nil {
		t.Fatalf("预埋 InsertRequestLog: %v", err)
	}

	// 迁移: 同 req_id 带报文 → 只更新, 不新增
	inserted, err := UpsertLocalRequestLog(newLocalLog("dup-1", `{"migrated":true}`))
	if err != nil {
		t.Fatalf("UpsertLocalRequestLog(existing): %v", err)
	}
	if inserted {
		t.Fatal("req_id 已存在时不应整行插入")
	}
	var cnt int
	if err := GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE req_id = 'dup-1'`).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("迁移造成重复行: %d", cnt)
	}
	var body string
	if err := GlobalDB.QueryRow(`SELECT request_body FROM request_logs WHERE req_id = 'dup-1'`).Scan(&body); err != nil {
		t.Fatalf("scan body: %v", err)
	}
	if body != `{"migrated":true}` {
		t.Fatalf("既有行报文未被回填: %q", body)
	}

	// 不存在的 req_id → 整行插入
	inserted, err = UpsertLocalRequestLog(newLocalLog("fresh-1", `{"new":1}`))
	if err != nil {
		t.Fatalf("UpsertLocalRequestLog(missing): %v", err)
	}
	if !inserted {
		t.Fatal("req_id 缺失时应整行插入")
	}
	if rows := QueryRecentLocalRequests(10); len(rows) != 2 {
		t.Fatalf("插入后回填条数不符: %d", len(rows))
	}

	// 重复迁移同一 req_id 幂等, 不产生重复行
	if _, err := UpsertLocalRequestLog(newLocalLog("fresh-1", `{"new":1}`)); err != nil {
		t.Fatalf("重复 upsert: %v", err)
	}
	if err := GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE req_id = 'fresh-1'`).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("重复迁移产生重复行: %d", cnt)
	}
}
