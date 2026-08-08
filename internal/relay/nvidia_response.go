package relay

// nvidia_response.go 收纳 NVIDIA 上游响应回写客户端的「非流式/直通/错误」三类写回链路。
// 从 nvidia.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。
//
// 本文件覆盖:
//   - (h *APICompatHandler) writeNvidiaResponse            统一响应回写
//   - (h *APICompatHandler) proxyNvidiaOpenAIPassthrough    OpenAI 透传
//   - (h *APICompatHandler) writeAnthropicErrorFromUpstream 上游错误转 Anthropic 错误
//   - (h *APICompatHandler) writeNvidiaAnthropicNormal      非流式 OpenAI->Anthropic 转写

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// writeNvidiaResponse 把上游 OpenAI Chat 响应回译成入站协议并写回客户端。
// inboundKind: "openai_chat"（透传）| "anthropic"（回译为 Messages）| "responses"（回译为 Responses API）。
// r 为入站请求,供流式分支透传 r.Context() 到 watchCancel,实现客户端取消即断 + 尾帧补发。
// writeNvidiaResponse 把上游响应按入站协议类型回写客户端。targetURL/upstreamBody 仅对 Anthropic 流式
// 入站有意义(供蓄流回放链路原账号重建上游请求实现断流重试);其余链路忽略这两个参数,不参与重试。
// inboundInputTokens 为入站请求本地估算的输入 token 数(保底 1),仅 anthropic 流式分支透传给
// writeNvidiaAnthropicStream → message_start.usage.input_tokens,让客户端流首即显示 ↑。
func (h *APICompatHandler) writeNvidiaResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, inboundKind string, isStreaming bool, model string, userSession *RelaySession, poolAccount *account.Account, targetURL string, upstreamBody []byte, inboundInputTokens int, startTs time.Time, firstByteRec *stats.FirstByteRecorder) {
	defer resp.Body.Close()

	// logCtx: 在分发出站协议前统一组装请求日志上下文, 共享给四个下行函数的 recordNvidiaUsage 调用点。
	// Host 优先取上游账号 BaseURL 的裸 host; poolAccount 为空时优先用入站 r.Host, 再回退占位 "nvidia"
	// (r.Host 为入站 Host 头, 比 "nvidia" 更可读; 整段不直接解引用 poolAccount, 故无 nil panic)。
	// Path/Method 取入站 r; Account 优先号池 Email, 缺则 userSession.UserID; SessionID 用
	// ocrSessionDisplay:SessionKey 优先(auth:acc:<16hex>,与 antigravity 号池链路 handler.go:442
	// 同款口径,也跟选号/降级日志里打出的会话 ID 同源),空则回退 userSession.Token(正式登录态),
	// 再空则回退 UserID(sk-ant bypass 场景 Token 恒空,SessionKey 已注入故走首项)。这样请求日志
	// 「会话 ID」列从恒 "-" 变为 auth:acc 口径值,与 antigravity 行可观测性对齐。
	var logCtx nvidiaLogCtx
	logCtx.Method = r.Method
	logCtx.Path = r.URL.Path
	logCtx.StartTs = startTs
	// 复用 handleNvidia 已完成打点(firstByteRec.MarkFirstByte 在 200 响应头到达、Peek 阻塞之前触发)
	// 的共享 TTFT 打点器, 而非在此处新建——新建会丢已打点, 且此函数在流式分支被 Peek(1024) 阻塞后才
	// 调用, 若在此创建并等 message_start 才打点, 小首帧场景 FirstByteMs 会兜底≈DurationMs。
	logCtx.FirstByteRec = firstByteRec
	logCtx.StatusCode = resp.StatusCode
	logCtx.Host = "nvidia"
	if r.Host != "" {
		logCtx.Host = r.Host
	}
	logCtx.SessionID = ""
	if poolAccount != nil {
		logCtx.Host = nvidiaHostFromBaseURL(poolAccount.BaseURL)
		logCtx.Account = poolAccount.Email
	}
	if userSession != nil {
		logCtx.SessionID = ocrSessionDisplay(userSession)
		if logCtx.Account == "" {
			logCtx.Account = userSession.UserID
		}
	}

	switch inboundKind {
	case "anthropic":
		// 入站是 Anthropic：需要把上游 OpenAI Chat 响应回译成 Anthropic Messages
		if isStreaming {
			h.writeNvidiaAnthropicStream(w, r, resp, model, userSession, poolAccount, targetURL, upstreamBody, inboundInputTokens, logCtx)
			return
		}
		h.writeNvidiaAnthropicNormal(w, resp, model, userSession, poolAccount, logCtx)
		return

	case "responses":
		// 入站是 Responses API(codex /v1/responses)：把上游 OpenAI Chat 响应回译成 Responses 格式。
		// 非流式聚合后回译；流式逐 SSE chunk 重写成 Responses 事件序列。
		if isStreaming {
			h.writeNvidiaResponsesStream(w, r, resp, model, userSession, poolAccount, logCtx)
			return
		}
		h.writeNvidiaResponsesNormal(w, resp, model, userSession, poolAccount, logCtx)
		return

	default:
		// 入站是 OpenAI Chat：直接透传上游响应（含流式 SSE）。
		// 方案 A：边透传边嗅探 usage，非流式从全量 JSON 提 usage，
		// 流式从 SSE 末帧 data:{...usage...} 提 usage，统计口径与 Anthropic 入站一致。
		inUsage, outUsage, cachedUsage := h.proxyNvidiaOpenAIPassthrough(r.Context(), w, resp, isStreaming, logCtx.FirstByteRec)
		h.recordNvidiaUsage(userSession, model, inUsage, outUsage, cachedUsage, poolAccount, logCtx)
	}
}

// proxyNvidiaOpenAIPassthrough 处理入站为 OpenAI Chat 时的上游响应透传，
// 同时嗅探出 (inputTokens, outputTokens, cachedTokens) 用于号池成员账号维度统计。
// 透传坚持逐字节不变：非流式先读全量 body 解析 usage 再原样写出；
// 流式逐行读 SSE 帧、逐帧原样透传，顺带解析每个 chunk 的 usage 字段(OpenAI 末帧 usage 字段为权威值)。
// cachedTokens 取上游 usage 的缓存命中口径(prompt_cache_hit_tokens 或 prompt_tokens_details.cached_tokens,
// 由 OpenAIChatUsage.CachedTokens() 统一解析),当前 NVIDIA 官方 NIM 端不回报 cache 字段,恒 0。
// 上游非 200(错误/限流/鉴权失败等)直接透传原 body，usage 返回 0 不计入号池账号成本。
// 返回 (inTokens, outTokens, cachedTokens)。
//
// ctx 为入站请求 r.Context()：流式透传时客户端取消 → watchCancel 捕获 ctx.Done() 立即
// Close 上游 resp.Body → scanner.Scan() 退出；随后在循环外补发一帧 data: [DONE]\n\n,
// 给 OpenAI 客户端 SDK 明确的流结束语义(避免客户端卡等上游末帧)。
func (h *APICompatHandler) proxyNvidiaOpenAIPassthrough(ctx context.Context, w http.ResponseWriter, resp *http.Response, isStreaming bool, firstByteRec *stats.FirstByteRecorder) (int, int, int) {
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
		return 0, 0, 0
	}

	if !isStreaming {
		// 非流式：全量读 body 解析 usage，原样透传。
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"read upstream passthrough body failed"}`))
			return 0, 0, 0
		}
		// 仅在解析成功时记 usage，避免坏响应污染统计；body 始终原样透传。
		var chatResp OpenAIChatResponse
		inUsage, outUsage, cachedUsage := 0, 0, 0
		if json.Unmarshal(bodyBytes, &chatResp) == nil {
			inUsage = chatResp.Usage.PromptTokens
			outUsage = chatResp.Usage.CompletionTokens
			cachedUsage = chatResp.Usage.CachedTokens()
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		// 非流式 OpenAI 透传:WriteHeader+写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
		// 此前缺失该打点 → FirstByteMs 兜底=DurationMs,前端「响应时间」被误读为「请求耗时」。
		if firstByteRec != nil {
			firstByteRec.MarkFirstByte()
		}
		return inUsage, outUsage, cachedUsage
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
	firstByteMarked := false
	markFirstByte := func() {
		if !firstByteMarked && firstByteRec != nil {
			firstByteMarked = true
			firstByteRec.MarkFirstByte() // 首帧即记录上游首字延迟(幂等 sync.Once)
		}
	}
	scanner := bufio.NewScanner(resp.Body)
	// 单帧可能较大(尤其带工具调用/长内容)，放宽单行上限避免截断丢 usage。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var inUsage, outUsage, cachedUsage int
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
		markFirstByte() // 首个非空 SSE 帧即上游首字时刻,触发 TTFT 打点(幂等)
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
			cachedUsage = chunk.Usage.CachedTokens()
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
	return inUsage, outUsage, cachedUsage
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

func (h *APICompatHandler) writeNvidiaAnthropicNormal(w http.ResponseWriter, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx nvidiaLogCtx) {
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
	// 非流式 Anthropic:WriteHeader+写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
	// 此前缺失该打点 → FirstByteMs 兜底=DurationMs,前端「响应时间」被误读为「请求耗时」。
	logCtx.FirstByteRec.MarkFirstByte()

	// 配额/统计回调(复用 statsTracker)
	// 非流式 Anthropic 入站 cached 取自已回译的 AnthropicResponseUsage.CacheReadInputTokens
	// (OpenAIChatToAnthropic 已从上游 chatResp.Usage.CachedTokens() 填充),当前 NVIDIA 官方
	// NIM 不回报 cache,恒 0;一旦上游/兼容端点回报 cache 字段,此处即如实计入。
	h.recordNvidiaUsage(userSession, model, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, anthResp.Usage.CachedTokens(), poolAccount, logCtx)
}
