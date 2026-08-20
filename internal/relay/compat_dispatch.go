package relay

import (
	"antigravity-proxy/internal/account"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// compat_dispatch.go: OpenAI Chat / Anthropic Messages 入口 handler + dispatchToGemini 分发 + removeAccountFromList。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

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

	// OpenAI Chat/Responses 入站不回译为 Anthropic 流(上游为 Gemini,响应仅 openai/responses 形态),
	// message_start 不出现,故 inboundInputTokens 传 0(无消费方读取)。
	// 第三个返回值 streamIncludeUsage 来自 openReq.StreamOptions.include_usage:请求式经
	// dispatchToGemini 透传到 handleStreamResponse,决定是否在流末尾补吐 OpenAI usage chunk。
	streamIncludeUsage := false
	if openReq.StreamOptions != nil && openReq.StreamOptions.IncludeUsage {
		streamIncludeUsage = true
	}
	h.dispatchToGemini(w, r, userSession, openReq.Model, geminiModel, geminiReq, openReq.Stream, apiFormat, 0, streamIncludeUsage)
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
	anthReq.UserAgent = r.Header.Get("User-Agent")

	geminiModel := MapClientModelToGemini(anthReq.Model, h.getModelMapping())
	geminiReq := TranslateAnthropicToGemini(&anthReq)

	h.log("Anthropic Request mapped. ClientModel: %s -> GeminiModel: %s | User: %s", anthReq.Model, geminiModel, userSession.UserKey)

	// 入站 input_tokens 估算:仅 anthropic 流式路径用此值填 message_start.usage.input_tokens,
	// 让客户端(Claude Code spinner)流首即显示 ↑(否则流首 0 使 spinner 只有 ↓ 无 ↑)。
	// 此处已解析出 AnthropicRequest,直接复用 estimateInputTokens;真实累计值仍由末帧 message_delta 覆盖。
	inboundInputTokens := estimateInputTokens(&anthReq)

	h.dispatchToGemini(w, r, userSession, anthReq.Model, geminiModel, geminiReq, anthReq.Stream, "anthropic", inboundInputTokens, false)
}

func (h *APICompatHandler) dispatchToGemini(
	w http.ResponseWriter,
	r *http.Request,
	userSession *RelaySession,
	clientModel string,
	geminiModel string,
	geminiReq *GeminiRequest,
	stream bool,
	apiFormat string,
	inboundInputTokens int,
	streamIncludeUsage bool,
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

	// 本地图片路径自愈(L2.5 预处理):Claude Code 等客户端对未识别模型会剔除 image 块,本地截图
	// 路径作为纯 text 块发来。先扫 text 块裸路径读图 OCR 注入,再交下方 DowngradeGeminiImagesToText
	// 做结构化 InlineData 降级。ocrSelf 自递归守卫与下方 image 降级共用同一头部判断。静默 miss 不报错。
	if r.Header.Get("X-Antigravity-OCR-Self") != "1" {
		if enriched := h.ocr.EnrichLocalImagePathsInGemini(geminiReq, userSession); enriched > 0 {
			h.log("✅ [Relay Compat] Gemini 检测到 %d 个本地图片路径,已读图 OCR 注入 text 块(会话 %s)", enriched, ocrSessionDisplay(userSession))
		}
	}

	// 假多模态转换自愈逻辑：检测当非 Gemini 模型遇到多模态图片时，自动调用本地多模态模型执行 OCR 转换
	// 由 L2 协议适配层 OCRService.DowngradeGeminiImagesToText 统一承载,替代原内联循环,
	// 行为逐行等价且可被第三方号池复用、可独立单测。
	hasImage := false
	for _, c := range geminiReq.Contents {
		for _, p := range c.Parts {
			if p.InlineData != nil && p.InlineData.Data != "" {
				hasImage = true
				break
			}
		}
		if hasImage {
			break
		}
	}

	// OCR 自递归守卫:若本请求来自 OCR 引擎跨号池出站(携带 X-Antigravity-OCR-Self: 1),
	// 其 image 块是给所选 Gemini 族多模态模型看的,跳过"非多模态→OCR 降级"的自愈逻辑。
	// 多模态判定由 h.ocr.modelSupportsImage 统一承载(配置优先 Multimodal 声明位 → 启发式模型名前缀
	// 白名单),替代原 `strings.Contains(..., "gemini")` 粗粒度判据:既覆盖 gemini 全系,也放行
	// qwen-vl / gpt-4o / glm-4v 等其它原生多模态上游,同时尊重用户在映射表显式声明的 false 否决。
	if hasImage && !h.ocr.modelSupportsImage(targetModelToQuery) && r.Header.Get("X-Antigravity-OCR-Self") != "1" {
		ocrModel := h.ocr.getOcrModel()
		h.log("⚠️ [Relay Compat] 检测到目标模型 %s 不支持多模态，但请求包含图片。正在自动通过本地 Gemini(%s)执行 OCR 和图片描述...（若截图含复杂代码/表格，可在设置中更换更强多模态 OCR 模型以提升转写保真）", targetModelToQuery, ocrModel)
		downgraded, ocrHits, ocrMisses, ocrErrDown := h.ocr.DowngradeGeminiImagesToText(geminiReq, userSession, targetModelToQuery)
		if ocrErrDown != nil {
			h.log("❌ [Relay Compat] %v", ocrErrDown)
		}
		if downgraded > 0 {
			h.log("✅ [Relay Compat] 图片 OCR 转换完成:降级 %d 张 | 缓存命中 %d / 未命中 %d", downgraded, ocrHits, ocrMisses)
		}
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

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(geminiReqBytes))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if h.accountMgr != nil {
		req.Header.Set("User-Agent", h.accountMgr.GetAntigravityUserAgent())
	} else {
		req.Header.Set("User-Agent", account.FormatAntigravityUserAgent(account.DefaultAntigravityCliVersion))
	}
	// 将用户的凭证传递给本地代理，本地代理将据此提取 sessionKey 自动粘性绑定账号池并执行扣费统计
	req.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	req.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	req.Header.Set("X-Antigravity-Original-Path", r.URL.Path)
	req.Header.Set("X-Antigravity-Original-Method", r.Method)
	// 客户端会话头透传:dispatchToGemini 上面不拷贝入站头(仅显式 Set 固定头),故 Antigravity
	// 池经 18443 代理选号时,Codex Session-Id / Claude X-Claude-Code-Session-Id 头会在此丢失 →
	// 18443 的 ExtractSessionKey 拿不到客户端会话键,全本地 Codex 会话共一个 auth:acc:<hex>。
	// 这里显式把 ExtractClientSessionHeader 结果(命中返回 claude:UUID/codex:UUID)透传给 18443,
	// 由 18443 优先读取作为 sticky 键,使 Antigravity 池也具备客户端会话级粘性。未命中(返回空串)
	// 不注入该头,18443 行为与旧行为一致(回退 ExtractSessionKey)。该头在上游剥离清单内,不外泄。
	if h.sessionRouter != nil {
		if clientSessHdr := h.sessionRouter.ExtractClientSessionHeader(r); clientSessHdr != "" {
			req.Header.Set("X-Antigravity-Client-Session", clientSessHdr)
		}
	}
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
		h.handleStreamResponse(r.Context(), w, resp.Body, userSession, clientModel, geminiModel, apiFormat, inboundInputTokens, streamIncludeUsage, startTime, r.URL.Path, reqID)
	} else {
		// 5. 非流式传输处理
		h.handleNormalResponse(w, resp.Body, userSession, clientModel, geminiModel, apiFormat, startTime, r.URL.Path, reqID)
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
