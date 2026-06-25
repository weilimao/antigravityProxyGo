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
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

type ProxyHandler struct {
	accountMgr         *account.Manager
	sessionRouter      *session.Router
	statsTracker       *stats.Tracker
	usageTracker       *stats.UsageTracker
	errLogger          *stats.RetryErrorLogger
	packetCap          *stats.PacketCapturer
	logFn              func(string)
	quotaFetch         func(*account.Account) (*account.QuotaResult, error)
	tokenRefresh       func(*account.Account) (string, error)
	setCapturedProject func(string, string)
	getStoredProject   func(string) string
	client             *http.Client
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
		accountMgr:         accountMgr,
		sessionRouter:      sessionRouter,
		statsTracker:       statsTracker,
		usageTracker:       usageTracker,
		errLogger:          errLogger,
		packetCap:          packetCap,
		logFn:              logFn,
		quotaFetch:         quotaFetch,
		tokenRefresh:       tokenRefresh,
		setCapturedProject: setCapturedProject,
		getStoredProject:   getStoredProject,
		client:             netutil.NewClient(5 * time.Minute),
	}
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

	// originalModel := currentModel
	// 如果是 gemini-cli 通道，映射客户端模型名字为个人支持的真实可用模型
	/*
		if h.accountMgr.GetActiveChannel() == "gemini-cli" {
			mappings := map[string]string{
				"gemini-3-flash-agent":       "gemini-3-pro-preview",
				"gemini-3.5-flash-low":       "gemini-3-flash-preview",
				"gemini-3.5-flash-extra-low": "gemini-3.1-flash-lite",
				"gemini-pro-agent":           "gemini-3.1-pro-preview",
				"gemini-3.1-pro-low":         "gemini-3.1-pro-preview",
			}
			if mapped, found := mappings[currentModel]; found {
				currentModel = mapped
			}

			// 拦截并 Mock 非模型请求，防止因缺少 cloudcode-pa 项目权限报错
			if strings.Contains(targetPath, "retrieveUserQuota") {
				h.logFn("⚖️ [gemini-cli 拦截] 拦截并 Mock 配额请求 (retrieveUserQuota)")
				mockQuotaResponse := map[string]interface{}{
					"quotaSummaries": []interface{}{
						map[string]interface{}{"model": "Gemini Weekly Quota", "usedFraction": 0.0},
						map[string]interface{}{"model": "Gemini 5-Hour Quota", "usedFraction": 0.0},
						map[string]interface{}{"model": "Claude Weekly Quota", "usedFraction": 0.0},
						map[string]interface{}{"model": "Claude 5-Hour Quota", "usedFraction": 0.0},
					},
					"groups": []interface{}{
						map[string]interface{}{
							"displayName": "Gemini Models",
							"buckets": []interface{}{
								map[string]interface{}{"displayName": "Weekly Limit", "remainingFraction": 1.0},
								map[string]interface{}{"displayName": "Five Hour Limit", "remainingFraction": 1.0},
							},
						},
						map[string]interface{}{
							"displayName": "Claude and GPT models",
							"buckets": []interface{}{
								map[string]interface{}{"displayName": "Weekly Limit", "remainingFraction": 1.0},
								map[string]interface{}{"displayName": "Five Hour Limit", "remainingFraction": 1.0},
							},
						},
					},
				}
				mockBytes, _ := json.Marshal(mockQuotaResponse)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", strconv.Itoa(len(mockBytes)))
				w.WriteHeader(200)
				w.Write(mockBytes)
				return
			}

			if strings.Contains(targetPath, "v1internal") && !isRealModelRequest(targetPath) {
				h.logFn("⚖️ [gemini-cli 拦截] 拦截并 Mock 遥测请求 (" + targetPath + ")")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				w.Write([]byte("{}"))
				return
			}

			// 如果是模型请求，则将 Host 和 Path 重写为个人通道公有接口
			if currentModel != "unknown" {
				targetHost = "generativelanguage.googleapis.com"

				// 解析动作 (如 generateContent 或 streamGenerateContent)
				action := "generateContent"
				if strings.Contains(targetPath, "streamGenerateContent") {
					action = "streamGenerateContent"
				} else if strings.Contains(targetPath, "predict") {
					action = "generateContent"
				} else {
					// 兜底提取动作
					parts := strings.Split(targetPath, ":")
					if len(parts) > 1 {
						rawAction := strings.Split(parts[len(parts)-1], "?")[0]
						if rawAction != "" {
							action = rawAction
						}
					}
				}

				isStreaming := action == "streamGenerateContent" || strings.Contains(targetPath, "alt=sse")
				if isStreaming && !strings.Contains(action, "streamGenerateContent") {
					action = "streamGenerateContent"
				}

				queryStr := ""
				if isStreaming {
					queryStr = "?alt=sse"
				} else {
					if r.URL.RawQuery != "" {
						queryStr = "?" + r.URL.RawQuery
					}
				}

				targetPath = fmt.Sprintf("/v1beta/models/%s:%s%s", currentModel, action, queryStr)
				h.logFn(fmt.Sprintf("🔄 [gemini-cli 重写] TargetHost -> %s, TargetPath -> %s", targetHost, targetPath))
			}
		}
	*/

	if h.handleProjectIntercept(w, targetPath) {
		return
	}

	sessionKey := h.sessionRouter.ExtractSessionKey(r, bodyBytes)

	var inTokens, outTokens, cachedTokens int
	var logged bool
	var allocatedAccount string
	var currentAttemptIndex int
	var sentBytes []byte
	var headersSent bool

	logRequestToTracker := func(statusCode int, errDetail string) {
		if allocatedAccount == "" {
			allocatedAccount = "直连"
		}
		h.logRequestToTracker(
			&logged,
			statusCode,
			errDetail,
			targetPath,
			cachedTokens,
			bodyBytes,
			currentModel,
			allocatedAccount,
			currentAttemptIndex,
			r,
			inTokens,
			outTokens,
			sessionKey,
			startTime,
			targetHost,
		)
	}

	attemptRequest := func(attemptIndex int) (int, map[string][]string, []byte, bool, error) {
		localTargetHost := targetHost
		localTargetPath := targetPath

		if attemptIndex > 0 && !isIgnoredTelemetry(localTargetPath) {
			h.statsTracker.TrackRetry(1)
		}

		customHeaders := make(http.Header)
		for k, values := range r.Header {
			customHeaders[k] = values
		}
		customHeaders.Set("Host", localTargetHost)

		var poolAccount *account.Account
		usePool := false
		var poolChannel string

		isModelReq := isRealModelRequest(localTargetPath) || localTargetHost == "aiplatform.googleapis.com"

		if isModelReq && h.accountMgr.GetProjectPoolMode() {
			// 推理模型请求 + 项目负载均衡开启 → 路由到项目池
			usePool = true
			poolChannel = "project"
		} else if !h.accountMgr.GetProjectPoolMode() {
			// 项目负载均衡未开启时，才遵循普通通道的 poolMode 设置
			// 若 projectPoolMode=true，非模型请求（fetchAvailableModels 等 Cloud Code API）
			// 直接透传原始 credentials，不注入项目池账号（项目账号无 Cloud Code API 权限）
			if h.accountMgr.IsPoolModeForActiveChannel() {
				usePool = true
				poolChannel = h.accountMgr.GetActiveChannel()
			}
		}

		if usePool {
			available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, currentModel)
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

			targetProject := ""
			targetModel := ""

			var bodyJson struct {
				Project string `json:"project"`
			}
			bodyParsed := false
			if len(bodyBytes) > 0 {
				bodyParsed = json.Unmarshal(bodyBytes, &bodyJson) == nil
			}

			if poolAccount.Provider == "project" {
				targetProject = poolAccount.ProjectID
				customHeaders.Set("x-goog-user-project", poolAccount.ProjectID)
				// 对于直发 Vertex AI（aiplatform.googleapis.com）的请求，模型名已经是正确的 Vertex AI
				// 原生格式（如 gemini-3.5-flash），不需要经过 mapModelForProject 做老版本降级映射；
				// 只有从 CloudCode 通道（cloudcode-pa）转发过来的请求才需要映射。
				if localTargetHost == "aiplatform.googleapis.com" {
					targetModel = currentModel
				} else {
					targetModel = mapModelForProject(currentModel)
				}
			} else {
				// 对于普通账号通道，我们强行将其改写规范为默认账号请求模式：
				// 1. 强行将项目 ID 覆写为共享账号的默认项目，避免账号因无权限访问自定义项目而报 403
				targetProject = "expanded-palisade-stpfc"
				// 2. 保持底层模型为原本传入的 currentModel，不进行 mapModelForProject 重新映射
				targetModel = ""
			}

			// Let quotaService capture project ID using the authenticated pool account email
			if bodyParsed && bodyJson.Project != "" {
				isDefault := bodyJson.Project == "expanded-palisade-stpfc" || strings.HasPrefix(bodyJson.Project, "expanded-palisade-")
				if !isDefault {
					if h.setCapturedProject != nil {
						h.setCapturedProject(poolAccount.Email, bodyJson.Project)
					}
				}
			}

			// Rewrite project and model fields in JSON payload
			if len(bodyBytes) > 0 && strings.Contains(customHeaders.Get("Content-Type"), "json") {
				var bodyMap map[string]interface{}
				if json.Unmarshal(bodyBytes, &bodyMap) == nil {
					bodyChanged := false

					if targetProject != "" {
						if origProjVal, exists := bodyMap["project"]; exists && origProjVal != targetProject {
							bodyMap["project"] = targetProject
							bodyChanged = true
						}
					} else {
						if _, exists := bodyMap["project"]; exists {
							delete(bodyMap, "project")
							bodyChanged = true
						}
					}

					// 写入模型名 (只限于 project 账号且 targetModel 有效)
					if poolAccount.Provider == "project" && targetModel != "" && targetModel != currentModel {
						if modelVal, exists := bodyMap["model"].(string); exists {
							if strings.HasPrefix(modelVal, "models/") {
								bodyMap["model"] = "models/" + targetModel
							} else {
								bodyMap["model"] = targetModel
							}
							bodyChanged = true
						}
					}

					if bodyChanged {
						newBodyBytes, errMarshal := json.Marshal(bodyMap)
						if errMarshal == nil {
							finalReqBody = newBodyBytes
							customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
							if attemptIndex == 0 {
								if poolAccount.Provider == "project" {
									h.logFn(fmt.Sprintf("🛡️ Injected project ID '%s' and model '%s' into payload.", targetProject, targetModel))
								} else if targetProject != "" {
									h.logFn(fmt.Sprintf("🛡️ Injected project ID '%s' into payload.", targetProject))
								} else {
									h.logFn("🛡️ Stripped 'project' ID from payload to avoid default project quota 429.")
								}
							}
						}
					}
				}
			}

			// 重写 Host 和 Path
			if poolAccount.Provider == "project" {
				if (localTargetHost == "cloudcode-pa.googleapis.com" || localTargetHost == "daily-cloudcode-pa.googleapis.com") && isRealModelRequest(localTargetPath) {
					localTargetHost = "aiplatform.googleapis.com"
					customHeaders.Set("Host", localTargetHost)

					action := "generateContent"
					if strings.Contains(localTargetPath, "streamGenerateContent") {
						action = "streamGenerateContent"
					} else if strings.Contains(localTargetPath, "predict") {
						action = "generateContent"
					} else {
						parts := strings.Split(localTargetPath, ":")
						if len(parts) > 1 {
							rawAction := strings.Split(parts[len(parts)-1], "?")[0]
							if rawAction != "" {
								action = rawAction
							}
						}
					}

					isStreaming := action == "streamGenerateContent" || strings.Contains(localTargetPath, "alt=sse")
					if isStreaming && !strings.Contains(action, "streamGenerateContent") {
						action = "streamGenerateContent"
					}

					queryStr := ""
					if isStreaming {
						queryStr = "?alt=sse"
					} else {
						if r.URL.RawQuery != "" {
							queryStr = "?" + r.URL.RawQuery
						}
					}

					localTargetPath = fmt.Sprintf("/v1/projects/%s/locations/global/publishers/google/models/%s:%s%s", poolAccount.ProjectID, targetModel, action, queryStr)
					if attemptIndex == 0 {
						h.logFn(fmt.Sprintf("🔄 [GCP Project 路由] 重写 API 地址: %s -> https://%s%s", r.URL.Path, localTargetHost, localTargetPath))
					}
				} else if localTargetHost == "aiplatform.googleapis.com" {
					// Vertex AI API 请求：重写 URL 路径中的项目 ID
					if strings.HasPrefix(localTargetPath, "/v1/projects/") {
						parts := strings.Split(localTargetPath, "/")
						if len(parts) > 3 && parts[1] == "v1" && parts[2] == "projects" {
							origProject := parts[3]
							if targetProject != "" && origProject != targetProject {
								parts[3] = targetProject
								localTargetPath = strings.Join(parts, "/")
								if attemptIndex == 0 {
									h.logFn(fmt.Sprintf("🔄 [Vertex AI 路由] 重写项目 ID: %s -> %s", origProject, targetProject))
								}
							}
						}
					}
					// 重写 URL 路径中的模型 ID
					if targetModel != "" {
						oldPath := localTargetPath
						localTargetPath = reModelInPath.ReplaceAllString(localTargetPath, "/models/"+targetModel)
						if attemptIndex == 0 && oldPath != localTargetPath {
							h.logFn(fmt.Sprintf("🔄 [Vertex AI 路由] 重写模型 ID: %s -> %s", currentModel, targetModel))
						}
					}
				}
			}
		}

		// Forward request
		targetUrl := "https://" + localTargetHost + localTargetPath
		proxyReq, errReq := http.NewRequestWithContext(r.Context(), r.Method, targetUrl, bytes.NewReader(finalReqBody))
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
		isStreaming := strings.Contains(localTargetPath, "streamGenerateContent")

		var skippedBytes int = 0
		if isStreaming && resp.StatusCode == 200 {
			if !headersSent {
				// Copy headers to writer
				for k, values := range resp.Header {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				w.Header().Del("Content-Length")
				w.WriteHeader(resp.StatusCode)
				headersSent = true
			}

			flusher, hasFlusher := w.(http.Flusher)
			if hasFlusher {
				flusher.Flush()
			}

			buf := make([]byte, 4096)
			var clientDisconnected bool = false
			var streamErr error = nil
			var mismatchHappened bool = false

			for {
				n, errR := resp.Body.Read(buf)
				if n > 0 {
					chunk := buf[:n]
					// 若此前尝试已发送过数据，则在此次尝试中跳过相同的前缀
					if len(sentBytes) > 0 && skippedBytes < len(sentBytes) && !mismatchHappened {
						bytesToCompare := len(sentBytes) - skippedBytes
						if bytesToCompare > len(chunk) {
							bytesToCompare = len(chunk)
						}

						// 验证前缀是否完全匹配
						mismatch := false
						for i := 0; i < bytesToCompare; i++ {
							if chunk[i] != sentBytes[skippedBytes+i] {
								mismatch = true
								break
							}
						}

						if !mismatch {
							// 匹配成功，安全跳过
							skippedBytes += bytesToCompare
							chunk = chunk[bytesToCompare:]
						} else {
							// 发生不一致，为安全起见降级为直接传输
							h.logFn(fmt.Sprintf("%s ⚠️ 重试流数据不匹配，将直接追加后续传输", logPrefix))
							mismatchHappened = true
						}
					}

					if len(chunk) > 0 {
						_, writeErr := w.Write(chunk)
						if writeErr != nil {
							clientDisconnected = true
							break
						}
						sentBytes = append(sentBytes, chunk...)
						if hasFlusher {
							flusher.Flush()
						}
					}
				}
				if errR != nil {
					if errR != io.EOF {
						h.logFn(fmt.Sprintf("%s ⚠️ Read error during streaming: %v", logPrefix, errR))
						streamErr = errR
					}
					break
				}
			}

			if clientDisconnected {
				// 客户端主动断开，直接返回不重试
				return resp.StatusCode, resp.Header, sentBytes, true, nil
			}
			if streamErr != nil {
				// 上游异常中断，触发重试
				return resp.StatusCode, resp.Header, sentBytes, true, errors.New("STREAM_INTERRUPTED")
			}
			respBodyBytes = sentBytes
		} else {
			respBodyBytes, errRead = io.ReadAll(resp.Body)
		}

		if errRead != nil {
			return resp.StatusCode, nil, nil, false, errRead
		}

		// Capture packet logging (Save to PacketCapturer) before any error short-circuit return
		h.packetCap.SavePacket(r.Method, localTargetHost, localTargetPath, r.Header, bodyBytes, resp.Header, respBodyBytes, resp.StatusCode)

		if resp.StatusCode >= 400 {
			if !(resp.StatusCode == 429 && strings.Contains(localTargetPath, "retrieveUserQuota")) {
				bodySnippet := string(respBodyBytes)
				if len(bodySnippet) > 1000 {
					bodySnippet = bodySnippet[:1000] + "... (truncated)"
				}
				h.logFn(fmt.Sprintf("%s ⚠️ 上游 HTTP %d 错误响应: %s", logPrefix, resp.StatusCode, bodySnippet))
			}
		}

		if resp.StatusCode == 401 {
			return 401, resp.Header, respBodyBytes, false, errors.New("TOKEN_EXPIRED")
		}

		// Handle Google Quota 429 Interception to prevent IDE infinite loop
		if resp.StatusCode == 429 && strings.Contains(localTargetPath, "retrieveUserQuota") {
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
		if (resp.StatusCode == 429 || resp.StatusCode == 403 || resp.StatusCode == 402) && !strings.Contains(localTargetPath, "retrieveUserQuota") {
			bodyStr := string(respBodyBytes)
			bodyStrLower := strings.ToLower(bodyStr)
			isCreditExempt := strings.Contains(bodyStrLower, "credit") || strings.Contains(bodyStrLower, "balance") || strings.Contains(bodyStrLower, "overage") || strings.Contains(bodyStrLower, "insufficient credits") || strings.Contains(bodyStrLower, "insufficient balance") || strings.Contains(bodyStrLower, "insufficient funds") || strings.Contains(bodyStrLower, "billing")

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

		// Analyze normal token counts from response body (if success)
		if resp.StatusCode == 200 && strings.Contains(strings.ToLower(localTargetPath), "generatecontent") {
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
		select {
		case <-r.Context().Done():
			h.logFn(fmt.Sprintf("%s ⏹️ 客户端已取消连接，终止负载均衡重试。", logPrefix))
			return
		default:
		}

		currentAttemptIndex = attempt
		if attempt > 0 {
			h.logFn(fmt.Sprintf("%s ⚖️ 正在进行负载均衡第 %d/%d 次尝试...", logPrefix, attempt+1, maxRetries+1))
		}

		// Fetch current active account mapping reference
		usePoolForRetry := false
		var retryChannel string
		if targetHost == "aiplatform.googleapis.com" {
			if h.accountMgr.GetProjectPoolMode() {
				usePoolForRetry = true
				retryChannel = "project"
			}
		} else {
			if h.accountMgr.IsPoolModeForActiveChannel() {
				usePoolForRetry = true
				retryChannel = h.accountMgr.GetActiveChannel()
			}
		}

		if usePoolForRetry {
			available := h.accountMgr.GetAvailableAccountsForChannel(retryChannel, currentModel)
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

		// 如果未开启号池负载均衡（直连模式），或项目负载均衡开启但本次请求是直接透传（非模型请求），
		// 失败时直接退出，不执行切换账号重试
		isDirectPassthrough := h.accountMgr.GetProjectPoolMode() && !isRealModelRequest(targetPath) && targetHost != "aiplatform.googleapis.com"
		if !h.accountMgr.IsPoolModeForActiveChannel() || isDirectPassthrough {
			h.logFn(fmt.Sprintf("%s ❌ [直连模式] 尝试失败: %v", logPrefix, errAttempt))
			logRequestToTracker(status, errAttempt.Error())

			if !headersSent {
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
			errAttempt.Error() == "SERVER_ERROR" ||
			errAttempt.Error() == "STREAM_INTERRUPTED"

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

			if errAttempt.Error() == "STREAM_INTERRUPTED" {
				lastAccountFailCount++
				if lastAccountFailCount >= 3 {
					h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 连续遇到流式中断达到 %d 次。标记临时冷静期 (60s) 并切换账号重试...", email, lastAccountFailCount))
					h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+60*1000, currentModel)
				} else {
					h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 遇到流式中断（第 %d/3 次）。不标记冷静期，将继续用当前账号尝试...", email, lastAccountFailCount))
				}
			}

			if errAttempt.Error() == "CAPACITY_EXHAUSTED" || errAttempt.Error() == "QUOTA_EXHAUSTED" {
				h.accountMgr.RecordAccountError(accId, status, currentModel, h.logFn)
			}
		}

		shouldRetry := isRetryable && attempt < maxRetries
		if errAttempt.Error() == "QUOTA_EXHAUSTED" {
			// If all active accounts of the target channel are cooled down, do not retry further
			targetChan := h.accountMgr.GetActiveChannel()
			if targetHost == "aiplatform.googleapis.com" {
				targetChan = "project"
			}
			hasAvail := false
			for _, a := range h.accountMgr.GetRawAccounts() {
				if a.Provider != targetChan || !a.Enabled {
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

			if !headersSent {
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
