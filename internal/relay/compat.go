package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/sigcache"
	"antigravity-proxy/internal/stats"

	"golang.org/x/sync/singleflight"
)

var localProxyAddr = "127.0.0.1:18443"

// defaultOcrModel 是入站 image 自愈降级时本地 Gemini OCR 模型的默认值。
// 与 settings.DefaultOcrModel 同值,供 settingsMgr==nil(relay 单测未注入)时兜底,保持旧行为。
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

// getOcrModel 读取入站 image 自愈降级使用的本地 Gemini OCR 模型名。
// settingsMgr 为 nil(relay 单测未注入)时回退 defaultOcrModel,保持旧行为;
// 配置空值时 settings 层已兜底,这里双保险再判一次。
func (h *APICompatHandler) getOcrModel() string {
	if h == nil || h.settingsMgr == nil {
		return defaultOcrModel
	}
	m := strings.TrimSpace(h.settingsMgr.GetOcrModel())
	if m == "" {
		return defaultOcrModel
	}
	return m
}

// ocrImageViaLocalGemini 调用本地 Gemini(默认 gemini-2.5-flash,前端可配)对一张 base64 图做 OCR,
// 返回识别出的纯文本。失败返回 "" + error。
//
// 抽出供两条入站链路复用,避免各写一份:
//   - Gemini 入站自愈(dispatchToGemini 内,目标模型非 gemini 时图转文)
//   - NVIDIA 入站 image 降级(handleNvidia 内,Anthropic image block 本地 OCR 抹成 text)
//
// 缓存策略(ocrCache + singleflight):
//   - 键 = UserKey|ocrModel|sha256(b64)[:16],按用户与 OCR 模型双重隔离;
//   - 命中即返回历史 OCR 文本,跳过 gemini 调用与 ~3s 延迟;
//   - miss 时 singleflight 合并同图并发为 1 次真上游调用,防缓存击穿;
//   - OCR 失败也缓存(短 TTL),熔断窗口内不再重打挂的 OCR 服务;
//   - 切换 ocrModel 后键变化,自动重新 OCR 新模型(配置改了立刻生效)。
// nil ocrCache 降级为零缓存(纯走上游),保持旧行为兼容,供单测或异常注入用。
//
// 返回的 cachedHit 标识本次结果是否来自缓存命中(true=命中即返,未触达上游;
// false=cache miss 真打了一次 gemini 上游,或 nil 缓存的纯走上游场景)。
// 供调用方(downgradeAnthropicImagesToText / dispatchToGemini)在日志里透出
// "本轮这张图是命中还是重新 OCR",消除"每次请求都打印 OCR 降级"日志的歧义。
func (h *APICompatHandler) ocrImageViaLocalGemini(userSession *RelaySession, b64Data string, mimeType string, userPromptText ...string) (text string, err error, cachedHit bool) {
	if h == nil || userSession == nil {
		return "", fmt.Errorf("ocrImageViaLocalGemini: nil handler or session"), false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", fmt.Errorf("ocrImageViaLocalGemini: empty image data"), false
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	ocrModel := h.getOcrModel()

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	// 缓存键首维:会话级隔离键(sessionKey 非空时优先,粒度比 UserKey 更细,按会话隔离;
	// 空则回退 UserKey,保持单测/未传场景的旧行为兼容)。sessionKey 由调用方(handleNvidia /
	// dispatchToGemini)经 ExtractSessionKey 算出,与 antigravity 链路同款口径,使同用户不同会话
	// 不共享缓存槽,且日志里能看到一致的会话 ID。
	ownerKey := ocrOwnerKey(userSession)

	// 命中缓存直接返回,跳过 gemini 调用与 ~3s 延迟(含失败条目短 TTL 熔断)。
	if h.ocrCache != nil {
		key := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
		if e, ok := h.ocrCache.get(key); ok {
			h.ocrCounters.hits.Add(1)
			return e.text, e.err, true
		}
		h.ocrCounters.misses.Add(1)
	}

	// singleflight:同步相邻并发对同图(同模型)的请求,首调用真打上游,其余阻塞等待结果共享。
	callKey := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
	v, callErr, _ := h.ocrInflight.Do(callKey, func() (interface{}, error) {
		text, err := h.ocrImageViaLocalGeminiUncached(userSession, b64Data, mimeType, ocrModel, promptCtx)
		ok := err == nil && strings.TrimSpace(text) != ""
		if h.ocrCache != nil {
			// 成功长 TTL / 失败短 TTL;失败也缓存命中即返回 err,避免熔断窗口内重打挂的服务。
			cachedText := text
			if !ok {
				cachedText = ""
			}
			h.ocrCache.set(callKey, cachedText, err, ok)
		}
		return text, err
	})
	if callErr != nil {
		return "", callErr, false
	}
	text, _ = v.(string)
	return text, nil, false
}

// ocrOwnerKey 返回 OCR 缓存键的首维隔离键:会话级 sessionKey 优先,空则回退 UserKey。
// 抽出便于 ocrImageViaLocalGemini 与 ocrImageCacheOnlyLookup 共享一致的取键口径。
// 设计:sessionKey 非空 → 按会话隔离(同用户多会话不共享缓存,语义更准且与日志会话 ID 对齐);
//      sessionKey 空 → 回退 UserKey(单测与未传 sessionKey 的旧调用,行为不变)。
func ocrOwnerKey(userSession *RelaySession) string {
	if userSession == nil {
		return ""
	}
	if strings.TrimSpace(userSession.SessionKey) != "" {
		return userSession.SessionKey
	}
	return userSession.UserKey
}

// ocrSessionDisplay 返回日志里展示的会话 ID:优先 userSession.SessionKey(由 handleNvidia 入口
// 经 ExtractSessionKey + auth:acc: 前缀算出,与 antigravity 号池链路同款口径),空则回退 UserID,
// 再空则 "-"。供 NVIDIA 选号/降级日志透出"哪个会话在打",便于排查同用户多会话的缓存隔离与配额归属。
func ocrSessionDisplay(userSession *RelaySession) string {
	if userSession == nil {
		return "-"
	}
	if k := strings.TrimSpace(userSession.SessionKey); k != "" {
		return k
	}
	if u := strings.TrimSpace(userSession.UserID); u != "" {
		return u
	}
	return "-"
}

// ocrImageCacheOnlyLookup 仅查 OCR 缓存,命中返回历史 OCR 文本(true),未命中返回("",false)。
// 绝不触达 singleflight / gemini 上游,供"最近 N 条消息窗口"之外的图片块复用:
//   - 命中(图在窗口内 OCR 过且仍驻留 LRU)→ 复用历史文本,不烧 antigravity 配额;
//   - 未命中 → 调用方写 imageNotExtractablePlaceholder 占位文本,绝不重新 OCR。
// 与 ocrImageViaLocalGemini 共享 ownerKey(会话级)+ ocrModel + 图指纹 + promptCtx 四维键,
// 故窗口内 OCR 入缓存的图,被推出窗口后仍可被本方法命中复用。
func (h *APICompatHandler) ocrImageCacheOnlyLookup(userSession *RelaySession, b64Data string, userPromptText ...string) (string, bool) {
	if h == nil || userSession == nil || h.ocrCache == nil {
		return "", false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", false
	}
	ocrModel := h.getOcrModel()
	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}
	key := ocrCacheKey(ocrOwnerKey(userSession), ocrModel, b64Data, promptCtx)
	if e, ok := h.ocrCache.get(key); ok && e.ok && strings.TrimSpace(e.text) != "" {
		// 仅复用成功条目;失败短 TTL 条目不在此复用(让调用方走占位,语义更清晰)。
		h.ocrCounters.hits.Add(1)
		return e.text, true
	}
	return "", false
}

// ocrImageViaLocalGeminiUncached 是 ocrImageViaLocalGemini 的纯上游调用实现,
// 无缓存、无 singleflight,纯粹把 base64 发给 18443 的指定 Gemini 模型跑 OCR。
// 抽出来便于 (a) 缓存层 miss 后复用 (b) 单测直接打 mock 校验上游请求形态。
// ocrModel 由调用方传入(取自 h.getOcrModel()),用于动态拼写 18443 URL,默认 gemini-2.5-flash。
func (h *APICompatHandler) ocrImageViaLocalGeminiUncached(userSession *RelaySession, b64Data string, mimeType string, ocrModel string, userPromptText ...string) (string, error) {
	if strings.TrimSpace(ocrModel) == "" {
		ocrModel = defaultOcrModel
	}

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	var ocrPrompt string
	if promptCtx != "" {
		ocrPrompt = fmt.Sprintf("你是一个顶级的多模态视觉分析助手。请深入分析图片内容并准确提取关键信息。\n\n【用户提问上下文】：用户在发送此图片时附带的提问/说明文本为：\n\"%s\"\n\n请按以下要求分析：\n1. [重点靶向分析]：结合上述用户的提问与关注点，重点分析图片中与问题相关的代码行、报错提示、界面元素或逻辑关系。\n2. [文字与代码精准提取 (OCR)]：\n   - 原样逐字提取图中涉及的代码、控制台报错或文本，保持原始缩进与排版，不要自动修正错别字，用 Markdown 代码块包裹。\n3. [画面结构与视觉布局]：描述界面 UI 状态、高亮提示框或图表节点关系。\n4. [输出要求]：直接输出结构化结果，严禁包含任何前言或客套话。", promptCtx)
	} else {
		ocrPrompt = "你是一个顶级的多模态视觉分析助手。请深入分析图片内容并准确提取关键信息。要求如下：\n1. [图像总体概览]：用一句话概括图片类型（如：IDE代码截图、控制台报错、UI界面、架构流程图等）及核心意图。\n2. [文字与代码精准提取 (OCR)]：\n   - 提取图中所有的文本、代码、终端命令与报错堆栈。\n   - 代码与报错日志必须【原样逐字提取】，严格保留缩进、换行与标点符号，严禁自动修改拼写错误。使用 Markdown 代码块包裹。\n3. [视觉布局与逻辑关系]：\n   - 若包含 UI 界面，注明高亮项、报错弹窗或按钮状态；\n   - 若包含流程图/表格，还原节点间的关系或表格数据。\n4. [输出要求]：直接输出结构化的提取与分析结果，不要包含任何前言、引言或客套话。"
	}

	ocrReq := GeminiRequest{
		Contents: []GeminiContent{
			{
				Role: "user",
				Parts: []GeminiPart{
					{Text: ocrPrompt},
					{InlineData: &GeminiBlob{MimeType: mimeType, Data: b64Data}},
				},
			},
		},
	}
	ocrReqBytes, errMarshal := json.Marshal(ocrReq)
	if errMarshal != nil {
		return "", fmt.Errorf("marshal ocr request: %w", errMarshal)
	}

	// 模型名参数化:默认 gemini-2.5-flash,前端可改任意 Gemini 系模型。
	ocrURL := fmt.Sprintf("http://%s/v1beta/models/%s:generateContent", localProxyAddr, ocrModel)
	ocrHTTPReq, errReq := http.NewRequest(http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return "", fmt.Errorf("create ocr request: %w", errReq)
	}
	ocrHTTPReq.Header.Set("Content-Type", "application/json")
	ocrHTTPReq.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	ocrHTTPReq.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		ocrHTTPReq.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Path", "/v1internal:generateContent/ocr-fallback")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Method", "POST")

	ocrResp, errDo := h.client.Do(ocrHTTPReq)
	if errDo != nil {
		return "", fmt.Errorf("execute ocr request: %w", errDo)
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return "", fmt.Errorf("ocr service returned status %d: %s", ocrResp.StatusCode, string(errBytes))
	}

	respBytes, _ := io.ReadAll(ocrResp.Body)
	var gemResp GeminiResponse
	if errUnmarshal := json.Unmarshal(respBytes, &gemResp); errUnmarshal != nil {
		return "", fmt.Errorf("unmarshal ocr response: %w", errUnmarshal)
	}
	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("ocr response candidates are empty")
	}
	return gemResp.Candidates[0].Content.Parts[0].Text, nil
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

	// 4. NVIDIA 专属号池接口 (/nvidia/v1/models, /nvidia/v1/chat/completions, /nvidia/v1/messages)
	if strings.HasPrefix(path, "/nvidia") {
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

func (h *APICompatHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	var supportedModels []string
	for _, entry := range h.getModelMapping() {
		if entry.Expose {
			supportedModels = append(supportedModels, entry.ClientModel)
		}
	}
	if len(supportedModels) == 0 {
		for _, entry := range settings.GetDefaultModelMappings() {
			if entry.Expose {
				supportedModels = append(supportedModels, entry.ClientModel)
			}
		}
	}

	isAnthropic := r.Header.Get("anthropic-version") != "" ||
		strings.Contains(r.Header.Get("User-Agent"), "Anthropic") ||
		(strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(r.URL.Path, "messages"))

	if isAnthropic {
		var data []map[string]interface{}
		for _, m := range supportedModels {
			data = append(data, map[string]interface{}{
				"type":         "model",
				"id":           m,
				"display_name": strings.Title(strings.ReplaceAll(m, "-", " ")),
				"created_at":   "2024-05-14T00:00:00Z",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"data":     data,
			"has_more": false,
		})
	} else {
		var data []map[string]interface{}
		for _, m := range supportedModels {
			data = append(data, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"created":  1715644800,
				"owned_by": "google",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   data,
		})
	}
}

func (h *APICompatHandler) handleOpenAIChat(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	r.Body.Close()

	openReq, err := ParseUnifiedOpenAIRequest(bodyBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid openai request: " + err.Error()})
		return
	}

	geminiModel := MapClientModelToGemini(openReq.Model, h.getModelMapping())
	geminiReq := TranslateOpenAIToGemini(openReq)

	h.log("OpenAI Request mapped. ClientModel: %s -> GeminiModel: %s | User: %s", openReq.Model, geminiModel, userSession.UserKey)

	apiFormat := "openai"
	if strings.Contains(r.URL.Path, "responses") {
		// Codex's /v1/responses requires OpenAI Responses API stream format
		apiFormat = "responses"
	}

	// 终极精确拦截：通过底层协议头与特定负载指纹，完美区分后台心跳/预生成与用户真实请求
	isCodexProbe := false
	if strings.Contains(strings.ToLower(openReq.Model), "gpt-5.4") {
		// 1. 【协议头指纹】严格拦截所有 Codex 引擎自发的后台线程（非人类主动提问）
		turnMetadata := r.Header.Get("X-Codex-Turn-Metadata")
		if strings.Contains(turnMetadata, `"thread_source":"system"`) {
			isCodexProbe = true
		}

		// 2. 【负载指纹兜底】精确拦截 Codex 偷偷生成的个性化推荐探测和其他后台任务
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "hyperpersonalized suggestions") {
			isCodexProbe = true
		}
	}

	if isCodexProbe {
		if openReq.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)

			if apiFormat == "responses" {
				txtDone := map[string]interface{}{
					"type":            "response.output_text.done",
					"sequence_number": 0,
					"item_id":         "mock_heartbeat_msg",
					"output_index":    0,
					"content_index":   0,
					"text":            "Ready",
				}
				txtBytes, _ := json.Marshal(txtDone)
				fmt.Fprintf(w, "event: response.output_text.done\ndata: %s\n\n", string(txtBytes))

				partDone := map[string]interface{}{
					"type":            "response.content_part.done",
					"sequence_number": 1,
					"item_id":         "mock_heartbeat_msg",
					"output_index":    0,
					"content_index":   0,
					"part": map[string]interface{}{
						"type": "output_text",
						"text": "Ready",
					},
				}
				partBytes, _ := json.Marshal(partDone)
				fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(partBytes))

				itemMsg := map[string]interface{}{
					"id":      "mock_heartbeat_msg",
					"type":    "message",
					"status":  "completed",
					"role":    "assistant",
					"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "Ready"}},
				}
				itemDone := map[string]interface{}{
					"type":            "response.output_item.done",
					"sequence_number": 2,
					"output_index":    0,
					"item":            itemMsg,
				}
				itemBytes, _ := json.Marshal(itemDone)
				fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemBytes))

				completedEvt := map[string]interface{}{
					"type":            "response.completed",
					"sequence_number": 3,
					"response": map[string]interface{}{
						"id":         "mock_heartbeat_resp",
						"object":     "response",
						"created_at": time.Now().Unix(),
						"status":     "completed",
						"usage": map[string]interface{}{
							"input_tokens":  10,
							"output_tokens": 10,
							"total_tokens":  20,
						},
						"output": []interface{}{itemMsg},
					},
				}
				completedBytes, _ := json.Marshal(completedEvt)
				fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(completedBytes))
			} else {
				finalChunk := OpenAIStreamChunk{
					ID:      "mock_heartbeat_resp",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   openReq.Model,
					Choices: []OpenAIStreamChoice{
						{Index: 0, Delta: OpenAIDelta{Content: "Ready"}, FinishReason: "stop"},
					},
				}
				finalBytes, _ := json.Marshal(finalChunk)
				fmt.Fprintf(w, "data: %s\n\n", string(finalBytes))
				fmt.Fprintf(w, "data: [DONE]\n\n")
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}

		// 非流式情况
		if apiFormat == "responses" {
			itemMsg := map[string]interface{}{
				"id":      "mock_heartbeat_msg",
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "Ready"}},
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id":         "mock_heartbeat_resp",
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  10,
					"output_tokens": 10,
					"total_tokens":  20,
				},
				"output": []interface{}{itemMsg},
			})
		} else {
			resp := OpenAIResponse{
				ID:      "mock_heartbeat_resp",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   openReq.Model,
				Choices: []OpenAIResponseChoice{
					{Index: 0, Message: OpenAIMessage{Role: "assistant", Content: "Ready"}, FinishReason: "stop"},
				},
				Usage: OpenAIResponseUsage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20},
			}
			writeJSON(w, http.StatusOK, resp)
		}
		return
	}

	h.dispatchToGemini(w, r, userSession, openReq.Model, geminiModel, geminiReq, openReq.Stream, apiFormat)
}

func (h *APICompatHandler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	var anthReq AnthropicRequest
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read body"})
		return
	}
	r.Body.Close()

	if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid anthropic request: " + err.Error()})
		return
	}

	geminiModel := MapClientModelToGemini(anthReq.Model, h.getModelMapping())
	geminiReq := TranslateAnthropicToGemini(&anthReq)

	h.log("Anthropic Request mapped. ClientModel: %s -> GeminiModel: %s | User: %s", anthReq.Model, geminiModel, userSession.UserKey)

	h.dispatchToGemini(w, r, userSession, anthReq.Model, geminiModel, geminiReq, anthReq.Stream, "anthropic")
}

func (h *APICompatHandler) dispatchToGemini(
	w http.ResponseWriter,
	r *http.Request,
	userSession *RelaySession,
	clientModel string,
	geminiModel string,
	geminiReq *GeminiRequest,
	stream bool,
	apiFormat string,
) {
	startTime := time.Now()

	// 1. 获取会话 Key
	tempBytesForSession, _ := json.Marshal(geminiReq)
	sessionKey := h.sessionRouter.ExtractSessionKey(r, tempBytesForSession)

	// 调用优化器执行压缩与模型路由降级
	targetModelToQuery, compressed := CheckAndOptimizeSession(
		r,
		geminiReq,
		geminiModel,
		sessionKey,
		userSession.UserKey,
		userSession.UserID,
		userSession.APIKeyID,
		h.client,
		h.settingsMgr,
		func(msg string) {
			h.log("%s", msg)
		},
	)
	if compressed {
		h.log("✅ [Relay Compat] 会话压缩成功，请求体已优化")
	}

	// 假多模态转换自愈逻辑：检测当非 Gemini 模型遇到多模态图片时，自动调用本地多模态模型执行 OCR 转换
	hasImage := false
	for _, c := range geminiReq.Contents {
		for _, p := range c.Parts {
			if p.InlineData != nil && p.InlineData.Data != "" {
				hasImage = true
				break
			}
		}
		if hasImage {
			break
		}
	}

	if hasImage && !strings.Contains(strings.ToLower(targetModelToQuery), "gemini") {
		// ocrModel 与降级链路同源(h.getOcrModel()),保证自愈文案与真实调用的 OCR 模型一致。
		ocrModel := h.getOcrModel()
		h.log("⚠️ [Relay Compat] 检测到目标模型 %s 不支持多模态，但请求包含图片。正在自动通过本地 Gemini(%s)执行 OCR 和图片描述...", targetModelToQuery, ocrModel)

		for i, c := range geminiReq.Contents {
			var userTextBuilder strings.Builder
			for _, p := range c.Parts {
				if p.Text != "" {
					if userTextBuilder.Len() > 0 {
						userTextBuilder.WriteString("\n")
					}
					userTextBuilder.WriteString(p.Text)
				}
			}
			userPromptCtx := userTextBuilder.String()

			for j, p := range c.Parts {
				if p.InlineData == nil || p.InlineData.Data == "" {
					continue
				}
				mime := p.InlineData.MimeType
				if mime == "" {
					mime = "image/jpeg"
				}
				ocrText, ocrErr, cachedHit := h.ocrImageViaLocalGemini(userSession, p.InlineData.Data, mime, userPromptCtx)
				if ocrErr != nil {
					h.log("❌ [Relay Compat] OCR 调用失败(字节 %d): %v", len(p.InlineData.Data), ocrErr)
					continue
				}
				if strings.TrimSpace(ocrText) == "" {
					h.log("⚠️ [Relay Compat] OCR 响应 candidates 为空")
					continue
				}
				h.log("✅ [Relay Compat] 图片 OCR 转换成功。字节大小: %d | 识别出字符数: %d | 缓存命中: %t", len(p.InlineData.Data), len(ocrText), cachedHit)

				// 文案模型名随 ocrModel 参数化,与 nvidiaImageOcrDescHeader 共享语义。
				descHeader := fmt.Sprintf("\n\n[本地中继服务已自动调用 %s 协助分析了用户发送的截图，内容提取如下：]\n%s\n[图片分析内容结束]\n", ocrModel, ocrText)

				// 把这个 part 里的图片清除，变成一个纯文本 part 并拼接识别到的内容
				geminiReq.Contents[i].Parts[j].InlineData = nil
				if geminiReq.Contents[i].Parts[j].Text != "" {
					geminiReq.Contents[i].Parts[j].Text += descHeader
				} else {
					geminiReq.Contents[i].Parts[j].Text = descHeader
				}
			}
		}
	}

	geminiReqBytes, err := json.Marshal(geminiReq)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to marshal gemini request"})
		return
	}

	// 调试日志：输出请求体摘要
	var roleSeq []string
	for _, c := range geminiReq.Contents {
		partTypes := ""
		for _, p := range c.Parts {
			if p.Text != "" {
				partTypes += "T"
			}
			if p.FunctionCall != nil {
				partTypes += "FC(" + p.FunctionCall.Name + ")"
			}
			if p.FunctionResponse != nil {
				partTypes += "FR(" + p.FunctionResponse.Name + ")"
			}
		}
		roleSeq = append(roleSeq, fmt.Sprintf("%s[%s]", c.Role, partTypes))
	}
	// 统计工具使用情况
	toolCount := 0
	if len(geminiReq.Tools) > 0 {
		toolCount = len(geminiReq.Tools[0].FunctionDeclarations)
	}
	h.log("📋 [调试] 请求体: %d 条消息 | 角色序列: %v | 工具数: %d | 体积: %d bytes",
		len(geminiReq.Contents), roleSeq, toolCount, len(geminiReqBytes))

	// 准备向本地核心代理服务 (18443 端口) 发起请求以复用成熟 of 账号池分发与自动重试逻辑
	action := "generateContent"
	queryStr := ""
	if stream {
		action = "streamGenerateContent"
		queryStr = "?alt=sse"
	}

	targetURL := fmt.Sprintf("http://%s/v1beta/models/%s:%s%s", localProxyAddr, targetModelToQuery, action, queryStr)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(geminiReqBytes))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")
	// 将用户的凭证传递给本地代理，本地代理将据此提取 sessionKey 自动粘性绑定账号池并执行扣费统计
	req.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	req.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	req.Header.Set("X-Antigravity-Original-Path", r.URL.Path)
	req.Header.Set("X-Antigravity-Original-Method", r.Method)
	h.log("Forwarding translated request to local proxy (18443) | Model: %s | Stream: %v", targetModelToQuery, stream)

	// 流式请求使用无超时 Client，避免长时间生成（>5min）被 http.Client.Timeout 截断
	httpClient := h.client
	if stream {
		httpClient = h.streamClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		h.log("❌ Failed to query local proxy: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to query local proxy: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		h.log("❌ Local proxy returned status %d: %s", resp.StatusCode, string(respBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	reqID := r.Header.Get("X-Antigravity-Req-ID")

	// 4. 流式传输（SSE）处理
	if stream {
		h.handleStreamResponse(r.Context(), w, resp.Body, userSession, clientModel, geminiModel, apiFormat, startTime, r.URL.Path, reqID)
	} else {
		// 5. 非流式传输处理
		h.handleNormalResponse(w, resp.Body, userSession, geminiModel, apiFormat, startTime, r.URL.Path, reqID)
	}
}

func removeAccountFromList(list []*account.Account, accountID string) []*account.Account {
	var result []*account.Account
	for _, a := range list {
		if a.ID != accountID {
			result = append(result, a)
		}
	}
	return result
}

func (h *APICompatHandler) handleNormalResponse(
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
	data, err := io.ReadAll(respBody)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to read google response"})
		return
	}

	var gemResp GeminiResponse
	if err := json.Unmarshal(data, &gemResp); err != nil {
		// 可能是被强制转换成了 SSE 流式响应 (如 antigravity 强制路由至 streamGenerateContent)
		if strings.Contains(string(data), "data: ") {
			var fullText string
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "data: ") {
					dataStr := strings.TrimPrefix(line, "data: ")
					if dataStr == "[DONE]" {
						continue
					}
					var chunk GeminiResponse
					if errChunk := json.Unmarshal([]byte(dataStr), &chunk); errChunk == nil {
						if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
							fullText += chunk.Candidates[0].Content.Parts[0].Text
						}
						if chunk.UsageMetadata.PromptTokenCount > 0 {
							gemResp.UsageMetadata.PromptTokenCount = chunk.UsageMetadata.PromptTokenCount
						}
						if chunk.UsageMetadata.CandidatesTokenCount > 0 {
							gemResp.UsageMetadata.CandidatesTokenCount = chunk.UsageMetadata.CandidatesTokenCount
						}
					}
				}
			}
			gemResp.Candidates = []GeminiCandidate{
				{Content: GeminiCandidateContent{Parts: []GeminiPart{{Text: fullText}}, Role: "model"}},
			}
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to parse google response: " + string(data)})
			return
		}
	}

	// 提取回复内容(thought 思考 / text 正文 + functionCall)与用量。
	// thought:true 的 part 文本是思考内容,必须与正文分离,不能混入 text——
	// 旧实现非流式路径忽略 part.Thought,把思考文本当正文回译,导致 Claude Code/Codex
	// 非流式请求把思考当正文显示(D)。此处把 thought 单独累积,后续按 apiFormat 独立输出。
	var contentBlocks []AnthropicContent
	var thinkingText strings.Builder
	hasFunctionCall := false
	if len(gemResp.Candidates) > 0 {
		for _, part := range gemResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				if part.Thought {
					// 思考内容单独累积,不进正文 contentBlocks
					thinkingText.WriteString(part.Text)
					continue
				}
				contentBlocks = append(contentBlocks, AnthropicContent{Type: "text", Text: part.Text})
			}
			if part.FunctionCall != nil {
				hasFunctionCall = true
				contentBlocks = append(contentBlocks, AnthropicContent{
					Type:  "tool_use",
					ID:    generateToolUseID(),
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				})
			}
		}
	}
	if len(contentBlocks) == 0 && thinkingText.Len() == 0 {
		contentBlocks = []AnthropicContent{{Type: "text", Text: ""}}
	}

	inTokens := gemResp.UsageMetadata.PromptTokenCount
	outTokens := gemResp.UsageMetadata.CandidatesTokenCount

	// 根据要求的 API 格式，翻译响应包
	if apiFormat == "openai" {
		replyText := ""
		var toolCalls []OpenAIToolCall
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			} else if b.Type == "tool_use" {
				argsJSON, _ := json.Marshal(b.Input)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   b.ID,
					Type: "function",
					Function: OpenAIToolCallFunction{
						Name:      b.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}
		openResp := OpenAIResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", rand.Int63()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   geminiModel,
			Choices: []OpenAIResponseChoice{
				{
					Index: 0,
					Message: OpenAIMessage{
						Role:      "assistant",
						Content:   replyText,
						ToolCalls: toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: OpenAIResponseUsage{
				PromptTokens:     inTokens,
				CompletionTokens: outTokens,
				TotalTokens:      inTokens + outTokens,
			},
		}
		writeJSON(w, http.StatusOK, &openResp)
	} else if apiFormat == "responses" {
		replyText := ""
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			}
		}
		respID := fmt.Sprintf("resp_%d", rand.Int63())
		// output 条目:若有思考,先插一个 reasoning message item(独立 output_index),
		// 再跟正文 message item。与流式路径 reasoning 独立 item 语义一致(B)。
		var outputItems []interface{}
		outIdx := 0
		if thinkingText.Len() > 0 {
			outputItems = append(outputItems, map[string]interface{}{
				"id":      fmt.Sprintf("msg_%s_r0", respID),
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": thinkingText.String()}},
			})
			outIdx = 1
		}
		outputItems = append(outputItems, map[string]interface{}{
			"id":      fmt.Sprintf("msg_%s_%d", respID, outIdx),
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "output_text", "text": replyText}},
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":         respID,
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": outputItems,
			},
		})
	} else { // anthropic
		// 若有思考内容,在正文前插入 thinking 块(对齐流式路径 thinking_delta + 空串 signature)。
		// thinking 块的 index 在 content 数组顺序中自然处于 text 块之前,符合 Anthropic 规范。
		var finalBlocks []AnthropicContent
		if thinkingText.Len() > 0 {
			finalBlocks = append(finalBlocks, AnthropicContent{
				Type:      "thinking",
				Thinking:  thinkingText.String(),
				Signature: "",
			})
		}
		finalBlocks = append(finalBlocks, contentBlocks...)
		if len(finalBlocks) == 0 {
			finalBlocks = []AnthropicContent{{Type: "text", Text: ""}}
		}
		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}
		anthResp := AnthropicResponse{
			ID:           fmt.Sprintf("msg_%d", rand.Int63()),
			Type:         "message",
			Role:         "assistant",
			Content:      finalBlocks,
			Model:        geminiModel,
			StopReason:   stopReason,
			StopSequence: nil,
			Usage: AnthropicResponseUsage{
				InputTokens:  inTokens,
				OutputTokens: outTokens,
			},
		}
		writeJSON(w, http.StatusOK, &anthResp)
	}

}

func (h *APICompatHandler) handleStreamResponse(
	ctx context.Context,
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	clientModel string,
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "streaming not supported by server"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK) // 显式发送 200 + SSE 响应头，确保客户端立即收到
	flusher.Flush()

	scanner := bufio.NewScanner(respBody)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer，防止大型 SSE 行被截断

	// 临时生成的流 ID 和固定时间戳，确保所有 chunk 完全一致，防止严格客户端断开
	streamID := fmt.Sprintf("msg_%d", rand.Int63())
	if apiFormat == "openai" {
		streamID = fmt.Sprintf("chatcmpl-%d", rand.Int63())
	}
	createdAt := startTime.Unix()

	// 保持透传客户端原始请求的模型 ID，避免 Claude Code 等 Strict 客户端校验失败
	displayModel := clientModel
	if displayModel == "" {
		displayModel = geminiModel
	}

	// 初始化计数
	inTokens := 0
	outTokens := 0

	var err error

	// 流式状态跟踪
	blockIndex := 0
	textBlockOpen := false
	thinkingBlockOpen := false
	thinkingEmittedAny := false // thinking 块是否已实际下发过 thinking_delta;关块前据此判断是否补 signature_delta
	hasFunctionCall := false
	openAIRoleSent := false

	// Anthropic 协议下，开始流时首发 message_start
	if apiFormat == "anthropic" {
		msgStart := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            streamID,
				"type":          "message",
				"role":          "assistant",
				"content":       []interface{}{},
				"model":         displayModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				// usage:对齐 Anthropic 官方流式 message_start 实例 {"input_tokens":N,"output_tokens":1},
				// output_tokens 起始占位为 1(官方惯例预扣占位,与 NVIDIA 路径 messageStartPayload 一致)。
				// input_tokens 此阶段尚无累计值,置 0,由末帧 message_delta 补累计真实值。
				"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": 1},
			},
		}
		msgStartBytes, _ := json.Marshal(msgStart)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(msgStartBytes))
		flusher.Flush()
	}

	// Responses API 专用变量
	seqNum := 0
	nextSeq := func() int {
		seqNum++
		return seqNum
	}
	responsesMsgOpened := false
	responsesMsgID := fmt.Sprintf("msg_%s_0", streamID)
	responsesMsgOutIdx := 0 // 正文 message item 占用的 output_index,首次开块时由 responsesOutIdx 分配
	var responsesTextBuf strings.Builder
	hasOpenAIToolCall := false

	// ===== Responses 协议 reasoning(思考)独立 item 状态机 =====
	// thought 与正文必须拆成各自独立的 output_item,不能共用一个 message 条目:
	// 旧实现 thought 与 text 共用 responsesMsgOpened/responsesMsgID/responsesTextBuf 且都硬编码 output_index 0,
	// 导致 thought 一旦真正流入(A 修复后),part 类型被正文覆盖、index 撞车、无收尾 done——Codex 收到损坏流。
	// 此处用独立状态把思考拆为单独的 reasoning item(item.type=message, content[].type=reasoning_text),
	// 与正文 message item、function_call item 各占一个 output_index,互不干扰。
	//
	// output_index 分配约定(responsesOutIdx 递增计数器,开块即分配并推进):
	//   - reasoning item 开块时取 responsesOutIdx 并 ++(reasoning 先到则占 0)
	//   - 正文 message item 开块时取 responsesOutIdx 并 ++(在其之后)
	//   - 每个 function_call item 开块时取 responsesOutIdx 并 ++
	// 每类 item 用各自记录的 index 续发 delta/done,互不撞车。
	responsesReasoningOpened := false
	responsesReasoningID := fmt.Sprintf("msg_%s_r0", streamID)
	responsesReasoningOutIdx := 0 // reasoning item 锁定的 output_index(开块时赋值)
	var responsesReasoningBuf strings.Builder

	responsesOutIdx := 0
	// closeResponsesReasoning 闭合已开启的 reasoning item:发 reasoning_text.done +
	// content_part.done + output_item.done 三件套,清零状态。幂等(未开则不动作)。
	// 不在此推进 responsesOutIdx(index 在开块时已分配并推进),只发收尾事件。
	closeResponsesReasoning := func() {
		if !responsesReasoningOpened {
			return
		}
		reasonsText := responsesReasoningBuf.String()
		reasonDone := map[string]interface{}{
			"type":            "response.reasoning_text.done",
			"sequence_number": nextSeq(),
			"item_id":         responsesReasoningID,
			"output_index":    responsesReasoningOutIdx,
			"content_index":   0,
			"text":            reasonsText,
		}
		rdBytes, _ := json.Marshal(reasonDone)
		fmt.Fprintf(w, "event: response.reasoning_text.done\ndata: %s\n\n", string(rdBytes))

		reasonPartDone := map[string]interface{}{
			"type":            "response.content_part.done",
			"sequence_number": nextSeq(),
			"item_id":         responsesReasoningID,
			"output_index":    responsesReasoningOutIdx,
			"content_index":   0,
			"part": map[string]interface{}{
				"type": "reasoning_text",
				"text": reasonsText,
			},
		}
		rpdBytes, _ := json.Marshal(reasonPartDone)
		fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(rpdBytes))

		reasonItemDone := map[string]interface{}{
			"type":            "response.output_item.done",
			"sequence_number": nextSeq(),
			"output_index":    responsesReasoningOutIdx,
			"item": map[string]interface{}{
				"id":      responsesReasoningID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": reasonsText}},
			},
		}
		ridBytes, _ := json.Marshal(reasonItemDone)
		fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(ridBytes))
		responsesReasoningOpened = false
	}

	// Responses 协议下，开始流时首发 response.created 和 response.in_progress
	if apiFormat == "responses" {
		createdEvt := map[string]interface{}{
			"type":            "response.created",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		createdBytes, _ := json.Marshal(createdEvt)
		fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", string(createdBytes))

		inprogEvt := map[string]interface{}{
			"type":            "response.in_progress",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		inprogBytes, _ := json.Marshal(inprogEvt)
		fmt.Fprintf(w, "event: response.in_progress\ndata: %s\n\n", string(inprogBytes))
		flusher.Flush()
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			h.log("⏹️ 客户端连接已取消，中断流式响应扫描")
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data:")
		dataStr = strings.TrimSpace(dataStr)
		if dataStr == "" {
			continue
		}

		var gemResp GeminiResponse
		if err = json.Unmarshal([]byte(dataStr), &gemResp); err != nil {
			continue
		}

		// 同步更新用量
		if gemResp.UsageMetadata.PromptTokenCount > 0 {
			inTokens = gemResp.UsageMetadata.PromptTokenCount
		}
		if gemResp.UsageMetadata.CandidatesTokenCount > 0 {
			outTokens = gemResp.UsageMetadata.CandidatesTokenCount
		}

		if len(gemResp.Candidates) == 0 {
			continue
		}

		// 只要拿到上游 Candidate 响应，第一时间发射 OpenAI role: "assistant" 声明首包（对齐 Antigravity-Manager 逻辑）
		if apiFormat == "openai" && !openAIRoleSent {
			initChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Role: "assistant"}, FinishReason: nil},
				},
			}
			initBytes, _ := json.Marshal(initChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
			flusher.Flush()
			openAIRoleSent = true
		}

		if len(gemResp.Candidates[0].Content.Parts) == 0 {
			continue
		}

		// 遍历所有 Parts，分别处理 text、thinking 和 functionCall
		for _, part := range gemResp.Candidates[0].Content.Parts {
			cleanText := SanitizeAllThoughtSignatures(part.Text)
			if cleanText != "" {
				if part.Thought {
					// 思考过程分片处理
					if apiFormat == "anthropic" {
						if textBlockOpen {
							stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
							stopBytes, _ := json.Marshal(stopEvt)
							fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
							blockIndex++
							textBlockOpen = false
						}
						if !thinkingBlockOpen {
							startEvt := map[string]interface{}{
								"type":          "content_block_start",
								"index":         blockIndex,
								"content_block": map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""},
							}
							startBytes, _ := json.Marshal(startEvt)
							fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(startBytes))
							thinkingBlockOpen = true
							thinkingEmittedAny = false
						}
						deltaEvt := map[string]interface{}{
							"type":  "content_block_delta",
							"index": blockIndex,
							"delta": map[string]interface{}{"type": "thinking_delta", "thinking": cleanText},
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
						thinkingEmittedAny = true
					} else if apiFormat == "openai" {
						chunk := OpenAIStreamChunk{
							ID:      streamID,
							Object:  "chat.completion.chunk",
							Created: createdAt,
							Model:   displayModel,
							Choices: []OpenAIStreamChoice{
								{Index: 0, Delta: OpenAIDelta{Content: cleanText}, FinishReason: nil},
							},
						}
						chunkBytes, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
					} else if apiFormat == "responses" {
						if !responsesReasoningOpened {
							responsesReasoningOutIdx = responsesOutIdx
							responsesOutIdx++
							itemAdded := map[string]interface{}{
								"type":            "response.output_item.added",
								"sequence_number": nextSeq(),
								"output_index":    responsesReasoningOutIdx,
								"item": map[string]interface{}{
									"id":      responsesReasoningID,
									"type":    "message",
									"status":  "in_progress",
									"role":    "assistant",
									"content": []interface{}{},
								},
							}
							itemBytes, _ := json.Marshal(itemAdded)
							fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

							partAdded := map[string]interface{}{
								"type":            "response.content_part.added",
								"sequence_number": nextSeq(),
								"item_id":         responsesReasoningID,
								"output_index":    responsesReasoningOutIdx,
								"content_index":   0,
								"part": map[string]interface{}{
									"type": "reasoning_text",
									"text": "",
								},
							}
							partBytes, _ := json.Marshal(partAdded)
							fmt.Fprintf(w, "event: response.content_part.added\ndata: %s\n\n", string(partBytes))
							responsesReasoningOpened = true
						}
						responsesReasoningBuf.WriteString(cleanText)
						deltaEvt := map[string]interface{}{
							"type":            "response.reasoning_text.delta",
							"sequence_number": nextSeq(),
							"item_id":         responsesReasoningID,
							"output_index":    responsesReasoningOutIdx,
							"content_index":   0,
							"delta":           cleanText,
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: response.reasoning_text.delta\ndata: %s\n\n", string(deltaBytes))
					}
					flusher.Flush()
				} else {
					// 正规 Output Text 消息文本处理
					if apiFormat == "openai" {
						if !openAIRoleSent {
							initChunk := OpenAIStreamChunk{
								ID:      streamID,
								Object:  "chat.completion.chunk",
								Created: createdAt,
								Model:   displayModel,
								Choices: []OpenAIStreamChoice{
									{Index: 0, Delta: OpenAIDelta{Role: "assistant"}, FinishReason: nil},
								},
							}
							initBytes, _ := json.Marshal(initChunk)
							fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
							flusher.Flush()
							openAIRoleSent = true
						}

						chunk := OpenAIStreamChunk{
							ID:      streamID,
							Object:  "chat.completion.chunk",
							Created: createdAt,
							Model:   displayModel,
							Choices: []OpenAIStreamChoice{
								{Index: 0, Delta: OpenAIDelta{Content: cleanText}, FinishReason: nil},
							},
						}
						chunkBytes, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
					} else if apiFormat == "responses" {
						// 进入正文 item 前,先把可能已开启的 reasoning item 闭合掉:
						// 思考与正文是两个独立 output_item,各自的 output_index 由 responsesOutIdx 分配。
						closeResponsesReasoning()
						if !responsesMsgOpened {
							responsesMsgOutIdx = responsesOutIdx
							responsesOutIdx++
							itemAdded := map[string]interface{}{
								"type":            "response.output_item.added",
								"sequence_number": nextSeq(),
								"output_index":    responsesMsgOutIdx,
								"item": map[string]interface{}{
									"id":      responsesMsgID,
									"type":    "message",
									"status":  "in_progress",
									"role":    "assistant",
									"content": []interface{}{},
								},
							}
							itemBytes, _ := json.Marshal(itemAdded)
							fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

							partAdded := map[string]interface{}{
								"type":            "response.content_part.added",
								"sequence_number": nextSeq(),
								"item_id":         responsesMsgID,
								"output_index":    responsesMsgOutIdx,
								"content_index":   0,
								"part": map[string]interface{}{
									"type": "output_text",
									"text": "",
								},
							}
							partBytes, _ := json.Marshal(partAdded)
							fmt.Fprintf(w, "event: response.content_part.added\ndata: %s\n\n", string(partBytes))
							responsesMsgOpened = true
						}
						responsesTextBuf.WriteString(cleanText)

						deltaEvt := map[string]interface{}{
							"type":            "response.output_text.delta",
							"sequence_number": nextSeq(),
							"item_id":         responsesMsgID,
							"output_index":    responsesMsgOutIdx,
							"content_index":   0,
							"delta":           cleanText,
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", string(deltaBytes))
					} else { // anthropic
						if thinkingBlockOpen {
							// 关 thinking 块前补一条空串 signature_delta(Gemini 已剥真签名,发空串占位),
							// 严格对齐官方 thinking_delta → signature_delta → content_block_stop 序列。
							// 若 thinking 块从未下发过 thinking_delta(thinkingEmittedAny==false,异常只开块),
							// 则不发 signature_delta 也不发 stop —— 但此处 thinkingBlockOpen 必伴随首 delta,
							// 故仅作防御保留对称逻辑,正常路径总会执行 signature_delta。
							if thinkingEmittedAny {
								sigEvt := map[string]interface{}{
									"type":  "content_block_delta",
									"index": blockIndex,
									"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
								}
								sigBytes, _ := json.Marshal(sigEvt)
								fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
								stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
								stopBytes, _ := json.Marshal(stopEvt)
								fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
								blockIndex++
							}
							thinkingBlockOpen = false
							thinkingEmittedAny = false
						}
						// 延迟开启 text block：仅在有实际文本时才发送 content_block_start
						if !textBlockOpen {
							blockStart := map[string]interface{}{
								"type":          "content_block_start",
								"index":         blockIndex,
								"content_block": map[string]interface{}{"type": "text", "text": ""},
							}
							blockStartBytes, _ := json.Marshal(blockStart)
							fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(blockStartBytes))
							textBlockOpen = true
						}
						delta := map[string]interface{}{
							"type":  "content_block_delta",
							"index": blockIndex,
							"delta": map[string]interface{}{"type": "text_delta", "text": cleanText},
						}
						deltaBytes, _ := json.Marshal(delta)
						fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
					}
					flusher.Flush()
				}
			}

			if part.FunctionCall != nil {
				if apiFormat == "openai" {
					hasOpenAIToolCall = true
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					startChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   geminiModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: 0,
											ID:    callID,
											Type:  "function",
											Function: OpenAIToolCallFunction{
												Name:      part.FunctionCall.Name,
												Arguments: "",
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					startBytes, _ := json.Marshal(startChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(startBytes))

					argsChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   geminiModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: 0,
											Function: OpenAIToolCallFunction{
												Arguments: string(argsJSON),
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					argsBytes, _ := json.Marshal(argsChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(argsBytes))
					flusher.Flush()
				} else if apiFormat == "responses" {
					// 进入工具调用 item 前,先关掉可能还开着的 reasoning item(思考与工具不同 item);
					// 正文 message item 若已开则保持开启(一个 response 可同时含 message + function_call)。
					closeResponsesReasoning()
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					fcItemID := fmt.Sprintf("fc_%s", callID)
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					argsStr := string(argsJSON)

					// 每个工具调用独占一个 output_index,开块即分配并推进
					fcOutIdx := responsesOutIdx
					responsesOutIdx++

					itemAdded := map[string]interface{}{
						"type":            "response.output_item.added",
						"sequence_number": nextSeq(),
						"output_index":    fcOutIdx,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "in_progress",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": "",
						},
					}
					itemBytes, _ := json.Marshal(itemAdded)
					fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

					deltaEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.delta",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    fcOutIdx,
						"call_id":         callID,
						"delta":           argsStr,
					}
					deltaBytes, _ := json.Marshal(deltaEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.delta\ndata: %s\n\n", string(deltaBytes))

					doneEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.done",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    fcOutIdx,
						"call_id":         callID,
						"arguments":       argsStr,
					}
					doneBytes, _ := json.Marshal(doneEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.done\ndata: %s\n\n", string(doneBytes))

					itemDone := map[string]interface{}{
						"type":            "response.output_item.done",
						"sequence_number": nextSeq(),
						"output_index":    fcOutIdx,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "completed",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": argsStr,
						},
					}
					itemDoneBytes, _ := json.Marshal(itemDone)
					fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemDoneBytes))
					flusher.Flush()
				} else if apiFormat == "anthropic" {
					hasFunctionCall = true

					// 先关闭未完成的 thinking block(与 text block 互斥,任一时刻至多其一开启)。
					// 旧实现只关 textBlockOpen、漏关 thinkingBlockOpen:模型"先思考再做工具调用"
					// (Claude Code + gemini 注入 includeThoughts 后的常态)时,thinking 块遗留未闭合,
					// 此处 tool_use 的 content_block_start 覆盖了 thinking 原索引(blockIndex 未推进),
					// 末尾收尾段再见 thinkingBlockOpen==true 在已偏移的 blockIndex 上补发
					// signature_delta/content_block_stop,命中从未 start 过的索引,
					// Claude Code cr[index] 查无此块 → "Content block not found"。
					// 补此闭合以对称 text 分支/末尾收尾两处的 thinking 关块逻辑。
					if thinkingBlockOpen {
						// 仅当确实下发过 thinking_delta 才补 signature_delta + stop(对齐官方
						// thinking_delta → signature_delta → content_block_stop 序列);
						// thinkingEmittedAny==false 属不可达防御(thought 分支 start 与 delta 同迭代下发),
						// 与 text 分支/收尾段守卫语义一致,不引入新行为。
						if thinkingEmittedAny {
							sigEvt := map[string]interface{}{
								"type":  "content_block_delta",
								"index": blockIndex,
								"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
							}
							sigBytes, _ := json.Marshal(sigEvt)
							fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
							stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
							stopBytes, _ := json.Marshal(stopEvt)
							fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
							blockIndex++
						}
						thinkingBlockOpen = false
						thinkingEmittedAny = false
						flusher.Flush()
					}

					// 再关闭未完成的 text block
					if textBlockOpen {
						stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
						stopBytes, _ := json.Marshal(stopEvt)
						fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
						blockIndex++
						textBlockOpen = false
						flusher.Flush()
					}

					toolID := generateToolUseID()

					// content_block_start: tool_use
					toolStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    toolID,
							"name":  part.FunctionCall.Name,
							"input": map[string]interface{}{},
						},
					}
					toolStartBytes, _ := json.Marshal(toolStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(toolStartBytes))

					// content_block_delta: input_json_delta（一次性发完 args JSON）
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					inputDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": blockIndex,
						"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(argsJSON)},
					}
					inputDeltaBytes, _ := json.Marshal(inputDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(inputDeltaBytes))

					// content_block_stop
					toolStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
					toolStopBytes, _ := json.Marshal(toolStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(toolStopBytes))
					blockIndex++

					flusher.Flush()
				}
			}
		}
	}

	// 发射结束帧
	if apiFormat == "openai" {
		if !openAIRoleSent && !hasOpenAIToolCall {
			initChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Role: "assistant", Content: "I have processed your request."}, FinishReason: nil},
				},
			}
			initBytes, _ := json.Marshal(initChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
			flusher.Flush()
			openAIRoleSent = true
		} else if openAIRoleSent && outTokens == 0 && !hasOpenAIToolCall {
			// 如果已经发过 role: "assistant" 初始帧，但上游因为 finishReason: OTHER 输出了 0 个 OutTokens
			// 补发非空 Content 帧，防止 Codex CLI 接收空 Content 而静默断开
			fallbackChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Content: "I have processed your request."}, FinishReason: nil},
				},
			}
			fallbackBytes, _ := json.Marshal(fallbackChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(fallbackBytes))
			flusher.Flush()
		}

		var finishReason interface{} = "stop"
		if hasOpenAIToolCall {
			finishReason = "tool_calls"
		}
		finalChunk := OpenAIStreamChunk{
			ID:      streamID,
			Object:  "chat.completion.chunk",
			Created: createdAt,
			Model:   displayModel,
			Choices: []OpenAIStreamChoice{
				{Index: 0, Delta: OpenAIDelta{}, FinishReason: finishReason},
			},
		}
		finalBytes, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(finalBytes))
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	} else if apiFormat == "responses" {
		// 收尾前先闭合可能仍开着的 reasoning item(若思考是最后一个 part 且后续无正文,
		// 循环内未触发 closeResponsesReasoning,在此补收尾 done 三件套)。
		closeResponsesReasoning()
		fullText := responsesTextBuf.String()
		var outputItems []interface{}

		if responsesMsgOpened {
			txtDone := map[string]interface{}{
				"type":            "response.output_text.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    responsesMsgOutIdx,
				"content_index":   0,
				"text":            fullText,
			}
			txtBytes, _ := json.Marshal(txtDone)
			fmt.Fprintf(w, "event: response.output_text.done\ndata: %s\n\n", string(txtBytes))

			partDone := map[string]interface{}{
				"type":            "response.content_part.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    responsesMsgOutIdx,
				"content_index":   0,
				"part": map[string]interface{}{
					"type": "output_text",
					"text": fullText,
				},
			}
			partBytes, _ := json.Marshal(partDone)
			fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(partBytes))

			itemMsg := map[string]interface{}{
				"id":      responsesMsgID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": fullText}},
			}
			itemDone := map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSeq(),
				"output_index":    responsesMsgOutIdx,
				"item":            itemMsg,
			}
			itemBytes, _ := json.Marshal(itemDone)
			fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemBytes))

			outputItems = append(outputItems, itemMsg)
		}

		completedEvt := map[string]interface{}{
			"type":            "response.completed",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": outputItems,
			},
		}
		completedBytes, _ := json.Marshal(completedEvt)
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(completedBytes))
	} else { // anthropic
		// 关闭未完成的 thinking block / text block
		if thinkingBlockOpen {
			// 关 thinking 块前补一条空串 signature_delta(仅当确实下发过 thinking_delta):
			// 严格对齐官方序列 thinking_delta → signature_delta → content_block_stop。
			if thinkingEmittedAny {
				sigEvt := map[string]interface{}{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
				}
				sigBytes, _ := json.Marshal(sigEvt)
				fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
				blockStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
				blockStopBytes, _ := json.Marshal(blockStop)
				fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))
				blockIndex++
			}
			thinkingBlockOpen = false
			thinkingEmittedAny = false
		}
		if textBlockOpen {
			blockStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
			blockStopBytes, _ := json.Marshal(blockStop)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))
			textBlockOpen = false
		}

		// 兜底：如果完全没有打开过任何 content block (blockIndex == 0 && !hasFunctionCall)，给 Anthropic / Claude Code 客户端发射一个空 text content block，确保 Claude Code 不会报错
		if blockIndex == 0 && !hasFunctionCall {
			startEvt := map[string]interface{}{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]interface{}{"type": "text", "text": ""},
			}
			startBytes, _ := json.Marshal(startEvt)
			fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(startBytes))

			deltaEvt := map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{"type": "text_delta", "text": ""},
			}
			deltaBytes, _ := json.Marshal(deltaEvt)
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))

			stopEvt := map[string]interface{}{"type": "content_block_stop", "index": 0}
			stopBytes, _ := json.Marshal(stopEvt)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
		}

		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}

		msgDelta := map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			// usage:官方明确 message_delta 的 token 计数为累计值(cumulative),
			// 故 input_tokens 填本轮累计输入(PromptTokenCount)、output_tokens 填累计输出
			// (CandidatesTokenCount)。与 NVIDIA 路径 messageDeltaPayload 双填对齐,
			// 让严格客户端(Claude Code SDK)的用度归集完整,松散客户端忽略多余字段不受影响。
			"usage": map[string]interface{}{"input_tokens": inTokens, "output_tokens": outTokens},
		}
		msgDeltaBytes, _ := json.Marshal(msgDelta)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", string(msgDeltaBytes))

		fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
	flusher.Flush()

	// 记录用量统计

}

type RateLimiter struct {
	mu           sync.Mutex
	userRequests map[string][]time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		userRequests: make(map[string][]time.Time),
	}
}

func (l *RateLimiter) Allow(userID string, limit int) bool {
	if limit <= 0 {
		limit = 30
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	oneMinuteAgo := now.Add(-1 * time.Minute)

	reqs := l.userRequests[userID]
	var validReqs []time.Time
	for _, t := range reqs {
		if t.After(oneMinuteAgo) {
			validReqs = append(validReqs, t)
		}
	}

	if len(validReqs) >= limit {
		l.userRequests[userID] = validReqs
		return false
	}

	validReqs = append(validReqs, now)
	l.userRequests[userID] = validReqs
	return true
}

func (h *APICompatHandler) handleV1Internal(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	// 自动补齐缺失的 project 和 requestId 字段，让客户端请求体最简
	var rawPayload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawPayload); err == nil {
		modified := false
		if _, exists := rawPayload["project"]; !exists {
			rawPayload["project"] = "favorable-synapse-ttvcb"
			modified = true
		}
		if _, exists := rawPayload["requestId"]; !exists {
			rawPayload["requestId"] = fmt.Sprintf("chat/%d-%d", time.Now().Unix(), rand.Intn(1000000))
			modified = true
		}
		if modified {
			if newBytes, errMar := json.Marshal(rawPayload); errMar == nil {
				bodyBytes = newBytes
			}
		}
	}

	// 动态检测目标 Action 和流式属性
	path := r.URL.Path // e.g. /v1internal:generateContent
	action := "generateContent"
	if strings.Contains(path, "streamGenerateContent") {
		action = "streamGenerateContent"
	}
	isStreaming := action == "streamGenerateContent" || strings.Contains(r.URL.RawQuery, "alt=sse")

	queryStr := ""
	if r.URL.RawQuery != "" {
		queryStr = "?" + r.URL.RawQuery
	} else if isStreaming && !strings.Contains(r.URL.RawQuery, "alt=sse") {
		queryStr = "?alt=sse"
	}

	if h.accountMgr == nil || h.sessionRouter == nil {
		// 防御性降级：用于兼容未配置账号管理器的单元测试或异常回退
		targetURL := fmt.Sprintf("http://%s%s%s", localProxyAddr, path, queryStr)
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+userSession.UserKey)
		req.Header.Set("X-Relay-User-Id", userSession.UserID)
		if userSession.APIKeyID != "" {
			req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
		}
		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to forward request to proxy: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		for k, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 4096)
		flusher, isFlusher := w.(http.Flusher)
		for {
			n, errRead := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if isFlusher {
					flusher.Flush()
				}
			}
			if errRead != nil {
				break
			}
		}
		return
	}

	// 直接从账号池中分配一个可用账号，实现类似抓包分析 of 直连调用
	poolChannel := h.accountMgr.GetActiveChannel()

	// 从请求体中动态解析模型名称，避免写死
	currentModel := "gemini-2.5-flash"
	var bodyJson struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Model != "" {
		parts := strings.Split(bodyJson.Model, "/models/")
		if len(parts) > 1 {
			currentModel = parts[1]
		} else {
			parts2 := strings.Split(bodyJson.Model, "/")
			currentModel = parts2[len(parts2)-1]
		}
	}

	available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, currentModel)

	// 如果通道未开启负载均衡（池模式关闭），限制仅包含第一个激活账号
	isPoolEnabled := false
	if poolChannel == "project" {
		isPoolEnabled = h.accountMgr.GetProjectPoolMode()
	} else {
		isPoolEnabled = h.accountMgr.GetPoolMode()
	}
	if !isPoolEnabled && len(available) > 0 {
		available = []*account.Account{available[0]}
	}

	// 临时记录失败的账号，防止本次请求多次分配它们
	skippedAccounts := make(map[string]bool)

	// 最多尝试账号池里的可用账号总数
	maxAttempts := len(available)
	if maxAttempts > 5 {
		maxAttempts = 5
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	var finalResp *http.Response
	var finalErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 筛选未在此次请求中失败的账号
		var activeAvailable []*account.Account
		for _, a := range available {
			if !skippedAccounts[a.ID] {
				activeAvailable = append(activeAvailable, a)
			}
		}

		if len(activeAvailable) == 0 {
			finalErr = fmt.Errorf("all accounts in pool failed with authentication/scope errors")
			break
		}

		sessionKey := userSession.UserID
		poolAccount := h.sessionRouter.GetOrAssignAccount(sessionKey, activeAvailable, h.logFn)
		if poolAccount == nil {
			finalErr = fmt.Errorf("no available account assigned from pool")
			break
		}
		actualProjectId := poolAccount.ProjectID

		// 构建发往谷歌的直连请求参数
		var targetHost string
		var targetPath string
		var finalReqBody = bodyBytes

		if poolAccount.Provider == "project" {
			// GCP 项目通道：直接发往 aiplatform.googleapis.com (Vertex AI)
			targetHost = "aiplatform.googleapis.com"
			vertexAction := "generateContent"
			if isStreaming {
				vertexAction = "streamGenerateContent"
			}
			targetModel := mapModelForProjectInRelay(currentModel)
			targetPath = fmt.Sprintf("/v1/projects/%s/locations/global/publishers/google/models/%s:%s%s", poolAccount.ProjectID, targetModel, vertexAction, queryStr)
		} else if poolAccount.Provider == "antigravity" {
			// 个人网页账号（网页通道，使用 Cloud Code 接口）：不管有没有专属项目，都发往云助手的 v1internal 接口，若无项目 ID 则使用公共默认值
			targetHost = "daily-cloudcode-pa.googleapis.com"
			targetPath = fmt.Sprintf("/v1internal:%s%s", action, queryStr)
			if actualProjectId == "" {
				actualProjectId = "favorable-synapse-ttvcb"
			}
			var v1internalReq map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &v1internalReq); err == nil {
				if _, exists := v1internalReq["project"]; exists {
					v1internalReq["project"] = actualProjectId

					// 注入缓存的 thoughtSignature 到 functionCall parts，
					// 替换哨兵值为真实签名，保证 v1internal API 思考链连续性
					if innerReq, ok := v1internalReq["request"].(map[string]interface{}); ok {
						sigcache.InjectCachedSignatures(innerReq, sigcache.GetGlobal(), userSession.UserID, currentModel)

						// 强效注入思考配置，强制 Gemini 上游输出带 thought:true 标记的明文思考内容
						if IsEnableThinkingMode() {
							genConfig, _ := innerReq["generationConfig"].(map[string]interface{})
							if genConfig == nil {
								genConfig = make(map[string]interface{})
								innerReq["generationConfig"] = genConfig
							}
							genConfig["thinkingConfig"] = map[string]interface{}{
								"includeThoughts": true,
							}
						}
					}

					if innerBytes, errMar := json.Marshal(v1internalReq); errMar == nil {
						finalReqBody = innerBytes
					}
				}
			}
		} else {
			// 其他号源（如 gemini-cli / API Key 等）：直接反向翻译并降级为标准的 generativelanguage 接口发送
			targetHost = "generativelanguage.googleapis.com"
			fallbackModel := currentModel
			if strings.Contains(fallbackModel, "low") || strings.Contains(fallbackModel, "agent") || strings.Contains(fallbackModel, "tab") || strings.Contains(fallbackModel, "3.5") {
				fallbackModel = "gemini-1.5-flash"
			}
			targetPath = fmt.Sprintf("/v1beta/models/%s:%s%s", fallbackModel, action, queryStr)

			// 剥离外层 v1internal 包体，提取内层标准的 request 字段
			var v1internalReq map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &v1internalReq); err == nil {
				if innerReq, exists := v1internalReq["request"]; exists {
					if innerBytes, errMar := json.Marshal(innerReq); errMar == nil {
						finalReqBody = innerBytes
					}
				}
			}
		}

		// 构造直连发包请求
		targetURL := fmt.Sprintf("https://%s%s", targetHost, targetPath)
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(finalReqBody))
		if err != nil {
			finalErr = err
			break
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		req.Header.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")

		h.log("🚀 [中继直连重试 %d/%d] 正在为用户 %s 分配账号 %s | 目标: https://%s%s", attempt+1, maxAttempts, userSession.UserID, poolAccount.Email, targetHost, targetPath)

		// 执行请求并流式响应
		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}

		resp, errDo := httpClient.Do(req)
		if errDo != nil {
			h.log("⚠️ [中继直连] 账号 %s 访问谷歌失败: %v", poolAccount.Email, errDo)
			skippedAccounts[poolAccount.ID] = true
			finalErr = errDo
			continue
		}

		// 如果账号没有对应作用域（403 ACCESS_TOKEN_SCOPE_INSUFFICIENT）
		// 或者没有云助手项目 IAM 权限（403 PERMISSION_DENIED），视为该账号在该通道不具备权限，自动剔除并换号重试！
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			h.log("⚠️ [中继直连] 账号 %s 遇到权限/Scope限制错误 (%d)，正在自动剔除并换号重试...", poolAccount.Email, resp.StatusCode)
			resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			finalResp = resp
			finalErr = fmt.Errorf("authentication error: status %d", resp.StatusCode)

			// 主动清除当前会话与报错账号的强行绑定关系，下次分发重选
			h.sessionRouter.UnbindSession(sessionKey)
			continue
		}

		finalResp = resp
		finalErr = nil
		break
	}

	if finalErr != nil {
		if finalResp != nil {
			// 写回最后的 403 / 401 响应体
			for k, values := range finalResp.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(finalResp.StatusCode)
			io.Copy(w, finalResp.Body)
			finalResp.Body.Close()
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": finalErr.Error()})
		}
		return
	}

	defer finalResp.Body.Close()

	// 拷贝响应头
	for k, values := range finalResp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(finalResp.StatusCode)

	// 流式传输响应体
	buf := make([]byte, 4096)
	flusher, isFlusher := w.(http.Flusher)
	for {
		n, errRead := finalResp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
			// 提取 thoughtSignature 并缓存，下次请求注入到 functionCall parts
			sigcache.GetGlobal().ExtractAndCacheSignatures(buf[:n], userSession.UserID)
		}
		if errRead != nil {
			break
		}
	}
}

func mapModelForProjectInRelay(modelName string) string {
	modelNameLower := strings.ToLower(modelName)
	if strings.Contains(modelNameLower, "gemini-2.0-flash") {
		return "gemini-2.0-flash"
	}
	if strings.Contains(modelNameLower, "gemini-2.0-pro") {
		return "gemini-2.0-pro-exp-02-05"
	}
	if strings.Contains(modelNameLower, "gemini-1.5-pro") {
		return "gemini-1.5-pro"
	}
	return "gemini-1.5-flash"
}
