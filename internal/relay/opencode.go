package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// opencode.go: OpenCode Zen 聚合网关号池中继处理器。
//
// 核心逻辑:
//   - 入口支持 /opencode/* (别名 /oc/*) 以及 /route/* 模型路由派发。
//   - 适配协议: 入站兼容 OpenAI Chat、Anthropic Messages、Codex Responses。
//   - 防封与指纹规整: 上游服务端对客户端请求头实施强指纹校验，自动规整注入：
//       User-Agent: opencode/1.18.31
//       x-opencode-client: desktop
//       x-opencode-project: global
//       x-opencode-session: ses_[12-hex-descending][14-base62]
//       x-opencode-request: msg_[12-hex][14-base62]
//   - 选号与熔断: 支持 sticky 亲和与 round-robin 轮询，支持在途并发上限与 429 自动冷却。

const (
	opencodeChannel         = "opencode"
	opencodeMaxAttemptsCap  = 5
	opencodeCooldownShortMs = 60 * 1000
	defaultOpenCodeUA       = "opencode/1.19.4 ai-sdk/provider-utils/4.0.40 runtime/bun/1.4.4"
	opencodeBase62Chars     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// pickOpenCodeAccount 结合 sticky 与 round-robin 游标从可用列表中选号。
func (h *APICompatHandler) pickOpenCodeAccount(lbMode, sessionKey string, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}
	if lbMode == "sticky" && h.sessionRouter != nil {
		assigned := h.sessionRouter.GetOrAssignAccount(sessionKey, accounts, h.logFn)
		if assigned != nil {
			return assigned
		}
	}
	cursor := atomic.AddUint64(&h.opencodeCursor, 1) - 1
	idx := int(cursor % uint64(len(accounts)))
	return accounts[idx]
}

// handleOpenCodeModels 处理 /opencode/v1/models 与 /oc/v1/models。
func (h *APICompatHandler) handleOpenCodeModels(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	seen := make(map[string]bool)
	var models []map[string]interface{}

	addModel := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		models = append(models, map[string]interface{}{
			"id":       id,
			"object":   "model",
			"created":  1700000000,
			"owned_by": "opencode",
		})
	}

	for _, m := range account.OpenCodeSupportedModels {
		addModel(m)
		addModel("opencode/" + m)
	}

	if h.accountMgr != nil {
		for _, acc := range h.accountMgr.GetAccounts() {
			if acc != nil && acc.Provider == "opencode" && acc.DefaultModel != "" {
				addModel(acc.DefaultModel)
				addModel("opencode/" + acc.DefaultModel)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// handleOpenCode 处理 /opencode/*, /oc/* 及 /route/* 命中 opencode 的所有请求。
func (h *APICompatHandler) handleOpenCode(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method == http.MethodGet || path == "/opencode/v1/models" || path == "/oc/v1/models" || strings.HasSuffix(path, "/models") {
		h.handleOpenCodeModels(w, r, userSession)
		return
	}

	bodyBytes, err := readBodyWithTimeout(r, nvidiaInboundReadTimeout)
	if err != nil {
		if errors.Is(err, ErrBodyReadTimeout) {
			kind := inboundKindOfPath(path)
			writeNvidiaInboundTimeout(w, kind)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	h.ensureSessionKey(userSession, r, bodyBytes)

	inboundAnthropic := strings.HasSuffix(path, "/v1/messages") || strings.HasSuffix(path, "/messages")
	inboundResponses := strings.HasSuffix(path, "/v1/responses") || strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
	inboundOpenAI := strings.HasSuffix(path, "/v1/chat/completions") || strings.HasSuffix(path, "/chat/completions")

	if strings.HasSuffix(path, "/v1/messages/count_tokens") || strings.HasSuffix(path, "/messages/count_tokens") {
		h.handleNvidiaCountTokens(w, bodyBytes)
		return
	}
	if !inboundAnthropic && !inboundResponses && !inboundOpenAI {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "unsupported opencode endpoint: use /opencode/v1/chat/completions or /opencode/v1/messages",
		})
		return
	}

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

	available := h.accountMgr.GetAvailableAccountsForChannel(opencodeChannel, inModel)
	if len(available) == 0 {
		isBenchmark := r.Header.Get("X-Antigravity-Benchmark") == "1"
		allAccounts := h.accountMgr.GetAccounts()
		var opencodeAccs []*account.Account
		var minCooldownRemaining int64 = 0
		nowMs := time.Now().UnixNano() / int64(time.Millisecond)

		for _, a := range allAccounts {
			if a.Provider == opencodeChannel && a.Enabled && !a.IsAuthExpired() {
				opencodeAccs = append(opencodeAccs, a)
				cat := h.accountMgr.GetModelCategoryByProvider(opencodeChannel, inModel)
				cd := int64(0)
				if a.Cooldowns != nil {
					if v, ok := a.Cooldowns[cat]; ok {
						cd = v
					} else if v, ok := a.Cooldowns[opencodeChannel]; ok {
						cd = v
					}
				} else {
					cd = a.CooldownUntil
				}
				if cd > nowMs {
					rem := (cd - nowMs) / 1000
					if minCooldownRemaining == 0 || rem < minCooldownRemaining {
						minCooldownRemaining = rem
					}
				}
			}
		}

		if isBenchmark && len(opencodeAccs) > 0 {
			// 测速探活特权：当号池中所有账号均因 429 冷却时，测速回环仍允许发起探活，以侦测上游是否已解封
			available = opencodeAccs
		} else if len(opencodeAccs) > 0 {
			// 存在账号但在冷却中：返回 429 限流并携带重试秒数，避免抛出误导性的 503
			if minCooldownRemaining <= 0 {
				minCooldownRemaining = 5
			}
			h.log("⏳ [OpenCode 中继] 号池所有账号均处于冷却中 (model=%s, retryAfter=%ds)", inModel, minCooldownRemaining)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", minCooldownRemaining))
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "opencode_pool_cooldown",
					"message": fmt.Sprintf("all OpenCode accounts are currently in cooldown, please retry after %d seconds", minCooldownRemaining),
					"code":    "rate_limit_exceeded",
				},
			})
			return
		} else {
			// 真正无任何已配置或启用的账号
			h.log("⛔ [OpenCode 中继] OpenCode 号池无已配置或启用的账号 (model=%s)", inModel)
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "opencode_pool_empty",
					"message": "no available OpenCode account configured in pool",
				},
			})
			return
		}
	}

	sessionKey := h.stickyKeyOf(userSession)
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetOpenCodeLBMode()
	}
	maxAttempts := len(available)
	if maxAttempts > opencodeMaxAttemptsCap {
		maxAttempts = opencodeMaxAttemptsCap
	}
	skippedAccounts := make(map[string]bool)
	var lastErrCode int
	var lastErrBody []byte

	startTs := time.Now()
	firstByteRec := stats.NewFirstByteRecorder(startTs)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		var activeAvailable []*account.Account
		for _, a := range available {
			if !skippedAccounts[a.ID] {
				activeAvailable = append(activeAvailable, a)
			}
		}
		if len(activeAvailable) == 0 {
			break
		}

		var poolAccount *account.Account
		limit := 10
		if h.accountMgr != nil {
			limit = h.accountMgr.GetOpenCodeMaxConcurrency()
		}
		filtered := activeAvailable
		if h.accountMgr != nil {
			filtered = h.accountMgr.FilterByConcurrency(activeAvailable, limit)
		}
		if len(filtered) > 0 {
			poolAccount = h.pickOpenCodeAccount(lbMode, sessionKey, filtered)
		} else if h.accountMgr != nil {
			overAcc := h.accountMgr.LeastLoadedAccount(activeAvailable)
			if overAcc != nil {
				poolAccount = overAcc
				h.log("⚠️ [并发限制] OpenCode 池并发全满(限 %d), 超额降级到最少并发号 %s", limit, overAcc.Email)
			}
		}
		if poolAccount == nil {
			break
		}

		if h.accountMgr != nil {
			h.accountMgr.AcquireAccount(poolAccount.ID)
		}
		upstreamModel := account.ResolveOpenCodeModel(inModel, poolAccount)

		// 构造上游 OpenAI Chat 请求体
		var upstreamReq *OpenAIChatRequest
		mappings := h.getRelayModelMappingSafe()

		if inboundAnthropic {
			var anthReq AnthropicRequest
			_ = json.Unmarshal(bodyBytes, &anthReq)
			anthReq.UserAgent = r.Header.Get("User-Agent")
			anthReq.Model = upstreamModel
			u, err := AnthropicToOpenAIChatPreservingImagesForProvider(&anthReq, false, "opencode", mappings)
			if err != nil {
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "anthropic to openai failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else if inboundResponses {
			u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
			if err != nil {
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "responses to openai failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else {
			var chatReq OpenAIChatRequest
			_ = json.Unmarshal(bodyBytes, &chatReq)
			chatReq.Model = upstreamModel
			upstreamReq = &chatReq
		}

		// 免费模型仅接受 stream=true(实测: stream=false 恒被上游 403 FreeTierError)。
		// 故客户端发非流式请求而目标为免费模型时, 强制向上游发流式, 拿到 SSE 后
		// 由本层聚合回非流式响应, 下游回写路径与协议转换逻辑完全复用。
		// upstreamStreaming 决定发往上游的 stream 字段; isStreaming 仍表示客户端期望形态。
		upstreamStreaming := isStreaming
		needAggregate := false
		if !isStreaming && isOpenCodeFreeModel(upstreamModel) {
			upstreamStreaming = true
			needAggregate = true
		}

		if upstreamStreaming {
			upstreamReq.Stream = true
			if upstreamReq.StreamOptions == nil {
				upstreamReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
		}

		upstreamBytes, _ := json.Marshal(upstreamReq)
		baseURL := account.NormalizeOpenCodeBaseURL(poolAccount.BaseURL)
		targetURL := baseURL + "/chat/completions"

		// Responses-only 模型(如 muse-spark-*): 上游仅接受 Responses API,
		// 打 /chat/completions 恒返回 400 ModelProtocolUnsupported。
		// 故改用 /responses 端点, 并直接透传客户端原始 body(仅替换 model 字段),
		// 不做 Chat 协议转换 —— 保留 Responses 形态的 input/tools/max_output_tokens 等字段。
		responsesOnly := isOpenCodeResponsesOnlyModel(upstreamModel)
		if responsesOnly {
			targetURL = baseURL + "/responses"
			// body 来源分两种:
			//   - 客户端本就是 Responses 格式(inboundResponses): 直接透传原始 body,
			//     仅替换 model 字段, 保留 input/tools/max_output_tokens 等专有字段;
			//   - 客户端是 Chat/Anthropic 格式: 需把 Chat body 转成 Responses 形态,
			//     否则上游报 "unknown parameter `max_tokens`" 等字段错误。
			if inboundResponses {
				if patched, ok := patchResponsesBodyModel(bodyBytes, upstreamModel); ok {
					upstreamBytes = patched
				} else {
					upstreamBytes = bodyBytes
				}
			} else {
				chatBody, _ := json.Marshal(upstreamReq)
				if converted, ok := chatToResponsesBody(chatBody, upstreamModel, translateToOpenCodeSession(sessionKey)); ok {
					upstreamBytes = converted
				} else {
					upstreamBytes = chatBody
				}
			}
			// Responses API 恒为流式驱动; 客户端若要非流式, 本地聚合后回写。
			upstreamStreaming = true
			needAggregate = !isStreaming
		}

		// 免费模型工具集补齐(实测 2026-09-27):
		// Zen 对免费模型做内容级嗅探 —— 请求体的 tools 必须覆盖 OpenCode agent 的
		// 核心工具集(bash/read/edit/glob/write/websearch/webfetch/task/todowrite/skill),
		// 否则上游拒绝服务:
		//   - 缺核心工具 → 1.1 秒内返回空流(0 delta, 无 completed), 或直接 403 FreeTierError;
		//   - 覆盖核心集 → 正常返回完整响应。
		// 实测关键案例: CLI 的 58 个工具(缺 websearch)被 403, 补齐后立即恢复。
		// 补齐仅用于通过上游嗅探, 不改变客户端语义。
		if isOpenCodeFreeModel(upstreamModel) {
			if responsesOnly {
				upstreamBytes = ensureResponsesStyleFreeTools(upstreamBytes)
			} else {
				upstreamBytes = ensureChatStyleFreeTools(upstreamBytes)
			}
			var pb struct {
				Tools []struct {
					Name string `json:"name"`
					Fn   struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(upstreamBytes, &pb)
			ns := make([]string, 0, len(pb.Tools))
			for _, t := range pb.Tools {
				n := t.Name
				if n == "" {
					n = t.Fn.Name
				}
				ns = append(ns, n)
			}
		}

		httpReq, errReq := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBytes))
		if errReq != nil {
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to build upstream request: " + errReq.Error()})
			return
		}

		// 注入 Canonical 请求头防封指纹（与官方 CLI 源码及监控 1:1 对齐）
		canonicalSession := strings.TrimSpace(r.Header.Get("x-opencode-session-id"))
		if canonicalSession == "" {
			canonicalSession = strings.TrimSpace(r.Header.Get("x-opencode-session"))
		}
		if canonicalSession == "" || !opencodeSessionRe.MatchString(canonicalSession) {
			canonicalSession = translateToOpenCodeSession(sessionKey)
		}
		clientType := strings.TrimSpace(r.Header.Get("x-opencode-client"))
		if clientType == "" || clientType == "desktop" {
			clientType = "cli"
		}
		projectID := strings.TrimSpace(r.Header.Get("x-opencode-project"))
		if projectID == "" {
			projectID = strings.TrimSpace(poolAccount.ProjectID)
		}
		if projectID == "" {
			projectID = "global"
		}
		canonicalRequest := strings.TrimSpace(r.Header.Get("x-opencode-request"))
		if canonicalRequest == "" {
			canonicalRequest = generateOpenCodeRequestID()
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		httpReq.Header.Set("User-Agent", defaultOpenCodeUA)
		httpReq.Header.Set("x-opencode-client", clientType)
		httpReq.Header.Set("x-opencode-project", projectID)
		// 官方 CLI 源码证实：x-opencode-session 与 x-opencode-session-id 必须同时存在且值完全相同
		httpReq.Header.Set("x-opencode-session", canonicalSession)
		httpReq.Header.Set("x-opencode-session-id", canonicalSession)
		httpReq.Header.Set("x-opencode-request", canonicalRequest)
		// 官方 CLI 监控实测统一为 Accept: */* (上游据此及 UA/cli 判定为官方 CLI 高优先级配额通道)
		httpReq.Header.Set("Accept", "*/*")

		client := h.streamClient
		if client == nil {
			client = h.client
		}
		if client == nil {
			client = http.DefaultClient
		}
		// opencode 上游直连: 显式禁用代理。
		// 动机(2026-09-27 实测): 共享 transport 的 GetSystemProxy 在系统代理为空时会
		// 降级使用探测到的本地 VPN 代理端口; 长流经该代理转发时会在数十秒后被中断,
		// 表现为 "http2: response body closed" / "unexpected EOF", 下游则收不到
		// response.completed, 客户端报 InvalidHTTPResponse 或内容被截断。
		// opencode.ai 为公网可达域名, 直连即可, 无需经本地代理。
		client = opencodeDirectClient

		resp, errDo := client.Do(httpReq)
		if resp != nil {
		}
		if errDo != nil {
			h.log("⚠️ [OpenCode 中继] 请求失败 (%s): %v, 换号重试", poolAccount.Email, errDo)
			skippedAccounts[poolAccount.ID] = true
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			bodyErrBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errStr := string(bodyErrBytes)
			skippedAccounts[poolAccount.ID] = true
			cooldownUntilMs := time.Now().UnixNano()/1e6 + opencodeCooldownShortMs
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, opencodeChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			h.log("⚠️ [OpenCode 中继] 429 配额上限或限流 (%s, model=%s): %s", poolAccount.Email, inModel, errStr)
			lastErrCode = http.StatusTooManyRequests
			lastErrBody = bodyErrBytes
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired {
			bodyErrBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errStr := string(bodyErrBytes)
			skippedAccounts[poolAccount.ID] = true
			cooldownUntilMs := time.Now().UnixNano()/1e6 + 24*3600*1000
			// 模型级细粒度隔离：仅针对当前模型打上冷却，不污染 poolAccount.NoQuota，保障免费与可用模型不被误杀
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, opencodeChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)

			if strings.Contains(errStr, "CreditsError") || strings.Contains(errStr, "No payment method") || strings.Contains(errStr, "billing") {
				h.log("⚠️ [OpenCode 中继] 模型欠费/无支付方式 (%s, model=%s): %s", poolAccount.Email, inModel, errStr)
			}
			lastErrCode = resp.StatusCode
			lastErrBody = bodyErrBytes
			continue
		}

		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			errBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			h.log("❌ [OpenCode 中继] 上游返回异常状态码 %d (%s, model=%s): %s", resp.StatusCode, poolAccount.Email, inModel, string(errBytes))
			h.accountMgr.ReleaseAccount(poolAccount.ID)

			errStr := string(errBytes)
			if strings.Contains(errStr, "FreeTierError") || strings.Contains(errStr, "OpenCode's free tier") {
				h.log("⛔ [OpenCode 中继] 上游拒绝 Free Tier 外部调用: OpenCode 官方已限制免费模型仅限官方客户端内部调用。建议使用 OpenCode 付费模型或在客户端直连。")
			}
			if strings.Contains(errStr, "Model is unavailable") {
				h.log("⛔ [OpenCode 中继] 上游提示模型已下线 (%s)", inModel)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(errBytes)
			return
		}

		if h.accountMgr != nil && (poolAccount.CooldownUntil > 0 || len(poolAccount.Cooldowns) > 0) {
			h.accountMgr.ClearAccountCooldown(poolAccount.ID)
		}

		logCtx := opencodeLogCtx{
			Method:       r.Method,
			Host:         opencodeHostFromBaseURL(baseURL),
			Path:         path,
			SessionID:    canonicalSession,
			Account:      poolAccount.Email,
			StatusCode:   http.StatusOK,
			StartTs:      startTs,
			FirstByteRec: firstByteRec,
			ReqBody:      upstreamReq,
		}

		// needAggregate: 客户端要非流式, 但免费模型只接受流式, 故上游按流式请求、
		// 本地聚合为完整 OpenAI Chat 响应, 再走下方既有非流式回写路径
		// (OpenAIChatToAnthropic / OpenAIChatToResponses), 下游协议转换零改动复用。
		if needAggregate {
			// 按上游端点选择聚合器: /responses 的事件模型与 Chat SSE 不同, 需分别解析。
			var aggResp *OpenAIChatResponse
			var aggErr error
			if responsesOnly {
				aggResp, aggErr = aggregateOpenAIResponsesSSE(resp.Body, upstreamModel)
			} else {
				aggResp, aggErr = aggregateOpenAIChatSSE(resp.Body, upstreamModel)
			}
			_ = resp.Body.Close()
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			if aggErr != nil {
				if uerr, ok := aggErr.(*openCodeUpstreamError); ok {
					status := http.StatusBadGateway
					if uerr.Type == "FreeTierError" {
						status = http.StatusForbidden
						h.log("⛔ [OpenCode 中继] 免费模型流式聚合时上游拒绝: %s (model=%s)", uerr.Message, inModel)
					}
					writeJSON(w, status, map[string]interface{}{
						"type":  "error",
						"error": map[string]interface{}{"type": uerr.Type, "message": uerr.Message},
					})
					return
				}
				writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to aggregate upstream stream: " + aggErr.Error()})
				return
			}

			inT := aggResp.Usage.PromptTokens
			outT := aggResp.Usage.CompletionTokens
			cachedT := aggResp.Usage.CachedTokens()
			h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)

			if inboundAnthropic {
				anthResp := OpenAIChatToAnthropic(aggResp)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(anthResp)
				return
			}
			if inboundResponses {
				respResp := OpenAIChatToResponses(aggResp, upstreamModel)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(respResp)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(aggResp)
			return
		}

		if isStreaming {
			if responsesOnly {
				// Responses-only 模型: 上游回 Responses SSE, 下游三种协议都需 Chat 语义。
				//
				// 关键(2026-09-27 修复): 必须**逐帧实时转换**, 不可先聚合再一次性发出。
				// 实测长输出请求(约 22K 字符)在聚合路径下出现 172 秒零输出、随后内容
				// 瞬间涌出 —— 客户端表现为"卡死"; 若中途连接抖动则表现为"输出一半停住"。
				// 实时转换让客户端逐字收到内容, 且上游中断时已产出内容不丢失。
				//
				// 做法: 把 Responses SSE 实时转成 Chat SSE 写入管道, 再由既有成熟转换器
				// (OpenAIChatSSEToAnthropicSSE / OpenAIChatSSEToResponsesSSE) 完成协议转换。
				pr, pw := io.Pipe()

				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				if flusher != nil {
					flusher.Flush()
				}

				// 上游 Responses SSE → Chat SSE, 逐帧写入 pw。
				// 放在 goroutine, 与下方下游转换器并行消费 pr, 形成实时管道。
				// 关键: 保活 ping 与转换器 SSE 帧必须共用**同一个 sink**(含同一把锁)。
				// flushWriter.writeEvent 分两次 WriteString 落一帧, 其 mu 保证帧原子性;
				// 若 ping 用另一把锁直写 w, 会插进半帧中间, 客户端报
				// "Malformed encoding found in chunked-encoding"(实测)。
				// 故在此创建 flushWriter, 交给转换器使用, 保活也调用它的 pingFrame()。
				ocStreamID := fmt.Sprintf("oc_%d", time.Now().UnixNano())
				ocSink := newFlushWriter(ocStreamID, bufio.NewWriter(w), flusher)
				convErrCh := make(chan error, 1)
				go func() {
					_, _, _, cErr := responsesSSEToChatSSE(resp.Body, pw, upstreamModel, func() {
						// flush 必须走 ocSink: 它持有转换器实际写入的 bufio,
						// 直接调 flusher.Flush() 只会刷底层 socket, bufio 内数据仍滞留。
						ocSink.flush()
					})
					// 必须关闭 pw: 下游转换器阻塞在 pr.Read 上, 只有 pw 关闭才能解除。
					// 用 CloseWithError 传递错误语义(io.EOF 视为正常结束)。
					if cErr != nil {
						_ = pw.CloseWithError(cErr)
					} else {
						_ = pw.Close()
					}
					convErrCh <- cErr
				}()

				// 关键: 传给转换器的 body 必须能同时关闭
				//   ① 上游响应体 resp.Body —— 使读上游的 goroutine 立即解除阻塞;
				//   ② 管道两端 —— 使下游读与生产端写都解除阻塞。
				// 转换器内部用 watchCancel(ctx, body): 客户端断开时 Close(body)。
				// 若只关管道而不关上游客, 生产端仍卡在读上游; 若只关上游而不关管道,
				// 下游会卡在 pr.Read —— 两者都会导致流无法收尾(实测 unexpected EOF)。
				pipeBody := &pipeReadCloser{pr: pr, pw: pw}

				// 用带锁的 writer 串行化「保活 ping」与「转换器 SSE 帧」的写入。
				// 转换器的 writeSSEFrame 分两次 WriteString(event) / WriteString(data),
				// 若 ping 插在两者之间, 客户端读到半帧并报
				// "chunk hex-length char not a hex digit"(HTTP chunked 解析失败)。
				// 加锁后每帧原子落盘。
				// 保活: reasoning 模型思考阶段可达 30-90 秒无任何 delta。
				// 下游转换器的 heartbeatWatchdog 仅在"已产出过至少一个 content_block"后才
				// 注入 ping(见其注释), 思考期无 delta 故不会 ping; 客户端会因长时间静默
				// 判定流异常并断开(实测 60-120 秒处 context canceled)。
				//
				// 实现要点: 心跳**直接写客户端 w**, 不注入管道。
				// 反例(实测): 向管道注入空 delta 帧会被下游转换器静默吞掉 ——
				// 转换器仅对非空 content/reasoning/tool_calls 产生输出, 空帧无任何写出,
				// 客户端依旧长时间无数据。
				// 直接写 w 需与转换器的 bufio 缓冲协调: 下方 keepAliveWrite 会在每次
				// 写前先 flush 转换器缓冲(通过 keepAliveFlush 回调), 避免帧交错。
				keepAliveDone := make(chan struct{})
				go func() {
					tk := time.NewTicker(2 * time.Second)
					defer tk.Stop()
					for {
						select {
						case <-keepAliveDone:
							return
						case <-r.Context().Done():
							return
						case <-tk.C:
							// 经 ocSink.pingFrame() 发送 Anthropic ping 心跳。
							//
							// 必须与转换器共用同一个 sink: flushWriter.writeEvent 分两次
							// WriteString 落一帧, 其内部 mu 保证帧原子性; 若心跳用另一把锁
							// 直写 w, 会插进半帧中间, 客户端报
							// "Malformed encoding found in chunked-encoding"(实测)。
							ocSink.pingFrame()
						}
					}
				}()

				var inT, outT, cachedT int
				if inboundAnthropic {
					inTokens := estimateInputTokensFromBody(bodyBytes)
					inT, outT, cachedT, _, _, _, _ = openAIChatSSEToAnthropicSSEIntoPinned(
						r.Context(), pr, pipeBody, ocSink, ocStreamID, upstreamModel, inTokens, nil)
					ocSink.flush()
				} else if inboundResponses {
					fw := newFlushWriter(fmt.Sprintf("oc_%d", time.Now().UnixNano()), bufio.NewWriter(w), flusher)
					inT, outT, cachedT = OpenAIChatSSEToResponsesSSE(r.Context(), pr, pipeBody, fw, upstreamModel)
					fw.flush()
				} else {
					// 入站即 OpenAI Chat: pr 已是 Chat SSE, 逐块透传并 flush。
					bw := bufio.NewWriter(w)
					buf := make([]byte, 4096)
					for {
						n, rErr := pr.Read(buf)
						if n > 0 {
							if _, wErr := bw.Write(buf[:n]); wErr != nil {
								break
							}
							_ = bw.Flush()
							if flusher != nil {
								flusher.Flush()
							}
						}
						if rErr != nil {
							break
						}
					}
					_ = bw.Flush()
				}

				// 关键顺序: 必须**先等生产端 goroutine 结束**, 再关闭上游响应体。
				// 反例(实测 2026-09-27): 若在等待前就 Close(resp.Body), 生产端仍在读上游,
				// 会立即收到 "http2: response body closed" 而中断, 导致:
				//   - 已产出内容被截断(实测 45 秒处断流);
				//   - 下游收不到 response.completed, 缺少 content_block_stop/message_stop;
				//   - 客户端报 InvalidHTTPResponse。
				// 生产端 goroutine 在读到上游 EOF 或错误后会自行退出, 故先等待是安全的;
				// 超时兜底用于上游长时间不结束时强制收尾。
				var convErr error
				select {
				case convErr = <-convErrCh:
				case <-time.After(180 * time.Second):
					h.log("⚠️ [OpenCode 中继] Responses 流转换超时(180s), 强制结束 (model=%s)", inModel)
					// 超时兜底: 关闭上游与管道, 唤醒所有阻塞方。
					_ = resp.Body.Close()
					_ = pw.CloseWithError(io.ErrClosedPipe)
					select {
					case convErr = <-convErrCh:
					case <-time.After(5 * time.Second):
					}
				}
				close(keepAliveDone)
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)

				if convErr != nil {
					if uerr, ok := convErr.(*openCodeUpstreamError); ok && uerr.Type == "FreeTierError" {
						h.log("⛔ [OpenCode 中继] Responses 模型上游拒绝: %s (model=%s)", uerr.Message, inModel)
					}
					// 流已开始写出, 无法再改状态码; 仅记录日志。
				}
				h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			}

			if inboundAnthropic {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				if flusher != nil {
					flusher.Flush()
				}
				bw := bufio.NewWriter(w)
				inTokens := estimateInputTokensFromBody(bodyBytes)
				inT, outT, cachedT, _ := OpenAIChatSSEToAnthropicSSE(r.Context(), resp.Body, resp.Body, bw, upstreamModel, inTokens, flusher)
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			} else if inboundResponses {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				flusher, _ := w.(http.Flusher)
				if flusher != nil {
					flusher.Flush()
				}
				fw := newFlushWriter(fmt.Sprintf("oc_%d", time.Now().UnixNano()), bufio.NewWriter(w), flusher)
				inT, outT, cachedT := OpenAIChatSSEToResponsesSSE(r.Context(), resp.Body, resp.Body, fw, upstreamModel)
				fw.flush()
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			} else {
				inT, outT, cachedT := h.proxyNvidiaOpenAIPassthrough(r.Context(), w, resp, true, firstByteRec)
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			}
		}

		// 非流式响应处理
		respBytes, rErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		h.accountMgr.ReleaseAccount(poolAccount.ID)
		if rErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to read upstream response: " + rErr.Error()})
			return
		}

		var chatResp OpenAIChatResponse
		if err := json.Unmarshal(respBytes, &chatResp); err == nil {
			inT := chatResp.Usage.PromptTokens
			outT := chatResp.Usage.CompletionTokens
			cachedT := chatResp.Usage.CachedTokens()
			h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)

			if inboundAnthropic {
				anthResp := OpenAIChatToAnthropic(&chatResp)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(anthResp)
				return
			} else if inboundResponses {
				respResp := OpenAIChatToResponses(&chatResp, upstreamModel)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(respResp)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
		return
	}

	if lastErrCode > 0 && len(lastErrBody) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastErrCode)
		_, _ = w.Write(lastErrBody)
		return
	}

	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "all opencode accounts exhausted or failed"})
}
