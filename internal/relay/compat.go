package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
)

var localProxyAddr = "127.0.0.1:18443"

type APICompatHandler struct {
	authMgr       *AuthManager
	accountMgr    *account.Manager
	sessionRouter *session.Router
	statsTracker  *StatsTracker
	logFn         func(string)
	client        *http.Client
	streamClient  *http.Client // 流式请求专用，不设全局超时，避免长生成被截断
	settingsMgr   settings.ManagerInterface
	rateLimiter   *RateLimiter
}

func NewAPICompatHandler(
	authMgr *AuthManager,
	accountMgr *account.Manager,
	sessionRouter *session.Router,
	statsTracker *StatsTracker,
	settingsMgr settings.ManagerInterface,
	logFn func(string),
) *APICompatHandler {
	return &APICompatHandler{
		authMgr:       authMgr,
		accountMgr:    accountMgr,
		sessionRouter: sessionRouter,
		statsTracker:  statsTracker,
		settingsMgr:   settingsMgr,
		logFn:         logFn,
		client:        netutil.NewClient(5 * time.Minute),
		streamClient:  &http.Client{Transport: netutil.NewTransport(), Timeout: 0},
		rateLimiter:   NewRateLimiter(),
	}
}

func (h *APICompatHandler) getModelMapping() []settings.ModelMappingEntry {
	if h.settingsMgr != nil {
		return h.settingsMgr.GetRelayModelMapping()
	}
	return settings.GetDefaultModelMappings()
}

func (h *APICompatHandler) log(format string, args ...interface{}) {
	if h.logFn != nil {
		h.logFn(fmt.Sprintf("[APICompat] "+format, args...))
	}
}

func (h *APICompatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 校验 Token（支持 Authorization Bearer 和 X-API-Key 两种形式）
	token := extractToken(r)
	if token == "" {
		h.log("🔑 Authentication failed: missing API Key / Token in request headers (URL: %s)", r.URL.Path)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing API Key"})
		return
	}
	session, err := h.authMgr.ValidateToken(token)
	if err != nil {
		h.log("🔑 Authentication failed: invalid API Key %q: %v (URL: %s)", token, err, r.URL.Path)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid API Key: " + err.Error()})
		return
	}

	path := r.URL.Path

	// 校验速率限制 (每分钟最多请求次数，默认为30次)
	if r.Method == http.MethodPost && (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages") {
		limit := 30
		user := h.authMgr.userMgr.GetUserByID(session.UserID)
		if user != nil && user.Quotas.RateLimit > 0 {
			limit = user.Quotas.RateLimit
		}
		if !h.rateLimiter.Allow(session.UserID, limit) {
			h.log("🚦 Rate limit exceeded for user %s (%d requests/min)", session.UserKey, limit)
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error": map[string]interface{}{
					"message": fmt.Sprintf("Rate limit exceeded. Maximum %d requests per minute.", limit),
					"type":    "rate_limit_error",
					"code":    "rate_limit_exceeded",
				},
			})
			return
		}
	}

	// 1. 模型列表接口
	if path == "/v1/models" && r.Method == http.MethodGet {
		h.handleModels(w, r)
		return
	}

	// 2. OpenAI 对话接口 (兼容 Codex 等客户端在“Chat Completions (转换)”模式下调用的 /v1/responses 路径)
	if (path == "/v1/chat/completions" || path == "/v1/responses") && r.Method == http.MethodPost {
		h.handleOpenAIChat(w, r, session)
		return
	}

	// 3. Anthropic 对话接口
	if path == "/v1/messages" && r.Method == http.MethodPost {
		h.handleAnthropicMessages(w, r, session)
		return
	}

	// 4. v1internal 接口 (支持 /v1internal:generateContent 或 /v1internal:streamGenerateContent)
	if strings.HasPrefix(path, "/v1internal:") && r.Method == http.MethodPost {
		h.handleV1Internal(w, r, session)
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "endpoint not found"})
}

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

func (h *APICompatHandler) handleOpenAIChat(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	r.Body.Close()

	openReq, err := ParseUnifiedOpenAIRequest(bodyBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
		return
	}

	geminiModel := MapClientModelToGemini(openReq.Model, h.getModelMapping())
	geminiReq := TranslateOpenAIToGemini(openReq)

	h.log("OpenAI Request mapped. ClientModel: %s -> GeminiModel: %s | User: %s", openReq.Model, geminiModel, userSession.UserKey)

	apiFormat := "openai"
	if strings.Contains(r.URL.Path, "responses") {
		// Codex's /v1/responses requires OpenAI Responses API stream format
		apiFormat = "responses"
	}

	// 终极精确拦截：通过底层协议头与特定负载指纹，完美区分后台心跳/预生成与用户真实请求
	isCodexProbe := false
	if strings.Contains(strings.ToLower(openReq.Model), "gpt-5.4") {
		// 1. 【协议头指纹】严格拦截所有 Codex 引擎自发的后台线程（非人类主动提问）
		turnMetadata := r.Header.Get("X-Codex-Turn-Metadata")
		if strings.Contains(turnMetadata, `"thread_source":"system"`) {
			isCodexProbe = true
		}

		// 2. 【负载指纹兜底】精确拦截 Codex 偷偷生成的个性化推荐探测和其他后台任务
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "hyperpersonalized suggestions") {
			isCodexProbe = true
		}
	}

	if isCodexProbe {
		if openReq.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)

			if apiFormat == "responses" {
				txtDone := map[string]interface{}{
					"type":            "response.output_text.done",
					"sequence_number": 0,
					"item_id":         "mock_heartbeat_msg",
					"output_index":    0,
					"content_index":   0,
					"text":            "Ready",
				}
				txtBytes, _ := json.Marshal(txtDone)
				fmt.Fprintf(w, "event: response.output_text.done\ndata: %s\n\n", string(txtBytes))

				partDone := map[string]interface{}{
					"type":            "response.content_part.done",
					"sequence_number": 1,
					"item_id":         "mock_heartbeat_msg",
					"output_index":    0,
					"content_index":   0,
					"part": map[string]interface{}{
						"type": "output_text",
						"text": "Ready",
					},
				}
				partBytes, _ := json.Marshal(partDone)
				fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(partBytes))

				itemMsg := map[string]interface{}{
					"id":      "mock_heartbeat_msg",
					"type":    "message",
					"status":  "completed",
					"role":    "assistant",
					"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "Ready"}},
				}
				itemDone := map[string]interface{}{
					"type":            "response.output_item.done",
					"sequence_number": 2,
					"output_index":    0,
					"item":            itemMsg,
				}
				itemBytes, _ := json.Marshal(itemDone)
				fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemBytes))

				completedEvt := map[string]interface{}{
					"type":            "response.completed",
					"sequence_number": 3,
					"response": map[string]interface{}{
						"id":         "mock_heartbeat_resp",
						"object":     "response",
						"created_at": time.Now().Unix(),
						"status":     "completed",
						"usage": map[string]interface{}{
							"input_tokens":  10,
							"output_tokens": 10,
							"total_tokens":  20,
						},
						"output": []interface{}{itemMsg},
					},
				}
				completedBytes, _ := json.Marshal(completedEvt)
				fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(completedBytes))
			} else {
				finalChunk := OpenAIStreamChunk{
					ID:      "mock_heartbeat_resp",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   openReq.Model,
					Choices: []OpenAIStreamChoice{
						{Index: 0, Delta: OpenAIDelta{Content: "Ready"}, FinishReason: "stop"},
					},
				}
				finalBytes, _ := json.Marshal(finalChunk)
				fmt.Fprintf(w, "data: %s\n\n", string(finalBytes))
				fmt.Fprintf(w, "data: [DONE]\n\n")
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}

		// 非流式情况
		if apiFormat == "responses" {
			itemMsg := map[string]interface{}{
				"id":      "mock_heartbeat_msg",
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "Ready"}},
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id":         "mock_heartbeat_resp",
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  10,
					"output_tokens": 10,
					"total_tokens":  20,
				},
				"output": []interface{}{itemMsg},
			})
		} else {
			resp := OpenAIResponse{
				ID:      "mock_heartbeat_resp",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   openReq.Model,
				Choices: []OpenAIResponseChoice{
					{Index: 0, Message: OpenAIMessage{Role: "assistant", Content: "Ready"}, FinishReason: "stop"},
				},
				Usage: OpenAIResponseUsage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20},
			}
			writeJSON(w, http.StatusOK, resp)
		}
		return
	}

	h.dispatchToGemini(w, r, userSession, geminiModel, geminiReq, openReq.Stream, apiFormat)
}

func (h *APICompatHandler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	var anthReq AnthropicRequest
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	r.Body.Close()

	if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
		return
	}

	geminiModel := MapClientModelToGemini(anthReq.Model, h.getModelMapping())
	geminiReq := TranslateAnthropicToGemini(&anthReq)

	h.log("Anthropic Request mapped. ClientModel: %s -> GeminiModel: %s | User: %s", anthReq.Model, geminiModel, userSession.UserKey)

	h.dispatchToGemini(w, r, userSession, geminiModel, geminiReq, anthReq.Stream, "anthropic")
}

func (h *APICompatHandler) dispatchToGemini(
	w http.ResponseWriter,
	r *http.Request,
	userSession *RelaySession,
	geminiModel string,
	geminiReq *GeminiRequest,
	stream bool,
	apiFormat string,
) {
	startTime := time.Now()

	// 1. 获取会话 Key
	tempBytesForSession, _ := json.Marshal(geminiReq)
	sessionKey := h.sessionRouter.ExtractSessionKey(r, tempBytesForSession)

	// 调用优化器执行压缩与模型路由降级
	targetModelToQuery, compressed := CheckAndOptimizeSession(
		r,
		geminiReq,
		geminiModel,
		sessionKey,
		userSession.UserKey,
		userSession.UserID,
		userSession.APIKeyID,
		h.client,
		h.settingsMgr,
		func(msg string) {
			h.log("%s", msg)
		},
	)
	if compressed {
		h.log("✅ [Relay Compat] 会话压缩成功，请求体已优化")
	}

	geminiReqBytes, err := json.Marshal(geminiReq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to marshal gemini request"})
		return
	}

	// 调试日志：输出请求体摘要
	var roleSeq []string
	for _, c := range geminiReq.Contents {
		partTypes := ""
		for _, p := range c.Parts {
			if p.Text != "" {
				partTypes += "T"
			}
			if p.FunctionCall != nil {
				partTypes += "FC(" + p.FunctionCall.Name + ")"
			}
			if p.FunctionResponse != nil {
				partTypes += "FR(" + p.FunctionResponse.Name + ")"
			}
		}
		roleSeq = append(roleSeq, fmt.Sprintf("%s[%s]", c.Role, partTypes))
	}
	// 统计工具使用情况
	toolCount := 0
	if len(geminiReq.Tools) > 0 {
		toolCount = len(geminiReq.Tools[0].FunctionDeclarations)
	}
	h.log("📋 [调试] 请求体: %d 条消息 | 角色序列: %v | 工具数: %d | 体积: %d bytes",
		len(geminiReq.Contents), roleSeq, toolCount, len(geminiReqBytes))

	// 准备向本地核心代理服务 (18443 端口) 发起请求以复用成熟 of 账号池分发与自动重试逻辑
	action := "generateContent"
	queryStr := ""
	if stream {
		action = "streamGenerateContent"
		queryStr = "?alt=sse"
	}

	targetURL := fmt.Sprintf("http://%s/v1beta/models/%s:%s%s", localProxyAddr, targetModelToQuery, action, queryStr)

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(geminiReqBytes))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	// 将用户的凭证传递给本地代理，本地代理将据此提取 sessionKey 自动粘性绑定账号池并执行扣费统计
	req.Header.Set("Authorization", "Bearer " + userSession.UserKey)
	req.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	req.Header.Set("X-Antigravity-Original-Path", r.URL.Path)
	req.Header.Set("X-Antigravity-Original-Method", r.Method)
	h.log("Forwarding translated request to local proxy (18443) | Model: %s | Stream: %v", targetModelToQuery, stream)

	// 流式请求使用无超时 Client，避免长时间生成（>5min）被 http.Client.Timeout 截断
	httpClient := h.client
	if stream {
		httpClient = h.streamClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		h.log("❌ Failed to query local proxy: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to query local proxy: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		h.log("❌ Local proxy returned status %d: %s", resp.StatusCode, string(respBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	reqID := r.Header.Get("X-Antigravity-Req-ID")

	// 4. 流式传输（SSE）处理
	if stream {
		h.handleStreamResponse(w, resp.Body, userSession, geminiModel, apiFormat, startTime, r.URL.Path, reqID)
	} else {
		// 5. 非流式传输处理
		h.handleNormalResponse(w, resp.Body, userSession, geminiModel, apiFormat, startTime, r.URL.Path, reqID)
	}
}


func removeAccountFromList(list []*account.Account, accountID string) []*account.Account {
	var result []*account.Account
	for _, a := range list {
		if a.ID != accountID {
			result = append(result, a)
		}
	}
	return result
}

func (h *APICompatHandler) handleNormalResponse(
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
	data, err := io.ReadAll(respBody)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to read google response"})
		return
	}

	var gemResp GeminiResponse
	if err := json.Unmarshal(data, &gemResp); err != nil {
		// 可能是被强制转换成了 SSE 流式响应 (如 antigravity 强制路由至 streamGenerateContent)
		if strings.Contains(string(data), "data: ") {
			var fullText string
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "data: ") {
					dataStr := strings.TrimPrefix(line, "data: ")
					if dataStr == "[DONE]" {
						continue
					}
					var chunk GeminiResponse
					if errChunk := json.Unmarshal([]byte(dataStr), &chunk); errChunk == nil {
						if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
							fullText += chunk.Candidates[0].Content.Parts[0].Text
						}
						if chunk.UsageMetadata.PromptTokenCount > 0 {
							gemResp.UsageMetadata.PromptTokenCount = chunk.UsageMetadata.PromptTokenCount
						}
						if chunk.UsageMetadata.CandidatesTokenCount > 0 {
							gemResp.UsageMetadata.CandidatesTokenCount = chunk.UsageMetadata.CandidatesTokenCount
						}
					}
				}
			}
			gemResp.Candidates = []GeminiCandidate{
				{Content: GeminiCandidateContent{Parts: []GeminiPart{{Text: fullText}}, Role: "model"}},
			}
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to parse google response: " + string(data)})
			return
		}
	}

	// 提取回复内容（text + functionCall）与用量
	var contentBlocks []AnthropicContent
	hasFunctionCall := false
	if len(gemResp.Candidates) > 0 {
		for _, part := range gemResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				contentBlocks = append(contentBlocks, AnthropicContent{Type: "text", Text: part.Text})
			}
			if part.FunctionCall != nil {
				hasFunctionCall = true
				contentBlocks = append(contentBlocks, AnthropicContent{
					Type:  "tool_use",
					ID:    generateToolUseID(),
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				})
			}
		}
	}
	if len(contentBlocks) == 0 {
		contentBlocks = []AnthropicContent{{Type: "text", Text: ""}}
	}

	inTokens := gemResp.UsageMetadata.PromptTokenCount
	outTokens := gemResp.UsageMetadata.CandidatesTokenCount

	// 根据要求的 API 格式，翻译响应包
	if apiFormat == "openai" {
		replyText := ""
		var toolCalls []OpenAIToolCall
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			} else if b.Type == "tool_use" {
				argsJSON, _ := json.Marshal(b.Input)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   b.ID,
					Type: "function",
					Function: OpenAIToolCallFunction{
						Name:      b.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}
		openResp := OpenAIResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", rand.Int63()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   geminiModel,
			Choices: []OpenAIResponseChoice{
				{
					Index: 0,
					Message: OpenAIMessage{
						Role:      "assistant",
						Content:   replyText,
						ToolCalls: toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: OpenAIResponseUsage{
				PromptTokens:     inTokens,
				CompletionTokens: outTokens,
				TotalTokens:      inTokens + outTokens,
			},
		}
		writeJSON(w, http.StatusOK, &openResp)
	} else if apiFormat == "responses" {
		replyText := ""
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			}
		}
		respID := fmt.Sprintf("resp_%d", rand.Int63())
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":         respID,
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": []interface{}{
					map[string]interface{}{
						"id":      fmt.Sprintf("msg_%s_0", respID),
						"type":    "message",
						"status":  "completed",
						"role":    "assistant",
						"content": []interface{}{map[string]interface{}{"type": "output_text", "text": replyText}},
					},
				},
			},
		})
	} else { // anthropic
		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}
		anthResp := AnthropicResponse{
			ID:           fmt.Sprintf("msg_%d", rand.Int63()),
			Type:         "message",
			Role:         "assistant",
			Content:      contentBlocks,
			Model:        geminiModel,
			StopReason:   stopReason,
			StopSequence: nil,
			Usage: AnthropicResponseUsage{
				InputTokens:  inTokens,
				OutputTokens: outTokens,
			},
		}
		writeJSON(w, http.StatusOK, &anthResp)
	}

}

func (h *APICompatHandler) handleStreamResponse(
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "streaming not supported by server"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK) // 显式发送 200 + SSE 响应头，确保客户端立即收到
	flusher.Flush()

	scanner := bufio.NewScanner(respBody)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer，防止大型 SSE 行被截断

	// 临时生成的流 ID 和固定时间戳，确保所有 chunk 完全一致，防止严格客户端断开
	streamID := fmt.Sprintf("msg_%d", rand.Int63())
	if apiFormat == "openai" {
		streamID = fmt.Sprintf("chatcmpl-%d", rand.Int63())
	}
	createdAt := startTime.Unix()

	// 初始化计数
	inTokens := 0
	outTokens := 0

	var err error

	// 流式状态跟踪
	blockIndex := 0
	textBlockOpen := false
	hasFunctionCall := false
	openAIRoleSent := false

	// Anthropic 协议下，开始流时首发 message_start
	if apiFormat == "anthropic" {
		msgStart := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            streamID,
				"type":          "message",
				"role":          "assistant",
				"content":       []interface{}{},
				"model":         geminiModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		}
		msgStartBytes, _ := json.Marshal(msgStart)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(msgStartBytes))
		flusher.Flush()
	}

	// Responses API 专用变量
	seqNum := 0
	nextSeq := func() int {
		seqNum++
		return seqNum
	}
	responsesMsgOpened := false
	responsesMsgID := fmt.Sprintf("msg_%s_0", streamID)
	var responsesTextBuf strings.Builder
	hasOpenAIToolCall := false

	// Responses 协议下，开始流时首发 response.created 和 response.in_progress
	if apiFormat == "responses" {
		createdEvt := map[string]interface{}{
			"type":            "response.created",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		createdBytes, _ := json.Marshal(createdEvt)
		fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", string(createdBytes))

		inprogEvt := map[string]interface{}{
			"type":            "response.in_progress",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		inprogBytes, _ := json.Marshal(inprogEvt)
		fmt.Fprintf(w, "event: response.in_progress\ndata: %s\n\n", string(inprogBytes))
		flusher.Flush()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data:")
		dataStr = strings.TrimSpace(dataStr)
		if dataStr == "" {
			continue
		}

		var gemResp GeminiResponse
		if err = json.Unmarshal([]byte(dataStr), &gemResp); err != nil {
			continue
		}

		// 同步更新用量
		if gemResp.UsageMetadata.PromptTokenCount > 0 {
			inTokens = gemResp.UsageMetadata.PromptTokenCount
		}
		if gemResp.UsageMetadata.CandidatesTokenCount > 0 {
			outTokens = gemResp.UsageMetadata.CandidatesTokenCount
		}

		if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
			continue
		}

		// 遍历所有 Parts，分别处理 text 和 functionCall
		for _, part := range gemResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				if apiFormat == "openai" {
					if !openAIRoleSent {
						initChunk := OpenAIStreamChunk{
							ID:      streamID,
							Object:  "chat.completion.chunk",
							Created: createdAt,
							Model:   geminiModel,
							Choices: []OpenAIStreamChoice{
								{Index: 0, Delta: OpenAIDelta{Role: "assistant"}, FinishReason: nil},
							},
						}
						initBytes, _ := json.Marshal(initChunk)
						fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
						flusher.Flush()
						openAIRoleSent = true
					}

					chunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   geminiModel,
						Choices: []OpenAIStreamChoice{
							{Index: 0, Delta: OpenAIDelta{Content: part.Text}, FinishReason: nil},
						},
					}
					chunkBytes, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
				} else if apiFormat == "responses" {
					if !responsesMsgOpened {
						itemAdded := map[string]interface{}{
							"type":            "response.output_item.added",
							"sequence_number": nextSeq(),
							"output_index":    0,
							"item": map[string]interface{}{
								"id":      responsesMsgID,
								"type":    "message",
								"status":  "in_progress",
								"role":    "assistant",
								"content": []interface{}{},
							},
						}
						itemBytes, _ := json.Marshal(itemAdded)
						fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

						partAdded := map[string]interface{}{
							"type":            "response.content_part.added",
							"sequence_number": nextSeq(),
							"item_id":         responsesMsgID,
							"output_index":    0,
							"content_index":   0,
							"part": map[string]interface{}{
								"type": "output_text",
								"text": "",
							},
						}
						partBytes, _ := json.Marshal(partAdded)
						fmt.Fprintf(w, "event: response.content_part.added\ndata: %s\n\n", string(partBytes))
						responsesMsgOpened = true
					}
					responsesTextBuf.WriteString(part.Text)

					deltaEvt := map[string]interface{}{
						"type":            "response.output_text.delta",
						"sequence_number": nextSeq(),
						"item_id":         responsesMsgID,
						"output_index":    0,
						"content_index":   0,
						"delta":           part.Text,
					}
					deltaBytes, _ := json.Marshal(deltaEvt)
					fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", string(deltaBytes))
				} else { // anthropic
					// 延迟开启 text block：仅在有实际文本时才发送 content_block_start
					if !textBlockOpen {
						blockStart := map[string]interface{}{
							"type":  "content_block_start",
							"index": blockIndex,
							"content_block": map[string]interface{}{"type": "text", "text": ""},
						}
						blockStartBytes, _ := json.Marshal(blockStart)
						fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(blockStartBytes))
						textBlockOpen = true
					}
					delta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": blockIndex,
						"delta": map[string]interface{}{"type": "text_delta", "text": part.Text},
					}
					deltaBytes, _ := json.Marshal(delta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
				}
				flusher.Flush()
			}

			if part.FunctionCall != nil {
				if apiFormat == "openai" {
					hasOpenAIToolCall = true
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					startChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   geminiModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: 0,
											ID:    callID,
											Type:  "function",
											Function: OpenAIToolCallFunction{
												Name:      part.FunctionCall.Name,
												Arguments: "",
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					startBytes, _ := json.Marshal(startChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(startBytes))

					argsChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   geminiModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: 0,
											Function: OpenAIToolCallFunction{
												Arguments: string(argsJSON),
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					argsBytes, _ := json.Marshal(argsChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(argsBytes))
					flusher.Flush()
				} else if apiFormat == "responses" {
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					fcItemID := fmt.Sprintf("fc_%s", callID)
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					argsStr := string(argsJSON)

					itemAdded := map[string]interface{}{
						"type":            "response.output_item.added",
						"sequence_number": nextSeq(),
						"output_index":    blockIndex,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "in_progress",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": "",
						},
					}
					itemBytes, _ := json.Marshal(itemAdded)
					fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

					deltaEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.delta",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    blockIndex,
						"call_id":         callID,
						"delta":           argsStr,
					}
					deltaBytes, _ := json.Marshal(deltaEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.delta\ndata: %s\n\n", string(deltaBytes))

					doneEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.done",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    blockIndex,
						"call_id":         callID,
						"arguments":       argsStr,
					}
					doneBytes, _ := json.Marshal(doneEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.done\ndata: %s\n\n", string(doneBytes))

					itemDone := map[string]interface{}{
						"type":            "response.output_item.done",
						"sequence_number": nextSeq(),
						"output_index":    blockIndex,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "completed",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": argsStr,
						},
					}
					itemDoneBytes, _ := json.Marshal(itemDone)
					fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemDoneBytes))
					blockIndex++
					flusher.Flush()
				} else if apiFormat == "anthropic" {
					hasFunctionCall = true

					// 在发出 tool_use 之前，先关闭未完成的 text block
					if textBlockOpen {
						stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
						stopBytes, _ := json.Marshal(stopEvt)
						fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
						blockIndex++
						textBlockOpen = false
						flusher.Flush()
					}

					toolID := generateToolUseID()

					// content_block_start: tool_use
					toolStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    toolID,
							"name":  part.FunctionCall.Name,
							"input": map[string]interface{}{},
						},
					}
					toolStartBytes, _ := json.Marshal(toolStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(toolStartBytes))

					// content_block_delta: input_json_delta（一次性发完 args JSON）
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					inputDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": blockIndex,
						"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(argsJSON)},
					}
					inputDeltaBytes, _ := json.Marshal(inputDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(inputDeltaBytes))

					// content_block_stop
					toolStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
					toolStopBytes, _ := json.Marshal(toolStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(toolStopBytes))
					blockIndex++

					flusher.Flush()
				}
			}
		}
	}

	// 发射结束帧
	if apiFormat == "openai" {
		var finishReason interface{} = "stop"
		if hasOpenAIToolCall {
			finishReason = "tool_calls"
		}
		finalChunk := OpenAIStreamChunk{
			ID:      streamID,
			Object:  "chat.completion.chunk",
			Created: createdAt,
			Model:   geminiModel,
			Choices: []OpenAIStreamChoice{
				{Index: 0, Delta: OpenAIDelta{}, FinishReason: finishReason},
			},
		}
		finalBytes, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(finalBytes))
		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else if apiFormat == "responses" {
		fullText := responsesTextBuf.String()
		var outputItems []interface{}

		if responsesMsgOpened {
			txtDone := map[string]interface{}{
				"type":            "response.output_text.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    0,
				"content_index":   0,
				"text":            fullText,
			}
			txtBytes, _ := json.Marshal(txtDone)
			fmt.Fprintf(w, "event: response.output_text.done\ndata: %s\n\n", string(txtBytes))

			partDone := map[string]interface{}{
				"type":            "response.content_part.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    0,
				"content_index":   0,
				"part": map[string]interface{}{
					"type": "output_text",
					"text": fullText,
				},
			}
			partBytes, _ := json.Marshal(partDone)
			fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(partBytes))

			itemMsg := map[string]interface{}{
				"id":      responsesMsgID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": fullText}},
			}
			itemDone := map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSeq(),
				"output_index":    0,
				"item":            itemMsg,
			}
			itemBytes, _ := json.Marshal(itemDone)
			fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemBytes))

			outputItems = append(outputItems, itemMsg)
		}

		completedEvt := map[string]interface{}{
			"type":            "response.completed",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": outputItems,
			},
		}
		completedBytes, _ := json.Marshal(completedEvt)
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(completedBytes))
	} else { // anthropic
		// 关闭未完成的 text block
		if textBlockOpen {
			blockStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
			blockStopBytes, _ := json.Marshal(blockStop)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))
		}

		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}

		msgDelta := map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]interface{}{"output_tokens": outTokens},
		}
		msgDeltaBytes, _ := json.Marshal(msgDelta)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", string(msgDeltaBytes))

		fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
	flusher.Flush()

	// 记录用量统计

}

type RateLimiter struct {
	mu           sync.Mutex
	userRequests map[string][]time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		userRequests: make(map[string][]time.Time),
	}
}

func (l *RateLimiter) Allow(userID string, limit int) bool {
	if limit <= 0 {
		limit = 30
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	oneMinuteAgo := now.Add(-1 * time.Minute)

	reqs := l.userRequests[userID]
	var validReqs []time.Time
	for _, t := range reqs {
		if t.After(oneMinuteAgo) {
			validReqs = append(validReqs, t)
		}
	}

	if len(validReqs) >= limit {
		l.userRequests[userID] = validReqs
		return false
	}

	validReqs = append(validReqs, now)
	l.userRequests[userID] = validReqs
	return true
}

func (h *APICompatHandler) handleV1Internal(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	// 动态检测目标 Action 和流式属性
	path := r.URL.Path // e.g. /v1internal:generateContent
	action := "generateContent"
	if strings.Contains(path, "streamGenerateContent") {
		action = "streamGenerateContent"
	}
	isStreaming := action == "streamGenerateContent" || strings.Contains(r.URL.RawQuery, "alt=sse")

	// 构造发往本地核心代理服务 (18443 端口) 的请求，保留原始路径与查询参数
	// 这样可以复用本地代理的账号池分发、重试以及计费统计逻辑
	queryStr := ""
	if r.URL.RawQuery != "" {
		queryStr = "?" + r.URL.RawQuery
	} else if isStreaming && !strings.Contains(r.URL.RawQuery, "alt=sse") {
		queryStr = "?alt=sse"
	}

	targetURL := fmt.Sprintf("http://%s%s%s", localProxyAddr, path, queryStr)

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
		return
	}

	// 复制头部
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer " + userSession.UserKey)
	req.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}

	// 执行请求并流式响应
	httpClient := h.client
	if isStreaming {
		httpClient = h.streamClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to forward request to proxy: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 拷贝响应头
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 流式传输响应体
	buf := make([]byte, 4096)
	flusher, isFlusher := w.(http.Flusher)
	for {
		n, errRead := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
		}
		if errRead != nil {
			break
		}
	}
}
