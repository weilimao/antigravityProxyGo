package relay

import (
	"fmt"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// passthrough_usage.go: Other 号池(通用透传转发器)的用量记录与请求日志落库。
// 对偶 nvidia_usage.go 的 recordNvidiaUsage 五落点链路:
//
//  落点1  中继用户维度统计(relay_stats.json) + 按 API Key 限额回填;
//  落点2  号池成员账号维度统计(usage.json, 前端「账号使用统计」页);
//  落点3  全局「使用趋势」综合桶(stats.Tracker.TrackRequestForModel → 顶部指标卡 + stats.Models 模型表 + trends 综合趋势);
//  落点4  请求日志(stats.Tracker.AddRequestLogForFamily → 仪表盘「请求日志」列表 + db 落库);
//
// 与 NVIDIA 的关键差异:Other 号池上游是第三方 OpenAI/Anthropic 兼容端点,可能支持 cache
// (cachedTokens 语义各异),本函数统一按 cachedTokens=0 / CacheStatus="NONE" 保守处理
// (同 NVIDIA 口径,避免误计缓存命中率)。Family 记 "other",供前端按族隔离/筛选,
// 复用既有 RequestLog 的 family 列,不污染 NVIDIA/gemini/claude 各族。

// passthroughLogCtx 是 Other 号池请求日志上下文,与 nvidiaLogCtx 同构,经各回写路径
// 传给 recordOtherUsage 装配 stats.RequestLog。
type passthroughLogCtx struct {
	Method       string
	Host         string
	Path         string
	SessionID    string
	Account      string
	StatusCode   int
	StartTs      time.Time
	FirstByteRec *stats.FirstByteRecorder
}

// otherReqLogSeq 是 Other 号池请求日志(落点4)的全局原子递增序列, 用于生成稳定且无碰撞的
// RequestLog.ID。语义与 nvidiaReqLogSeq 相同: 单独纳秒时间戳高并发易碰撞, 叠加单调递增
// 序列后即使同纳秒也唯一; ID 用 "otherlog-" 前缀与 OTHER 号池请求日志命名空间对齐排查。
var otherReqLogSeq uint64

// recordOtherUsage 记录 Other 号池一次成功请求的用量到五处落点。
// 与 recordNvidiaUsage 对偶:input==0 && output==0 时整函数早退(避免空桶/噪声)。
// userSession 为 nil 时跳过落点1/2(单测/未注入场景),但不影响落点3/4
// (globalStatsTracker 注入后仍可记录, 与 recordNvidiaUsage 降级语义一致)。
// cached 为上游回报的缓存命中 token(cachedTokens), 决定 CacheStatus 与缓存命中率口径:
// >0 → "HIT", 否则维持 "NONE"(兼容 OpenRF 之下无 cache 的第三方上游)。
func (h *APICompatHandler) recordOtherUsage(userSession *RelaySession, model string, input, output, cached int, poolAccount *account.Account, logCtx passthroughLogCtx) {
	if input == 0 && output == 0 {
		return
	}

	// 1) 中继用户维度统计(relay_stats.json) + 按 API Key 限额回填。
	// 模型名不前缀 "other/" —— 与 gemini/claude 直连链路同构, 使 TotalCacheEligibleInputTokens
	// (缓存命中率分母)正常累加(Other 上游可能支持 cache, 与 NVIDIA 刻意排除口径相反)。
	if h.statsTracker != nil && userSession != nil {
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:      fmt.Sprintf("other-%d", time.Now().UnixNano()),
			UserID:     userSession.UserID,
			UserKey:    userSession.UserKey,
			ModelName:  model,
			InTokens:   input,
			OutTokens:  output,
			CachedTokens: cached,
			Method:     "POST",
			Host:       logCtx.Host,
			Path:       logCtx.Path,
			StatusCode: 200,
		})

		// 单 API Key 的用量回填(与 NVIDIA / gemini/claude 链路对齐)。
		if h.authMgr != nil && h.authMgr.userMgr != nil && userSession.APIKeyID != "" {
			h.authMgr.userMgr.RecordAPIKeyUsage(userSession.UserID, userSession.APIKeyID, false, int64(input+output))
		}
	}

	// 2) 号池成员账号维度统计(usage.json) —— 复用既有的 usageTracker(账号使用统计页)。
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

	// 3/4) 全局综合统计 + 请求日志(globalStatsTracker)。
	// 落点3 使 Other 号池请求首次计入顶部指标卡 + 模型统计表 + 综合趋势桶;
	// 落点4 把 Other 请求写入仪表盘「请求日志」列表(绕过 AddRequestLog 的 isRealModel 过滤,
	// 由 family="other" 显式入库, 与 NVIDIA 的 AddRequestLogForFamily 同策略)。
	if h.globalStatsTracker != nil {
		h.globalStatsTracker.TrackRequestForModel(model, input, output, cached)

		// 号池命中率筛选: Other 池按组细分为 other:<groupId> 桶(与 TrackRequestForModel 并行,
		// 只写 Pools 子聚合, 不动全局标量)。poolAccount nil 时 Other 无"直连归 other"语义, 跳过
		// (避免误归 antigravity 桶串扰)。GroupID 缺失兜底 other:__unknown__。
		if poolAccount != nil {
			poolKey := stats.PoolKeyForProvider(poolAccount.Provider, poolAccount.GroupID)
			h.globalStatsTracker.TrackRequestForPool(model, input, output, cached, poolKey)
		}

		// DurationMs 采用「第一帧→流结束」的流式耗时口径(StreamDurationMs, 不含 TTFT,
		// 与前端「响应时间」列语义分离); TTFT(FirstByteMs) 仍为请求→首帧的端到端截断。
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
			ID:           fmt.Sprintf("otherlog-%d-%d", atomic.AddUint64(&otherReqLogSeq, 1), time.Now().UnixNano()),
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
			Family:       "other",
		}
		h.globalStatsTracker.AddRequestLogForFamily(reqLog)
	}
}

// passthroughHostFromBaseURL 从上游账号 BaseURL 提取裸 host(与 nvidiaHostFromBaseURL 同构,
// 但回退占位用 "other" 而非 "nvidia")。复用 nvidiaHostFromBaseURL 的解析逻辑, 保持口径一致。
func passthroughHostFromBaseURL(baseURL string) string {
	h := nvidiaHostFromBaseURL(baseURL)
	if h == "" || h == "nvidia" {
		return "other"
	}
	return h
}