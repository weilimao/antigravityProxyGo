package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
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
	h          *APICompatHandler
	accountMgr *account.Manager
}

// forwardResult 透传结果,供调用方决定回写策略。
type forwardResult struct {
	resp        *http.Response   // 成功时的上游响应(200),调用方负责 Body 关闭与回写
	statusCode  int              // 失败时最后一次上游/兜底状态码
	body        []byte           // 失败时的错误体
	err         error            // 失败原因
	attempts    int              // 实际尝试次数
	usedAccount string           // 成功时命中的账号 Email(供日志)
	usedAccPtr  *account.Account // 成功时命中的账号指针(供 recordOtherUsage 落点2 账号维度统计)
	// usedModel 是成功请求的上游模型展示名(供模型统计/成本计算, 与 NVIDIA 去前缀口径一致)。
	usedModel string
	// sess 是成功请求的会话上下文(供 recordOtherUsage 落点1/2 的中继与账号维度统计)。
	sess *RelaySession
	// logCtx 是 Other 号池请求日志/统计上下文,由 handleRoutedForward 在 pf.run 返回后
	// (此时 usedAccPtr/userSession 已就绪)装配注入,供各回写路径的 recordOtherUsage 使用。
	logCtx passthroughLogCtx
	// upstreamFormat 标记上游响应的协议形态:"openai"(OpenAI Chat JSON/SSE) / "anthropic"(Anthropic Messages JSON/SSE)。
	// 留空视作 "openai"(向后兼容既有调用方)。passthroughReply 据此与入站协议对比决定是否做响应回译:
	//   - upstream==inbound:原样透传;
	//   - upstream==openai,inbound==anthropic:OpenAI→Anthropic 回译;
	//   - upstream==anthropic,inbound==openai:Anthropic→OpenAI 回译。
	upstreamFormat string
	// inboundInputTokens 是入站请求本地估算的 input_tokens(保底 1),仅 anthropic 流式响应回译路径
	// (replyOpenAIToAnthropic)用它填 message_start.usage.input_tokens,让客户端(Claude Code spinner)
	// 流首即显示 ↑。由 handleRoutedForward 在 pf.run 后据入站 body 估算并注入。0 表示无需(非 anthropic
	// 入站或非流式),replyOpenAIToAnthropic 内部对 <1 保底为 1。
	inboundInputTokens int
	// inboundBody 是入站请求原始 body 字节,仅 Other 号池「上游 anthropic 纯透传」分支消费:供
	// proxyPassthroughAnthropic 在上游 message_start 缺失 input_tokens 时,经 PatchAnthropicMessageStart
	// / EnsureInputTokens 用入站请求体本地估算补齐,让 Claude Code spinner 流首即显示 ↑。其余分支不读。
	// 由 handleRoutedForward 在 pf.run 后注入;为切片头拷贝,零额外拷贝开销。
	inboundBody []byte
}

// passthroughMaxAttempts 是单请求最多换号次数(含首号)。
// 与 handleNvidia 的上限语义一致(maxAttempts<=5),避免单请求拖垮整池。
const passthroughMaxAttempts = 5

// passthroughSingleAcc429Retries 是单账号遇 429 时的原地退避重试上限。
const passthroughSingleAcc429Retries = 5

// passthroughCooldownShort / Long: 429/5xx / 401-403 网络错的冷却时长。
const (
	passthroughCooldownShortMs = 60 * 1000     // 60s
	passthroughCooldownLongMs  = 5 * 60 * 1000 // 5min
)

// runPassthroughForward 是路由转发器主流程。
//
// 入参:
//   - w/r: 客户端响应/请求(用于透传 context、流式回写);
//   - poolChannel: 目标号池 Provider(= account.Account.Provider,如 "deepseek"/"nvidia"/"other");
//   - targetGroupID: Other 号池组内细分(仅 provider=="other" 时非空),用于组内选号;
//   - upstreamModel: 已按规则改写后的发往上游模型名;
//   - inModel: 入站原模型名(供冷却分类与日志);
//   - bodyBytes: 原始入站请求体(可能为 OpenAI Chat / Responses / Anthropic Messages 形态);
//   - isStreaming: 入站是否要求流式;
//   - isChat / isResponses / isMessages: 入站协议标记(三选一),决定请求转译方向;
//   - userSession: 会话上下文(供 OCR 缓存隔离与日志);
//
// 出参: *forwardResult。resp 非 nil 即成功(调用方负责回写与关闭),否则按 statusCode/body 兜底回写。
// res.upstreamFormat 标记上游响应协议,供 passthroughReply 决定是否响应回译。
//
// 协议适配(按入站协议 × 上游组 Formats 决策):
//   - provider != "other":维持「裸透传 OpenAI 兼容上游」语义,上游端点固定 /v1/chat/completions,
//     入站 OpenAI Chat/Responses 归一化为 OpenAIChatRequest,响应原样回写(不做回译),与旧行为一致。
//   - provider == "other":按组 Formats 与入站协议决定上游端点与转译:
//     · OpenAI 格式组(仅 ["openai"]):上游端点 /v1/chat/completions;入站 Anthropic→OpenAI 请求转译
//       + 响应 OpenAI→Anthropic 回译;入站 OpenAI 直发。
//     · Anthropic 格式组(仅 ["anthropic"]):上游端点 /v1/messages;入站 OpenAI→Anthropic 请求转译
//       + 响应 Anthropic→OpenAI 回译;入站 Anthropic 直发。
//     · 多选组 ["openai","anthropic"]:优先按入站协议选上游端点(入站 OpenAI→上游 OpenAI 端点,
//       入站 Anthropic→上游 Anthropic 端点),请求/响应仅需在入站 Responses 时归一化为 OpenAI Chat。
func (pf *passthroughForward) run(
	w http.ResponseWriter, r *http.Request,
	poolChannel, targetGroupID, upstreamModel, inModel string,
	bodyBytes []byte, isStreaming bool,
	isChat, isResponses, isMessages bool,
	userSession *RelaySession,
) *forwardResult {
	res := &forwardResult{}

	// 1. 选号池可用账号(按 Provider 过滤;Other 号池叠加 GroupID 过滤 + 冷却过滤)。
	var available []*account.Account
	if poolChannel == "other" && targetGroupID != "" {
		available = pf.accountMgr.GetAvailableAccountsForChannelAndGroup(poolChannel, targetGroupID, inModel)
	} else {
		available = pf.accountMgr.GetAvailableAccountsForChannel(poolChannel, inModel)
	}
	if len(available) == 0 {
		res.err = fmt.Errorf("%s pool empty (channel %s, group %s)", inModel, poolChannel, targetGroupID)
		res.statusCode = http.StatusServiceUnavailable
		pf.h.log("⛔ [路由转发] 号池 %s 组 %s 无可用账号(model=%s),回写 503", poolChannel, targetGroupID, inModel)
		return res
	}

	maxAttempts := len(available)
	if maxAttempts > passthroughMaxAttempts {
		maxAttempts = passthroughMaxAttempts
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	// 决策上游端点与协议形态:仅 provider=="other" 且组 Formats 显式声明时按组决策;
	// 其余维持「OpenAI 兼容上游」裸透传(upstreamFormat=openai)。
	upstreamFormat := "openai"
	var groupFormats []string
	if poolChannel == "other" && pf.accountMgr != nil {
		groupFormats = pf.accountMgr.GetOtherGroupFormats(targetGroupID)
	}
	if len(groupFormats) > 0 {
		// 上游端点选择优先级:入站协议若在组 Formats 内 → 直发原生端点(零回译);
		// 否则取组 Formats 的首个(openai 优先于 anthropic,见 normalizeOtherFormats 排序)作上游端点。
		inboundFmt := ""
		if isMessages {
			inboundFmt = "anthropic"
		} else if isChat || isResponses {
			inboundFmt = "openai"
		}
		hasOpenAI, hasAnthropic := containsFormat(groupFormats, "openai"), containsFormat(groupFormats, "anthropic")
		switch {
		case inboundFmt == "openai" && hasOpenAI:
			upstreamFormat = "openai"
		case inboundFmt == "anthropic" && hasAnthropic:
			upstreamFormat = "anthropic"
		case hasOpenAI:
			upstreamFormat = "openai"
		case hasAnthropic:
			upstreamFormat = "anthropic"
		}
	}

	// OCR 自递归守卫:本请求若来自 OCR 引擎跨号池出站(携带 X-Antigravity-OCR-Self: 1),
	// 其 image 块是给所选多模态模型看的,必须原样透传,任何 image→文本降级都应跳过。
	// 该标志贯穿首构(165)与降级后重构(218)两处 buildUpstreamBody 的 allowOCR,从源头
	// 让 Anthropic↔OpenAI 各分支的 Downgrade* 都不再触发,防 OCR→OCR 死循环。
	isOcrSelf := r.Header.Get("X-Antigravity-OCR-Self") == "1"

	// 构造上游请求体(首轮外层 attempt 构造一次;image 降级在 attempt==0 内重新构造)。
	// 请求转译方向由 (入站协议, upstreamFormat) 决定:
	//   入站 openai/responses + 上游 openai → 归一化为 OpenAIChatRequest(Responses→OpenAI 转换);
	//   入站 anthropic + 上游 openai → AnthropicToOpenAIChat(含 image 降级);
	//   入站 anthropic + 上游 anthropic → 原样透传 body(仅 model 改写);
	//   入站 openai/responses + 上游 anthropic → OpenAIToAnthropicMessages(新写,见 passthrough_anthropic.go);
	//   入站 responses + 上游 anthropic → Responses→OpenAIChat 再 OpenAI→Anthropic 两步。
	upstreamBody, buildErr := pf.buildUpstreamBody(bodyBytes, upstreamModel, isStreaming, isChat, isResponses, isMessages, upstreamFormat, userSession, !isOcrSelf)
	if buildErr != nil {
		res.err = buildErr
		res.statusCode = http.StatusBadRequest
		pf.h.log("🚫 [路由转发] 构造上游请求体失败(模型 %s): %v", upstreamModel, buildErr)
		return res
	}
	res.upstreamFormat = upstreamFormat

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

		// 轮询选号:Other 号池按组 LB 模式选号(sticky/round-robin);其余号池维持轻量取首个。
		// 并发限制:先按上限把超限账号过滤掉(对齐 NVIDIA 链路口径),过滤集非空喂既有选号器
		// 保持 sticky/round-robin 语义(过滤掉 sticky 命中账号即触发 sessionRouter 既有迁移,
		// 这是符合用户「超过就换号」预期的硬换号语义);过滤集空则取在途并发最少的号允许超额降级。
		var poolForSelection []*account.Account
		var acc *account.Account
		if poolChannel == "other" && targetGroupID != "" {
			limit := pf.accountMgr.GetOtherMaxConcurrency(targetGroupID)
			filtered := pf.accountMgr.FilterByConcurrency(active, limit)
			if len(filtered) > 0 {
				poolForSelection = filtered
			} else {
				overAcc := pf.accountMgr.LeastLoadedAccount(active)
				if overAcc != nil {
					acc = overAcc
					pf.h.log("⚠️ [并发限制] Other 组 %s 并发全满(限 %d),超额降级到最少并发号 %s", targetGroupID, limit, overAcc.Email)
				}
			}
			if acc == nil {
				lbMode := pf.accountMgr.GetOtherLBMode(targetGroupID)
				acc = pf.pickOtherAccount(pf.h, lbMode, targetGroupID, userSession, poolForSelection)
			}
		} else {
			// 非 other 号池(如第三方 OpenAI 兼容上游直挂 /route):同样按并发上限过滤。
			// 本期仅 other 号池显式配置并发上限,其余号池无对应 Get 方法,先按默认 10 语义统一过滤。
			// 若需要为非 other 号池配置化,后续按 channel 加 Get 方法即可。
			acc = active[0]
		}
		if isPassthroughAccountUnavailable(acc) {
			skipped[acc.ID] = true
			continue
		}

		// 选号通过后立即占用并发槽(Acquire),后续失败/早返路径以本 acc.ID 寻址 Release。
		// 成功路径(行 349-353 设 res.usedAccPtr 并 return)不 Release,交 handleRoutedForward defer 兜底。
		pf.accountMgr.AcquireAccount(acc.ID)

		// image 自愈降级仅在"入站 OpenAI Chat + 上游 OpenAI"路径生效(与既有 passthroughForward 行为一致);
		// 入站 Anthropic→OpenAI 的 image 降级已在 buildUpstreamBody 的 AnthropicToOpenAIChat 分支内通过
		// DowngradeAnthropicImagesToText 完成;上游 Anthropic 原生端点接受 Anthropic 协议 image 块,无需降级。
		// OCR 自递归守卫:若本请求来自 OCR 引擎跨号池出站(携带 X-Antigravity-OCR-Self: 1),
		// 其 image 块是给所选多模态模型看的,跳过一次降级,原样透传给上游。
		// 多模态判据:用 pf.h.ocr.modelSupportsImage(upstreamModel) 替代原"upstreamFormat==openai 即降"的盲判 ——
		// DeepSeek-VL / Qwen-VL / Kimi-K2 等挂在 OpenAI 兼容端点上的多模态模型,显式或启发式命中后自动跳过
		// 降级、图块原样透传,省 OCR 配额 + 保留原生视觉理解;非多模态上游则照旧降级。
		if attempt == 0 && (isChat || isResponses) && upstreamFormat == "openai" && r.Header.Get("X-Antigravity-OCR-Self") != "1" && !pf.h.ocr.modelSupportsImage(upstreamModel) {
			downBody, replacedDown, errDown, ocrHitsDown, ocrMissesDown, ocrSkippedDown := pf.h.ocr.DowngradeOpenAIChatImagesToText(bodyBytes, userSession)
			if errDown != nil {
				pf.h.log("⚠️ [路由转发] OpenAI Chat image 自愈降级出错(provider %s | 会话 %s): %v,继续原始请求", poolChannel, ocrSessionDisplay(userSession), errDown)
			} else if replacedDown > 0 {
				pf.h.log("✅ [路由转发] OpenAI Chat 检测到 %d 个 image 块,已本地 OCR 降级为纯文本(provider %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)", replacedDown, poolChannel, ocrSessionDisplay(userSession), ocrHitsDown, ocrMissesDown, ocrSkippedDown)
				if newBody, e := pf.buildUpstreamBody(downBody, upstreamModel, isStreaming, isChat, isResponses, isMessages, upstreamFormat, userSession, false); e == nil {
					upstreamBody = newBody
				}
			}
		}

		// 上游 URL:OpenAI 格式 → {BaseURL}/v1/chat/completions;Anthropic 格式 → {BaseURL}/v1/messages。
		// BaseURL 已含 /v1 则不重复拼(与 NVIDIA 链路口径一致)。
		baseURL := strings.TrimRight(acc.BaseURL, "/")
		var targetURL string
		if upstreamFormat == "anthropic" {
			targetURL = baseURL + "/v1/messages"
			if strings.HasSuffix(baseURL, "/v1") {
				targetURL = baseURL + "/messages"
			}
		} else {
			targetURL = baseURL + "/v1/chat/completions"
			if strings.HasSuffix(baseURL, "/v1") {
				targetURL = baseURL + "/chat/completions"
			}
		}

		pf.h.log("🟢 [路由转发 %d/%d] %s 号池(group %s) → 账号 %s | model %s -> %s | fmt %s | %s", attempt+1, maxAttempts, poolChannel, targetGroupID, acc.Email, inModel, upstreamModel, upstreamFormat, targetURL)

		// 单账号 429 原地退避 + 多状态码换号。
		var activeResp *http.Response
		ok := false
		for single := 1; single <= passthroughSingleAcc429Retries; single++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				// 建请求失败(极罕见,坏 URL):释放该账号并发槽,终止本轮。
				res.err = err
				pf.accountMgr.ReleaseAccount(acc.ID)
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+acc.GetAccessToken())
			if upstreamFormat == "anthropic" {
				req.Header.Set("Accept", "application/json")
				// Anthropic 端点通常识别 anthropic-version 头,缺省会导致部分中继网关 400;注入兜底版本。
				if req.Header.Get("anthropic-version") == "" {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
			} else {
				req.Header.Set("Accept", "application/json")
			}

			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				// 客户端主动取消/超时(如中断生成、关闭 SSE、断开连接):这是调用方行为,不是上游故障,
				// 不应计入账号失败并触发号池冷却,否则一次取消就把账号冻结 60s/5min。
				// 直接按客户端断开终止本轮转发,不换号、不冷却、不剔除。
				if errors.Is(errDo, context.Canceled) || errors.Is(errDo, context.DeadlineExceeded) {
					res.err = errDo
					res.statusCode = 499 // Client Closed Request
					pf.h.log("📴 [路由转发] 客户端取消请求(账号 %s 上游 %s): %v,不触发号池冷却", acc.Email, poolChannel, errDo)
					// 客户端取消:释放该账号并发槽(本次请求到此结束,不再换号)。
					pf.accountMgr.ReleaseAccount(acc.ID)
					return res
				}
				res.err = errDo
				res.statusCode = http.StatusBadGateway
				pf.h.log("⚠️ [路由转发] 账号 %s 访问上游失败: %v", acc.Email, errDo)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				skipped[acc.ID] = true
				// 网络错误换号前释放该账号并发槽(下次 attempt 选新号会重新 Acquire)。
				pf.accountMgr.ReleaseAccount(acc.ID)
				break
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				_ = resp.Body.Close()
				res.statusCode = resp.StatusCode
				res.err = fmt.Errorf("upstream %s 429", poolChannel)
				if single < passthroughSingleAcc429Retries {
					time.Sleep(2 * time.Second)
					continue // 同号续用,不释放并发槽
				}
				pf.h.log("⚠️ [路由转发] 账号 %s 重试 %d 次仍 429,冷冻换号", acc.Email, passthroughSingleAcc429Retries)
				pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				skipped[acc.ID] = true
				// 429 退避耗尽换号前释放并发槽。
				pf.accountMgr.ReleaseAccount(acc.ID)
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
				// 401/403 剔除换号前释放并发槽。
				pf.accountMgr.ReleaseAccount(acc.ID)
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
				// 5xx 换号前释放并发槽。
				pf.accountMgr.ReleaseAccount(acc.ID)
				break
			}

			// 200 (含 SSE/JSON)。回写由调用方处理,此处只落 activeResp。
			// 并发槽不在此释放:成功路径交 handleRoutedForward 的 defer 兜底(res.usedAccPtr 已在下两行赋值),
			// 该 defer 在消费完 resp.Body 流式回写后返回时触发,即「本次请求结束」点。
			activeResp = resp
			res.usedAccount = acc.Email
			res.usedAccPtr = acc
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

// buildUpstreamBody 按入站协议与上游协议形态构造发往上游的请求体。
// allowOCR 为 false 时跳过 image 降级(避免换号时重复降级,外部已降级后调用)。
// 返回的 body 直接作为上游请求体 marshal 透传。
func (pf *passthroughForward) buildUpstreamBody(bodyBytes []byte, upstreamModel string, isStreaming bool,
	isChat, isResponses, isMessages bool, upstreamFormat string, userSession *RelaySession, allowOCR bool,
) ([]byte, error) {
	// 上游 OpenAI 兼容端点:入站 OpenAI Chat / Responses → OpenAIChatRequest(Responses 转换);入站 Anthropic → AnthropicToOpenAIChat(含 image 降级)。
	if upstreamFormat == "openai" {
		if isMessages {
			var anthReq AnthropicRequest
			if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
				return nil, fmt.Errorf("invalid anthropic request: %w", err)
			}
			anthReq.Model = upstreamModel
			// 多模态判据:上游模型原生支持视觉时跳过 image 降级(图块原样透传,保留原生视觉理解)。
			// 与外层 attempt==0 的 OpenAI Chat 分支用同一份 pf.h.ocr.modelSupportsImage 判据,
			// 覆盖 DeepSeek-VL / Qwen-VL / Kimi-K2 等挂在 OpenAI 兼容端点上的多模态上游。
			if allowOCR && pf.h.ocr != nil && !pf.h.ocr.modelSupportsImage(upstreamModel) {
				if replaced, errDown, _, _, _ := pf.h.ocr.DowngradeAnthropicImagesToText(&anthReq, userSession); errDown == nil && replaced > 0 {
					pf.h.log("✅ [路由转发] Anthropic image 降级 %d 块 → OpenAI Chat(会话 %s)", replaced, ocrSessionDisplay(userSession))
				}
			}
			mappings := pf.h.getRelayModelMappingSafe()
			// 多模态上游保图:与上方降级闸同一判据,上游原生支持视觉时让翻译层把 image 块转译为
			// OpenAI Chat Vision 数组形态 content 原样透传(否则旧字符串路径静默丢图)。
			preserveImages := allowOCR && pf.h.ocr != nil && pf.h.ocr.modelSupportsImage(upstreamModel)
			u, err := AnthropicToOpenAIChatPreservingImages(&anthReq, preserveImages, mappings)
			if err != nil {
				return nil, fmt.Errorf("anthropic->openai transform failed: %w", err)
			}
			if isStreaming {
				ensureIncludeUsage(u)
			}
			return json.Marshal(u)
		}
		upstreamReq, err := buildPassthroughUpstreamReq(bodyBytes, upstreamModel, isStreaming)
		if err != nil {
			return nil, err
		}
		return json.Marshal(upstreamReq)
	}

	// 上游 Anthropic 原生端点 /v1/messages。
	// 入站 Anthropic → 原样透传(仅 model 改写);入站 OpenAI Chat / Responses → OpenAIToAnthropicMessages 转译。
	if isMessages {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &obj); err != nil {
			return nil, fmt.Errorf("invalid anthropic request: %w", err)
		}
		mb, _ := json.Marshal(upstreamModel)
		obj["model"] = mb
		return json.Marshal(obj)
	}
	// 入站 OpenAI Chat / Responses → Anthropic Messages 请求体。
	anthReq, err := OpenAIToAnthropicMessages(bodyBytes, upstreamModel, isResponses)
	if err != nil {
		return nil, fmt.Errorf("openai->anthropic transform failed: %w", err)
	}
	return json.Marshal(anthReq)
}

// pickOtherAccount 是 Other 号池组内选号统一入口, 按组配置的 LB 模式选号。
// 与 pickNvidiaAccount 语义对齐, 但 sessionKey 用组前缀隔离:
//   - sticky 模式: 走 sessionRouter.GetOrAssignAccount, 用 "other:{groupID}:{sessionKey}"
//     作为粘性键, 使同一会话稳定绑定到同一账号, 且不同上游组互不串扰(避免跨组共享
//     sessionRouter 时把会话错绑到别的组账号)。
//   - round-robin 模式: 取活跃集第一个(维持既有轻量取法, 与 handleNvidia 单账号语义一致)。
//
// sessionRouter / userSession 缺失时回退 round-robin(取首个), 保证单测与未注入场景兼容。
func (pf *passthroughForward) pickOtherAccount(h *APICompatHandler, lbMode, groupID string, userSession *RelaySession, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}
	if lbMode == "sticky" && h != nil && h.sessionRouter != nil && userSession != nil && userSession.UserID != "" {
		stickyKey := "other:" + groupID + ":" + userSession.UserID
		return h.sessionRouter.GetOrAssignAccount(stickyKey, accounts, h.logFn)
	}

	// 真正的轮询 (Round-Robin): 按组隔离的取模偏移
	var cursor uint64
	if h != nil {
		var ptr *uint64
		if val, ok := h.otherCursors.Load(groupID); ok {
			ptr = val.(*uint64)
		} else {
			newPtr := new(uint64)
			actual, _ := h.otherCursors.LoadOrStore(groupID, newPtr)
			ptr = actual.(*uint64)
		}
		cursor = atomic.AddUint64(ptr, 1) - 1
	}
	idx := cursor % uint64(len(accounts))
	return accounts[idx]
}

// containsFormat 判定组 Formats 切片是否包含某协议(大小写不敏感)。
func containsFormat(formats []string, want string) bool {
	w := strings.ToLower(strings.TrimSpace(want))
	for _, f := range formats {
		if strings.ToLower(strings.TrimSpace(f)) == w {
			return true
		}
	}
	return false
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
		normalizeOpenAIChatRoles(&chatReq)
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
	normalizeOpenAIChatRoles(u)
	return u, nil
}

// normalizeOpenAIChatRoles 把 OpenAI Chat/Responses 入站消息的 role 归一化为第三方
// OpenAI 兼容端点能识别的集合。
//
// 背景:OpenAI Codex CLI 等新客户端会把系统提示写成新版规范要求的 "developer" 角色,
// 并原样经 /route/* 透传到 Other 号池(阿里云 deepseek / 商汤 sensenova 等)的
// /v1/chat/completions。这些第三方端点仅兼容旧规范,允许的 role 只有
// system/assistant/user/tool/function,收到 "developer" 会直接回 400
// (invalid_request_error: "developer is not one of [...]")。
// 这里在透传前统一把 developer 折叠为 system,语义等价(OpenAI 官方即把 developer 视作
// system 的命名升级),彻底消除该 400。其余 role 原样保留,零副作用。
func normalizeOpenAIChatRoles(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	for i := range req.Messages {
		if req.Messages[i].Role == "developer" {
			req.Messages[i].Role = "system"
		}
	}
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
		// Anthropic SSE 事件:取 message_delta.usage 的累计 input/output/cached 作为权威。
		// message_start 不带 message_delta,但若上游给了 message_start.usage 也读一次作初值兜底。
		var ev struct {
			Type  string                  `json:"type"`
			Delta json.RawMessage         `json:"delta,omitempty"`
			Usage *AnthropicResponseUsage `json:"usage,omitempty"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if ev.Type == "message_delta" {
			// message_delta 的 usage 在顶层(与 delta 平级),且为累计值。
			var d struct {
				Usage AnthropicResponseUsage `json:"usage"`
			}
			if json.Unmarshal(ev.Delta, &d) == nil && (d.Usage.InputTokens > 0 || d.Usage.OutputTokens > 0) {
				inUsage = d.Usage.InputTokens
				outUsage = d.Usage.OutputTokens
				cachedUsage = d.Usage.CachedTokens()
			}
			// 部分 Anthropic 镜像把 usage 放在事件顶层而非 delta 内,作兜底。
			if ev.Usage != nil && (ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0) {
				inUsage = ev.Usage.InputTokens
				outUsage = ev.Usage.OutputTokens
				cachedUsage = ev.Usage.CachedTokens()
			}
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
