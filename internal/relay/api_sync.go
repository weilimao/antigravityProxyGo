package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// allowedSyncFiles 云端与客户端之间允许安全同步的文件白名单
var allowedSyncFiles = map[string]bool{
	"accounts_nvidia.json":   true,
	"accounts_other.json":    true,
	"accounts_pool.json":     true,
	"pricing.json":           true,
	"config.json":            true,
	"relay_users.json":       true,
	"relay_packages.json":    true,
}

// handleSyncFull 导出云端当前数据目录下所有的号池、配置、用户与套餐文件内容
func (h *APIHandler) handleSyncFull(w http.ResponseWriter, r *http.Request) {
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
	if !session.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	if h.dataDir == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "data directory not configured"})
		return
	}

	files := make(map[string]string)
	for fname := range allowedSyncFiles {
		fpath := filepath.Join(h.dataDir, fname)
		data, err := os.ReadFile(fpath)
		if err == nil {
			files[fname] = string(data)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"files":   files,
	})
}

// handleSyncPush 接收客户端在服务器模式下修改的数据文件并实时写入落盘与通知热重载
func (h *APIHandler) handleSyncPush(w http.ResponseWriter, r *http.Request) {
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
	if !session.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission denied: admin only"})
		return
	}

	if h.dataDir == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "data directory not configured"})
		return
	}

	var req struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": fmt.Sprintf("invalid payload: %v", err)})
		return
	}

	cleanName := filepath.Base(req.Filename)
	if !allowedSyncFiles[cleanName] {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": fmt.Sprintf("file not allowed for sync: %s", cleanName)})
		return
	}

	targetPath := filepath.Join(h.dataDir, cleanName)
	if err := os.WriteFile(targetPath, []byte(req.Content), 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": fmt.Sprintf("failed to write file: %v", err)})
		return
	}

	// 触发热重载回调（若已注入）
	if h.onSyncReload != nil {
		h.onSyncReload(cleanName)
	}

	h.log("📥 [配置同步] 已成功同步更新服务端文件: %s (大小: %d bytes)", cleanName, len(req.Content))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"filename": cleanName,
	})
}
