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

// patchResponsesBodyModel 替换 Responses API 请求体的顶层 model 字段。
//
// 与 patchRoutedBodyModel 的差异: 后者用 map[string]json.RawMessage 保留所有字段,
// 本函数同样保留全部字段(含 input/tools/max_output_tokens/store/include 等 Responses
// 专有字段), 仅覆盖 model。返回 (新body, 是否成功); 解析失败时返回 (nil, false),
// 由调用方回退原始 body。
func patchResponsesBodyModel(body []byte, model string) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, false
	}
	mb, err := json.Marshal(model)
	if err != nil {
		return nil, false
	}
	obj["model"] = mb
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// isOpenCodeResponsesOnlyModel 判定模型是否仅支持 OpenAI Responses API。
//
// 实测(2026-09-27, 对 Zen /v1/models 全量免费模型逐个打两个端点):
//   - muse-spark-1.2-contributor-free / muse-spark-1.3-contributor-free:
//     POST /chat/completions → 400 {"type":"ModelProtocolUnsupported"}
//     POST /responses        → 200 (需带完整 tools, 否则 403 FreeTierError)
//   - jev-1.13-free: 两个端点均 400/403, 上游未开放, 不在此列(避免无效转发)。
//
// 命中该判定的模型, 中继需把上游端点从 /chat/completions 切到 /responses,
// 并保持 Responses 协议透传(不做 Chat 转换), 否则必然 400。
func isOpenCodeResponsesOnlyModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	// muse-spark 系列: 实测 1.2 / 1.3 均仅支持 Responses API。
	return strings.HasPrefix(name, "muse-spark-")
}

// isOpenCodeFreeModel 判定模型是否为 OpenCode Zen 免费额度模型。
//
// 判定依据(2026-09-27 对 Zen /v1/models 与实测):
//   - 免费模型 id 统一以 "-free" 结尾(如 mimo-v2.5-free / ling-3.0-flash-fin-free);
//   - big-pickle 为免费但无 -free 后缀, 需显式列出。
//
// 采用后缀判定而非硬编码全量清单: Zen 会持续新增免费模型(如 jev-1.13-free /
// longcat-2.5-preview-free / space-bunny-free 均晚于代码内 OpenCodeSupportedModels
// 清单出现), 硬编码清单会漏判导致这些模型在非流式请求下被上游 403。
//
// 该判定的唯一用途: 免费模型仅接受 stream=true, 故非流式请求需强制走上游流式
// 并在本地聚合(见 opencode_sse_aggregate.go)。付费模型不受此限, 保持原行为。
func isOpenCodeFreeModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	// 剥离可能存在的 "opencode/" 前缀与变体后缀, 兼容 clientModel 形态传入。
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	if name == "big-pickle" {
		return true
	}
	return strings.HasSuffix(name, "-free")
}

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
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		if baseURL == "" {
			baseURL = account.DefaultOpenCodeBaseURL
		}
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
				if converted, ok := chatToResponsesBody(chatBody, upstreamModel); ok {
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
		// Zen 对免费模型做内容级嗅探 —— tools 必须同时含 bash 与 read(OpenCode agent
		// 的标志性工具对), 否则上游只回一个 response.created 便结束流(空响应)或直接
		// 403。单变量实验: [bash,read]→正常; [read]/[bash]/[bash,edit]/[glob,read] 等
		// 任意组合→仅 120 字节空流。补齐仅用于通过嗅探, 不改变客户端语义。
		if isOpenCodeFreeModel(upstreamModel) {
			if responsesOnly {
				upstreamBytes = ensureResponsesStyleFreeTools(upstreamBytes)
			} else {
				upstreamBytes = ensureChatStyleFreeTools(upstreamBytes)
			}
		}

		httpReq, errReq := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBytes))
		if errReq != nil {
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to build upstream request: " + errReq.Error()})
			return
		}

		// 注入 Canonical 请求头防封指纹（优先透传客户端传入的合法会话头）
		canonicalSession := strings.TrimSpace(r.Header.Get("x-opencode-session"))
		if canonicalSession == "" || !opencodeSessionRe.MatchString(canonicalSession) {
			canonicalSession = translateToOpenCodeSession(sessionKey)
		}
		canonicalRequest := strings.TrimSpace(r.Header.Get("x-opencode-request"))
		if canonicalRequest == "" {
			canonicalRequest = generateOpenCodeRequestID()
		}
		clientType := strings.TrimSpace(r.Header.Get("x-opencode-client"))
		if clientType == "" {
			clientType = "desktop"
		}
		projectID := strings.TrimSpace(r.Header.Get("x-opencode-project"))
		if projectID == "" {
			projectID = "global"
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		httpReq.Header.Set("User-Agent", defaultOpenCodeUA)
		httpReq.Header.Set("x-opencode-client", clientType)
		httpReq.Header.Set("x-opencode-project", projectID)
		httpReq.Header.Set("x-opencode-session", canonicalSession)
		httpReq.Header.Set("x-opencode-request", canonicalRequest)
		// Accept 头按**上游实际协议**设置, 而非客户端期望形态:
		// 免费模型/Responses-only 模型即便客户端要非流式, 上游仍必须是流式,
		// 此时若发 Accept: application/json 会与 stream=true 语义冲突, 部分上游据此拒绝。
		if upstreamStreaming {
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
			cooldownUntilMs := time.Now().UnixNano()/1e6 + 24*3600*1000
			// 模型级细粒度隔离：仅针对当前模型打上冷却，不污染 poolAccount.NoQuota，保障免费与可用模型不被误杀
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, opencodeChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)

			if strings.Contains(errStr, "CreditsError") || strings.Contains(errStr, "No payment method") || strings.Contains(errStr, "billing") {
				h.log("⚠️ [OpenCode 中继] 模型欠费/无支付方式 (%s, model=%s): %s", poolAccount.Email, inModel, errStr)
			}
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
			// Responses-only 模型: 上游回的是 Responses SSE, 但下游三个回写路径
			// (Anthropic / Responses / OpenAI 直出) 都以 Chat 结构为输入。
			// 故先聚合为 Chat 响应, 再复用既有回写逻辑 —— 避免为每种协议各写一份
			// Responses SSE 转换器, 也保证 reasoning/tool_calls 语义一致。
			if responsesOnly {
				aggResp, aggErr := aggregateOpenAIResponsesSSE(resp.Body, upstreamModel)
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				if aggErr != nil {
					if uerr, ok := aggErr.(*openCodeUpstreamError); ok {
						status := http.StatusBadGateway
						if uerr.Type == "FreeTierError" {
							status = http.StatusForbidden
							h.log("⛔ [OpenCode 中继] Responses 模型上游拒绝: %s (model=%s)", uerr.Message, inModel)
						}
						writeJSON(w, status, map[string]interface{}{
							"type":  "error",
							"error": map[string]interface{}{"type": uerr.Type, "message": uerr.Message},
						})
						return
					}
					writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to aggregate responses stream: " + aggErr.Error()})
					return
				}
				inT := aggResp.Usage.PromptTokens
				outT := aggResp.Usage.CompletionTokens
				cachedT := aggResp.Usage.CachedTokens()
				h.recordOpenCodeUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)

				// 客户端要求流式时, 必须输出 SSE 流而非 JSON —— 否则客户端会判定
				// "流断了"并反复重试(实测真实 opencode CLI 走 Anthropic 入站时,
				// 收到 JSON 会持续重发请求直至超时)。
				// 做法: 把聚合结果重建为 Chat SSE, 复用既有协议转换器输出,
				// 保证事件序列与 usage 口径与常规路径完全一致。
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
					sseBytes := rebuildChatSSEFromAggregated(aggResp)
					bw := bufio.NewWriter(w)
					inTokens := estimateInputTokensFromBody(bodyBytes)
					_, _, _, _ = OpenAIChatSSEToAnthropicSSE(r.Context(), bytes.NewReader(sseBytes), io.NopCloser(bytes.NewReader(sseBytes)), bw, upstreamModel, inTokens, flusher)
					_ = bw.Flush()
					return
				}
				if inboundResponses {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(http.StatusOK)
					flusher, _ := w.(http.Flusher)
					if flusher != nil {
						flusher.Flush()
					}
					sseBytes := rebuildChatSSEFromAggregated(aggResp)
					fw := newFlushWriter(fmt.Sprintf("oc_%d", time.Now().UnixNano()), bufio.NewWriter(w), flusher)
					_, _, _ = OpenAIChatSSEToResponsesSSE(r.Context(), bytes.NewReader(sseBytes), io.NopCloser(bytes.NewReader(sseBytes)), fw, upstreamModel)
					fw.flush()
					return
				}
				// 客户端入站即 OpenAI Chat + 要求流式: 直接回重建的 Chat SSE。
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(rebuildChatSSEFromAggregated(aggResp))
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
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

	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "all opencode accounts exhausted or failed"})
}
