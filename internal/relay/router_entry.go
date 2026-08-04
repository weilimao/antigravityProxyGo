package relay

import (
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
	provider, upstreamModel, matched := h.resolveRoutedTarget(inModel)
	if !matched {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "no route rule matched for model: " + inModel,
		})
		return
	}

	h.log("🔀 [路由转发] model %s → provider %s (upstream %s) | stream=%v | user %s", inModel, provider, upstreamModel, isStreaming, userSession.UserKey)

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

	// 非 nvidia/google 族(如第三方 OpenAI 兼容 API DeepSeek/Moonshot/Qwen) → 通用透传转发器。Anthropic 入站先转 OpenAI Chat(裸透传不做回译,保持响应原样)。
	// 入站 /route/v1/messages 的响应是 OpenAI 形态,客户端若按 Anthropic 解析会不兼容 —— 这是本转发器
	// 的已知取舍:Anthropic 客户端请继续走 /nvidia/v1/messages(有回译) 或上游原生 Anthropic 端点;
	// /route/* 面向「已是 OpenAI 兼容」的客户端。若将来需要,在此加 Anthropic→OpenAI 请求 + OpenAI→Anthropic 响应回译即可。
	pf := &passthroughForward{h: h, accountMgr: h.accountMgr}
	res := pf.run(w, r, provider, upstreamModel, inModel, bodyBytes, isStreaming, userSession)
	h.passthroughReply(w, r.Context(), res, isStreaming)
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
