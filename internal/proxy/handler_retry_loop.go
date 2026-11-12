package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
	"encoding/json"
	"strings"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// handler_retry_loop.go: runRetryLoop —— 重试循环 + 账号冷静期 + Token 刷新 + 退避。
// 从 ServeHTTP 的 retry 段逐行搬移,逻辑零回归。
// maxRetries/lastUsedAccount/lastAccountID/lastAccountFailCount 为方法内局部(不跨阶段);
// 调用 sc.attemptRequest(attempt) 与 sc.logRequestToTracker(...)。

func (sc *serveContext) runRetryLoop(w http.ResponseWriter, r *http.Request) {
	maxRetries := sc.h.getMaxRetries()
	if !sc.h.isRetryEnabled() {
		maxRetries = 0
	}
	var poolErrorRule *settings.PoolErrorRetryConfig
	if sc.h.isRetryEnabled() && sc.h.SettingsMgr != nil {
		rule := sc.h.SettingsMgr.GetPoolErrorRetry()
		if rule.Enabled {
			poolErrorRule = &rule
			if rule.MaxRetries > 0 {
				maxRetries = rule.MaxRetries
			}
		}
	}
	var lastUsedAccount *account.Account
	var lastAccountID string
	var lastAccountFailCount int
	var sameAccountStartAttempt int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-sc.r.Context().Done():
			sc.h.logFn(fmt.Sprintf("%s ⏹️ 客户端已取消连接，终止负载均衡重试。", sc.logPrefix))
			return
		default:
		}

		sc.currentAttemptIndex = attempt
		if attempt > 0 {
			sc.h.logFn(fmt.Sprintf("%s ⚖️ 正在进行负载均衡第 %d/%d 次尝试...", sc.logPrefix, attempt+1, maxRetries+1))
		}

		// Fetch current active account mapping reference
		usePoolForRetry := false
		var retryChannel string

		isPoolReq := isRealModelRequest(sc.targetPath) || isAgentRequest(sc.targetPath) || sc.targetHost == "aiplatform.googleapis.com"
		if isPoolReq {
			usePoolForRetry = true
			retryChannel = sc.h.getGoogleChannel()
		}

		if usePoolForRetry {
			available, retryLimit := sc.selectCandidatePoolAccounts(retryChannel)

			// 并发过滤(与 routeForAttempt 同口径):避免二次 GetOrAssignAccount 的 lastUsedAccount
			// 指向已过滤的超限账号 → 误冷却。本循环仅做 sticky 跟踪(供下方冷静期/Token 刷新寻址),
			// 不 acquire/release(routeForAttempt+forwardForAttempt 已严格配对)。
			filtered := sc.h.accountMgr.FilterByConcurrency(available, retryLimit)
			if len(filtered) > 0 {
				lastUsedAccount = sc.h.sessionRouter.GetOrAssignAccount(sc.sessionKey, filtered, nil)
			} else if overAcc := sc.h.accountMgr.LeastLoadedAccount(available); overAcc != nil {
				lastUsedAccount = overAcc
			}
		}

		if lastUsedAccount != nil {
			if lastUsedAccount.ID != lastAccountID {
				lastAccountID = lastUsedAccount.ID
				lastAccountFailCount = 0
				sameAccountStartAttempt = attempt
			}
		}
		sameAccountLimit := computeSameAccountLimit(maxRetries, sameAccountStartAttempt)

		status, headers, body, isStreamed, errAttempt := sc.attemptRequest(attempt)

		if errAttempt == nil {
			// Successful request
			if lastUsedAccount != nil {
				sc.h.accountMgr.ResetAccountError(lastUsedAccount.ID)
			}
			sc.logRequestToTracker(status, "")

			if !isStreamed {
				// Write response back to client
				for k, values := range headers {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(status)
				w.Write(body)
			}
			return
		}

		if sc.r.Context().Err() != nil {
			sc.h.logFn(fmt.Sprintf("%s ⏹️ 客户端已取消连接，终止后续 503 重试与账号切换。", sc.logPrefix))
			return
		}

		// 如果全局停用了代理重试，请求不走任何代理软件重试与切号，默认透传上游原生响应
		if !sc.h.isRetryEnabled() {
			sc.h.logFn(fmt.Sprintf("%s ❌ [代理重试已停用] 上游请求失败，透传原生响应: %v", sc.logPrefix, errAttempt))
			sc.logRequestToTracker(status, errAttempt.Error())

			if !sc.headersSent {
				for k, values := range headers {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				statusCode := status
				if statusCode <= 0 {
					statusCode = http.StatusBadGateway
				}
				w.WriteHeader(statusCode)
				w.Write(body)
			}
			return
		}

		// 如果未开启号池负载均衡（直连模式），或项目负载均衡开启但本次请求是直接透传（非模型或 Agent 请求），
		// 或本次请求是 Google 侧遥测/埋点端点（recordCodeAssistMetrics 等，命中 isIgnoredTelemetry），
		// 失败时直接退出，不执行切换账号重试 ——
		// 避免上游瞬时 5xx 被拉成 21 次空转重试，并在耗尽后向客户端伪造 429 配额耗尽（IDE 误判）。
		isDirectPassthrough := sc.h.accountMgr.GetProjectPoolMode() && !isRealModelRequest(sc.targetPath) && !isAgentRequest(sc.targetPath) && sc.targetHost != "aiplatform.googleapis.com"
		isTelemetryEndpoint := isIgnoredTelemetry(sc.targetPath)
		if !sc.h.accountMgr.IsPoolModeForActiveChannel() || isDirectPassthrough || isTelemetryEndpoint {
			sc.h.logFn(fmt.Sprintf("%s ❌ [直连模式] 尝试失败: %v", sc.logPrefix, errAttempt))
			sc.logRequestToTracker(status, errAttempt.Error())

			if !sc.headersSent {
				for k, values := range headers {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				statusCode := status
				if statusCode <= 0 {
					statusCode = http.StatusBadGateway
				}
				w.WriteHeader(statusCode)
				w.Write(body)
			}
			return
		}

		// Process Errors (Rate Limits, Quota Exceeded, Token Expired, Custom Rules)
		errStr := errAttempt.Error()
		isCustomSwitch := strings.HasPrefix(errStr, "CUSTOM_SWITCH_ACCOUNT")
		isCustomRetry := strings.HasPrefix(errStr, "CUSTOM_RETRY_ACCOUNT")

		isRetryable := isCustomSwitch ||
			isCustomRetry ||
			errStr == "MODEL_NOT_SUPPORTED" ||
			errStr == "CAPACITY_EXHAUSTED" ||
			errStr == "QUOTA_EXHAUSTED" ||
			errStr == "RATE_LIMITED" ||
			errStr == "TOKEN_EXPIRED" ||
			errStr == "CREDITS_EXHAUSTED" ||
			errStr == "SERVER_ERROR" ||
			errStr == "STREAM_INTERRUPTED"

		targetChan := sc.h.getGoogleChannel()
		if sc.targetHost == "aiplatform.googleapis.com" {
			targetChan = "project"
		}
		switchedAccount := false

		if lastUsedAccount != nil {
			accId := lastUsedAccount.ID
			email := lastUsedAccount.Email

			if errAttempt.Error() == "MODEL_NOT_SUPPORTED" {
				sc.h.logFn(fmt.Sprintf("🚫 [模型能力学习] 账号 %s 不支持模型 %s (上游 404)，已标记 24h 模型级屏蔽并自动切换支持该模型的账号重试...", email, sc.currentModel))
				sc.h.accountMgr.MarkAccountModelUnsupported(accId, sc.currentModel, 24*time.Hour)
				sc.h.sessionRouter.UnbindSession(sc.sessionKey)
				sc.markAccountExcluded(accId)
				switchedAccount = true
			}

			if errAttempt.Error() == "TOKEN_EXPIRED" && sc.h.tokenRefresh != nil {
				sc.h.logFn(fmt.Sprintf("🔑 [负载均衡] 检测到账号 %s Token 已过期 (401)。正在自动刷新...", email))
				newToken, refreshErr := sc.h.tokenRefresh(lastUsedAccount)
				if refreshErr == nil {
					lastUsedAccount.SetAccessToken(newToken)
					sc.h.accountMgr.UpdateAccessToken(accId, newToken)
					sc.h.logFn(fmt.Sprintf("🔑 [负载均衡] 账号 %s Token 自动刷新成功，即将重试...", email))
				} else {
					sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s Token 自动刷新失败: %v", email, refreshErr))
				}
			}

			if errAttempt.Error() == "CREDITS_EXHAUSTED" {
				sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 检测到账号 %s 积分已耗尽。标记冷静期并获取真实配额...", email))
				sc.h.accountMgr.UpdateAccountCredits(accId, 0)
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, sc.currentModel)
				if sc.hasAlternativeAccount(targetChan, accId) {
					sc.markAccountExcluded(accId)
					switchedAccount = true
				}

				if sc.h.quotaFetch != nil {
					go func(a *account.Account) {
						res, qErr := sc.h.quotaFetch(a)
						if qErr == nil && res != nil {
							sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
						}
					}(lastUsedAccount)
				}
			}

			if errAttempt.Error() == "RATE_LIMITED" {
				// 瞬时速率/并发限制(classify 拆分:无 long-term 配额信号的 429 RESOURCE_EXHAUSTED)。
				// 不挂 5 分钟冷静期(会误拉黑正常账号几秒后被配额恢复日志擦除但其间连锁误伤并发请求),
				// 改挂 10s 短冷静期 + 异步 quotaFetch 真实配额兜底自愈:
				//   - 几秒后 quotaFetch 回来若配额正常,冷静期被 UpdateAccountQuota->OnQuotaRestored 提前清零;
				//   - 若配额真有问题则维持冷静期,下轮 retry 自然 reassign 切号。
				sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 瞬时速率/并发限制 (RATE_LIMITED)。标记 10s 短冷静期并异步校准配额...", email))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+10*1000, sc.currentModel)
				if sc.hasAlternativeAccount(targetChan, accId) {
					sc.markAccountExcluded(accId)
					switchedAccount = true
				}

				if sc.h.quotaFetch != nil {
					go func(a *account.Account) {
						res, qErr := sc.h.quotaFetch(a)
						if qErr == nil && res != nil {
							sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
						}
					}(lastUsedAccount)
				}
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" {
				lastAccountFailCount++
				hasAlt := sc.hasAlternativeAccount(targetChan, accId)
				if hasAlt && lastAccountFailCount >= sameAccountLimit {
					sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到模型容量耗尽 (CAPACITY_EXHAUSTED) 达到 %d/%d 次。标记临时冷静期 (60s) 并切换账号重试...", email, lastAccountFailCount, sameAccountLimit))
					sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+60*1000, sc.currentModel)
					sc.markAccountExcluded(accId)
					switchedAccount = true
				} else {
					sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 模型容量耗尽 (CAPACITY_EXHAUSTED，第 %d/%d 次)。标记临时冷静期并同步获取真实配额...", email, lastAccountFailCount, sameAccountLimit))
					sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, sc.currentModel)
					sc.syncVerifyQuotaOnCapacityExhausted(lastUsedAccount, email)
				}
			} else if errAttempt.Error() == "QUOTA_EXHAUSTED" {
				sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 配额已耗尽 (QUOTA_EXHAUSTED)。标记冷静期并获取真实配额...", email))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, sc.currentModel)
				if sc.hasAlternativeAccount(targetChan, accId) {
					sc.markAccountExcluded(accId)
					switchedAccount = true
				}

				if sc.h.quotaFetch != nil {
					go func(a *account.Account) {
						res, qErr := sc.h.quotaFetch(a)
						if qErr == nil && res != nil {
							sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
						}
					}(lastUsedAccount)
				}
			}

			if errAttempt.Error() == "SERVER_ERROR" {
				lastAccountFailCount++
				if sc.handleThresholdRetrySwitch(accId, email, "服务器错误", status, lastAccountFailCount, sameAccountLimit, settings.DefaultSwitchCooldownSecs, targetChan) {
					switchedAccount = true
				}
			}

			if errAttempt.Error() == "STREAM_INTERRUPTED" {
				lastAccountFailCount++
				if sc.handleThresholdRetrySwitch(accId, email, "流式中断", status, lastAccountFailCount, sameAccountLimit, settings.DefaultSwitchCooldownSecs, targetChan) {
					switchedAccount = true
				}
			}

			if isCustomSwitch {
				cooldownSecs := settings.DefaultSwitchCooldownSecs
				if poolErrorRule != nil && poolErrorRule.SwitchCooldownSecs >= 0 {
					cooldownSecs = poolErrorRule.SwitchCooldownSecs
				}
				sc.markAccountExcluded(accId)
				switchedAccount = true
				if cooldownSecs > 0 {
					sc.h.logFn(fmt.Sprintf("🔀 [负载均衡] 账号 %s 遇到报错 (%d) 命中自定义切号规则。设置临时冷静期 (%ds) 并切换下一个账号重试...", email, status, cooldownSecs))
					sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+int64(cooldownSecs)*1000, sc.currentModel)
				} else {
					sc.h.logFn(fmt.Sprintf("🔀 [负载均衡] 账号 %s 遇到报错 (%d) 命中自定义切号规则 (不设全局冷静期，仅本次重试切换)...", email, status))
				}
			}

			if isCustomRetry {
				lastAccountFailCount++
				cooldownSecs := settings.DefaultSwitchCooldownSecs
				if poolErrorRule != nil && poolErrorRule.SwitchCooldownSecs >= 0 {
					cooldownSecs = poolErrorRule.SwitchCooldownSecs
				}
				if sc.handleThresholdRetrySwitch(accId, email, "自定义重试报错", status, lastAccountFailCount, sameAccountLimit, cooldownSecs, targetChan) {
					switchedAccount = true
				}
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" || errAttempt.Error() == "QUOTA_EXHAUSTED" {
				sc.h.accountMgr.RecordAccountError(accId, status, sc.currentModel, sc.h.logFn)
			}
		}

		shouldRetry := isRetryable && attempt < maxRetries
		if errAttempt.Error() == "MODEL_NOT_SUPPORTED" || isCustomSwitch {
			if !sc.hasAnyNonExcludedAccount(targetChan) {
				shouldRetry = false
			}
		}
		if errAttempt.Error() == "QUOTA_EXHAUSTED" {
			// If all active accounts of the target channel are cooled down or excluded, do not retry further
			if !sc.hasAnyNonExcludedAccount(targetChan) {
				shouldRetry = false
			}
		}

		if shouldRetry {
			jitter := rand.Float64() * 500.0
			maxDelayMs := float64(sc.h.getMaxRetryDelay() * 1000)
			delay := math.Min(float64(sc.h.statsTracker.GetTotalRetries()*1000), maxDelayMs) + jitter
			if isCustomSwitch || (isCustomRetry && switchedAccount) {
				sc.h.logFn(fmt.Sprintf("%s 🔀 请求遇到状态码 %d，将在 %dms 后切换下一个账号重试...", sc.logPrefix, status, int(delay)))
			} else if isCustomRetry {
				sc.h.logFn(fmt.Sprintf("%s 🔁 请求遇到状态码 %d，命中自定义重试规则。将在 %dms 后当前账号重试...", sc.logPrefix, status, int(delay)))
			} else if switchedAccount {
				sc.h.logFn(fmt.Sprintf("%s ⚠️ 请求失败 (%s)。将在 %dms 后自动切换账号重试...", sc.logPrefix, errAttempt.Error(), int(delay)))
			} else {
				sc.h.logFn(fmt.Sprintf("%s ⚠️ 请求失败 (%s)。将在 %dms 后重试...", sc.logPrefix, errAttempt.Error(), int(delay)))
			}

			sc.h.errLogger.Log("RETRY", sc.targetPath, sc.currentModel, sc.allocatedAccount, attempt+1, errAttempt.Error())
			select {
			case <-sc.r.Context().Done():
				sc.h.logFn(fmt.Sprintf("%s ⚠️ 请求在等待重试时被客户端取消", sc.logPrefix))
				return
			case <-time.After(time.Duration(delay) * time.Millisecond):
			}
		} else {
			sc.h.logFn(fmt.Sprintf("%s ❌ [负载均衡] 尝试失败: %v", sc.logPrefix, errAttempt))
			sc.logRequestToTracker(429, errAttempt.Error())

			if !sc.headersSent {
				if (isCustomSwitch || isCustomRetry || errStr == "MODEL_NOT_SUPPORTED") && status > 0 && len(body) > 0 {
					for k, values := range headers {
						for _, v := range values {
							w.Header().Add(k, v)
						}
					}
					w.WriteHeader(status)
					w.Write(body)
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(429)
					errResp := map[string]interface{}{
						"error": map[string]interface{}{
							"code":    429,
							"message": "Active accounts quota exhausted",
							"status":  "RESOURCE_EXHAUSTED",
						},
					}
					b, _ := json.Marshal(errResp)
					w.Write(b)
				}
			}
			return
		}
	}
}

// computeSameAccountLimit 根据总重试上限与当前账号切入位置计算本账号允许的连续同号重试阈值，
// 保证不会因单个账号耗尽全部全局重试次数而丧失切换至下一可用账号的机会。
func computeSameAccountLimit(maxRetries, sameAccountStartAttempt int) int {
	if maxRetries <= 0 {
		return 1
	}
	limit := 3
	if remainingBudget := maxRetries - sameAccountStartAttempt; remainingBudget > 0 && remainingBudget < limit {
		limit = remainingBudget
	}
	return limit
}

// syncVerifyQuotaOnCapacityExhausted 在遇到 CAPACITY_EXHAUSTED 且尚未达到切号阈值时同步查询配额，
// 若账号自身配额确实正常则自动解除 5 分钟误冷却以支持原号短重试。
func (sc *serveContext) syncVerifyQuotaOnCapacityExhausted(acc *account.Account, email string) {
	if sc.h.quotaFetch == nil || acc == nil {
		return
	}
	res, qErr := sc.h.quotaFetch(acc)
	if qErr == nil && res != nil {
		sc.h.accountMgr.UpdateAccountQuota(acc.ID, res)
		refreshedAcc := sc.h.accountMgr.GetAccountByID(acc.ID)
		if refreshedAcc != nil {
			cat := sc.h.accountMgr.GetModelCategory(sc.currentModel)
			cooldown := int64(0)
			if refreshedAcc.Cooldowns != nil {
				if c, ok := refreshedAcc.Cooldowns[cat]; ok {
					cooldown = c
				}
			} else {
				cooldown = refreshedAcc.CooldownUntil
			}
			if cooldown == 0 {
				sc.h.logFn(fmt.Sprintf("✅ [负载均衡] 账号 %s 额度充足，已同步解除冷静期，恢复可用状态。", email))
			}
		}
	} else if qErr != nil {
		sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 同步刷新配额失败: %v", email, qErr))
	}
}

// handleThresholdRetrySwitch 统一处理同号连续失败（SERVER_ERROR / STREAM_INTERRUPTED / 自定义重试）后的阈值切号判定：
// 达到同号重试上限且号池仍有其他可用账号时，挂临时冷静期（若 >0）并加入本请求排除集合，返回 true；
// 若号池无其他可用账号则保留原号继续完成剩余重试机会，返回 false。
func (sc *serveContext) handleThresholdRetrySwitch(accId, email, errKind string, status, failCount, sameAccountLimit, cooldownSecs int, targetChan string) bool {
	statusDesc := ""
	if status > 0 {
		statusDesc = fmt.Sprintf(" (%d)", status)
	}
	if failCount >= sameAccountLimit {
		if sc.hasAlternativeAccount(targetChan, accId) {
			if cooldownSecs > 0 {
				sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到%s%s达到 %d/%d 次。标记临时冷静期 (%ds) 并切换账号重试...", email, errKind, statusDesc, failCount, sameAccountLimit, cooldownSecs))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+int64(cooldownSecs)*1000, sc.currentModel)
			} else {
				sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到%s%s达到 %d/%d 次。不设全局冷静期，本次请求内切换下一账号重试...", email, errKind, statusDesc, failCount, sameAccountLimit))
			}
			sc.markAccountExcluded(accId)
			return true
		}
		sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 账号 %s 连续遇到%s%s达到 %d/%d 次，当前号池无其他可用账号，将继续用当前账号尝试...", email, errKind, statusDesc, failCount, sameAccountLimit))
		return false
	}
	sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 遇到%s%s（第 %d/%d 次）。不标记冷静期，将继续用当前账号尝试...", email, errKind, statusDesc, failCount, sameAccountLimit))
	return false
}
