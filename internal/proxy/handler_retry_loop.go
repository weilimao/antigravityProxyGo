package proxy

import (
	"antigravity-proxy/internal/account"
	"encoding/json"
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
	var lastUsedAccount *account.Account
	var lastAccountID string
	var lastAccountFailCount int

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
			available := sc.h.accountMgr.GetAvailableAccountsForChannel(retryChannel, sc.currentModel)

			// 如果通道未开启负载均衡（池模式关闭），限制 available 仅包含第一个激活账号
			// 确保所有会话和请求均使用同一个单账号
			isPoolEnabled := false
			if retryChannel == "project" {
				isPoolEnabled = sc.h.accountMgr.GetProjectPoolMode()
			} else if retryChannel == "nvidia" {
				isPoolEnabled = sc.h.accountMgr.GetNvidiaPoolMode()
			} else {
				isPoolEnabled = sc.h.accountMgr.GetPoolMode()
			}
			if !isPoolEnabled && len(available) > 0 {
				available = []*account.Account{available[0]}
			}

			lastUsedAccount = sc.h.sessionRouter.GetOrAssignAccount(sc.sessionKey, available, nil)
		}

		if lastUsedAccount != nil {
			if lastUsedAccount.ID != lastAccountID {
				lastAccountID = lastUsedAccount.ID
				lastAccountFailCount = 0
			}
		}

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

		// 如果未开启号池负载均衡（直连模式），或项目负载均衡开启但本次请求是直接透传（非模型或 Agent 请求），
		// 失败时直接退出，不执行切换账号重试
		isDirectPassthrough := sc.h.accountMgr.GetProjectPoolMode() && !isRealModelRequest(sc.targetPath) && !isAgentRequest(sc.targetPath) && sc.targetHost != "aiplatform.googleapis.com"
		if !sc.h.accountMgr.IsPoolModeForActiveChannel() || isDirectPassthrough {
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

		// Process Errors (Rate Limits, Quota Exceeded, Token Expired)
		isRetryable := errAttempt.Error() == "CAPACITY_EXHAUSTED" ||
			errAttempt.Error() == "QUOTA_EXHAUSTED" ||
			errAttempt.Error() == "RATE_LIMITED" ||
			errAttempt.Error() == "TOKEN_EXPIRED" ||
			errAttempt.Error() == "CREDITS_EXHAUSTED" ||
			errAttempt.Error() == "SERVER_ERROR" ||
			errAttempt.Error() == "STREAM_INTERRUPTED"

		if lastUsedAccount != nil {
			accId := lastUsedAccount.ID
			email := lastUsedAccount.Email

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

				go func(a *account.Account) {
					res, qErr := sc.h.quotaFetch(a)
					if qErr == nil && res != nil {
						sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
					}
				}(lastUsedAccount)
			}

			if errAttempt.Error() == "RATE_LIMITED" {
				// 瞬时速率/并发限制(classify 拆分:无 long-term 配额信号的 429 RESOURCE_EXHAUSTED)。
				// 不挂 5 分钟冷静期(会误拉黑正常账号几秒后被配额恢复日志擦除但其间连锁误伤并发请求),
				// 改挂 10s 短冷静期 + 异步 quotaFetch 真实配额兜底自愈:
				//   - 几秒后 quotaFetch 回来若配额正常,冷静期被 UpdateAccountQuota->OnQuotaRestored 提前清零;
				//   - 若配额真有问题则维持冷静期,下轮 retry 自然 reassign 切号。
				// 这把 antigravity 个人号同 sessionKey 并发挤同号产生的 200ms~3s 瞬时拒绝,
				// 与主请求占槽的并发争用隔离开,不再升级成 5 分钟拉黑 -> 子 agent 雪崩。
				sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 瞬时速率/并发限制 (RATE_LIMITED)。标记 10s 短冷静期并异步校准配额...", email))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+10*1000, sc.currentModel)

				go func(a *account.Account) {
					res, qErr := sc.h.quotaFetch(a)
					if qErr == nil && res != nil {
						sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
					}
				}(lastUsedAccount)
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" {
				sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 模型容量耗尽 (CAPACITY_EXHAUSTED，服务超载或限频)。标记临时冷静期并同步获取真实配额...", email))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, sc.currentModel)

				res, qErr := sc.h.quotaFetch(lastUsedAccount)
				if qErr == nil && res != nil {
					sc.h.accountMgr.UpdateAccountQuota(lastUsedAccount.ID, res)
					// 重新检查当前冷静期状态，确认是否清零
					refreshedAcc := sc.h.accountMgr.GetAccountByID(lastUsedAccount.ID)
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
			} else if errAttempt.Error() == "QUOTA_EXHAUSTED" {
				sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 配额已耗尽 (QUOTA_EXHAUSTED)。标记冷静期并获取真实配额...", email))
				sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, sc.currentModel)

				go func(a *account.Account) {
					res, qErr := sc.h.quotaFetch(a)
					if qErr == nil && res != nil {
						sc.h.accountMgr.UpdateAccountQuota(a.ID, res)
					}
				}(lastUsedAccount)
			}

			if errAttempt.Error() == "SERVER_ERROR" {
				lastAccountFailCount++
				if lastAccountFailCount >= 3 {
					sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到服务器错误 (%d) 达到 %d 次。标记临时冷静期 (60s) 并切换账号重试...", email, status, lastAccountFailCount))
					sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+60*1000, sc.currentModel)
				} else {
					sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 遇到服务器错误 (%d)（第 %d/3 次）。不标记冷静期，将继续用当前账号尝试...", email, status, lastAccountFailCount))
				}
			}

			if errAttempt.Error() == "STREAM_INTERRUPTED" {
				lastAccountFailCount++
				if lastAccountFailCount >= 3 {
					sc.h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到流式中断达到 %d 次。标记临时冷静期 (60s) 并切换账号重试...", email, lastAccountFailCount))
					sc.h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+60*1000, sc.currentModel)
				} else {
					sc.h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 遇到流式中断（第 %d/3 次）。不标记冷静期，将继续用当前账号尝试...", email, lastAccountFailCount))
				}
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" || errAttempt.Error() == "QUOTA_EXHAUSTED" {
				sc.h.accountMgr.RecordAccountError(accId, status, sc.currentModel, sc.h.logFn)
			}
		}

		shouldRetry := isRetryable && attempt < maxRetries
		if errAttempt.Error() == "QUOTA_EXHAUSTED" {
			// If all active accounts of the target channel are cooled down, do not retry further
			targetChan := sc.h.getGoogleChannel()
			if sc.targetHost == "aiplatform.googleapis.com" {
				targetChan = "project"
			}
			hasAvail := false
			for _, a := range sc.h.accountMgr.GetRawAccounts() {
				if a.Provider != targetChan || !a.Enabled {
					continue
				}
				cat := sc.h.accountMgr.GetModelCategory(sc.currentModel)
				cooldown := int64(0)
				if a.Cooldowns != nil {
					if c, ok := a.Cooldowns[cat]; ok {
						cooldown = c
					}
				} else {
					cooldown = a.CooldownUntil
				}
				if cooldown == 0 || time.Now().UnixNano()/1e6 >= cooldown {
					hasAvail = true
					break
				}
			}
			if !hasAvail {
				shouldRetry = false
			}
		}

		if shouldRetry {
			jitter := rand.Float64() * 500.0
			maxDelayMs := float64(sc.h.getMaxRetryDelay() * 1000)
			delay := math.Min(float64(sc.h.statsTracker.GetTotalRetries()*1000), maxDelayMs) + jitter
			sc.h.logFn(fmt.Sprintf("%s ⚠️ 请求失败 (%s)。将在 %dms 后自动切换账号重试...", sc.logPrefix, errAttempt.Error(), int(delay)))

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
			return
		}
	}
}
