package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/db"
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
	getMaxRetries      func() int
	relayStatsCallback func(userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs int64, statusCode int, reqID string)
	relayQuotaCheck    func(userID, apiKeyID, modelName string) error
	client             *http.Client

	// 远程中继转发相关
	getRemoteRelay     func() RemoteRelayInterface
	remoteClient       *http.Client
	remoteClientMu     sync.Mutex
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
	getMaxRetries func() int,
	relayStatsCallback func(userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs int64, statusCode int, reqID string),
	relayQuotaCheck func(userID, apiKeyID, modelName string) error,
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
		getMaxRetries:      getMaxRetries,
		relayStatsCallback: relayStatsCallback,
		relayQuotaCheck:    relayQuotaCheck,
		client:             netutil.NewClient(5 * time.Minute),
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	relayUserID, _ := r.Context().Value(RelayUserCtxKey).(string)
	if relayUserID == "" {
		relayUserID = r.Header.Get("X-Relay-User-Id")
	}
	relayAPIKeyID, _ := r.Context().Value(RelayAPIKeyCtxKey).(string)
	if relayAPIKeyID == "" {
		relayAPIKeyID = r.Header.Get("X-Relay-Api-Key-Id")
	}
	_ = relayUserID // used later for relay stats callback
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

	// ==========================================
	// 拦截并 Mock Antigravity 客户端登录验证请求
	// ==========================================
	isRelayConnected := false
	if h.getRemoteRelay != nil && h.getRemoteRelay() != nil {
		isRelayConnected = h.getRemoteRelay().IsConnected()
	}
	hasLocalAccounts := false
	if h.accountMgr != nil {
		hasLocalAccounts = len(h.accountMgr.GetRawAccounts()) > 0
	}

	if (isRelayConnected || hasLocalAccounts) && strings.Contains(targetPath, "v1internal") {
		if strings.Contains(targetPath, "fetchUserInfo") {
			if h.logFn != nil {
				h.logFn("⚖️ [Mock] 拦截并放行客户端登录验证 (fetchUserInfo)")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{"regionCode":"JP","userSettings":{}}`))
			return
		} else if strings.Contains(targetPath, "loadCodeAssist") {
			if h.logFn != nil {
				h.logFn("⚖️ [Mock] 拦截并放行客户端权限验证 (loadCodeAssist)")
			}
			mockCodeAssist := `{"allowedTiers":[{"description":"Gemini-powered code suggestions and chat in multiple IDEs","id":"free-tier","isDefault":true,"name":"Antigravity","privacyNotice":{"showNotice":true}},{"description":"Unlimited coding assistant with the most powerful Gemini models","id":"standard-tier","name":"Antigravity","privacyNotice":{},"userDefinedCloudaicompanionProject":true,"usesGcpTos":true}],"cloudaicompanionProject":"favorable-synapse-ttvcb","currentTier":{"description":"Gemini-powered code suggestions and chat in multiple IDEs","id":"free-tier","name":"Antigravity","privacyNotice":{"showNotice":true},"upgradeSubscriptionText":"Upgrade to get 1,500 requests per day with Agent Mode and Gemini CLI, access to Gemini in Google Cloud, plus $1,000 in Google Cloud credits","upgradeSubscriptionType":"GDP_HELIUM","upgradeSubscriptionUri":"https://codeassist.google.com/upgrade"},"gcpManaged":false,"paidTier":{"availableCredits":[{"creditType":"GOOGLE_ONE_AI","minimumCreditAmountForUsage":"50"}],"description":"Google AI Pro","id":"g1-pro-tier","name":"Google AI Pro","upgradeSubscriptionText":"You can upgrade to a Google AI Ultra plan to receive higher rate limits.","upgradeSubscriptionUri":"https://antigravity.google/g1-upgrade"},"upgradeSubscriptionUri":"https://codeassist.google.com/upgrade"}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(mockCodeAssist))
			return
		} else if strings.Contains(targetPath, "fetchAdminControls") || strings.Contains(targetPath, "listExperiments") {
			if h.logFn != nil {
				h.logFn(fmt.Sprintf("⚖️ [Mock] 拦截并响应客户端配置请求 (%s)", targetPath))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{}`))
			return
		}
	}

	// 远程中继转发（客户端模式）
	if h.getRemoteRelay != nil {
		if rr := h.getRemoteRelay(); rr != nil && rr.IsConnected() {
			isLocalRelayLoop := false
			incomingRelayUserID, _ := r.Context().Value(RelayUserCtxKey).(string)
			if incomingRelayUserID != "" {
				conf := rr.GetConfig()
				if conf.IsLocal {
					isLocalRelayLoop = true
				}
			}
			if !isLocalRelayLoop {
				h.forwardThroughRemote(w, r, bodyBytes, targetHost, targetPath, rr)
				return
			}
		}
	}

	logPrefix := fmt.Sprintf("[%s -> %s%s]", r.Method, targetHost, r.URL.Path)

	currentModel := "unknown"
	modelMatch := reModelInPath.FindStringSubmatch(targetPath)
	if len(modelMatch) > 1 {
		currentModel = modelMatch[1]
	} else if strings.Contains(strings.ToLower(targetPath), "generatecontent") {
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

	if relayUserID != "" && h.relayQuotaCheck != nil {
		if err := h.relayQuotaCheck(relayUserID, relayAPIKeyID, currentModel); err != nil {
			if h.logFn != nil {
				h.logFn(fmt.Sprintf("⛔ Relay Quota Exceeded for %s: %v", relayUserID, err))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(fmt.Sprintf(`{"error":{"code":403,"message":"%s"}}`, err.Error())))
			return
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

	rawSessionKey := h.sessionRouter.ExtractSessionKey(r, bodyBytes)

	// 根据负载均衡通道类型，将项目负载均衡会话与账号负载均衡会话区分开，防止因账号池不同而交替覆盖会话绑定
	isPoolReq := isRealModelRequest(targetPath) || isAgentRequest(targetPath) || targetHost == "aiplatform.googleapis.com"
	poolChannel := "antigravity"
	if isPoolReq {
		poolChannel = h.accountMgr.GetActiveChannel()
	}

	sessionKey := rawSessionKey
	if poolChannel == "project" {
		if strings.HasPrefix(rawSessionKey, "auth:") {
			sessionKey = "auth:prj:" + strings.TrimPrefix(rawSessionKey, "auth:")
		} else if strings.HasPrefix(rawSessionKey, "sock:") {
			sessionKey = "sock:prj:" + strings.TrimPrefix(rawSessionKey, "sock:")
		} else {
			sessionKey = "prj:" + rawSessionKey
		}
	} else {
		if strings.HasPrefix(rawSessionKey, "auth:") {
			sessionKey = "auth:acc:" + strings.TrimPrefix(rawSessionKey, "auth:")
		} else if strings.HasPrefix(rawSessionKey, "sock:") {
			sessionKey = "sock:acc:" + strings.TrimPrefix(rawSessionKey, "sock:")
		} else {
			sessionKey = "acc:" + rawSessionKey
		}
	}

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

		isPoolReq := isRealModelRequest(localTargetPath) || isAgentRequest(localTargetPath) || localTargetHost == "aiplatform.googleapis.com"

		if isPoolReq {
			usePool = true
			poolChannel = h.accountMgr.GetActiveChannel()
		}

		if usePool {
			available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, currentModel)

			// 如果通道未开启负载均衡（池模式关闭），限制 available 仅包含第一个激活账号
			// 确保所有会话和请求均使用同一个单账号
			isPoolEnabled := false
			if poolChannel == "project" {
				isPoolEnabled = h.accountMgr.GetProjectPoolMode()
			} else {
				isPoolEnabled = h.accountMgr.GetPoolMode()
			}
			if !isPoolEnabled && len(available) > 0 {
				available = []*account.Account{available[0]}
			}

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
				} else {
					// 非模型推理请求（如 Agent 请求）或直接请求 Vertex AI/Cloud AI Companion 请求：
					// 仅重写 URL 路径中的项目 ID，支持 v1, v1beta, v1alpha 等 API 版本
					parts := strings.Split(localTargetPath, "/")
					if len(parts) > 3 && (parts[1] == "v1" || strings.HasPrefix(parts[1], "v1beta") || strings.HasPrefix(parts[1], "v1alpha")) && parts[2] == "projects" {
						origProject := parts[3]
						if targetProject != "" && origProject != targetProject {
							parts[3] = targetProject
							localTargetPath = strings.Join(parts, "/")
							if attemptIndex == 0 {
								h.logFn(fmt.Sprintf("🔄 [项目路由] 重写 URL 路径中的项目 ID: %s -> %s", origProject, targetProject))
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
			} else {
				// 对于 Antigravity 个人通道网页账户：如果拦截到发往 generativelanguage 官方模型的推理请求，
				// 底层在发送给谷歌前，动态且防呆地将其伪装改写为发往云助手的正式推理项目接口，以避免 scopes 不足的 403 拒绝
				if localTargetHost == "generativelanguage.googleapis.com" && isRealModelRequest(localTargetPath) {
					localTargetHost = "daily-cloudcode-pa.googleapis.com"
					customHeaders.Set("Host", localTargetHost)

					action := "streamGenerateContent"
					queryStr := "?alt=sse"
					localTargetPath = fmt.Sprintf("/v1internal:%s%s", action, queryStr)

					var standardReq map[string]interface{}
					if err := json.Unmarshal(finalReqBody, &standardReq); err == nil {
						modelName := targetModel
						if modelName == "" {
							modelName = currentModel
						}

						standardReq["sessionId"] = fmt.Sprintf("-%d", time.Now().UnixNano()/1e6)

						actualProjectId := poolAccount.ProjectID
						if actualProjectId == "" {
							actualProjectId = h.getStoredProject("default")
						}
						if actualProjectId == "" {
							actualProjectId = "favorable-synapse-ttvcb" // 最终兜底
						}

						wrappedReq := map[string]interface{}{
							"project": actualProjectId,
							"requestId": fmt.Sprintf("chat/%d-%d", time.Now().Unix(), rand.Intn(1000000)),
							"request": standardReq,
							"model": modelName,
							"userAgent": "antigravity",
							"requestType": "chat",
							"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
						}
						if wrappedBytes, err := json.Marshal(wrappedReq); err == nil {
							finalReqBody = wrappedBytes
							customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
							customHeaders.Set("User-Agent", "antigravity/hub/2.2.1 windows/amd64")
						}
					}

					if attemptIndex == 0 {
						h.logFn(fmt.Sprintf("🔄 [Antigravity 网页路由] 重写并封装专有载荷: %s -> https://%s%s", r.URL.Path, localTargetHost, localTargetPath))
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
				bodySnippet := string(decompressIfNeeded(respBodyBytes, resp.Header))
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

			if resp.StatusCode == 429 || resp.StatusCode == 403 || resp.StatusCode == 402 {
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
			bodyStr := string(decompressIfNeeded(respBodyBytes, resp.Header))
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
				if relayUserID != "" && h.relayStatsCallback != nil {
					reqID := r.Header.Get("X-Antigravity-Req-ID")
					var headerKeys []string
					for k := range r.Header {
						headerKeys = append(headerKeys, fmt.Sprintf("%s=%v", k, r.Header.Values(k)))
					}
					if f, err := os.OpenFile(`B:\antigravityProxy\data\debug.log`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
						f.WriteString(fmt.Sprintf("[%s] ServeHTTP headers: %s\n", time.Now().Format(time.RFC3339), strings.Join(headerKeys, " | ")))
						f.Close()
					}
					h.relayStatsCallback(relayUserID, relayAPIKeyID, currentModel, inTokens, outTokens, cachedTokens,
						r.Method, r.Host, r.URL.Path, sessionKey, time.Since(startTime).Milliseconds(), resp.StatusCode, reqID)
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

	maxRetries := h.getMaxRetries()
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
		
		isPoolReq := isRealModelRequest(targetPath) || isAgentRequest(targetPath) || targetHost == "aiplatform.googleapis.com"
		if isPoolReq {
			usePoolForRetry = true
			retryChannel = h.accountMgr.GetActiveChannel()
		}

		if usePoolForRetry {
			available := h.accountMgr.GetAvailableAccountsForChannel(retryChannel, currentModel)

			// 如果通道未开启负载均衡（池模式关闭），限制 available 仅包含第一个激活账号
			// 确保所有会话和请求均使用同一个单账号
			isPoolEnabled := false
			if retryChannel == "project" {
				isPoolEnabled = h.accountMgr.GetProjectPoolMode()
			} else {
				isPoolEnabled = h.accountMgr.GetPoolMode()
			}
			if !isPoolEnabled && len(available) > 0 {
				available = []*account.Account{available[0]}
			}

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

		// 如果未开启号池负载均衡（直连模式），或项目负载均衡开启但本次请求是直接透传（非模型或 Agent 请求），
		// 失败时直接退出，不执行切换账号重试
		isDirectPassthrough := h.accountMgr.GetProjectPoolMode() && !isRealModelRequest(targetPath) && !isAgentRequest(targetPath) && targetHost != "aiplatform.googleapis.com"
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
				h.logFn(fmt.Sprintf("⚠️ [负载均衡] 检测到账号 %s 模型容量耗尽 (CAPACITY_EXHAUSTED，服务超载或限频)。标记临时冷静期并同步获取真实配额...", email))
				h.accountMgr.SetAccountCooldown(accId, time.Now().UnixNano()/1e6+5*60*1000, currentModel)

				res, qErr := h.quotaFetch(lastUsedAccount)
				if qErr == nil && len(res.Buckets) > 0 {
					h.accountMgr.UpdateAccountCooldownFromQuota(lastUsedAccount.ID, res.Buckets)
					// 重新检查当前冷静期状态，确认是否清零
					refreshedAcc := h.accountMgr.GetAccountByID(lastUsedAccount.ID)
					if refreshedAcc != nil {
						cat := h.accountMgr.GetModelCategory(currentModel)
							cooldown := int64(0)
							if refreshedAcc.Cooldowns != nil {
								if c, ok := refreshedAcc.Cooldowns[cat]; ok {
									cooldown = c
								}
							} else {
								cooldown = refreshedAcc.CooldownUntil
							}
						if cooldown == 0 {
							h.logFn(fmt.Sprintf("✅ [负载均衡] 账号 %s 额度充足，已同步解除冷静期，恢复可用状态。", email))
						}
					}
				} else if qErr != nil {
					h.logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 同步刷新配额失败: %v", email, qErr))
				}
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

// getRemoteClient 动态获取并复用具备远程中继拨号链路的全局 http.Client 单例
func (h *ProxyHandler) getRemoteClient() *http.Client {
	h.remoteClientMu.Lock()
	defer h.remoteClientMu.Unlock()

	if h.remoteClient != nil {
		return h.remoteClient
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if currentRr := h.getRemoteRelay(); currentRr != nil && currentRr.IsConnected() {
				return currentRr.DialThroughRemote(addr)
			}
			return nil, errors.New("remote relay disconnected")
		},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	h.remoteClient = &http.Client{
		Transport: transport,
		Timeout:   5 * time.Minute,
	}
	return h.remoteClient
}

// forwardThroughRemote 处理客户端模式下的 HTTP 请求路由，将请求在 TLS 层上中继至远端服务器并执行流式转发与抓包
func (h *ProxyHandler) forwardThroughRemote(w http.ResponseWriter, r *http.Request, bodyBytes []byte, targetHost, targetPath string, rr RemoteRelayInterface) {
	startTime := time.Now()
	relayAPIKeyID, _ := r.Context().Value(RelayAPIKeyCtxKey).(string)
	if relayAPIKeyID == "" {
		relayAPIKeyID = r.Header.Get("X-Relay-Api-Key-Id")
	}
	logPrefix := fmt.Sprintf("[RemoteForward][%s -> %s%s]", r.Method, targetHost, r.URL.Path)
	if h.logFn != nil {
		h.logFn(fmt.Sprintf("%s 🌐 正在将本地 IDE 请求中继转发至远程服务器...", logPrefix))
	}

	// 1. 构造发往公网目标的 HTTPS 请求，使得中继服务器接收时能执行 MITM 解密
	targetUrl := "https://" + targetHost + targetPath
	proxyReq, errReq := http.NewRequestWithContext(r.Context(), r.Method, targetUrl, bytes.NewReader(bodyBytes))
	if errReq != nil {
		h.logFn(fmt.Sprintf("❌ Failed to create remote forward request: %v", errReq))
		http.Error(w, errReq.Error(), http.StatusInternalServerError)
		return
	}

	// 2. 复制原始请求头
	for k, values := range r.Header {
		proxyReq.Header[k] = values
	}
	proxyReq.Header.Set("Host", targetHost)

	// Generate and set unique request ID for Option B async logging
	reqID := fmt.Sprintf("rl_%d", time.Now().UnixNano())
	proxyReq.Header.Set("X-Antigravity-Req-ID", reqID)

	// 3. 使用全局复用的中继 Client 发送请求
	client := h.getRemoteClient()
	resp, errDo := client.Do(proxyReq)
	if errDo != nil {
		h.logFn(fmt.Sprintf("❌ Remote relay forward Do failed: %v", errDo))
		http.Error(w, errDo.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 4. 将响应头及状态码写回 IDE 客户端
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 5. 转发响应体并捕获用于本地抓包记录
	flusher, isFlusher := w.(http.Flusher)
	buf := make([]byte, 4096)
	var respBodyBuf bytes.Buffer

	for {
		n, errRead := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, errWrite := w.Write(chunk)
			if errWrite != nil {
				h.logFn(fmt.Sprintf("⚠️ Failed to write response to client: %v", errWrite))
				break
			}
			if isFlusher {
				flusher.Flush()
			}
			// 仅在数据量较小时记录响应体，避免过量占用内存
			if respBodyBuf.Len() < 5*1024*1024 {
				respBodyBuf.Write(chunk)
			}
		}
		if errRead != nil {
			if errRead != io.EOF {
				h.logFn(fmt.Sprintf("⚠️ Error reading response from remote: %v", errRead))
			}
			break
		}
	}

	// 6. 保存至本地数据包记录器，使前端的“拦截历史”依然能抓包展示
	if h.packetCap != nil {
		h.packetCap.SavePacket(r.Method, targetHost, targetPath, r.Header, bodyBytes, resp.Header, respBodyBuf.Bytes(), resp.StatusCode)
	}

	if h.logFn != nil {
		h.logFn(fmt.Sprintf("%s ✅ 远程中继转发完成，状态码: %d", logPrefix, resp.StatusCode))
	}

	// 7. 直接在客户端本地计算并保存请求日志，不再网络中继拉取服务端日志
	inTokens, outTokens, cachedTokens := 0, 0, 0
	currentModel := "unknown"
	modelMatch := reModelInPath.FindStringSubmatch(targetPath)
	if len(modelMatch) > 1 {
		currentModel = modelMatch[1]
	} else if strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		currentModel = "antigravity-core"
		if len(bodyBytes) > 0 {
			var bodyJson struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Model != "" {
				currentModel = bodyJson.Model
			}
		}
	}

	if resp.StatusCode == 200 && strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		bodyStr := string(decompressIfNeeded(respBodyBuf.Bytes(), resp.Header))
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
	}

	isRealModel := strings.Contains(strings.ToLower(r.URL.Path), "generatecontent") || strings.Contains(strings.ToLower(r.URL.Path), "predict")
	if isRealModel && currentModel != "" && currentModel != "unknown" {
		rate := h.statsTracker.GetPricingMgr().GetPricingForModel(currentModel)
		nonCachedIn := inTokens - cachedTokens
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
		inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0
		totalCost := inputCost + outputCost + cachedCost

		logMethod := r.Method
		if m := r.Header.Get("X-Antigravity-Original-Method"); m != "" {
			logMethod = m
		}
		logPath := r.URL.Path
		if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
			logPath = p
		}
		sessionID := "remote_session"
		if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
			sessionID = "compat-api"
		}

		dbItem := &db.RequestLog{
			ReqID:        reqID,
			Timestamp:    time.Now().Format(time.RFC3339),
			Mode:         "remote",
			UserID:       rr.GetConfig().UserKey,
			ModelName:    currentModel,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Cost:         totalCost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   time.Since(startTime).Milliseconds(),
			StatusCode:   resp.StatusCode,
			Method:       logMethod,
			Host:         targetHost,
			Path:         logPath,
			SessionID:    sessionID,
		}
		_ = db.InsertRequestLog(dbItem)

		// Record locally in memory tracker so it shows up on the client dashboard
		h.statsTracker.AddRequestLog(&stats.RequestLog{
			ID:           reqID,
			Timestamp:    time.Now().Format("01/02 15:04:05"),
			Method:       logMethod,
			Host:         targetHost,
			Path:         logPath,
			Model:        currentModel,
			Account:      rr.GetConfig().UserKey,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Cost:         totalCost,
			StatusCode:   resp.StatusCode,
			SessionID:    sessionID,
			DurationMs:   time.Since(startTime).Milliseconds(),
		})

		// Record usage locally so the client UI can reflect the remote quota consumption
		h.usageTracker.RecordUsage(stats.UsageSample{
			ModelName:    currentModel,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Account:      nil, // This will map to "direct" which is used for global totals like remote quotas
		})

		// 触发 Relay Quota 扣减（如果是 Relay 下游用户请求）
		relayUserID, _ := r.Context().Value(RelayUserCtxKey).(string)
		if relayUserID != "" && h.relayStatsCallback != nil {
			h.relayStatsCallback(relayUserID, relayAPIKeyID, currentModel, inTokens, outTokens, cachedTokens,
				r.Method, targetHost, targetPath, "remote_session", time.Since(startTime).Milliseconds(), resp.StatusCode, reqID)
		}
	}
}
