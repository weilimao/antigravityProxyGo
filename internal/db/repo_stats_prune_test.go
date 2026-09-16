package db

import (
	"fmt"
	"testing"
	"time"
)

func TestPruneUserRequestLogs_LimitTo150(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	userID := "test-user-limit-150"

	// 连续插入 160 条记录，每次 InsertRequestLog 都会自动调用 PruneUserRequestLogs
	for i := 1; i <= 160; i++ {
		log := &RequestLog{
			ReqID:      fmt.Sprintf("req-limit-%d", i),
			Timestamp:  time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			Mode:       "local",
			UserID:     userID,
			ModelName:  "gpt-4o",
			DurationMs: 100,
			StatusCode: 200,
		}
		if err := InsertRequestLog(log); err != nil {
			t.Fatalf("InsertRequestLog(%d): %v", i, err)
		}
	}

	// 验证表中只保留最新的 150 条
	var count int
	err := GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id = ?`, userID).Scan(&count)
	if err != nil {
		t.Fatalf("Query count failed: %v", err)
	}
	if count != 150 {
		t.Fatalf("Expected 150 logs remaining, but got %d", count)
	}

	// 验证最旧的 10 条 (req-limit-1 ~ req-limit-10) 已经被淘汰
	for i := 1; i <= 10; i++ {
		reqID := fmt.Sprintf("req-limit-%d", i)
		var exists int
		_ = GlobalDB.QueryRow(`SELECT 1 FROM request_logs WHERE req_id = ?`, reqID).Scan(&exists)
		if exists == 1 {
			t.Errorf("Expected %s to be pruned, but it still exists", reqID)
		}
	}

	// 验证最新的 (req-limit-11 ~ req-limit-160) 都存在
	for i := 11; i <= 160; i += 20 {
		reqID := fmt.Sprintf("req-limit-%d", i)
		var exists int
		_ = GlobalDB.QueryRow(`SELECT 1 FROM request_logs WHERE req_id = ?`, reqID).Scan(&exists)
		if exists != 1 {
			t.Errorf("Expected %s to exist, but it was deleted", reqID)
		}
	}
}

func TestPruneAllUsersRequestLogs(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	userA := "user-batch-a"
	userB := "user-batch-b"

	// 插入一批数据，绕过 InsertRequestLog 的自动 prune (直接 SQL insert) 模拟存量数据膨胀
	query := `
		INSERT INTO request_logs (
			server_log_id, req_id, timestamp, mode, user_id, model_name,
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, first_byte_ms, status_code,
			method, host, path, session_id, family, reasoning_effort,
			request_body, request_headers, cache_status
		) VALUES (0, ?, ?, 'local', ?, 'test', 0, 0, 0, 0, 0, 0, 0, 0, 0, 200, 'POST', '', '', '', '', '', '', '', '')
	`

	for i := 1; i <= 160; i++ {
		_, err := GlobalDB.Exec(query, fmt.Sprintf("req-a-%d", i), time.Now().Format(time.RFC3339), userA)
		if err != nil {
			t.Fatalf("Direct insert A failed: %v", err)
		}
		_, err = GlobalDB.Exec(query, fmt.Sprintf("req-b-%d", i), time.Now().Format(time.RFC3339), userB)
		if err != nil {
			t.Fatalf("Direct insert B failed: %v", err)
		}
	}

	// 验证插入前各自有 160 条
	var countA, countB int
	_ = GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id = ?`, userA).Scan(&countA)
	_ = GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id = ?`, userB).Scan(&countB)
	if countA != 160 || countB != 160 {
		t.Fatalf("Pre-condition failed: countA=%d, countB=%d", countA, countB)
	}

	// 执行全局批量修剪
	if err := PruneAllUsersRequestLogs(150); err != nil {
		t.Fatalf("PruneAllUsersRequestLogs failed: %v", err)
	}

	// 验证修剪后各自正好剩 150 条
	_ = GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id = ?`, userA).Scan(&countA)
	_ = GlobalDB.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id = ?`, userB).Scan(&countB)
	if countA != 150 {
		t.Errorf("Expected userA to have 150 logs, got %d", countA)
	}
	if countB != 150 {
		t.Errorf("Expected userB to have 150 logs, got %d", countB)
	}
}
