package db

import (
	"database/sql"
	"fmt"
)

// RequestLog represents a single request entry in DB
type RequestLog struct {
	ID           int64   `json:"id"` // SQLite rowid/autoincrement
	ServerLogID  int64   `json:"server_log_id"`
	ReqID        string  `json:"req_id"`
	Timestamp    string  `json:"timestamp"` // ISO8601 string
	Mode         string  `json:"mode"`
	UserID       string  `json:"user_id"`
	ModelName    string  `json:"model_name"`
	InTokens     int     `json:"in_tokens"`
	OutTokens    int     `json:"out_tokens"`
	CachedTokens int     `json:"cached_tokens"`
	Cost         float64 `json:"cost"`
	InputCost    float64 `json:"input_cost"`
	OutputCost   float64 `json:"output_cost"`
	CachedCost   float64 `json:"cached_cost"`
	DurationMs   int64   `json:"duration_ms"`
	StatusCode   int     `json:"status_code"`
	Method       string  `json:"method"`
	Host         string  `json:"host"`
	Path         string  `json:"path"`
	SessionID    string  `json:"session_id"`
}

// InsertRequestLog inserts a new request log into the database
func InsertRequestLog(log *RequestLog) error {
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
		INSERT INTO request_logs (
			server_log_id, req_id, timestamp, mode, user_id, model_name, 
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code,
			method, host, path, session_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := GlobalDB.Exec(query,
		log.ServerLogID, log.ReqID, log.Timestamp, log.Mode, log.UserID, log.ModelName,
		log.InTokens, log.OutTokens, log.CachedTokens, log.Cost, log.InputCost, log.OutputCost, log.CachedCost, log.DurationMs, log.StatusCode,
		log.Method, log.Host, log.Path, log.SessionID,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	log.ID = id
	return nil
}

// GetMaxServerLogID retrieves the maximum server_log_id for a given user and mode
func GetMaxServerLogID(userID, mode string) int64 {
	if GlobalDB == nil {
		return 0
	}
	row := GlobalDB.QueryRow(`SELECT max(server_log_id) FROM request_logs WHERE user_id = ? AND mode = ?`, userID, mode)
	var maxID sql.NullInt64
	if err := row.Scan(&maxID); err == nil && maxID.Valid {
		return maxID.Int64
	}
	return 0
}

// GetRequestLogsSince retrieves logs for a user/mode that were created after lastID
func GetRequestLogsSince(userID, mode string, lastID int64, limit int) ([]*RequestLog, error) {
	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT 
			id, server_log_id, req_id, timestamp, mode, user_id, model_name, 
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code,
			method, host, path, session_id
		FROM request_logs 
		WHERE user_id = ? AND mode = ? AND id > ?
		ORDER BY id ASC
		LIMIT ?
	`

	rows, err := GlobalDB.Query(query, userID, mode, lastID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*RequestLog
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(
			&l.ID, &l.ServerLogID, &l.ReqID, &l.Timestamp, &l.Mode, &l.UserID, &l.ModelName,
			&l.InTokens, &l.OutTokens, &l.CachedTokens, &l.Cost, &l.InputCost, &l.OutputCost, &l.CachedCost, &l.DurationMs, &l.StatusCode,
			&l.Method, &l.Host, &l.Path, &l.SessionID,
		); err != nil {
			return nil, err
		}
		logs = append(logs, &l)
	}

	return logs, nil
}

// GetTokensForUserModelFamilySince calculates total tokens for a specific model family since a given timestamp
func GetTokensForUserModelFamilySince(userID string, modelKeyword string, sinceIso string) (int64, error) {
	if GlobalDB == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT SUM(in_tokens + out_tokens) 
		FROM request_logs 
		WHERE user_id = ? 
		AND model_name LIKE ? 
		AND timestamp >= ?
	`
	likePattern := "%" + modelKeyword + "%"
	row := GlobalDB.QueryRow(query, userID, likePattern, sinceIso)
	
	var total sql.NullInt64
	if err := row.Scan(&total); err != nil {
		return 0, err
	}
	
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

// GetOldestRequestTimestampSince retrieves the timestamp of the oldest request for a specific model family since a given timestamp
func GetOldestRequestTimestampSince(userID string, modelKeyword string, sinceIso string) (string, error) {
	if GlobalDB == nil {
		return "", fmt.Errorf("database not initialized")
	}

	query := `
		SELECT MIN(timestamp) 
		FROM request_logs 
		WHERE user_id = ? 
		AND model_name LIKE ? 
		AND timestamp >= ?
	`
	likePattern := "%" + modelKeyword + "%"
	row := GlobalDB.QueryRow(query, userID, likePattern, sinceIso)

	var firstTimestamp sql.NullString
	if err := row.Scan(&firstTimestamp); err != nil {
		return "", err
	}

	if !firstTimestamp.Valid {
		return "", nil
	}
	return firstTimestamp.String, nil
}

// GetMaxLogID retrieves the maximum local id for a given user and mode
func GetMaxLogID(userID, mode string) int64 {
	if GlobalDB == nil {
		return 0
	}
	row := GlobalDB.QueryRow(`SELECT max(id) FROM request_logs WHERE user_id = ? AND mode = ?`, userID, mode)
	var maxID sql.NullInt64
	if err := row.Scan(&maxID); err == nil && maxID.Valid {
		return maxID.Int64
	}
	return 0
}

// HasServerLogID checks if a log with the given server_log_id already exists locally for this user and mode
func HasServerLogID(userID string, serverLogID int64, mode string) bool {
	if GlobalDB == nil {
		return false
	}
	row := GlobalDB.QueryRow(`SELECT 1 FROM request_logs WHERE user_id = ? AND server_log_id = ? AND mode = ? LIMIT 1`, userID, serverLogID, mode)
	var val int
	if err := row.Scan(&val); err == nil {
		return true
	}
	return false
}




// GetQuotaWindowStart retrieves the window_start time for a quota type.
func GetQuotaWindowStart(userID string, quotaType string) (string, error) {
	if GlobalDB == nil {
		return "", fmt.Errorf("database not initialized")
	}
	var windowStart string
	err := GlobalDB.QueryRow(`SELECT window_start FROM quota_windows WHERE user_id = ? AND quota_type = ?`, userID, quotaType).Scan(&windowStart)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return windowStart, nil
}

// SetQuotaWindowStart sets the window_start time for a quota type.
func SetQuotaWindowStart(userID string, quotaType string, windowStart string) error {
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := GlobalDB.Exec(`
		REPLACE INTO quota_windows (user_id, quota_type, window_start) 
		VALUES (?, ?, ?)
	`, userID, quotaType, windowStart)
	return err
}
