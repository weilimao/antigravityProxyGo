package relay

// compat.go: relay 层兼容层主入口 —— APICompatHandler 结构体定义 + 构造函数 + ServeHTTP 路由分发。
// 原单文件 2064 行已按职责拆分,本文件只保留结构体/构造/路由主干与其直接依赖的工具变量,
// 各业务 handler 按职责拆到卫星文件(同包内共享符号,逻辑逐行等价,零回归):
//   compat_models.go        /v1/models 模型列表 handler
//   compat_dispatch.go      OpenAI/Anthropic 入口 handler + dispatchToGemini 分发
//   compat_response.go      Gemini 非流式响应 -> 客户端协议回译
//   compat_stream.go        Gemini 流式 SSE -> 客户端协议流式 SSE 回译
//   compat_v1internal.go    /v1internal:* 桥接 + mapModelForProjectInRelay
//   compat_ocr.go           OCR 包级共享辅助(ocrOwnerKey/ocrSessionDisplay,无运行时状态)
//   ocr_engine.go            OCR 引擎核心 L1(OCRService,从 compat_ocr.go 抽离)
//   ocr_downgrade_anthropic.go  Anthropic 协议降级 L2
//   ocr_downgrade_gemini.go     Gemini 协议降级 L2
//   ocr_downgrade_openai.go     OpenAI Chat 协议降级 L2
//   ocr_fetch.go / ocr_dataurl.go  URL/Data URL 图片抓取(P2)
//   compat_ratelimit.go     RateLimiter 限流器(已在 Step 2-b 拆出)

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var localProxyAddr = "127.0.0.1:18443"

// localRelayAddr 是本地中继服务器(18444)回环地址,承载 /route/* 通用按模型路由入口。
// OCR 引擎在处理非 Google 族前缀模型(如 nvidia/xxx、other/openai/xxx)时,
// 把 Gemini 请求转译为 OpenAI Chat 后打到该入口,由 handleRoutedForward 路由到对应号池。
var localRelayAddr = "127.0.0.1:18444"

const defaultOcrModel = "gemini-2.5-flash"

type APICompatHandler struct {
	authMgr       *AuthManager
	accountMgr    *account.Manager
	sessionRouter *session.Router
	statsTracker  *StatsTracker
	// usageTracker 用于把号池成员账号维度的请求/Token/成本/模型用量计入
	// “账号使用统计”(usage.json)，与 Gemini/claude 直连链路口径一致。
	// NVIDIA 中继链路在 recordNvidiaUsage 内调用它，使每个号池账号
	// (AccountMeta.ID/Email/Provider/ProjectID/ScopeType) 在前端账号使用统计页可见。
	usageTracker *stats.UsageTracker
	// globalStatsTracker 是全局 *stats.Tracker(主进程 handler 用的那个, 非 relay 的 StatsTracker)。
	// NVIDIA 中继链路在 recordNvidiaUsage 末尾用它把号池用量计入「使用趋势-NVIDIA」专用桶
	// (TrackNvidiaRequest), 与综合全局桶 trends 物理隔离。装配点在 app_relay.go 经
	// SetGlobalStatsTracker 注入; 未注入(如 relay 单测)时为 nil, recordNvidiaUsage 降级跳过,
	// 不影响既有 relay 维度统计。保持非构造参数 + setter 形式, 避免改动 NewAPICompatHandler
	// 签名牵连 7 处现有调用点与单测。
	globalStatsTracker *stats.Tracker
	logFn              func(string)
	client             *http.Client
	streamClient       *http.Client // 流式请求专用，不设全局超时，避免长生成被截断
	settingsMgr        settings.ManagerInterface
	rateLimiter        *RateLimiter
	// nvidiaCursor 是 round-robin 模式下用于打破"最少计数平局"的全局游标, 单调递增。
	// 历史上它是纯取模轮询游标; 接入 nvidiaStats 后退化为"候选集合内取模打破平局"用。
	nvidiaCursor uint64
	// nvidiaStats 是 NVIDIA 号池"每账号最近 1 分钟请求计数盘", 供选号时优先挑
	// 计数最少的账号, 把突发高并发洪流摊到负载最轻的账号上, 降低单账号 1 分钟内
	// >40 次必然 429 的概率。纯内存易失, 不持久化, 重启清零。详见 nvidia_counter.go。
	nvidiaStats *nvidiaReqStats
	// nvidiaStreamRetryWait 是 Anthropic 流式入站"上游断流服务端蓄流重试"的单次退避间隔,
	// 生产默认 5 秒(见 nvidia.go pullAnthropicStreamWithRetry);测试可覆盖为小值以快跑重试用例,
	// 避免 fix 的 5s×N 退避把单测拖到分钟级。零值时构造兜底为 5s。
	nvidiaStreamRetryWait time.Duration
	// nvidiaStreamCycleWait 是 Anthropic 流式断流"周期之间"的等待间隔:每个周期 = 5 次直连蓄流重试
	// + 1 次兜底出站代理,周期之间等本字段后把请求计数归零继续下一周期,连续 3 个周期(由 maxCycles 控制)
	// 全失败才回 overloaded_error 取消请求。生产默认 10 秒;测试可覆盖为小值避免把单测拖到 20s+。零值兜底 10s。
	nvidiaStreamCycleWait time.Duration
	// ocr 是入站 image 自愈降级的本地 Gemini OCR 引擎(L1),与号池入口解耦。
	// 由 OCRService 承载 cache(LRU + SQLite 持久化 + TTL)+ singleflight + 计数器,
	// 协议适配层(L2: DowngradeAnthropicImagesToText / DowngradeGeminiImagesToText /
	// DowngradeOpenAIChatImagesToText)挂在 OCRService 上,各号池入口只调 L2。
	// nil 时降级为无 OCR 能力(号池入口应在用前判空),保持单测与未注入场景兼容。
	ocr *OCRService
}

func NewAPICompatHandler(
	authMgr *AuthManager,
	accountMgr *account.Manager,
	sessionRouter *session.Router,
	statsTracker *StatsTracker,
	usageTracker *stats.UsageTracker,
	settingsMgr settings.ManagerInterface,
	logFn func(string),
) *APICompatHandler {
	return &APICompatHandler{
		authMgr:               authMgr,
		accountMgr:            accountMgr,
		sessionRouter:         sessionRouter,
		statsTracker:          statsTracker,
		usageTracker:          usageTracker,
		settingsMgr:           settingsMgr,
		logFn:                 logFn,
		client:                netutil.NewClient(5 * time.Minute),
		streamClient:          &http.Client{Transport: netutil.NewTransport(), Timeout: 0},
		rateLimiter:           NewRateLimiter(),
		nvidiaStats:           newNvidiaReqStats(),
		nvidiaStreamRetryWait: 5 * time.Second,
		nvidiaStreamCycleWait: 10 * time.Second,
		// ocr 引擎默认 cache 参数:容量 256 / 成功 TTL 24h / 失败 TTL 30s(见 ocr_cache.go)。
		// 与原 ocrCache: newOcrLRUCache(0,0,0) 等价;OCRService 内部自构 cache,
		// 并承接 settingsMgr / client / logFn,使 L1 与号池入口解耦。
		ocr: NewOCRService(settingsMgr, netutil.NewClient(5*time.Minute), logFn),
	}
}

// WireOcrRouteResolver 把 OCR 引擎的跨号池路由解析闭包绑定到 APICompatHandler.
// 注入后 OCRService 能按带前缀模型名(如 nvidia/xxx、other/openai/xxx)解析目标号池,
// 从而把 OCR 出站从纯 Google 家族(18443)扩展到其它号池多模态模型(18444 /route)。
// 在 NewAPICompatHandler 返回后由 caller 显式调用,避免构造器内循环引用。
func (h *APICompatHandler) WireOcrRouteResolver() {
	if h == nil || h.ocr == nil {
		return
	}
	h.ocr.SetRouteResolver(func(model string) (string, string, string, bool) {
		return h.resolveRoutedTarget(model)
	})
	// 同步注入模型映射查询闭包,供 OCR 降级闸 modelSupportsImage 做"配置优先"判定:
	// OCRService 不直接 handler,故用闭包捕获 getRelayModelMappingSafe(已含 recover 防 nil manager)。
	// 闭包把 settings.LookupModelMultimodalFlag 的 (*bool, found) 三态透传给降级闸,
	// 与 SetRouteResolver 同款解耦;relay 单测不调本方法即保持纯启发式旧行为。
	h.ocr.SetMappingResolver(func(model string) (*bool, bool) {
		return settings.LookupModelMultimodalFlag(h.getRelayModelMappingSafe(), model)
	})
}

// SetGlobalStatsTracker 注入全局 *stats.Tracker, 使 NVIDIA 中继链路能把号池用量计入
// 「使用趋势-NVIDIA」专用桶 (TrackNvidiaRequest)。仅 app_relay.go 装配处调用一次;
// relay 单测不调, 字段保持 nil, recordNvidiaUsage 自动降级跳过该落点, 不崩溃不误计。
func (h *APICompatHandler) SetGlobalStatsTracker(t *stats.Tracker) {
	if h == nil {
		return
	}
	h.globalStatsTracker = t
}

func (h *APICompatHandler) getModelMapping() []settings.ModelMappingEntry {
	if h.settingsMgr != nil {
		return h.settingsMgr.GetRelayModelMapping()
	}
	return settings.GetDefaultModelMappings()
}

func (h *APICompatHandler) log(format string, args ...interface{}) {
	if h.logFn != nil {
		h.logFn(fmt.Sprintf("[APICompat] "+format, args...))
	}
}

func (h *APICompatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// 校验 Token（支持 Authorization Bearer 和 X-API-Key 两种形式）
	token := extractToken(r)
	if token == "" {
		h.log("🔑 Authentication failed: missing API Key / Token in request headers (URL: %s)", path)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "missing API Key"})
		return
	}

	h.log("📥 [请求接入] %s %s | Token: %s", r.Method, path, token)

	session, err := h.authMgr.ValidateToken(token)
	if err != nil {
		h.log("🔑 Authentication failed: invalid API Key %q: %v (URL: %s)", token, err, path)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid API Key: " + err.Error()})
		return
	}

	// 校验速率限制 (每分钟最多请求次数，默认为30次)
	if r.Method == http.MethodPost && (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/responses" || path == "/responses/compact" || path == "/v1/messages") {
		limit := 30
		user := h.authMgr.userMgr.GetUserByID(session.UserID)
		if user != nil && user.Quotas.RateLimit > 0 {
			limit = user.Quotas.RateLimit
		}
		if !h.rateLimiter.Allow(session.UserID, limit) {
			h.log("🚦 Rate limit exceeded for user %s (%d requests/min)", session.UserKey, limit)
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error": map[string]interface{}{
					"message": fmt.Sprintf("Rate limit exceeded. Maximum %d requests per minute.", limit),
					"type":    "rate_limit_error",
					"code":    "rate_limit_exceeded",
				},
			})
			return
		}
	}

	// 1. 模型列表接口
	if path == "/v1/models" && r.Method == http.MethodGet {
		h.handleModels(w, r)
		return
	}

	// 2. OpenAI 对话接口 (兼容 Codex 等客户端调用的 /v1/responses, /responses, /responses/compact 路径)
	if (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/responses" || path == "/responses/compact") && r.Method == http.MethodPost {
		h.handleOpenAIChat(w, r, session)
		return
	}

	// 3. Anthropic 对话接口
	if path == "/v1/messages" && r.Method == http.MethodPost {
		h.handleAnthropicMessages(w, r, session)
		return
	}

	// 4. NVIDIA 专属号池接口 (/nvidia/v1/models, /nvidia/v1/chat/completions, /nvidia/v1/messages, 以及 /vc/* 别名路由)
	// /vc 作为 /nvidia 的纯别名前缀,分发到同一 handleNvidia 链路;前缀精确化经
	// nvidiaAliasPrefixMatch 收敛(排除 /vcard 等紧跟非斜杠字符的误吞路径),见 nvidiaPathPrefix.go。
	if nvidiaAliasPrefixMatch(path) {
		h.handleNvidia(w, r, session)
		return
	}

	// 6. 通用按模型路由入口 /route/*:按入站 model 匹配 RelayModelRoutes 规则,
	// 分发到对应 Provider 号池(复用 handleNvidia 的重特化链路,或走通用透传转发器)。
	// 入口由 router_entry.go 实现,这里只做前缀分流。
	if routedRoutePrefixMatch(path) {
		h.handleRoutedForward(w, r, session)
		return
	}

	// 5. v1internal 接口 (支持 /v1internal:generateContent 或 /v1internal:streamGenerateContent)
	if strings.HasPrefix(path, "/v1internal:") && r.Method == http.MethodPost {
		h.handleV1Internal(w, r, session)
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "endpoint not found"})
}
