package relay

import (
	"net/http"
	"strings"
	"antigravity-proxy/internal/settings"
)

// compat_models.go: /v1/models 模型列表 handler(OpenAI 与 Anthropic 两种响应形态)。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

func (h *APICompatHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "Anthropic") ||
		(strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(r.URL.Path, "messages"))

	// buildExposedModelMap 把「暴露的模型 -> OwnedBy 归属」聚成 map,供两种响应形态共用。
	// OwnedBy 取自 ModelMappingEntry.OwnedBy;留空时由 inferOwnedBy 按模型名前缀兜底。
	exposed := h.buildExposedModelMap()

	if isAnthropic {
		var data []map[string]interface{}
		for _, m := range exposed {
			data = append(data, map[string]interface{}{
				"type":         "model",
				"id":           m.ID,
				"display_name": strings.Title(strings.ReplaceAll(m.ID, "-", " ")),
				"created_at":   "2024-05-14T00:00:00Z",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"data":     data,
			"has_more": false,
		})
	} else {
		var data []map[string]interface{}
		for _, m := range exposed {
			data = append(data, map[string]interface{}{
				"id":       m.ID,
				"object":   "model",
				"created":  1715644800,
				"owned_by": m.OwnedBy,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   data,
		})
	}
}

// exposedModel 是 handleModels 输出条目的内部表示。
type exposedModel struct {
	ID      string
	OwnedBy string
}

// buildExposedModelMap 收集所有 Expose=true 的模型及其 owned_by 归属,保持映射表顺序。
// 优先取 settings 配置(RelayModelMapping);为空时回退默认映射表。每条目 OwnedBy 留空则兜底推断。
func (h *APICompatHandler) buildExposedModelMap() []exposedModel {
	mappings := h.getModelMapping()
	if len(mappings) == 0 {
		mappings = settings.GetDefaultModelMappings()
	}
	out := make([]exposedModel, 0, len(mappings))
	for _, entry := range mappings {
		if !entry.Expose {
			continue
		}
		ownedBy := strings.TrimSpace(entry.OwnedBy)
		if ownedBy == "" {
			ownedBy = inferOwnedBy(entry.ClientModel)
		}
		out = append(out, exposedModel{ID: entry.ClientModel, OwnedBy: ownedBy})
	}
	return out
}


