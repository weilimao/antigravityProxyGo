package db

import (
	"database/sql"
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
	db.SetMaxOpenConns(1) // SQLite works best with 1 writer to avoid BUSY errors
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	GlobalDB = db
	return runMigrations(db)
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

func runMigrations(db *sql.DB) error {
	// Enable WAL mode for better concurrency and performance
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		log.Printf("Warning: Failed to enable WAL mode: %v\n", err)
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
		`CREATE INDEX IF NOT EXISTS idx_req_logs_timestamp ON request_logs(timestamp);`,
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

	return nil
}
