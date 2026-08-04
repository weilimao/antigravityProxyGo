package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

type ProxyHandler struct {
	accountMgr             *account.Manager
	sessionRouter          *session.Router
	statsTracker           *stats.Tracker
	usageTracker           *stats.UsageTracker
	errLogger              *stats.RetryErrorLogger
	packetCap              *stats.PacketCapturer
	logFn                  func(string)
	quotaFetch             func(*account.Account) (*account.QuotaResult, error)
	tokenRefresh           func(*account.Account) (string, error)
	setCapturedProject     func(string, string)
	getStoredProject       func(string) string
	getMaxRetries          func() int
	getMaxRetryDelay       func() int
	getMaxRequestBodyBytes func() int64
	getRequestTimeout      func() int
	relayStatsCallback     func(allocatedAccount, userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs int64, statusCode int, reqID string)
	relayQuotaCheck        func(userID, apiKeyID, modelName string) error
	client                 *http.Client
	SettingsMgr            settings.ManagerInterface

	// 远程中继转发相关
	getRemoteRelay func() RemoteRelayInterface
	remoteClient   *http.Client
	remoteClientMu sync.Mutex
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
	getMaxRetryDelay func() int,
	getMaxRequestBodyBytes func() int64,
	getRequestTimeout func() int,
	relayStatsCallback func(allocatedAccount, userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs int64, statusCode int, reqID string),
	relayQuotaCheck func(userID, apiKeyID, modelName string) error,
) *ProxyHandler {
	return &ProxyHandler{
		accountMgr:             accountMgr,
		sessionRouter:          sessionRouter,
		statsTracker:           statsTracker,
		usageTracker:           usageTracker,
		errLogger:              errLogger,
		packetCap:              packetCap,
		logFn:                  logFn,
		quotaFetch:             quotaFetch,
		tokenRefresh:           tokenRefresh,
		setCapturedProject:     setCapturedProject,
		getStoredProject:       getStoredProject,
		getMaxRetries:          getMaxRetries,
		getMaxRetryDelay:       getMaxRetryDelay,
		getMaxRequestBodyBytes: getMaxRequestBodyBytes,
		getRequestTimeout:      getRequestTimeout,
		relayStatsCallback:     relayStatsCallback,
		relayQuotaCheck:        relayQuotaCheck,
		client:                 netutil.NewClient(10 * time.Minute),
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

	// 使用可配置的请求体大小限制，防止异常大请求体导致内存暴涨
	maxBodyBytes := int64(50 * 1024 * 1024) // 默认 50MB
	if h.getMaxRequestBodyBytes != nil {
		if configured := h.getMaxRequestBodyBytes(); configured > 0 {
			maxBodyBytes = configured
		}
	}
	bodyReadStart := time.Now()
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	// 仅对流式接口注入自定义提示词前缀
	if h.SettingsMgr != nil && len(bodyBytes) > 0 && isRealModelRequest(r.URL.Path) {
		isStreaming := strings.Contains(r.URL.Path, "streamGenerateContent") || strings.Contains(r.URL.RawQuery, "alt=sse")
		if isStreaming {
			prefix := h.SettingsMgr.GetPromptPrefix()
			if prefix != "" {
				bodyBytes = injectPromptPrefix(bodyBytes, prefix)
			}
		}
	}

	// 全局工具声明清洗：对所有模型请求（无论 v1internal 还是 generativelanguage），
	// 清洗 tools 中的 JSON Schema 以符合 Gemini API 要求，防止 MALFORMED_FUNCTION_CALL。
	// 这是参考 Antigravity-Manager 的 clean_json_schema 实现的核心防护。
	if len(bodyBytes) > 0 && isRealModelRequest(r.URL.Path) {
		bodyBytes = cleanToolDeclarationsInBody(bodyBytes)
	}

	// 诊断计时：如果请求体读取超过 1 秒，输出警告日志定位 IDE 端慢发送问题
	bodyReadMs := time.Since(bodyReadStart).Milliseconds()
	overallMs := time.Since(startTime).Milliseconds()
	if bodyReadMs > 1000 || overallMs > 2000 {
		if h.logFn != nil {
			h.logFn(fmt.Sprintf("⏱️ [诊断计时] %s %s%s | 请求体读取: %dms (大小: %d bytes) | ServeHTTP 总耗时: %dms",
				r.Method, r.Host, r.URL.Path, bodyReadMs, len(bodyBytes), overallMs))
		}
	}

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
			if incomingRelayUserID == "" {
				incomingRelayUserID = r.Header.Get("X-Relay-User-Id")
			}
			if incomingRelayUserID != "" {
				isLocalRelayLoop = true
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

	// [新增] 全局模型请求拦截与覆写
	if h.SettingsMgr != nil && h.SettingsMgr.GetCustomModelOverrideEnabled() {
		overrideID := h.SettingsMgr.GetCustomModelOverrideID()
		if overrideID != "" && currentModel != "unknown" {
			// 按前缀绕过:客户端原始模型名(去 models/ 前缀、小写化)若以白名单中
			// 任一前缀开头,则跳过覆写原样透传。默认白名单 ["tab"] 放行 Tab 补全模型,
			// 避免其被改向推理上游触发 400 INVALID_ARGUMENT。
			bypassed := shouldBypassOverride(currentModel, h.SettingsMgr.GetBypassOverridePrefixes())
			if bypassed {
				if h.logFn != nil {
					h.logFn(fmt.Sprintf("⏭️ [全局覆写] 模型 %s 命中绕过前缀,跳过覆写原样透传", currentModel))
				}
			} else {
				oldModel := currentModel
				// 如果路径中包含原模型名，将其替换为新模型名
				if strings.Contains(targetPath, "/models/"+oldModel) {
					targetPath = strings.Replace(targetPath, "/models/"+oldModel, "/models/"+overrideID, 1)
					r.URL.Path = strings.Replace(r.URL.Path, "/models/"+oldModel, "/models/"+overrideID, 1)
				}
				currentModel = overrideID
				if h.logFn != nil {
					h.logFn(fmt.Sprintf("🔄 [全局覆写] 无论客户端请求何种模型，强制覆写: %s -> %s", oldModel, overrideID))
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
		poolChannel = h.getGoogleChannel()
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

	// 代理侧主动会话优化与压缩核心 (当请求直接打到 18443 时)
	if h.SettingsMgr != nil && len(bodyBytes) > 0 && strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		if h.logFn != nil {
			h.logFn(fmt.Sprintf("🔍 [18443 劫持诊断] 收到模型请求 | sessionKey: %s | targetPath: %s", sessionKey, targetPath))
		}
		// 检测是否为嵌套包装格式，以正确解包出 GeminiRequest 进行会话压缩优化
		var bodyMap map[string]interface{}
		isWrappedPayload := false
		if json.Unmarshal(bodyBytes, &bodyMap) == nil {
			if _, hasRequest := bodyMap["request"]; hasRequest {
				isWrappedPayload = true
			}
		}

		var geminiReq relay.GeminiRequest
		var err error
		if isWrappedPayload {
			if reqField, ok := bodyMap["request"]; ok {
				reqBytes, marshalErr := json.Marshal(reqField)
				if marshalErr == nil {
					err = json.Unmarshal(reqBytes, &geminiReq)
				} else {
					err = marshalErr
				}
			} else {
				err = fmt.Errorf("missing request field in wrapped payload")
			}
		} else {
			err = json.Unmarshal(bodyBytes, &geminiReq)
		}

		if err != nil {
			if h.logFn != nil {
				h.logFn(fmt.Sprintf("❌ [18443 劫持诊断] json.Unmarshal 失败: %v", err))
			}
		} else {
			userKey := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(userKey), "bearer ") {
				userKey = userKey[7:]
			}
			optimizedModel, compressed := relay.CheckAndOptimizeSession(
				r,
				&geminiReq,
				currentModel,
				sessionKey,
				userKey,
				relayUserID,
				relayAPIKeyID,
				h.client,
				h.SettingsMgr,
				func(msg string) {
					if h.logFn != nil {
						h.logFn(msg)
					}
				},
			)
			if compressed {
				var newBytes []byte
				if isWrappedPayload {
					// 嵌套格式：仅替换 request 字段内的 contents，保留外层包装
					compressedContents, marshalErr := json.Marshal(geminiReq.Contents)
					if marshalErr != nil {
						if h.logFn != nil {
							h.logFn(fmt.Sprintf("❌ [18443 代理劫持] 压缩后序列化 contents 失败: %v", marshalErr))
						}
					} else {
						if reqField, ok := bodyMap["request"].(map[string]interface{}); ok {
							reqField["contents"] = json.RawMessage(compressedContents)
							if newBody, err2 := json.Marshal(bodyMap); err2 == nil {
								newBytes = newBody
							}
						}
					}
				} else {
					// 标准 Gemini 格式：直接序列化整个 GeminiRequest
					newBytes, err = json.Marshal(geminiReq)
				}

				if len(newBytes) > 0 {
					bodyBytes = newBytes
					if currentModel != optimizedModel {
						targetPath = strings.Replace(targetPath, "/models/"+currentModel, "/models/"+optimizedModel, 1)
						r.URL.Path = strings.Replace(r.URL.Path, "/models/"+currentModel, "/models/"+optimizedModel, 1)
					}
					currentModel = optimizedModel
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
					r.ContentLength = int64(len(bodyBytes))
					if h.logFn != nil {
						h.logFn(fmt.Sprintf("🔄 [18443 代理劫持] 成功对会话 %s 实施了自定义压缩，新体积为 %d 字节，目标路径重定向为 %s (嵌套格式: %v)", sessionKey, len(bodyBytes), targetPath, isWrappedPayload))
					}
				} else if err != nil {
					if h.logFn != nil {
						h.logFn(fmt.Sprintf("❌ [18443 代理劫持] 压缩后序列化失败: %v，保留原始请求体", err))
					}
				}
			}
		}
	}

	// ---- serveContext 装配:把 setup 计算出的请求元数据提升为 sc 字段 ----
	// (原 L547-L552 的 var{...} 闭包捕获块改为结构体字段,供 route/forward/classify/retry 跨阶段共享)
	sc := &serveContext{
		h:             h,
		w:             w,
		r:             r,
		startTime:     startTime,
		relayUserID:   relayUserID,
		relayAPIKeyID: relayAPIKeyID,
		targetHost:    targetHost,
		targetPath:    targetPath,
		bodyBytes:     bodyBytes,
		currentModel:  currentModel,
		logPrefix:     logPrefix,
		rawSessionKey: rawSessionKey,
		sessionKey:    sessionKey,
		maxBodyBytes:  maxBodyBytes,
	}
	// headersSent / allocatedAccount / inTokens / outTokens / cachedTokens / logged /
	// currentAttemptIndex 零值初始化,在 forward/classify/retry 阶段原地读写。
	sc.runRetryLoop(w, r)
}
