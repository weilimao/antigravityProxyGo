package relay

import (
	"encoding/json"
	"net/http"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"
)

type BenchmarkScheduler interface {
	RunNow()
	RunModelNow(model string)
	IsRunning() bool
	LastRun() time.Time
	PendingModels() []string
	ResetLastRun()
}

// handleAdminBenchmarkGet 供 Web 平台读取 18444 中继服务端当前生效的测速配置与结果
func (h *APIHandler) handleAdminBenchmarkGet(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	cfg := settings.BenchmarkConfig{}
	if h.settingsMgr != nil {
		cfg = h.settingsMgr.GetBenchmarkConfig()
	}
	results, _ := db.ListBenchmarkResults()
	
	lastRun := time.Time{}
	running := false
	var pendingModels []string
	if h.benchmarkScheduler != nil {
		lastRun = h.benchmarkScheduler.LastRun()
		running = h.benchmarkScheduler.IsRunning()
		pendingModels = h.benchmarkScheduler.PendingModels()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"config": map[string]interface{}{
			"enabled":         cfg.Enabled,
			"models":          cfg.Models,
			"intervalMinutes": cfg.IntervalMinutes,
			"prompt":          cfg.Prompt,
			"timeoutMs":       cfg.TimeoutMs,
		},
		"results":       results,
		"pendingModels": pendingModels,
		"lastRun":       lastRun.Format(time.RFC3339),
		"running":       running,
	})
}

// handleAdminBenchmarkSetConfig 供 Web 平台更新 18444 中继服务端的测速配置
func (h *APIHandler) handleAdminBenchmarkSetConfig(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req settings.BenchmarkConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	if h.settingsMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "settings manager not initialized"})
		return
	}

	if err := h.settingsMgr.SetBenchmarkConfig(req); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	if h.benchmarkScheduler != nil {
		h.benchmarkScheduler.ResetLastRun()
	}

	cfg := h.settingsMgr.GetBenchmarkConfig()
	h.log("✅ [测速] 管理员更新测速配置: %d 个模型", len(cfg.Models))
	
	_ = db.PruneBenchmarkResults(cfg.Models)
	
	if len(cfg.Models) > 0 && h.benchmarkScheduler != nil {
		go h.benchmarkScheduler.RunNow()
	} else if len(cfg.Models) == 0 {
		_ = db.ClearBenchmarkResults()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"config": map[string]interface{}{
			"enabled":         cfg.Enabled,
			"models":          cfg.Models,
			"intervalMinutes": cfg.IntervalMinutes,
			"prompt":          cfg.Prompt,
			"timeoutMs":       cfg.TimeoutMs,
		},
	})
}

// handleAdminBenchmarkRun 供 Web 平台手动触发全部模型测速
func (h *APIHandler) handleAdminBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	if h.benchmarkScheduler == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "benchmark scheduler not running"})
		return
	}

	go h.benchmarkScheduler.RunNow()
	h.log("⚡ [测速] Web管理端手动触发一轮模型测速")
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleAdminBenchmarkRunModel 供 Web 平台手动触发单个模型测速
func (h *APIHandler) handleAdminBenchmarkRunModel(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	if req.Model == "" || h.benchmarkScheduler == nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "model empty or scheduler not running"})
		return
	}

	go h.benchmarkScheduler.RunModelNow(req.Model)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleAdminBenchmarkModels 供 Web 平台获取可用候选模型池
func (h *APIHandler) handleAdminBenchmarkModels(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	seen := make(map[string]bool)
	var out []string

	if h.settingsMgr != nil {
		mapping := h.settingsMgr.GetRelayModelMapping()
		for _, e := range mapping {
			if !e.Expose {
				continue
			}
			m := e.ClientModel
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"models":  out,
	})
}
