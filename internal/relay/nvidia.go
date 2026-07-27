package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/stats"
)

// nvidia.go 实现 /nvidia/* 路由的主链路：
// 入站 Anthropic(/nvidia/v1/messages) 或 OpenAI Chat(/nvidia/v1/chat/completions) →
// 选号(支持游标轮询默认与粘性会话) → 协议转换 → 直连 NVIDIA 上游 → 响应回译 → 换号重试。
// 完整闭环以 internal/relay/compat.go 的 handleV1Internal 为模板。

// nvidiaChannel 是 NVIDIA 号池的通道标识。
const nvidiaChannel = "nvidia"

// nvidiaReplayMaxBytes 是 NVIDIA Anthropic 流式蓄流回放的单流最大蓄流字节数。
// 超过即判定为"超大输出":直连重试轮退回边读边写旧路径(放弃重试能力)兜底失败;
// 兜底轮把它转为 lastErr 落 overloaded_error(兜底无可退的边读边写路径)。
// 抽为包级常量是因为直连循环与兜底 helper 必须共用同一阈值,避免两处硬编码值飘移导致
// "直连认为可蓄、兜底认为超大"这类判定不一致。16 MiB 是经验值,覆盖绝大多数对话回复。
const nvidiaReplayMaxBytes = 16 * 1024 * 1024 // 16 MiB

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

// handleNvidia 处理 /nvidia/* 请求。
func (h *APICompatHandler) handleNvidia(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method == http.MethodGet || path == "/nvidia/v1/models" || path == "/nvidia/models" || strings.HasSuffix(path, "/models") {
		h.handleNvidiaModels(w, r, userSession)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	reqID := fmt.Sprintf("nv_%d", time.Now().UnixNano())
	if h.settingsMgr != nil {
		enabled := h.settingsMgr.GetEnableDebuggerMode()
		logPath := h.settingsMgr.GetResolvedDebuggerLogPath()
		GetGlobalDebugger().Configure(enabled, logPath)
	}
	GetGlobalDebugger().LogClientRequest(reqID, r.Method, r.URL.Path, r.Header, bodyBytes)

	// 入站协议判定：按路径决定（三选一）
	inboundAnthropic := strings.HasSuffix(path, "/v1/messages")
	inboundOpenAI     := strings.HasSuffix(path, "/v1/chat/completions")
	inboundResponses  := strings.HasSuffix(path, "/v1/responses")
	if !inboundAnthropic && !inboundOpenAI && !inboundResponses {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "unsupported nvidia endpoint: use /nvidia/v1/messages, /nvidia/v1/chat/completions or /nvidia/v1/responses",
		})
		return
	}

	// 解析入站请求以确定模型与 stream。
	// Responses 入站虽有独立的 input[] 结构，但 model/stream 字段同构，先提取这两个公共字段，
	// 请求体到 OpenAIChatRequest 的完整转换在选号后按 inbound 分支执行。
	var inModel string
	var isStreaming bool
	if inboundAnthropic {
		var req AnthropicRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else if inboundResponses {
		// Responses 请求统一走 ParseUnifiedOpenAIRequest 解析（兼容 input[] 与 messages[]），
		// model/stream 取用统一产物的对应字段。
		req, err := ParseUnifiedOpenAIRequest(bodyBytes)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid responses request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else {
		var req OpenAIChatRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	}
	// 流式也可能通过 Accept: text/event-stream 或 stream=true 表达，此处仅以 body.stream 为准。


	// NVIDIA family 配额预扣额校验（独立于 gemini/claude）
	if h.authMgr != nil && h.authMgr.userMgr != nil {
		user := h.authMgr.userMgr.GetUserByID(userSession.UserID)
		if user != nil {
			if err := nvidiaQuotaCheck(userSession.UserID, user.Quotas.Nvidia); err != nil {
				writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
					"error": map[string]interface{}{
						"type":    "quota_exceeded",
						"message": err.Error(),
					},
				})
				return
			}
		}
	}

	// 选号：只要携带 /nvidia/ 前缀，全量使用英伟达号池进行会话粘性负载均衡与轮询换号
	poolChannel := nvidiaChannel
	available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, inModel)

	if len(available) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "nvidia_pool_empty",
				"message": "no available NVIDIA account in pool (channel nvidia)",
			},
		})
		return
	}

	skippedAccounts := make(map[string]bool)
	maxAttempts := len(available)
	if maxAttempts > 5 {
		maxAttempts = 5
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	sessionKey := userSession.UserID
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetNvidiaLBMode()
	}

	var lastResp *http.Response
	var lastErr error
	var lastErrBody []byte
	var lastErrCode int

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var activeAvailable []*account.Account
		for _, a := range available {
			if !skippedAccounts[a.ID] {
				activeAvailable = append(activeAvailable, a)
			}
		}
		if len(activeAvailable) == 0 {
			if lastErr == nil {
				lastErr = fmt.Errorf("all nvidia accounts in pool failed")
			}
			break
		}

		var poolAccount *account.Account
		poolAccount = h.pickNvidiaAccount(lbMode, sessionKey, activeAvailable)
		if poolAccount == nil {
			lastErr = fmt.Errorf("no available nvidia account assigned from pool")
			break
		}

		// 模型映射(账号级四档位)
		upstreamModel := mapNvidiaModel(inModel, poolAccount)

		// 根据入站协议构造发往上游的 OpenAI Chat 请求体
		var upstreamReq *OpenAIChatRequest
		if inboundAnthropic {
			var anthReq AnthropicRequest
			if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
				return
			}
			anthReq.Model = upstreamModel
			u, err := AnthropicToOpenAIChat(&anthReq)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "anthropic->openai transform failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else if inboundResponses {
			// Responses(含 codex /v1/responses) → 统一解析 → OpenAIChatRequest
			u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "responses->openai transform failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else {
			var chatReq OpenAIChatRequest
			if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
				return
			}
			chatReq.Model = upstreamModel
			// 流式注入 stream_options.include_usage，确保上游在 SSE 末尾吐 usage
			if chatReq.Stream && (chatReq.StreamOptions == nil || !chatReq.StreamOptions.IncludeUsage) {
				chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
			upstreamReq = &chatReq
		}

		// 构造上游 URL：{BaseURL}/v1/chat/completions (若 base_url 已含 /v1 后缀，不再重复拼接)
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		targetURL := baseURL + "/v1/chat/completions"
		if strings.HasSuffix(baseURL, "/v1") {
			targetURL = baseURL + "/chat/completions"
		}

		upstreamBody, err := json.Marshal(upstreamReq)
		if err != nil {
			lastErr = err
			break
		}

		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
		if err != nil {
			lastErr = err
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
		req.Header.Set("Accept", "application/json")
		// NVIDIA 上游不识别 anthropic 头，不注入

		h.log("🟢 [NVIDIA 中继 %d/%d] 用户 %s 分配账号 %s | 模型 %s -> %s | %s", attempt+1, maxAttempts, userSession.UserID, poolAccount.Email, inModel, upstreamModel, targetURL)

		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}

		// 单账号针对 429 尝试最多 5 次，重试 5 次均 429 失败才切下一个账号
		var activeResp *http.Response
		accountSuccess := false
		const maxSingleAcc429Retries = 5
		// 压缩断路器：单请求服务端就地压缩重试计数（文档 §3 MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES=3）。
		singleCompressFailures := 0
		// 首次 ResourceExhausted 帧的原始文本，用于压缩救不活时判定是否回写 400 引导客户端自压（治本）。
		var resentExhaustedFrame string

		for singleAttempt := 1; singleAttempt <= maxSingleAcc429Retries; singleAttempt++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				lastErr = err
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
			req.Header.Set("Accept", "application/json")

			if singleAttempt > 1 {
				h.log("🔄 [NVIDIA 中继 429 重试 %d/%d] 账号 %s 遇到 429 限流，等待 2 秒后原地重试...", singleAttempt, maxSingleAcc429Retries, poolAccount.Email)
			}

			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				h.log("⚠️ [NVIDIA 中继] 账号 %s 访问上游失败: %v", poolAccount.Email, errDo)
				skippedAccounts[poolAccount.ID] = true
				lastErr = errDo
				lastResp = nil
				// 网络错误：短期冷静该号 60s，换号重试
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				break
			}

			// 处理 429 限流：5 次以内原地退避 2 秒重试，重试 5 次均 429 失败才冷冻切号
			if resp.StatusCode == http.StatusTooManyRequests {
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream status 429")

				if singleAttempt < maxSingleAcc429Retries {
					time.Sleep(2 * time.Second)
					continue
				}

				h.log("⚠️ [NVIDIA 中继] 账号 %s 重试 %d 次仍返回 429，冷冻该账号并换号...", poolAccount.Email, maxSingleAcc429Retries)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				break
			}

			// 401/403：鉴权或配额问题，不原号重试，换号
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				h.log("⚠️ [NVIDIA 中继] 账号 %s 上游返回 %d，剔除换号重试...", poolAccount.Email, resp.StatusCode)
				cooldown := 5 * 60 * 1000
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream status %d", resp.StatusCode)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+int64(cooldown), nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				break
			}

			// 5xx：服务端错误，换号重试
			if resp.StatusCode >= 500 {
				h.log("⚠️ [NVIDIA 中继] 账号 %s 上游 5xx(%d)，换号重试...", poolAccount.Email, resp.StatusCode)
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream server error %d", resp.StatusCode)
				skippedAccounts[poolAccount.ID] = true
				break
			}

			// 成功 HTTP 200
			if isStreaming {
				bufReader := bufio.NewReader(resp.Body)
				peekBytes, _ := bufReader.Peek(1024)
				peekStr := string(peekBytes)
				if strings.Contains(peekStr, `"error"`) && (strings.Contains(peekStr, `"ResourceExhausted"`) || strings.Contains(peekStr, `"internal_server_error"`) || strings.Contains(peekStr, `"Internal server error"`)) {
					// 上游流内首帧抛出 ResourceExhausted / internal_server_error(500)：
					// 根因多为 worker 缓冲打满，单请求体积过大是诱因之一；换号无效（同端点簇）。
					// 先尝试服务端就地压缩请求体并原地复用当前号重试（救当前请求）。
					h.log("⚠️ [NVIDIA 中继] 账号 %s 上游流内首帧抛出 ResourceExhausted/500，先尝试就地压缩请求体重试...", poolAccount.Email)
					resp.Body.Close()
					lastResp = nil
					lastErr = fmt.Errorf("nvidia upstream sse error: %s", truncateBody(peekBytes, 200))
					if resentExhaustedFrame == "" {
						resentExhaustedFrame = peekStr
					}

					// 压缩断路器：最多 3 轮
					if singleCompressFailures < 3 {
						compReq, compOK, compEnabled := h.tryCompressNvidiaRequest(upstreamReq)
						if compEnabled && compOK && compReq != nil {
							if newBody, e := json.Marshal(compReq); e == nil && len(newBody) < len(upstreamBody) {
								h.log("🧩 [NVIDIA 中继] 已压缩请求体 %d → %d 字节，原地复用账号 %s 重试...", len(upstreamBody), len(newBody), poolAccount.Email)
								upstreamBody, upstreamReq = newBody, compReq
								singleCompressFailures++
								continue // 复用当前号、不冷冻、不 break 到外层换号
							}
						}
						// 压缩无效或不可压缩：计一次失败，仍在本号重试（给上游 worker 可能短暂的限流恢复机会）
						singleCompressFailures++
						if singleCompressFailures < 3 {
							continue
						}
					}

					// 压缩 3 轮仍失败：判定是否"上下文超窗/无可恢复"语义。
					// 若是 → 回写 Anthropic 标准 invalid_request_error 400，引导客户端本地 /compact 自压（治本）。
					// 否则 → 保留原冷冻换号路径。
					if looksLikeContextTooLong(resentExhaustedFrame) {
						h.log("🛑 [NVIDIA 中继] 账号 %s 压缩 3 轮仍失败且首帧含上下文超窗语义，回写 400 invalid_request_error 引导客户端自压...", poolAccount.Email)
						h.replyAnthropicContextTooLong(w, inboundAnthropic, upstreamModel)
						return
					}
					h.log("⚠️ [NVIDIA 中继] 账号 %s 压缩重试耗尽，冷冻该账号并换号...", poolAccount.Email)
					skippedAccounts[poolAccount.ID] = true
					h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
					h.sessionRouter.UnbindSession(sessionKey)
					break
				}
				resp.Body = struct {
					io.Reader
					io.Closer
				}{
					Reader: bufReader,
					Closer: resp.Body,
				}
			}

			activeResp = resp
			accountSuccess = true
			break
		}

		if accountSuccess && activeResp != nil {
			inboundKind := "openai_chat"
			if inboundAnthropic {
				inboundKind = "anthropic"
			} else if inboundResponses {
				inboundKind = "responses"
			}
			h.writeNvidiaResponse(w, r, activeResp, inboundKind, isStreaming, upstreamModel, userSession, poolAccount, targetURL, upstreamBody)
			return
		}
	}

	// 重试用尽：回写最后一次上游错误状态码与错误体（若有），否则 502 兜底
	if lastResp != nil {
		defer lastResp.Body.Close()
		for k, values := range lastResp.Header {
			if isAnthropicPassthroughHeader(k) {
				continue
			}
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(lastResp.StatusCode)
		_, _ = io.Copy(w, lastResp.Body)
		return
	}
	if lastErrBody != nil && lastErrCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastErrCode)
		_, _ = w.Write(lastErrBody)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "nvidia pool exhausted: " + errStr(lastErr)})
}

// writeNvidiaResponse 把上游 OpenAI Chat 响应回译成入站协议并写回客户端。
// inboundKind: "openai_chat"（透传）| "anthropic"（回译为 Messages）| "responses"（回译为 Responses API）。
// r 为入站请求,供流式分支透传 r.Context() 到 watchCancel,实现客户端取消即断 + 尾帧补发。
// writeNvidiaResponse 把上游响应按入站协议类型回写客户端。targetURL/upstreamBody 仅对 Anthropic 流式
// 入站有意义(供蓄流回放链路原账号重建上游请求实现断流重试);其余链路忽略这两个参数,不参与重试。
func (h *APICompatHandler) writeNvidiaResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, inboundKind string, isStreaming bool, model string, userSession *RelaySession, poolAccount *account.Account, targetURL string, upstreamBody []byte) {
	defer resp.Body.Close()

	switch inboundKind {
	case "anthropic":
		// 入站是 Anthropic：需要把上游 OpenAI Chat 响应回译成 Anthropic Messages
		if isStreaming {
			h.writeNvidiaAnthropicStream(w, r, resp, model, userSession, poolAccount, targetURL, upstreamBody)
			return
		}
		h.writeNvidiaAnthropicNormal(w, resp, model, userSession, poolAccount)
		return

	case "responses":
		// 入站是 Responses API(codex /v1/responses)：把上游 OpenAI Chat 响应回译成 Responses 格式。
		// 非流式聚合后回译；流式逐 SSE chunk 重写成 Responses 事件序列。
		if isStreaming {
			h.writeNvidiaResponsesStream(w, r, resp, model, userSession, poolAccount)
			return
		}
		h.writeNvidiaResponsesNormal(w, resp, model, userSession, poolAccount)
		return

	default:
		// 入站是 OpenAI Chat：直接透传上游响应（含流式 SSE）。
		// 方案 A：边透传边嗅探 usage，非流式从全量 JSON 提 usage，
		// 流式从 SSE 末帧 data:{...usage...} 提 usage，统计口径与 Anthropic 入站一致。
		inUsage, outUsage := h.proxyNvidiaOpenAIPassthrough(r.Context(), w, resp, isStreaming)
		h.recordNvidiaUsage(userSession, model, inUsage, outUsage, poolAccount)
	}
}

// proxyNvidiaOpenAIPassthrough 处理入站为 OpenAI Chat 时的上游响应透传，
// 同时嗅探出 (inputTokens, outputTokens) 用于号池成员账号维度统计。
// 透传坚持逐字节不变：非流式先读全量 body 解析 usage 再原样写出；
// 流式逐行读 SSE 帧、逐帧原样透传，顺带解析每个 chunk 的 usage 字段(OpenAI 末帧 usage 字段为权威值)。
// 上游非 200(错误/限流/鉴权失败等)直接透传原 body，usage 返回 0 不计入号池账号成本。
// 返回 (inTokens, outTokens)。
//
// ctx 为入站请求 r.Context()：流式透传时客户端取消 → watchCancel 捕获 ctx.Done() 立即
// Close 上游 resp.Body → scanner.Scan() 退出；随后在循环外补发一帧 data: [DONE]\n\n,
// 给 OpenAI 客户端 SDK 明确的流结束语义(避免客户端卡等上游末帧)。
func (h *APICompatHandler) proxyNvidiaOpenAIPassthrough(ctx context.Context, w http.ResponseWriter, resp *http.Response, isStreaming bool) (int, int) {
	// 复制上游响应头(保留 Content-Type 等给客户端)，再写状态码。
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

	// 上游非 200：直接透传错误体，不嗅探 usage。
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return 0, 0
	}

	if !isStreaming {
		// 非流式：全量读 body 解析 usage，原样透传。
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"read upstream passthrough body failed"}`))
			return 0, 0
		}
		// 仅在解析成功时记 usage，避免坏响应污染统计；body 始终原样透传。
		var chatResp OpenAIChatResponse
		inUsage, outUsage := 0, 0
		if json.Unmarshal(bodyBytes, &chatResp) == nil {
			inUsage = chatResp.Usage.PromptTokens
			outUsage = chatResp.Usage.CompletionTokens
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		return inUsage, outUsage
	}

	// 流式：逐行嗅探 SSE，逐行原样透传，末帧 usage 为权威。
	// X-Accel-Buffering: no 禁止 Nginx / 反代缓冲 SSE，避免下游体感"攒一批再发"被误判非流式，
	// 与 antigravity 链路 internal/relay/compat.go:835 对齐。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	// 客户端取消即断：ctx.Done() → Close 上游 body → scanner.Scan() 立即返回
	if ctx != nil {
		stop := watchCancel(ctx, resp.Body)
		defer stop()
	}
	scanner := bufio.NewScanner(resp.Body)
	// 单帧可能较大(尤其带工具调用/长内容)，放宽单行上限避免截断丢 usage。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var inUsage, outUsage int
	doneSent := false // 是否已向下游透传过 [DONE] 终止帧
	for scanner.Scan() {
		line := scanner.Text()
		// SSE 规范：每帧以单个 \n 结尾为边界；OpenAI 上游多以 \n\n 分隔事件，
		// 这里按行写出，并在非空行末补 \n 还原原始边界。
		if line == "" {
			// 空行作为事件分隔，原样写一个换行维持 SSE 事件边界。
			_, _ = w.Write([]byte("\n"))
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		_, _ = w.Write([]byte(line + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
		// 仅解析 data: 行嗅探 usage(注释行 :xxx 与 event: 行跳过)。
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			doneSent = true
			continue
		}
		var chunk OpenAIChatStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			inUsage = chunk.Usage.PromptTokens
			outUsage = chunk.Usage.CompletionTokens
		}
	}
	// 上游未发出 [DONE](常见于客户端取消触发 body.Close 后 scanner 提前退出):
	// 补发一帧 data: [DONE]\n\n,给 OpenAI 客户端 SDK 明确的流结束语义。
	// ctx 取消或上游异常截断均走此兜底,确保下游不卡在"等末帧"状态。
	if !doneSent {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	return inUsage, outUsage
}

// writeNvidiaAnthropicNormal 处理非流式 Anthropic 入站：读全量 OpenAI Chat 响应 → 回译 → 写出。
// writeAnthropicErrorFromUpstream 把 NVIDIA(OpenAI 兼容)上游的非 200 错误体翻译成
// Anthropic 标准错误结构回写给客户端,而非裸透 OpenAI JSON。
//
// 背景:Claude Code / VSCode 插件等 Anthropic 客户端按 {"type":"error","error":{...}}
// 识别错误;若直接回写 OpenAI 的 {"error":{"message":...,"code":...}},客户端无法识别
// 错误协议,表现为卡住或奇怪报错("断了不干活"的诱因之一)。
//
// 状态码沿用上游原值(400→400,5xx→5xx);错误文案透传上游 message 原文,便于从 CLI 报错
// 直接定位 NVIDIA 真实原因(如 missing field content / model not found 等)。
// 解析失败的兜底:仍透传上游原文 message,保证客户端能看到可读错误而非空结构。
func (h *APICompatHandler) writeAnthropicErrorFromUpstream(w http.ResponseWriter, statusCode int, upstreamBody []byte) {
	// 解析上游 OpenAI 错误体 {"error":{"message":...,"type":...,"code":...}}
	type openAIErrBody struct {
		Error *struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code"`
		} `json:"error"`
	}
	errType := "invalid_request_error"
	errMsg := string(upstreamBody) // 兜底:解析失败时把上游原文塞进 message,保证客户端能看到可读内容
	if len(upstreamBody) > 0 {
		var parsed openAIErrBody
		if json.Unmarshal(upstreamBody, &parsed) == nil && parsed.Error != nil {
			if parsed.Error.Message != "" {
				errMsg = parsed.Error.Message
			}
			// NVIDIA 常见 type:"internal_server_error"(对应 500);5xx 映射 API error,
			// 4xx 映射 invalid_request_error,与 Anthropic 官方错误语义对齐。
			if parsed.Error.Type != "" {
				if statusCode >= 500 {
					errType = "api_error"
				} else {
					errType = "invalid_request_error"
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": errMsg,
		},
	})
	_, _ = w.Write(payload)
}

func (h *APICompatHandler) writeNvidiaAnthropicNormal(w http.ResponseWriter, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
		return
	}
	if resp.StatusCode != http.StatusOK {
		// 上游非 200:翻译成 Anthropic 标准错误结构回写(原裸透 OpenAI JSON 会让 CLI 无法识别)
		h.writeAnthropicErrorFromUpstream(w, resp.StatusCode, bodyBytes)
		return
	}
	var chatResp OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "invalid openai response json: " + err.Error()})
		return
	}
	anthResp := OpenAIChatToAnthropic(&chatResp)
	anthResp.Model = model
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	payload, _ := json.Marshal(anthResp)
	_, _ = w.Write(payload)

	// 配额/统计回调(复用 statsTracker)
	h.recordNvidiaUsage(userSession, model, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, poolAccount)
}

// writeNvidiaAnthropicStream 处理流式 Anthropic 入站：上游 OpenAI Chat SSE → Anthropic SSE。
// 响应头对齐 compat.go:826-837(Gemini 链路)保证 SSE 不被反代/框架缓冲:
//   - X-Accel-Buffering: no 禁止 Nginx 聚合 SSE;
//   - http.Flusher 逐帧 push 到 TCP socket,避免仅写到 http.ResponseWriter 内部缓冲。
//
// 蓄流回放架构(上游断流服务端无缝重试):
// 不再"边读上游边写客户端"。而是先用 replayWriter 在内存把整条上游 SSE 翻译攒成完整 Anthropic SSE
// (期间若上游中途断流 unexpected EOF,本方法丢弃本次 buffer、睡眠 5s 后原账号重建上游请求重拉,
// 最多重试 5 次,不换号);只有当整条 ready(收到 finish_reason 且无上游错误)后,才 WriteHeader(200)
// + SSE 头并把 buffer 逐帧 flush 回放给客户端。重试期间客户端未收到任何字节,不会出现"半截内容冲突";
// 重试耗尽则回写 Anthropic overloaded_error 让 CLI 看到真实失败(不再静默补 end_turn 假闭合)。
//
// r 透传 r.Context():客户端取消时立即终止重试与重拉;poolAccount 重试全程保持同一账号(按要求不换号)。
// targetURL/upstreamBody 由主循环透传,供重试时原样重建上游 POST 请求体与目标 URL(不重新选号、不改动请求)。
func (h *APICompatHandler) writeNvidiaAnthropicStream(w http.ResponseWriter, r *http.Request, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, targetURL string, upstreamBody []byte) {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		// 上游非 200:翻译成 Anthropic 标准错误结构回写(原裸透 OpenAI JSON 会让 CLI 卡住/报奇怪错误)
		h.writeAnthropicErrorFromUpstream(w, resp.StatusCode, bodyBytes)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.log("⚠️ [NVIDIA Anthropic 流式] http.ResponseWriter 不支持 Flusher, 降级为仅 bufio flush (SSE 实时性可能打折)")
	}

	// 蓄流回放:先在内存把整条上游 SSE 攒成完整 Anthropic SSE,断流可原账号重拉(≤5×5s),不换号;
	// 攒齐后再写响应头并逐帧回放给客户端;重试期间不写任何字节,不会半截冲突。
	replay, in, out, finalErr := h.pullAnthropicStreamWithRetry(r, resp, poolAccount, targetURL, upstreamBody, model)
	if finalErr != nil {
		// 重试耗尽:回写 Anthropic overloaded_error(529 语义),让 CLI 识别为真实失败而非"本轮正常结束"。
		// 取代旧的"补 end_turn 假闭合"——后者会让 CLI 拿到内容残缺但语义正常的流,既不重试也不报错,任务就卡死。
		resp.Body.Close()
		h.log("🛑 [NVIDIA Anthropic 流式] 上游断流, 服务端重试 5 次仍失败, 回写 overloaded_error: %v", finalErr)
		h.replyAnthropicOverloaded(w)
		return
	}

	// 整条 ready:写 SSE 响应头并逐帧回放 buffer 给客户端。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush() // 立即把响应头推给客户端, 让其尽早进入 SSE 等待状态
	}
	bw := bufio.NewWriter(w)
	bw.Write(replay.bytes())
	bw.Flush()
	if ok {
		flusher.Flush() // 收尾刷净, 确保 message_stop 落盘
	}
	h.recordNvidiaUsage(userSession, model, in, out, poolAccount)
}

// pullAnthropicStreamWithRetry 把上游 OpenAI Chat SSE 翻译成完整 Anthropic SSE 并蓄流进 replayWriter,
// 上游中途断流(unexpected EOF / 未给出 finish_reason)时丢弃本次 buffer、睡眠 5s 后原账号重建上游请求重拉,
// 最多重试 5 次,全程不换号。客户端 r.Context() 取消时立即终止重试与重拉。
//
// 返回 (replay, in, out, err):
//   - replay:整条 ready 的 Anthropic SSE 字节缓冲,供调用方逐帧 flush 回放;
//   - in/out:成功这次的累计 input/output tokens,用于号池账号维度统计;失败时为 0;
//   - err:重试耗尽仍失败时非 nil(含最后一次上游错误),调用方据此回写 overloaded_error 给 CLI。
//
// 完整性判定:openAIChatSSEToAnthropicSSEInto 返回 finishEmitted==true && err==nil 视为完整。
// finishEmitted==true 表示收到上游 finish_reason 帧;err==nil 排除 ctx 取消与上游 SSE error chunk
// 及 scanner.Err()(UnexpectedEOF 等)。两者同时满足才认定本轮蓄流可回放给客户端。
//
// 重试约束(对齐用户需求):不重新选号、不冷冻账号、不改请求体;仅以同一 poolAccount 复用 targetURL +
// upstreamBody 重建 POST 上游。与现有"429 退避换号"链路独立,不触发 skippedAccounts/cooldown 联动。
//
// 超大流保护:蓄流字节数超过 replayMaxBytes(16 MiB)判定为超大输出,直接退回"边读边写旧路径"(不重试),
// 避免无界内存占用。这是蓄流架构的固有约束:超大输出回退实时透传,放弃断流重试能力。
func (h *APICompatHandler) pullAnthropicStreamWithRetry(r *http.Request, firstResp *http.Response, poolAccount *account.Account, targetURL string, upstreamBody []byte, model string) (replay *replayWriter, in, out int, finalErr error) {
	const (
		maxRetries = 5
		// replayMaxBytes 复用包级 nvidiaReplayMaxBytes,保证直连与兜底 helper 同一阈值。
		replayMaxBytes = nvidiaReplayMaxBytes
	)
	// 单次重试退避:生产默认 5s(nvidiaStreamRetryWait,构造初始化);测试可覆盖为小值快跑。
	// 零值兜底为 5s,避免误装配导致无退避狂打上游。
	retryWait := h.nvidiaStreamRetryWait
	if retryWait <= 0 {
		retryWait = 5 * time.Second
	}
	streamID := fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	httpClient := h.streamClient
	ctx := r.Context()

	// 本次循环的活跃响应体(逐次重拉新建,每次结束后 Close)
	var activeResp *http.Response = firstResp
	var activeBody io.ReadCloser = firstResp.Body
	// 第一轮复用主循环已 Do 出来的 firstResp;从第二轮起重建上游请求。
	for attempt := 0; attempt < maxRetries; attempt++ {
		// 重拉准备:第 0 轮用现成 firstResp,其后各轮新建上游请求(原账号、原请求体)。
		if attempt > 0 {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				finalErr = fmt.Errorf("rebuild nvidia upstream request: %w", err)
				if activeBody != nil {
					activeBody.Close()
				}
				return nil, 0, 0, finalErr
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
			req.Header.Set("Accept", "application/json")
			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				finalErr = fmt.Errorf("nvidia upstream retry do failed: %w", errDo)
				h.log("⚠️ [NVIDIA Anthropic 流式] 断流重试 %d/%d 账号 %s 重拉失败: %v", attempt+1, maxRetries, poolAccount.Email, errDo)
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				finalErr = fmt.Errorf("nvidia upstream retry status %d", resp.StatusCode)
				h.log("⚠️ [NVIDIA Anthropic 流式] 断流重试 %d/%d 账号 %s 上游返回 %d", attempt+1, maxRetries, poolAccount.Email, resp.StatusCode)
				continue
			}
			activeResp = resp
			activeBody = resp.Body
		} else if attempt == 0 && activeResp.StatusCode != http.StatusOK {
			// 主循环已保证流式入站进入本函数时 resp.StatusCode==200,此处仅防御性兼容。
			finalErr = fmt.Errorf("nvidia upstream status %d", activeResp.StatusCode)
			activeBody.Close()
			return nil, 0, 0, finalErr
		}

		rw := newReplayWriter()
		attemptIn, attemptOut, finishEmitted, streamTerminated, sseErr := openAIChatSSEToAnthropicSSEInto(ctx, activeBody, activeBody, rw, streamID, model)
		activeBody.Close() // 本轮上游响应体读完即关,下一轮(若有)重拉会拿到全新 body

		// 完整性判定:收到 finish_reason 帧或上游流以 [DONE]/正常 EOF 正常终止,且无上游错误/未 ctx 取消 → 可回放。
		// streamTerminated 兜底 NIM 等上游"不发 finish_reason、仅 usage+[DONE]"的合法收尾形态,
		// 避免把它误判为断流而触发无意义重试。真·断流(unexpected EOF 在 [DONE] 前)使 streamTerminated=false
		// 且 sseErr!=nil,本判定不满足,落入重试路径。
		if sseErr == nil && (finishEmitted || streamTerminated) {
			return rw, attemptIn, attemptOut, nil
		}
		// ctx 取消:客户端已断,不再重试,带上 ctx 错误返回。
		if ctx != nil && ctx.Err() != nil {
			finalErr = fmt.Errorf("client context cancelled during nvidia stream retry: %w", ctx.Err())
			if sseErr != nil {
				finalErr = fmt.Errorf("%v (last sse err: %v)", finalErr, sseErr)
			}
			return nil, 0, 0, finalErr
		}
		// 超大流保护:蓄流超过阈值,判定为超大输出,退回边读边写(不再重试)。
		if rw.len() > replayMaxBytes {
			h.log("⚠️ [NVIDIA Anthropic 流式] 蓄流 %d 字节超阈值 %d, 退回边读边写路径放弃重试", rw.len(), replayMaxBytes)
			finalErr = fmt.Errorf("nvidia replay stream oversized %d bytes (exceeds %d), fall back to passthrough", rw.len(), replayMaxBytes)
			return nil, 0, 0, finalErr
		}
		// 不完整(断流/未收尾/上游内嵌 error chunk):记录原因,睡眠 5s 后重拉(最后一轮不再睡)。
		lastErr := sseErr
		if lastErr == nil {
			lastErr = fmt.Errorf("nvidia upstream stream incomplete (finishEmitted=%v streamTerminated=%v)", finishEmitted, streamTerminated)
		}
		finalErr = lastErr
		h.log("⚠️ [NVIDIA Anthropic 流式] 上游断流判定不完整(已攒 %d 字节), 将重试 %d/%d 账号 %s: %v", rw.len(), attempt+1, maxRetries, poolAccount.Email, lastErr)
		if attempt < maxRetries-1 {
			// 睡眠 5s 但受 ctx 取消打断,客户端断开立即放弃重试不空跑。
			select {
			case <-ctx.Done():
				finalErr = fmt.Errorf("client context cancelled during retry backoff: %w", ctx.Err())
				return nil, 0, 0, finalErr
			case <-time.After(retryWait):
			}
		}
	}
	// ===== 直连 5s×5 同账号重试耗尽后,切兜底出站代理再 1 轮(单次请求级,不记忆状态) =====
	// 兜底代理只对本机到上游的网络路径类断流有效;对上游 worker 过载/上游节点抖动这类上游侧故障,
	// 换出口到达同一上游集群多半仍失败——这是物理事实,兜底尽力而为,1 轮不成即回 overloaded_error。
	// 仅 NVIDIA 链路生效(本函数即 NVIDIA Anthropic 流式);不换号、不改请求体,仅换 transport 出口。
	// 配置为空 / 解析失败时跳过兜底,直接回 overloaded_error。
	if h.settingsMgr != nil && h.settingsMgr.GetFallbackProxyEnabled() {
		fbAddr := h.settingsMgr.GetFallbackProxyAddress()
		fbUser := h.settingsMgr.GetFallbackProxyUsername()
		fbPass := h.settingsMgr.GetFallbackProxyPassword()
		fbClient, fbErr := netutil.GetFallbackClient(fbAddr, fbUser, fbPass)
		if fbErr != nil {
			h.log("⚠️ [NVIDIA Anthropic 流式] 兜底代理配置无效,跳过兜底: %v (addr=%s)", fbErr, fbAddr)
		} else if fbClient != nil {
			h.log("🛟 [NVIDIA Anthropic 流式] 直连重试耗尽,切兜底代理 %s 再试 1 轮 账号 %s", fbAddr, poolAccount.Email)
			fbReplay, fbIn, fbOut, fbFinalErr := h.pullAnthropicStreamOneRound(ctx, fbClient, poolAccount, targetURL, upstreamBody, streamID, model, "兜底")
			if fbFinalErr == nil && fbReplay != nil {
				return fbReplay, fbIn, fbOut, nil
			}
			if fbFinalErr != nil {
				finalErr = fmt.Errorf("fallback proxy round also failed: %w", fbFinalErr)
			}
		}
	}
	// 重试耗尽(含兜底也失败):返回最后一次错误原因,由调用方回写 overloaded_error。
	return nil, 0, 0, finalErr
}

// pullAnthropicStreamOneRound 用指定的 httpClient(potentially 兜底代理 client)向 NVIDIA 上游发一次请求,
// 蓄流翻译成 Anthropic SSE 并做完整性判定,返回 (replay, in, out, err)。
// ok=err==nil&&replay!=nil 表示本流完整可回放;否则 err 含原因供调用方记录。
// 抽出供直连重试循环与兜底 1 轮复用,避免拉轮逻辑复制粘贴漂移。roundLabel 仅用于日志区分("兜底"/"直连")。
func (h *APICompatHandler) pullAnthropicStreamOneRound(ctx context.Context, httpClient *http.Client, poolAccount *account.Account, targetURL string, upstreamBody []byte, streamID, model, roundLabel string) (*replayWriter, int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("rebuild nvidia upstream request (%s): %w", roundLabel, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) do failed: %w", roundLabel, errDo)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) status %d", roundLabel, resp.StatusCode)
	}
	rw := newReplayWriter()
	attemptIn, attemptOut, finishEmitted, streamTerminated, sseErr := openAIChatSSEToAnthropicSSEInto(ctx, resp.Body, resp.Body, rw, streamID, model)
	resp.Body.Close()
	// 完整性判定:收到 finish_reason 或 [DONE]/正常 EOF 正常终止,且无上游错误/未 ctx 取消 → 可回放。
	if sseErr == nil && (finishEmitted || streamTerminated) {
		return rw, attemptIn, attemptOut, nil
	}
	// ctx 取消:带上 ctx 错误返回,调用方据此放弃后续重试。
	if ctx != nil && ctx.Err() != nil {
		err := fmt.Errorf("client context cancelled during (%s) round: %w", roundLabel, ctx.Err())
		if sseErr != nil {
			err = fmt.Errorf("%v (last sse err: %v)", err, sseErr)
		}
		return nil, 0, 0, err
	}
	// 超大流保护:蓄流超过 nvidiaReplayMaxBytes 判定为超大输出。
	// 与直连循环不同,helper 当前只服务兜底 1 轮,兜底无可退的边读边写路径,故直接转为 lastErr,
	// 让上层 pullAnthropicStreamWithRetry 把 finalErr 设为兜底失败、落 overloaded_error。
	// 该判定也保护兜底轮不被超大流撑爆内存(与直连循环保护阈值一致,见 nvidiaReplayMaxBytes)。
	if rw.len() > nvidiaReplayMaxBytes {
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) replay oversized %d bytes (exceeds %d), abort fallback round", roundLabel, rw.len(), nvidiaReplayMaxBytes)
	}
	// 不完整:返回原因(断流/未收尾/上游内嵌 error chunk)。
	lastErr := sseErr
	if lastErr == nil {
		lastErr = fmt.Errorf("nvidia upstream (%s) stream incomplete (finishEmitted=%v streamTerminated=%v)", roundLabel, finishEmitted, streamTerminated)
	}
	return nil, 0, 0, lastErr
}

// replyAnthropicOverloaded 回写 Anthropic 标准 overloaded_error 给客户端(Claude Code CLI 据此识别为
// 上游过载/断流失败,可走自身处理逻辑而非把残缺流当正常结束)。取代旧的"补 end_turn 假闭合静默"路径。
// 用 529 状态码对齐 Anthropic 官方 overloaded 语义;响应体为 Anthropic error 结构。
func (h *APICompatHandler) replyAnthropicOverloaded(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable) // 503
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "overloaded_error",
			"message": "NVIDIA upstream stream interrupted and server-side retry exhausted (5x5s, same account).",
		},
	})
	_, _ = w.Write(payload)
}

// recordNvidiaUsage 记录 NVIDIA 用量。
// 一处落点：relayStatsMgr(RelaySample/relay_stats.json，按中继 UserID 分桶，用于中继用户维度统计与按 Key 限额回填)；
// 另一处落点：usageTracker(UsageSample/usage.json，按号池成员账号 AccountMeta 分桶，用于前端“账号使用统计”页，
//   使每个 NVIDIA 号池账号的请求次数/Token/成本/模型可见，与 Gemini/claude 直连链路口径一致)。
// ModelName 在 relayStatsMgr 侧带 "nvidia/" 前缀，使 DB 的 family LIKE 查询("nvidia/") 能命中 NVIDIA 族，
// 不污染 gemini/claude 统计；usageTracker 侧去前缀喂入，前端模型列显示为 upstreamModel(如 z-ai/glm-5.2)，
// pricing 的 fuzzy 匹配仍能按子串(kimi/llama/nemotron)命价。
func (h *APICompatHandler) recordNvidiaUsage(userSession *RelaySession, model string, input, output int, poolAccount *account.Account) {
	if input == 0 && output == 0 {
		return
	}

	// 1) 中继用户维度统计(relay_stats.json) + 按 API Key 限额回填
	if h.statsTracker != nil && userSession != nil {
		prefixedModel := model
		if !strings.HasPrefix(model, "nvidia/") {
			prefixedModel = "nvidia/" + model
		}
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:     fmt.Sprintf("nv-%d", time.Now().UnixNano()),
			UserID:    userSession.UserID,
			UserKey:   userSession.UserKey,
			ModelName: prefixedModel,
			InTokens:  input,
			OutTokens: output,
			Method:    "POST",
			Host:      "nvidia",
			Path:      "/nvidia",
			StatusCode: 200,
		})

		// 单 API Key 的 NVIDIA 用量回填（与 UsedNvidiaTokens 配合形成按 Key 限额）。
		// 与 gemini/claude 链路(app.go proxyHandler 回调 RecordAPIKeyUsage)对齐。
		if h.authMgr != nil && h.authMgr.userMgr != nil && userSession.APIKeyID != "" {
			h.authMgr.userMgr.RecordAPIKeyUsage(userSession.UserID, userSession.APIKeyID, false, int64(input+output))
		}
	}

	// 2) 号池成员账号维度统计(usage.json) —— 复用 Gemini/claude 直连链路同样口径。
	// NVIDIA 上游走 OpenAI Chat 协议，响应 usage 无 cache 概念，CachedTokens 置 0。
	if h.usageTracker == nil || userSession == nil {
		return
	}
	var accMeta *stats.AccountMeta
	if poolAccount != nil {
		accMeta = &stats.AccountMeta{
			ID:        poolAccount.ID,
			Email:     poolAccount.Email,
			Provider:  poolAccount.Provider,
			ProjectID: poolAccount.ProjectID,
			ScopeType: poolAccount.ScopeType,
		}
	}
	displayModel := model
	if strings.HasPrefix(displayModel, "nvidia/") {
		displayModel = strings.TrimPrefix(displayModel, "nvidia/")
	}
	h.usageTracker.RecordUsage(stats.UsageSample{
		ModelName:    displayModel,
		InTokens:     input,
		OutTokens:    output,
		CachedTokens: 0,
		Account:      accMeta,
	})
}

// isAnthropicPassthroughHeader 判断是否为 anthropic 专属头(不透传给客户端)。
func isAnthropicPassthroughHeader(k string) bool {
	switch strings.ToLower(k) {
	case "anthropic-version", "anthropic-beta", "x-api-key", "x-goog-api-key":
		return true
	}
	return false
}

func errStr(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// pickNvidiaAccount 是 NVIDIA 选号统一入口, 兼顾 /nvidia/v1/models 与 /nvidia/{messages,chat/completions,responses} 两处调用点,
// 兼容 sticky 粘性与 round-robin 两种 LB 模式, 统一接入"每账号最近 1 分钟请求计数盘"。
//
// 选号语义:
//   - sticky 模式: 走 sessionRouter.GetOrAssignAccount 保持原哈希粘性语义不变, 仅在选定后 Tick 计数,
//     使其负载被如实记录(跨模式信息不割裂)。
//   - round-robin 模式: 按 nvidiaStats 的最近 1 分钟计数"最少优先", 计数相同时用全局游标
//     nvidiaCursor 取模打破平局。首轮所有账号计数 == 0 时候选集合含全部账号, 退化为原取模轮询,
//     既有行为/测试断言自动兼容。
//
// 选定后无论哪种模式均调用 nvidiaStats.Tick 记录本次占用, 为后续选号提供"窗口内已承担请求次数"信号。
//
// sessionKey / sessionRouter 仅 sticky 路径使用, round-robin 路径不依赖, 允许为空。
// 返回 nil 仅当入参 accounts 为空。
func (h *APICompatHandler) pickNvidiaAccount(lbMode, sessionKey string, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}

	var assigned *account.Account
	if lbMode == "sticky" && h.sessionRouter != nil {
		assigned = h.sessionRouter.GetOrAssignAccount(sessionKey, accounts, h.logFn)
	} else {
		// round-robin 最少计数优先 + 游标打破平局
		ids := make([]string, len(accounts))
		for i, a := range accounts {
			ids[i] = a.ID
		}
		candidates, _ := h.nvidiaStats.pickLeastCountIndex(ids)
		cursor := atomic.AddUint64(&h.nvidiaCursor, 1) - 1
		idx := candidates[int(cursor%uint64(len(candidates)))]
		assigned = accounts[idx]
	}

	if assigned != nil && h.nvidiaStats != nil {
		// 如实记录本次占用, 为突发高并发洪流提供"窗口内已承担次数"信号。
		// 注:即便选号读到的计数已偏高, 这里仍追加记录, 让后续选号感知真实负载。
		h.nvidiaStats.Tick(assigned.ID)
	}
	return assigned
}
