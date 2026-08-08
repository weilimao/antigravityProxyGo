package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/relay"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handler_attempt_routing.go: routeForAttempt —— 号池账号选择 + 项目/模型/URL/载荷重写。
// 从 ServeHTTP 闭包 attemptRequest 的 routing 段逐行搬移,逻辑零回归。
// 唯一错误返回:nil 池账号 -> errors.New("QUOTA_EXHAUSTED")(对应原 attemptRequest 内 return)。

func (sc *serveContext) routeForAttempt(attemptIndex int) (*routeOutcome, error) {
	localTargetHost := sc.targetHost
	localTargetPath := sc.targetPath

	if attemptIndex > 0 && !isIgnoredTelemetry(localTargetPath) {
		sc.h.statsTracker.TrackRetry(1)
	}

	customHeaders := make(http.Header)
	for k, values := range sc.r.Header {
		customHeaders[k] = values
	}
	customHeaders.Set("Host", localTargetHost)

	var poolAccount *account.Account
	usePool := false
	var poolChannel string

	isPoolReq := isRealModelRequest(localTargetPath) || isAgentRequest(localTargetPath) || localTargetHost == "aiplatform.googleapis.com"

	if isPoolReq {
		usePool = true
		poolChannel = sc.h.getGoogleChannel()
	}

	if usePool {
		available := sc.h.accountMgr.GetAvailableAccountsForChannel(poolChannel, sc.currentModel)

		// 如果通道未开启负载均衡（池模式关闭），限制 available 仅包含第一个激活账号
		// 确保所有会话和请求均使用同一个单账号
		isPoolEnabled := false
		if poolChannel == "project" {
			isPoolEnabled = sc.h.accountMgr.GetProjectPoolMode()
		} else {
			isPoolEnabled = sc.h.accountMgr.GetPoolMode()
		}
		if !isPoolEnabled && len(available) > 0 {
			available = []*account.Account{available[0]}
		}

		// 并发限制:按上限过滤超限账号再喂 sessionRouter(保持 sticky/round-robin 语义,
		// sessionRouter 既有「原绑定不可用→删除改绑」分支会永久迁移达上限的 sticky 号,零改共享组件)。
		// 过滤集空则取在途并发最少的号允许超额降级(对齐用户「绝不硬拒」预期),
		// 此时不再走 sessionRouter 绑定(超额临时号,不永久改绑到超额状态)。
		var limit int
		if poolChannel == "project" {
			limit = sc.h.accountMgr.GetProjectMaxConcurrency()
		} else {
			limit = sc.h.accountMgr.GetAntigravityMaxConcurrency()
		}
		filtered := sc.h.accountMgr.FilterByConcurrency(available, limit)
		if len(filtered) > 0 {
			poolAccount = sc.h.sessionRouter.GetOrAssignAccount(sc.sessionKey, filtered, sc.h.logFn)
		} else {
			overAcc := sc.h.accountMgr.LeastLoadedAccount(available)
			if overAcc != nil {
				poolAccount = overAcc
				sc.h.logFn(fmt.Sprintf("⚠️ [并发限制] %s 池并发全满(限 %d),超额降级到最少并发号 %s", poolChannel, limit, overAcc.Email))
			}
		}
		if poolAccount == nil {
			return nil, errors.New("QUOTA_EXHAUSTED")
		}

		// 选号通过后立即占用并发槽:本次请求打到该账号起算占 1 个槽,
		// 直到本次请求结束(forwardForAttempt 顶部 defer 释放)。
		sc.h.accountMgr.AcquireAccount(poolAccount.ID)
	}

	var finalReqBody = sc.bodyBytes

	if poolAccount != nil {
		customHeaders.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		sc.allocatedAccount = poolAccount.Email

		if attemptIndex == 0 {
			sc.h.logFn(fmt.Sprintf("⚖️ [负载均衡] 请求已分配账号: %s (%s) | 目标模型: %s", poolAccount.Email, poolAccount.Provider, sc.currentModel))
		} else {
			sc.h.logFn(fmt.Sprintf("⚖️ [负载均衡] 请求重试，重新分配账号: %s (%s) | 目标模型: %s", poolAccount.Email, poolAccount.Provider, sc.currentModel))
		}

		targetProject := ""
		targetModel := ""

		var bodyJson struct {
			Project string `json:"project"`
		}
		bodyParsed := false
		if len(sc.bodyBytes) > 0 {
			bodyParsed = json.Unmarshal(sc.bodyBytes, &bodyJson) == nil
		}

		if poolAccount.Provider == "project" {
			targetProject = poolAccount.ProjectID
			customHeaders.Set("x-goog-user-project", poolAccount.ProjectID)
			// 对于直发 Vertex AI（aiplatform.googleapis.com）的请求，模型名已经是正确的 Vertex AI
			// 原生格式（如 gemini-3.5-flash），不需要经过 mapModelForProject 做老版本降级映射；
			// 只有从 CloudCode 通道（cloudcode-pa）转发过来的请求才需要映射。
			if localTargetHost == "aiplatform.googleapis.com" {
				targetModel = sc.currentModel
			} else {
				targetModel = mapModelForProject(sc.currentModel)
			}
		} else if strings.Contains(localTargetPath, "v1internal") {
			// 对于 v1internal 接口，当使用个人账号通道时，直接保留客户端传入的项目 ID，避免因权限缺失报错。
			targetProject = bodyJson.Project
			targetModel = ""
		} else {
			// 对于普通账号通道，我们强行将其改写规范为默认账号请求模式：
			// 1. 强行将项目 ID 覆写为共享账号的默认项目，避免账号因无权限访问自定义项目而报 403
			targetProject = "expanded-palisade-stpfc"
			// 2. 保持底层模型为原本传入的 sc.currentModel，不进行 mapModelForProject 重新映射
			targetModel = ""
		}

		// Let quotaService capture project ID using the authenticated pool account email
		if bodyParsed && bodyJson.Project != "" {
			isDefault := bodyJson.Project == "expanded-palisade-stpfc" || strings.HasPrefix(bodyJson.Project, "expanded-palisade-")
			if !isDefault {
				if sc.h.setCapturedProject != nil {
					sc.h.setCapturedProject(poolAccount.Email, bodyJson.Project)
				}
			}
		}

		// Rewrite project and model fields in JSON payload
		if len(sc.bodyBytes) > 0 && strings.Contains(customHeaders.Get("Content-Type"), "json") {
			var bodyMap map[string]interface{}
			if json.Unmarshal(sc.bodyBytes, &bodyMap) == nil {
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

				// 决定最终要写入的 Model
				finalTargetModel := targetModel
				if finalTargetModel == "" {
					finalTargetModel = sc.currentModel
				}

				// 写入模型名 (当 finalTargetModel 与原始 body 里的 model 不一致时触发重写)
				if finalTargetModel != "" && finalTargetModel != "unknown" {
					if modelVal, exists := bodyMap["model"].(string); exists {
						oldModelRaw := strings.TrimPrefix(modelVal, "models/")
						if oldModelRaw != finalTargetModel {
							if strings.HasPrefix(modelVal, "models/") {
								bodyMap["model"] = "models/" + finalTargetModel
							} else {
								bodyMap["model"] = finalTargetModel
							}
							bodyChanged = true
						}
					}
				}

				// 写入思维链配置 (当 CustomThinkingOverrideEnabled 开启且非 Tab 补全模型时触发)
				if sc.h.SettingsMgr != nil && sc.h.SettingsMgr.GetCustomThinkingOverrideEnabled() {
					checkModel := finalTargetModel
					if checkModel == "" {
						if mVal, ok := bodyMap["model"].(string); ok {
							checkModel = strings.TrimPrefix(mVal, "models/")
						}
					}
					isTabModel := strings.Contains(strings.ToLower(checkModel), "tab")

					if !isTabModel {
						supportsThinking := sc.h.SettingsMgr.GetCustomThinkingSupports()
						budget := sc.h.SettingsMgr.GetCustomThinkingBudget()
						minBudget := sc.h.SettingsMgr.GetCustomThinkingMinBudget()
						maxOutputTokens := sc.h.SettingsMgr.GetCustomMaxOutputTokens()

						var thinkingCfg map[string]interface{}
						if !supportsThinking || budget == 0 {
							thinkingCfg = map[string]interface{}{
								"includeThoughts": false,
							}
						} else if budget == -1 {
							// -1 代表自适应/动态预算：包含 includeThoughts=true，但不传递 thinkingBudget 字段给谷歌上游（避免 400 错误）
							thinkingCfg = map[string]interface{}{
								"includeThoughts": true,
							}
						} else {
							// >0 代表固定预算：下限校准后传递给谷歌
							clampedBudget := budget
							if minBudget > 0 && clampedBudget < minBudget {
								clampedBudget = minBudget
							}
							thinkingCfg = map[string]interface{}{
								"includeThoughts": true,
								"thinkingBudget":  clampedBudget,
							}

							// claude-* 经 antigravity 号池(daily-cloudcode-pa)上游会重译为 Vertex Anthropic messages,
							// 严格校验 max_tokens > thinking.budget_tokens;违反返回 400 INVALID_ARGUMENT
							// ("max_tokens must be greater than thinking.budget_tokens",request_id 形如 req_vrtx_...)。
							// 仅当注入了固定 thinkingBudget(clampedBudget>0)且目标为 claude-sonnet/opus
							// (MapClientModelToGemini 保留原样的 claude)时,确保 maxOutputTokens > thinkingBudget,
							// 避免 Codex/Claude Code 走 antigravity 号池 claude 模型时触发 400。
							// gemini flash/pro、includeThoughts:false、-1 自适应等路径 committedBudget<=0 不受守护。
							maxOutputTokens = relay.CalcClaudeGuaranteedMaxOutput(
								clampedBudget, maxOutputTokens, relay.IsClaudeModelForBudget(checkModel),
							)
						}

						// 1. 处理 v1internal 结构的 request.generationConfig
						if reqMap, ok := bodyMap["request"].(map[string]interface{}); ok {
							genConfig, ok := reqMap["generationConfig"].(map[string]interface{})
							if !ok {
								genConfig = make(map[string]interface{})
								reqMap["generationConfig"] = genConfig
							}
							genConfig["thinkingConfig"] = thinkingCfg
							if maxOutputTokens > 0 {
								genConfig["maxOutputTokens"] = maxOutputTokens
							}
							bodyChanged = true
						}

						// 2. 处理根节点的 generationConfig 结构
						if genConfig, ok := bodyMap["generationConfig"].(map[string]interface{}); ok {
							genConfig["thinkingConfig"] = thinkingCfg
							if maxOutputTokens > 0 {
								genConfig["maxOutputTokens"] = maxOutputTokens
							}
							bodyChanged = true
						}

						if attemptIndex == 0 {
							sc.h.logFn(fmt.Sprintf("🧠 [思维链覆写] 动态注入 thinkingConfig: supports=%v, budget=%d, maxOutputTokens=%d", supportsThinking, budget, maxOutputTokens))
						}
					}
				}

				if bodyChanged {
					newBodyBytes, errMarshal := json.Marshal(bodyMap)
					if errMarshal == nil {
						finalReqBody = newBodyBytes
						customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
						if attemptIndex == 0 {
							if poolAccount.Provider == "project" {
								sc.h.logFn(fmt.Sprintf("🛡️ Injected project ID '%s' and model '%s' into payload.", targetProject, targetModel))
							} else if targetProject != "" {
								sc.h.logFn(fmt.Sprintf("🛡️ Injected project ID '%s' into payload.", targetProject))
							} else {
								sc.h.logFn("🛡️ Stripped 'project' ID from payload to avoid default project quota 429.")
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
					if sc.r.URL.RawQuery != "" {
						queryStr = "?" + sc.r.URL.RawQuery
					}
				}

				localTargetPath = fmt.Sprintf("/v1/projects/%s/locations/global/publishers/google/models/%s:%s%s", poolAccount.ProjectID, targetModel, action, queryStr)
				if attemptIndex == 0 {
					sc.h.logFn(fmt.Sprintf("🔄 [GCP Project 路由] 重写 API 地址: %s -> https://%s%s", sc.r.URL.Path, localTargetHost, localTargetPath))
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
							sc.h.logFn(fmt.Sprintf("🔄 [项目路由] 重写 URL 路径中的项目 ID: %s -> %s", origProject, targetProject))
						}
					}
				}
				// 重写 URL 路径中的模型 ID
				if targetModel != "" {
					oldPath := localTargetPath
					localTargetPath = reModelInPath.ReplaceAllString(localTargetPath, "/models/"+targetModel)
					if attemptIndex == 0 && oldPath != localTargetPath {
						sc.h.logFn(fmt.Sprintf("🔄 [Vertex AI 路由] 重写模型 ID: %s -> %s", sc.currentModel, targetModel))
					}
				}
			}
		} else {
			// 对于 Antigravity 个人通道网页账户：
			isV1Internal := strings.Contains(localTargetPath, "v1internal") && isRealModelRequest(localTargetPath)

			hasCustomProject := poolAccount.ProjectID != ""
			if !hasCustomProject && sc.h.getStoredProject != nil {
				hasCustomProject = sc.h.getStoredProject(poolAccount.Email) != ""
			}

			if isV1Internal && !hasCustomProject {
				// 核心大招：如果这是一个 v1internal 请求，且当前账号没有任何捕获的专属项目 ID，
				// 我们直接将其反向翻译并降级为标准的 generativelanguage 接口发送，彻底规避 IAM 权限 403 限制！
				localTargetHost = "generativelanguage.googleapis.com"
				customHeaders.Set("Host", localTargetHost)

				action := "generateContent"
				if strings.Contains(localTargetPath, "streamGenerateContent") {
					action = "streamGenerateContent"
				}
				isStreaming := action == "streamGenerateContent" || strings.Contains(localTargetPath, "alt=sse")
				if isStreaming && !strings.Contains(action, "streamGenerateContent") {
					action = "streamGenerateContent"
				}
				queryStr := ""
				if isStreaming {
					queryStr = "?alt=sse"
				}

				modelName := targetModel
				if modelName == "" {
					modelName = sc.currentModel
				}
				if modelName == "" || modelName == "unknown" || modelName == "antigravity-core" {
					modelName = "gemini-1.5-flash" // 默认好用且免费的模型
				}

				localTargetPath = fmt.Sprintf("/v1beta/models/%s:%s%s", modelName, action, queryStr)

				// 剥离外层 v1internal 包体，提取内层标准的 request 字段
				var v1internalReq map[string]interface{}
				if err := json.Unmarshal(finalReqBody, &v1internalReq); err == nil {
					if innerReqRaw, exists := v1internalReq["request"]; exists {
						if innerReq, ok := innerReqRaw.(map[string]interface{}); ok {
							// 核心修复：codex cli 的 v1internal 可能把 tools, systemInstruction 等丢在外层
							// 降级为官方接口时，必须将这些外层配置合入 innerReq 避免工具约束丢失导致模型幻觉
							for _, key := range []string{"tools", "systemInstruction", "generationConfig", "toolConfig"} {
								if val, hasKey := v1internalReq[key]; hasKey {
									if _, alreadyInInner := innerReq[key]; !alreadyInInner {
										innerReq[key] = val
									}
								}
							}

							// 降级翻译后执行完整的 Gemini 兼容性清洗：
							// 1. 清除 thoughtSignature（标准 API 不支持，会触发 MALFORMED_FUNCTION_CALL）
							// 2. 清洗工具声明中的 JSON Schema（移除 Gemini 不支持的 $schema/additionalProperties/format 等字段）
							// 3. 注入宽松 toolConfig
							// 这是修复 Codex CLI 频繁断连的核心手段，参考 Antigravity-Manager 的 clean_json_schema 实现
							cleanAndPrepareGeminiRequest(innerReq)

							if innerBytes, errMar := json.Marshal(innerReq); errMar == nil {
								finalReqBody = innerBytes
								customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
							}
						}
					}
				}

				if attemptIndex == 0 {
					sc.h.logFn(fmt.Sprintf("🔄 [Antigravity 降级翻译] v1internal 无项目权限账号降级为官方通用接口: %s -> https://%s%s", sc.r.URL.Path, localTargetHost, localTargetPath))
				}
			} else if localTargetHost == "generativelanguage.googleapis.com" && isRealModelRequest(localTargetPath) {
				// 如果原本就是 generativelanguage 且有专属项目，则按原逻辑伪装升级
				if hasCustomProject {
					localTargetHost = "daily-cloudcode-pa.googleapis.com"
					customHeaders.Set("Host", localTargetHost)

					action := "generateContent"
					queryStr := ""
					if strings.Contains(localTargetPath, "streamGenerateContent") || strings.Contains(sc.r.URL.RawQuery, "alt=sse") {
						action = "streamGenerateContent"
						queryStr = "?alt=sse"
					}
					localTargetPath = fmt.Sprintf("/v1internal:%s%s", action, queryStr)

					var standardReq map[string]interface{}
					if err := json.Unmarshal(finalReqBody, &standardReq); err == nil {
						modelName := targetModel
						if modelName == "" {
							modelName = sc.currentModel
						}

						if sc.rawSessionKey != "" {
							hasher := sha256.New()
							hasher.Write([]byte(sc.rawSessionKey))
							standardReq["sessionId"] = "-" + hex.EncodeToString(hasher.Sum(nil))[:16]
						} else {
							standardReq["sessionId"] = fmt.Sprintf("-%d", time.Now().UnixNano()/1e6)
						}

						actualProjectId := poolAccount.ProjectID
						if actualProjectId == "" && sc.h.getStoredProject != nil {
							actualProjectId = sc.h.getStoredProject(poolAccount.Email)
						}
						if actualProjectId == "" {
							actualProjectId = sc.h.getStoredProject("default")
						}
						if actualProjectId == "" {
							actualProjectId = "favorable-synapse-ttvcb" // 最终兜底
						}

						wrappedReq := map[string]interface{}{
							"project":            actualProjectId,
							"requestId":          fmt.Sprintf("agent/%d-%d", time.Now().Unix(), rand.Intn(1000000)),
							"request":            standardReq,
							"model":              modelName,
							"userAgent":          "antigravity",
							"requestType":        "agent",
							"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
						}

						// 注入缓存的 thoughtSignature 到 functionCall parts，替换 skip_thought_signature_validator 哨兵值
						// 保证 v1internal API 思考链连续性，参考 Antigravity-Manager 的 SignatureCache 实现
						if innerReq, ok := wrappedReq["request"].(map[string]interface{}); ok {
							InjectCachedSignatures(innerReq, sc.rawSessionKey, modelName)
						}

						if wrappedBytes, err := json.Marshal(wrappedReq); err == nil {
							finalReqBody = wrappedBytes
							customHeaders.Set("Content-Length", strconv.Itoa(len(finalReqBody)))
							customHeaders.Set("User-Agent", "antigravity/hub/2.2.1 windows/amd64")
						}
					}

					if attemptIndex == 0 {
						sc.h.logFn(fmt.Sprintf("🔄 [Antigravity 网页路由] 重写并封装专有载荷: %s -> https://%s%s", sc.r.URL.Path, localTargetHost, localTargetPath))
					}
				} else {
					// 否则（无项目且直接调用 generativelanguage），保留原样发送
					if attemptIndex == 0 {
						sc.h.logFn(fmt.Sprintf("🔄 [Antigravity 通用路由] 保留官方通用接口访问: %s -> https://%s%s", sc.r.URL.Path, localTargetHost, localTargetPath))
					}
				}
			}
		}
	}
	// 路由完成:把 localTargetHost/Path + customHeaders + finalReqBody + poolAccount 打包返回,
	// forward/classify 阶段读这些(原值); sc.targetHost/targetPath 在路由后保持 setup 原值不动。
	return &routeOutcome{
		poolAccount:   poolAccount,
		targetHost:    localTargetHost,
		targetPath:    localTargetPath,
		customHeaders: customHeaders,
		finalReqBody:  finalReqBody,
	}, nil
}
