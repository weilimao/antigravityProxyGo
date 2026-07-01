package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
)

type APIHandler struct {
	authMgr    *AuthManager
	statsMgr   *StatsTracker
	packageMgr *PackageManager
	logFn      func(string)
	caCertPath string // 服务器 CA 证书路径，供远程客户端下载
}

func compareQuotas(q1, q2 UserQuotas) bool {
	checkFamily := func(f1, f2 ModelQuota) bool {
		return f1.EnableFixed == f2.EnableFixed &&
			f1.FixedTokens == f2.FixedTokens &&
			f1.EnableHourly == f2.EnableHourly &&
			f1.HourlyHours == f2.HourlyHours &&
			f1.HourlyTokens == f2.HourlyTokens &&
			f1.EnableDaily == f2.EnableDaily &&
			f1.DailyDays == f2.DailyDays &&
			f1.DailyTokens == f2.DailyTokens
	}
	rl1 := q1.RateLimit
	if rl1 <= 0 {
		rl1 = 30
	}
	rl2 := q2.RateLimit
	if rl2 <= 0 {
		rl2 = 30
	}
	return checkFamily(q1.Gemini, q2.Gemini) &&
		checkFamily(q1.Claude, q2.Claude) &&
		q1.ValidDuration == q2.ValidDuration &&
		q1.ValidUnit == q2.ValidUnit &&
		rl1 == rl2
}

func NewAPIHandler(authMgr *AuthManager, statsMgr *StatsTracker, packageMgr *PackageManager, logFn func(string), caCertPath string) *APIHandler {
	return &APIHandler{
		authMgr:    authMgr,
		statsMgr:   statsMgr,
		packageMgr: packageMgr,
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
	case path == "/api/trends" && r.Method == http.MethodGet:
		h.handleTrends(w, r)
	case path == "/api/logs/sync" && r.Method == http.MethodGet:
		h.handleLogsSync(w, r)
	case path == "/api/logs/detail" && r.Method == http.MethodGet:
		h.handleLogDetail(w, r)
	case path == "/api/cert" && r.Method == http.MethodGet:
		h.handleCert(w, r)
	case path == "/api/keys" && r.Method == http.MethodGet:
		h.handleGetAPIKeys(w, r)
	case path == "/api/keys" && r.Method == http.MethodPost:
		h.handleCreateAPIKey(w, r)
	case path == "/api/keys/update-quota" && r.Method == http.MethodPost:
		h.handleUpdateAPIKeyQuota(w, r)
	case strings.HasPrefix(path, "/api/keys/") && r.Method == http.MethodDelete:
		h.handleDeleteAPIKey(w, r)
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

func (h *APIHandler) handleGetAPIKeys(w http.ResponseWriter, r *http.Request) {
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
	user := h.authMgr.userMgr.GetUserByID(session.UserID)
	if user == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "user not found"})
		return
	}
	keys := user.APIKeys
	if keys == nil {
		keys = make([]UserAPIKey, 0)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"keys":    keys,
	})
}

func (h *APIHandler) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request"})
		return
	}
	if req.Name == "" {
		req.Name = "Default Key"
	}
	newKey, err := h.authMgr.userMgr.CreateAPIKey(session.UserID, req.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"key":     newKey,
	})
}

func (h *APIHandler) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
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
	keyID := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if keyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing key id"})
		return
	}
	err = h.authMgr.userMgr.DeleteAPIKey(session.UserID, keyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func (h *APIHandler) handleUpdateAPIKeyQuota(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		ID                string `json:"id"`
		LimitGeminiTokens int64  `json:"limitGeminiTokens"`
		LimitClaudeTokens int64  `json:"limitClaudeTokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing key id"})
		return
	}
	err = h.authMgr.userMgr.UpdateAPIKeyQuota(session.UserID, req.ID, req.LimitGeminiTokens, req.LimitClaudeTokens)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
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
		
		packageName := "自定义套餐"
		isGeminiDisabled := !user.Quotas.Gemini.EnableFixed && !user.Quotas.Gemini.EnableHourly && !user.Quotas.Gemini.EnableDaily
		isClaudeDisabled := !user.Quotas.Claude.EnableFixed && !user.Quotas.Claude.EnableHourly && !user.Quotas.Claude.EnableDaily
		if isGeminiDisabled && isClaudeDisabled {
			packageName = "无访问权限"
		}
		if h.packageMgr != nil {
			pkgs := h.packageMgr.GetPackages()
			for _, pkg := range pkgs {
				if compareQuotas(user.Quotas, pkg.Quotas) {
					packageName = pkg.Name
					break
				}
			}
		}
		statsCopy.PackageName = packageName
		
		usage := make(map[string]int64)
		resetAt := make(map[string]string)
		
		// For Gemini quotas
		if user.Quotas.Gemini.EnableHourly && user.Quotas.Gemini.HourlyHours > 0 {
			windowStart, windowEnd := FixedWindowBounds(user.Quotas.Gemini.HourlyHours)
			since := windowStart.Format(time.RFC3339)
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "gemini", since); err == nil {
				usage["gemini_hourly"] = u
			}
			resetAt["gemini_hourly"] = windowEnd.Format(time.RFC3339)
		}
		if user.Quotas.Gemini.EnableDaily && user.Quotas.Gemini.DailyDays > 0 {
			windowStart, windowEnd := FixedWindowBounds(user.Quotas.Gemini.DailyDays * 24)
			since := windowStart.Format(time.RFC3339)
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "gemini", since); err == nil {
				usage["gemini_daily"] = u
			}
			resetAt["gemini_daily"] = windowEnd.Format(time.RFC3339)
		}
		if user.Quotas.Gemini.EnableFixed {
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "gemini", "1970-01-01T00:00:00Z"); err == nil {
				usage["gemini_fixed"] = u
			}
		}

		// For Claude quotas
		if user.Quotas.Claude.EnableHourly && user.Quotas.Claude.HourlyHours > 0 {
			windowStart, windowEnd := FixedWindowBounds(user.Quotas.Claude.HourlyHours)
			since := windowStart.Format(time.RFC3339)
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "claude", since); err == nil {
				usage["claude_hourly"] = u
			}
			resetAt["claude_hourly"] = windowEnd.Format(time.RFC3339)
		}
		if user.Quotas.Claude.EnableDaily && user.Quotas.Claude.DailyDays > 0 {
			windowStart, windowEnd := FixedWindowBounds(user.Quotas.Claude.DailyDays * 24)
			since := windowStart.Format(time.RFC3339)
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "claude", since); err == nil {
				usage["claude_daily"] = u
			}
			resetAt["claude_daily"] = windowEnd.Format(time.RFC3339)
		}
		if user.Quotas.Claude.EnableFixed {
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "claude", "1970-01-01T00:00:00Z"); err == nil {
				usage["claude_fixed"] = u
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
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "cert path not configured"})
		return
	}

	data, err := os.ReadFile(h.caCertPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to read cert file: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", "attachment; filename=antigravity-ca.crt")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1]
	}
	return ""
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (h *APIHandler) handleLogsSync(w http.ResponseWriter, r *http.Request) {
	// Deprecated: return empty logs to save network bandwidth and SQLite workload
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":  []*db.RequestLog{},
		"maxId": int64(0),
	})
}

func (h *APIHandler) handleTrends(w http.ResponseWriter, r *http.Request) {
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

	trends, err := db.GetUserHourlyTrends(session.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "Failed to query trends: " + err.Error()})
		return
	}

	if trends == nil {
		trends = []*db.HourlyTrendSummary{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"trends": trends,
	})
}

func (h *APIHandler) handleLogDetail(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing token"})
		return
	}

	_, err := h.authMgr.ValidateToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}

	reqID := r.URL.Query().Get("req_id")
	if reqID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing req_id"})
		return
	}

	log := h.statsMgr.GetCachedLog(reqID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"log": log,
	})
}
