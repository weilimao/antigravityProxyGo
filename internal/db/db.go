package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	GlobalDB *sql.DB
	dbMutex  sync.Mutex
)

// InitDB initializes the global SQLite database instance
func InitDB(dataDir string) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB != nil {
		return nil
	}

	dbPath := filepath.Join(dataDir, "antigravity.db")
	
	// Create directory if not exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("failed to create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(50) // Allow concurrent readers in WAL mode
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	GlobalDB = db
	return runMigrations(db, dataDir)
}

// CloseDB closes the database connection
func CloseDB() {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	if GlobalDB != nil {
		_ = GlobalDB.Close()
		GlobalDB = nil
	}
}

func runMigrations(db *sql.DB, dataDir string) error {
	// Enable WAL mode and busy timeout for better concurrency and performance
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		log.Printf("Warning: Failed to enable WAL mode/busy timeout: %v\n", err)
	}

	schemas := []string{
		`CREATE TABLE IF NOT EXISTS request_logs (
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
			status_code INTEGER NOT NULL DEFAULT 200,
			method TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_req_logs_user_mode ON request_logs(user_id, mode);`,
		`CREATE INDEX IF NOT EXISTS idx_req_logs_user_timestamp ON request_logs(user_id, timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_req_logs_timestamp ON request_logs(timestamp);`,
		`CREATE TABLE IF NOT EXISTS user_hourly_trends (
			user_id TEXT NOT NULL,
			hour_bucket TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			in_tokens INTEGER NOT NULL DEFAULT 0,
			out_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0.0,
			input_cost REAL NOT NULL DEFAULT 0.0,
			output_cost REAL NOT NULL DEFAULT 0.0,
			cached_cost REAL NOT NULL DEFAULT 0.0,
			PRIMARY KEY (user_id, hour_bucket)
		);`,
		`CREATE TABLE IF NOT EXISTS auto_trigger_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			account_ids TEXT NOT NULL,
			model_names TEXT NOT NULL,
			prompt TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			interval_seconds INTEGER DEFAULT 0,
			next_trigger_time TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, schema := range schemas {
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("migration failed for schema %s: %w", schema, err)
		}
	}

	// Add new columns if not exist (for existing databases)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN server_log_id INTEGER NOT NULL DEFAULT 0;`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN input_cost REAL NOT NULL DEFAULT 0.0;`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN output_cost REAL NOT NULL DEFAULT 0.0;`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN cached_cost REAL NOT NULL DEFAULT 0.0;`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN method TEXT NOT NULL DEFAULT '';`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN host TEXT NOT NULL DEFAULT '';`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN path TEXT NOT NULL DEFAULT '';`)
	_, _ = db.Exec(`ALTER TABLE request_logs ADD COLUMN session_id TEXT NOT NULL DEFAULT '';`)

	// 1. Resolve and map client-side raw keys (e.g. "weilimao") to server-side user IDs (e.g. hash)
	relayUsersPath := filepath.Join(dataDir, "relay_users.json")
	if bytes, err := os.ReadFile(relayUsersPath); err == nil {
		var users []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if err := json.Unmarshal(bytes, &users); err == nil {
			for _, u := range users {
				if u.ID != "" && u.Key != "" {
					// Update raw request_logs
					_, _ = db.Exec("UPDATE request_logs SET user_id = ? WHERE user_id = ? AND mode = 'remote'", u.ID, u.Key)
					
					// Merge existing hourly trends
					_, _ = db.Exec(`
						INSERT INTO user_hourly_trends (
							user_id, hour_bucket, requests, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
						)
						SELECT ?, hour_bucket, requests, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
						FROM user_hourly_trends
						WHERE user_id = ?
						ON CONFLICT(user_id, hour_bucket) DO UPDATE SET
							requests = requests + excluded.requests,
							in_tokens = in_tokens + excluded.in_tokens,
							out_tokens = out_tokens + excluded.out_tokens,
							cached_tokens = cached_tokens + excluded.cached_tokens,
							cost = cost + excluded.cost,
							input_cost = input_cost + excluded.input_cost,
							output_cost = output_cost + excluded.output_cost,
							cached_cost = cached_cost + excluded.cached_cost;
					`, u.ID, u.Key)

					// Delete old key entries
					_, _ = db.Exec("DELETE FROM user_hourly_trends WHERE user_id = ?", u.Key)
				}
			}
		}
	}

	// 2. Deduplicate existing remote logs to clean up any duplicates created by the previous bug
	_, _ = db.Exec(`DELETE FROM request_logs WHERE mode = 'remote' AND id NOT IN (SELECT MIN(id) FROM request_logs WHERE mode = 'remote' GROUP BY server_log_id);`)

	// 3. Migrate existing request logs into user_hourly_trends
	_, _ = db.Exec(`
		INSERT OR IGNORE INTO user_hourly_trends (
			user_id, hour_bucket, requests, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
		)
		SELECT 
			user_id,
			substr(timestamp, 6, 2) || '/' || substr(timestamp, 9, 2) || ' ' || substr(timestamp, 12, 2) || ':00' as hour_bucket,
			count(*),
			sum(in_tokens),
			sum(out_tokens),
			sum(cached_tokens),
			sum(cost),
			sum(input_cost),
			sum(output_cost),
			sum(cached_cost)
		FROM request_logs
		WHERE mode = 'remote' AND user_id IS NOT NULL AND timestamp IS NOT NULL AND length(timestamp) >= 19
		GROUP BY user_id, hour_bucket;
	`)

	// 4. Clean up any invalid formatted trend rows starting with year format e.g. "2026-"
	_, _ = db.Exec("DELETE FROM user_hourly_trends WHERE hour_bucket LIKE '202%';")

	return nil
}
