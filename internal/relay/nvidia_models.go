package relay

// nvidia_models.go 收纳 NVIDIA /nvidia/v1/models 接口的模型清单构造与 fallback 解析,
// 以及日志体截断工具函数。从 nvidia.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。
//
// 本文件覆盖:
//   - defaultNvidiaFallbackModelIDs / formatNvidiaModelList / filterNvidiaModelIDs
//   - extractNvidiaModelIDs / buildFallbackNvidiaModels
//   - (h *APICompatHandler) handleNvidiaModels
//   - truncateBody (日志体截断,被 nvidia_responses.go 复用)

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"antigravity-proxy/internal/account"
)

// defaultNvidiaFallbackModelIDs 返回号池空/上游失败时的兜底模型 id 清单(上游 id 命名空间)。
func defaultNvidiaFallbackModelIDs() []string {
	return []string{
		"claude-sonnet-4-5",
		"claude-opus-4-6",
		"claude-haiku-4-5",
		"claude-fable-5",
		"deepseek-ai/deepseek-r1",
		"deepseek-ai/deepseek-v3",
		"meta/llama-3.3-70b-instruct",
		"moonshotai/kimi-k2.5",
		"z-ai/glm-5.2",
	}
}

// formatNvidiaModelList 把一批上游 id 组装成客户端期望的模型列表响应。
// isAnthropic=true → Anthropic /v1/models 形态 {"data":[{"type":"model","id":...}],"has_more":false}；
// isAnthropic=false → 标准 OpenAI list 形态 {"object":"list","data":[{"id":...,"object":"model"}]}。
// 作为路径 (a)/(b)/(c) 回写过滤后统一出口，避免形态组装逻辑散落多处。
func formatNvidiaModelList(ids []string, isAnthropic bool) map[string]interface{} {
	if ids == nil {
		ids = []string{}
	}
	if isAnthropic {
		type anthropicModel struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		anthModels := make([]anthropicModel, 0, len(ids))
		for _, id := range ids {
			anthModels = append(anthModels, anthropicModel{Type: "model", ID: id})
		}
		return map[string]interface{}{
			"data":     anthModels,
			"has_more": false,
		}
	}

	type openAIModel struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	oaiModels := make([]openAIModel, 0, len(ids))
	for _, id := range ids {
		oaiModels = append(oaiModels, openAIModel{ID: id, Object: "model"})
	}
	return map[string]interface{}{
		"object": "list",
		"data":   oaiModels,
	}
}

// filterNvidiaModelIDs 把上游 id 列表按全局"NVIDIA 专属模型清单"过滤。
// preferred 为空 → 原样返回全部(不过滤，语义=放行全量)；
// preferred 非空 → 仅保留命中清单的 id，保持原顺序(命中顺序，非清单顺序)。
func filterNvidiaModelIDs(ids []string, preferred []string) []string {
	if len(preferred) == 0 {
		return ids
	}
	allow := make(map[string]struct{}, len(preferred))
	for _, p := range preferred {
		if p = strings.TrimSpace(p); p != "" {
			allow[p] = struct{}{}
		}
	}
	if len(allow) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := allow[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// extractNvidiaModelIDs 从上游 /v1/models 原始 body 中抽取所有非空 data[].id，过滤掉空 id。
func extractNvidiaModelIDs(body []byte) ([]string, bool) {
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, true
}

// buildFallbackNvidiaModels 兜底列表按全局专属清单过滤后再组装。
// 号池空且清单非空时 = 兜底 ∩ 清单(语义=即便降级也只让客户端看见清单内模型)。
func buildFallbackNvidiaModels(isAnthropic bool, preferred []string) map[string]interface{} {
	ids := filterNvidiaModelIDs(defaultNvidiaFallbackModelIDs(), preferred)
	return formatNvidiaModelList(ids, isAnthropic)
}

// handleNvidiaModels 处理 /nvidia/v1/models 或 /nvidia/models 请求：
// 从 NVIDIA 号池选取可用账号，剥离 /nvidia 前缀后向远端 <BaseURL>/v1/models 发起 GET 请求并透传响应。
// 回给客户端的模型列表会按全局"NVIDIA 专属模型清单"过滤：清单空=全量；清单非空=仅清单内。
func (h *APICompatHandler) handleNvidiaModels(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	// 检测客户端是否为 Anthropic 协议 (如 Cherry Studio Messages 模式或 Claude Code)
	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.HasPrefix(r.Header.Get("x-api-key"), "sk-ant-") ||
		strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "anthropic")

	// 全局专属清单：清单非空时客户端可见模型被白名单收窄(空=不过滤=全量)。
	// 必须守护 settingsMgr == nil:测试构造 handler 时传 nil([nvidia_test.go] L34)，否则 panic。
	var preferred []string
	if h.settingsMgr != nil {
		preferred = h.settingsMgr.GetNvidiaPreferredModels()
	}

	var available []*account.Account
	if h.accountMgr != nil {
		available = h.accountMgr.GetEnabledNvidiaAccounts()
	}

	if len(available) == 0 {
		h.log("⚠️ [NVIDIA 模型列表透传] 号池中无可用 NVIDIA 账号，返回默认模型列表(按专属清单过滤)")
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}

	sessionKey := ""
	if userSession != nil {
		sessionKey = userSession.UserID
	}
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetNvidiaLBMode()
	}
	var poolAccount *account.Account
	poolAccount = h.pickNvidiaAccount(lbMode, sessionKey, available)
	if poolAccount == nil {
		poolAccount = available[0]
	}

	// 构造发往上游的 URL：剥离 /nvidia 本地路由前缀，强匹配上游 /v1/models
	baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
	targetURL := baseURL + "/v1/models"
	if strings.HasSuffix(baseURL, "/v1") {
		targetURL = baseURL + "/models"
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		h.log("❌ [NVIDIA 模型列表透传] 构造请求失败: %v", err)
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}
	req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
	req.Header.Set("Accept", "application/json")

	h.log("🟢 [NVIDIA 模型列表透传] 使用账号 %s | BaseURL: %s | 请求上游: %s | Token前缀: %s...",
		poolAccount.Email, poolAccount.BaseURL, targetURL,
		func() string {
			t := poolAccount.AccessToken
			if len(t) > 12 {
				return t[:12]
			}
			return t
		}())

	resp, errDo := h.client.Do(req)
	if errDo != nil {
		h.log("❌ [NVIDIA 模型列表透传] 上游网络请求失败: %v | 目标: %s", errDo, targetURL)
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}
	defer resp.Body.Close()

	bodyBytes, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		h.log("❌ [NVIDIA 模型列表透传] 读取上游响应体失败: %v", errRead)
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.log("⚠️ [NVIDIA 模型列表透传] 上游响应状态码 %d 非 200 | 响应体: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}

	// 解析上游返回的模型数量用于日志
	ids, ok := extractNvidiaModelIDs(bodyBytes)
	h.log("✅ [NVIDIA 模型列表透传] 上游返回 %d 个模型 | 状态码: %d", len(ids), resp.StatusCode)
	if !ok || len(ids) == 0 {
		// 解析失败或上游空列表 → 退回兜底(同样按清单过滤)
		h.log("⚠️ [NVIDIA 模型列表透传] 上游响应为空或 JSON 解析失败，返回默认模型列表")
		writeJSON(w, http.StatusOK, buildFallbackNvidiaModels(isAnthropic, preferred))
		return
	}

	// 按全局专属清单过滤(空清单=不过滤)
	filtered := filterNvidiaModelIDs(ids, preferred)

	if isAnthropic {
		// 路径 (b):Anthropic 入站 → 组 Anthropic 形态回写
		writeJSON(w, http.StatusOK, formatNvidiaModelList(filtered, true))
		return
	}

	// 路径 (c):OpenAI 入站
	// 清单非空 → 解析重写为过滤后的标准 OpenAI list(不再逐字节透传，丢失上游附加 header/字段，
	// NVIDIA /v1/models 实测仅 id/object，信息无实质损失)。
	// 清单空 → 沿用原始严格透传(逐字节 body + 全 header)，零回归。
	if len(preferred) > 0 {
		writeJSON(w, http.StatusOK, formatNvidiaModelList(filtered, false))
		return
	}

	// 严格透传上游 HTTP 200 真实响应
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(bodyBytes)
}

// truncateBody 截断响应体用于日志输出，避免超长日志。
func truncateBody(body []byte, maxLen int) string {
	if len(body) <= maxLen {
		return string(body)
	}
	return string(body[:maxLen]) + "...(truncated)"
}
