package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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
	writeJSON(w, http.StatusOK, stats)
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
