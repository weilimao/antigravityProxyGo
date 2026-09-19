package relay

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// workbuddy.go: 腾讯 WorkBuddy 免费模型号池中继处理器。
//
// 核心逻辑:
//   - 入口支持 /workbuddy/* (别名 /wb/*) 以及 /route/* 模型路由派发。
//   - 适配协议: 入站兼容 OpenAI Chat、Anthropic Messages、Codex Responses。
//   - 突破限制一: 远端仅支持 stream: true，请求上游时强制 stream: true；若客户端为非流式，
//     在内存中消费 SSE 流并聚合生成完整 JSON 回写。
//   - 突破限制二: 远端首条消息必须是 role: "system"，若缺少则自动在首位注入默认 system prompt。
//   - 请求头注入: 包含 X-IDE-Type: CodeBuddy, X-IDE-Name: WorkBuddy AI, X-IDE-Version: 5.5.2 等必要头。

const (
	workbuddyChannel           = "workbuddy"
	workbuddyMaxAttemptsCap    = 5
	workbuddyQuotaRetryWaitMs  = 5 * 1000
	workbuddyCooldownShortMs   = 60 * 1000
	defaultWorkBuddySystemText = "You are a helpful assistant."
)

// generateWorkBuddyUUID 生成符合 RFC 4122 v4 规范的唯一 UUID。
func generateWorkBuddyUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ensureWorkBuddySystemPrompt 确保请求 messages 首条为 system prompt。
func ensureWorkBuddySystemPrompt(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	if len(req.Messages) == 0 {
		req.Messages = []ChatMessage{
			{Role: "system", Content: defaultWorkBuddySystemText},
		}
		return
	}
	if !strings.EqualFold(req.Messages[0].Role, "system") {
		newMessages := make([]ChatMessage, 0, len(req.Messages)+1)
		newMessages = append(newMessages, ChatMessage{
			Role:    "system",
			Content: defaultWorkBuddySystemText,
		})
		newMessages = append(newMessages, req.Messages...)
		req.Messages = newMessages
	}
}

var reWorkBuddyBillingHeader = regexp.MustCompile(`(?i)x-anthropic-billing-header[^\r\n]*[\r\n]*`)

// sanitizeWorkBuddyText 统一脱敏字符串中触碰 WorkBuddy 安全策略（11128）的客户端渠道特征。
func sanitizeWorkBuddyText(text string) string {
	if text == "" {
		return ""
	}
	if reWorkBuddyBillingHeader.MatchString(text) {
		text = reWorkBuddyBillingHeader.ReplaceAllString(text, "")
	}
	if strings.Contains(strings.ToLower(text), "x-anthropic-billing-header") {
		// 兜底清除无换行或特殊分隔符的残留标记
		reFallback := regexp.MustCompile(`(?i)x-anthropic-billing-header[a-zA-Z0-9_=:;.\- ]*`)
		text = reFallback.ReplaceAllString(text, "")
	}
	if strings.Contains(text, "You are Claude Code, Anthropic's official CLI for Claude.") {
		text = strings.ReplaceAll(text, "You are Claude Code, Anthropic's official CLI for Claude.", "You are an interactive AI assistant helping with coding.")
	}
	if strings.Contains(text, "Claude Code") {
		text = strings.ReplaceAll(text, "Claude Code", "AI Assistant")
	}
	if strings.Contains(text, "claude-code") {
		text = strings.ReplaceAll(text, "claude-code", "ai-assistant")
	}
	if strings.Contains(text, "claude-cli") {
		text = strings.ReplaceAll(text, "claude-cli", "ai-cli")
	}
	return text
}

// sanitizeWorkBuddyMessages 清洗触碰 WorkBuddy 安全策略（11128）的客户端特有标识（如 Claude Code CLI、计费头、Tools描述等）。
func sanitizeWorkBuddyMessages(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	for i := range req.Messages {
		req.Messages[i].Content = sanitizeWorkBuddyText(req.Messages[i].Content)
		if len(req.Messages[i].ContentParts) > 0 {
			for j := range req.Messages[i].ContentParts {
				switch p := req.Messages[i].ContentParts[j].(type) {
				case ChatMessageTextPart:
					p.Text = sanitizeWorkBuddyText(p.Text)
					req.Messages[i].ContentParts[j] = p
				case *ChatMessageTextPart:
					if p != nil {
						p.Text = sanitizeWorkBuddyText(p.Text)
					}
				}
			}
		}
	}
	for i := range req.Tools {
		req.Tools[i].Function.Description = sanitizeWorkBuddyText(req.Tools[i].Function.Description)
	}
}

// aggregateOpenAISSEStream 在内存中把上游 OpenAI SSE 流聚合成非流式完整响应结构。
func aggregateOpenAISSEStream(reader io.Reader, fallbackModel string) (*OpenAIChatResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var id string
	model := fallbackModel
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	finishReason := "stop"
	var usage *OpenAIChatUsage

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Role             string `json:"role"`
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *OpenAIChatUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" {
			id = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				contentBuilder.WriteString(c.Delta.Content)
			}
			if c.Delta.ReasoningContent != "" {
				reasoningBuilder.WriteString(c.Delta.ReasoningContent)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finishReason = *c.FinishReason
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}

	fullContent := contentBuilder.String()
	fullReasoning := reasoningBuilder.String()

	resp := &OpenAIChatResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChatChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:             "assistant",
					Content:          fullContent,
					ReasoningContent: fullReasoning,
				},
				FinishReason: finishReason,
			},
		},
	}
	if usage != nil {
		resp.Usage = *usage
	} else {
		promptTokens := 10
		compTokens := (len(fullContent) + len(fullReasoning)) / 4
		if compTokens < 1 {
			compTokens = 1
		}
		resp.Usage = OpenAIChatUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: compTokens,
			TotalTokens:      promptTokens + compTokens,
		}
	}
	return resp, nil
}

// pickWorkBuddyAccount 结合 sticky 与 round-robin 游标从可用列表中选号。
func (h *APICompatHandler) pickWorkBuddyAccount(lbMode, sessionKey string, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}
	if lbMode == "sticky" && h.sessionRouter != nil {
		assigned := h.sessionRouter.GetOrAssignAccount(sessionKey, accounts, h.logFn)
		if assigned != nil {
			return assigned
		}
	}
	cursor := atomic.AddUint64(&h.workbuddyCursor, 1) - 1
	idx := int(cursor % uint64(len(accounts)))
	return accounts[idx]
}

// handleWorkBuddy 处理 /workbuddy/*, /wb/* 及 /route/* 命中 workbuddy 的所有请求。
func (h *APICompatHandler) handleWorkBuddy(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method == http.MethodGet || path == "/workbuddy/v1/models" || path == "/wb/v1/models" || strings.HasSuffix(path, "/models") {
		h.handleWorkBuddyModels(w, r, userSession)
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
			"error": "unsupported workbuddy endpoint: use /workbuddy/v1/chat/completions or /workbuddy/v1/messages",
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

	available := h.accountMgr.GetAvailableAccountsForChannel(workbuddyChannel, inModel)
	if len(available) == 0 {
		h.log("⛔ [WorkBuddy 中继] WorkBuddy 号池无可用账号 (model=%s)", inModel)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "workbuddy_pool_empty",
				"message": "no available WorkBuddy account in pool",
			},
		})
		return
	}

	sessionKey := h.stickyKeyOf(userSession)
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetWorkBuddyLBMode()
	}
	maxAttempts := len(available)
	if maxAttempts > workbuddyMaxAttemptsCap {
		maxAttempts = workbuddyMaxAttemptsCap
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
			limit = h.accountMgr.GetWorkBuddyMaxConcurrency()
		}
		filtered := activeAvailable
		if h.accountMgr != nil {
			filtered = h.accountMgr.FilterByConcurrency(activeAvailable, limit)
		}
		if len(filtered) > 0 {
			poolAccount = h.pickWorkBuddyAccount(lbMode, sessionKey, filtered)
		} else if h.accountMgr != nil {
			overAcc := h.accountMgr.LeastLoadedAccount(activeAvailable)
			if overAcc != nil {
				poolAccount = overAcc
				h.log("⚠️ [并发限制] WorkBuddy 池并发全满(限 %d), 超额降级到最少并发号 %s", limit, overAcc.Email)
			}
		}
		if poolAccount == nil {
			break
		}

		if h.accountMgr != nil {
			h.accountMgr.AcquireAccount(poolAccount.ID)
		}
		upstreamModel := account.ResolveWorkBuddyModel(inModel, poolAccount)

		// 构造上游 OpenAI Chat 请求体并解析思考意图
		var upstreamReq *OpenAIChatRequest
		var thinkMode workbuddyThinkingMode
		var thinkEffort string
		globalOn := IsEnableThinkingMode()

		if inboundAnthropic {
			var anthReq AnthropicRequest
			_ = json.Unmarshal(bodyBytes, &anthReq)
			anthReq.UserAgent = r.Header.Get("User-Agent")
			anthReq.Model = upstreamModel
			thinkMode, thinkEffort = workbuddyResolveAnthropicThinking(&anthReq, globalOn)
			mappings := h.getRelayModelMappingSafe()
			u, err := AnthropicToOpenAIChatPreservingImagesForProvider(&anthReq, false, "workbuddy", mappings)
			if err != nil {
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "anthropic to openai failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else if inboundResponses {
			thinkMode, thinkEffort = workbuddyResolveOpenAIThinking(bodyBytes, globalOn)
			u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
			if err != nil {
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "responses to openai failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else {
			thinkMode, thinkEffort = workbuddyResolveOpenAIThinking(bodyBytes, globalOn)
			var chatReq OpenAIChatRequest
			_ = json.Unmarshal(bodyBytes, &chatReq)
			chatReq.Model = upstreamModel
			upstreamReq = &chatReq
		}

		// 注入 WorkBuddy 思考参数 (官方顶层 reasoning_effort, 绝不注入 NIM kwargs)
		workbuddyApplyThinkingToChat(upstreamReq, thinkMode, thinkEffort)

		// 硬性限制二：首条消息强制注入 System Prompt
		ensureWorkBuddySystemPrompt(upstreamReq)

		// 清洗触碰 WorkBuddy 安全策略（11128）的客户端特有标识（如 Claude Code CLI、计费头等）
		sanitizeWorkBuddyMessages(upstreamReq)

		// 硬性限制一：向 WorkBuddy 上游强制发送 stream: true
		upstreamReq.Stream = true
		if upstreamReq.StreamOptions == nil {
			upstreamReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
		}

		upstreamBytes, _ := json.Marshal(upstreamReq)
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		if baseURL == "" {
			baseURL = account.DefaultWorkBuddyBaseURL
		}
		targetURL := baseURL + "/v2/chat/completions"

		httpReq, errReq := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBytes))
		if errReq != nil {
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": errReq.Error()})
			return
		}

		// 注入 WorkBuddy 必要 Headers
		httpReq.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
		httpReq.Header.Set("Content-Type", "application/json")
		convID := sessionKey
		if convID == "" {
			convID = generateWorkBuddyUUID()
		}
		httpReq.Header.Set("X-Conversation-ID", convID)
		httpReq.Header.Set("X-Request-ID", generateWorkBuddyUUID())
		httpReq.Header.Set("X-IDE-Type", "CodeBuddy")
		httpReq.Header.Set("X-IDE-Name", "WorkBuddy AI")
		httpReq.Header.Set("X-IDE-Version", "5.5.2")
		httpReq.Header.Set("Accept", "text/event-stream")

		client := h.streamClient
		if client == nil {
			client = h.client
		}
		if client == nil {
			client = http.DefaultClient
		}

		resp, errDo := client.Do(httpReq)
		if errDo != nil {
			h.log("⚠️ [WorkBuddy 中继] 请求失败 (%s): %v, 换号重试", poolAccount.Email, errDo)
			skippedAccounts[poolAccount.ID] = true
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			_ = resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			cooldownUntilMs := time.Now().UnixNano()/1e6 + 60*1000
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, workbuddyChannel, inModel)
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			cooldownUntilMs := time.Now().UnixNano()/1e6 + 24*3600*1000
			h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, workbuddyChannel, inModel)
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
			h.log("❌ [WorkBuddy 中继] 上游返回异常状态码 %d (%s): %s", resp.StatusCode, poolAccount.Email, string(errBytes))
			// 若上游返回 11128 或渠道安全策略拦截，冷冻该账号并换号重试(防止单账号受限拖垮整个模型分支)
			if resp.StatusCode == http.StatusBadRequest && (strings.Contains(string(errBytes), "11128") || strings.Contains(string(errBytes), "unapproved channel")) {
				skippedAccounts[poolAccount.ID] = true
				cooldownUntilMs := time.Now().UnixNano()/1e6 + 5*60*1000 // 冷冻 5 分钟
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, workbuddyChannel, inModel)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				continue
			}
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(errBytes)
			return
		}

		// 上游 200 成功响应
		firstByteRec.MarkFirstByte()

		logCtx := workbuddyLogCtx{
			Method:          r.Method,
			Host:            workbuddyHostFromBaseURL(poolAccount.BaseURL),
			Path:            r.URL.Path,
			SessionID:       sessionKey,
			Account:         poolAccount.Email,
			StatusCode:      http.StatusOK,
			StartTs:         startTs,
			FirstByteRec:    firstByteRec,
			ReqBody:         parseInboundBodyForLog(bodyBytes),
			ReqHeaders:      collectInboundHeadersForLog(r.Header),
			ReasoningEffort: upstreamReq.ReasoningEffort,
		}

		if isStreaming {
			// 客户端请求流式
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
				h.recordWorkBuddyUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
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
				fw := newFlushWriter(fmt.Sprintf("wb_%d", time.Now().UnixNano()), bufio.NewWriter(w), flusher)
				inT, outT, cachedT := OpenAIChatSSEToResponsesSSE(r.Context(), resp.Body, resp.Body, fw, upstreamModel)
				fw.flush()
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				h.recordWorkBuddyUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			} else {
				inT, outT, cachedT := h.proxyNvidiaOpenAIPassthrough(r.Context(), w, resp, true, firstByteRec)
				_ = resp.Body.Close()
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				h.recordWorkBuddyUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)
				return
			}
		}

		// 客户端请求非流式：在 Go 内存中消费 SSE 流聚合为完整 JSON
		chatResp, aggErr := aggregateOpenAISSEStream(resp.Body, upstreamModel)
		_ = resp.Body.Close()
		h.accountMgr.ReleaseAccount(poolAccount.ID)

		if aggErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to aggregate upstream stream: " + aggErr.Error()})
			return
		}

		inT := chatResp.Usage.PromptTokens
		outT := chatResp.Usage.CompletionTokens
		cachedT := chatResp.Usage.CachedTokens()
		h.recordWorkBuddyUsage(userSession, inModel, inT, outT, cachedT, poolAccount, logCtx)

		if inboundAnthropic {
			anthResp := OpenAIChatToAnthropic(chatResp)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(anthResp)
			return
		} else if inboundResponses {
			respResp := OpenAIChatToResponses(chatResp, upstreamModel)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(respResp)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(chatResp)
		return
	}

	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "all workbuddy accounts exhausted or failed"})
}

// handleWorkBuddyModels 返回 WorkBuddy 提供的免费模型列表。
func (h *APICompatHandler) handleWorkBuddyModels(w http.ResponseWriter, r *http.Request, _ *RelaySession) {
	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.HasPrefix(r.Header.Get("x-api-key"), "sk-ant-") ||
		strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "anthropic")

	models := account.WorkBuddySupportedModels
	if isAnthropic {
		list := make([]map[string]interface{}, 0, len(models))
		for _, m := range models {
			list = append(list, map[string]interface{}{
				"id":           m,
				"type":         "model",
				"display_name": m,
				"created_at":   time.Now().UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"data":     list,
			"has_more": false,
		})
		return
	}

	list := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		list = append(list, map[string]interface{}{
			"id":       m,
			"object":   "model",
			"created":  1700000000,
			"owned_by": "workbuddy",
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   list,
	})
}
