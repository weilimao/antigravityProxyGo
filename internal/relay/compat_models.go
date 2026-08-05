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

	// /route/* 入口下,模型映射 ClientModel 可能带 "{provider}/" 前缀(如 nvidia/deepseek-ai/deepseek-v4-pro),
	// 用于精准路由到对应号池。模型列表是否展示这类带前缀条目按入口区分:
	//   - /route  入口(r.URL.Path 以 /route 开头, 含 /route/v1/models): 列出全部 Expose 条目(含带前缀名),
	//     客户端据此名请求即可精准路由;
	//   - /v1/*  入口(裸名直连链路如 /v1/chat/completions): 过滤掉「非 Google 族带 provider/ 前缀」的条目,
	//     避免裸名客户端误请求不可路由的模型。Google 族(google/gcp/antigravity/gemini-cli)裸名条目照常列出。
	includePrefixed := strings.HasPrefix(r.URL.Path, "/route")

	// buildExposedModelMap 把「暴露的模型 -> OwnedBy 归属」聚成 map,供两种响应形态共用。
	// OwnedBy 取自 ModelMappingEntry.OwnedBy;留空时由 inferOwnedBy 按模型名前缀兜底。
	exposed := h.buildExposedModelMap(includePrefixed)

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
//
// includePrefixed 控制是否收录「非 Google 族带 {provider}/ 前缀」的 /route 专属条目:
//   - true(/route 入口): 全部收录,客户端据此前缀名精准路由到对应号池;
//   - false(/v1/* 裸名直连入口): 过滤掉这类带前缀条目,避免裸名客户端误请求不可路由的模型。
//   Google 族(google/gcp/antigravity/gemini-cli/空)的裸名条目不受此开关影响,始终收录。
func (h *APICompatHandler) buildExposedModelMap(includePrefixed bool) []exposedModel {
	mappings := h.getModelMapping()
	if len(mappings) == 0 {
		mappings = settings.GetDefaultModelMappings()
	}
	out := make([]exposedModel, 0, len(mappings))
	for _, entry := range mappings {
		if !entry.Expose {
			continue
		}
		if !includePrefixed && isRoutedPrefixedModel(entry.ClientModel) {
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

// isRoutedPrefixedModel 判定 clientModel 是否为「非 Google 族带 {provider}/ 前缀」的 /route 专属条目。
// 形如 "nvidia/deepseek-ai/deepseek-v4-pro" / "deepseek/deepseek-chat" 命中;
// "gemini-2.5-pro"(无斜杠前缀) / "google/gemini-2.5-pro"(Google 族前缀) 不命中。
// 与 router_entry.go 的 isGoogleProvider 同口径:Google 族(google/gcp/antigravity/gemini-cli/空)
// 视作裸名直连链路专属,不在 /route 前缀过滤范围内。
func isRoutedPrefixedModel(clientModel string) bool {
	m := strings.TrimSpace(clientModel)
	idx := strings.Index(m, "/")
	if idx <= 0 {
		return false
	}
	provider := strings.ToLower(m[:idx])
	if provider == "" {
		return false
	}
	return !isGoogleProvider(provider)
}


