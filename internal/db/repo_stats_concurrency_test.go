package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite"
)

// TestInsertRequestLog_HighConcurrency 验证高并发下多协程密集写入 InsertRequestLog 无锁死争用
func TestInsertRequestLog_HighConcurrency(t *testing.T) {
	// 1. 沙箱隔离：创建临时测试目录
	tempDir, err := os.MkdirTemp("", "antigravity_test_db_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// 2. 严格的环境清理 Teardown 钩子
	defer func() {
		if GlobalDB != nil {
			_ = GlobalDB.Close()
			GlobalDB = nil
		}
		_ = os.RemoveAll(tempDir)
	}()

	testDBPath := filepath.Join(tempDir, "test_antigravity.db")
	dbConn, err := sql.Open("sqlite", testDBPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	GlobalDB = dbConn

	// 初始化 request_logs 表结构
	schema := `
	CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_log_id INTEGER NOT NULL DEFAULT 0,
		req_id TEXT NOT NULL,
		timestamp TEXT NOT NULL,
		mode TEXT NOT NULL,
		user_id TEXT,
		model_name TEXT NOT NULL,
		in_tokens INTEGER NOT NULL DEFAULT 0,
		out_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		cost REAL NOT NULL DEFAULT 0.0,
		input_cost REAL NOT NULL DEFAULT 0.0,
		output_cost REAL NOT NULL DEFAULT 0.0,
		cached_cost REAL NOT NULL DEFAULT 0.0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		first_byte_ms INTEGER NOT NULL DEFAULT 0,
		status_code INTEGER NOT NULL DEFAULT 200,
		method TEXT NOT NULL DEFAULT '',
		host TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		family TEXT NOT NULL DEFAULT '',
		reasoning_effort TEXT NOT NULL DEFAULT '',
		request_body TEXT NOT NULL DEFAULT '',
		request_headers TEXT NOT NULL DEFAULT '',
		cache_status TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := GlobalDB.Exec(schema); err != nil {
		t.Fatalf("failed to create test schema: %v", err)
	}

	// 3. 模拟 30 个 Goroutine，每个并发写入 10 条日志，总共 300 条
	concurrency := 30
	perWorker := 10
	var wg sync.WaitGroup
	errChan := make(chan error, concurrency*perWorker)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				reqLog := &RequestLog{
					ReqID:      fmt.Sprintf("req_test_%d_%d", workerID, j),
					Timestamp:  time.Now().Format("2006-01-02T15:04:05.000Z"),
					Mode:       "relay",
					UserID:     "user_test",
					ModelName:  "claude-3-7-sonnet",
					InTokens:   100,
					OutTokens:  200,
					StatusCode: 200,
				}
				if insertErr := InsertRequestLog(reqLog); insertErr != nil {
					errChan <- insertErr
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	elapsed := time.Since(start)
	t.Logf("Completed %d concurrent log inserts in %v", concurrency*perWorker, elapsed)

	// 验证无任何写入错误
	var insertErrors []error
	for e := range errChan {
		insertErrors = append(insertErrors, e)
	}
	if len(insertErrors) > 0 {
		t.Fatalf("encountered %d insert errors during concurrent test: %v", len(insertErrors), insertErrors[0])
	}

	// 验证数据库总行数精确为 300
	var count int
	if err := GlobalDB.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&count); err != nil {
		t.Fatalf("failed to query count: %v", err)
	}
	if count != concurrency*perWorker {
		t.Errorf("expected count=%d, got %d", concurrency*perWorker, count)
	}

	// 4. 验证后台定期修剪逻辑工作正常
	if err := PruneGlobalRequestLogs(150); err != nil {
		t.Fatalf("PruneGlobalRequestLogs failed: %v", err)
	}
	var countAfterPrune int
	if err := GlobalDB.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&countAfterPrune); err != nil {
		t.Fatalf("failed to query count after prune: %v", err)
	}
	if countAfterPrune != 150 {
		t.Errorf("expected count after prune=150, got %d", countAfterPrune)
	}
}
