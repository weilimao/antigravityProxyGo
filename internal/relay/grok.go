package relay

// grok.go 实现 /grok/*(及别名 /xai/*)路由的主链路, 并承接 /route/* 命中 "grok" Provider 的派发。
//
// 链路形态与 handleNvidia(/nvidia/*)同构, 关键差异:
//   - 上游固定为 xAI OpenAI Chat 兼容端点(POST {BaseURL}/v1/chat/completions), 走顶层 reasoning_effort
//     注入思考(取值 none/low/medium/high), 由 grok_thinking.go 的 grokApplyThinkingToChat 在翻译链
//     构造出 OpenAIChatRequest 之后【显式覆盖】reasoning_effort 字段 —— 不走 NIM 的 chat_template_kwargs。
//     核心语义(用户需求): 客户端的关闭信号必须翻译为上游字段值 "none" 发出去, 而非省略
//     (xAI 官方默认 low 即常驻思考, 省略≠关闭), 这正是 Grok 路径与 nvidia/other 池的根本不同。
//   - 不接 NIM 流内 ResourceExhausted 就地压缩重试(tryCompressNvidiaRequest)与蓄流回放重试
//     (pullAnthropicStreamWithRetry): xAI 官方端点稳定, 直连 + 429 退避 + 换号重试足够;
//     流式回译走「边转译边写」的简单直通链路(与 router_entry.go replyOpenAIToAnthropic 同款, 非 nvidia 的重试态机)。
//   - 选号用 pickGrokAccount(sticky + grokCursor 取模轮询), 不接 nvidiaStats 1 分钟计数盘。
//
// 翻译链复用既有成果(零新增转译代码):
//   - 入站 Anthropic → AnthropicToOpenAIChatPreservingImages(保留多模态上游的 image 块);
//   - 入站 Responses → ResponsesToOpenAIChat(codex /v1/responses);
//   - 入站 OpenAI   → 直接 Unmarshal(含 OCR 降级 image_url 块);
//   - 响应回译: OpenAIChat 上游响应按入站协议回写 —— anthropic 用 OpenAIChatToAnthropic /
//     OpenAIChatSSEToAnthropicSSE, responses 用 OpenAIChatToResponses / OpenAIChatSSEToResponsesSSE,
//     openai_chat 用 proxyNvidiaOpenAIPassthrough(通用 OpenAI Chat 透传 + usage 嗅探, 函数本身与
//     NVIDIA 无耦合, 名称历史沿用, 复用零重复)。
//
// 统计落库经 recordGrokUsage(grok_usage.go), Family="grok", PoolKey="grok", relay 维度模型名带 "grok/" 前缀。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// grokMaxAttempts 是单请求最多换号次数(含首号), 与 handleNvidia 的 maxAttempts<=5 上限语义一致,
// 避免单请求拖垮整池。Grok 池账号通常少, 若可用号 <5 则按池实际数量轮一遍。
const grokMaxAttemptsCap = 5

// grokSingleAcc429Retries 是单账号应对 429/403 的原地重试上限(首次 + 1 次重试 = 2)。
// 与 handleNvidia/passthrough 的「5 次退避」不同: 用户语义要求「等几秒再试一次, 仍报错就冷却一天换号」,
// 故此值为 2 —— 首次失败后等 grokQuotaRetryWaitMs(5s) 原地再打 1 次, 第二次仍失败即挂可配置长冷却换号。
const grokSingleAcc429Retries = 2

// grokQuotaRetryWaitMs 是单账号遇 429/403 首失败后, 原地再次重试前的等待时长(5 秒)。
// 用户语义「等五秒再试一次」, 仅此一次等待; 仍失败即挂号池可配置冷却(默认 24h)换号。
const grokQuotaRetryWaitMs = 5 * 1000

// grokCooldownMs 是网络错误时该号的冷却时长(60s), 与 passthroughCooldownShortMs 一致。
const grokCooldownShortMs = 60 * 1000

// handleGrok 处理 /grok/* 与 /xai/* 请求(并承接 /route/* 命中 "grok" Provider 的派发)。
func (h *APICompatHandler) handleGrok(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")
	// GET 连通性探测与模型列表: 客户端把 BaseURL 设为 http://[host]:18444/grok 或 /xai,
	// 发起 GET /grok, GET /grok/v1, GET /grok/v1/models 探测时统一走模型列表(与 handleNvidia 同构)。
	if r.Method == http.MethodGet || path == "/grok/v1/models" || path == "/xai/v1/models" || path == "/grok/models" || path == "/xai/models" || strings.HasSuffix(path, "/models") {
		h.handleGrokModels(w, r, userSession)
		return
	}

	bodyBytes, err := readBodyWithTimeout(r, nvidiaInboundReadTimeout)
	if err != nil {
		if errors.Is(err, ErrBodyReadTimeout) {
			// 入站 body 读取超时: 客户端只发 header 不发 body, 或入站链路半死。
			// 回写 408 让 Claude Code 等 Anthropic 客户端识别后自动换连接重试(与 handleNvidia 同口径)。
			kind := inboundKindOfPath(path)
			h.log("⏱️ [Grok 中继] 入站 body 读取超时(%s), 路径 %s, 判定客户端未发完整请求体/链路半死, 回写 408 %s", nvidiaInboundReadTimeout, path, kind)
			writeNvidiaInboundTimeout(w, kind)
			return
		}
		h.log("❌ [Grok 中继] 入站 body 读取失败(路径 %s): %v", path, err)
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	reqID := fmt.Sprintf("grok_%d", time.Now().UnixNano())
	// start: 入站请求接入时刻, 作为「请求日志」DurationMs 的端到端耗时基准, 经 writeGrokResponse
	// → recordGrokUsage 透传到落点4(与 gemini/claude/nvidia 直连链路口径一致)。
	start := time.Now()
	// firstByteRec: 全程共享的 TTFT 打点器, 以入站接入时刻 start 为基准。
	firstByteRec := stats.NewFirstByteRecorder(start)
	if h.settingsMgr != nil {
		enabled := h.settingsMgr.GetEnableDebuggerMode()
		logPath := h.settingsMgr.GetResolvedDebuggerLogPath()
		GetGlobalDebugger().Configure(enabled, logPath)
	}
	GetGlobalDebugger().LogClientRequest(reqID, r.Method, r.URL.Path, r.Header, bodyBytes)

	// 会话级隔离键注入(与 handleNvidia 同款口径, 见 relay.session_key.ensureSessionKey):
	// 客户端原生会话头优先(Claude/Codex UUID)→ ExtractSessionKey 兜底, 供 OCR 缓存等按会话隔离。
	h.ensureSessionKey(userSession, r, bodyBytes)

	// 入站协议判定: 按路径三选一(与 handleNvidia 同构)。
	inboundAnthropic := strings.HasSuffix(path, "/v1/messages")
	inboundOpenAI := strings.HasSuffix(path, "/v1/chat/completions")
	inboundResponses := strings.HasSuffix(path, "/v1/responses")
	// count_tokens: Anthropic 可选端点, 纯本地字符级粗估回 200, 不请求上游(复用 handleNvidiaCountTokens,
	// 该函数本身与 NVIDIA 无耦合, 仅做 AnthropicRequest 估算, 与 grok 入站 Anthropic 同构)。
	if strings.HasSuffix(path, "/v1/messages/count_tokens") {
		h.handleNvidiaCountTokens(w, bodyBytes)
		return
	}
	if !inboundAnthropic && !inboundOpenAI && !inboundResponses {
		h.log("🚫 [Grok 中继] 不支持的端点 %s (支持 /grok/v1/messages|/xai/v1/messages, /grok/v1/chat/completions|/xai/v1/chat/completions, /grok/v1/responses|/xai/v1/responses), 回写 404", path)
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "unsupported grok endpoint: use /grok/v1/messages, /grok/v1/chat/completions or /grok/v1/responses (or the /xai alias)",
		})
		return
	}

	// 解析入站请求以确定模型与 stream(三协议 model/stream 字段同构, 与 handleNvidia 同口径)。
	var inModel string
	var isStreaming bool
	if inboundAnthropic {
		var req AnthropicRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			h.log("🚫 [Grok 中继] Anthropic 入站请求体解析失败(路径 %s): %v, 回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else if inboundResponses {
		req, err := ParseUnifiedOpenAIRequest(bodyBytes)
		if err != nil {
			h.log("🚫 [Grok 中继] Responses 入站请求体解析失败(路径 %s): %v, 回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid responses request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else {
		var req OpenAIChatRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			h.log("🚫 [Grok 中继] OpenAI Chat 入站请求体解析失败(路径 %s): %v, 回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	}

	// API Key 模型授权校验: 在 Grok 配额校验前拦截未授权模型(精确匹配 inModel, 与 handleNvidia 同口径)。
	// 仅一级入口(直连 /grok/*、/xai/*)执行; route 链路(/route/* 命中 grok 复用本 handler)时
	// body model 已被改写为无前缀上游名, 改在前置 handleRoutedForward 用原始 inModel 校验, 这里跳过。
	if h.authMgr != nil && h.authMgr.userMgr != nil && !routedRoutePrefixMatch(r.URL.Path) {
		if err := h.authMgr.userMgr.IsModelAuthorizedForAPIKey(userSession.UserID, userSession.APIKeyID, inModel); err != nil {
			h.log("🚫 [Grok 中继] API Key 模型授权校验未通过: %v (User: %s)", err, userSession.UserKey)
			writeModelNotAuthorized(w, inModel)
			return
		}
	}

	// Grok family 配额预扣额校验(独立于 gemini/claude, 与 handleNvidia 的 nvidiaQuotaCheck 同构):
	// 走 UserQuotas.Grok 的 hourly/daily 滚动窗口 + "grok/" 前缀 LIKE 命中族用量, 超额回写 429。
	// 未配置任何限额(EnableHourly==false && EnableDaily==false)时 grokQuotaCheck 直接放行(nil), 零影响。
	if h.authMgr != nil && h.authMgr.userMgr != nil {
		user := h.authMgr.userMgr.GetUserByID(userSession.UserID)
		if user != nil {
			if err := grokQuotaCheck(userSession.UserID, user.Quotas.Grok); err != nil {
				h.log("🚦 [Grok 中继] 用户 %s Grok 配额校验未通过: %v, 回写 429 quota_exceeded", userSession.UserKey, err)
				writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
					"error": map[string]interface{}{
						"type":    "quota_exceeded",
						"message": err.Error(),
					},
				})
				return
			}
		}
	}

	// 选号池: 只要携带 /grok|/xai 前缀(或 /route 命中 grok), 全量使用 Grok 号池做负载均衡与轮询换号。
	poolChannel := grokChannel
	available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, inModel)

	if len(available) == 0 {
		h.log("⛔ [Grok 中继] Grok 号池无可用账号(channel=grok, model=%s), 回写 503 grok_pool_empty", inModel)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "grok_pool_empty",
				"message": "no available Grok account in pool (channel grok)",
			},
		})
		return
	}

	skippedAccounts := make(map[string]bool)
	maxAttempts := len(available)
	if maxAttempts > grokMaxAttemptsCap {
		maxAttempts = grokMaxAttemptsCap
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	// sticky 选号键:优先已注入的 userSession.SessionKey(按客户端会话粘性,使同一 Codex 用户
	// 的不同会话散到不同号),空则回退 UserID(按用户粘性,脚本/SDK 直调旧行为)。
	// 注入在入口 h.ensureSessionKey 完成(客户端头优先 → ExtractSessionKey 兜底)。
	sessionKey := h.stickyKeyOf(userSession)
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetGrokLBMode()
	}

	// 全局思考总闸(与 nvidia/other 链路同源): IsEnableThinkingMode==false 表示用户全局关闭思考。
	// Grok 的「全局关」语义需真正关掉上游推理(发 reasoning_effort="none"), 由 grokApplyThinkingToChat 落地。
	globalThinkingOn := IsEnableThinkingMode()

	var lastErr error
	var lastErrBody []byte
	var lastErrCode int

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 取消守卫(对齐 handleNvidia): 客户端主动取消时 r.Context() 即被撤销, 必须在此立即终止换号
		// 循环, 否则后续每个号的上游请求都会被同一已取消的 context 砍掉, 连续误冷冻整个号池。
		select {
		case <-r.Context().Done():
			h.log("⏹️ [Grok 中继] 客户端已取消连接(会话 %s), 终止换号重试。", ocrSessionDisplay(userSession))
			return
		default:
		}

		var activeAvailable []*account.Account
		for _, a := range available {
			if !skippedAccounts[a.ID] {
				activeAvailable = append(activeAvailable, a)
			}
		}
		if len(activeAvailable) == 0 {
			if lastErr == nil {
				lastErr = fmt.Errorf("all grok accounts in pool failed")
			}
			break
		}

		var poolAccount *account.Account
		// 并发限制: 先按上限过滤超限账号(与 handleNvidia/passthrough 同口径)。
		// 过滤集非空喂 pickGrokAccount(保持 sticky/round-robin 语义); 过滤集空则取在途并发最少的号
		// 允许超额降级(对齐用户「绝不硬拒」预期)。
		limit := h.accountMgr.GetGrokMaxConcurrency()
		filtered := h.accountMgr.FilterByConcurrency(activeAvailable, limit)
		if len(filtered) > 0 {
			poolAccount = h.pickGrokAccount(lbMode, sessionKey, filtered)
		} else {
			overAcc := h.accountMgr.LeastLoadedAccount(activeAvailable)
			if overAcc != nil {
				poolAccount = overAcc
				h.log("⚠️ [并发限制] Grok 池并发全满(限 %d), 超额降级到最少并发号 %s", limit, overAcc.Email)
			}
		}
		if poolAccount == nil {
			lastErr = fmt.Errorf("no available grok account assigned from pool")
			break
		}

		// 选号通过后立即占用并发槽。后续所有失败/早返 break/return 以本 poolAccount.ID 寻址 Release;
		// 成功路径在请求结束前 Release。严格按退出路径手工配对(与 handleNvidia 同款, 不依赖 defer)。
		h.accountMgr.AcquireAccount(poolAccount.ID)

		// 模型映射(账号级档位, 与 ResolveNvidiaModel 同构): 剥离 [1M] 后缀, 命中档位取账号字段,
		// 客户端显式具名上游模型(含 /)优先透传; 客户端传了模型名时优先透传(不被账号 DefaultModel 压制),
		// 仅客户端未传模型名时才回退 DefaultModel。客户端传什么模型就路由到中继模型映射配置的模型。
		upstreamModel := account.ResolveGrokModel(inModel, poolAccount)

		// 根据入站协议构造发往上游的 OpenAI Chat 请求体, 并在翻译【之后】用 grokApplyThinkingToChat
		// 显式覆盖 reasoning_effort(Grok 路径的核心差异: 显式传 none 表达关闭, 绝不归一空串省略)。
		// OCR 自递归守卫: 本请求若来自 OCR 引擎跨号池出站(打 18444 /route 命中本池), 携带
		// X-Antigravity-OCR-Self: 1, image 块原样透传不得再触发降级(与 handleNvidia 同口径)。
		ocrSelf := r.Header.Get("X-Antigravity-OCR-Self") == "1"
		var upstreamReq *OpenAIChatRequest
		if inboundAnthropic {
			var anthReq AnthropicRequest
			if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
				h.log("🚫 [Grok 中继] 选号后 Anthropic 请求体二次解析失败(账号 %s): %v, 回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
				return
			}
			anthReq.Model = upstreamModel
			anthReq.UserAgent = r.Header.Get("User-Agent")

			// 识别客户端思考意图(在翻译前, 基于 Anthropic thinking 字段 / 全局总闸)。
			thinkMode, thinkEffort := grokResolveAnthropicThinking(&anthReq, globalThinkingOn)

			// image 自愈降级: 仅当上游模型不原生支持多模态时把入站 Anthropic image 块先用本地 Gemini
			// OCR 降级为纯文本。判据由 h.ocr.modelSupportsImage 统一承载(配置优先 → 启发式模型名前缀白名单)。
			// Grok 默认不在白名单 → 走降级(与 nvidia 默认口径一致); 用户可在 RelayModelMapping 显式声明
			// Grok 多模态模型的 Multimodal=true 跳过降级、保留原生视觉理解。
			if !ocrSelf && !h.ocr.modelSupportsImage(upstreamModel) {
				replaced, errDown, ocrHits, ocrMisses, ocrSkipped := h.ocr.DowngradeAnthropicImagesToText(&anthReq, userSession)
				if errDown != nil {
					h.log("⚠️ [Grok 中继] image 自愈降级出错(账号 %s | 会话 %s): %v, 继续原始请求", poolAccount.Email, ocrSessionDisplay(userSession), errDown)
				} else if replaced > 0 {
					h.log("✅ [Grok 中继] 检测到 %d 个 image 块, 已本地 OCR 降级为纯文本(账号 %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)", replaced, poolAccount.Email, ocrSessionDisplay(userSession), ocrHits, ocrMisses, ocrSkipped)
				}
			}

			mappings := h.getRelayModelMappingSafe()
			// 多模态上游保图: 上游模型原生支持视觉时, 让翻译层把 image 块转译为 OpenAI Chat Vision
			// 数组形态 content 原样透传, 否则翻译层旧默认分支会静默丢弃图块(与 handleNvidia 同口径)。
			preserveImages := !ocrSelf && h.ocr.modelSupportsImage(upstreamModel)
			u, err := AnthropicToOpenAIChatPreservingImagesForProvider(&anthReq, preserveImages, "grok", mappings)
			if err != nil {
				h.log("🚫 [Grok 中继] Anthropic→OpenAI 转换失败(账号 %s): %v, 回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "anthropic->openai transform failed: " + err.Error()})
				return
			}
			// 翻译链已构造 OpenAIChatRequest(可能含 NIM 残留的 chat_template_kwargs=None), 在此显式覆盖
			// reasoning_effort 为 Grok 三态语义值(off→"none", on→档, unspecified→""省略) + 清空 ChatTemplateKwargs。
			grokApplyThinkingToChat(u, thinkMode, thinkEffort)
			if u.Stream && (u.StreamOptions == nil || !u.StreamOptions.IncludeUsage) {
				u.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
			upstreamReq = u
		} else if inboundResponses {
			// Responses(含 codex /v1/responses) → 统一解析 → OpenAIChatRequest。
			u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
			if err != nil {
				h.log("🚫 [Grok 中继] Responses→OpenAI 转换失败(账号 %s): %v, 回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "responses->openai transform failed: " + err.Error()})
				return
			}
			// Responses 入站思考等级透传: 从原始 body 提 reasoning_effort(顶层 Codex 形态)或
			// reasoning.effort(OpenRouter 形态), 经 grokResolveOpenAIThinking 三态分类后注入(Grok 显式传 none 语义)。
			thinkMode, thinkEffort := grokResolveOpenAIThinking(bodyBytes, globalThinkingOn)
			grokApplyThinkingToChat(u, thinkMode, thinkEffort)
			if u.Stream && (u.StreamOptions == nil || !u.StreamOptions.IncludeUsage) {
				u.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
			upstreamReq = u
		} else {
			// OpenAI Chat 入站(含 Vision 数组形态 content): 先降级 image_url 块为纯文本, 再 Unmarshal
			// (与 handleNvidia 同口径: 避免非多模态上游触发 400 / 内容丢失; 多模态上游跳过降级保图)。
			if !ocrSelf && !h.ocr.modelSupportsImage(upstreamModel) {
				downBody, replacedDown, errDown, ocrHitsDown, ocrMissesDown, ocrSkippedDown := h.ocr.DowngradeOpenAIChatImagesToText(bodyBytes, userSession)
				if errDown != nil {
					h.log("⚠️ [Grok 中继] OpenAI Chat image 自愈降级出错(账号 %s | 会话 %s): %v, 继续原始请求", poolAccount.Email, ocrSessionDisplay(userSession), errDown)
				} else if replacedDown > 0 {
					h.log("✅ [Grok 中继] OpenAI Chat 检测到 %d 个 image 块, 已本地 OCR 降级为纯文本(账号 %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)", replacedDown, poolAccount.Email, ocrSessionDisplay(userSession), ocrHitsDown, ocrMissesDown, ocrSkippedDown)
					bodyBytes = downBody
				}
			}
			var chatReq OpenAIChatRequest
			if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
				h.log("🚫 [Grok 中继] 选号后 OpenAI Chat 请求体二次解析失败(账号 %s): %v, 回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
				return
			}
			chatReq.Model = upstreamModel
			// OpenAI Chat 入站思考等级透传(与 Responses 分支同款口径, Grok 显式传 none 语义)。
			thinkMode, thinkEffort := grokResolveOpenAIThinking(bodyBytes, globalThinkingOn)
			grokApplyThinkingToChat(&chatReq, thinkMode, thinkEffort)
			if chatReq.Stream && (chatReq.StreamOptions == nil || !chatReq.StreamOptions.IncludeUsage) {
				chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
			upstreamReq = &chatReq
		}

		// 构造上游 URL: {BaseURL}/v1/chat/completions(若 base_url 已含 /v1 后缀, 不再重复拼接, 与 handleNvidia 同口径)。
		// 启用 Cloudflare Worker 代理出口时,把 baseURL 改写为 Worker URL,
		// 真正上游经 X-Target-Upstream 头透传给 Worker(与 NVIDIA 链路同口径)。
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		workerProxyActive := false
		if h.settingsMgr != nil && h.settingsMgr.IsGrokWorkerProxyEnabled() {
			if w := strings.TrimRight(h.settingsMgr.GetGrokWorkerProxyURL(), "/"); w != "" {
				baseURL = w
				workerProxyActive = true
			}
		}
		targetURL := BuildOpenAIChatURL(baseURL)

		upstreamBody, err := json.Marshal(upstreamReq)
		if err != nil {
			lastErr = err
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			break
		}

		proxyTag := ""
		if workerProxyActive {
			proxyTag = " [Worker 代理出口]"
		}
		h.log("🟢 [Grok 中继 %d/%d]%s 用户 %s 分配账号 %s | 模型 %s -> %s | 思考 reasoning_effort=%q | 会话 %s | %s", attempt+1, maxAttempts, proxyTag, userSession.UserID, poolAccount.Email, inModel, upstreamModel, upstreamReq.ReasoningEffort, ocrSessionDisplay(userSession), targetURL)

		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}

		// 单账号针对 429/403 尝试 grokSingleAcc429Retries 次(首次 + 1 次重试):
		// 首失败等 5s 再打 1 次, 第二次仍失败即挂号池可配置长冷却(默认 24h)换号(用户语义)。
		var activeResp *http.Response
		accountSuccess := false
		for singleAttempt := 1; singleAttempt <= grokSingleAcc429Retries; singleAttempt++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				lastErr = err
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}
			req.Header.Set("Content-Type", "application/json")
			// Grok 上游鉴权: Authorization: Bearer <APIKey>(APIKey 复用 AccessToken 字段, 与 NVIDIA/Other 同源)。
			req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
			req.Header.Set("Accept", "application/json")
			// 账号专属出口伪装 IP:经 Worker 代理出口时通过 X-Egress-IP 头透传(与 NVIDIA 链路口径一致)。
			if strings.TrimSpace(poolAccount.EgressIP) != "" {
				req.Header.Set("X-Egress-IP", strings.TrimSpace(poolAccount.EgressIP))
			}
			// Worker 通用代理出口:注入 X-Target-Upstream 头让 Worker 知道真正上游地址。
			if workerProxyActive {
				req.Header.Set("X-Target-Upstream", strings.TrimRight(poolAccount.BaseURL, "/"))
			}
			// Grok CLI 身份头注入(仅对 cli-chat-proxy.grok.com 上游生效):对齐 CLIProxyAPI2
			// xai_executor.go:1141-1145, 规避上游 426 版本闸门("Grok CLI version (none) outdated.
			// Please update to version 0.1.202 or later")。版本号取号池全局配置 GetGrokCliVersion(默认 1.0.0);
			// 不经 chat-proxy 的号(api.x.ai/v1 / 自建反代)applyGrokCLIHeaders 内部 host 判定不注入。
			applyGrokCLIHeaders(req, poolAccount.BaseURL, h.accountMgr.GetGrokCliVersion())

			if singleAttempt > 1 {
				h.log("🔄 [Grok 中继 429/403 重试 %d/%d] 账号 %s 首次失败, 等待 5 秒后原地重试...", singleAttempt, grokSingleAcc429Retries, poolAccount.Email)
			}

			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				// 客户端主动取消特判(与 handleNvidia 同口径): context.Canceled 时该号健康, 不冷冻不换号,
				// 直接整体退出(换下一个号仍会被同一已取消的 context 砍掉, 把整池挨个"砍头"刷屏)。
				if errors.Is(errDo, context.Canceled) || errors.Is(errDo, context.DeadlineExceeded) {
					h.log("⏹️ [Grok 中继] 账号 %s 上游请求被客户端取消(ctx err=%v), 终止换号重试(不冷冻该号)。", poolAccount.Email, errDo)
					lastErr = errDo
					h.accountMgr.ReleaseAccount(poolAccount.ID)
					return
				}
				h.log("⚠️ [Grok 中继] 账号 %s 访问上游失败: %v", poolAccount.Email, errDo)
				skippedAccounts[poolAccount.ID] = true
				lastErr = errDo
				// 网络错误: 短期冷静该号 60s, 换号重试(与 handleNvidia 同口径)。
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+int64(grokCooldownShortMs), grokChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 429 限流: 首次等 5s 原地重试 1 次, 重试仍失败即挂号池可配置长冷却(默认 24h)换号(用户语义)。
			if resp.StatusCode == http.StatusTooManyRequests {
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("grok upstream status 429")
				if singleAttempt < grokSingleAcc429Retries {
					time.Sleep(grokQuotaRetryWaitMs * time.Millisecond)
					continue
				}
				cooldownHours := h.accountMgr.GetGrokQuotaCooldownHours()
				cooldownUntilMs := time.Now().UnixNano()/1e6 + int64(cooldownHours)*3600*1000
				h.log("⚠️ [Grok 中继] 账号 %s 等待 5s 重试 %d 次仍返回 429, 视为额度超限, 冷冻该账号 %d 小时并换号...", poolAccount.Email, grokSingleAcc429Retries, cooldownHours)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, grokChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 401/403: 首次等 5s 原地重试 1 次, 重试仍失败即挂号池可配置长冷却(默认 24h)换号。
			// (401 坏/过期 Key 当天不会自愈, 一天冷冻避免刷屏; 403 鉴权/配额耗尽同口径, 与用户语义一致。)
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("grok upstream status %d", resp.StatusCode)
				if singleAttempt < grokSingleAcc429Retries {
					h.log("🔄 [Grok 中继] 账号 %s 上游返回 %d, 等待 5 秒后原地重试...", poolAccount.Email, resp.StatusCode)
					time.Sleep(grokQuotaRetryWaitMs * time.Millisecond)
					continue
				}
				cooldownHours := h.accountMgr.GetGrokQuotaCooldownHours()
				cooldownUntilMs := time.Now().UnixNano()/1e6 + int64(cooldownHours)*3600*1000
				h.log("⚠️ [Grok 中继] 账号 %s 等待 5s 重试仍返回 %d, 视为鉴权/配额超限, 冷冻该账号 %d 小时并换号...", poolAccount.Email, resp.StatusCode, cooldownHours)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, cooldownUntilMs, grokChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 5xx: 服务端错误, 换号重试(短期冷却, 与 handleNvidia 同口径)。
			if resp.StatusCode >= 500 {
				h.log("⚠️ [Grok 中继] 账号 %s 上游 5xx(%d), 换号重试...", poolAccount.Email, resp.StatusCode)
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("grok upstream server error %d", resp.StatusCode)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 成功 HTTP 200: 上游响应头已到达 —— 在此刻打点 TTFT(幂等 sync.Once), 早于流式回译写出,
			// TTFT 如实反映上游响应头到达时刻(与 handleNvidia 同口径)。
			firstByteRec.MarkFirstByte()
			activeResp = resp
			accountSuccess = true
			break
		}

		if accountSuccess && activeResp != nil {
			inboundKind := "openai_chat"
			if inboundAnthropic {
				inboundKind = "anthropic"
			} else if inboundResponses {
				inboundKind = "responses"
			}
			// 入站 input_tokens 估算: 仅 anthropic 流式分支用此值填 message_start.usage.input_tokens,
			// 让客户端(Claude Code spinner)流首即显示 ↑(非 anthropic/非流式分支传 0 即可, writeGrokResponse
			// 仅在 anthropic+stream 路径透传它, 其余路径不读)。与 handleNvidia 同口径。
			var inboundInputTokens int
			if inboundAnthropic {
				inboundInputTokens = estimateInputTokensFromBody(bodyBytes)
			}
			// 把请求交 writeGrokResponse 按入站协议回译并回写, 内部调 recordGrokUsage 落库五落点。
			// inboundBody 透传入站原始请求体(bodyBytes), 供 grokLogCtx.ReqBody 落库(前端详情弹窗展示「入站时」请求体)。
			// resolvedEffort 为命中上游思考等级(grokApplyThinkingToChat 落地的 upstreamReq.ReasoningEffort:
			// off→"none"、on→档、unspecified→""), 透传给 writeGrokResponse 装配 logCtx.ReasoningEffort,
			// 供前端「模型」列追加 (档) 后缀展示。
			h.writeGrokResponse(w, r, activeResp, inboundKind, isStreaming, upstreamModel, userSession, poolAccount, bodyBytes, inboundInputTokens, start, firstByteRec, upstreamReq.ReasoningEffort)
			// 响应流结束后释放并发槽(writeGrokResponse 返回即流结束, 与 handleNvidia 成功路径同口径)。
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			return
		}
	}

	// 重试用尽: 回写最后一次上游错误状态码与错误体(若有), 否则 502 兜底(与 handleNvidia 同口径)。
	if lastErrBody != nil && lastErrCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastErrCode)
		_, _ = w.Write(lastErrBody)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "grok pool exhausted: " + errStr(lastErr)})
}

// handleGrokModels 处理 /grok/v1/models 或 /grok/models(及 /xai 别名)请求:
// 从 Grok 号池选取可用账号, 剥离 /grok|/xai 前缀后向远端 <BaseURL>/v1/models 发起 GET 请求并透传响应。
// 与 handleNvidiaModels 同构, 但用 Grok 兜底模型清单与 GetEnabledGrokAccounts/getGrokContextWindow。
func (h *APICompatHandler) handleGrokModels(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	// 检测客户端是否为 Anthropic 协议(如 Cherry Studio Messages 模式或 Claude Code)。
	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.HasPrefix(r.Header.Get("x-api-key"), "sk-ant-") ||
		strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "anthropic")

	// Grok 模型清单透传不接「全局专属清单过滤」(NVIDIA 有 GetNvidiaPreferredModels 白名单收窄客户端可见模型,
	// Grok 池模型数量少且用户自定义度高, 直接全量透传上游 /v1/models 结果, 见方案)。
	var available []*account.Account
	if h.accountMgr != nil {
		available = h.accountMgr.GetEnabledGrokAccounts()
	}

	// 查询函数: 按上游模型 id 取声明的上下文窗口(映射条目 MaxInputTokens; 未配置回退 grokContextWindow)。
	// settingsMgr==nil(测试构造)时返回 nil, format 层不附加字段(与 handleNvidiaModels 同口径)。
	var windowLookup func(string) int64
	if h.settingsMgr != nil {
		windowLookup = h.settingsMgr.GetMaxInputTokensByModel(nil, grokContextWindow)
	}

	if len(available) == 0 {
		h.log("⚠️ [Grok 模型列表透传] 号池中无可用 Grok 账号, 返回默认模型列表")
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}

	sessionKey := ""
	if userSession != nil {
		sessionKey = userSession.UserID
	}
	lbMode := "round-robin"
	if h.accountMgr != nil {
		lbMode = h.accountMgr.GetGrokLBMode()
	}
	var poolAccount *account.Account
	poolAccount = h.pickGrokAccount(lbMode, sessionKey, available)
	if poolAccount == nil {
		poolAccount = available[0]
	}

	// 构造发往上游的 URL: 剥离 /grok|/xai 本地路由前缀, 强匹配上游 /v1/models。
	// 启用 Cloudflare Worker 代理出口时改写 baseURL,真正上游经 X-Target-Upstream 透传(同对话链路)。
	baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
	workerProxyActive := false
	if h.settingsMgr != nil && h.settingsMgr.IsGrokWorkerProxyEnabled() {
		if w := strings.TrimRight(h.settingsMgr.GetGrokWorkerProxyURL(), "/"); w != "" {
			baseURL = w
			workerProxyActive = true
		}
	}
	targetURL := BuildOpenAIModelsURL(baseURL)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		h.log("❌ [Grok 模型列表透传] 构造请求失败: %v", err)
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}
	req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
	req.Header.Set("Accept", "application/json")
	// 账号专属出口伪装 IP:经 Worker 代理出口时通过 X-Egress-IP 头透传(与对话链路同口径)。
	if strings.TrimSpace(poolAccount.EgressIP) != "" {
		req.Header.Set("X-Egress-IP", strings.TrimSpace(poolAccount.EgressIP))
	}
	// Worker 通用代理出口:注入 X-Target-Upstream 头让 Worker 知道真正上游地址。
	if workerProxyActive {
		req.Header.Set("X-Target-Upstream", strings.TrimRight(poolAccount.BaseURL, "/"))
	}
	// Grok CLI 身份头注入(对齐对话请求 grok.go 上游注入, 见 applyGrokCLIHeaders):模型列表端点
	// 同样受 chat-proxy 版本闸门约束, 不注入会 426 走 buildFallbackGrokModels 兜底, 拿不到上游真模型清单。
	applyGrokCLIHeaders(req, poolAccount.BaseURL, h.accountMgr.GetGrokCliVersion())

	h.log("🟢 [Grok 模型列表透传] 使用账号 %s | BaseURL: %s | 请求上游: %s | Token前缀: %s...",
		poolAccount.Email, poolAccount.BaseURL, targetURL,
		func() string {
			t := poolAccount.AccessToken
			if len(t) > 12 {
				return t[:12]
			}
			return t
		}())

	resp, errDo := h.client.Do(req)
	if errDo != nil {
		h.log("❌ [Grok 模型列表透传] 上游网络请求失败: %v | 目标: %s", errDo, targetURL)
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}
	defer resp.Body.Close()

	bodyBytes, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		h.log("❌ [Grok 模型列表透传] 读取上游响应体失败: %v", errRead)
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.log("⚠️ [Grok 模型列表透传] 上游响应状态码 %d 非 200 | 响应体: %s", resp.StatusCode, truncateBody(bodyBytes, 500))
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}

	ids, ok := extractNvidiaModelIDs(bodyBytes) // 标准 OpenAI list 形态 {data:[{id}]}, 与 NVIDIA 上游同构, 复用解析。
	h.log("✅ [Grok 模型列表透传] 上游返回 %d 个模型 | 状态码: %d", len(ids), resp.StatusCode)
	if !ok || len(ids) == 0 {
		h.log("⚠️ [Grok 模型列表透传] 上游响应为空或 JSON 解析失败, 返回默认模型列表")
		writeJSON(w, http.StatusOK, buildFallbackGrokModels(isAnthropic))
		return
	}

	// Grok 池不过滤专属清单: 全量透传上游模型 id(见上方注释)。
	if isAnthropic {
		// Anthropic 入站 → 组 Anthropic 形态回写, 并为每个模型附带声明/兜底的上下文窗口。
		writeJSON(w, http.StatusOK, formatNvidiaModelListAnthropic(ids, windowLookup))
		return
	}
	// OpenAI 入站 → 组标准 OpenAI list 形态回写(formatNvidiaModelList 是通用 OpenAI list 组装, 与 NVIDIA 无耦合, 复用)。
	writeJSON(w, http.StatusOK, formatNvidiaModelList(ids, false))
}

// grokContextWindow 是 Grok 号池模型未显式配置 MaxInputTokens 时的兜底上下文窗口。
// 131072 对齐 xAI 官方 Grok 系列主流的 128K 上下文窗口(grok-4 系列标准窗口;长上下文变体另配)。
// 上游窗口各异, 声明一个合理默认让客户端模型列表按 Anthropic schema 有值、便于未来客户端读取
// (见 formatNvidiaModelListAnthropic)。映射条目显式配置优先覆盖本默认。
const grokContextWindow int64 = 131_072

// defaultGrokFallbackModelIDs 返回号池空/上游失败时的兜底模型 id 清单(xAI 官方上游 id 命名空间)。
func defaultGrokFallbackModelIDs() []string {
	return []string{
		"grok-4.3",
		"grok-4-fast",
		"grok-4",
		"grok-3",
		"grok-3-mini",
		"grok-2",
	}
}

// buildFallbackGrokModels 组 Grok 兜底模型清单(Grok 池不做专属清单过滤, 全量兜底)。
// isAnthropic=true → Anthropic 形态; 否则标准 OpenAI list 形态(复用 formatNvidiaModelList 通用组装)。
func buildFallbackGrokModels(isAnthropic bool) map[string]interface{} {
	ids := defaultGrokFallbackModelIDs()
	if isAnthropic {
		return formatNvidiaModelListAnthropic(ids, nil) // 兜底无窗口声明查询, 不附加 max_input_tokens
	}
	return formatNvidiaModelList(ids, false)
}
