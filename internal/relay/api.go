package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
)

type APIHandler struct {
	authMgr    *AuthManager
	statsMgr   *StatsTracker
	logFn      func(string)
	caCertPath string // 服务器 CA 证书路径，供远程客户端下载
}

func NewAPIHandler(authMgr *AuthManager, statsMgr *StatsTracker, logFn func(string), caCertPath string) *APIHandler {
	return &APIHandler{
		authMgr:    authMgr,
		statsMgr:   statsMgr,
		logFn:      logFn,
		caCertPath: caCertPath,
	}
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/api/health" && r.Method == http.MethodGet:
		h.handleHealth(w, r)
	case path == "/api/auth/login" && r.Method == http.MethodPost:
		h.handleLogin(w, r)
	case path == "/api/auth/logout" && r.Method == http.MethodPost:
		h.handleLogout(w, r)
	case path == "/api/stats" && r.Method == http.MethodGet:
		h.handleStats(w, r)
	case path == "/api/logs/sync" && r.Method == http.MethodGet:
		h.handleLogsSync(w, r)
	case path == "/api/cert" && r.Method == http.MethodGet:
		h.handleCert(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "not found",
		})
	}
}

func (h *APIHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
	})
}

func (h *APIHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string `json:"key"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	session, err := h.authMgr.Login(req.Key, req.Password)
	if err != nil {
		h.log("Login failed for key=%s: %v", req.Key, err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	h.log("Login succeeded for key=%s userId=%s", req.Key, session.UserID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"token":     session.Token,
		"expiresAt": session.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *APIHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "missing token",
		})
		return
	}

	h.authMgr.Logout(token)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func (h *APIHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "missing token",
		})
		return
	}

	session, err := h.authMgr.ValidateToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	stats := h.statsMgr.GetUserStats(session.UserID)
	if stats == nil {
		stats = &RelayUserStats{
			UserID:  session.UserID,
			UserKey: session.UserKey,
			Models:  make(map[string]*RelayModelStats),
		}
	}

	// Make a shallow copy to inject quotas without mutating memory stats
	statsCopy := *stats
	
	user := h.authMgr.userMgr.GetUserByID(session.UserID)
	if user != nil {
		statsCopy.Quotas = user.Quotas
		
		usage := make(map[string]int64)
		resetAt := make(map[string]string)
		
		// For Gemini quotas
		if user.Quotas.Gemini.EnableHourly && user.Quotas.Gemini.HourlyHours > 0 {
			since := time.Now().Add(-time.Duration(user.Quotas.Gemini.HourlyHours) * time.Hour).Format(time.RFC3339)
			tokens, _ := db.GetTokensForUserModelFamilySince(user.ID, "gemini", since)
			usage["gemini_hourly"] = tokens
			if tokens > 0 {
				if firstTs, err := db.GetOldestRequestTimestampSince(user.ID, "gemini", since); err == nil && firstTs != "" {
					if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
						resetAt["gemini_hourly"] = parsed.Add(time.Duration(user.Quotas.Gemini.HourlyHours) * time.Hour).Format(time.RFC3339)
					}
				}
			}
		}
		if user.Quotas.Gemini.EnableDaily && user.Quotas.Gemini.DailyDays > 0 {
			since := time.Now().Add(-time.Duration(user.Quotas.Gemini.DailyDays*24) * time.Hour).Format(time.RFC3339)
			tokens, _ := db.GetTokensForUserModelFamilySince(user.ID, "gemini", since)
			usage["gemini_daily"] = tokens
			if tokens > 0 {
				if firstTs, err := db.GetOldestRequestTimestampSince(user.ID, "gemini", since); err == nil && firstTs != "" {
					if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
						resetAt["gemini_daily"] = parsed.Add(time.Duration(user.Quotas.Gemini.DailyDays*24) * time.Hour).Format(time.RFC3339)
					}
				}
			}
		}
		
		// For Claude quotas
		if user.Quotas.Claude.EnableHourly && user.Quotas.Claude.HourlyHours > 0 {
			since := time.Now().Add(-time.Duration(user.Quotas.Claude.HourlyHours) * time.Hour).Format(time.RFC3339)
			tokens, _ := db.GetTokensForUserModelFamilySince(user.ID, "claude", since)
			usage["claude_hourly"] = tokens
			if tokens > 0 {
				if firstTs, err := db.GetOldestRequestTimestampSince(user.ID, "claude", since); err == nil && firstTs != "" {
					if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
						resetAt["claude_hourly"] = parsed.Add(time.Duration(user.Quotas.Claude.HourlyHours) * time.Hour).Format(time.RFC3339)
					}
				}
			}
		}
		if user.Quotas.Claude.EnableDaily && user.Quotas.Claude.DailyDays > 0 {
			since := time.Now().Add(-time.Duration(user.Quotas.Claude.DailyDays*24) * time.Hour).Format(time.RFC3339)
			tokens, _ := db.GetTokensForUserModelFamilySince(user.ID, "claude", since)
			usage["claude_daily"] = tokens
			if tokens > 0 {
				if firstTs, err := db.GetOldestRequestTimestampSince(user.ID, "claude", since); err == nil && firstTs != "" {
					if parsed, err := time.Parse(time.RFC3339, firstTs); err == nil {
						resetAt["claude_daily"] = parsed.Add(time.Duration(user.Quotas.Claude.DailyDays*24) * time.Hour).Format(time.RFC3339)
					}
				}
			}
		}
		
		statsCopy.CurrentUsage = usage
		statsCopy.ResetAt = resetAt
	}

	writeJSON(w, http.StatusOK, &statsCopy)
}

func (h *APIHandler) log(format string, args ...interface{}) {
	if h.logFn != nil {
		h.logFn(fmt.Sprintf("[RelayAPI] "+format, args...))
	}
}

func (h *APIHandler) handleCert(w http.ResponseWriter, _ *http.Request) {
	if h.caCertPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "CA certificate path not configured",
		})
		return
	}

	data, err := os.ReadFile(h.caCertPath)
	if err != nil {
		h.log("Failed to read CA cert: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "failed to read CA certificate",
		})
		return
	}

	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=\"antigravity-relay-ca.pem\"")
	w.WriteHeader(http.StatusOK)
	h.log("Successfully served CA certificate to remote client")
	_, _ = w.Write(data)
}

func extractBearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		fmt.Printf("[RelayAPI] Failed to write JSON response: %v\n", err)
	}
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (h *APIHandler) handleLogsSync(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing token"})
		return
	}

	session, err := h.authMgr.ValidateToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}

	lastIDStr := r.URL.Query().Get("last_id")
	limitStr := r.URL.Query().Get("limit")
	
	var lastID int64 = 0
	if lastIDStr != "" {
		lastID, _ = strconv.ParseInt(lastIDStr, 10, 64)
	}
	
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	logs, err := db.GetRequestLogsSince(session.UserID, "remote", lastID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "Failed to query logs: " + err.Error()})
		return
	}

	if logs == nil {
		logs = []*db.RequestLog{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs": logs,
	})
}
