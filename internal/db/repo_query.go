package db

import (
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
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost, duration_ms, status_code
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
		); err == nil {
			logs = append(logs, &l)
		}
	}
	if logs == nil {
		logs = []*RequestLog{}
	}
	return logs
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
