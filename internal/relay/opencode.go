package relay

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
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
	defaultOpenCodeUA       = "opencode/1.18.31"
	opencodeBase62Chars     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var (
	opencodeSessionCounter uint64
	opencodeSessionLastTs  int64
	opencodeSessionMu      sync.Mutex
	opencodeSessionRe      = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
)

// generateOpenCodeSessionID 生成符合 OpenCode Canonical 规范的会话 ID (ses_ + 12 hex + 14 Base62)。
func generateOpenCodeSessionID() string {
	opencodeSessionMu.Lock()
	now := time.Now().UnixMilli()
	if now != opencodeSessionLastTs {
		opencodeSessionLastTs = now
		opencodeSessionCounter = 0
	}
	opencodeSessionCounter++
	cnt := opencodeSessionCounter
	opencodeSessionMu.Unlock()

	current := (uint64(now) * 0x1000) + (cnt & 0x0FFF)
	val := ^current

	var timeBytes [6]byte
	for i := 0; i < 6; i++ {
		shift := 40 - 8*i
		timeBytes[i] = byte((val >> shift) & 0xFF)
	}
	timeHex := hex.EncodeToString(timeBytes[:])

	var randBytes [14]byte
	_, _ = rand.Read(randBytes[:])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for _, b := range randBytes {
		randBase62.WriteByte(opencodeBase62Chars[int(b)%62])
	}

	return fmt.Sprintf("ses_%s%s", timeHex, randBase62.String())
}

// generateOpenCodeRequestID 生成符合 OpenCode Canonical 规范的请求 ID (msg_ + 12 hex + 14 Base62)。
func generateOpenCodeRequestID() string {
	now := time.Now().UnixMilli()
	current := (uint64(now) * 0x1000) + 1
	var timeBytes [6]byte
	for i := 0; i < 6; i++ {
		shift := 40 - 8*i
		timeBytes[i] = byte((current >> shift) & 0xFF)
	}
	timeHex := hex.EncodeToString(timeBytes[:])

	var randBytes [14]byte
	_, _ = rand.Read(randBytes[:])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for _, b := range randBytes {
		randBase62.WriteByte(opencodeBase62Chars[int(b)%62])
	}

	return fmt.Sprintf("msg_%s%s", timeHex, randBase62.String())
}

// translateToOpenCodeSession 将下游客户端的会话标识映射为合法的 OpenCode Canonical 格式。
func translateToOpenCodeSession(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if opencodeSessionRe.MatchString(trimmed) {
		return trimmed
	}
	h := sha256.Sum256([]byte("opencode\x00generic\x00" + trimmed))
	timeHex := hex.EncodeToString(h[:6])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for i := 6; i < 20; i++ {
		randBase62.WriteByte(opencodeBase62Chars[int(h[i])%62])
	}
	return fmt.Sprintf("ses_%s%s", timeHex, randBase62.String())
}

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
		h.log("⛔ [OpenCode 中继] OpenCode 号池无可用账号 (model=%s)", inModel)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "opencode_pool_empty",
				"message": "no available OpenCode account in pool",
			},
		})
		return
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

		if isStreaming && upstreamReq.StreamOptions == nil {
			upstreamReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
		}

		upstreamBytes, _ := json.Marshal(upstreamReq)
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		if baseURL == "" {
			baseURL = account.DefaultOpenCodeBaseURL
		}
		targetURL := baseURL + "/chat/completions"

		httpReq, errReq := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBytes))
		if errReq != nil {
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to build upstream request: " + errReq.Error()})
			return
		}

		// 注入 Canonical 请求头防封指纹
		canonicalSession := translateToOpenCodeSession(sessionKey)
		canonicalRequest := generateOpenCodeRequestID()

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		httpReq.Header.Set("User-Agent", defaultOpenCodeUA)
		httpReq.Header.Set("x-opencode-client", "desktop")
		httpReq.Header.Set("x-opencode-project", "global")
		httpReq.Header.Set("x-opencode-session", canonicalSession)
		httpReq.Header.Set("x-opencode-request", canonicalRequest)
		if isStreaming {
			httpReq.Header.Set("Accept", "text/event-stream")
		} else {
			httpReq.Header.Set("Accept", "application/json")
		}

		client := h.streamClient
		if client == nil {
			client = h.client
		}
		if client == nil {
			client = http.DefaultClient
		}

		resp, errDo := client.Do(httpReq)
		if errDo != nil {
			h.log("⚠️ [OpenCode 中继] 请求失败 (%s): %v, 换号重试", poolAccount.Email, errDo)
			skippedAccounts[poolAccount.ID] = true
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			_ = resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			cooldownUntilMs := time.Now().UnixNano()/1e6 + opencodeCooldownShortMs
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, opencodeChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired {
			bodyErrBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errStr := string(bodyErrBytes)
			skippedAccounts[poolAccount.ID] = true
			if strings.Contains(errStr, "CreditsError") || strings.Contains(errStr, "No payment method") || strings.Contains(errStr, "billing") {
				h.log("⚠️ [OpenCode 中继] 账号欠费/无支付方式 (%s): %s", poolAccount.Email, errStr)
				poolAccount.NoQuota = true
			}
			cooldownUntilMs := time.Now().UnixNano()/1e6 + 24*3600*1000
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, opencodeChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)
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
			h.log("❌ [OpenCode 中继] 上游返回异常状态码 %d (%s): %s", resp.StatusCode, poolAccount.Email, string(errBytes))
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(errBytes)
			return
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

		if isStreaming {
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

	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "all opencode accounts exhausted or failed"})
}
