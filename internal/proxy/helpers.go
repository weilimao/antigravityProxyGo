package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/stats"
)

// 包级正则变量：避免每次请求重复编译，消除 GC 压力
var (
	reModelInPath     = regexp.MustCompile(`/models/([^:]+)`)
	reModelInBody     = regexp.MustCompile(`(?:models/)?(.+)`)
	rePromptTokens    = regexp.MustCompile(`"promptTokenCount"\s*:\s*(\d+)`)
	reCandidateTokens = regexp.MustCompile(`"candidatesTokenCount"\s*:\s*(\d+)`)
	reCachedTokens    = regexp.MustCompile(`"cachedContentTokenCount"\s*:\s*(\d+)`)
	// reNonEmptyText 匹配 "text": " 后跟至少一个非引号字符，用于判断响应是否含非空文本。
	// 模型返回 functionCall 时 text 常为空，属于正常工具调用响应，不应判为空响应。
	reNonEmptyText = regexp.MustCompile(`"text"\s*:\s*"[^"]`)
)

func isIgnoredTelemetry(path string) bool {
	// 如果是真实的模型请求或 Agent 请求，即使包含 v1internal 也不应被忽略
	if isRealModelRequest(path) || isAgentRequest(path) {
		return false
	}
	return strings.Contains(path, "v1internal") && !strings.Contains(path, "retrieveUserQuota")
}

func isRealModelRequest(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "generatecontent") || strings.Contains(p, "predict")
}

func isAgentRequest(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "agent")
}

func mapModelForProject(modelName string) string {
	modelNameLower := strings.ToLower(modelName)
	if strings.HasPrefix(modelNameLower, "models/") {
		modelNameLower = modelNameLower[7:]
	}
	// gemini-3.x 及更新系列：直接透传原始模型名，不做版本降级
	if strings.Contains(modelNameLower, "gemini-3") {
		return modelNameLower
	}
	if strings.Contains(modelNameLower, "gemini-1.5-pro") || strings.Contains(modelNameLower, "gemini-2.0-pro") || strings.Contains(modelNameLower, "gemini-2.5-pro") {
		return modelNameLower
	}
	if strings.Contains(modelNameLower, "gemini-1.5-flash") || strings.Contains(modelNameLower, "gemini-2.0-flash") || strings.Contains(modelNameLower, "gemini-2.5-flash") || strings.Contains(modelNameLower, "gemini-2.5-flash-lite") {
		return modelNameLower
	}
	if strings.Contains(modelNameLower, "pro") || strings.Contains(modelNameLower, "agent") {
		return "gemini-1.5-pro"
	}
	return "gemini-1.5-flash"
}

// handleProjectIntercept 处理项目渠道下的拦截并进行 Mock 响应
func (h *ProxyHandler) handleProjectIntercept(w http.ResponseWriter, targetPath string) bool {
	if h.accountMgr.GetActiveChannel() != "project" {
		return false
	}

	// 如果没有开启项目负载均衡，我们直接走直连，不进行任何 Mock 拦截
	if !h.accountMgr.GetProjectPoolMode() {
		return false
	}

	if strings.Contains(targetPath, "retrieveUserQuota") {
		h.logFn("⚖️ [project 拦截] 拦截并 Mock 配额请求 (retrieveUserQuota)")
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
		return true
	}

	// fetchAvailableModels 直接透传，不做 Mock —— 账号负载均衡只换 Token，不干预模型列表
	if strings.Contains(targetPath, "fetchAvailableModels") {
		return false
	}

	if strings.Contains(targetPath, "v1internal") && !isRealModelRequest(targetPath) && !isAgentRequest(targetPath) {
		h.logFn("⚖️ [project 拦截] 拦截并 Mock 遥测请求 (" + targetPath + ")")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte("{}"))
		return true
	}

	return false
}

// logRequestToTracker 记录请求日志并发送统计数据
func (h *ProxyHandler) logRequestToTracker(
	logged *bool,
	statusCode int,
	errDetail string,
	targetPath string,
	cachedTokens int,
	bodyBytes []byte,
	currentModel string,
	allocatedAccount string,
	currentAttemptIndex int,
	r *http.Request,
	inTokens int,
	outTokens int,
	sessionKey string,
	startTime time.Time,
	targetHost string,
) {
	if *logged {
		return
	}
	*logged = true

	cacheStatus := "NONE"
	if statusCode == 200 && strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		if cachedTokens > 0 {
			cacheStatus = "HIT"
		} else {
			cacheStatus = "MISS"
		}
	} else if strings.Contains(strings.ToLower(targetPath), "generatecontent") {
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

	logPath := targetPath
	if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
		logPath = p
	}
	logMethod := r.Method
	if m := r.Header.Get("X-Antigravity-Original-Method"); m != "" {
		logMethod = m
	}
	logSession := sessionKey
	if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
		logSession = "compat-api"
	}

	h.statsTracker.AddRequestLog(&stats.RequestLog{
		ID:             fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Intn(1000)),
		Timestamp:      time.Now().Format("01/02 15:04:05"),
		Method:         logMethod,
		Host:           targetHost,
		Path:           logPath,
		Model:          currentModel,
		Account:        allocatedAccount,
		InTokens:       inTokens,
		OutTokens:      outTokens,
		CachedTokens:   cachedTokens,
		CacheStatus:    cacheStatus,
		StatusCode:     statusCode,
		RequestBody:    reqBody,
		RequestHeaders: headersMap,
		SessionID:      logSession,
		DurationMs:     time.Since(startTime).Milliseconds(),
	})
}

// stripThoughtSignature 递归清除 JSON 请求体中所有 parts 里的 thoughtSignature 字段。
// 标准 generativelanguage API 不支持 thoughtSignature，降级翻译时若保留该字段，
// 会导致 Google 上游解析异常，触发 MALFORMED_FUNCTION_CALL 错误使流式响应提前中断。
func stripThoughtSignature(obj interface{}) {
	switch v := obj.(type) {
	case map[string]interface{}:
		// 删除当前层级的 thoughtSignature
		delete(v, "thoughtSignature")
		// 递归处理所有值
		for _, val := range v {
			stripThoughtSignature(val)
		}
	case []interface{}:
		for _, item := range v {
			stripThoughtSignature(item)
		}
	}
}

// decompressIfNeeded returns the decompressed bytes if the headers indicate the content is gzipped.
func decompressIfNeeded(body []byte, headers http.Header) []byte {
	if len(body) == 0 {
		return body
	}
	isGzip := false
	if strings.Contains(strings.ToLower(headers.Get("Content-Encoding")), "gzip") {
		isGzip = true
	}
	if isGzip {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			defer reader.Close()
			decompressed, errRead := io.ReadAll(reader)
			if errRead == nil {
				return decompressed
			}
		}
	}
	return body
}
