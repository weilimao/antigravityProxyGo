package relay

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
)

// AdminLogItem represents a single log entry displayed in the logs table
type AdminLogItem struct {
	ID              int64   `json:"id"`
	ReqID           string  `json:"reqId"`
	Timestamp       string  `json:"timestamp"`
	Method          string  `json:"method"`
	Host            string  `json:"host"`
	Path            string  `json:"path"`
	SessionID       string  `json:"sessionId"`
	Model           string  `json:"model"`
	Account         string  `json:"account"`
	InTokens        int     `json:"inTokens"`
	OutTokens       int     `json:"outTokens"`
	CachedTokens    int     `json:"cachedTokens"`
	Cost            float64 `json:"cost"`
	InputCost       float64 `json:"inputCost"`
	OutputCost      float64 `json:"outputCost"`
	CachedCost      float64 `json:"cachedCost"`
	FirstByteMs     int64   `json:"firstByteMs"`
	DurationMs      int64   `json:"durationMs"`
	CacheStatus     string  `json:"cacheStatus"`
	StatusCode      int     `json:"statusCode"`
	Family          string  `json:"family"`
	ReasoningEffort string  `json:"reasoningEffort"`
	RequestBody     string  `json:"requestBody,omitempty"`
	RequestHeaders  string  `json:"requestHeaders,omitempty"`
}

// AdminLogSummary represents aggregated statistics
type AdminLogSummary struct {
	TotalRequests     int     `json:"totalRequests"`
	TotalInputTokens  int64   `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalCachedTokens int64   `json:"totalCachedTokens"`
	TotalCost         float64 `json:"totalCost"`
	InputCost         float64 `json:"inputCost"`
	OutputCost        float64 `json:"outputCost"`
	CachedCost        float64 `json:"cachedCost"`
	CacheHitRate      float64 `json:"cacheHitRate"`
}

// ModelPerfStat represents per-model latency and count statistics
type ModelPerfStat struct {
	Model       string  `json:"model"`
	Count       int     `json:"count"`
	AvgDuration float64 `json:"avgDurationMs"`
	AvgTTFT     float64 `json:"avgTtftMs"`
}

// AdminLogsResponse represents the full response payload for /api/admin/logs
type AdminLogsResponse struct {
	Success   bool             `json:"success"`
	Total     int              `json:"total"`
	Page      int              `json:"page"`
	PageSize  int              `json:"pageSize"`
	Summary   AdminLogSummary  `json:"summary"`
	ModelPerf []ModelPerfStat  `json:"modelPerf"`
	List      []AdminLogItem   `json:"list"`
	Accounts  []string         `json:"accounts"`
}

// handleAdminLogs handles GET /api/admin/logs
func (h *APIHandler) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	q := r.URL.Query()
	username := strings.TrimSpace(q.Get("username"))
	if username == "" {
		username = strings.TrimSpace(q.Get("account"))
	}
	status := strings.TrimSpace(q.Get("status"))
	search := strings.TrimSpace(q.Get("search"))

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	if pageSize < 1 {
		pageSize = 15
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// 1. Resolve user mapping if username is provided
	var targetSessionIDs []string
	var targetUserIDs []string
	var resolvedUser *RelayUser
	if username != "" && username != "all" {
		targetUserIDs = append(targetUserIDs, username)
		if h.authMgr != nil && h.authMgr.userMgr != nil {
			resolvedUser = h.authMgr.userMgr.GetUserByKey(username)
			if resolvedUser == nil {
				resolvedUser = h.authMgr.userMgr.GetUserByID(username)
			}
			if resolvedUser != nil {
				targetSessionIDs = append(targetSessionIDs, resolvedUser.ID)
				targetUserIDs = append(targetUserIDs, resolvedUser.Key)
				for _, k := range resolvedUser.APIKeys {
					if k.Key != "" {
						targetUserIDs = append(targetUserIDs, k.Key)
					}
				}
			}
		}
	}

	// 2. Fetch log accounts for dropdown
	accounts := h.getLogAccounts()

	// 3. Query from db.GlobalDB if initialized
	if db.GlobalDB == nil {
		writeJSON(w, http.StatusOK, AdminLogsResponse{
			Success:   true,
			Total:     0,
			Page:      page,
			PageSize:  pageSize,
			Summary:   AdminLogSummary{},
			ModelPerf: []ModelPerfStat{},
			List:      []AdminLogItem{},
			Accounts:  accounts,
		})
		return
	}

	// Build WHERE conditions
	whereClauses := []string{"1=1"}
	var args []interface{}

	// Account filter
	if len(targetSessionIDs) > 0 || len(targetUserIDs) > 0 {
		var userSubClauses []string
		for _, sid := range targetSessionIDs {
			userSubClauses = append(userSubClauses, "session_id = ?")
			args = append(args, sid)
		}
		for _, uid := range targetUserIDs {
			userSubClauses = append(userSubClauses, "user_id = ?")
			args = append(args, uid)
		}
		if len(userSubClauses) > 0 {
			whereClauses = append(whereClauses, "("+strings.Join(userSubClauses, " OR ")+")")
		}
	}

	// Status filter
	switch strings.ToLower(status) {
	case "success":
		whereClauses = append(whereClauses, "status_code < 400")
	case "error":
		whereClauses = append(whereClauses, "status_code >= 400")
	case "hit":
		whereClauses = append(whereClauses, "(cache_status = 'HIT' OR cached_tokens > 0)")
	case "miss":
		whereClauses = append(whereClauses, "(cache_status = 'MISS' OR (status_code < 400 AND cached_tokens = 0))")
	}

	// Search filter
	if search != "" {
		likeArg := "%" + search + "%"
		whereClauses = append(whereClauses, "(req_id LIKE ? OR path LIKE ? OR host LIKE ? OR model_name LIKE ? OR session_id LIKE ? OR user_id LIKE ? OR method LIKE ?)")
		for i := 0; i < 7; i++ {
			args = append(args, likeArg)
		}
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 4. Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM request_logs WHERE %s", whereSQL)
	var totalCount int
	if err := db.GlobalDB.QueryRow(countQuery, args...).Scan(&totalCount); err != nil {
		totalCount = 0
	}
	if (len(targetSessionIDs) > 0 || len(targetUserIDs) > 0) && totalCount > 150 {
		totalCount = 150
	}

	// 5. Summary metrics
	summaryQuery := fmt.Sprintf(`
		SELECT 
			count(*), 
			COALESCE(sum(in_tokens), 0), 
			COALESCE(sum(out_tokens), 0), 
			COALESCE(sum(cached_tokens), 0), 
			COALESCE(sum(cost), 0.0), 
			COALESCE(sum(input_cost), 0.0), 
			COALESCE(sum(output_cost), 0.0), 
			COALESCE(sum(cached_cost), 0.0) 
		FROM request_logs 
		WHERE %s`, whereSQL)

	var summary AdminLogSummary
	var rawInTokens, rawOutTokens, rawCachedTokens sql.NullInt64
	var rawCost, rawInCost, rawOutCost, rawCacheCost sql.NullFloat64
	var rawReqCount int

	if err := db.GlobalDB.QueryRow(summaryQuery, args...).Scan(
		&rawReqCount,
		&rawInTokens,
		&rawOutTokens,
		&rawCachedTokens,
		&rawCost,
		&rawInCost,
		&rawOutCost,
		&rawCacheCost,
	); err == nil {
		summary.TotalRequests = rawReqCount
		summary.TotalInputTokens = rawInTokens.Int64
		summary.TotalOutputTokens = rawOutTokens.Int64
		summary.TotalCachedTokens = rawCachedTokens.Int64
		summary.TotalCost = math.Round(rawCost.Float64*1000000.0) / 1000000.0
		summary.InputCost = math.Round(rawInCost.Float64*1000000.0) / 1000000.0
		summary.OutputCost = math.Round(rawOutCost.Float64*1000000.0) / 1000000.0
		summary.CachedCost = math.Round(rawCacheCost.Float64*1000000.0) / 1000000.0

		if summary.TotalInputTokens > 0 {
			summary.CacheHitRate = math.Round((float64(summary.TotalCachedTokens)/float64(summary.TotalInputTokens))*1000.0) / 10.0
		}
	}

	// 若查询指定中继用户，且中继统计管理器中记录了该用户的权威生命周期请求与用量，在未做二次状态/搜索过滤时优先以其为准
	if resolvedUser != nil && h.statsMgr != nil {
		if uStats := h.statsMgr.GetUserStats(resolvedUser.ID); uStats != nil && uStats.TotalRequests > 0 {
			if status == "" && search == "" {
				summary.TotalRequests = uStats.TotalRequests
				summary.TotalInputTokens = int64(uStats.TotalInputTokens)
				summary.TotalOutputTokens = int64(uStats.TotalOutputTokens)
				summary.TotalCachedTokens = int64(uStats.TotalCachedTokens)
				summary.TotalCost = math.Round(uStats.TotalCost*1000000.0) / 1000000.0
				if totalCount == 0 {
					totalCount = uStats.TotalRequests
				}
			} else if summary.TotalRequests == 0 {
				summary.TotalRequests = uStats.TotalRequests
			}
		}
	}

	// 6. Model perf breakdown
	perfQuery := fmt.Sprintf(`
		SELECT 
			model_name, 
			count(*), 
			COALESCE(avg(duration_ms), 0.0), 
			COALESCE(avg(first_byte_ms), 0.0) 
		FROM request_logs 
		WHERE %s 
		GROUP BY model_name 
		ORDER BY count(*) DESC 
		LIMIT 10`, whereSQL)

	var modelPerfs []ModelPerfStat
	if pRows, err := db.GlobalDB.Query(perfQuery, args...); err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var mp ModelPerfStat
			if err := pRows.Scan(&mp.Model, &mp.Count, &mp.AvgDuration, &mp.AvgTTFT); err == nil {
				mp.AvgDuration = math.Round(mp.AvgDuration*10.0) / 10.0
				mp.AvgTTFT = math.Round(mp.AvgTTFT*10.0) / 10.0
				modelPerfs = append(modelPerfs, mp)
			}
		}
	}
	if modelPerfs == nil {
		modelPerfs = []ModelPerfStat{}
	}

	// 7. Paginated log items
	offset := (page - 1) * pageSize
	actualPageSize := pageSize
	if len(targetSessionIDs) > 0 || len(targetUserIDs) > 0 {
		if offset >= 150 {
			writeJSON(w, http.StatusOK, AdminLogsResponse{
				Success:   true,
				Total:     totalCount,
				Page:      page,
				PageSize:  pageSize,
				Summary:   summary,
				ModelPerf: modelPerfs,
				List:      []AdminLogItem{},
				Accounts:  accounts,
			})
			return
		}
		if offset+actualPageSize > 150 {
			actualPageSize = 150 - offset
		}
	}

	listArgs := append(args, actualPageSize, offset)
	listQuery := fmt.Sprintf(`
		SELECT
			id, req_id, timestamp, mode, user_id, model_name,
			in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost,
			duration_ms, first_byte_ms, status_code,
			method, host, path, session_id, family, reasoning_effort, cache_status
		FROM request_logs
		WHERE %s
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, whereSQL)

	var list []AdminLogItem
	rows, err := db.GlobalDB.Query(listQuery, listArgs...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item AdminLogItem
			var rawMode string
			if err := rows.Scan(
				&item.ID, &item.ReqID, &item.Timestamp, &rawMode, &item.Account, &item.Model,
				&item.InTokens, &item.OutTokens, &item.CachedTokens, &item.Cost, &item.InputCost, &item.OutputCost, &item.CachedCost,
				&item.DurationMs, &item.FirstByteMs, &item.StatusCode,
				&item.Method, &item.Host, &item.Path, &item.SessionID, &item.Family, &item.ReasoningEffort, &item.CacheStatus,
			); err == nil {
				if t, parseErr := time.Parse(time.RFC3339, item.Timestamp); parseErr == nil {
					item.Timestamp = t.Format(time.RFC3339)
				} else if tLegacy, legacyErr := time.ParseInLocation("01/02 15:04:05", item.Timestamp, time.Local); legacyErr == nil {
					now := time.Now()
					full := time.Date(now.Year(), tLegacy.Month(), tLegacy.Day(), tLegacy.Hour(), tLegacy.Minute(), tLegacy.Second(), 0, time.Local)
					if full.After(now) {
						full = full.AddDate(-1, 0, 0)
					}
					item.Timestamp = full.Format(time.RFC3339)
				}
				if item.CacheStatus == "" {
					if item.CachedTokens > 0 {
						item.CacheStatus = "HIT"
					} else {
						item.CacheStatus = "MISS"
					}
				}
				list = append(list, item)
			}
		}
	}
	if list == nil {
		list = []AdminLogItem{}
	}

	writeJSON(w, http.StatusOK, AdminLogsResponse{
		Success:   true,
		Total:     totalCount,
		Page:      page,
		PageSize:  pageSize,
		Summary:   summary,
		ModelPerf: modelPerfs,
		List:      list,
		Accounts:  accounts,
	})
}

// handleAdminLogDetail handles GET /api/admin/logs/detail?id=...&req_id=...
func (h *APIHandler) handleAdminLogDetail(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	reqID := strings.TrimSpace(r.URL.Query().Get("req_id"))
	idStr := strings.TrimSpace(r.URL.Query().Get("id"))

	if reqID == "" && idStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "req_id or id is required"})
		return
	}

	// 1. Try memory cache first
	if reqID != "" && h.statsMgr != nil {
		if cached := h.statsMgr.GetCachedLog(reqID); cached != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"log":     cached,
			})
			return
		}
	}

	// 2. Query from SQLite db.GlobalDB
	if db.GlobalDB == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "log not found"})
		return
	}

	var query string
	var arg interface{}
	if idStr != "" {
		query = `
			SELECT
				id, req_id, timestamp, mode, user_id, model_name,
				in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost,
				duration_ms, first_byte_ms, status_code,
				method, host, path, session_id, family, reasoning_effort,
				request_body, request_headers, cache_status
			FROM request_logs
			WHERE id = ?
			LIMIT 1`
		arg = idStr
	} else {
		query = `
			SELECT
				id, req_id, timestamp, mode, user_id, model_name,
				in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost,
				duration_ms, first_byte_ms, status_code,
				method, host, path, session_id, family, reasoning_effort,
				request_body, request_headers, cache_status
			FROM request_logs
			WHERE req_id = ?
			LIMIT 1`
		arg = reqID
	}

	var l db.RequestLog
	err := db.GlobalDB.QueryRow(query, arg).Scan(
		&l.ID, &l.ReqID, &l.Timestamp, &l.Mode, &l.UserID, &l.ModelName,
		&l.InTokens, &l.OutTokens, &l.CachedTokens, &l.Cost, &l.InputCost, &l.OutputCost, &l.CachedCost,
		&l.DurationMs, &l.FirstByteMs, &l.StatusCode,
		&l.Method, &l.Host, &l.Path, &l.SessionID, &l.Family, &l.ReasoningEffort,
		&l.RequestBody, &l.RequestHeaders, &l.CacheStatus,
	)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "log detail not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"log":     l,
	})
}

// handleAdminLogAccounts handles GET /api/admin/logs/accounts
func (h *APIHandler) handleAdminLogAccounts(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	accounts := h.getLogAccounts()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"accounts": accounts,
	})
}

// getLogAccounts collects all registered usernames and accounts in request_logs
func (h *APIHandler) getLogAccounts() []string {
	accSet := make(map[string]bool)

	// 1. Registered relay users
	if h.authMgr != nil && h.authMgr.userMgr != nil {
		for _, u := range h.authMgr.userMgr.GetUsers() {
			if u.Key != "" {
				accSet[u.Key] = true
			}
		}
	}

	// 2. Distinct accounts from request_logs (完整纳入全部号池成员账号与上游账号)
	if db.GlobalDB != nil {
		if rows, err := db.GlobalDB.Query("SELECT DISTINCT user_id FROM request_logs WHERE user_id <> '' ORDER BY user_id ASC LIMIT 200"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var uid string
				if err := rows.Scan(&uid); err == nil && uid != "" {
					accSet[uid] = true
				}
			}
		}
	}

	var list []string
	for acc := range accSet {
		list = append(list, acc)
	}
	sort.Strings(list)
	return list
}
