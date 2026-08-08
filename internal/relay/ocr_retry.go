package relay

// ocr_retry.go —— OCR 上游调用的瞬时失败重试基础设施(L1)。
//
// 背景:OCR 真打上游(ocrImageUncached / ocrImageUncachedViaRoute)此前是单次
// s.client.Do,瞬时性失败(上游 EOF / 502 / 超时)直接判失败并经 OcrImage 写入
// failure 短 TTL(30s)熔断条目,导致一次抖动让同图在 30s 内都识别不出。
// 本文件提供「单次 miss 内最多 ocrMaxAttempts 次尝试 + 退避 + 总超时上界」,
// 仅覆盖瞬时性失败,确定性失败(4xx 非 429 / 编解码错 / 空候选)不重试。
//
// 设计契约(必须与 ocr_engine.go 调用方共同维持):
//   - 重试发生在 singleflight 的 call 函数体内,并发同图仍合并为 1 次系列重试;
//   - 任一尝试成功即返回(交上层写 success 长 TTL 24h),全部耗尽返回最后一次 error
//     (交上层写 failure 短 TTL 30s 熔断)—— 上层缓存契约零变动;
//   - ocrCallWithRetry 不感知协议,只接收「一次尝试」闭包并按返回错误分类决定是否再来;
//   - 重试尝试在带总超时的 ctx 下进行(NewRequestWithContext),修正此前 http.NewRequest
//     用 Background 最坏可挂 5min 的隐患。
//
// 与号池层重试的关系:OCR 经 18443/18444 进入号池后,号池自身已有重试(handler_retry_loop.go);
// OCR 侧重试 = 在号池一轮未能恢复时再发起一轮(可能换账号),属刻意分层重试,
// ocrRetryTotalTimeout 兜底避免无界放大。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ocrMaxAttempts 是单次 OCR miss 内的最大尝试次数(1 次原调 + 2 次重试)。
// 取 3:瞬时抖动(上游 EOF / 502 / 超时)通常 1 次重试即恢复,留第 3 次兜更长尾抖动;
// 再往上会放大 OCR 总延迟与上游压力,得不偿失。
const ocrMaxAttempts = 3

// ocrRetryWait 是两次尝试之间的固定退避(可被 ctx 中止)。
// 取 800ms:兼顾「给上游喘息」与「不把单图 OCR 拖到秒级体感差」;
// 不用指数退避是因为 ocrMaxAttempts 小(3 次),线性 800ms × 2 的方差足够,实现更简、可测。
//
// 声明为 var 而非 const:单测可覆盖为小值快跑重试用例,避免 800ms×2 退避把单测拖到秒级,
// 与 nvidiaStreamRetryWait(可覆盖的退避字段)同款测试可调口径。生产默认 800ms,零值兜底同值。
var ocrRetryWait = 800 * time.Millisecond

// ocrRetryTotalTimeout 是整条 OCR(含退避 + 多次尝试)的总时长上界。
// 取 30s:OCR 本质是「降级自愈」,不能让一张图的重试拖垮整个入站请求的延迟;
// 覆盖最坏 3 次「上游慢响应 + 2 段退避」后留余量,到点即中止、落占位文本兜底。
//
// 声明为 var:单测可覆盖为小值,避免超时用例等满 30s。生产默认 30s,零值兜底 30s。
var ocrRetryTotalTimeout = 30 * time.Second

// ocrRetryStatusCodes 是判定为「瞬时性、值得重试」的上游 HTTP 状态码集合。
// 429(限流,号池已切下一账号)/ 5xx(上游临时故障)→ 重试;
// 4xx 非 429(如 400 校验失败 / 401 鉴权失败 / 403 安全拦截)→ 确定性失败,不重试。
var ocrRetryStatusCodes = map[int]bool{
	http.StatusTooManyRequests:     true, // 429
	http.StatusInternalServerError: true, // 500
	http.StatusBadGateway:          true, // 502
	http.StatusServiceUnavailable:  true, // 503
	http.StatusGatewayTimeout:      true, // 504
}

// ocrFailFastStatusPrefix 是「快速失败」的错误消息子串,命中即视为确定性失败不重试。
// ocrImageUncached 对非 200 会把状态码 + 响应体拼成 "status %d: %s" 形式;
// ocrImageUncachedViaRoute 用 "ocr route service returned status %d: %s" 形式;
// 二者均会被 retryableStatusFromErr 解析。本集合覆盖常见安全拦截 / 鉴权失败标识,
// 避免 403/401 偶发被错误归进可重试 5xx 的边界(状态码本身已挡,这里是双保险)。
var ocrFailFastHints = []string{
	"403", "Forbidden",
	"401", "Unauthorized",
	"SSRF",
	"not allowed",
}

// ocrAttemptResult 是一次「真打上游」尝试的返回:正文切片 + error(可为 nil)。
// 由调用方提供的闭包产生,ocrCallWithRetry 据此决定是否重试 / 最终返回什么。
type ocrAttemptResult struct {
	text string
	err  error
}

// ocrCallWithRetry 在带总超时的 ctx 下,对一次「真打上游并解析」的闭包最多执行
// ocrMaxAttempts 次,仅对瞬时性失败重试,退避 ocrRetryWait,任一成功即返回。
//
// 入参:
//   - parent:调用方传入的 ctx,可携带入站请求的取消信号(主请求被客户端取消即中止重试)。
//     nil 时退化为 context.Background()。
//   - label:日志标识(如 "ocr" / "ocr route"),用于失败日志透出是哪条 OCR 路径。
//   - logf:OCRService.logf 的等价签名,可为 nil(单测)。
//   - attempt:一次「真上游 + 解析」的闭包。每次重试都会重新调用(可构造全新请求)。
//     闭包内若遇 transport 错误应返回带该 error 的 result;遇非 200 应把 status/正文
//     拼进 error 文本(由 isOcrRetryableErr 解析)。
//
// 返回:最后一次 attempt 的 result(成功即提前返回,而非最后一次)。
//
// 不直接持有 *OCRService,纯函数 + 注入,便于单测用合成 http.RoundTripper 注入失败而
// 无需启动 httptest 服务。
func ocrCallWithRetry(parent context.Context, label string, logf func(string, ...interface{}), attempt func(ctx context.Context) ocrAttemptResult) ocrAttemptResult {
	if attempt == nil {
		return ocrAttemptResult{err: errors.New("ocr retry: nil attempt fn")}
	}
	// 总超时上界:即便上游持续挂起,也最多 ocrRetryTotalTimeout 后中止,
	// 修正此前 http.NewRequest(Background) 最坏挂 5min 的隐患。零值兜底 30s。
	totalTimeout := ocrRetryTotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = 30 * time.Second
	}
	base := parent
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, totalTimeout)
	defer cancel()

	// 退避间隔:零值兜底 800ms,避免误装配导致无退避狂打上游。
	wait := ocrRetryWait
	if wait <= 0 {
		wait = 800 * time.Millisecond
	}

	var last ocrAttemptResult
	for i := 1; i <= ocrMaxAttempts; i++ {
		// 每次尝试用同一 ctx(NewRequestWithContext 绑定它),ctx 到点 / 主请求取消即中止。
		r := attempt(ctx)
		if r.err == nil {
			return r // 成功,交上层写 success 长 TTL。
		}
		last = r
		if !isOcrRetryableErr(r.err) {
			// 确定性失败(4xx 非 429 / 编解码 / 空候选 / SSRF)→ 不重试,返回。
			if logf != nil {
				logf("%s 上游调用确定性失败不重试(%d/%d): %v", label, i, ocrMaxAttempts, r.err)
			}
			return r
		}
		if logf != nil {
			logf("⚠️ [OCR] %s 上游瞬时失败(%d/%d),%v 后重试: %v", label, i, ocrMaxAttempts, wait, r.err)
		}
		// 退避,ctx 到点即中止(不空跑到下一次 attempt 撞 deadline)。
		select {
		case <-ctx.Done():
			return ocrAttemptResult{err: fmt.Errorf("%s 上游调用重试被中止: %w", label, ctx.Err())}
		case <-time.After(wait):
		}
	}
	if logf != nil {
		logf("❌ [OCR] %s 上游重试 %d 次仍失败: %v", label, ocrMaxAttempts, last.err)
	}
	return last
}

// isOcrRetryableErr 判定一个 OCR 上游错误是否值得重试。
//
// 瞬时性(重试):
//   - 传输层:io.EOF / io.ErrUnexpectedEOF / net.Error 的 Timeout / connection reset / 拨号超时;
//   - 上游 HTTP:retryableStatusFromErr 从 "status %d: %s" 文本解析出 429/5xx;
//   - context 超时(本应被 ctx.Done 中止,但若 Tight 端点慢回 short err 也可再来一次);
//   - Likon: 带 "EOF" / "connection reset" / "broken pipe" / "timeout" / "deadline" 文本的 error。
//
// 确定性(不重试):
//   - 4xx 非 429(状态码层挡掉,文本层双保险 ocrFailFastHints);
//   - SSRF 拒绝(errSSRFRejected)、非 image content-type(errNotImage);
//   - marshal/unmarshal 等「输入本身有问题」类 error(无 status、无网络特征)。
//
// 保守原则:拿不准就归为「不重试」,避免对确定性失败狂打上游。
func isOcrRetryableErr(err error) bool {
	if err == nil {
		return false
	}
	// SSRF 拒绝非瞬时性,不重试(避免对被守卫拒绝的目标反复拨号)。
	if errors.Is(err, errSSRFRejected) || errors.Is(err, errNotImage) {
		return false
	}
	// 显式 fail-fast 文本命中 → 确定性失败。
	low := strings.ToLower(err.Error())
	for _, hint := range ocrFailFastHints {
		if strings.Contains(low, strings.ToLower(hint)) {
			return false
		}
	}
	// 上游 HTTP 状态码解析:命中可重试状态码(429/5xx)→ 重试。
	if code, ok := retryableStatusFromErr(err); ok {
		if ocrRetryStatusCodes[code] {
			return true
		}
		// 解析到状态码但不在可重试集合(如 400/404)→ 确定性失败。
		return false
	}
	// 传输层瞬时错误:标准库 + 自定义文案双判定。
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// http.Client.Do 把连接重置 / broken pipe 等包成 *url.Error,文案里带这些关键词。
	// 「no such host」属 DNS 永久失败,刻意不在重试集合(不在此分支命中)。
	if strings.Contains(low, "connection reset") ||
		strings.Contains(low, "broken pipe") ||
		strings.Contains(low, "timeout") ||
		strings.Contains(low, "deadline") ||
		strings.Contains(low, "eof") {
		return true
	}
	return false
}

// retryableStatusFromErr 从形如 "ocr service returned status 503: ..." 或
// "... status %d: %s" 的 error 文本里解析出 HTTP 状态码。
// ocrImageUncached / ocrImageUncachedViaRoute 把非 200 响应拼成 "status %d: %s" 文本返回,
// 这里用字符串扫描定位 "status " 后面的数字,避免改这两个函数的 error 格式契约。
// 成功返回 (code, true);解析不到返回 (0, false)(交调用方走传输层判定分支)。
func retryableStatusFromErr(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	msg := err.Error()
	idx := strings.Index(msg, "status ")
	if idx < 0 {
		return 0, false
	}
	rest := msg[idx+len("status "):]
	// 读取连续数字。
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	var code int
	for _, ch := range rest[:end] {
		code = code*10 + int(ch-'0')
		if code > 9999 { // 防溢出兜底
			return 0, false
		}
	}
	return code, true
}
