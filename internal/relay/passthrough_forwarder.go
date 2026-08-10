package relay

import (
	"bytes"
	"context"
	"errors"
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
// 仅对非 Other 号池生效:Other 号池(provider=="other")不启用请求冷却,
// 上游 429/5xx/401/403/网络错仅触发同请求内换号(skipped),不写账号冷静期。
// Other 多为 free 档自定义上游,限流频繁,冷却 60s/5min 反而误冻账号导致连续 503。
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
//   - 响应 OpenAI→Anthropic 回译;入站 OpenAI 直发。
//     · Anthropic 格式组(仅 ["anthropic"]):上游端点 /v1/messages;入站 OpenAI→Anthropic 请求转译
//   - 响应 Anthropic→OpenAI 回译;入站 Anthropic 直发。
//     · 多选组 ["openai","anthropic"]:优先按入站协议选上游端点(入站 OpenAI→上游 OpenAI 端点,
//     入站 Anthropic→上游 Anthropic 端点),请求/响应仅需在入站 Responses 时归一化为 OpenAI Chat。
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
			// 本地图片路径自愈(L2.5 预处理):先扫 text 块裸本地图片路径读图 OCR 注入,再走结构化
			// image 块降级。命中时 bodyBytes 替换为注入后的新 body,**并立即用新 body 重建 upstreamBody**
			// (因为上方 L179 已用原始 bodyBytes 构造过 upstreamBody,若仅改 bodyBytes 而不重建 upstreamBody,
			// 下游仍会发原样 upstreamBody,本地路径注入无效)。静默 miss 不报错。
			if nb, enriched := pf.h.ocr.EnrichLocalImagePathsInOpenAIChat(bodyBytes, userSession); enriched > 0 {
				bodyBytes = nb
				if newUpstream, be := pf.buildUpstreamBody(bodyBytes, upstreamModel, isStreaming, isChat, isResponses, isMessages, upstreamFormat, userSession, false); be == nil {
					upstreamBody = newUpstream
				}
				pf.h.log("✅ [路由转发] OpenAI Chat 检测到 %d 个本地图片路径,已读图 OCR 注入 text 块(provider %s | 会话 %s)", enriched, poolChannel, ocrSessionDisplay(userSession))
			}
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
				// Other 号池不启用请求冷却:网络错仅换号,不写冷静(见 passthroughCooldownShort 注释)。
				if poolChannel != "other" {
					pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				}
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
				// Other 号池不启用请求冷却:429 退避耗尽仅换号,不写冷静。
				if poolChannel != "other" {
					pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				}
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
				// Other 号池不启用请求冷却:401/403 仅换号,不写 5min 冷静。
				if poolChannel != "other" {
					pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownLongMs, poolChannel, inModel)
				}
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
				// Other 号池不启用请求冷却:5xx 仅换号,不写冷静。
				if poolChannel != "other" {
					pf.accountMgr.SetAccountCooldownForChannel(acc.ID, time.Now().UnixNano()/1e6+passthroughCooldownShortMs, poolChannel, inModel)
				}
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
