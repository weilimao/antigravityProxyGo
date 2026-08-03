package proxy

import (
	"antigravity-proxy/internal/stats"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// handler_attempt_classify.go: classifyResponse —— 响应分类 + token 计数 + 抓包落库 + relay 回调。
// 从 ServeHTTP 闭包 attemptRequest 的 classify 段逐行搬移,逻辑零回归。
//
// 阶段衔接契约:
//   - 仅当 forwardForAttempt 未 finalized(BuildReq/Do 失败/流式断流/流式中断/流式 disconnect/
//     非流式读失败 等已 finalized 直返)时才进入本阶段,故原 closure 的 `errRead != nil 早返回` 已由
//     forward 的非流式读失败 finalized 兜底,此处不再重复判 errRead。
//   - forward 通过 forwardOutcome 携带 status/headers/body/streamed 进入;本阶段不可再访问 resp /
//     respBodyBytes / errRead(已不在作用域),统一读 fo.*。
//   - SavePacket 在本阶段入口(对应原 closure 内 L1246),统一前置,与原控制流逐行一致。

func (sc *serveContext) classifyResponse(attemptIndex int, ro *routeOutcome, fo forwardOutcome) (int, map[string][]string, []byte, bool, error) {
	status := fo.status
	headers := fo.headers
	body := fo.body
	isStreamed := fo.streamed

	// 非流式响应提取 thoughtSignature 并缓存，下次请求注入到 functionCall parts
	if isRealModelRequest(ro.targetPath) && sc.rawSessionKey != "" {
		GetSignatureCache().ExtractFromFullResponse(body, sc.rawSessionKey)
	}

	// Capture packet logging (Save to PacketCapturer) before any error short-circuit return
	sc.h.packetCap.SavePacket(sc.r.Method, ro.targetHost, ro.targetPath, ro.customHeaders, ro.finalReqBody, headers, body, status)

	if status >= 400 {
		if !(status == 429 && strings.Contains(ro.targetPath, "retrieveUserQuota")) {
			bodySnippet := string(decompressIfNeeded(body, headers))
			if len(bodySnippet) > 1000 {
				bodySnippet = bodySnippet[:1000] + "... (truncated)"
			}
			sc.h.logFn(fmt.Sprintf("%s ⚠️ 上游 HTTP %d 错误响应: %s", sc.logPrefix, status, bodySnippet))
		}
	}

	if status == 401 {
		return 401, headers, body, false, errors.New("TOKEN_EXPIRED")
	}

	// Handle Google Quota 429 Interception to prevent IDE infinite loop
	if status == 429 && strings.Contains(ro.targetPath, "retrieveUserQuota") {
		sc.h.logFn("⚠️ Intercepted 429 from Google Quota API. Mocking 200 OK to prevent IDE infinite loop.")
		mockQuotaResponse := map[string]interface{}{
			"quotaSummaries": []interface{}{
				map[string]interface{}{"model": "Gemini Weekly Quota", "usedFraction": 1.0},
				map[string]interface{}{"model": "Gemini 5-Hour Quota", "usedFraction": 1.0},
				map[string]interface{}{"model": "Claude Weekly Quota", "usedFraction": 1.0},
				map[string]interface{}{"model": "Claude 5-Hour Quota", "usedFraction": 1.0},
			},
			"groups": []interface{}{
				map[string]interface{}{
					"displayName": "Gemini Models",
					"buckets": []interface{}{
						map[string]interface{}{"displayName": "Weekly Limit", "remainingFraction": 0.0},
						map[string]interface{}{"displayName": "Five Hour Limit", "remainingFraction": 0.0},
					},
				},
				map[string]interface{}{
					"displayName": "Claude and GPT models",
					"buckets": []interface{}{
						map[string]interface{}{"displayName": "Weekly Limit", "remainingFraction": 0.0},
						map[string]interface{}{"displayName": "Five Hour Limit", "remainingFraction": 0.0},
					},
				},
			},
		}
		mockBytes, _ := json.Marshal(mockQuotaResponse)
		headersCopy := make(map[string][]string)
		for k, v := range headers {
			headersCopy[k] = v
		}
		headersCopy["Content-Length"] = []string{strconv.Itoa(len(mockBytes))}
		headersCopy["Content-Type"] = []string{"application/json"}
		return 200, headersCopy, mockBytes, false, nil
	}

	// 429 Quota Error
	if (status == 429 || status == 403 || status == 402) && !strings.Contains(ro.targetPath, "retrieveUserQuota") {
		bodyStr := string(body)
		bodyStrLower := strings.ToLower(bodyStr)
		isCreditExempt := strings.Contains(bodyStrLower, "credit") || strings.Contains(bodyStrLower, "balance") || strings.Contains(bodyStrLower, "overage") || strings.Contains(bodyStrLower, "insufficient credits") || strings.Contains(bodyStrLower, "insufficient balance") || strings.Contains(bodyStrLower, "insufficient funds") || strings.Contains(bodyStrLower, "billing")

		if isCreditExempt {
			return status, headers, body, false, errors.New("CREDITS_EXHAUSTED")
		}

		if status == 429 || status == 403 || status == 402 {
			bodyStrLower := strings.ToLower(bodyStr)
			// 区分「真配额耗尽」与「瞬时速率/并发限制」:
			// antigravity 个人号(v1internal 上游)在同一 sessionKey 瞬时并发打同一账号时,
			// Google 回 429 RESOURCE_EXHAUSTED 但 body 内无任何 long-term 配额信号
			// (无 resetTime / quota limit / daily / concurrent 等字样),仅通用 exhausted 语。
			// 这种 3~5 秒后异步 quotaFetch 即测得配额正常(见 retry loop「配额恢复」日志),
			// 实质是瞬时速率/并发槽满,而非长期配额耗尽。
			// 旧逻辑一律判 QUOTA_EXHAUSTED -> 挂 5 分钟冷静期 -> 主+子并发挤同号时雪崩。
			// 现拆分:仅当 body 含明确长期配额信号(daily/resetTime/quota limit/duration/sustainable)
			// 才判 QUOTA_EXHAUSTED;否则判 RATE_LIMITED,由 retry loop 短冷静期(10s)+ 异步 quotaFetch
			// 兜底自愈,避免把几秒的瞬时拒绝延展成 5 分钟拉黑。
			isLongTermQuota := strings.Contains(bodyStrLower, "daily") ||
				strings.Contains(bodyStrLower, "resettime") ||
				strings.Contains(bodyStrLower, "reset_time") ||
				strings.Contains(bodyStrLower, "quota limit") ||
				strings.Contains(bodyStrLower, "duration") ||
				strings.Contains(bodyStrLower, "sustainable") ||
				strings.Contains(bodyStrLower, "per day") ||
				strings.Contains(bodyStrLower, "perminutequota")

			if isLongTermQuota {
				return 429, headers, body, false, errors.New("QUOTA_EXHAUSTED")
			}

			// 通用速率/资源瞬时限制(MODEL_CAPACITY_EXHAUSTED 走 503 分支,此处不命中):
			// RESOURCE_EXHAUSTED / exhausted / limit / rate / quota(无 long-term 信号时)统一归 RATE_LIMITED。
			isRateLimited := strings.Contains(bodyStr, "RESOURCE_EXHAUSTED") || strings.Contains(bodyStr, "RATE_LIMIT_EXCEEDED") || strings.Contains(bodyStr, "exhausted") || strings.Contains(bodyStr, "limit") || strings.Contains(bodyStrLower, "rate") || strings.Contains(bodyStrLower, "quota")
			if isRateLimited {
				return 429, headers, body, false, errors.New("RATE_LIMITED")
			}
		}
	}

	// 503 Capacity Exhausted
	if status == 503 {
		if strings.Contains(string(body), "MODEL_CAPACITY_EXHAUSTED") {
			return 503, headers, body, false, errors.New("CAPACITY_EXHAUSTED")
		}
	}

	// Server Errors (500 Internal Server Error, 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout)
	if status == 500 || status == 502 || status == 503 || status == 504 {
		return status, headers, body, false, errors.New("SERVER_ERROR")
	}

	// Analyze normal token counts from response body (if success)
	if status == 200 && strings.Contains(strings.ToLower(ro.targetPath), "generatecontent") {
		bodyStr := string(decompressIfNeeded(body, headers))
		pm := rePromptTokens.FindAllStringSubmatch(bodyStr, -1)
		cm := reCandidateTokens.FindAllStringSubmatch(bodyStr, -1)
		cc := reCachedTokens.FindAllStringSubmatch(bodyStr, -1)

		if len(pm) > 0 && len(pm[len(pm)-1]) > 1 {
			sc.inTokens, _ = strconv.Atoi(pm[len(pm)-1][1])
		}
		if len(cm) > 0 && len(cm[len(cm)-1]) > 1 {
			sc.outTokens, _ = strconv.Atoi(cm[len(cm)-1][1])
		}
		if len(cc) > 0 && len(cc[len(cc)-1]) > 1 {
			sc.cachedTokens, _ = strconv.Atoi(cc[len(cc)-1][1])
		}

		if sc.inTokens > 0 || sc.outTokens > 0 {
			sc.h.statsTracker.TrackRequest(sc.currentModel, sc.inTokens, sc.outTokens, sc.cachedTokens)
			if sc.relayUserID != "" && sc.h.relayStatsCallback != nil {
				reqID := sc.r.Header.Get("X-Antigravity-Req-ID")
				var headerKeys []string
				for k := range sc.r.Header {
					headerKeys = append(headerKeys, fmt.Sprintf("%s=%v", k, sc.r.Header.Values(k)))
				}

				sc.h.relayStatsCallback(sc.allocatedAccount, sc.relayUserID, sc.relayAPIKeyID, sc.currentModel, sc.inTokens, sc.outTokens, sc.cachedTokens,
					sc.r.Method, sc.r.Host, sc.r.URL.Path, sc.sessionKey, time.Since(sc.startTime).Milliseconds(), status, reqID)
			}
			var accMeta *stats.AccountMeta
			if ro.poolAccount != nil {
				accMeta = &stats.AccountMeta{
					ID:        ro.poolAccount.ID,
					Email:     ro.poolAccount.Email,
					Provider:  ro.poolAccount.Provider,
					ProjectID: ro.poolAccount.ProjectID,
					ScopeType: ro.poolAccount.ScopeType,
				}
			}
			sc.h.usageTracker.RecordUsage(stats.UsageSample{
				ModelName:    sc.currentModel,
				InTokens:     sc.inTokens,
				OutTokens:    sc.outTokens,
				CachedTokens: sc.cachedTokens,
				Account:      accMeta,
			})

			hitRate := 0.0
			if sc.inTokens > 0 {
				hitRate = float64(sc.cachedTokens) / float64(sc.inTokens) * 100.0
			}
			sc.h.logFn(fmt.Sprintf("📊 [%s] Usage: %d In | %d Out | %d Cached (Hit rate: %.1f%%)", sc.currentModel, sc.inTokens, sc.outTokens, sc.cachedTokens, hitRate))
		}
	}

	return status, headers, body, isStreamed && status == 200, nil
}
