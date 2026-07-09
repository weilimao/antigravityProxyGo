package db

import (
	"database/sql"
	"fmt"
	"math"
)

type SummaryStats struct {
	TotalRequests     int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCachedTokens int
	TotalCost         float64
	Models            map[string]*ModelStatsSummary
}

type ModelStatsSummary struct {
	Reqs         int     `json:"reqs"`
	InTokens     int     `json:"inTokens"`
	OutTokens    int     `json:"outTokens"`
	CachedTokens int     `json:"cachedTokens"`
	Cost         float64 `json:"cost"`
}

type HourlyTrendSummary struct {
	Time       string  `json:"time"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	Requests   int     `json:"requests"`
	Cost       float64 `json:"cost"`
	InputCost  float64 `json:"inputCost"`
	OutputCost float64 `json:"outputCost"`
	CachedCost float64 `json:"cachedCost"`
}

func QuerySummaryStats(userID, mode string) SummaryStats {
	sum := SummaryStats{
		Models: make(map[string]*ModelStatsSummary),
	}
	if GlobalDB == nil {
		return sum
	}

	query := `
		SELECT 
			model_name, count(*), sum(in_tokens), sum(out_tokens), sum(cached_tokens), sum(cost)
		FROM request_logs
		WHERE user_id = ? AND mode = ?
		GROUP BY model_name
	`

	rows, err := GlobalDB.Query(query, userID, mode)
	if err != nil {
		return sum
	}
	defer rows.Close()

	for rows.Next() {
		var m string
		var reqs, inT, outT, cacheT int
		var cost float64
		if err := rows.Scan(&m, &reqs, &inT, &outT, &cacheT, &cost); err == nil {
			sum.TotalRequests += reqs
			sum.TotalInputTokens += inT
			sum.TotalOutputTokens += outT
			sum.TotalCachedTokens += cacheT
			sum.TotalCost = math.Round((sum.TotalCost+cost)*1000000.0) / 1000000.0

			sum.Models[m] = &ModelStatsSummary{
				Reqs:         reqs,
				InTokens:     inT,
				OutTokens:    outT,
				CachedTokens: cacheT,
				Cost:         math.Round(cost*1000000.0) / 1000000.0,
			}
		}
	}

	return sum
}

func QueryRecentRequests(userID, mode string, limit int) []*RequestLog {
	if GlobalDB == nil {
		return []*RequestLog{}
	}

	query := `
		SELECT 
			id, server_log_id, req_id, timestamp, mode, user_id, model_name, 
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code,
			method, host, path, session_id
		FROM request_logs 
		WHERE user_id = ? AND mode = ?
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`

	rows, err := GlobalDB.Query(query, userID, mode, limit)
	if err != nil {
		return []*RequestLog{}
	}
	defer rows.Close()

	var logs []*RequestLog
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(
			&l.ID, &l.ServerLogID, &l.ReqID, &l.Timestamp, &l.Mode, &l.UserID, &l.ModelName,
			&l.InTokens, &l.OutTokens, &l.CachedTokens, &l.Cost, &l.InputCost, &l.OutputCost, &l.CachedCost, &l.DurationMs, &l.StatusCode,
			&l.Method, &l.Host, &l.Path, &l.SessionID,
		); err == nil {
			logs = append(logs, &l)
		}
	}
	if logs == nil {
		logs = []*RequestLog{}
	}
	return logs
}

func QueryAllRequestLogs() ([]*RequestLog, error) {
	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT
			id, server_log_id, req_id, timestamp, mode, user_id, model_name,
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code,
			method, host, path, session_id
		FROM request_logs
		ORDER BY timestamp DESC, id DESC
	`

	rows, err := GlobalDB.Query(query)
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
		); err == nil {
			logs = append(logs, &l)
		}
	}
	if logs == nil {
		logs = []*RequestLog{}
	}
	return logs, nil
}

func QueryHourlyTrends(userID, mode string) []*HourlyTrendSummary {
	if GlobalDB == nil {
		return []*HourlyTrendSummary{}
	}

	// Group by time_bucket (convert ISO timestamp to "MM/DD HH:00" format)
	// For example: "2026-06-27T00:51:10+08:00" -> month/day hour: "06/27 00:00"
	query := `
		SELECT 
			substr(timestamp, 6, 2) || '/' || substr(timestamp, 9, 2) || ' ' || substr(timestamp, 12, 2) || ':00' as bucket,
			sum(in_tokens), sum(out_tokens), sum(cached_tokens), count(*), sum(cost), sum(input_cost), sum(output_cost), sum(cached_cost)
		FROM request_logs
		WHERE user_id = ? AND mode = ?
		GROUP BY bucket
		ORDER BY bucket ASC
		LIMIT 720
	`

	rows, err := GlobalDB.Query(query, userID, mode)
	if err != nil {
		return []*HourlyTrendSummary{}
	}
	defer rows.Close()

	var trends []*HourlyTrendSummary
	for rows.Next() {
		var b string
		var inT, outT, cacheT, reqs int
		var cost, inCost, outCost, cacheCost float64
		if err := rows.Scan(&b, &inT, &outT, &cacheT, &reqs, &cost, &inCost, &outCost, &cacheCost); err == nil {
			trends = append(trends, &HourlyTrendSummary{
				Time:       b,
				Input:      inT,
				Output:     outT,
				Cached:     cacheT,
				Requests:   reqs,
				Cost:       math.Round(cost*1000000.0) / 1000000.0,
				InputCost:  math.Round(inCost*1000000.0) / 1000000.0,
				OutputCost: math.Round(outCost*1000000.0) / 1000000.0,
				CachedCost: math.Round(cacheCost*1000000.0) / 1000000.0,
			})
		}
	}
	if trends == nil {
		trends = []*HourlyTrendSummary{}
	}
	return trends
}

func GetUserHourlyTrends(userID string) ([]*HourlyTrendSummary, error) {
	if GlobalDB == nil {
		return []*HourlyTrendSummary{}, fmt.Errorf("database not initialized")
	}

	query := `
		SELECT hour_bucket, requests, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
		FROM user_hourly_trends
		WHERE user_id = ?
		ORDER BY hour_bucket ASC
		LIMIT 720
	`

	rows, err := GlobalDB.Query(query, userID)
	if err != nil {
		return []*HourlyTrendSummary{}, err
	}
	defer rows.Close()

	var trends []*HourlyTrendSummary
	for rows.Next() {
		var b string
		var reqs, inT, outT, cacheT int
		var cost, inCost, outCost, cacheCost float64
		if err := rows.Scan(&b, &reqs, &inT, &outT, &cacheT, &cost, &inCost, &outCost, &cacheCost); err == nil {
			trends = append(trends, &HourlyTrendSummary{
				Time:       b,
				Input:      inT,
				Output:     outT,
				Cached:     cacheT,
				Requests:   reqs,
				Cost:       math.Round(cost*1000000.0) / 1000000.0,
				InputCost:  math.Round(inCost*1000000.0) / 1000000.0,
				OutputCost: math.Round(outCost*1000000.0) / 1000000.0,
				CachedCost: math.Round(cacheCost*1000000.0) / 1000000.0,
			})
		}
	}
	if trends == nil {
		trends = []*HourlyTrendSummary{}
	}
	return trends, nil
}

func UpsertHourlyTrendsBatch(tx *sql.Tx, userID string, hourBucket string, requests int, inTokens int, outTokens int, cachedTokens int, cost float64, inCost float64, outCost float64, cacheCost float64) error {
	query := `
		INSERT INTO user_hourly_trends (
			user_id, hour_bucket, requests, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, hour_bucket) DO UPDATE SET
			requests = requests + excluded.requests,
			in_tokens = in_tokens + excluded.in_tokens,
			out_tokens = out_tokens + excluded.out_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens,
			cost = cost + excluded.cost,
			input_cost = input_cost + excluded.input_cost,
			output_cost = output_cost + excluded.output_cost,
			cached_cost = cached_cost + excluded.cached_cost;
	`
	_, err := tx.Exec(query, userID, hourBucket, requests, inTokens, outTokens, cachedTokens, cost, inCost, outCost, cacheCost)
	return err
}

// GetLastInTokensBySession 获取指定会话最新一次成功请求的输入 Token 数量
func GetLastInTokensBySession(sessionID string) (int, error) {
	if GlobalDB == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var dbRows []string
	rows, errQuery := GlobalDB.Query("SELECT id, session_id, in_tokens, status_code, mode FROM request_logs ORDER BY id DESC LIMIT 5")
	if errQuery == nil {
		defer rows.Close()
		for rows.Next() {
			var id, inT, status int
			var sess, md string
			if errS := rows.Scan(&id, &sess, &inT, &status, &md); errS == nil {
				dbRows = append(dbRows, fmt.Sprintf("[ID:%d|Sess:%s|InT:%d|Status:%d|Mode:%s]", id, sess, inT, status, md))
			}
		}
	}

	var inTokens int
	query := `
		SELECT in_tokens 
		FROM request_logs 
		WHERE session_id = ? AND status_code = 200 
		ORDER BY id DESC 
		LIMIT 1
	`
	err := GlobalDB.QueryRow(query, sessionID).Scan(&inTokens)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("未命中此 sessionID 的记录。最近写入错: '%s'。最新 5 条数据: %v", LastInsertError, dbRows)
	}
	if err != nil {
		return 0, fmt.Errorf("查询出错: %v。最近写入错: '%s'。最新 5 条数据: %v", err, LastInsertError, dbRows)
	}
	return inTokens, nil
}

