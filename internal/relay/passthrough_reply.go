package relay

// passthrough_reply.go: 透传上游响应回写与 usage 嗅探(passthroughReplyResponses
// Responses 回译 / passthroughReply 兜底回写 / passthroughWriteSuccess 纯透传 + OpenAI/
// Anthropic 形状分发 / proxyPassthroughOpenAI & proxyPassthroughAnthropic 逐帧透传 + usage
// 嗅探 / patchAnthropicNonStreamInputTokens 非流式 input_tokens 补齐 / isPassthroughHopHeader
// 透传头剔除 / isClientGone 客户端断开判定),从 passthrough_forwarder.go 按职责物理拆分,
// 同 package relay 跨文件符号自动解析,逐字等价零回归。isClientGone 当前无调用方(保留备用)。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/stats"
)

// passthroughReplyResponses 处理「入站 Responses API + 上游 OpenAI Chat」的响应回译。
// 上游返回 OpenAI Chat JSON/SSE(choices),Codex /v1/responses 客户端需要 Responses 事件流(response.completed)。
// 复用 NVIDIA 链路的转换器:
//   - 非流式:读全量上游 JSON → OpenAIChatToResponses → 写 Responses JSON。
//   - 流式:上游 OpenAI Chat SSE → OpenAIChatSSEToResponsesSSE → Responses SSE 事件序列。
func (h *APICompatHandler) passthroughReplyResponses(w http.ResponseWriter, r *http.Request, res *forwardResult, isStreaming bool, model string) {
	if res == nil || res.resp == nil {
		h.passthroughReply(w, r.Context(), res, isStreaming)
		return
	}
	defer res.resp.Body.Close()

	if res.resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.resp.Body)
		h.log("⚠️ [路由转发 Responses] 上游状态码 %d 非透传 | body: %s", res.resp.StatusCode, truncateBody(bodyBytes, 500))
		writeResponsesError(w, res.resp.StatusCode, bodyBytes)
		return
	}

	if !isStreaming {
		bodyBytes, err := io.ReadAll(res.resp.Body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "read upstream body failed: " + err.Error()})
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
		if res.logCtx.FirstByteRec != nil {
			res.logCtx.FirstByteRec.MarkFirstByte()
		}
		return
	}

	// 流式:上游 OpenAI Chat SSE → Responses SSE。
	flusher, ok := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush()
	}
	if res.logCtx.FirstByteRec != nil {
		res.logCtx.FirstByteRec.MarkFirstByte()
	}
	reqID := fmt.Sprintf("passthrough_resp_%d", time.Now().UnixNano())
	fw := newFlushWriter(reqID, bufio.NewWriter(w), flusher)
	OpenAIChatSSEToResponsesSSE(r.Context(), res.resp.Body, res.resp.Body, fw, model)
	fw.flush()
}

// passthroughReply 把 forwardResult 回写到客户端,并在 upstreamFormat 与入站协议不一致时做响应回译。
//
// 回译决策(由 h.passthroughReply 调用前已无法回到入站协议标记,故要求调用方在 handleRoutedForward
// 通过 isMessages/isChat/isResponses 推断 inboundFormat 并传入):
//   - inboundFmt==upstreamFormat:原样透传(流式边读边写、非流式拷 body);
//   - inboundFmt=openai + upstream=anthropic:响应 Anthropic→OpenAI 回译;
//   - inboundFmt=anthropic + upstream=openai:响应 OpenAI→Anthropic 回译;
//   - 失败兜底按 statusCode/body 回写;无 body 则 502。
//
// res.upstreamFormat 留空时视作 "openai"(向后兼容既有 deepseek/qwen 等非 other 调用方)。
func (h *APICompatHandler) passthroughReply(w http.ResponseWriter, ctx context.Context, res *forwardResult, isStreaming bool) {
	if res == nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "route forward: no result"})
		return
	}
	// 客户端主动取消(499):连接已断开,回写无意义,直接返回避免误写 502 掩盖取消语义。
	if res.statusCode == 499 {
		return
	}
	if res.resp != nil {
		defer res.resp.Body.Close()

		// 回译方向决策:仅当 upstreamFormat 与入站不一致时才转译;一致时纯透传。
		// inboundFmt 由调用方在 handleRoutedForward 推断后未透传,这里按 Content-Type 兜底推断:
		// 入站协议已知为 isMessages(由 handleRoutedForward 分支已决定),但本函数签名无 isMessages,
		// 故要求调用方在 res.upstreamFormat 已含上游形态后,由调用方决定是否回译。
		// 简化:本函数接收调用方传入的 isStreaming 与已在 res 中标记的 upstreamFormat,
		// 回译与否由 handleRoutedForward 在调用前按入站协议判断后通过专用回复路径处理。
		h.passthroughWriteSuccess(w, res, isStreaming)
		return
	}

	// 失败兜底。
	if res.body != nil && res.statusCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.statusCode)
		_, _ = w.Write(res.body)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "route forward exhausted: " + errStr(res.err)})
}

// passthroughWriteSuccess 是纯透传的成功响应回写(upstream==inbound, 无协议回译)。
// 透传同时嗅探 usage(仿 proxyNvidiaOpenAIPassthrough), 成功路径(200)经 recordOtherUsage
// 落库(请求日志/模型统计/趋势/中继与账号维度)。非流式先读全量 body 解析 usage 再原样写出;
// 流式逐行读 SSE 帧、逐帧原样透传, 顺带解析每个 chunk 的 usage 字段(OpenAI 末帧 usage 为权威值)。
//
// 按 res.upstreamFormat 分发到对应透传函数:
//   - upstream anthropic(Other 号池 Anthropic 格式组纯透传):proxyPassthroughAnthropic ——
//     必须就地修补 message_start.usage.input_tokens(上游缺 0 时本地估算补齐),否则 Claude Code
//     spinner 流首只有 ↓ 无 ↑;并按 Anthropic 形状(input_tokens/output_tokens)解析 usage 喂统计。
//   - upstream openai(默认 / 其它号池裸透传):proxyPassthroughOpenAI —— 按 OpenAI 末帧 usage 解析。
func (h *APICompatHandler) passthroughWriteSuccess(w http.ResponseWriter, res *forwardResult, isStreaming bool) {
	if res == nil || res.resp == nil {
		return
	}
	// 透传上游头(剔除 hop-by-hop 与鉴权),保持裸透传语义。
	for k, vs := range res.resp.Header {
		if isPassthroughHopHeader(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		if isStreaming {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
	}
	if isStreaming {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}

	var inUsage, outUsage, cachedUsage int
	if res.upstreamFormat == "anthropic" {
		inUsage, outUsage, cachedUsage = h.proxyPassthroughAnthropic(w, res.resp, isStreaming, res.logCtx.FirstByteRec, res.inboundBody)
	} else {
		inUsage, outUsage, cachedUsage = h.proxyPassthroughOpenAI(w, res.resp, isStreaming, res.logCtx.FirstByteRec)
	}
	// 成功路径(200)且 usage 非空时记录到五落点;非 200 / usage 为空 → recordOtherUsage 早退。
	if res.resp.StatusCode == http.StatusOK {
		h.recordOtherUsage(res.sess, res.usedModel, inUsage, outUsage, cachedUsage, res.usedAccPtr, res.logCtx)
	}
}

// proxyPassthroughOpenAI 透传上游 OpenAI Chat 响应到客户端, 同时嗅探 (inputTokens, outputTokens, cachedTokens)。
// 对偶 proxyNvidiaOpenAIPassthrough, 但入参是 forwardResult 拆出的 resp 与 isStreaming, 供
// passthroughWriteSuccess 复用。非流式全量读 body 解析 usage 后原样写出; 流式逐行透传 + 末帧
// usage 权威。上游非 200 直接透传错误体, usage 返回 0。
func (h *APICompatHandler) proxyPassthroughOpenAI(w http.ResponseWriter, resp *http.Response, isStreaming bool, firstByteRec *stats.FirstByteRecorder) (int, int, int) {
	if resp == nil {
		return 0, 0, 0
	}
	// 上游非 200: 直接透传错误体, 不嗅探 usage。
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return 0, 0, 0
	}
	if !isStreaming {
		// 非流式: 全量读 body, 解析 usage, 原样透传。
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"read upstream passthrough body failed"}`))
			return 0, 0, 0
		}
		var chatResp OpenAIChatResponse
		inUsage, outUsage, cachedUsage := 0, 0, 0
		if json.Unmarshal(bodyBytes, &chatResp) == nil {
			inUsage = chatResp.Usage.PromptTokens
			outUsage = chatResp.Usage.CompletionTokens
			cachedUsage = chatResp.Usage.CachedTokens()
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		// 非流式透传:WriteHeader+写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
		if firstByteRec != nil {
			firstByteRec.MarkFirstByte()
		}
		return inUsage, outUsage, cachedUsage
	}

	// 流式: 逐行嗅探 SSE, 逐行原样透传, 末帧 usage 为权威。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var inUsage, outUsage, cachedUsage int
	doneSent := false
	firstByteMarked := false
	markFirstByte := func() {
		if firstByteMarked || firstByteRec == nil {
			return
		}
		firstByteMarked = true
		// 幂等(FirstByteRecorder.sync.Once),首帧即记录上游真实首字延迟。
		firstByteRec.MarkFirstByte()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			_, _ = w.Write([]byte("\n"))
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		markFirstByte()
		_, _ = w.Write([]byte(line + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
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
	if !doneSent {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	return inUsage, outUsage, cachedUsage
}

// proxyPassthroughAnthropic 透传上游 Anthropic Messages 响应(/v1/messages)到客户端,
// 同时嗅探 input/output/cached usage 供统计落库 + 补齐 message_start.usage.input_tokens。
//
// 设计动机:Anthropic 官方真机在 message_start 即给出真实 input_tokens(服务端一进来即知);
// 但经第三方 Anthropic 镜像/网关代理时,该流首帧常缺 input_tokens 或为 0,导致 Claude Code
// 客户端(Claude Code spinner)流首只有 ↓(output 累计)而无 ↑(input) —— 因为其 ↑ 字段
// 完全由 message_start.usage.input_tokens(+cache_creation/cache_read)驱动,见官方协议:
// total input = input_tokens + cache_creation_input_tokens + cache_read_input_tokens。
// 本函数在透传流首 message_start 帧时,若 input_tokens <= 0 则用入站请求体本地估算
// (PatchAnthropicMessageStart → EnsureInputTokens → estimateInputTokens)就地补齐,
// 让 spinner 流首即显示非零 ↑;真实累计值仍由末帧 message_delta.usage 覆盖,结算精度不受影响。
//
// usage 解析按 Anthropic 形状(message_delta.usage.input_tokens/output_tokens/cumulative),
// 而非 OpenAI 的 prompt_tokens/completion_tokens(原 proxyPassthroughOpenAI 走 OpenAI 形状,
// 用于 anthropic 上游会恒读到 0,导致 recordOtherUsage 因 input==0&&output==0 早退、统计漏记)。
//
// inboundBody 为入站请求体字节,供补齐时估算;nil/空时由 EnsureInputTokens 保底 1。
//
// 返回累计 (input, output, cached):cached 取 message_delta.usage.cache_read_input_tokens。
func (h *APICompatHandler) proxyPassthroughAnthropic(w http.ResponseWriter, resp *http.Response, isStreaming bool, firstByteRec *stats.FirstByteRecorder, inboundBody []byte) (int, int, int) {
	if resp == nil {
		return 0, 0, 0
	}
	// 上游非 200: 直接透传错误体, 不嗅探 usage。
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return 0, 0, 0
	}
	if !isStreaming {
		// 非流式: 全量读 body, 解析 Anthropic usage, 原样透传。
		// Anthropic 非流式响应顶层 usage 即真实 input/output,无需补齐(镜像是非流式体的 usage 通常齐全);
		// 仍保险地用 EnsureInputTokens 在 input_tokens<=0 时按入站请求体估算补救。
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"read upstream passthrough body failed"}`))
			return 0, 0, 0
		}
		var anthResp AnthropicResponse
		inUsage, outUsage, cachedUsage := 0, 0, 0
		if json.Unmarshal(bodyBytes, &anthResp) == nil {
			inUsage = anthResp.Usage.InputTokens
			outUsage = anthResp.Usage.OutputTokens
			cachedUsage = anthResp.Usage.CachedTokens()
			if inUsage <= 0 {
				inUsage = EnsureInputTokens(0, inboundBody)
				// 把补齐值回写进透传 body,让客户端拿到(非流式没有 message_start 帧,
				// 客户端读顶层 usage;补齐后原样写出需改值)。
				bodyBytes = patchAnthropicNonStreamInputTokens(bodyBytes, inUsage)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(bodyBytes)
		// 非流式透传:WriteHeader+写出即首字时刻,触发 TTFT 打点(幂等 sync.Once)。
		if firstByteRec != nil {
			firstByteRec.MarkFirstByte()
		}
		return inUsage, outUsage, cachedUsage
	}

	// 流式: 逐行嗅探 Anthropic SSE, 逐行透传, message_start 缺 input_tokens 时补齐,
	// message_delta.usage(input_tokens/output_tokens/cache_read_input_tokens,均为累计)为权威。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var inUsage, outUsage, cachedUsage int
	doneSent := false
	firstByteMarked := false
	markFirstByte := func() {
		if firstByteMarked || firstByteRec == nil {
			return
		}
		firstByteMarked = true
		firstByteRec.MarkFirstByte()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			_, _ = w.Write([]byte("\n"))
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		markFirstByte()
		// 仅对含 message_start 的 data: 行就地补齐 input_tokens(流首缺失 → Claude Code spinner 无 ↑)。
		// 其余事件原样透传。
		outLine := line
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "message_start") {
			if patched := PatchAnthropicMessageStart([]byte(line), inboundBody); patched != nil {
				outLine = string(patched)
			}
		}
		_, _ = w.Write([]byte(outLine + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			doneSent = true
			continue
		}
		// Anthropic SSE 事件:message_start.message.usage.input_tokens 为输入 token 权威来源,
		// message_delta.usage 的累计 output_tokens 为输出权威;两者均嗅探。
		var ev struct {
			Type  string                  `json:"type"`
			Delta json.RawMessage         `json:"delta,omitempty"`
			Usage *AnthropicResponseUsage `json:"usage,omitempty"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if ev.Type == "message_start" {
			// message_start.message.usage.input_tokens 是输入 token 的权威来源
			// (Anthropic 协议: message_delta 通常只带 output_tokens, 不带 input_tokens)。
			// 此前注释声明"若上游给了 message_start.usage 也读一次作初值兜底"但未实现,
			// 导致遵循标准协议的第三方镜像(如 api.radium.cloud)统计 ↑ 恒为 0。
			var ms struct {
				Message struct {
					Usage AnthropicResponseUsage `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &ms) == nil && ms.Message.Usage.InputTokens > 0 {
				inUsage = ms.Message.Usage.InputTokens
			}
		} else if ev.Type == "message_delta" {
			// message_delta 的 usage 在 delta 内或事件顶层,且为累计值。
			// 仅当字段 > 0 时才覆盖,避免标准 Anthropic 的 message_delta(无 input_tokens)
			// 把 message_start 已设的 inUsage 清零。
			var d struct {
				Usage AnthropicResponseUsage `json:"usage"`
			}
			if json.Unmarshal(ev.Delta, &d) == nil && (d.Usage.InputTokens > 0 || d.Usage.OutputTokens > 0) {
				if d.Usage.InputTokens > 0 {
					inUsage = d.Usage.InputTokens
				}
				if d.Usage.OutputTokens > 0 {
					outUsage = d.Usage.OutputTokens
				}
				if c := d.Usage.CachedTokens(); c > 0 {
					cachedUsage = c
				}
			}
			// 部分 Anthropic 镜像把 usage 放在事件顶层而非 delta 内,作兜底。
			if ev.Usage != nil && (ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0) {
				if ev.Usage.InputTokens > 0 {
					inUsage = ev.Usage.InputTokens
				}
				if ev.Usage.OutputTokens > 0 {
					outUsage = ev.Usage.OutputTokens
				}
				if c := ev.Usage.CachedTokens(); c > 0 {
					cachedUsage = c
				}
			}
		}
	}
	// 流末兜底:上游 message_start 缺 input_tokens 且 message_delta 也未带时,
	// 按入站请求体估算(与非流式分支同口径),避免统计落库 ↑0。
	if inUsage == 0 && outUsage > 0 {
		inUsage = EnsureInputTokens(0, inboundBody)
	}
	if !doneSent {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	return inUsage, outUsage, cachedUsage
}

// patchAnthropicNonStreamInputTokens 把非流式 Anthropic 响应体顶层 usage.input_tokens 补齐为 estimated,
// 兜底场景:第三方 Anthropic 镜像非流式 usage.input_tokens 缺失/为 0 时,保证客户端读到的顶层 usage 非零。
// 用 map 重编以最小侵入(不破坏其它字段如 content/model);解析失败回退原 body,零负作用。
func patchAnthropicNonStreamInputTokens(body []byte, estimated int) []byte {
	if estimated <= 0 || len(body) == 0 {
		return body
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	rawUsage, ok := obj["usage"]
	if !ok {
		return body
	}
	var usage map[string]interface{}
	if json.Unmarshal(rawUsage, &usage) != nil {
		return body
	}
	cur := 0
	if v, exists := usage["input_tokens"]; exists {
		if n, ok := v.(float64); ok {
			cur = int(n)
		}
	}
	if cur > 0 {
		return body // 上游已给非零,不覆盖。
	}
	usage["input_tokens"] = estimated
	newUsage, err := json.Marshal(usage)
	if err != nil {
		return body
	}
	obj["usage"] = newUsage
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// isPassthroughHopHeader 判定是否为透传时应剔除的头(含鉴权,避免泄露 key)。
func isPassthroughHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "authorization", "www-authenticate", "proxy-authenticate",
		"proxy-authorization", "connection", "keep-alive",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

// isClientGone 判定 io.Copy 错误是否客户端断开(非转发器自身故障),仅供日志降噪。
func isClientGone(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "broken pipe") || strings.Contains(s, "connection reset")
}
