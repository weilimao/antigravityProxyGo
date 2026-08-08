package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/stats"
)

// router_entry.go: 通用按模型路由入口 /route/* 的实现。
//
// 与既有链路的分工:
//   - /v1/chat/completions 等  → 走 handleOpenAIChat(强绑 Gemini 上游);
//   - /nvidia/* 与 /vc/*        → 走 handleNvidia(nvidia 号池重特化链路);
//   - /route/* (本文件)         → 按 RelayModelRoutes 规则把入站 model 动态分发到任意
//     Provider 号池:命中 "nvidia" 复用 handleNvidia,否则走通用透传转发器(passthrough_forwarder.go)。
//
// 设计动机:仓库未来会接 DeepSeek/Qwen/Moonshot 等任意 OpenAI 兼容号池,每加一个不必再写新链路,
// 只需前端在 RelayModelRoutes 里加一条「pattern → provider」规则,并给该 provider 存若干带
// BaseURL+APIKey 的账号即可。

// routedRoutePrefixMatch 判定 path 是否落在 /route 前缀下。
// 与 nvidiaAliasPrefixMatch 同等精度:相等或紧跟 / 子路径才命中,排除 /router / /routerfoo 等误吞。
func routedRoutePrefixMatch(path string) bool {
	if path == "/route" {
		return true
	}
	return strings.HasPrefix(path, "/route/")
}

// handleRoutedForward 是 /route/* 的主分发:按入站 model → 选目标号池 → 转发。
//
// 支持的入站端点(在 /route 前缀下):
//   /route/v1/chat/completions   OpenAI Chat 形态(含 stream)
//   /route/v1/responses          Responses 形态(Codex)
//   /route/v1/messages           Anthropic 形态(转 OpenAI 后透传,不做回译)
//   /route/v1/models             模型列表(展示当前 RelayModelMapping 里 Expose 的模型 + 动态 owned_by)
func (h *APICompatHandler) handleRoutedForward(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")

	// GET 连通性测试与模型列表: 当客户端将 BaseURL 设为 http://[host]:18444/route，
	// 发起 GET /route, GET /route/v1 或 GET /route/v1/models 探测时，统一返回 200 OK 与模型列表！
	if r.Method == http.MethodGet && (path == "/route" || path == "/route/v1" || path == "/route/v1/models" || strings.HasSuffix(path, "/models")) {
		h.handleModels(w, r)
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "unsupported /route endpoint"})
		return
	}

	// 兼容带有或不带有 /v1 前缀的各类客户端生成端点
	isChat := strings.HasSuffix(path, "/v1/chat/completions") || strings.HasSuffix(path, "/chat/completions")
	isResponses := strings.HasSuffix(path, "/v1/responses") || strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
	isMessages := strings.HasSuffix(path, "/v1/messages") || strings.HasSuffix(path, "/messages")
	if !isChat && !isResponses && !isMessages {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "unsupported /route endpoint: use /route/v1/chat/completions, /route/v1/responses or /route/v1/messages",
		})
		return
	}

	// 读入站 body(带超时,复用 nvidia 的 readBodyWithTimeout 防 handler 钉死)。
	bodyBytes, err := readBodyWithTimeout(r, nvidiaInboundReadTimeout)
	if err != nil {
		if errors.Is(err, ErrBodyReadTimeout) {
			h.log("⏱️ [路由转发] 入站 body 读取超时 %s,回写 408", nvidiaInboundReadTimeout)
			writeJSON(w, http.StatusRequestTimeout, map[string]interface{}{"error": "request body read timeout"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	// 抽取入站 model 与 stream 字段(三协议取同名字段)。
	inModel, isStreaming, perr := extractRoutedModelStream(path, bodyBytes)
	if perr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": perr.Error()})
		return
	}
	if strings.TrimSpace(inModel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing model in request body"})
		return
	}

	// 解析规则表 → 目标 Provider 与上游模型。
	provider, targetGroupID, upstreamModel, matched := h.resolveRoutedTarget(inModel)
	if !matched {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "no route rule matched for model: " + inModel,
		})
		return
	}

	h.log("🔀 [路由转发] model %s → provider %s (group %s) (upstream %s) | stream=%v | user %s", inModel, provider, targetGroupID, upstreamModel, isStreaming, userSession.UserKey)

	// 命中 nvidia 号池 → 复用既有 handleNvidia 重特化链路(Anthropic↔OpenAI 回译、流内压缩、思考注入)。
	// handleNvidia 自行从 r 读取 body,故把已读的 bodyBytes 恢复回 r.Body 供其二次读取。
	if provider == "nvidia" {
		// 注意:TargetModel 为空时透传 inModel,但 handleNvidia 内部还有账号级档位映射,
		// 这里不越权改写 body model.handleNvidia 走 nvidia 池选号,与 /nvidia 路径行为完全等价。
		// 故直接把请求路径改写为 /nvidia/* 形态后交给 handleNvidia 处理更稳——但为避免破坏原始
		// r.URL.Path(下游统计依赖),采取轻量做法:把 model 改写为 upstreamModel 后塞回 body。
		newBody := patchRoutedBodyModel(bodyBytes, upstreamModel)
		r.Body = io.NopCloser(strings.NewReader(newBody))
		r.ContentLength = int64(len(newBody))
		h.handleNvidia(w, r, userSession)
		return
	}

	// 命中 antigravity / google / gcp 等 Google 族号池 → 复用既有 handleAnthropicMessages / handleOpenAIChat 核心链路(含 Gemini 官方/v1internal 动态调度与 OAuth 鉴权)。
	if isGoogleProvider(provider) {
		newBody := patchRoutedBodyModel(bodyBytes, upstreamModel)
		r.Body = io.NopCloser(strings.NewReader(newBody))
		r.ContentLength = int64(len(newBody))
		if isMessages {
			h.handleAnthropicMessages(w, r, userSession)
		} else {
			h.handleOpenAIChat(w, r, userSession)
		}
		return
	}

	// 非 nvidia/google 族(如第三方 OpenAI 兼容 API DeepSeek/Moonshot/Qwen,或 Other 号池自定义组) → 通用透传转发器。
	// Other 号池(provider=="other")按组内 Formats 与入站协议决定转译方向:
	//   - OpenAI 格式组 + 入站 OpenAI Chat → 纯透传;
	//   - OpenAI 格式组 + 入站 Anthropic Messages → 请求 AnthropicToOpenAIChat + 响应 OpenAI→Anthropic 回译;
	//   - Anthropic 格式组 + 入站 Anthropic Messages → 纯透传 Anthropic 协议(POST {BaseURL}/v1/messages);
	//   - Anthropic 格式组 + 入站 OpenAI Chat → 请求 OpenAI→Anthropic + 响应 Anthropic→OpenAI 回译;
	//   - 多选 formats 组 → 按入站协议选上游端点(若入站协议在组 Formats 内则直发原生端点,否则转译为 Formats 内的优先协议)。
	// passthroughForward 内部按 (provider, targetGroupID) 选号与决策端点。
	// start: 入站请求接入时刻, 作为「请求日志」DurationMs 的端到端耗时基准(与 NVIDIA/gemini/claude
	// 直连链路口径一致), 经下方装配 logCtx → recordOtherUsage 透传到落点4。
	start := time.Now()
	pf := &passthroughForward{h: h, accountMgr: h.accountMgr}
	res := pf.run(w, r, provider, targetGroupID, upstreamModel, inModel, bodyBytes, isStreaming, isChat, isResponses, isMessages, userSession)

	// 并发槽兜底释放:成功路径(pf.run 内 res.usedAccPtr 已赋值且未释放)在此 defer 释放,
	// 时序在下方各 reply 函数消费完 res.resp.Body 流式回写返回之后(defer LIFO,后注册先执行?
	// 不——defer 按注册顺序逆序执行,本 defer 先于 reply 调用注册,故在 reply 返回后才 fire,
	// 即「本次请求结束」点)。失败路径 usedAccPtr 为 nil(pf.run 内已显式 Release),本 defer 不双减。
	// nil 防御:res 可能为 nil(passthroughForward.run 理论不返回 nil,但防御保平安)。
	if res != nil && res.usedAccPtr != nil {
		usedAccID := res.usedAccPtr.ID
		defer h.accountMgr.ReleaseAccount(usedAccID)
	}

	// 装配 Other 号池请求日志/统计上下文(在 pf.run 返回后, 此时 res.usedAccPtr 已就绪)。
	// 供三个回写路径 passthroughWriteSuccess / replyOpenAIToAnthropic / replyAnthropicToOpenAI
	// 的 recordOtherUsage 使用。Host 优先取上游账号 BaseURL 裸 host, 其次入站 r.Host。
	res.usedModel = upstreamModel
	res.sess = userSession
	res.logCtx = passthroughLogCtx{
		Method:     r.Method,
		Host:       r.Host,
		Path:       r.URL.Path,
		StartTs:    start,
		StatusCode: 200, // 成功路径恒 200, 由各回写函数在非 200 时跳过统计
		// FirstByteRec 记录「请求接入 → 上游首字节回写」的 TTFT(首字响应延迟),
		// 与 DurationMs(端到端总耗时) 语义独立。与 NVIDIA 链路 (nvidia_response.go:46)
		// 对齐: 由下方各回写路径在首帧写出时 MarkFirstByte() 打点; 未打点时
		// recordOtherUsage 兜底以 DurationMs 填充, 避免恒 0。
		FirstByteRec: stats.NewFirstByteRecorder(start),
	}
	if res.usedAccPtr != nil {
		res.logCtx.Host = passthroughHostFromBaseURL(res.usedAccPtr.BaseURL)
		res.logCtx.Account = res.usedAccPtr.Email
	}
	if userSession != nil {
		res.logCtx.SessionID = ocrSessionDisplay(userSession)
		if res.logCtx.Account == "" {
			res.logCtx.Account = userSession.UserID
		}
	}

	// 入站请求体注入:供 Other 号池「上游 anthropic 纯透传」分支(proxyPassthroughAnthropic)
	// 在上游 message_start 缺 input_tokens 时,经 PatchAnthropicMessageStart / EnsureInputTokens
	// 用入站请求体本地估算补齐,让 Claude Code spinner 流首即显示 ↑。仅 isMessages + 上游
	// anthropic 分支实际消费;其余分支不读该字段。切片头拷贝零开销。
	if isMessages {
		res.inboundBody = bodyBytes
	}

	// 入站 input_tokens 估算:仅「入站 anthropic + 上游 openai」流式回译路径(replyOpenAIToAnthropic)
	// 用此值填 message_start.usage.input_tokens,让 Claude Code spinner 在流首即显示 ↑(否则流首 0
	// 使 spinner 只有 ↓ 无 ↑)。入站为 Anthropic 时 bodyBytes 即 Anthropic Messages 原始 body,用
	// estimateInputTokensFromBody 估算;非 anthropic 入站不回译 Anthropic 流,inboundInputTokens 留 0。
	// 真实累计值仍由末帧 message_delta.usage 覆盖,此值仅影响 spinner 进行中的 ↑ 显示。
	if isMessages {
		res.inboundInputTokens = estimateInputTokensFromBody(bodyBytes)
	}

	// 响应回译决策:入站协议 vs 上游 upstremFormat 不一致时转译,一致时纯透传。
	// inboundFmt 由入站路径推断;upstreamFormat 由 passthroughForward 按组 Formats 决策后落入 res.upstreamFormat。
	inboundFmt := ""
	if isMessages {
		inboundFmt = "anthropic"
	} else if isChat || isResponses {
		inboundFmt = "openai"
	}
	upFmt := res.upstreamFormat
	if upFmt == "" {
		upFmt = "openai"
	}
	// 非 other 号池或 deepseek/qwen 等 openai 兼容上游:res.upstreamFormat 留空视作 openai,与入站 openai/chat 一致 → 纯透传。
	if isResponses && upFmt == "openai" {
		// 入站 Responses API(codex /v1/responses) + 上游 OpenAI Chat:响应必须回译为 Responses 事件流,
		// 否则 Codex 收到 OpenAI Chat SSE(choices+[DONE])会判定「stream closed before response.completed」。
		// 复用 NVIDIA 链路的 OpenAIChat*→Responses 转换器(nvidia_responses.go)。
		h.passthroughReplyResponses(w, r, res, isStreaming, upstreamModel)
		return
	}
	if inboundFmt == upFmt || (provider != "other" && upFmt == "openai") {
		h.passthroughReply(w, r.Context(), res, isStreaming)
		return
	}
	// 响应回译(upstream != inbound):入站 Anthropic + 上游 OpenAI → OpenAI→Anthropic 回译(NVIDIA 复用成果);
	// 入站 OpenAI/Responses + 上游 Anthropic → Anthropic→OpenAI 回译(passthrough_anthropic.go 成果)。
	// inboundInputTokens 已在上方(入站 Anthropic 时)写入 res.inboundInputTokens,此处透传给
	// passthroughReplyTranslated → replyOpenAIToAnthropic → message_start.usage.input_tokens,
	// 让客户端(Claude Code spinner)流首即显示 ↑;真实累计值仍由末帧 message_delta.usage 覆盖。
	h.passthroughReplyTranslated(w, r, res, isStreaming, inboundFmt, upFmt, upstreamModel, res.inboundInputTokens)
}

// passthroughReplyTranslated 在上游协议与入站协议不一致时做响应回译后回写客户端。
// 复用 NVIDIA 链路的 OpenAI→Anthropic 回译成果(OpenAIChatToAnthropic / OpenAIChatSSEToAnthropicSSE)
// 与本文件族的 Anthropic→OpenAI 回译成果(AnthropicResponseToOpenAIChat / anthropicSSEToOpenAIChatSSEInto)。
// inboundInputTokens 仅「入站 Anthropic + 上游 OpenAI + 流式」分支消费,填 message_start.usage.input_tokens,
// 让客户端(Claude Code spinner)流首即显示 ↑;其余分支忽略。
func (h *APICompatHandler) passthroughReplyTranslated(w http.ResponseWriter, r *http.Request, res *forwardResult, isStreaming bool, inboundFmt, upFmt, model string, inboundInputTokens int) {
	if res == nil || res.resp == nil {
		// 回退到普通兜底回复(失败路径已在 passthroughReply 处理)。
		h.passthroughReply(w, r.Context(), res, isStreaming)
		return
	}
	defer res.resp.Body.Close()

	// 入站 Anthropic + 上游 OpenAI → OpenAI→Anthropic 回译(复用 NVIDIA 链路成果)。
	if inboundFmt == "anthropic" && upFmt == "openai" {
		h.replyOpenAIToAnthropic(w, r, res, isStreaming, model, inboundInputTokens)
		return
	}
	// 入站 OpenAI/Responses + 上游 Anthropic → Anthropic→OpenAI 回译。
	if inboundFmt == "openai" && upFmt == "anthropic" {
		h.replyAnthropicToOpenAI(w, r, res, isStreaming, model)
		return
	}
	// 其它组合(含一致的)走纯透传。
	h.passthroughReply(w, r.Context(), res, isStreaming)
}

// replyOpenAIToAnthropic 把上游 OpenAI Chat 响应回译为 Anthropic Messages 响应回写客户端。
// 复用 OpenAIChatToAnthropic(非流式)与 OpenAIChatSSEToAnthropicSSE(流式),逻辑与 NVIDIA 链路一致。
// 回写同时捕获 usage 并经 recordOtherUsage 落库(请求日志/模型统计/趋势/中继与账号维度)。
// inboundInputTokens 为入站 Anthropic 请求本地估算的输入 token 数(保底 1),仅流式分支写入
// message_start.usage.input_tokens,让客户端(Claude Code spinner)流首即显示 ↑;非流式分支忽略。
func (h *APICompatHandler) replyOpenAIToAnthropic(w http.ResponseWriter, r *http.Request, res *forwardResult, isStreaming bool, model string, inboundInputTokens int) {
	resp := res.resp
	if resp.StatusCode != http.StatusOK {
		// 上游非 200:错误体原样透传(含错误 JSON),客户端按其协议解析。
		for k, vs := range resp.Header {
			if isPassthroughHopHeader(k) {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if isStreaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		bw := bufio.NewWriter(w)
		// 首字节回写即 TTFT 打点(幂等 sync.Once), 与 NVIDIA 链路口径一致。
		res.logCtx.FirstByteRec.MarkFirstByte()
		in, out, cached, _ := OpenAIChatSSEToAnthropicSSE(r.Context(), resp.Body, resp.Body, bw, model, inboundInputTokens, flusher)
		_ = bw.Flush()
		if flusher != nil {
			flusher.Flush()
		}
		h.recordOtherUsage(res.sess, model, in, out, cached, res.usedAccPtr, res.logCtx)
		return
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
		return
	}
	var chatResp OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "invalid openai response json: " + err.Error()})
		return
	}
	anthResp := OpenAIChatToAnthropic(&chatResp)
	anthResp.Model = model
	payload, _ := json.Marshal(anthResp)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
	// 非流式:写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
	res.logCtx.FirstByteRec.MarkFirstByte()
	h.recordOtherUsage(res.sess, model, chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens, chatResp.Usage.CachedTokens(), res.usedAccPtr, res.logCtx)
}

// replyAnthropicToOpenAI 把上游 Anthropic Messages 响应回译为 OpenAI Chat 响应回写客户端。
// 非流式用 AnthropicResponseToOpenAIChat;流式用 AnthropicSSEToOpenAIChatSSE。
// 回写同时捕获 usage 并经 recordOtherUsage 落库(请求日志/模型统计/趋势/中继与账号维度)。
func (h *APICompatHandler) replyAnthropicToOpenAI(w http.ResponseWriter, r *http.Request, res *forwardResult, isStreaming bool, model string) {
	resp := res.resp
	if resp.StatusCode != http.StatusOK {
		for k, vs := range resp.Header {
			if isPassthroughHopHeader(k) {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	if isStreaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// 首字节回写即 TTFT 打点(幂等 sync.Once), 与 NVIDIA 链路口径一致。
		res.logCtx.FirstByteRec.MarkFirstByte()
		in, out, cached, _ := AnthropicSSEToOpenAIChatSSE(resp.Body, w, model)
		if flusher != nil {
			flusher.Flush()
		}
		h.recordOtherUsage(res.sess, model, in, out, cached, res.usedAccPtr, res.logCtx)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"read upstream body failed"}`))
		return
	}
	var anthResp AnthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthResp); err != nil {
		_, _ = w.Write([]byte(`{"error":"invalid anthropic response json"}`))
		return
	}
	chatResp := AnthropicResponseToOpenAIChat(&anthResp)
	chatResp.Model = model
	payload, _ := json.Marshal(chatResp)
	_, _ = w.Write(payload)
	// 非流式:写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
	res.logCtx.FirstByteRec.MarkFirstByte()
	h.recordOtherUsage(res.sess, model, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, anthResp.Usage.CachedTokens(), res.usedAccPtr, res.logCtx)
}

func isGoogleProvider(p string) bool {
	c := strings.ToLower(strings.TrimSpace(p))
	return c == "google" || c == "antigravity" || c == "gcp" || c == "project" || c == "gemini-cli" || c == ""
}

// extractRoutedModelStream 从入站 body 抽 model 与 stream。
// 三协议字段同构(model/stream),按路径分支选结构解析。
func extractRoutedModelStream(path string, body []byte) (model string, streaming bool, err error) {
	switch {
	case strings.HasSuffix(path, "/v1/messages"):
		var req AnthropicRequest
		if e := json.Unmarshal(body, &req); e != nil {
			return "", false, errors.New("invalid anthropic request: " + e.Error())
		}
		return req.Model, req.Stream, nil
	case strings.HasSuffix(path, "/v1/responses"):
		req, e := ParseUnifiedOpenAIRequest(body)
		if e != nil {
			return "", false, errors.New("invalid responses request: " + e.Error())
		}
		return req.Model, req.Stream, nil
	default: // /v1/chat/completions
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream,omitempty"`
		}
		if e := json.Unmarshal(body, &req); e != nil {
			return "", false, errors.New("invalid openai request: " + e.Error())
		}
		return req.Model, req.Stream, nil
	}
}

// patchRoutedBodyModel 替换入站 body 的顶层 model 字段(最小侵入)。
// 用 map 合并避免对各种协议结构体各写一份;model 之外的其它字段原样保留。
func patchRoutedBodyModel(body []byte, model string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		// body 不是合法 JSON 对象(极罕见),整体回退:不 patch 直接返回原 body。
		return string(body)
	}
	mb, _ := json.Marshal(model)
	obj["model"] = mb
	out, err := json.Marshal(obj)
	if err != nil {
		return string(body)
	}
	return string(out)
}
