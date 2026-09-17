package relay

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// workbuddy_usage.go: 腾讯 WorkBuddy 号池的请求日志/统计落库链路。
//
// 与 grok_usage.go / nvidia_usage.go / passthrough_usage.go 保持对偶形态:
//   - relay 维度模型名带 "workbuddy/" 前缀, 并在请求日志 Family="workbuddy" 显式入库;
//   - 号池成员账号维度(usage.json)去前缀展示上游真实模型名(deepseek-v4.1-flash 等);
//   - 全局综合统计(TrackRequestForModel + TrackRequestForPool("workbuddy"))把 WorkBuddy 用量计入
//     顶部指标卡 / 模型统计表 / 综合趋势桶 / 缓存命中率「workbuddy」子桶;
//   - 请求日志调用 AddRequestLogForFamily(reqLog), 使前端「使用详情」面板能完整展示请求明细。

var workbuddyReqLogSeq uint64

type workbuddyLogCtx struct {
	Method          string
	Host            string
	Path            string
	SessionID       string
	Account         string
	StatusCode      int
	StartTs         time.Time
	FirstByteRec    *stats.FirstByteRecorder
	ReqBody         interface{}
	ReqHeaders      interface{}
	ReasoningEffort string
}

func workbuddyHostFromBaseURL(baseURL string) string {
	h := nvidiaHostFromBaseURL(baseURL)
	if h == "" || h == "nvidia" {
		return "workbuddy"
	}
	return h
}

// recordWorkBuddyUsage 记录 WorkBuddy 一次成功请求的用量到各落点。
func (h *APICompatHandler) recordWorkBuddyUsage(userSession *RelaySession, model string, input, output, cached int, poolAccount *account.Account, logCtx workbuddyLogCtx) {
	// 测速回环探测请求(IsBenchmark)不计入任何统计, 不污染仪表盘请求/成功率/Token。
	if userSession != nil && userSession.IsBenchmark {
		return
	}

	usageAvailable := !(input == 0 && output == 0)

	cleanModel := strings.TrimSpace(model)
	cleanModel = strings.TrimPrefix(cleanModel, "workbuddy/")
	cleanModel = strings.TrimPrefix(cleanModel, "wb/")

	displayModel := model
	if !strings.HasPrefix(displayModel, "workbuddy/") && !strings.HasPrefix(displayModel, "wb/") {
		displayModel = "workbuddy/" + displayModel
	}

	// 1. 中继用户维度统计 (relay_stats.json) + API Key 限额回填
	if usageAvailable && h.statsTracker != nil && userSession != nil {
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:        fmt.Sprintf("wb-%d", time.Now().UnixNano()),
			UserID:       userSession.UserID,
			UserKey:      userSession.UserKey,
			ModelName:    displayModel,
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

	// 2. 账号池成员维度统计 (usage.json)
	if usageAvailable && h.usageTracker != nil && userSession != nil {
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
			ModelName:    cleanModel,
			InTokens:     input,
			OutTokens:    output,
			CachedTokens: cached,
			Account:      accMeta,
		})
	}

	// 3. 全局综合统计 (globalStatsTracker) 与 请求日志落库
	if h.globalStatsTracker != nil {
		h.globalStatsTracker.TrackRequestForModel(displayModel, input, output, cached)
		if poolAccount != nil {
			h.globalStatsTracker.TrackRequestForPool(displayModel, input, output, cached, "workbuddy")
		}

		// 4. 请求日志 (AddRequestLogForFamily, family="workbuddy")
		end := time.Now()
		endToEndMs := end.Sub(logCtx.StartTs).Milliseconds()
		durationMs := int64(0)
		if logCtx.FirstByteRec != nil {
			durationMs = logCtx.FirstByteRec.StreamDurationMs(end)
		}
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
		seq := atomic.AddUint64(&workbuddyReqLogSeq, 1)
		reqLog := &stats.RequestLog{
			ID:             fmt.Sprintf("wblog-%d-%d", seq, rand.Intn(1000)),
			Timestamp:      time.Now().Format("01/02 15:04:05"),
			Method:         logCtx.Method,
			Host:           logCtx.Host,
			Path:           logCtx.Path,
			Model:          displayModel,
			InTokens:       input,
			OutTokens:      output,
			CachedTokens:   cached,
			CacheStatus:    cacheStatus,
			StatusCode:     logCtx.StatusCode,
			Account:        logCtx.Account,
			RequestBody:    logCtx.ReqBody,
			RequestHeaders: logCtx.ReqHeaders,
			SessionID:      logCtx.SessionID,
			DurationMs:     durationMs,
			FirstByteMs:    firstByteMs,
			Family:         "workbuddy",
			ReasoningEffort: logCtx.ReasoningEffort,
		}
		h.globalStatsTracker.AddRequestLogForFamily(reqLog)
	}
}
