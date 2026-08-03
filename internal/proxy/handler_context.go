package proxy

import (
	"antigravity-proxy/internal/account"
	"net/http"
	"time"
)

// handler_context.go: ServeHTTP 请求级上下文 + 阶段编排器。
//
// 原 ServeHTTP 1558 行单函数含 logRequestToTracker / attemptRequest 两个闭包及一个重试循环,
// 闭包捕获大量可变局部变量。本拆分把它们提升为 (sc *serveContext) 方法,跨阶段共享的可变状态
// 提升为 serveContext 字段,物理搬移,逻辑逐行等价,零回归。
//
// 阶段划分(详见各卫星文件):
//   handler_attempt_routing.go   routeForAttempt     号池选择 + 项目/模型/URL/载荷重写
//   handler_attempt_forward.go   forwardForAttempt   建请求 + client.Do + 流式/非流式读取 + 签名提取
//   handler_attempt_classify.go  classifyResponse    401/429/503/5xx/200 token 计数分类 + 抓包落库
//   handler_retry_loop.go        runRetryLoop        重试循环 + 账号冷静期/Token 刷新/退避
//
// 关键不变量(经 Plan 对齐原控制流验证):
//  1. sc.targetHost/sc.targetPath 路由后保持 setup 原值,routing 只写 routeOutcome 携带的
//     localTargetHost/Path(copy),重试循环的通道探测仍读原值,不受路由重写影响。
//  2. sc.headersSent 一旦流式 200 阶段置 true 后永不重置 —— w.WriteHeader 已提交,
//     后续重试跳过写头,与原闭包外 var headersSent bool 跨 attempt 持久语义一致。
//  3. customHeaders 是 map(ref),routeForAttempt 创建并返回,forward 原地清洗,classify 读做
//     SavePacket —— 同一 map 引用贯穿三阶段,不做拷贝。
//  4. SavePacket 只发生在 classify 入口(对应原 L1244),forward 的 finalized 早返回
//     (建请求失败/client.Do 失败/流式断流/流式中断/读失败)不抓包 —— 与原控制流逐行一致。
//  5. attemptRequest 严格 route -> forward(若 finalized 直接返) -> classify;流式成功正常 EOF
//     (finalized=false)落条到 classify,与原 L1218 落条到 L1234 完全一致。

type routeOutcome struct {
	poolAccount   *account.Account
	targetHost    string
	targetPath    string
	customHeaders http.Header
	finalReqBody  []byte
}

type forwardOutcome struct {
	status    int
	headers   http.Header
	body      []byte
	streamed  bool
	err       error
	finalized bool
}

type serveContext struct {
	h *ProxyHandler
	w http.ResponseWriter
	r *http.Request

	// setup 阶段写入,各阶段只读(setup 后不再变)
	startTime     time.Time
	relayUserID   string
	relayAPIKeyID string
	targetHost    string // 原始 host —— 路由只改 routeOutcome.targetHost,不改这个
	targetPath    string // 原始 path —— 同上
	bodyBytes     []byte
	currentModel  string
	logPrefix     string
	rawSessionKey string
	sessionKey    string
	maxBodyBytes  int64 // 非流式读取上限(io.LimitReader)

	// retry-loop 写入,logRequestToTracker 读
	currentAttemptIndex int

	// routing 写入(poolAccount.Email),logRequestToTracker 内兜底 "直连"
	allocatedAccount string

	// classify 写入,logRequestToTracker 读
	inTokens     int
	outTokens    int
	cachedTokens int

	// logRequestToTracker 经 h.logRequestToTracker(&logged, ...) 读写
	logged bool

	// forward 流式 200 阶段写,retry-loop 读,永不重置
	headersSent bool
}

func (sc *serveContext) logRequestToTracker(statusCode int, errDetail string) {
	if sc.allocatedAccount == "" {
		sc.allocatedAccount = "直连"
	}
	sc.h.logRequestToTracker(
		&sc.logged,
		statusCode,
		errDetail,
		sc.targetPath,
		sc.cachedTokens,
		sc.bodyBytes,
		sc.currentModel,
		sc.allocatedAccount,
		sc.currentAttemptIndex,
		sc.r,
		sc.inTokens,
		sc.outTokens,
		sc.sessionKey,
		sc.startTime,
		sc.targetHost,
	)
}

// attemptRequest 是阶段编排器:route -> forward -> classify。
// 返回签名严格对齐原闭包 attemptRequest(attemptIndex):
//
//	(resp.StatusCode(0 表示无 resp), resp.Header, respBodyBytes/sentBytes, isStreamed, err/sentinel)
func (sc *serveContext) attemptRequest(attemptIndex int) (int, map[string][]string, []byte, bool, error) {
	ro, err := sc.routeForAttempt(attemptIndex)
	if err != nil {
		return 0, nil, nil, false, err // 对应原 L619 QUOTA_EXHAUSTED
	}
	fo := sc.forwardForAttempt(attemptIndex, ro)
	if fo.finalized {
		return fo.status, fo.headers, fo.body, fo.streamed, fo.err
	}
	return sc.classifyResponse(attemptIndex, ro, fo)
}
