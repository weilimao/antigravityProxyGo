package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
)

// passthrough_forwarder.go: 通用 OpenAI 兼容透传转发器。
//
// 设计目标:让 /route/* 专属入口能「按入站 model 动态路由到任意 Provider 号池」,
// 而无需为每个第三方上游(DeepSeek / Moonshot / Qwen / 自建 OpenAI 兼容网关)各写一条专用链路。
// 适用面:上游遵循 OpenAI Chat Completions / Responses 协议(/v1/chat/completions),
// 鉴权为 Authorization: Bearer <api-key>,响应为标准 JSON 或 SSE(无需协议回译)。
//
// 已有的 NVIDIA 链路(internal/relay/nvidia.go)因其做了 Anthropic↔OpenAI 转译、流内
// ResourceExhausted 就地压缩、思考注入等重特化逻辑,保持原样不动;本转发器只承接「裸透传」场景。
// 当路由规则把某模型指向 "nvidia" Provider 时,仍走 handleNvidia,以复用其重特化能力。

// passthroughForward 是单次「按 model 选号池 → 选号 → 透传 → 换号重试」的执行体。
type passthroughForward struct {
	h       *APICompatHandler
	accountMgr *account.Manager
}

// forwardResult 透传结果,供调用方决定回写策略。
type forwardResult struct {
	resp        *http.Response // 成功时的上游响应(200),调用方负责 Body 关闭与回写
	statusCode  int            // 失败时最后一次上游/兜底状态码
	body        []byte         // 失败时的错误体
	err         error          // 失败原因
	attempts    int            // 实际尝试次数
	usedAccount string        // 成功时命中的账号 Email(供日志)
}

// passthroughMaxAttempts 是单请求最多换号次数(含首号)。
// 与 handleNvidia 的上限语义一致(maxAttempts<=5),避免单请求拖垮整池。
const passthroughMaxAttempts = 5

// passthroughSingleAcc429Retries 是单账号遇 429 时的原地退避重试上限。
const passthroughSingleAcc429Retries = 5

// passthroughCooldownShort / Long: 429/5xx / 401-403 网络错的冷却时长。
const (
	passthroughCooldownShortMs = 60 * 1000   // 60s
	passthroughCooldownLongMs  = 5 * 60 * 1000 // 5min
)

// runPassthroughForward 是路由转发器主流程。
//
// 入参:
//   - w/r: 客户端响应/请求(用于透传 context、流式回写);
//   - poolChannel: 目标号池 Provider(= account.Account.Provider,如 "deepseek"/"nvidia");
//   - upstreamModel: 已按规则改写后的发往上游模型名;
//   - inModel: 入站原模型名(供冷却分类与日志);
//   - bodyBytes: 原始入站请求体(可能为 OpenAI Chat / Responses 形态);
//   - isStreaming: 入站是否要求流式;
//
// 出参: *forwardResult。resp 非 nil 即成功(调用方负责回写与关闭),否则按 statusCode/body 兜底回写。
//
// 协议适配:入站 OpenAI Chat 与 Responses 都先归一化为 OpenAIChatRequest(复用既有
// ResponsesToOpenAIChat / 直解),改写 model 与 stream_options.include_usage 后 marshal 透传;
// 响应原样回写(SSE / JSON 均透传,不做回译),保持「裸透传」语义。
func (pf *passthroughForward) run(
	w http.ResponseWriter, r *http.Request,
	poolChannel, upstreamModel, inModel string,
	bodyBytes []byte, isStreaming bool,
	userSession *RelaySession,
) *forwardResult {
	res := &forwardResult{}

	// 1. 选号池可用账号(复用 GetAvailableAccountsForChannel,按 Provider 过滤 + 冷却过滤)。
	available := pf.accountMgr.GetAvailableAccountsForChannel(poolChannel, inModel)
	if len(available) == 0 {
		res.err = fmt.Errorf("%s pool empty (channel %s)", inModel, poolChannel)
		res.statusCode = http.StatusServiceUnavailable
		pf.h.log("⛔ [路由转发] 号池 %s 无可用账号(model=%s),回写 503", poolChannel, inModel)
		return res
	}

	maxAttempts := len(available)
	if maxAttempts > passthroughMaxAttempts {
		maxAttempts = passthroughMaxAttempts
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	skipped := make(map[string]bool)
	httpClient := pf.h.client
	if isStreaming {
		httpClient = pf.h.streamClient
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var active []*account.Account
		for _, a := range available {
			if !skipped[a.ID] {
				active = append(active, a)
			}
		}
		if len(active) == 0 {
			if res.err == nil {
				res.err = fmt.Errorf("all %s accounts in pool failed", poolChannel)
			}
			break
		}

		// 轮询选号(取活跃集第一个 → 与 handleNvidia 单账号模式语义一致的轻量选法)。
		acc := active[0]
		if isPassthroughAccountUnavailable(acc) {
			skipped[acc.ID] = true
			continue
		}

		// 构造上游请求体:入站 OpenAI Chat / Responses → OpenAIChatRequest,改写 model。
		upstreamReq, err := buildPassthroughUpstreamReq(bodyBytes, upstreamModel, isStreaming)
		if err != nil {
			res.err = err
			res.statusCode = http.StatusBadRequest
			pf.h.log("🚫 [路由转发] 构造上游请求体失败(模型 %s): %v", upstreamModel, err)
			return res
		}
		// image 自愈降级:第三方号池(DeepSeek/Moonshot/Qwen 等)目标模型大多不支持多模态,
		// 入站 OpenAI Chat 若含 image_url 数组形态 content,经 buildPassthroughUpstreamReq 的
		// json.Unmarshal(OpenAIChatRequest) 会被拒收(ChatMessage.Content 是 string)→ 400。
		// 故在对自然入站体(bodyBytes)降级后再重建上游请求体,确保 image_url 已转文本。
		// 仅在首轮 attempt 执行一次(同请求内换号不重复降级,OCR 结果已确定性)。
		if attempt == 0 {
			downBody, replacedDown, errDown, ocrHitsDown, ocrMissesDown, ocrSkippedDown := pf.h.ocr.DowngradeOpenAIChatImagesToText(bodyBytes, userSession)
			if errDown != nil {
				pf.h.log("⚠️ [路由转发] OpenAI Chat image 自愈降级出错(provider %s | 会话 %s): %v,继续原始请求", poolChannel, ocrSessionDisplay(userSession), errDown)
			} else if replacedDown > 0 {
				pf.h.log("✅ [路由转发] OpenAI Chat 检测到 %d 个 image 块,已本地 OCR 降级为纯文本(provider %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)", replacedDown, poolChannel, ocrSessionDisplay(userSession), ocrHitsDown, ocrMissesDown, ocrSkippedDown)
				upstreamReq, err = buildPassthroughUpstreamReq(downBody, upstreamModel, isStreaming)
				if err != nil {
					res.err = err
					res.statusCode = http.StatusBadRequest
					pf.h.log("🚫 [路由转发] 降级后重建上游请求体失败(模型 %s): %v", upstreamModel, err)
					return res
				}
			}
		}
		upstreamBody, err := json.Marshal(upstreamReq)
		if err != nil {
			res.err = err
			res.statusCode = http.StatusInternalServerError
			return res
		}

		// 2b. 上游 URL: {BaseURL}/v1/chat/completions,BaseURL 已含 /v1 则不重复拼。
		baseURL := strings.TrimRight(acc.BaseURL, "/")
		targetURL := baseURL + "/v1/chat/completions"
		if strings.HasSuffix(baseURL, "/v1") {
			targetURL = baseURL + "/chat/completions"
		}

		pf.h.log("🟢 [路由转发 %d/%d] %s 号池 → 账号 %s | model %s -> %s | %s", attempt+1, maxAttempts, poolChannel, acc.Email, inModel, upstreamModel, targetURL)

		// 2c. 单账号 429 原地退避 + 多状态码换号。
		var activeResp *http.Response
		ok := false
		for single := 1; single <= passthroughSingleAcc429Retries; single++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				res.err = err
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+acc.GetAccessToken())
			req.Header.Set("Accept", "application/json")

			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				res.err = errDo
				res.statusCode = http.StatusBadGateway
				pf.h.log("⚠️ [路由转发] 账号 %s 访问上游失败: %v", acc.Email, errDo)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				skipped[acc.ID] = true
				break
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				_ = resp.Body.Close()
				res.statusCode = resp.StatusCode
				res.err = fmt.Errorf("upstream %s 429", poolChannel)
				if single < passthroughSingleAcc429Retries {
					time.Sleep(2 * time.Second)
					continue
				}
				pf.h.log("⚠️ [路由转发] 账号 %s 重试 %d 次仍 429,冷冻换号", acc.Email, passthroughSingleAcc429Retries)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				skipped[acc.ID] = true
				break
			}

			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				res.statusCode = resp.StatusCode
				res.body = body
				res.err = fmt.Errorf("upstream %s %d", poolChannel, resp.StatusCode)
				pf.h.log("⚠️ [路由转发] 账号 %s 上游 %d,剔除换号", acc.Email, resp.StatusCode)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownLongMs, poolChannel, inModel)
				skipped[acc.ID] = true
				break
			}

			if resp.StatusCode >= 500 {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				res.statusCode = resp.StatusCode
				res.body = body
				res.err = fmt.Errorf("upstream %s server error %d", poolChannel, resp.StatusCode)
				pf.h.log("⚠️ [路由转发] 账号 %s 上游 5xx(%d),换号", acc.Email, resp.StatusCode)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				skipped[acc.ID] = true
				break
			}

			// 200 (含 SSE/JSON)。回写由调用方处理,此处只落 activeResp。
			activeResp = resp
			res.usedAccount = acc.Email
			ok = true
			break
		}

		if ok && activeResp != nil {
			res.resp = activeResp
			res.statusCode = activeResp.StatusCode
			return res
		}
		res.attempts++
	}

	return res
}

// isPassthroughAccountUnavailable 判定透传候选账号是否「不可用」(跳过换号)。
// 与 handleNvidia 的 IsNvidiaAvailable 口径一致,但通用化:不限制 Provider,
// 只要 Provider 匹配且启用、有 AccessToken 且配了 BaseURL 即可用。
func isPassthroughAccountUnavailable(a *account.Account) bool {
	return a == nil || !a.Enabled || a.GetAccessToken() == "" || strings.TrimSpace(a.BaseURL) == ""
}

// buildPassthroughUpstreamReq 把入站 body 归一化为 OpenAIChatRequest 并改写 model/stream_options。
// 入站可能是 OpenAI Chat(直解)或 Responses(ResponsesToOpenAIChat 转换);二者产出同构,
// 这里统一在产出后 set model,避免对两种入站各写一份改写逻辑。
func buildPassthroughUpstreamReq(bodyBytes []byte, upstreamModel string, isStreaming bool) (*OpenAIChatRequest, error) {
	// 先尝试 Chat 直解;失败再尝试 Responses。两路径产出 OpenAIChatRequest。
	var chatReq OpenAIChatRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err == nil && len(chatReq.Messages) > 0 {
		chatReq.Model = upstreamModel
		if isStreaming {
			ensureIncludeUsage(&chatReq)
		}
		return &chatReq, nil
	}
	// Responses 形态(如 Codex /v1/responses):input[] 而非 messages[]。
	u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("unrecognized openai/responses request body: %w", err)
	}
	if isStreaming {
		ensureIncludeUsage(u)
	}
	return u, nil
}

// ensureIncludeUsage 注入 stream_options.include_usage,确保上游 SSE 末尾吐 usage,
// 与 handleNvidia 的流式行为一致,供统计/计费链路回收 token。
func ensureIncludeUsage(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		req.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
}

// passthroughReply 把 forwardResult 回写到客户端。
// 成功(resp!=nil):流式时边读边写 body 并按上游 Content-Type 回写、SSE 头另设;
//   非流式直接拷 body。
// 失败:按 statusCode/body 兜底;无 body 则 502。
func (h *APICompatHandler) passthroughReply(w http.ResponseWriter, ctx context.Context, res *forwardResult, isStreaming bool) {
	if res == nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "route forward: no result"})
		return
	}
	if res.resp != nil {
		defer res.resp.Body.Close()
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
		w.WriteHeader(res.resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		if _, err := io.Copy(w, res.resp.Body); err != nil && !isClientGone(err) {
			h.log("⚠️ [路由转发] 回写上游响应体中断: %v", err)
		}
		if flusher != nil {
			flusher.Flush()
		}
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
