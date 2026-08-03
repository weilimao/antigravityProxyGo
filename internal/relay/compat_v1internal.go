package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/sigcache"
)

// compat_v1internal.go: /v1internal:generateContent / streamGenerateContent 桥接到 Gemini 号池 + mapModelForProjectInRelay 模型映射。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

func (h *APICompatHandler) handleV1Internal(w http.ResponseWriter, r *http.Request, userSession *RelaySession) {
	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "failed to read request body"})
		return
	}
	r.Body.Close()

	// 自动补齐缺失的 project 和 requestId 字段，让客户端请求体最简
	var rawPayload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawPayload); err == nil {
		modified := false
		if _, exists := rawPayload["project"]; !exists {
			rawPayload["project"] = "favorable-synapse-ttvcb"
			modified = true
		}
		if _, exists := rawPayload["requestId"]; !exists {
			rawPayload["requestId"] = fmt.Sprintf("chat/%d-%d", time.Now().Unix(), rand.Intn(1000000))
			modified = true
		}
		if modified {
			if newBytes, errMar := json.Marshal(rawPayload); errMar == nil {
				bodyBytes = newBytes
			}
		}
	}

	// 动态检测目标 Action 和流式属性
	path := r.URL.Path // e.g. /v1internal:generateContent
	action := "generateContent"
	if strings.Contains(path, "streamGenerateContent") {
		action = "streamGenerateContent"
	}
	isStreaming := action == "streamGenerateContent" || strings.Contains(r.URL.RawQuery, "alt=sse")

	queryStr := ""
	if r.URL.RawQuery != "" {
		queryStr = "?" + r.URL.RawQuery
	} else if isStreaming && !strings.Contains(r.URL.RawQuery, "alt=sse") {
		queryStr = "?alt=sse"
	}

	if h.accountMgr == nil || h.sessionRouter == nil {
		// 防御性降级：用于兼容未配置账号管理器的单元测试或异常回退
		targetURL := fmt.Sprintf("http://%s%s%s", localProxyAddr, path, queryStr)
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to create request: " + err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+userSession.UserKey)
		req.Header.Set("X-Relay-User-Id", userSession.UserID)
		if userSession.APIKeyID != "" {
			req.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
		}
		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to forward request to proxy: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		for k, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 4096)
		flusher, isFlusher := w.(http.Flusher)
		for {
			n, errRead := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if isFlusher {
					flusher.Flush()
				}
			}
			if errRead != nil {
				break
			}
		}
		return
	}

	// 直接从账号池中分配一个可用账号，实现类似抓包分析 of 直连调用
	poolChannel := h.accountMgr.GetActiveChannel()

	// 从请求体中动态解析模型名称，避免写死
	currentModel := "gemini-2.5-flash"
	var bodyJson struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Model != "" {
		parts := strings.Split(bodyJson.Model, "/models/")
		if len(parts) > 1 {
			currentModel = parts[1]
		} else {
			parts2 := strings.Split(bodyJson.Model, "/")
			currentModel = parts2[len(parts2)-1]
		}
	}

	available := h.accountMgr.GetAvailableAccountsForChannel(poolChannel, currentModel)

	// 如果通道未开启负载均衡（池模式关闭），限制仅包含第一个激活账号
	isPoolEnabled := false
	if poolChannel == "project" {
		isPoolEnabled = h.accountMgr.GetProjectPoolMode()
	} else {
		isPoolEnabled = h.accountMgr.GetPoolMode()
	}
	if !isPoolEnabled && len(available) > 0 {
		available = []*account.Account{available[0]}
	}

	// 临时记录失败的账号，防止本次请求多次分配它们
	skippedAccounts := make(map[string]bool)

	// 最多尝试账号池里的可用账号总数
	maxAttempts := len(available)
	if maxAttempts > 5 {
		maxAttempts = 5
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	var finalResp *http.Response
	var finalErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 筛选未在此次请求中失败的账号
		var activeAvailable []*account.Account
		for _, a := range available {
			if !skippedAccounts[a.ID] {
				activeAvailable = append(activeAvailable, a)
			}
		}

		if len(activeAvailable) == 0 {
			finalErr = fmt.Errorf("all accounts in pool failed with authentication/scope errors")
			break
		}

		sessionKey := userSession.UserID
		poolAccount := h.sessionRouter.GetOrAssignAccount(sessionKey, activeAvailable, h.logFn)
		if poolAccount == nil {
			finalErr = fmt.Errorf("no available account assigned from pool")
			break
		}
		actualProjectId := poolAccount.ProjectID

		// 构建发往谷歌的直连请求参数
		var targetHost string
		var targetPath string
		var finalReqBody = bodyBytes

		if poolAccount.Provider == "project" {
			// GCP 项目通道：直接发往 aiplatform.googleapis.com (Vertex AI)
			targetHost = "aiplatform.googleapis.com"
			vertexAction := "generateContent"
			if isStreaming {
				vertexAction = "streamGenerateContent"
			}
			targetModel := mapModelForProjectInRelay(currentModel)
			targetPath = fmt.Sprintf("/v1/projects/%s/locations/global/publishers/google/models/%s:%s%s", poolAccount.ProjectID, targetModel, vertexAction, queryStr)
		} else if poolAccount.Provider == "antigravity" {
			// 个人网页账号（网页通道，使用 Cloud Code 接口）：不管有没有专属项目，都发往云助手的 v1internal 接口，若无项目 ID 则使用公共默认值
			targetHost = "daily-cloudcode-pa.googleapis.com"
			targetPath = fmt.Sprintf("/v1internal:%s%s", action, queryStr)
			if actualProjectId == "" {
				actualProjectId = "favorable-synapse-ttvcb"
			}
			var v1internalReq map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &v1internalReq); err == nil {
				if _, exists := v1internalReq["project"]; exists {
					v1internalReq["project"] = actualProjectId

					// 注入缓存的 thoughtSignature 到 functionCall parts，
					// 替换哨兵值为真实签名，保证 v1internal API 思考链连续性
					if innerReq, ok := v1internalReq["request"].(map[string]interface{}); ok {
						sigcache.InjectCachedSignatures(innerReq, sigcache.GetGlobal(), userSession.UserID, currentModel)

						// 强效注入思考配置，强制 Gemini 上游输出带 thought:true 标记的明文思考内容
						if IsEnableThinkingMode() {
							genConfig, _ := innerReq["generationConfig"].(map[string]interface{})
							if genConfig == nil {
								genConfig = make(map[string]interface{})
								innerReq["generationConfig"] = genConfig
							}
							genConfig["thinkingConfig"] = map[string]interface{}{
								"includeThoughts": true,
							}
						}
					}

					if innerBytes, errMar := json.Marshal(v1internalReq); errMar == nil {
						finalReqBody = innerBytes
					}
				}
			}
		} else {
			// 其他号源（如 gemini-cli / API Key 等）：直接反向翻译并降级为标准的 generativelanguage 接口发送
			targetHost = "generativelanguage.googleapis.com"
			fallbackModel := currentModel
			if strings.Contains(fallbackModel, "low") || strings.Contains(fallbackModel, "agent") || strings.Contains(fallbackModel, "tab") || strings.Contains(fallbackModel, "3.5") {
				fallbackModel = "gemini-1.5-flash"
			}
			targetPath = fmt.Sprintf("/v1beta/models/%s:%s%s", fallbackModel, action, queryStr)

			// 剥离外层 v1internal 包体，提取内层标准的 request 字段
			var v1internalReq map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &v1internalReq); err == nil {
				if innerReq, exists := v1internalReq["request"]; exists {
					if innerBytes, errMar := json.Marshal(innerReq); errMar == nil {
						finalReqBody = innerBytes
					}
				}
			}
		}

		// 构造直连发包请求
		targetURL := fmt.Sprintf("https://%s%s", targetHost, targetPath)
		req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(finalReqBody))
		if err != nil {
			finalErr = err
			break
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+poolAccount.GetAccessToken())
		req.Header.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")

		h.log("🚀 [中继直连重试 %d/%d] 正在为用户 %s 分配账号 %s | 目标: https://%s%s", attempt+1, maxAttempts, userSession.UserID, poolAccount.Email, targetHost, targetPath)

		// 执行请求并流式响应
		httpClient := h.client
		if isStreaming {
			httpClient = h.streamClient
		}

		resp, errDo := httpClient.Do(req)
		if errDo != nil {
			h.log("⚠️ [中继直连] 账号 %s 访问谷歌失败: %v", poolAccount.Email, errDo)
			skippedAccounts[poolAccount.ID] = true
			finalErr = errDo
			continue
		}

		// 如果账号没有对应作用域（403 ACCESS_TOKEN_SCOPE_INSUFFICIENT）
		// 或者没有云助手项目 IAM 权限（403 PERMISSION_DENIED），视为该账号在该通道不具备权限，自动剔除并换号重试！
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			h.log("⚠️ [中继直连] 账号 %s 遇到权限/Scope限制错误 (%d)，正在自动剔除并换号重试...", poolAccount.Email, resp.StatusCode)
			resp.Body.Close()
			skippedAccounts[poolAccount.ID] = true
			finalResp = resp
			finalErr = fmt.Errorf("authentication error: status %d", resp.StatusCode)

			// 主动清除当前会话与报错账号的强行绑定关系，下次分发重选
			h.sessionRouter.UnbindSession(sessionKey)
			continue
		}

		finalResp = resp
		finalErr = nil
		break
	}

	if finalErr != nil {
		if finalResp != nil {
			// 写回最后的 403 / 401 响应体
			for k, values := range finalResp.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(finalResp.StatusCode)
			io.Copy(w, finalResp.Body)
			finalResp.Body.Close()
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": finalErr.Error()})
		}
		return
	}

	defer finalResp.Body.Close()

	// 拷贝响应头
	for k, values := range finalResp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(finalResp.StatusCode)

	// 流式传输响应体
	buf := make([]byte, 4096)
	flusher, isFlusher := w.(http.Flusher)
	for {
		n, errRead := finalResp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
			// 提取 thoughtSignature 并缓存，下次请求注入到 functionCall parts
			sigcache.GetGlobal().ExtractAndCacheSignatures(buf[:n], userSession.UserID)
		}
		if errRead != nil {
			break
		}
	}
}


func mapModelForProjectInRelay(modelName string) string {
	modelNameLower := strings.ToLower(modelName)
	if strings.Contains(modelNameLower, "gemini-2.0-flash") {
		return "gemini-2.0-flash"
	}
	if strings.Contains(modelNameLower, "gemini-2.0-pro") {
		return "gemini-2.0-pro-exp-02-05"
	}
	if strings.Contains(modelNameLower, "gemini-1.5-pro") {
		return "gemini-1.5-pro"
	}
	return "gemini-1.5-flash"
}


