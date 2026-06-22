package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

// 包级正则变量：避免每次请求重复编译，消除 GC 压力
var (
	reModelInPath     = regexp.MustCompile(`/models/([^:]+)`)
	reModelInBody     = regexp.MustCompile(`(?:models/)?(.+)`)
	rePromptTokens    = regexp.MustCompile(`"promptTokenCount"\s*:\s*(\d+)`)
	reCandidateTokens = regexp.MustCompile(`"candidatesTokenCount"\s*:\s*(\d+)`)
	reCachedTokens    = regexp.MustCompile(`"cachedContentTokenCount"\s*:\s*(\d+)`)
)

type ProxyHandler struct {
	accountMgr   *account.Manager
	sessionRouter *session.Router
	statsTracker  *stats.Tracker
	usageTracker  *stats.UsageTracker
	errLogger    *stats.RetryErrorLogger
	packetCap    *stats.PacketCapturer
	logFn        func(string)
	quotaFetch   func(*account.Account) (*account.QuotaResult, error)
	tokenRefresh func(*account.Account) (string, error)
	setCapturedProject func(string, string)
	getStoredProject func(string) string
	client       *http.Client
}

func NewProxyHandler(
	accountMgr *account.Manager,
	sessionRouter *session.Router,
	statsTracker *stats.Tracker,
	usageTracker *stats.UsageTracker,
	errLogger *stats.RetryErrorLogger,
	packetCap *stats.PacketCapturer,
	logFn func(string),
	quotaFetch func(*account.Account) (*account.QuotaResult, error),
	tokenRefresh func(*account.Account) (string, error),
	setCapturedProject func(string, string),
	getStoredProject func(string) string,
) *ProxyHandler {
	return &ProxyHandler{
		accountMgr:    accountMgr,
		sessionRouter: sessionRouter,
		statsTracker:  statsTracker,
		usageTracker:  usageTracker,
		errLogger:     errLogger,
		packetCap:     packetCap,
		logFn:         logFn,
		quotaFetch:    quotaFetch,
		tokenRefresh:  tokenRefresh,
		setCapturedProject: setCapturedProject,
		getStoredProject: getStoredProject,
		client:        netutil.NewClient(5 * time.Minute),
	}
}

func isIgnoredTelemetry(path string) bool {
	return strings.Contains(path, "v1internal") && !strings.Contains(path, "retrieveUserQuota")
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method == http.MethodConnect {
		http.Error(w, "CONNECT not supported inside Decrypted Server", http.StatusBadRequest)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	// Capture active project from request body
	if len(bodyBytes) > 0 {
		var bodyJson struct {
			Project string `json:"project"`
		}
		if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Project != "" {
			isDefault := bodyJson.Project == "expanded-palisade-stpfc" || strings.HasPrefix(bodyJson.Project, "expanded-palisade-")
			if !isDefault {
				email := "default"
				authHeader := r.Header.Get("Authorization")
				if strings.HasPrefix(authHeader, "Bearer ") {
					token := authHeader[7:]
					for _, acc := range h.accountMgr.GetRawAccounts() {
						if acc.GetAccessToken() == token {
							email = acc.Email
							break
						}
					}
				}
				// Let quotaService capture project ID
				if h.logFn != nil {
					h.logFn(fmt.Sprintf("🛡️ Captured project ID '%s' for %s", bodyJson.Project, email))
				}
				if h.setCapturedProject != nil {
					h.setCapturedProject(email, bodyJson.Project)
				}
			}
		}
	}

	targetHost := "cloudcode-pa.googleapis.com"
	targetPath := r.URL.Path + r.URL.RawQuery
	if r.URL.RawQuery != "" {
		targetPath = r.URL.Path + "?" + r.URL.RawQuery
	}

	if r.Host != "" {
		targetHost = strings.Split(r.Host, ":")[0]
	}

	// Local mapping fallback
	if targetHost == "127.0.0.1" || targetHost == "localhost" {
		ua := strings.ToLower(r.Header.Get("User-Agent"))
		if strings.Contains(r.URL.Path, "generativelanguage") || strings.Contains(r.URL.Path, "models") {
			targetHost = "generativelanguage.googleapis.com"
		} else if strings.Contains(r.URL.Path, "daily-cloudcode-pa") || strings.Contains(ua, "antigravity") {
			targetHost = "daily-cloudcode-pa.googleapis.com"
		} else {
			targetHost = "cloudcode-pa.googleapis.com"
		}
	}

	logPrefix := fmt.Sprintf("[%s -> %s%s]", r.Method, targetHost, r.URL.Path)

	currentModel := "unknown"
	modelMatch := reModelInPath.FindStringSubmatch(targetPath)
	if len(modelMatch) > 1 {
		currentModel = modelMatch[1]
	} else if strings.Contains(targetPath, "streamGenerateContent") {
		currentModel = "antigravity-core"
		if len(bodyBytes) > 0 {
			var bodyJson struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Model != "" {
				m := reModelInBody.FindStringSubmatch(bodyJson.Model)
				if len(m) > 1 {
					currentModel = m[1]
				}
			}
		}
	}

	sessionKey := h.sessionRouter.ExtractSessionKey(r, bodyBytes)

	var inTokens, outTokens, cachedTokens int
	var logged bool
	var allocatedAccount string
	var currentAttemptIndex int

	logRequestToTracker := func(statusCode int, errDetail string) {
		if logged {
			return
		}
		logged = true

		cacheStatus := "NONE"
		if statusCode == 200 && strings.Contains(targetPath, "GenerateContent") {
			if cachedTokens > 0 {
				cacheStatus = "HIT"
			} else {
				cacheStatus = "MISS"
			}
		} else if strings.Contains(targetPath, "GenerateContent") {
			cacheStatus = "MISS"
		}

		var reqBody interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
				reqBody = string(bodyBytes)
			}
		}

		if statusCode >= 400 && !isIgnoredTelemetry(targetPath) {
			h.statsTracker.TrackError(1)
			errReason := errDetail
			if errReason == "" {
				switch statusCode {
				case 429:
					errReason = "QUOTA_EXHAUSTED / RATE_LIMIT"
				case 503:
					errReason = "CAPACITY_EXHAUSTED / SERVICE_UNAVAILABLE"
				case 401:
					errReason = "TOKEN_EXPIRED / UNAUTHORIZED"
				default:
					errReason = fmt.Sprintf("HTTP Status %d", statusCode)
				}
			}
			h.errLogger.Log("ERROR", targetPath, currentModel, allocatedAccount, currentAttemptIndex+1, errReason)
		}

		headersMap := make(map[string]interface{})
		for k, v := range r.Header {
			if len(v) > 0 {
				headersMap[k] = v[0]
			}
		}

		h.statsTracker.AddRequestLog(&stats.RequestLog{
			ID:             fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Intn(1000)),
			Timestamp:      time.Now().Format("01/02 15:04:05"),
			Method:         r.Method,
			Host:           targetHost,
			Path:           targetPath,
			Model:          currentModel,
			Account:        allocatedAccount,
			InTokens:       inTokens,
			OutTokens:      outTokens,
			CachedTokens:   cachedTokens,
			CacheStatus:    cacheStatus,
			StatusCode:     statusCode,
			RequestBody:    reqBody,
			RequestHeaders: headersMap,
			SessionID:      sessionKey,
			DurationMs:     time.Since(startTime).Milliseconds(),
		})
	}

	attemptRequest := func(attemptIndex int) (int, map[string][]string, []byte, bool, error) {
		if attemptIndex > 0 && !isIgnoredTelemetry(targetPath) {
			h.statsTracker.TrackRetry(1)
		}

		customHeaders := make(http.Header)
		for k, values := range r.Header {
			customHeaders[k] = values
		}
		customHeaders.Set("Host", targetHost)

		var poolAccount *account.Account
		if h.accountMgr.GetPoolMode() || h.accountMgr.GetProjectPoolMode() {
			available := h.accountMgr.GetAvailableAccounts(currentModel)
			poolAccount = h.sessionRouter.GetOrAssignAccount(sessionKey, available, h.logFn)
			if poolAccount == nil {
				return 0, nil, nil, false, errors.New("QUOTA_EXHAUSTED")
			}
		}

		var finalReqBody = bodyBytes
		if poolAccount != nil {
			customHeaders.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
			allocatedAccount = poolAccount.Email

			if attemptIndex == 0 {
				h.logFn(fmt.Sprintf("⚖️ [负载均衡] 请求已分配账号: %s (%s) | 目标模型: %s", poolAccount.Email, poolAccount.Provider, currentModel))
			} else {
				h.logFn(fmt.Sprintf("⚖️ [负载均衡] 请求重试，重新分配账号: %s (%s) | 目标模型: %s", poolAccount.Email, poolAccount.Provider, currentModel))
			}

			// Let quotaService capture project ID using the authenticated pool account email
			if len(bodyBytes) > 0 {
				var bodyJson struct {
					Project string `json:"project"`
				}
				if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Project != "" {
					isDefault := bodyJson.Project == "expanded-palisade-stpfc" || strings.HasPrefix(bodyJson.Project, "expanded-palisade-")
					if !isDefault {
						if h.setCapturedProject != nil {
							h.setCapturedProject(poolAccount.Email, bodyJson.Project)
						}
					}
				}
			}

			// Rewrite project field in JSON payload
			if len(bodyBytes) > 0 && strings.Contains(customHeaders.Get("Content-Type"), "json") {
				var bodyMap map[string]interface{}
				if json.Unmarshal(bodyBytes, &bodyMap) == nil {
					targetProject := ""
					if poolAccount.Provider != "antigravity" {
						targetProject = poolAccount.ProjectID
					} else {
						// For antigravity, try to get stored custom project ID first
						customProj := poolAccount.ProjectID
						if customProj == "" && h.getStoredProject != nil {
							customProj = h.getStoredProject(poolAccount.Email)
						}

						if customProj != "" && customProj != "expanded-palisade-stpfc" && !strings.HasPrefix(customProj, "expanded-palisade-") {
							// If we have a custom project, use it!
							targetProject = customProj
						} else {
							// Check if this is a premium account (Pro/Ultra/Enterprise)
							isPremium := poolAccount.Tier == "Pro" || poolAccount.Tier == "Ultra" || poolAccount.Tier == "Enterprise"

							// Otherwise, strip default project ID to avoid 429
							if origProj, exists := bodyMap["project"].(string); exists {
								if origProj == "expanded-palisade-stpfc" || strings.HasPrefix(origProj, "expanded-palisade-") {
									if isPremium {
										// For premium accounts without a custom project ID, keep the default project ID to allow Pro limits
										targetProject = origProj
									} else {
										// For free accounts, strip it to avoid shared default project quota 429
										targetProject = ""
									}
								} else {
									targetProject = origProj
								}
							}
						}
					}

					bodyChanged := false
					if targetProject != "" {
						if _, exists := bodyMap["project"]; exists && bodyMap["project"] != targetProject {
							bodyMap["project"] = targetProject
							bodyChanged = true
						}
					} else {
						if _, exists := bodyMap["project"]; exists {
							delete(bodyMap, "project")
							bodyChanged = true
						}
					}

					if bodyChanged {
						newBodyBytes, errMarshal := json.Marshal(bodyMap)
						if errMarshal == nil {
							finalReqBody = newBodyBytes
							customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
							if attemptIndex == 0 && targetProject != "" {
								h.logFn(fmt.Sprintf("🛡️ Injected project ID '%s' into payload.", targetProject))
							} else if attemptIndex == 0 {
								h.logFn("🛡️ Stripped 'project' ID from payload to avoid default project quota 429.")
							}
						}
					}
				}
			}
		}

		// Forward request
		targetUrl := "https://" + targetHost + targetPath
		proxyReq, errReq := http.NewRequest(r.Method, targetUrl, bytes.NewReader(finalReqBody))
		if errReq != nil {
			return 0, nil, nil, false, errReq
		}
		proxyReq.Header = customHeaders

		resp, errDo := h.client.Do(proxyReq)
		if errDo != nil {
			return 0, nil, nil, false, errDo
		}
		defer resp.Body.Close()

		var respBodyBytes []byte
		var errRead error
		isStreaming := strings.Contains(targetPath, "streamGenerateContent")

		if isStreaming && resp.StatusCode == 200 {
			// Copy headers to writer
			for k, values := range resp.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(resp.StatusCode)

			var accumulatedBytes bytes.Buffer
			flusher, hasFlusher := w.(http.Flusher)
			if hasFlusher {
				flusher.Flush()
			}

			buf := make([]byte, 4096)
			for {
				n, errR := resp.Body.Read(buf)
				if n > 0 {
					_, writeErr := w.Write(buf[:n])
					if writeErr != nil {
						break
					}
					accumulatedBytes.Write(buf[:n])
					if hasFlusher {
						flusher.Flush()
					}
				}
				if errR != nil {
					if errR != io.EOF {
						h.logFn(fmt.Sprintf("%s ⚠️ Read error during streaming: %v", logPrefix, errR))
					}
					break
				}
			}
			respBodyBytes = accumulatedBytes.Bytes()
		} else {
			respBodyBytes, errRead = io.ReadAll(resp.Body)
		}

		if errRead != nil {
			return resp.StatusCode, nil, nil, false, errRead
		}

		if resp.StatusCode == 401 {
			return 401, resp.Header, respBodyBytes, false, errors.New("TOKEN_EXPIRED")
		}

		// Handle Google Quota 429 Interception to prevent IDE infinite loop
		if resp.StatusCode == 429 && strings.Contains(targetPath, "retrieveUserQuota") {
			h.logFn("⚠️ Intercepted 429 from Google Quota API. Mocking 200 OK to prevent IDE infinite loop.")
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
			for k, v := range resp.Header {
				headersCopy[k] = v
			}
			headersCopy["Content-Length"] = []string{strconv.Itoa(len(mockBytes))}
			headersCopy["Content-Type"] = []string{"application/json"}
			return 200, headersCopy, mockBytes, false, nil
		}

		// 429 Quota Error
		if (resp.StatusCode == 429 || resp.StatusCode == 403 || resp.StatusCode == 402) && !strings.Contains(targetPath, "retrieveUserQuota") {
			bodyStr := string(respBodyBytes)
			bodyStrLower := strings.ToLower(bodyStr)
			isCreditExempt := strings.Contains(bodyStrLower, "credit") || strings.Contains(bodyStrLower, "balance") || strings.Contains(bodyStrLower, "overage") || strings.Contains(bodyStrLower, "insufficient")

			if isCreditExempt {
				return resp.StatusCode, resp.Header, respBodyBytes, false, errors.New("CREDITS_EXHAUSTED")
			}

			if resp.StatusCode == 429 {
				isQuotaError := strings.Contains(bodyStr, "RESOURCE_EXHAUSTED") || strings.Contains(bodyStr, "quota") || strings.Contains(bodyStr, "exhausted") || strings.Contains(bodyStr, "limit") || strings.Contains(bodyStr, "MODEL_CAPACITY_EXHAUSTED")
				if isQuotaError {
					return 429, resp.Header, respBodyBytes, false, errors.New("QUOTA_EXHAUSTED")
				}
			}
		}

		// 503 Capacity Exhausted
		if resp.StatusCode == 503 {
			if strings.Contains(string(respBodyBytes), "MODEL_CAPACITY_EXHAUSTED") {
				return 503, resp.Header, respBodyBytes, false, errors.New("CAPACITY_EXHAUSTED")
			}
		}

		// Server Errors (500 Internal Server Error, 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout)
		if resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			return resp.StatusCode, resp.Header, respBodyBytes, false, errors.New("SERVER_ERROR")
		}

		// Capture packet logging (Save to PacketCapturer)
		if resp.StatusCode == 200 && !isIgnoredTelemetry(targetPath) {
			if !h.packetCap.IsCaptured(r.Method, targetHost, targetPath) {
				h.packetCap.SavePacket(r.Method, targetHost, targetPath, r.Header, bodyBytes, resp.Header, respBodyBytes, resp.StatusCode)
			}
		}

		// Analyze normal token counts from response body (if success)
		if resp.StatusCode == 200 && strings.Contains(targetPath, "GenerateContent") {
			bodyStr := string(respBodyBytes)
			pm := rePromptTokens.FindAllStringSubmatch(bodyStr, -1)
			cm := reCandidateTokens.FindAllStringSubmatch(bodyStr, -1)
			cc := reCachedTokens.FindAllStringSubmatch(bodyStr, -1)

			if len(pm) > 0 && len(pm[len(pm)-1]) > 1 {
				inTokens, _ = strconv.Atoi(pm[len(pm)-1][1])
			}
			if len(cm) > 0 && len(cm[len(cm)-1]) > 1 {
				outTokens, _ = strconv.Atoi(cm[len(cm)-1][1])
			}
			if len(cc) > 0 && len(cc[len(cc)-1]) > 1 {
				cachedTokens, _ = strconv.Atoi(cc[len(cc)-1][1])
			}

			if inTokens > 0 || outTokens > 0 {
				h.statsTracker.TrackRequest(currentModel, inTokens, outTokens, cachedTokens)
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
					ModelName:    currentModel,
					InTokens:     inTokens,
					OutTokens:    outTokens,
					CachedTokens: cachedTokens,
					Account:      accMeta,
				})

				hitRate := 0.0
				if inTokens > 0 {
					hitRate = float64(cachedTokens) / float64(inTokens) * 100.0
				}
				h.logFn(fmt.Sprintf("📊 [%s] Usage: %d In | %d Out | %d Cached (Hit rate: %.1f%%)", currentModel, inTokens, outTokens, cachedTokens, hitRate))
			}
		}

		return resp.StatusCode, resp.Header, respBodyBytes, isStreaming && resp.StatusCode == 200, nil
	}

	maxRetries := 20
	var lastUsedAccount *account.Account
	var lastAccountID string
	var lastAccountFailCount int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		currentAttemptIndex = attempt
		if attempt > 0 {
			h.logFn(fmt.Sprintf("%s ⚖️ 正在进行负载均衡第 %d/%d 次尝试...", logPrefix, attempt+1, maxRetries+1))
		}

		// Fetch current active account mapping reference
		if h.accountMgr.GetPoolMode() || h.accountMgr.GetProjectPoolMode() {
			available := h.accountMgr.GetAvailableAccounts(currentModel)
			lastUsedAccount = h.sessionRouter.GetOrAssignAccount(sessionKey, available, nil)
		}

		if lastUsedAccount != nil {
			if lastUsedAccount.ID != lastAccountID {
				lastAccountID = lastUsedAccount.ID
				lastAccountFailCount = 0
			}
		}

		status, headers, body, isStreamed, errAttempt := attemptRequest(attempt)

		if errAttempt == nil {
			// Successful request
			if lastUsedAccount != nil {
				h.accountMgr.ResetAccountError(lastUsedAccount.ID)
			}
			logRequestToTracker(status, "")

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

		// Process Errors (Rate Limits, Quota Exceeded, Token Expired)
		isRetryable := errAttempt.Error() == "CAPACITY_EXHAUSTED" ||
			errAttempt.Error() == "QUOTA_EXHAUSTED" ||
			errAttempt.Error() == "TOKEN_EXPIRED" ||
			errAttempt.Error() == "CREDITS_EXHAUSTED" ||
			errAttempt.Error() == "SERVER_ERROR"

		if lastUsedAccount != nil {
			accId := lastUsedAccount.ID
			email := lastUsedAccount.Email

			if errAttempt.Error() == "TOKEN_EXPIRED" && h.tokenRefresh != nil {
				h.logFn(fmt.Sprintf("🔑 [负载均衡] 检测到账号 %s Token 已过期 (401)。正在自动刷新...", email))
				newToken, refreshErr := h.tokenRefresh(lastUsedAccount)
				if refreshErr == nil {
					lastUsedAccount.SetAccessToken(newToken)
					h.accountMgr.UpdateAccessToken(accId, newToken)
					h.logFn(fmt.Sprintf("🔑 [负载均衡] 账号 %s Token 自动刷新成功，即将重试...", email))
				} else {
					h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s Token 自动刷新失败: %v", email, refreshErr))
				}
			}

			if errAttempt.Error() == "CREDITS_EXHAUSTED" {
				h.logFn(fmt.Sprintf("❌ [负载均衡] 检测到账号 %s 积分已耗尽。标记冷静期并获取真实配额...", email))
				h.accountMgr.UpdateAccountCredits(accId, 0)
				h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, currentModel)

				go func(a *account.Account) {
					res, qErr := h.quotaFetch(a)
					if qErr == nil && len(res.Buckets) > 0 {
						h.accountMgr.UpdateAccountCooldownFromQuota(a.ID, res.Buckets)
					}
				}(lastUsedAccount)
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" {
				h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 模型容量耗尽 (CAPACITY_EXHAUSTED，服务超载或限频)。标记临时冷静期并获取真实配额...", email))
				h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, currentModel)

				go func(a *account.Account) {
					res, qErr := h.quotaFetch(a)
					if qErr == nil && len(res.Buckets) > 0 {
						h.accountMgr.UpdateAccountCooldownFromQuota(a.ID, res.Buckets)
						// 重新检查当前冷静期状态，确认是否清零
						refreshedAcc := h.accountMgr.GetAccountByID(a.ID)
						if refreshedAcc != nil {
							cat := h.accountMgr.GetModelCategory(currentModel)
							cooldown := refreshedAcc.CooldownUntil
							if refreshedAcc.Cooldowns != nil {
								if c, ok := refreshedAcc.Cooldowns[cat]; ok {
									cooldown = c
								}
							}
							if cooldown == 0 {
								h.logFn(fmt.Sprintf("✅ [负载均衡] 账号 %s 额度充足，已自动解除冷静期，恢复可用状态。", email))
							}
						}
					}
				}(lastUsedAccount)
			} else if errAttempt.Error() == "QUOTA_EXHAUSTED" {
				h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 配额已耗尽 (QUOTA_EXHAUSTED)。标记冷静期并获取真实配额...", email))
				h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, currentModel)

				go func(a *account.Account) {
					res, qErr := h.quotaFetch(a)
					if qErr == nil && len(res.Buckets) > 0 {
						h.accountMgr.UpdateAccountCooldownFromQuota(a.ID, res.Buckets)
					}
				}(lastUsedAccount)
			}

			if errAttempt.Error() == "SERVER_ERROR" {
				lastAccountFailCount++
				if lastAccountFailCount >= 3 {
					h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到服务器错误 (%d) 达到 %d 次。标记临时冷静期 (60s) 并切换账号重试...", email, status, lastAccountFailCount))
					h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+60*1000, currentModel)
				} else {
					h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 遇到服务器错误 (%d)（第 %d/3 次）。不标记冷静期，将继续用当前账号尝试...", email, status, lastAccountFailCount))
				}
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" || errAttempt.Error() == "QUOTA_EXHAUSTED" {
				h.accountMgr.RecordAccountError(accId, status, currentModel, h.logFn)
			}
		}

		shouldRetry := isRetryable && attempt < maxRetries
		if errAttempt.Error() == "QUOTA_EXHAUSTED" {
			// If all active accounts are cooled down, do not retry further
			hasAvail := false
			for _, a := range h.accountMgr.GetRawAccounts() {
				if !a.Enabled {
					continue
				}
				cat := h.accountMgr.GetModelCategory(currentModel)
				cooldown := a.CooldownUntil
				if a.Cooldowns != nil {
					if c, ok := a.Cooldowns[cat]; ok {
						cooldown = c
					}
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
			delay := math.Min(float64(h.statsTracker.GetPayload(nil)["stats"].(stats.GlobalStats).TotalRetries*1000), 10000.0) + jitter
			h.logFn(fmt.Sprintf("%s ⚠️ 请求失败 (%s)。将在 %dms 后自动切换账号重试...", logPrefix, errAttempt.Error(), int(delay)))

			h.errLogger.Log("RETRY", targetPath, currentModel, allocatedAccount, attempt+1, errAttempt.Error())
			select {
			case <-r.Context().Done():
				h.logFn(fmt.Sprintf("%s ⚠️ 请求在等待重试时被客户端取消", logPrefix))
				return
			case <-time.After(time.Duration(delay) * time.Millisecond):
			}
		} else {
			h.logFn(fmt.Sprintf("%s ❌ [负载均衡] 尝试失败: %v", logPrefix, errAttempt))
			logRequestToTracker(429, errAttempt.Error())

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
			return
		}
	}
}
