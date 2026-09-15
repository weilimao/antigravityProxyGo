package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"
	internalstats "antigravity-proxy/internal/stats"
)

type APIHandler struct {
	authMgr            *AuthManager
	statsMgr           *StatsTracker
	packageMgr         *PackageManager
	logFn              func(string)
	caCertPath         string // 服务器 CA 证书路径，供远程客户端下载
	caCertProvider     func() ([]byte, error)
	loginLimiter       *RateLimiter
	settingsMgr        settings.ManagerInterface
	accountMgr         *account.Manager
	dataDir            string
	onSyncReload       func(string)
	globalStatsTracker *internalstats.Tracker
	platformRouter     http.Handler
	benchmarkScheduler BenchmarkScheduler
}

func (h *APIHandler) SetCACertProvider(fn func() ([]byte, error)) {
	h.caCertProvider = fn
}

func (h *APIHandler) SetGlobalStatsTracker(t *internalstats.Tracker) {
	h.globalStatsTracker = t
}

func (h *APIHandler) SetDataDir(dir string) {
	h.dataDir = dir
}

func (h *APIHandler) SetOnSyncReload(fn func(string)) {
	h.onSyncReload = fn
}

func (h *APIHandler) SetPlatformRouter(router http.Handler) {
	h.platformRouter = router
}

func (h *APIHandler) SetBenchmarkScheduler(b BenchmarkScheduler) {
	h.benchmarkScheduler = b
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

func NewAPIHandler(authMgr *AuthManager, statsMgr *StatsTracker, packageMgr *PackageManager, logFn func(string), caCertPath string, settingsMgr settings.ManagerInterface, accountMgr *account.Manager) *APIHandler {
	if logFn == nil {
		logFn = func(string) {}
	}
	h := &APIHandler{
		authMgr:      authMgr,
		statsMgr:     statsMgr,
		packageMgr:   packageMgr,
		logFn:        logFn,
		caCertPath:   caCertPath,
		loginLimiter: NewRateLimiter(),
		settingsMgr:  settingsMgr,
		accountMgr:   accountMgr,
	}
	return h
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
	case path == "/api/keys/models" && r.Method == http.MethodGet:
		h.handleGetAPIKeyModels(w, r)
	case strings.HasPrefix(path, "/api/keys/") && r.Method == http.MethodDelete:
		h.handleDeleteAPIKey(w, r)
	case path == "/api/models/mapping" && r.Method == http.MethodGet:
		h.handleGetModelMapping(w, r)
	case path == "/api/models/mapping" && r.Method == http.MethodPost:
		h.handleSetModelMapping(w, r)
	case path == "/api/models/auto-config" && r.Method == http.MethodGet:
		h.handleGetUserAutoConfig(w, r)
	case path == "/api/models/auto-config" && r.Method == http.MethodPost:
		h.handleSetUserAutoConfig(w, r)
	case path == "/api/admin/users/sync" && r.Method == http.MethodPost:
		h.handleAdminUserSync(w, r)
	case path == "/api/admin/keys/create" && r.Method == http.MethodPost:
		h.handleAdminKeyCreate(w, r)
	case path == "/api/admin/keys/delete" && (r.Method == http.MethodDelete || r.Method == http.MethodPost):
		h.handleAdminKeyDelete(w, r)
	case path == "/api/admin/models/available" && r.Method == http.MethodGet:
		h.handleAdminAvailableModels(w, r)
	case path == "/api/admin/models/other-groups" && r.Method == http.MethodGet:
		h.handleAdminOtherGroups(w, r)
	case path == "/api/admin/models/fetch-channel" && (r.Method == http.MethodPost || r.Method == http.MethodGet):
		h.handleAdminFetchChannelModels(w, r)
	case path == "/api/admin/models/fetch-other" && r.Method == http.MethodPost:
		h.handleAdminFetchOtherGroupModels(w, r)
	case path == "/api/admin/settings/ocr" && r.Method == http.MethodGet:
		h.handleAdminOcrGet(w, r)
	case path == "/api/admin/settings/ocr" && r.Method == http.MethodPost:
		h.handleAdminOcrSet(w, r)
	case path == "/api/admin/benchmark" && r.Method == http.MethodGet:
		h.handleAdminBenchmarkGet(w, r)
	case path == "/api/admin/benchmark/config" && r.Method == http.MethodPost:
		h.handleAdminBenchmarkSetConfig(w, r)
	case path == "/api/admin/benchmark/run" && r.Method == http.MethodPost:
		h.handleAdminBenchmarkRun(w, r)
	case path == "/api/admin/benchmark/run-model" && r.Method == http.MethodPost:
		h.handleAdminBenchmarkRunModel(w, r)
	case path == "/api/admin/benchmark/models" && r.Method == http.MethodGet:
		h.handleAdminBenchmarkModels(w, r)
	case path == "/api/sync/full" && r.Method == http.MethodGet:
		h.handleSyncFull(w, r)
	case path == "/api/sync/push" && r.Method == http.MethodPost:
		h.handleSyncPush(w, r)
	case strings.HasPrefix(path, "/api/v1/"):
		if h.platformRouter != nil {
			h.platformRouter.ServeHTTP(w, r)
			return
		}
		fallthrough
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
	// IP-based login rate limiting: max 5 attempts per minute per IP
	clientIP := r.RemoteAddr
	if idx := strings.LastIndex(clientIP, ":"); idx != -1 {
		clientIP = clientIP[:idx]
	}
	if !h.loginLimiter.Allow("login_ip:"+clientIP, 5) {
		h.log("Login rate limited for IP=%s", clientIP)
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"success": false,
			"error":   "too many login attempts, please try again later",
		})
		return
	}

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
		h.log("Login failed for IP=%s: %v", clientIP, err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	h.log("Login succeeded for key=%s userId=%s isAdmin=%v", req.Key, session.UserID, session.IsAdmin)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"token":     session.Token,
		"isAdmin":   session.IsAdmin,
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
		ID                string   `json:"id"`
		LimitGeminiTokens int64    `json:"limitGeminiTokens"`
		LimitClaudeTokens int64    `json:"limitClaudeTokens"`
		AllowedModels     []string `json:"allowedModels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing key id"})
		return
	}
	err = h.authMgr.userMgr.UpdateAPIKeyQuota(session.UserID, req.ID, req.LimitGeminiTokens, req.LimitClaudeTokens, req.AllowedModels)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleGetAPIKeyModels 返回当前中继对外暴露的模型清单(模型映射里 Expose==true 的
// ClientModel 去重排序),供前端在编辑 API Key 授权模型时作为可选候选下拉。
func (h *APIHandler) handleGetAPIKeyModels(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing token"})
		return
	}
	if _, err := h.authMgr.ValidateToken(token); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	var mapping []settings.ModelMappingEntry
	if h.settingsMgr != nil {
		mapping = h.settingsMgr.GetRelayModelMapping()
	}
	seen := make(map[string]bool)
	var models []string
	for _, e := range mapping {
		if !e.Expose {
			continue
		}
		if seen[e.ClientModel] {
			continue
		}
		seen[e.ClientModel] = true
		models = append(models, e.ClientModel)
	}
	sort.Strings(models)
	if models == nil {
		models = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"models":  models,
	})
}

// writeModelNotAuthorized 写 403 模型未授权响应,供各号池 handler 在
// IsModelAuthorizedForAPIKey 校验失败时统一回写(OpenAI/Anthropic 客户端均可识别)。
func writeModelNotAuthorized(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusForbidden, map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "model_not_authorized",
			"message": fmt.Sprintf("model %q is not authorized for this API key", model),
		},
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

	uStats := h.statsMgr.GetUserStats(session.UserID)
	if uStats == nil {
		uStats = &RelayUserStats{
			UserID:  session.UserID,
			UserKey: session.UserKey,
			Models:  make(map[string]*RelayModelStats),
		}
	}

	// Make a shallow copy to inject quotas without mutating memory stats
	statsCopy := *uStats

	// 若当前调用者具备管理员权限且单用户中继统计无独立消费时，
	// 优先从服务器全局 stats.Tracker 注入整机运行大盘统计，确保管理端面板展示真实大盘
	if session.IsAdmin && h.globalStatsTracker != nil && statsCopy.TotalRequests == 0 {
		globalPayload := h.globalStatsTracker.GetPayload(nil)
		if gStats, ok := globalPayload["stats"].(internalstats.GlobalStats); ok {
			statsCopy.TotalRequests = gStats.TotalRequests
			statsCopy.TotalInputTokens = gStats.TotalInputTokens
			statsCopy.TotalOutputTokens = gStats.TotalOutputTokens
			statsCopy.TotalCachedTokens = gStats.TotalCachedTokens
			statsCopy.TotalCacheEligibleInputTokens = gStats.TotalCacheEligibleInputTokens
			statsCopy.TotalCost = gStats.TotalCost
			if statsCopy.Models == nil {
				statsCopy.Models = make(map[string]*RelayModelStats)
			}
			for mName, mStat := range gStats.Models {
				if mStat != nil {
					statsCopy.Models[mName] = &RelayModelStats{
						Model:        mName,
						RequestCount: mStat.Reqs,
						InputTokens:  mStat.InTokens,
						OutputTokens: mStat.OutTokens,
						CachedTokens: mStat.CachedTokens,
						TotalCost:    mStat.Cost,
					}
				}
			}
		}
	}

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
			if u, resetStr, err := GetActiveWindow(session.UserID, "gemini", "gemini_hourly", user.Quotas.Gemini.HourlyHours, false); err == nil {
				usage["gemini_hourly"] = u
				if resetStr != "" {
					resetAt["gemini_hourly"] = resetStr
				}
			}
		}
		if user.Quotas.Gemini.EnableDaily && user.Quotas.Gemini.DailyDays > 0 {
			if u, resetStr, err := GetActiveWindow(session.UserID, "gemini", "gemini_daily", user.Quotas.Gemini.DailyDays*24, false); err == nil {
				usage["gemini_daily"] = u
				if resetStr != "" {
					resetAt["gemini_daily"] = resetStr
				}
			}
		}
		if user.Quotas.Gemini.EnableFixed {
			if u, err := db.GetTokensForUserModelFamilySince(session.UserID, "gemini", "1970-01-01T00:00:00Z"); err == nil {
				usage["gemini_fixed"] = u
			}
		}

		// For Claude quotas
		if user.Quotas.Claude.EnableHourly && user.Quotas.Claude.HourlyHours > 0 {
			if u, resetStr, err := GetActiveWindow(session.UserID, "claude", "claude_hourly", user.Quotas.Claude.HourlyHours, false); err == nil {
				usage["claude_hourly"] = u
				if resetStr != "" {
					resetAt["claude_hourly"] = resetStr
				}
			}
		}
		if user.Quotas.Claude.EnableDaily && user.Quotas.Claude.DailyDays > 0 {
			if u, resetStr, err := GetActiveWindow(session.UserID, "claude", "claude_daily", user.Quotas.Claude.DailyDays*24, false); err == nil {
				usage["claude_daily"] = u
				if resetStr != "" {
					resetAt["claude_daily"] = resetStr
				}
			}
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

func (h *APIHandler) handleCert(w http.ResponseWriter, r *http.Request) {
	// Require authentication to download CA certificate
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing token"})
		return
	}
	if _, err := h.authMgr.ValidateToken(token); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}

	var data []byte
	var err error
	if h.caCertPath != "" {
		data, err = os.ReadFile(h.caCertPath)
	}
	if (err != nil || len(data) == 0) && h.caCertProvider != nil {
		data, err = h.caCertProvider()
	}

	if err != nil || len(data) == 0 {
		errMsg := "cert file not available"
		if err != nil {
			errMsg = err.Error()
		}
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to read cert file: " + errMsg})
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
