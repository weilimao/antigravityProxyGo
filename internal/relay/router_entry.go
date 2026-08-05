package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
	pf := &passthroughForward{h: h, accountMgr: h.accountMgr}
	res := pf.run(w, r, provider, targetGroupID, upstreamModel, inModel, bodyBytes, isStreaming, isChat, isResponses, isMessages, userSession)

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
	if inboundFmt == upFmt || (provider != "other" && upFmt == "openai") {
		h.passthroughReply(w, r.Context(), res, isStreaming)
		return
	}
	// 响应回译(upstream != inbound):入站 Anthropic + 上游 OpenAI → OpenAI→Anthropic 回译(NVIDIA 复用成果);
	// 入站 OpenAI/Responses + 上游 Anthropic → Anthropic→OpenAI 回译(passthrough_anthropic.go 成果)。
	h.passthroughReplyTranslated(w, r, res, isStreaming, inboundFmt, upFmt, upstreamModel)
}

// passthroughReplyTranslated 在上游协议与入站协议不一致时做响应回译后回写客户端。
// 复用 NVIDIA 链路的 OpenAI→Anthropic 回译成果(OpenAIChatToAnthropic / OpenAIChatSSEToAnthropicSSE)
// 与本文件族的 Anthropic→OpenAI 回译成果(AnthropicResponseToOpenAIChat / anthropicSSEToOpenAIChatSSEInto)。
func (h *APICompatHandler) passthroughReplyTranslated(w http.ResponseWriter, r *http.Request, res *forwardResult, isStreaming bool, inboundFmt, upFmt, model string) {
	if res == nil || res.resp == nil {
		// 回退到普通兜底回复(失败路径已在 passthroughReply 处理)。
		h.passthroughReply(w, r.Context(), res, isStreaming)
		return
	}
	defer res.resp.Body.Close()

	// 入站 Anthropic + 上游 OpenAI → OpenAI→Anthropic 回译(复用 NVIDIA 链路成果)。
	if inboundFmt == "anthropic" && upFmt == "openai" {
		h.replyOpenAIToAnthropic(w, r, res.resp, isStreaming, model)
		return
	}
	// 入站 OpenAI/Responses + 上游 Anthropic → Anthropic→OpenAI 回译。
	if inboundFmt == "openai" && upFmt == "anthropic" {
		h.replyAnthropicToOpenAI(w, r, res.resp, isStreaming, model)
		return
	}
	// 其它组合(含一致的)走纯透传。
	h.passthroughReply(w, r.Context(), res, isStreaming)
}

// replyOpenAIToAnthropic 把上游 OpenAI Chat 响应回译为 Anthropic Messages 响应回写客户端。
// 复用 OpenAIChatToAnthropic(非流式)与 OpenAIChatSSEToAnthropicSSE(流式),逻辑与 NVIDIA 链路一致。
func (h *APICompatHandler) replyOpenAIToAnthropic(w http.ResponseWriter, r *http.Request, resp *http.Response, isStreaming bool, model string) {
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
		_, _, _ = OpenAIChatSSEToAnthropicSSE(r.Context(), resp.Body, resp.Body, bw, model, flusher)
		_ = bw.Flush()
		if flusher != nil {
			flusher.Flush()
		}
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
}

// replyAnthropicToOpenAI 把上游 Anthropic Messages 响应回译为 OpenAI Chat 响应回写客户端。
// 非流式用 AnthropicResponseToOpenAIChat;流式用 AnthropicSSEToOpenAIChatSSE。
func (h *APICompatHandler) replyAnthropicToOpenAI(w http.ResponseWriter, r *http.Request, resp *http.Response, isStreaming bool, model string) {
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
		_, _, _ = AnthropicSSEToOpenAIChatSSE(resp.Body, w, model)
		if flusher != nil {
			flusher.Flush()
		}
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
