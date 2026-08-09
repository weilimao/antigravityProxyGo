package relay

import (
	"fmt"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// google_usage.go: Antigravity 号池直连出站的用量记录与请求日志落库。
// 对偶 passthrough_usage.go 的 recordOtherUsage / nvidia_usage.go 的 recordNvidiaUsage,
// 补上 handleV1Internal 直连出站(不经本地 18443 代理)这一处的按池记账缺口。
//
// 为什么只在 handleV1Internal 直连路径接入(防双计设计, 关键约束):
//   - handleOpenAIChat / handleAnthropicMessages 的 dispatchToGemini 恒把请求转给本地 18443
//     代理(localProxyAddr), 由 proxy.handler_attempt_classify.go:146-198 在 generatecontent-200
//     时已统一完成 TrackRequest + TrackRequestForPool + usageTracker.RecordUsage +
//     relayStatsCallback + AddRequestLog 全部记账(那是 8/8 按池筛选上线后截图里 41 reqs 的来源)。
//     同一事务在 relay 层再记一遍 → 每笔请求的 antigravity 桶/全局/请求日志全部翻倍, 故这里不接入。
//   - handleV1Internal 的 Provider∈{antigravity, project, 其余} 分支是 relay 侧自行选号、
//     直接向 daily-cloudcode-pa / aiplatform / generativelanguage 出站, 18443 代理从未见到
//     这笔请求: relay_stats.json(中继用户维) 之外, stats.json 的 Pools["antigravity"] /
//     Models / trends / 请求日志 / usage.json(账号维) 全部缺失。这是「缓存命中率」卡片
//     Antigravity 池自 8/8 起只有几百万 token 残缺快照的第二大主因(第一大由 stats.go
//     BackfillPoolFromModels 存量回填解决)。
//   - 故本文件只由 compat_v1internal.go 的直连出站成功路径调用, 五落点记账(对标
//     recordNvidiaUsage/recordOtherUsage); 不要在任何经 18443 的路径上调用本函数。
//
// 落点:
//  落点1  中继用户维度统计(relay_stats.json) + 按 API Key 限额回填;
//  落点2  号池成员账号维度统计(usage.json, 前端「账号使用统计」页);
//  落点3  全局综合统计(stats.Tracker.TrackRequestForModel → 顶部指标卡 + Models 模型表 + trends);
//  落点4  按池命中率子聚合(stats.Tracker.TrackRequestForPool → Pools["..."], 与
//         proxy.classifyResponse 的 TrackRequestForPool 同 machinery, 只写池桶不动全局标量);
//  落点5  请求日志(stats.Tracker.AddRequestLogForFamily → 仪表盘「请求日志」, family="antigravity")。

// googleLogCtx 是 Antigravity 直连出站请求日志上下文, 与 nvidiaLogCtx / passthroughLogCtx
// 同构, 经 handleV1Internal 装配后传给 recordGoogleUsage 构造 stats.RequestLog。
type googleLogCtx struct {
	Method       string
	Host         string
	Path         string
	SessionID    string
	Account      string
	StatusCode   int
	StartTs      time.Time
	FirstByteRec *stats.FirstByteRecorder
}

// googleReqLogSeq 是 Antigravity 直连请求日志(落点5)的全局原子递增序列, 语义与
// nvidiaReqLogSeq / otherReqLogSeq 相同(纳秒时间戳高并发易碰撞, 叠加单调序列保唯一;
// ID 用 "aglog-" 前缀与 antigravity 号池日志命名空间对齐排查)。
var googleReqLogSeq uint64

// recordGoogleUsage 记录 Antigravity 号池直连出站一次成功请求的用量到五处落点。
// 与 recordNvidiaUsage / recordOtherUsage 对偶: input==0 && output==0 时整函数早退。
// userSession 为 nil 时跳过落点1/2, 不影响落点3/4/5(globalStatsTracker 注入后仍可记录)。
// poolAccount 为所选直连账号: 用于落点2 的 AccountMeta 与落点4 的池 key(PoolKeyForProvider;
// antigravity/project/google/gemini-cli/空 provider 一律归 Pools["antigravity"])。
// 注意: 本函数只应被 handleV1Internal 直连出站路径调用——任何经 18443 的请求已由
// proxy.classifyResponse 记过, 在此再记会造成 antigravity 池桶/全局/请求日志全部翻倍。
func (h *APICompatHandler) recordGoogleUsage(userSession *RelaySession, model string, input, output, cached int, poolAccount *account.Account, logCtx googleLogCtx) {
	if input == 0 && output == 0 {
		return
	}

	// 落点1 中继用户维度统计(relay_stats.json) + 按 API Key 限额回填。
	if h.statsTracker != nil && userSession != nil {
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:        fmt.Sprintf("ag-%d", time.Now().UnixNano()),
			UserID:       userSession.UserID,
			UserKey:      userSession.UserKey,
			ModelName:    model,
			InTokens:     input,
			OutTokens:    output,
			CachedTokens: cached,
			Method:       "POST",
			Host:         logCtx.Host,
			Path:         logCtx.Path,
			StatusCode:   200,
		})

		if h.authMgr != nil && h.authMgr.userMgr != nil && userSession.APIKeyID != "" {
			h.authMgr.userMgr.RecordAPIKeyUsage(userSession.UserID, userSession.APIKeyID, false, int64(input+output))
		}
	}

	// 落点2 号池成员账号维度统计(usage.json)。
	if h.usageTracker == nil || userSession == nil {
		return
	}
	var accMeta *stats.AccountMeta
	if poolAccount != nil {
		accMeta = &stats.AccountMeta{
			ID:        poolAccount.ID,
			Email:     poolAccount.Email,
			Provider:  poolAccount.Provider,
			ProjectID: poolAccount.ProjectID,
			ScopeType: poolAccount.ScopeType,
		}
	}
	h.usageTracker.RecordUsage(stats.UsageSample{
		ModelName:    model,
		InTokens:     input,
		OutTokens:    output,
		CachedTokens: cached,
		Account:      accMeta,
	})

	// 落点3/4/5 全局综合统计 + 按池命中率子聚合 + 请求日志。
	if h.globalStatsTracker != nil {
		h.globalStatsTracker.TrackRequestForModel(model, input, output, cached)

		// 落点4 号池命中率筛选: 直连出站与 proxy.classifyResponse 同 machinery,
		// 池 key 由 poolAccount.Provider 推(antigravity/project/google/gemini-cli/空 → antigravity)。
		poolKey := stats.PoolKeyForProvider(poolAccount.Provider, "")
		h.globalStatsTracker.TrackRequestForPool(model, input, output, cached, poolKey)

		// DurationMs 采用「第一帧→流结束」的流式耗时口径; TTFT(FirstByteMs) 端到端截断。
		end := time.Now()
		endToEndMs := end.Sub(logCtx.StartTs).Milliseconds()
		durationMs := logCtx.FirstByteRec.StreamDurationMs(end)
		if durationMs <= 0 {
			durationMs = 1
		}
		var firstByteMs int64
		if logCtx.FirstByteRec != nil {
			firstByteMs = logCtx.FirstByteRec.FirstByteMs(endToEndMs)
		} else {
			firstByteMs = endToEndMs
		}
		cacheStatus := "NONE"
		if cached > 0 {
			cacheStatus = "HIT"
		}
		reqLog := &stats.RequestLog{
			ID:           fmt.Sprintf("aglog-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&googleReqLogSeq, 1)),
			Timestamp:    time.Now().Format("01/02 15:04:05"),
			Method:       logCtx.Method,
			Host:         logCtx.Host,
			Path:         logCtx.Path,
			Model:        model,
			InTokens:     input,
			OutTokens:    output,
			CachedTokens: cached,
			CacheStatus:  cacheStatus,
			StatusCode:   logCtx.StatusCode,
			Account:      logCtx.Account,
			SessionID:    logCtx.SessionID,
			DurationMs:   durationMs,
			FirstByteMs:  firstByteMs,
			Family:       "antigravity",
		}
		h.globalStatsTracker.AddRequestLogForFamily(reqLog)
	}
}

// googleUsageTailMaxBytes 是 handleV1Internal 流式拷贝时累计响应尾部用于解析用量的上限。
// Gemini/v1internal 的 usageMetadata 恒在响应末尾(非流式为 JSON 末尾, 流式为核心 SSE 末帧),
// 只需保留末尾 512KB 即可解析出 promptTokenCount / candidatesTokenCount / cachedContentTokenCount,
// 避免像 proxy 那样仅追前 1MB 而错过超大响应的尾部 usage。
const googleUsageTailMaxBytes = 512 * 1024

// googleTailWriter 是「最近 N 字节」尾部环形缓冲写入器: 恒最多保留 maxLen 字节,
// 供 handleV1Internal 在流式/非流式拷贝的同时透过 io.MultiWriter 收集响应尾部。
// 设计要点:
//   - 无随响应体增长的内存占用(恒 ≤ maxLen), 超大非流式 JSON 响应也不 OOM;
//   - usageMetadata 恒在响应末尾, 循环结束后一次 Bytes() 解析即为权威末帧;
//   - 与 proxy 侧仅追前 1MB 不同, 这里刻意存「尾部」——usage 计数器在末尾, 而非开头。
type googleTailWriter struct {
	buf        []byte
	start, len int
}

// newGoogleTailWriter 构造尾部环形缓冲写入器, maxLen 为保留的最大字节数(必>0)。
func newGoogleTailWriter(maxLen int) *googleTailWriter {
	return &googleTailWriter{buf: make([]byte, maxLen)}
}

// Write 把 p 追加进环形缓冲; 超过容量时丢弃最旧的字节, 保留最近 maxLen 字节。
// io.Writer 约定: 返回 len(p) 且 nil error(w 侧不截断, 上层 MultiWriter 仍透传原文)。
func (w *googleTailWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		w.buf[w.start] = b
		w.start = (w.start + 1) % len(w.buf)
		if w.len < len(w.buf) {
			w.len++
		}
	}
	return len(p), nil
}

// Bytes 返回当前保留的字节序列(按实际写入顺序, 非环形地址序)。
// 容量为 0 时不返回 nil(与 bytes.Buffer 的零值语义兼容: 空非 nil 切片)。
func (w *googleTailWriter) Bytes() []byte {
	n := w.len
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		idx := (w.start + len(w.buf) - n + i) % len(w.buf)
		out[i] = w.buf[idx]
	}
	return out
}

// 包级正则: 与 proxy/helpers.go 同款口径, relay 侧无法接触 proxy 私有变量, 故本地复刻。
// 解析 v1internal/Gemini 响应中的 usageMetadata 字段, 取「最后一个」匹配(流式下末帧权威)。
var (
	googRePromptTokens    = regexp.MustCompile(`"promptTokenCount"\s*:\s*(\d+)`)
	googReCandidateTokens = regexp.MustCompile(`"candidatesTokenCount"\s*:\s*(\d+)`)
	googReCachedTokens    = regexp.MustCompile(`"cachedContentTokenCount"\s*:\s*(\d+)`)
)

// googleUsageFromRaw 从响应体字节中解析用量 (in/out/cached)。
// findLastInt 复刻 proxy.classifyResponse 的取末个匹配语义: 流式 SSE 每帧都会带 usageMetadata,
// 帧间可能逐帧递增, 末帧为权威累计值。解析失败/缺字段返回 0, 由调用方 input==0&&output==0 早退。
func googleUsageFromRaw(raw []byte) (in, out, cached int) {
	if pm := googRePromptTokens.FindAllSubmatch(raw, -1); len(pm) > 0 {
		if v, err := strconv.Atoi(string(pm[len(pm)-1][1])); err == nil {
			in = v
		}
	}
	if cm := googReCandidateTokens.FindAllSubmatch(raw, -1); len(cm) > 0 {
		if v, err := strconv.Atoi(string(cm[len(cm)-1][1])); err == nil {
			out = v
		}
	}
	if cc := googReCachedTokens.FindAllSubmatch(raw, -1); len(cc) > 0 {
		if v, err := strconv.Atoi(string(cc[len(cc)-1][1])); err == nil {
			cached = v
		}
	}
	return in, out, cached
}