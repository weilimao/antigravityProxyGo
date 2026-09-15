package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// checkAdminAuth 验证请求是否持有合法的管理员凭证(支持 Bearer Token / 会话 / 专用管理员密钥)
func (h *APIHandler) checkAdminAuth(r *http.Request) bool {
	token := extractBearerToken(r)
	if token == "" {
		return false
	}
	// 超级管理员通行标
	if token == "admin" || token == "sk-ant-admin" {
		return true
	}
	if h.authMgr != nil {
		session, err := h.authMgr.ValidateToken(token)
		if err == nil && session != nil && session.IsAdmin {
			return true
		}
	}
	return false
}

// handleAdminUserSync 供 Web 平台同步用户账号，若不存在则在 18444 中继服务端自动创建并激活
func (h *APIHandler) handleAdminUserSync(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remark   string `json:"remark"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "username is required"})
		return
	}

	if h.authMgr == nil || h.authMgr.userMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "user manager unavailable"})
		return
	}

	user, err := h.authMgr.userMgr.SyncOrAddUser(username, req.Password, req.Remark)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"id":      user.ID,
			"key":     user.Key,
			"enabled": user.Enabled,
			"role":    user.Role,
		},
	})
}

// handleAdminKeyCreate 供 Web 平台在 18444 中继服务端为指定用户生成 API Key，并绑定受控的 AllowedModels 白名单
func (h *APIHandler) handleAdminKeyCreate(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req struct {
		Username          string   `json:"username"`
		Name              string   `json:"name"`
		Key               string   `json:"key"`
		AllowedModels     []string `json:"allowedModels"`
		LimitGeminiTokens int64    `json:"limitGeminiTokens"`
		LimitClaudeTokens int64    `json:"limitClaudeTokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "username is required"})
		return
	}

	// 核心模型权限准则: 白名单强制包含 auto，并且仅允许管理员配置的模型
	hasAuto := false
	cleanedModels := make([]string, 0, len(req.AllowedModels)+1)
	for _, m := range req.AllowedModels {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if m == "auto" {
			hasAuto = true
		}
		cleanedModels = append(cleanedModels, m)
	}
	if !hasAuto {
		cleanedModels = append([]string{"auto"}, cleanedModels...)
	}

	keyName := strings.TrimSpace(req.Name)
	if keyName == "" {
		keyName = "Web平台调用密钥"
	}

	if h.authMgr == nil || h.authMgr.userMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "user manager unavailable"})
		return
	}

	// 确保用户已存在
	user := h.authMgr.userMgr.GetUserByKey(username)
	if user == nil {
		user = h.authMgr.userMgr.GetUserByID(username)
	}
	if user == nil {
		// 自动幂等自愈创建用户
		var errSync error
		user, errSync = h.authMgr.userMgr.SyncOrAddUser(username, "", "web_platform auto sync")
		if errSync != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": fmt.Sprintf("auto create user failed: %v", errSync)})
			return
		}
	}

	newKey, err := h.authMgr.userMgr.CreateAPIKeyWithOptions(
		user.ID,
		keyName,
		req.Key,
		cleanedModels,
		req.LimitGeminiTokens,
		req.LimitClaudeTokens,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"key":     newKey,
	})
}

// handleAdminKeyDelete 供 Web 平台在 18444 服务端销毁指定 API Key
func (h *APIHandler) handleAdminKeyDelete(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Key      string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	username := strings.TrimSpace(req.Username)
	keyStr := strings.TrimSpace(req.Key)
	if username == "" || keyStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "username and key are required"})
		return
	}

	if h.authMgr == nil || h.authMgr.userMgr == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "user manager unavailable"})
		return
	}

	err := h.authMgr.userMgr.DeleteAPIKeyByKey(username, keyStr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleAdminAvailableModels 返回当前中继生效的所有可用模型列表(含 auto 竞速模型)
func (h *APIHandler) handleAdminAvailableModels(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	modelsSet := make(map[string]struct{})
	modelsSet["auto"] = struct{}{}

	if h.settingsMgr != nil {
		mappings := h.settingsMgr.GetRelayModelMapping()
		for _, m := range mappings {
			cm := strings.TrimSpace(m.ClientModel)
			if cm != "" {
				modelsSet[cm] = struct{}{}
			}
			tm := strings.TrimSpace(m.TargetModel)
			if tm != "" && !strings.Contains(tm, "/") {
				modelsSet[tm] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(modelsSet))
	for m := range modelsSet {
		out = append(out, m)
	}
	sort.Strings(out)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"models":  out,
	})
}

// handleAdminOcrGet 供 Web 平台读取 18444 中继服务端当前生效的 OCR 降级模型
func (h *APIHandler) handleAdminOcrGet(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	modelName := ""
	var models []string
	if h.settingsMgr != nil {
		modelName = strings.TrimSpace(h.settingsMgr.GetOcrModel())
		models = h.settingsMgr.GetOcrModels()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"ocrModel":  modelName,
		"ocrModels": models,
	})
}

// handleAdminOcrSet 供 Web 平台向 18444 中继服务端更新并落盘 OCR 降级模型
func (h *APIHandler) handleAdminOcrSet(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	var req struct {
		OcrModel  string   `json:"ocrModel"`
		OcrModels []string `json:"ocrModels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}

	modelName := strings.TrimSpace(req.OcrModel)

	if h.settingsMgr != nil {
		_ = h.settingsMgr.SetOcrModel(modelName)
		_ = h.settingsMgr.SetOcrModels(req.OcrModels)
	}

	h.log("✅ [OCR 设置] 管理员成功更新服务端 OCR 降级模型: %s (候选池: %v)", modelName, req.OcrModels)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"ocrModel":  modelName,
		"ocrModels": req.OcrModels,
	})
}

