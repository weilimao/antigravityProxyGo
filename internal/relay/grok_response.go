package relay

// grok_response.go: Grok(x.ai) 上游响应回写客户端的「非流式/流式/错误/Responses」四类写回链路。
//
// 与 nvidia_response.go(writeNvidiaResponse 等)对偶, 关键差异:
//   - Grok 不接 NVIDIA 的「蓄流回放重试」(pullAnthropicStreamWithRetry)态机 —— xAI 官方端点稳定,
//     流式回译走「边转译边写」的简单直通链路(与 router_entry.go replyOpenAIToAnthropic 同款):
//       · anthropic 流式 → OpenAIChatSSEToAnthropicSSE(复用 NVIDIA 链路翻译成果, 逐帧 flush);
//       · anthropic 非流式 → OpenAIChatToAnthropic;
//       · responses 流式/非流式 → OpenAIChatSSEToResponsesSSE / OpenAIChatToResponses(复用 nvidia_responses.go);
//       · openai_chat → proxyNvidiaOpenAIPassthrough(OpenAI Chat 透传 + usage 嗅探, 函数本身与 NVIDIA
//         无耦合, 名称历史沿用, 直接复用零重复)。
//   - 上游非 200 错误体回写: anthropic 入站用 writeAnthropicErrorFromUpstream(翻译为 Anthropic 错误结构,
//     Claude Code 等 Anthropic 客户端据此识别失败); responses 入站用 writeResponsesError; openai_chat 入站
//     原样透传错误体(客户端按其协议解析)。
//   - 五落点统计经 recordGrokUsage(grok_usage.go): Family="grok"、PoolKey="grok"、relay 维度模型名带 "grok/" 前缀。
//
// 函数命名沿用 nvidia_ 前缀的历史惯例(表明其承载的请求日志/统计上下文类型), 但内部逻辑全部走通用翻译链,
// 与 NVIDIA NIM 上游无任何耦合 —— Grok 路径只复用「OpenAI Chat ↔ Anthropic/Responses」的翻译成果。

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// writeGrokResponse 把上游 OpenAI Chat 响应回译成入站协议并写回客户端。
// inboundKind: "openai_chat"(透传) | "anthropic"(回译为 Messages) | "responses"(回译为 Responses API)。
// r 为入站请求, 供流式分支透传 r.Context() 到 watchCancel(客户端取消即断 + 尾帧补发);
// 同时 r.Header 在装配 logCtx 时经 collectInboundHeadersForLog 采集为 ReqHeaders 落库。
// inboundBody 为入站原始请求体字节(handleGrok 在入口读出并全链路透传), 经 parseInboundBodyForLog
// 注入 logCtx.ReqBody 落库, 使前端详情弹窗能展示「入站时」的请求体。
// inboundInputTokens 为入站请求本地估算的输入 token 数(保底 1), 仅 anthropic 流式分支透传给
// OpenAIChatSSEToAnthropicSSE → message_start.usage.input_tokens, 让客户端流首即显示 ↑。
// startTs / firstByteRec 由 handleGrok 入口起算与全程共享, 用于请求日志的 DurationMs / FirstByteMs。
func (h *APICompatHandler) writeGrokResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, inboundKind string, isStreaming bool, model string, userSession *RelaySession, poolAccount *account.Account, inboundBody []byte, inboundInputTokens int, startTs time.Time, firstByteRec *stats.FirstByteRecorder) {
	defer resp.Body.Close()

	// logCtx: 在分发出站协议前统一组装请求日志上下文(与 writeNvidiaResponse 同构), 共享给四个下行
	// 分支的 recordGrokUsage 调用点。Host 优先取上游账号 BaseURL 的裸 host; poolAccount 为空时优先用
	// 入站 r.Host, 再回退占位 "grok"。Account 优先号池 Email, 缺则 userSession.UserID; SessionID 用
	// ocrSessionDisplay: SessionKey 优先(auth:acc:<16hex> 口径, 与 nvidia/antigravity 链路同款), 空则
	// 回退 userSession.Token, 再空回退 UserID。
	var logCtx grokLogCtx
	logCtx.Method = r.Method
	logCtx.Path = r.URL.Path
	logCtx.StartTs = startTs
	// 复用 handleGrok 已完成打点(firstByteRec.MarkFirstByte 在 200 响应头到达后已触发)的共享 TTFT 打点器,
	// 与 writeNvidiaResponse 同口径: 不在此新建(会丢已打点)。
	logCtx.FirstByteRec = firstByteRec
	logCtx.StatusCode = resp.StatusCode
	logCtx.Host = "grok"
	if r.Host != "" {
		logCtx.Host = r.Host
	}
	logCtx.SessionID = ""
	if poolAccount != nil {
		logCtx.Host = grokHostFromBaseURL(poolAccount.BaseURL)
		logCtx.Account = poolAccount.Email
	}
	if userSession != nil {
		logCtx.SessionID = ocrSessionDisplay(userSession)
		if logCtx.Account == "" {
			logCtx.Account = userSession.UserID
		}
	}

	// 入站请求头/请求体落库注入(与 writeNvidiaResponse 同口径):
	//   - inboundBody 为入站原始请求体字节, 与前端「入站时」展示语义一致;
	//   - r.Header 经 collectInboundHeadersForLog 对 Authorization / x-api-key 等敏感头以 "<redacted>" 占位,
	//     杜绝把客户端凭证写进仪表盘与 SQLite。
	logCtx.ReqBody = parseInboundBodyForLog(inboundBody)
	logCtx.ReqHeaders = collectInboundHeadersForLog(r.Header)

	switch inboundKind {
	case "anthropic":
		// 入站是 Anthropic: 需把上游 OpenAI Chat 响应回译成 Anthropic Messages。
		if isStreaming {
			h.writeGrokAnthropicStream(w, r, resp, model, userSession, poolAccount, inboundInputTokens, logCtx)
			return
		}
		h.writeGrokAnthropicNormal(w, resp, model, userSession, poolAccount, logCtx)
		return

	case "responses":
		// 入站是 Responses API(codex /v1/responses): 把上游 OpenAI Chat 响应回译成 Responses 格式
		// (复用 nvidia_responses.go 的 writeNvidiaResponsesNormal / writeNvidiaResponsesStream —— 二者内部
		// 调 recordNvidiaUsage, 这里改调与之同构的 writeGrokResponses* 把落库切到 Family="grok")。
		if isStreaming {
			h.writeGrokResponsesStream(w, r, resp, model, userSession, poolAccount, logCtx)
			return
		}
		h.writeGrokResponsesNormal(w, resp, model, userSession, poolAccount, logCtx)
		return

	default:
		// 入站是 OpenAI Chat: 直接透传上游响应(含流式 SSE), 边透传边嗅探 usage, 统计口径与 Anthropic 入站一致。
		// proxyNvidiaOpenAIPassthrough 是通用 OpenAI Chat 透传+usage 嗅探函数, 与 NVIDIA 无耦合, 直接复用。
		inUsage, outUsage, cachedUsage := h.proxyNvidiaOpenAIPassthrough(r.Context(), w, resp, isStreaming, logCtx.FirstByteRec)
		h.recordGrokUsage(userSession, model, inUsage, outUsage, cachedUsage, poolAccount, logCtx)
	}
}

// writeGrokAnthropicStream 处理流式 Anthropic 入站: 上游 OpenAI Chat SSE → Anthropic SSE, 边转译边写。
// 与 NVIDIA 的蓄流回放重试(writeNvidiaAnthropicStream)不同 —— Grok 走简单直通链路(与 router_entry.go
// replyOpenAIToAnthropic 同款), 不做断流重试(xAI 官方端点稳定); 响应头对齐 SSE 不缓冲规范。
// inboundInputTokens 写入 message_start.usage.input_tokens(保底 1), 让客户端流首即显示 ↑;
// 真实累计值由末帧 message_delta.usage 覆盖, 结算精度不受影响。
func (h *APICompatHandler) writeGrokAnthropicStream(w http.ResponseWriter, r *http.Request, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, inboundInputTokens int, logCtx grokLogCtx) {
	if resp.StatusCode != http.StatusOK {
		// 上游非 200: 翻译成 Anthropic 标准错误结构回写(原裸透 OpenAI JSON 会让 CLI 卡住/报奇怪错误)。
		bodyBytes, _ := io.ReadAll(resp.Body)
		h.writeAnthropicErrorFromUpstream(w, resp.StatusCode, bodyBytes)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.log("⚠️ [Grok Anthropic 流式] http.ResponseWriter 不支持 Flusher, 降级为仅 bufio flush (SSE 即时性可能打折)")
	}
	// 响应头对齐 compat.go Gemini 链路保证 SSE 不被反代/框架缓冲(与 writeNvidiaAnthropicStream 同口径):
	//   - X-Accel-Buffering: no 禁止 Nginx 聚合 SSE;
	//   - http.Flusher 逐帧 push 到 TCP socket, 避免仅写到 http.ResponseWriter 内部缓冲。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush() // 立即把响应头推给客户端, 让其尽早进入 SSE 等待状态
	}
	// 流式 Anthropic 头已推给客户端: 触发 TTFT 打点(幂等 sync.Once, 首帧即记录)。
	// handleGrok 在 200 响应头到达时已 MarkFirstByte 过一次, 此处幂等 sync.Once 二次调用无副作用兜底。
	logCtx.FirstByteRec.MarkFirstByte()
	bw := bufio.NewWriter(w)
	// 复用 NVIDIA 链路的 OpenAIChatSSEToAnthropicSSE(OpenAI Chat SSE → Anthropic SSE 翻译 + 逐帧 flush):
	// 该函数是纯翻译器, 与 NVIDIA 上游无耦合, 透传 r.Context() 到 watchCancel 实现客户端取消即断 +
	// message_delta/message_stop 尾帧补发。inboundInputTokens 填 message_start.usage.input_tokens。
	in, out, cached, _ := OpenAIChatSSEToAnthropicSSE(r.Context(), resp.Body, resp.Body, bw, model, inboundInputTokens, flusher)
	_ = bw.Flush()
	if ok {
		flusher.Flush()
	}
	h.recordGrokUsage(userSession, model, in, out, cached, poolAccount, logCtx)
}

// writeGrokAnthropicNormal 处理非流式 Anthropic 入站: 读全量上游 OpenAI Chat 响应 → 回译 → 写出。
// 与 writeNvidiaAnthropicNormal 同构, 仅落库切到 recordGrokUsage(Family="grok")。
func (h *APICompatHandler) writeGrokAnthropicNormal(w http.ResponseWriter, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx grokLogCtx) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
		return
	}
	if resp.StatusCode != http.StatusOK {
		// 上游非 200: 翻译成 Anthropic 标准错误结构回写(原裸透 OpenAI JSON 会让 CLI 无法识别)。
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
	// 非流式 Anthropic: WriteHeader+写出即首字时刻, 触发 TTFT 打点(幂等 sync.Once)。
	logCtx.FirstByteRec.MarkFirstByte()

	// 落库: cached 取已回译的 AnthropicResponseUsage.CacheReadInputTokens
	// (OpenAIChatToAnthropic 已从上游 chatResp.Usage.CachedTokens() 填充, xAI 端点若支持 prompt caching
	// 即如实透传 cache 命中, 否则恒 0)。
	h.recordGrokUsage(userSession, model, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, anthResp.Usage.CachedTokens(), poolAccount, logCtx)
}

// writeGrokResponsesNormal 处理非流式 Responses 入站: 读全量上游 OpenAI Chat 响应 → 回译 → 写出。
// 与 writeNvidiaResponsesNormal 同构, 仅落库切到 recordGrokUsage(Family="grok")。
func (h *APICompatHandler) writeGrokResponsesNormal(w http.ResponseWriter, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx grokLogCtx) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
		return
	}
	if resp.StatusCode != http.StatusOK {
		// 上游非 200: 包成 Responses 风格错误体透传(复用 nvidia_responses.go, 与 NVIDIA 无耦合)。
		h.log("⚠️ [Grok Responses] 上游状态码 %d 非透传 | body: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeResponsesError(w, resp.StatusCode, bodyBytes)
		return
	}
	var chatResp OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "invalid openai response json: " + err.Error()})
		return
	}
	rr := OpenAIChatToResponses(&chatResp, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(jsonString(rr)))
	// 非流式 Responses: WriteHeader+写出即首字时刻, 触发 TTFT 打点(幂等 sync.Once)。
	logCtx.FirstByteRec.MarkFirstByte()

	// 落库: cached 取上游 OpenAI Chat usage 的缓存命中口径(chatResp.Usage.CachedTokens()),
	// xAI 端点若支持 prompt caching 即如实计入, 否则恒 0。
	h.recordGrokUsage(userSession, model, rr.Usage.InputTokens, rr.Usage.OutputTokens, chatResp.Usage.CachedTokens(), poolAccount, logCtx)
}

// writeGrokResponsesStream 处理流式 Responses 入站: 上游 OpenAI Chat SSE → Responses SSE 事件序列。
// 与 writeNvidiaResponsesStream 同构, 仅落库切到 recordGrokUsage(Family="grok")。
func (h *APICompatHandler) writeGrokResponsesStream(w http.ResponseWriter, r *http.Request, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, logCtx grokLogCtx) {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		h.log("⚠️ [Grok Responses 流式] 上游状态码 %d 非透传 | body: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeResponsesError(w, resp.StatusCode, bodyBytes)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.log("⚠️ [Grok Responses 流式] http.ResponseWriter 不支持 Flusher, 降级为仅 bufio flush")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush()
	}
	// 流式 Responses 头已推给客户端: 触发 TTFT 打点(幂等 sync.Once, 首帧即记录)。
	logCtx.FirstByteRec.MarkFirstByte()

	reqID := "grok_resp_" // OpenAIChatSSEToResponsesSSE 内部自构 streamID, 此处 reqID 仅符合 fw 构造签名(不参与输出)。
	_ = reqID
	fw := newFlushWriter("grok_resp", bufio.NewWriter(w), flusher)
	// 复用 nvidia_responses.go 的 OpenAIChatSSEToResponsesSSE(OpenAI Chat SSE → Responses SSE 事件序列):
	// 该函数是纯翻译器, 与 NVIDIA 上游无耦合, 透传 r.Context() 到 watchCancel 实现客户端取消即断 +
	// response.completed 尾帧自动补发。
	in, out, cached := OpenAIChatSSEToResponsesSSE(r.Context(), resp.Body, resp.Body, fw, model)
	fw.flush()

	// 落库: cached 取上游末帧 usage 的缓存命中口径, xAI 端点若支持 prompt caching 即如实计入。
	h.recordGrokUsage(userSession, model, in, out, cached, poolAccount, logCtx)
}
