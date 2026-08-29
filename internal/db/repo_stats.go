package db

import (
	"database/sql"
	"fmt"
)

var LastInsertError string

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
	FirstByteMs  int64   `json:"first_byte_ms"`
	StatusCode   int     `json:"status_code"`
	Method       string  `json:"method"`
	Host         string  `json:"host"`
	Path         string  `json:"path"`
	SessionID    string  `json:"session_id"`
	// Family 标记请求所属协议族(gemini/claude 直连默认 "", NVIDIA 号池链路记 "nvidia")。
	// 供远程聚合查询按族过滤, 与 stats.RequestLog.Family / RequestLogLite.Family 同义。
	Family string `json:"family"`
	// ReasoningEffort 记录命中上游的思考等级(映射折叠后真正发给上游的值, 非客户端原始意图档)。
	// 供前端请求日志「模型」列追加 (档) 后缀展示。空串 = 未开思考 / 全局关 / 无该概念。
	// 与 stats.RequestLog.ReasoningEffort / RequestLogLite.ReasoningEffort 同义。
	ReasoningEffort string `json:"reasoning_effort"`
	// RequestBody/RequestHeaders: 本地模式请求报文(Truncate 后的 JSON 文本, 空串=未存)。
	// 仅最新 N 条保留(PruneLocalRequestBodies 周期置空老行), 供「查看详情」跨重启可读。
	RequestBody    string `json:"request_body"`
	RequestHeaders string `json:"request_headers"`
	// CacheStatus 缓存命中标记(HIT/MISS/NONE), 与 stats.RequestLog.CacheStatus 同义。
	CacheStatus string `json:"cache_status"`
}

// InsertRequestLog inserts a new request log into the database
func InsertRequestLog(log *RequestLog) error {
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
		INSERT INTO request_logs (
			server_log_id, req_id, timestamp, mode, user_id, model_name,
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, first_byte_ms, status_code,
			method, host, path, session_id, family, reasoning_effort,
			request_body, request_headers, cache_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := GlobalDB.Exec(query,
		log.ServerLogID, log.ReqID, log.Timestamp, log.Mode, log.UserID, log.ModelName,
		log.InTokens, log.OutTokens, log.CachedTokens, log.Cost, log.InputCost, log.OutputCost, log.CachedCost, log.DurationMs, log.FirstByteMs, log.StatusCode,
		log.Method, log.Host, log.Path, log.SessionID, log.Family, log.ReasoningEffort,
		log.RequestBody, log.RequestHeaders, log.CacheStatus,
	)
	if err != nil {
		LastInsertError = err.Error()
		return err
	}
	LastInsertError = ""
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
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, first_byte_ms, status_code,
			method, host, path, session_id, family, reasoning_effort
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
			&l.InTokens, &l.OutTokens, &l.CachedTokens, &l.Cost, &l.InputCost, &l.OutputCost, &l.CachedCost, &l.DurationMs, &l.FirstByteMs, &l.StatusCode,
			&l.Method, &l.Host, &l.Path, &l.SessionID, &l.Family, &l.ReasoningEffort,
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

// QueryRecentLocalRequests 读取最新 limit 条 mode='local' 请求日志(含 request_body/request_headers/cache_status),
// 按 id DESC 返回(最新在前)。供 stats.Tracker 启动时从 DB 回填内存环:请求列表与详情弹窗跨重启可用,
// 替代旧实现中 stats.json 携带 150 条完整报文的 12MB 级持久化。DB 未初始化或查询失败返回 nil。
func QueryRecentLocalRequests(limit int) []*RequestLog {
	if GlobalDB == nil || limit <= 0 {
		return nil
	}
	query := `
		SELECT
			id, server_log_id, req_id, timestamp, mode, user_id, model_name,
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, first_byte_ms, status_code,
			method, host, path, session_id, family, reasoning_effort,
			request_body, request_headers, cache_status
		FROM request_logs
		WHERE mode = 'local'
		ORDER BY id DESC
		LIMIT ?
	`
	rows, err := GlobalDB.Query(query, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var logs []*RequestLog
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(
			&l.ID, &l.ServerLogID, &l.ReqID, &l.Timestamp, &l.Mode, &l.UserID, &l.ModelName,
			&l.InTokens, &l.OutTokens, &l.CachedTokens, &l.Cost, &l.InputCost, &l.OutputCost, &l.CachedCost, &l.DurationMs, &l.FirstByteMs, &l.StatusCode,
			&l.Method, &l.Host, &l.Path, &l.SessionID, &l.Family, &l.ReasoningEffort,
			&l.RequestBody, &l.RequestHeaders, &l.CacheStatus,
		); err != nil {
			return nil
		}
		logs = append(logs, &l)
	}
	return logs
}

// PruneLocalRequestBodies 将 mode='local' 老行的 request_body/request_headers 置空,
// 仅保留最新 keep 条的报文(与内存环 MaxRequestLogs 口径一致), 防 request_logs 随报文累积无限膨胀。
// 标量字段一律保留(user_hourly_trends 重建与远端聚合依赖全量标量历史)。幂等、可随时重跑。
func PruneLocalRequestBodies(keep int) error {
	if GlobalDB == nil || keep < 0 {
		return nil
	}
	_, err := GlobalDB.Exec(`
		UPDATE request_logs
		SET request_body = '', request_headers = ''
		WHERE mode = 'local'
		  AND (request_body <> '' OR request_headers <> '')
		  AND id <= (SELECT IFNULL(MAX(id), 0) FROM request_logs WHERE mode = 'local') - ?
	`, keep)
	return err
}

// UpsertLocalRequestLog 按 req_id 幂等落一条本地模式日志:已存在则仅回填报文与缓存标记
// (旧双写时代 DB 里可能已有同 req_id 的标量行,如 stats.json 存量迁移),不存在则整行插入。
// 传入 log.Mode 为空时兜底 "local"。返回 affected 语义: true=整行插入, false=仅更新报文。
func UpsertLocalRequestLog(log *RequestLog) (bool, error) {
	if GlobalDB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	if log.Mode == "" {
		log.Mode = "local"
	}
	res, err := GlobalDB.Exec(`
		UPDATE request_logs
		SET request_body = ?, request_headers = ?, cache_status = ?
		WHERE req_id = ? AND mode = 'local'
	`, log.RequestBody, log.RequestHeaders, log.CacheStatus, log.ReqID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		return false, nil
	}
	if err := InsertRequestLog(log); err != nil {
		return false, err
	}
	return true, nil
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
