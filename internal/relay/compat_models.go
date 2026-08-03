package relay

import (
	"net/http"
	"strings"
	"antigravity-proxy/internal/settings"
)

// compat_models.go: /v1/models 模型列表 handler(OpenAI 与 Anthropic 两种响应形态)。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

func (h *APICompatHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	var supportedModels []string
	for _, entry := range h.getModelMapping() {
		if entry.Expose {
			supportedModels = append(supportedModels, entry.ClientModel)
		}
	}
	if len(supportedModels) == 0 {
		for _, entry := range settings.GetDefaultModelMappings() {
			if entry.Expose {
				supportedModels = append(supportedModels, entry.ClientModel)
			}
		}
	}

	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "Anthropic") ||
		(strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(r.URL.Path, "messages"))

	if isAnthropic {
		var data []map[string]interface{}
		for _, m := range supportedModels {
			data = append(data, map[string]interface{}{
				"type":         "model",
				"id":           m,
				"display_name": strings.Title(strings.ReplaceAll(m, "-", " ")),
				"created_at":   "2024-05-14T00:00:00Z",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"data":     data,
			"has_more": false,
		})
	} else {
		var data []map[string]interface{}
		for _, m := range supportedModels {
			data = append(data, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"created":  1715644800,
				"owned_by": "google",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   data,
		})
	}
}


