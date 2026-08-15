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
	"net/url"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

// nvidiaLogCtx 携带一次 NVIDIA 请求落地为「请求日志」+「全局综合统计」所需的最小上下文。
// recordNvidiaUsage 现在除了既有的 relay/usage/nvidiaTrends 三处落点, 还会把同一笔请求写入
// stats.Tracker 的全局 stats.Models + trends(落点4, 经 TrackRequestForFamily) 与请求日志
// (落点5, 经 AddRequestLogForFamily), 使 NVIDIA 用量初次进入仪表盘「模型统计」/「综合趋势」/
// 「请求日志」与顶部指标卡; family="nvidia" 做逻辑隔离, nvidiaTrends 物理隔离桶仍由
// TrackNvidiaRequest 单独累加, 互不污染。Host 为上游账号 BaseURL 的裸 host; Method/Path 取自
// 入站 r; StatusCode 取自上游响应(recordNvidiaUsage 仅在成功路径调用, 恒 200); SessionID
// 经 ocrSessionDisplay 取 userSession.SessionKey(auth:acc:<16hex>,与 antigravity 号池链路同款口径)
// 优先,空则回退 Token,再空回退 UserID; DurationMs 由 handleNvidia 入口起算的 startTs 算得端到端耗时。
type nvidiaLogCtx struct {
	Method       string
	Host         string
	Path         string
	SessionID    string
	Account      string
	StatusCode   int
	StartTs      time.Time
	FirstByteRec *stats.FirstByteRecorder
	// ReqBody 是入站请求体经 parseInboundBodyForLog 解析后的结构化值(空 → nil,
	// 合法 JSON → interface{},非 JSON → 原始字符串),供 recordNvidiaUsage 落库为
	// stats.RequestLog.RequestBody,使前端「请求参数详情」弹窗能展示入站请求体而非
	// 「无请求参数」兜底。由 writeNvidiaResponse 在装配 logCtx 时从入站 bodyBytes 注入;
	// 超长字段后续由 stats.TruncateRequestBody 统一截断防 OOM。
	ReqBody interface{}
	// ReqHeaders 是入站请求头经 collectInboundHeadersForLog 采集(含敏感头脱敏)后的
	// {键: 值} 映射(空入站头 → nil 接口, 与 ReqBody 口径对称),供 recordNvidiaUsage 落库为
	// stats.RequestLog.RequestHeaders,使前端「请求参数详情」弹窗能展示入站请求头而非
	// 「无请求头数据」兜底。同样由 writeNvidiaResponse 在装配 logCtx 时从入站 r.Header 注入。
	ReqHeaders interface{}
	// ReasoningEffort 是本次请求「命中上游」的思考等级(NVIDIA-NIM 池取 chat_template_kwargs.
	// reasoning_effort, 映射折叠后真正发给 NIM 的值, 只剩 high/max)。由 handleNvidia 在
	// upstreamReq 构造完成后经 extractNvidiaResolvedEffort 提取, 经 writeNvidiaResponse 透传到
	// recordNvidiaUsage → stats.RequestLog.ReasoningEffort, 供前端「模型」列追加 (档) 后缀展示。
	// 空串 = 客户端未开思考 / 全局关 / 模型禁注入 kwargs, 前端不渲染后缀。
	ReasoningEffort string
}

// nvidiaHostFromBaseURL 从上游账号 BaseURL(如 https://integrate.api.nvidia.com/v1)
// 提取裸 host(如 integrate.api.nvidia.com), 与 gemini/claude 直连链路 RequestLog.Host 只存
// 裸 host 的口径一致。解析失败时回退为去掉协议前缀的 BaseURL, 仍保可读性。
func nvidiaHostFromBaseURL(baseURL string) string {
	b := strings.TrimSpace(baseURL)
	if b == "" {
		return "nvidia"
	}
	if u, err := url.Parse(b); err == nil && u.Host != "" {
		return u.Host
	}
	// 兜底: 去掉常见协议前缀
	for _, scheme := range []string{"https://", "http://"} {
		b = strings.TrimPrefix(b, scheme)
	}
	if idx := strings.IndexAny(b, "/"); idx > 0 {
		b = b[:idx]
	}
	return b
}

// nvidia.go 实现 /nvidia/* 路由的主链路：
// 入站 Anthropic(/nvidia/v1/messages) 或 OpenAI Chat(/nvidia/v1/chat/completions) →
// 选号(支持游标轮询默认与粘性会话) → 协议转换 → 直连 NVIDIA 上游 → 响应回译 → 换号重试。
// 完整闭环以 internal/relay/compat.go 的 handleV1Internal 为模板。

// nvidiaChannel 是 NVIDIA 号池的通道标识。
const nvidiaChannel = "nvidia"

// nvidiaReqLogSeq 是 NVIDIA 请求日志(落点5)的全局原子递增序列, 用于生成稳定且无碰撞的 RequestLog.ID。
// 单独纳秒时间戳在高并发下易碰撞, 叠加单调递增序列后即使同纳秒也唯一; ID 与落点1 RelaySample.ReqID
// (nv-<nanos>) 同前缀但语义不同(落点5 是仪表盘「请求日志」行 ID, 落点1 是 relay 维度统计样本 ID),
// 用 "nvlog-" 前缀区分两套日志系统的命名空间, 便于把同一笔请求在两处日志里对照排查。
var nvidiaReqLogSeq uint64

// nvidiaReplayMaxBytes 是 NVIDIA Anthropic 流式蓄流回放的单流最大蓄流字节数。
// 超过即判定为"超大输出":直连重试轮退回边读边写旧路径(放弃重试能力)兜底失败;
// 兜底轮把它转为 lastErr 落 overloaded_error(兜底无可退的边读边写路径)。
// 抽为包级常量是因为直连循环与兜底 helper 必须共用同一阈值,避免两处硬编码值飘移导致
// "直连认为可蓄、兜底认为超大"这类判定不一致。16 MiB 是经验值,覆盖绝大多数对话回复。
const nvidiaReplayMaxBytes = 16 * 1024 * 1024 // 16 MiB

// handleNvidia 处理 /nvidia/* 请求。
func (h *APICompatHandler) handleNvidia(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method == http.MethodGet || path == "/nvidia/v1/models" || path == "/nvidia/models" || strings.HasSuffix(path, "/models") {
		h.handleNvidiaModels(w, r, userSession)
		return
	}

	bodyBytes, err := readBodyWithTimeout(r, nvidiaInboundReadTimeout)
	if err != nil {
		if errors.Is(err, ErrBodyReadTimeout) {
			// 入站 body 读取超时:客户端只发了 header 不发 body,或入站链路半死
			// (TCP keep-alive 最快 2h 才探测,应用层会永久挂在 io.ReadAll)。
			// 回写 408 让 Claude Code 等 Anthropic 客户端识别后自动换连接重试,
			// 而非让 handler 永久卡死、上游日志 🟢 永远打不出。
			kind := inboundKindOfPath(path)
			h.log("⏱️ [NVIDIA 中继] 入站 body 读取超时(%s),路径 %s,判定客户端未发完整请求体/链路半死,回写 408 %s", nvidiaInboundReadTimeout, path, kind)
			writeNvidiaInboundTimeout(w, kind)
			return
		}
		h.log("❌ [NVIDIA 中继] 入站 body 读取失败(路径 %s): %v", path, err)
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	reqID := fmt.Sprintf("nv_%d", time.Now().UnixNano())
	// start: 入站请求接入时刻, 作为「请求日志」DurationMs 的端到端耗时基准(与 gemini/claude
	// 直连链路口径一致), 经 writeNvidiaResponse → recordNvidiaUsage 透传到落点5。
	start := time.Now()
	// firstByteRec: 全程共享的 TTFT 打点器, 以入站接入时刻 start 为基准。
	// 修复点: 之前在 writeNvidiaResponse 内新建并到 message_start 才打点, 而 handleNvidia 流式
	// 分支的 bufio.Peek(1024)(本文件下方)会阻塞到上游累积 ≥1024 字节才放行 writeNvidiaResponse,
	// 小首帧(<1024B)场景下打点被推迟到请求末尾 → FirstByteMs 兜底≈DurationMs → 前端「响应时间==耗时」。
	// 现改为在 handleNvidia 唯一成功 200 判定点(下方 line 508, 响应头已到达、Peek 阻塞之前)打点,
	// TTFT 如实反映上游响应头到达时刻。
	firstByteRec := stats.NewFirstByteRecorder(start)
	if h.settingsMgr != nil {
		enabled := h.settingsMgr.GetEnableDebuggerMode()
		logPath := h.settingsMgr.GetResolvedDebuggerLogPath()
		GetGlobalDebugger().Configure(enabled, logPath)
	}
	GetGlobalDebugger().LogClientRequest(reqID, r.Method, r.URL.Path, r.Header, bodyBytes)

	// 会话级隔离键注入:供 OCR 缓存等按会话隔离的特性共享同一会话 ID(同用户不同会话不共享
	// 缓存槽),并贯穿下方日志让"哪个会话在打"可观测。
	//
	// 取键优先级与 Claude/Codex 客户端头识别见 relay.session_key.ensureSessionInfo 的统一注入器:
	//   1. 客户端原生会话头(X-Claude-Code-Session-Id / Codex Session-Id / Thread-Id)→ "claude:UUID"/"codex:UUID";
	//   2. ExtractSessionKey + auth:acc:/sock:acc:/acc: 前缀(脚本/SDK 直调兜底)。
	// sessionRouter==nil(单测未注入)且无客户端头时跳过注入,OCR 缓存回退 UserKey 隔离。
	h.ensureSessionKey(userSession, r, bodyBytes)

	// 入站协议判定：按路径决定（三选一）
	inboundAnthropic := strings.HasSuffix(path, "/v1/messages")
	inboundOpenAI := strings.HasSuffix(path, "/v1/chat/completions")
	inboundResponses := strings.HasSuffix(path, "/v1/responses")
	// count_tokens:Anthropic 可选端点(官方 LLM Gateway Protocol 标 optional),CLI 用它预估 input_tokens。
	// 纯本地字符级粗估后回 200,不请求上游、不消耗号池、不计费(见 anthropic_count_tokens.go)。
	// 必须在下方三个生成端点的 404 兜底之前识别,否则会被当成"不支持的端点"回 404 —— 虽官方允许降级,
	// 但那样 CLI 拿不到 relay 侧估算值且日志噪声明显。
	if strings.HasSuffix(path, "/v1/messages/count_tokens") {
		h.handleNvidiaCountTokens(w, bodyBytes)
		return
	}
	if !inboundAnthropic && !inboundOpenAI && !inboundResponses {
		h.log("🚫 [NVIDIA 中继] 不支持的端点 %s (支持 /nvidia/v1/messages|/vc/v1/messages, /nvidia/v1/chat/completions|/vc/v1/chat/completions, /nvidia/v1/responses|/vc/v1/responses),回写 404", path)
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "unsupported nvidia endpoint: use /nvidia/v1/messages, /nvidia/v1/chat/completions or /nvidia/v1/responses (or the /vc alias)",
		})
		return
	}

	// 解析入站请求以确定模型与 stream。
	// Responses 入站虽有独立的 input[] 结构，但 model/stream 字段同构，先提取这两个公共字段，
	// 请求体到 OpenAIChatRequest 的完整转换在选号后按 inbound 分支执行。
	var inModel string
	var isStreaming bool
	if inboundAnthropic {
		var req AnthropicRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			h.log("🚫 [NVIDIA 中继] Anthropic 入站请求体解析失败(路径 %s): %v,回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else if inboundResponses {
		// Responses 请求统一走 ParseUnifiedOpenAIRequest 解析（兼容 input[] 与 messages[]），
		// model/stream 取用统一产物的对应字段。
		req, err := ParseUnifiedOpenAIRequest(bodyBytes)
		if err != nil {
			h.log("🚫 [NVIDIA 中继] Responses 入站请求体解析失败(路径 %s): %v,回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid responses request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	} else {
		var req OpenAIChatRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			h.log("🚫 [NVIDIA 中继] OpenAI Chat 入站请求体解析失败(路径 %s): %v,回写 400", path, err)
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
			return
		}
		inModel = req.Model
		isStreaming = req.Stream
	}
	// 流式也可能通过 Accept: text/event-stream 或 stream=true 表达，此处仅以 body.stream 为准。

	// NVIDIA family 配额预扣额校验（独立于 gemini/claude）
	if h.authMgr != nil && h.authMgr.userMgr != nil {
		user := h.authMgr.userMgr.GetUserByID(userSession.UserID)
		if user != nil {
			if err := nvidiaQuotaCheck(userSession.UserID, user.Quotas.Nvidia); err != nil {
				h.log("🚦 [NVIDIA 中继] 用户 %s NVIDIA 配额校验未通过: %v,回写 429 quota_exceeded", userSession.UserKey, err)
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

	// 选号：只要携带 /nvidia/ 前缀，全量使用英伟达号池进行会话粘性负载均衡与轮询换号
	poolChannel := nvidiaChannel
	available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, inModel)

	if len(available) == 0 {
		h.log("⛔ [NVIDIA 中继] NVIDIA 号池无可用账号(channel=nvidia, model=%s),回写 503 nvidia_pool_empty", inModel)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "nvidia_pool_empty",
				"message": "no available NVIDIA account in pool (channel nvidia)",
			},
		})
		return
	}

	skippedAccounts := make(map[string]bool)
	maxAttempts := len(available)
	if maxAttempts > 5 {
		maxAttempts = 5
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
		lbMode = h.accountMgr.GetNvidiaLBMode()
	}

	var lastResp *http.Response
	var lastErr error
	var lastErrBody []byte
	var lastErrCode int

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 取消守卫(对齐 handler_retry_loop.go:24-30):客户端主动取消时 r.Context() 即被撤销,
		// 必须在此立即终止换号循环。否则后续每个号的上游请求因绑定 r.Context()(见下方
		// http.NewRequestWithContext(r.Context(), ...))会在 Do() 阶段瞬间返回 context.Canceled,
		// 连续砍掉整个号池(maxAttempts 上限 5)、并把可用号误拉黑 60s 冷却 → 紧接着的真实请求
		// 可能撞 nvidia_pool_empty。这是 "context canceled 刷屏 + 号池被误冷冻" 的根因。
		select {
		case <-r.Context().Done():
			h.log("⏹️ [NVIDIA 中继] 客户端已取消连接(会话 %s),终止换号重试。", ocrSessionDisplay(userSession))
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
				lastErr = fmt.Errorf("all nvidia accounts in pool failed")
			}
			break
		}

		var poolAccount *account.Account
		// 并发限制:先按上限过滤超限账号(对齐 passthrough 链路口径)。
		// 过滤集非空喂 pickNvidiaAccount(保持 sticky/round-robin + nvidiaStats 最少计数语义,
		// nvidiaStats 是 1 分钟历史请求计数,与在途并发计数正交,过滤在先、nvm 计数在后);
		// 过滤集空则取在途并发最少的号允许超额降级(对齐用户「绝不硬拒」预期)。
		limit := h.accountMgr.GetNvidiaMaxConcurrency()
		filtered := h.accountMgr.FilterByConcurrency(activeAvailable, limit)
		if len(filtered) > 0 {
			poolAccount = h.pickNvidiaAccount(lbMode, sessionKey, filtered)
		} else {
			overAcc := h.accountMgr.LeastLoadedAccount(activeAvailable)
			if overAcc != nil {
				poolAccount = overAcc
				h.log("⚠️ [并发限制] NVIDIA 池并发全满(限 %d),超额降级到最少并发号 %s", limit, overAcc.Email)
			}
		}
		if poolAccount == nil {
			lastErr = fmt.Errorf("no available nvidia account assigned from pool")
			break
		}

		// 选号通过后立即占用并发槽。后续所有失败/早返 break/return 以本 poolAccount.ID 寻址 Release;
		// 成功路径(行 614 writeNvidiaResponse 调用后)在请求结束前 Release(蓄流重试全程占槽,见 nvidia_stream.go)。
		h.accountMgr.AcquireAccount(poolAccount.ID)
		// acquired 标志用于外层 break 后统一判定:正常外层换号(被 skippedAccounts 标记)需释放;
		// 但成功路径不通过外层 break 退出(直接 writeNvidiaResponse + return),故此处 acquire 与下方各
		// 释放点严格按退出路径手工配对,不依赖 defer(defer 在 for 循环体内每次迭代不会立即执行)。
		acquired := true
		_ = acquired

		// 模型映射(账号级四档位)
		upstreamModel := mapNvidiaModel(inModel, poolAccount)

		// 根据入站协议构造发往上游的 OpenAI Chat 请求体
		// (思考注入由翻译函数内部依据 body 显式思考信号判定,不再解析 redact-thinking 头:
		// claude-cli 2.1.220 该头在开/关两态均常驻,与思考 on/off 无关,曾据此短路导致开思考失效。)

		// 根据入站协议构造发往上游的 OpenAI Chat 请求体
		var upstreamReq *OpenAIChatRequest
		// OCR 自递归守卫:本请求若来自 OCR 引擎的跨号池出站(打 18444 /route 命中本池),
		// 携带 X-Antigravity-OCR-Self: 1,其 image 块是给所选多模态模型看的,必须原样透传,
		// 不得再次触发 image→文本降级(否则形成 OCR→OCR 死循环)。
		ocrSelf := r.Header.Get("X-Antigravity-OCR-Self") == "1"
		if inboundAnthropic {
			var anthReq AnthropicRequest
			if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
				h.log("🚫 [NVIDIA 中继] 选号后 Anthropic 请求体二次解析失败(账号 %s): %v,回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
				return
			}
			anthReq.Model = upstreamModel
			anthReq.UserAgent = r.Header.Get("User-Agent")

			// 本地图片路径自愈(L2.5 预处理):Claude Code 等客户端对未识别模型(如 nvidia/z-ai/glm-4.9)
			// 会在发送前剔除 image 块,本地截图路径作为纯 text 块发来。此处先扫 text 块裸路径,
			// 命中本机真实图片文件则读→base64→OCR→descHeader 原地注入,再交下游 image 降级与转发。
			// 仅本地自用直连(ocrSelf=false 且来源=official_bypass/default_bypass)放行;静默 miss 不报错。
			if !ocrSelf {
				if enriched := h.ocr.EnrichLocalImagePathsInAnthropic(&anthReq, userSession); enriched > 0 {
					h.log("✅ [NVIDIA 中继] 检测到 %d 个本地图片路径,已读图 OCR 注入 text 块(账号 %s | 会话 %s)", enriched, poolAccount.Email, ocrSessionDisplay(userSession))
				}
			}

			// image 自愈降级:仅当上游模型不原生支持多模态时才把入站 Anthropic 的 image
			// content block 先用本地 Gemini(gemini-2.5-flash)OCR 降级为纯文本(避免直送触发 400 /
			// 内容丢失),上游段永远只见 text、零负担。判据由 h.ocr.modelSupportsImage 统一承载
			// (配置优先 RelayModelMapping 的 Multimodal 声明位 → 启发式模型名前缀白名单),替代原
			// "NVIDIA 池一律降"的盲判:NIM 上游若为 qwen-vl / gpt-4o / glm-4v 等多模态模型会自动跳过
			// 降级、图块原样透传,省 OCR 配额 + 保留原生视觉理解;DeepSeek/glm 等非多模态则照旧降级。
			// 仅 AnthropicMessage.UnmarshalJSON 解析出 Source 字段后才命中(见 compat_translate.go)。
			if !ocrSelf && !h.ocr.modelSupportsImage(upstreamModel) {
				replaced, errDown, ocrHits, ocrMisses, ocrSkipped := h.ocr.DowngradeAnthropicImagesToText(&anthReq, userSession)
				if errDown != nil {
					h.log("⚠️ [NVIDIA 中继] image 自愈降级出错(账号 %s | 会话 %s): %v,继续原始请求", poolAccount.Email, ocrSessionDisplay(userSession), errDown)
				} else if replaced > 0 {
					// 透出三计数(命中/未命中/窗外占位)与会话 ID,消除"每次请求都打印 OCR 降级"日志的歧义:
					// 命中=历史图纳秒级直接返回(不烧 antigravity 额度、无 ~3s 延迟);
					// 未命中=cache miss 真打了 gemini 上游重新 OCR;窗外占位=末尾 10 条之外的图缓存未命中,
					// 走占位文本兜底,绝不重新 OCR(省配额)。
					h.log("✅ [NVIDIA 中继] 检测到 %d 个 image 块,已本地 OCR 降级为纯文本(账号 %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)。若截图含复杂代码/表格,可在设置中将 OCR 模型切换为更强的多模态模型以提升转写保真", replaced, poolAccount.Email, ocrSessionDisplay(userSession), ocrHits, ocrMisses, ocrSkipped)
				}
			}

			mappings := h.getRelayModelMappingSafe()
			// 多模态上游保图:上游模型原生支持视觉时,image 块不走降级(nvidia.go 上方闸已跳过),
			// 必须让翻译层把 image 块转译为 OpenAI Chat Vision 数组形态 content 原样透传,否则
			// AnthropicToOpenAIChat 旧默认分支会静默丢弃图块(字符串 content 装不下 image_url)。
			// 非多模态 & ocrSelf 自递归 → 走旧字符串路径(零回归)。
			preserveImages := !ocrSelf && h.ocr.modelSupportsImage(upstreamModel)
			u, err := AnthropicToOpenAIChatPreservingImages(&anthReq, preserveImages, mappings)
			if err != nil {
				h.log("🚫 [NVIDIA 中继] Anthropic→OpenAI 转换失败(账号 %s): %v,回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "anthropic->openai transform failed: " + err.Error()})
				return
			}
			upstreamReq = u
		} else if inboundResponses {
			// Responses(含 codex /v1/responses) → 统一解析 → OpenAIChatRequest
			u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
			if err != nil {
				h.log("🚫 [NVIDIA 中继] Responses→OpenAI 转换失败(账号 %s): %v,回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "responses->openai transform failed: " + err.Error()})
				return
			}
			// Codex/Responses 入站的思考等级透传:与 OpenAI Chat 入站同款口径,从原始 body 提
			// reasoning_effort(顶层,Codex 形态)或 reasoning.effort(OpenRouter 形态),按 NIM 取值
			// 模式映射后注入 chat_template_kwargs,让 NIM 推理模型按等级思考。
			// ResponsesToOpenAIChat → ParseUnifiedOpenAIRequest 的解析结构未接 reasoning_effort 字段,
			// 解析阶段即丢,故必须在此用原始 bodyBytes 二次提取后原地注入(与 injectNvidiaChatTemplateKwargs
			// 在 OpenAI Chat 分支的用法逐行等价)。
			mappings := h.getRelayModelMappingSafe()
			injectNvidiaChatTemplateKwargs(u, bodyBytes, upstreamModel, mappings)
			upstreamReq = u
		} else {
			// OpenAI Chat 入站(含 Vision 数组形态 content):先降级 image_url 块为纯文本,再 Unmarshal。
			// 必要性有二:(1) ChatMessage.Content 刻意是 string(nvidia_translate_types.go 注释:NVIDIA/多数
			//   OpenAI 兼容上游用 serde 反序列化要求每条 message 显式带 content 字段),标准 json.Unmarshal
			//   遇数组形态 content 会直接拒收整条请求 → 客户端拿到 400;降级后 content 全为 string,下游
			//   Unmarshal 成功。(2) NVIDIA 上游(glm-5.2 等)不支持多模态,image_url 直送上游会触发 400 /
			//   内容丢失;先用本地 Gemini OCR 把每张图降级为纯文本,上游段永远只见 text、零负担。
			//   失败不阻断(占位文本兜底)。无图时原样返回(零变更)。
			// 多模态判据由 h.ocr.modelSupportsImage 统一承载:NIM 上游若为 qwen-vl / gpt-4o / glm-4v 等
			// 多模态模型会自动跳过降级、图块原样透传(保留原生视觉理解);非多模态则照旧降级。
			if !ocrSelf && !h.ocr.modelSupportsImage(upstreamModel) {
				// 本地图片路径自愈(L2.5 预处理):同 inboundAnthropic 分支,先扫 text 块裸路径注入再走 image 降级。
				// EnrichLocalImagePathsInOpenAIChat 返回 newBody(命中时为新 bytes,未命中时原样返回入参),
				// 此处用其结果替换 bodyBytes 再交 DowngradeOpenAIChatImagesToText 做结构化 image 块降级。
				if nb, enriched := h.ocr.EnrichLocalImagePathsInOpenAIChat(bodyBytes, userSession); enriched > 0 {
					bodyBytes = nb
					h.log("✅ [NVIDIA 中继] OpenAI Chat 检测到 %d 个本地图片路径,已读图 OCR 注入 text 块(账号 %s | 会话 %s)", enriched, poolAccount.Email, ocrSessionDisplay(userSession))
				}
				downBody, replacedDown, errDown, ocrHitsDown, ocrMissesDown, ocrSkippedDown := h.ocr.DowngradeOpenAIChatImagesToText(bodyBytes, userSession)
				if errDown != nil {
					h.log("⚠️ [NVIDIA 中继] OpenAI Chat image 自愈降级出错(账号 %s | 会话 %s): %v,继续原始请求", poolAccount.Email, ocrSessionDisplay(userSession), errDown)
				} else if replacedDown > 0 {
					h.log("✅ [NVIDIA 中继] OpenAI Chat 检测到 %d 个 image 块,已本地 OCR 降级为纯文本(账号 %s | 会话 %s | 缓存命中 %d / 未命中 %d / 窗外占位 %d)", replacedDown, poolAccount.Email, ocrSessionDisplay(userSession), ocrHitsDown, ocrMissesDown, ocrSkippedDown)
					bodyBytes = downBody
				}
			}
			var chatReq OpenAIChatRequest
			if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
				h.log("🚫 [NVIDIA 中继] 选号后 OpenAI Chat 请求体二次解析失败(账号 %s): %v,回写 400", poolAccount.Email, err)
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
				return
			}
			chatReq.Model = upstreamModel
			// 流式注入 stream_options.include_usage，确保上游在 SSE 末尾吐 usage
			if chatReq.Stream && (chatReq.StreamOptions == nil || !chatReq.StreamOptions.IncludeUsage) {
				chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
			}
			// Codex/OpenAI 入站的思考等级透传:从原始 body 提 reasoning_effort(顶层)
			// 或 reasoning.effort(OpenRouter 形态),按 NIM 取值模式映射后注入 chat_template_kwargs,
			// 让 NIM 推理模型按等级思考。客户端未传思考 → 不注入 → 上游行为不变(回归安全)。
			// 注意:顶层 reasoning_effort 不是 NIM 认的字段(NIM 认 chat_template_kwargs),
			// 故 OpenAIChatRequest 无该字段,从原始 bodyBytes 单独取,避免污染上游请求结构。
			mappings := h.getRelayModelMappingSafe()
			injectNvidiaChatTemplateKwargs(&chatReq, bodyBytes, upstreamModel, mappings)
			upstreamReq = &chatReq
		}

		// 构造上游 URL：优先使用全局 Worker 代理出口（若启用），否则回退账号本身的 BaseURL
		baseURL := strings.TrimRight(poolAccount.BaseURL, "/")
		if h.isNvidiaWorkerProxyEnabledSafe() {
			workerURL := strings.TrimRight(h.getNvidiaWorkerProxyURLSafe(), "/")
			if workerURL != "" {
				baseURL = workerURL
			}
		}
		targetURL := baseURL + "/v1/chat/completions"
		if strings.HasSuffix(baseURL, "/v1") {
			targetURL = baseURL + "/chat/completions"
		}

		upstreamBody, err := json.Marshal(upstreamReq)
		if err != nil {
			lastErr = err
			// 上游请求体构造失败:释放并发槽再换号。
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			break
		}

		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
		if err != nil {
			lastErr = err
			// 建请求失败(坏 URL):释放并发槽再换号。
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
		req.Header.Set("Accept", "application/json")
		if strings.TrimSpace(poolAccount.EgressIP) != "" {
			req.Header.Set("X-Egress-IP", strings.TrimSpace(poolAccount.EgressIP))
		}
		// NVIDIA 上游不识别 anthropic 头，不注入

		proxyTag := ""
		if h.isNvidiaWorkerProxyEnabledSafe() {
			proxyTag = " [Worker 代理出口]"
		}
		egressTag := ""
		if strings.TrimSpace(poolAccount.EgressIP) != "" {
			egressTag = " (IP: " + strings.TrimSpace(poolAccount.EgressIP) + ")"
		}
		h.log("🟢 [NVIDIA 中继 %d/%d]%s 用户 %s 分配账号 %s%s | 模型 %s -> %s | 会话 %s | %s", attempt+1, maxAttempts, proxyTag, userSession.UserID, poolAccount.Email, egressTag, inModel, upstreamModel, ocrSessionDisplay(userSession), targetURL)

		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}

		// 单账号针对 429 尝试最多 5 次，重试 5 次均 429 失败才切下一个账号
		var activeResp *http.Response
		accountSuccess := false
		const maxSingleAcc429Retries = 5
		// 压缩断路器：单请求服务端就地压缩重试计数（文档 §3 MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES=3）。
		singleCompressFailures := 0
		// 首次 ResourceExhausted 帧的原始文本，用于压缩救不活时判定是否回写 400 引导客户端自压（治本）。
		var resentExhaustedFrame string

		for singleAttempt := 1; singleAttempt <= maxSingleAcc429Retries; singleAttempt++ {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				lastErr = err
				// 内层建请求失败(坏 context):释放并发槽(外层 NewRequest 已 Acquire)。
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
			req.Header.Set("Accept", "application/json")
			if strings.TrimSpace(poolAccount.EgressIP) != "" {
				req.Header.Set("X-Egress-IP", strings.TrimSpace(poolAccount.EgressIP))
			}

			if singleAttempt > 1 {
				h.log("🔄 [NVIDIA 中继 429 重试 %d/%d] 账号 %s 遇到 429 限流，等待 2 秒后原地重试...", singleAttempt, maxSingleAcc429Retries, poolAccount.Email)
			}

			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				// 客户端主动取消特判:r.Context() 被撤销时,上游 Do() 瞬间返回 context.Canceled
				// (请求未真正发往上游)。此时该号本身健康,绝不能拉黑 60s 冷却,也不能继续换号
				// (换下一个号仍会被同一已取消的 context 砍掉,把整个号池挨个"砍头"刷屏)。
				// 直接整体退出,不写响应(客户端已断开,写了也是对空管道写)。
				if errors.Is(errDo, context.Canceled) || errors.Is(errDo, context.DeadlineExceeded) {
					h.log("⏹️ [NVIDIA 中继] 账号 %s 上游请求被客户端取消(ctx err=%v),终止换号重试(不冷冻该号)。", poolAccount.Email, errDo)
					lastErr = errDo
					// 客户端断开:本次请求结束,释放并发槽。
					h.accountMgr.ReleaseAccount(poolAccount.ID)
					return
				}
				h.log("⚠️ [NVIDIA 中继] 账号 %s 访问上游失败: %v", poolAccount.Email, errDo)
				skippedAccounts[poolAccount.ID] = true
				lastErr = errDo
				lastResp = nil
				// 网络错误：短期冷静该号 60s，换号重试
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				// 网络错误换号:本次请求在该号上结束,释放并发槽。
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 处理 429 限流：5 次以内原地退避 2 秒重试，重试 5 次均 429 失败才冷冻切号
			if resp.StatusCode == http.StatusTooManyRequests {
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream status 429")

				if singleAttempt < maxSingleAcc429Retries {
					time.Sleep(2 * time.Second)
					continue
				}

				h.log("⚠️ [NVIDIA 中继] 账号 %s 重试 %d 次仍返回 429，冷冻该账号并换号...", poolAccount.Email, maxSingleAcc429Retries)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				// 429 耗尽换号:释放并发槽。
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 401/403：鉴权或配额问题，不原号重试，换号
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				h.log("⚠️ [NVIDIA 中继] 账号 %s 上游返回 %d，剔除换号重试...", poolAccount.Email, resp.StatusCode)
				cooldown := 5 * 60 * 1000
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream status %d", resp.StatusCode)
				skippedAccounts[poolAccount.ID] = true
				h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+int64(cooldown), nvidiaChannel, inModel)
				h.sessionRouter.UnbindSession(sessionKey)
				// 401/403 鉴权失败换号:释放并发槽。
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 5xx：服务端错误，换号重试
			if resp.StatusCode >= 500 {
				h.log("⚠️ [NVIDIA 中继] 账号 %s 上游 5xx(%d)，换号重试...", poolAccount.Email, resp.StatusCode)
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastResp = nil
				lastErrBody = errBody
				lastErrCode = resp.StatusCode
				lastErr = fmt.Errorf("nvidia upstream server error %d", resp.StatusCode)
				skippedAccounts[poolAccount.ID] = true
				// 5xx 换号:释放并发槽。
				h.accountMgr.ReleaseAccount(poolAccount.ID)
				break
			}

			// 成功 HTTP 200
			// 上游响应头已到达——在此刻打点 TTFT(幂等 sync.Once), 严格早于下方流式 Peek(1024) 阻塞。
			// 这样小首帧(<1024B)场景下 FirstByteMs 如实反映上游响应头到达时刻, 不再被 Peek 推迟成≈DurationMs。
			firstByteRec.MarkFirstByte()
			if isStreaming {
				bufReader := bufio.NewReader(resp.Body)
				peekBytes, _ := bufReader.Peek(1024)
				peekStr := string(peekBytes)
				if strings.Contains(peekStr, `"error"`) && (strings.Contains(peekStr, `"ResourceExhausted"`) || strings.Contains(peekStr, `"internal_server_error"`) || strings.Contains(peekStr, `"Internal server error"`)) {
					// 上游流内首帧抛出 ResourceExhausted / internal_server_error(500)：
					// 根因多为 worker 缓冲打满，单请求体积过大是诱因之一；换号无效（同端点簇）。
					// 先尝试服务端就地压缩请求体并原地复用当前号重试（救当前请求）。
					h.log("⚠️ [NVIDIA 中继] 账号 %s 上游流内首帧抛出 ResourceExhausted/500，先尝试就地压缩请求体重试...", poolAccount.Email)
					resp.Body.Close()
					lastResp = nil
					lastErr = fmt.Errorf("nvidia upstream sse error: %s", truncateBody(peekBytes, 200))
					if resentExhaustedFrame == "" {
						resentExhaustedFrame = peekStr
					}

					// 压缩断路器：最多 3 轮
					if singleCompressFailures < 3 {
						compReq, compOK, compEnabled := h.tryCompressNvidiaRequest(upstreamReq)
						if compEnabled && compOK && compReq != nil {
							if newBody, e := json.Marshal(compReq); e == nil && len(newBody) < len(upstreamBody) {
								h.log("🧩 [NVIDIA 中继] 已压缩请求体 %d → %d 字节，原地复用账号 %s 重试...", len(upstreamBody), len(newBody), poolAccount.Email)
								upstreamBody, upstreamReq = newBody, compReq
								singleCompressFailures++
								continue // 复用当前号、不冷冻、不 break 到外层换号
							}
						}
						// 压缩无效或不可压缩：计一次失败，仍在本号重试（给上游 worker 可能短暂的限流恢复机会）
						singleCompressFailures++
						if singleCompressFailures < 3 {
							continue
						}
					}

					// 压缩 3 轮仍失败：判定是否"上下文超窗/无可恢复"语义。
					// 若是 → 回写 Anthropic 标准 invalid_request_error 400，引导客户端本地 /compact 自压（治本）。
					// 否则 → 保留原冷冻换号路径。
					if looksLikeContextTooLong(resentExhaustedFrame) {
						h.log("🛑 [NVIDIA 中继] 账号 %s 压缩 3 轮仍失败且首帧含上下文超窗语义，回写 400 invalid_request_error 引导客户端自压...", poolAccount.Email)
						h.replyAnthropicContextTooLong(w, inboundAnthropic, upstreamModel)
						// 上下文超窗回写 400 后本次请求结束:释放并发槽。
						h.accountMgr.ReleaseAccount(poolAccount.ID)
						return
					}
					h.log("⚠️ [NVIDIA 中继] 账号 %s 压缩重试耗尽，冷冻该账号并换号...", poolAccount.Email)
					skippedAccounts[poolAccount.ID] = true
					h.accountMgr.SetAccountCooldownForChannel(poolAccount.ID, time.Now().UnixNano()/1e6+60*1000, nvidiaChannel, inModel)
					h.sessionRouter.UnbindSession(sessionKey)
					// 压缩耗尽换号:释放并发槽。
					h.accountMgr.ReleaseAccount(poolAccount.ID)
					break
				}
				resp.Body = struct {
					io.Reader
					io.Closer
				}{
					Reader: bufReader,
					Closer: resp.Body,
				}
			}

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
			// 入站 input_tokens 估算:仅 anthropic 流式分支用此值填 message_start.usage.input_tokens,
			// 让客户端(Claude Code spinner)流首即显示 ↑。非 anthropic/非流式分支忽略此值,传 0 即可
			// (writeNvidiaResponse 仅在 anthropic+stream 路径透传它,其余路径不读)。estimateInputTokens
			// 吃 *AnthropicRequest,此处 bodyBytes 是入站原始 body(Anthropic 形态),用其估算;
			// OpenAI Chat/Responses 入站不回译 Anthropic,无需估算,传 0。
			var inboundInputTokens int
			if inboundAnthropic {
				inboundInputTokens = estimateInputTokensFromBody(bodyBytes)
			}
			// inboundBody 透传入站原始请求体(bodyBytes, 非协议转换后的 upstreamBody),
			// 供 writeNvidiaResponse 两侧消费:Anthropic 流式回译在上游断流时以完整上游请求体重连;
			// 同时作为入站请求体原貌注入 logCtx.ReqBody 落库(前端详情弹窗展示「入站时」请求体)。
			// resolvedEffort: 本次请求命中上游的思考等级(upstreamReq 构造完成后, 经 injectNvidiaChatTemplateKwargs
			// 注入到 chat_template_kwargs.reasoning_effort 的值, 只剩 high/max), 透传给 writeNvidiaResponse
			// 装配 logCtx.ReasoningEffort 落库, 供前端「模型」列追加 (档) 后缀展示。
			resolvedEffort := extractNvidiaResolvedEffort(upstreamReq)
			h.writeNvidiaResponse(w, r, activeResp, inboundKind, isStreaming, upstreamModel, userSession, poolAccount, targetURL, upstreamBody, bodyBytes, inboundInputTokens, start, firstByteRec, resolvedEffort)
			// 蓄流重试(pullAnthropicStreamWithRetry)全程占账号槽,writeNvidiaResponse 返回即响应流结束,
			// 本次请求结束,释放并发槽。release 必须在 writeNvidiaResponse 返回之后(见方案 §4)。
			h.accountMgr.ReleaseAccount(poolAccount.ID)
			return
		}
	}

	// 重试用尽：回写最后一次上游错误状态码与错误体（若有），否则 502 兜底
	if lastResp != nil {
		defer lastResp.Body.Close()
		for k, values := range lastResp.Header {
			if isAnthropicPassthroughHeader(k) {
				continue
			}
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(lastResp.StatusCode)
		_, _ = io.Copy(w, lastResp.Body)
		return
	}
	if lastErrBody != nil && lastErrCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastErrCode)
		_, _ = w.Write(lastErrBody)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "nvidia pool exhausted: " + errStr(lastErr)})
}

func (h *APICompatHandler) getRelayModelMappingSafe() (mappings []settings.ModelMappingEntry) {
	if h == nil || h.settingsMgr == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			mappings = nil
		}
	}()
	return h.settingsMgr.GetRelayModelMapping()
}

func (h *APICompatHandler) isNvidiaWorkerProxyEnabledSafe() (enabled bool) {
	if h == nil || h.settingsMgr == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return h.settingsMgr.IsNvidiaWorkerProxyEnabled()
}

func (h *APICompatHandler) getNvidiaWorkerProxyURLSafe() (url string) {
	if h == nil || h.settingsMgr == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			url = ""
		}
	}()
	return h.settingsMgr.GetNvidiaWorkerProxyURL()
}
