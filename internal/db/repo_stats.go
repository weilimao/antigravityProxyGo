package db

import (
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
}

// InsertRequestLog inserts a new request log into the database
func InsertRequestLog(log *RequestLog) error {
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
		INSERT INTO request_logs (
			server_log_id, req_id, timestamp, mode, user_id, model_name, 
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := GlobalDB.Exec(query,
		log.ServerLogID, log.ReqID, log.Timestamp, log.Mode, log.UserID, log.ModelName,
		log.InTokens, log.OutTokens, log.CachedTokens, log.Cost, log.InputCost, log.OutputCost, log.CachedCost, log.DurationMs, log.StatusCode,
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
	var maxID sqlNullableInt64
	if err := row.Scan(&maxID.val); err == nil && maxID.val != nil {
		return *maxID.val
	}
	return 0
}

type sqlNullableInt64 struct {
	val *int64
}

// GetRequestLogsSince retrieves logs for a user/mode that were created after lastID
func GetRequestLogsSince(userID, mode string, lastID int64, limit int) ([]*RequestLog, error) {
	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT 
			id, server_log_id, req_id, timestamp, mode, user_id, model_name, 
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code
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
		); err != nil {
			return nil, err
		}
		logs = append(logs, &l)
	}

	return logs, nil
}
