package relay

import (
	"fmt"
	"net/http"
	"strings"

	"antigravity-proxy/internal/settings"
)

// handleGetModelMapping 导出远端服务器当前配置的模型映射表
func (h *APIHandler) handleGetModelMapping(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized: missing token"})
		return
	}
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	if h.settingsMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"mappings": []settings.ModelMappingEntry{},
		})
		return
	}

	mappings := h.settingsMgr.GetRelayModelMapping()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"mappings": mappings,
	})
}

// handleSetModelMapping 接收客户端提交的模型映射表并实时落盘生效
func (h *APIHandler) handleSetModelMapping(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized: missing token"})
		return
	}
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	if h.settingsMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "settings manager not initialized"})
		return
	}

	var req struct {
		Mappings []settings.ModelMappingEntry `json:"mappings"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("invalid request payload: %v", err),
		})
		return
	}

	if err := h.settingsMgr.SetRelayModelMapping(req.Mappings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("failed to save model mappings: %v", err),
		})
		return
	}

	h.log("✅ 远程客户端更新模型映射成功，当前共 %d 项", len(req.Mappings))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"count":   len(req.Mappings),
	})
}

// handleGetUserAutoConfig 导出当前登录用户账号专属的 Auto 竞速配置
func (h *APIHandler) handleGetUserAutoConfig(w http.ResponseWriter, r *http.Request) {
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

	// 1. 若中继用户管理器已初始化，优先查找当前用户私有配置
	if h.authMgr != nil && h.authMgr.userMgr != nil {
		user := h.authMgr.userMgr.GetUserByID(session.UserID)
		if user != nil && user.AutoConfig != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"isUser":  true,
				"config":  user.AutoConfig,
				"userKey": user.Key,
			})
			return
		}
	}

	// 2. 若用户未定制专属配置，从全局设置管理器中提取默认 auto 映射项进行回显
	defaultCfg := UserAutoConfig{
		Enabled:          false,
		CandidateModels:  []string{},
		UseBenchmarkPool: false,
	}
	if h.settingsMgr != nil {
		for _, entry := range h.settingsMgr.GetRelayModelMapping() {
			if strings.EqualFold(strings.TrimSpace(entry.ClientModel), "auto") {
				defaultCfg.Enabled = entry.Expose
				defaultCfg.CandidateModels = entry.CandidateModels
				defaultCfg.UseBenchmarkPool = entry.IsUseBenchmarkPool()
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"isUser":  false,
		"config":  defaultCfg,
		"userKey": session.UserKey,
	})
}

// handleSetUserAutoConfig 接收当前登录用户提交的专属 Auto 竞速配置并落盘
func (h *APIHandler) handleSetUserAutoConfig(w http.ResponseWriter, r *http.Request) {
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
		Config UserAutoConfig `json:"config"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("invalid request payload: %v", err),
		})
		return
	}

	if h.authMgr == nil || h.authMgr.userMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "user manager not initialized",
		})
		return
	}

	if err := h.authMgr.userMgr.UpdateUserAutoConfig(session.UserID, req.Config); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("failed to save user auto config: %v", err),
		})
		return
	}

	h.log("✅ 中继用户 %s (ID: %s) 成功更新个人专属 Auto 竞速配置 (启用: %v, 候选数: %d)",
		session.UserKey, session.UserID, req.Config.Enabled, len(req.Config.CandidateModels))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"config":  req.Config,
		"userKey": session.UserKey,
	})
}
