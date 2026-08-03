package relay

// compat.go: relay 层兼容层主入口 —— APICompatHandler 结构体定义 + 构造函数 + ServeHTTP 路由分发。
// 原单文件 2064 行已按职责拆分,本文件只保留结构体/构造/路由主干与其直接依赖的工具变量,
// 各业务 handler 按职责拆到卫星文件(同包内共享符号,逻辑逐行等价,零回归):
//   compat_models.go        /v1/models 模型列表 handler
//   compat_dispatch.go      OpenAI/Anthropic 入口 handler + dispatchToGemini 分发
//   compat_response.go      Gemini 非流式响应 -> 客户端协议回译
//   compat_stream.go        Gemini 流式 SSE -> 客户端协议流式 SSE 回译
//   compat_v1internal.go    /v1internal:* 桥接 + mapModelForProjectInRelay
//   compat_ocr.go           入站 image 本地 Gemini OCR 降级(已在 Step 2-b 拆出)
//   compat_ratelimit.go     RateLimiter 限流器(已在 Step 2-b 拆出)

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"golang.org/x/sync/singleflight"
)

var localProxyAddr = "127.0.0.1:18443"


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
	usageTracker  *stats.UsageTracker
	// globalStatsTracker 是全局 *stats.Tracker(主进程 handler 用的那个, 非 relay 的 StatsTracker)。
	// NVIDIA 中继链路在 recordNvidiaUsage 末尾用它把号池用量计入「使用趋势-NVIDIA」专用桶
	// (TrackNvidiaRequest), 与综合全局桶 trends 物理隔离。装配点在 app_relay.go 经
	// SetGlobalStatsTracker 注入; 未注入(如 relay 单测)时为 nil, recordNvidiaUsage 降级跳过,
	// 不影响既有 relay 维度统计。保持非构造参数 + setter 形式, 避免改动 NewAPICompatHandler
	// 签名牵连 7 处现有调用点与单测。
	globalStatsTracker *stats.Tracker
	logFn         func(string)
	client        *http.Client
	streamClient  *http.Client // 流式请求专用，不设全局超时，避免长生成被截断
	settingsMgr   settings.ManagerInterface
	rateLimiter   *RateLimiter
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
	// ocrCache 是 NVIDIA/Gemini 入站 image 降级时,本地 Gemini OCR 结果的进程内缓存。
	// 键 = UserKey|ocrModel|sha256(b64)[:16],LRU 限条数 + TTL,叠加 singleflight 防并发击穿。
	// 解决 Claude Code 无状态每轮重发历史图导致同一张图被重复 OCR 的浪费(每图 ~3s + 一次号池额度)。
	// nil 时降级为零缓存(纯内存命中),保持旧行为兼容,供单测与未注入场景使用。
	ocrCache *ocrLRUCache
	// ocrInflight 管理同图并发的 singleflight,键同 ocrCache。Do(key, fn) 按 key 串行化,
	// 同图 N 路并发只真打上游 1 次,其余阻塞等待结果共享,防缓存击穿。
	ocrInflight singleflight.Group
	// ocrCounters 统计 OCR 缓存命中/未命中,供日志显示降级收益。计数走 atomic 不进 LRU 锁。
	ocrCounters ocrCacheCounters
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
		authMgr:       authMgr,
		accountMgr:    accountMgr,
		sessionRouter: sessionRouter,
		statsTracker:  statsTracker,
		usageTracker:  usageTracker,
		settingsMgr:   settingsMgr,
		logFn:         logFn,
		client:        netutil.NewClient(5 * time.Minute),
		streamClient:  &http.Client{Transport: netutil.NewTransport(), Timeout: 0},
		rateLimiter:   NewRateLimiter(),
		nvidiaStats:   newNvidiaReqStats(),
		nvidiaStreamRetryWait: 5 * time.Second,
		// ocrCache 默认参数:容量 256 / 成功 TTL 30min / 失败 TTL 30s。
		// 容量按"每用户活跃历史图"估算,256 足够覆盖一段长会话的不重复图;
		// 失败短 TTL 30s 熔断窗口,避免持续重打挂的 OCR 服务。
		ocrCache: newOcrLRUCache(0, 0, 0),
	}
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

	// 5. v1internal 接口 (支持 /v1internal:generateContent 或 /v1internal:streamGenerateContent)
	if strings.HasPrefix(path, "/v1internal:") && r.Method == http.MethodPost {
		h.handleV1Internal(w, r, session)
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "endpoint not found"})
}


