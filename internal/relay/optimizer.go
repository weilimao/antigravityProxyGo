package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"
)

var reCompressionPrompt = regexp.MustCompile(`(?i)(compress\s+(?:the\s+)?(?:context|conversation|chat|history)|summarize\s+(?:the\s+)?(?:conversation|chat|history|discussion)|minify\s+(?:the\s+)?(?:context|history)|对话(?:压缩|总结|摘要|精简)|历史(?:压缩|总结|摘要|精简))`)

func writeLog(msg string, logFn func(string)) {
	if logFn != nil {
		logFn(msg)
	}
	log.Println("[SessionOptimizer] " + msg)
}

// CheckAndOptimizeSession 检查并优化会话上下文。
// 返回:
//   1. 最终发往底座实际请求的目标模型 (如降级为 gemini-2.5-flash-lite)
//   2. 是否执行了就地压缩重构
func CheckAndOptimizeSession(
	r *http.Request,
	geminiReq *GeminiRequest,
	geminiModel string,
	sessionKey string,
	userKey string,
	userID string,
	apiKeyID string,
	httpClient *http.Client,
	settingsMgr settings.ManagerInterface,
	logFn func(string),
) (string, bool) {
	cfg := settingsMgr.GetSessionOptimization()
	if !cfg.EnableCustomCompression {
		return geminiModel, false
	}

	targetModel := geminiModel

	// 1. 自动检测客户端发起的压缩请求并执行模型降级路由
	if isClientCompressionRequest(geminiReq) {
		lowModelMapped := MapClientModelToGemini(cfg.SummaryModel, settingsMgr.GetRelayModelMapping())
		writeLog(fmt.Sprintf("🔍 [模型降级] 检测到客户端自发的上下文压缩请求，将调用模型由 %s 强制降级路由为 %s...", geminiModel, lowModelMapped), logFn)
		return lowModelMapped, true
	}

	// 2. 代理侧主动 Token 超限检测与拦截压缩
	if sessionKey != "" {
		lastInTokens, err := db.GetLastInTokensBySession(sessionKey)
		writeLog(fmt.Sprintf("🔍 [DB 检索诊断] 会话: %s | 上一轮 Token: %d | 错误信息: %v", sessionKey, lastInTokens, err), logFn)
		if err == nil && lastInTokens > cfg.MaxTokensThreshold {
			writeLog(fmt.Sprintf("🚨 会话 %s 的上一次输入 Token [%d] 超过阈值 [%d]，代理主动触发压缩核心...", sessionKey, lastInTokens, cfg.MaxTokensThreshold), logFn)
			if executeActiveCompression(geminiReq, cfg, userKey, userID, apiKeyID, httpClient, settingsMgr, logFn) {
				return geminiModel, true
			}
			// 压缩失败（如摘要模型调用失败），保持原请求体不变，避免破坏上游载荷格式
			writeLog("⚠️ 代理侧主动压缩未成功，本次请求降级放弃压缩，保留原始请求体", logFn)
		}
	}

	return targetModel, false
}

func isClientCompressionRequest(geminiReq *GeminiRequest) bool {
	if geminiReq.SystemInstruction != nil {
		for _, p := range geminiReq.SystemInstruction.Parts {
			if p.Text != "" && reCompressionPrompt.MatchString(p.Text) {
				return true
			}
		}
	}
	if len(geminiReq.Contents) > 0 {
		lastContent := geminiReq.Contents[len(geminiReq.Contents)-1]
		for _, p := range lastContent.Parts {
			if p.Text != "" && reCompressionPrompt.MatchString(p.Text) {
				return true
			}
		}
	}
	return false
}

func executeActiveCompression(
	geminiReq *GeminiRequest,
	cfg settings.SessionOptimizationConfig,
	userKey string,
	userID string,
	apiKeyID string,
	httpClient *http.Client,
	settingsMgr settings.ManagerInterface,
	logFn func(string),
) bool {
	keepRecentCount := cfg.KeepRecentTurns * 2
	sliceIdx := len(geminiReq.Contents) - keepRecentCount

	// 确保 sliceIdx 为偶数，以保证：
	// 1. messagesToCompress 包含完整轮次的对话（以 model 结尾）
	// 2. recentMessages 以 user 开始，与前面的 summary model 确认消息交替
	if sliceIdx%2 != 0 {
		sliceIdx--
	}

	// 如果计算出的 sliceIdx < 2，说明按原计划保留后，没有足够的历史进行压缩。
	// 此时若消息总数允许，我们动态调整为保留最近消息的最小合理状态（只压缩最老的那轮），即 sliceIdx = 2
	if sliceIdx < 2 {
		sliceIdx = 2
	}

	// 再次检查，若消息总条数太少，无历史可供压缩，则放弃压缩
	if len(geminiReq.Contents) <= sliceIdx {
		return false
	}

	// 若发生了动态调整，则输出提示日志
	if len(geminiReq.Contents)-sliceIdx < keepRecentCount {
		writeLog(fmt.Sprintf("💡 会话历史长度 (%d) 不足保留设定值 (%d)，动态调整为保留最近 %d 条消息以执行压缩",
			len(geminiReq.Contents), keepRecentCount, len(geminiReq.Contents)-sliceIdx), logFn)
	}

	messagesToCompress := geminiReq.Contents[:sliceIdx]
	recentMessages := geminiReq.Contents[sliceIdx:]

	var sb strings.Builder
	sb.WriteString("Please write a concise summary of the following conversation history. Focus on retaining code details, user intents, and system instructions, ignoring greeting filler:\n\n")
	for _, c := range messagesToCompress {
		role := c.Role
		if role == "" {
			role = "user"
		}
		var partsText []string
		for _, p := range c.Parts {
			if p.Text != "" {
				partsText = append(partsText, p.Text)
			}
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, strings.Join(partsText, " ")))
	}

	writeLog(fmt.Sprintf("⏳ 正在调用 %s 生成 %d 条历史消息的背景摘要...", cfg.SummaryModel, len(messagesToCompress)), logFn)

	summaryText, err := callSummaryModelDirect(sb.String(), cfg.SummaryModel, userKey, userID, apiKeyID, httpClient, settingsMgr)
	if err != nil {
		writeLog(fmt.Sprintf("❌ 自动生成摘要失败: %v，本次请求降级放弃压缩", err), logFn)
		return false
	}

	newContents := []GeminiContent{
		{
			Role: "user",
			Parts: []GeminiPart{{Text: fmt.Sprintf("[System notification: The following is a summary of the past conversation background to save token context. Please base your knowledge on it but focus on the subsequent messages]\n\nBackground Summary:\n%s", summaryText)}},
		},
		{
			Role: "model",
			Parts: []GeminiPart{{Text: "Understood. I have memorized this background summary and will assist you based on it."}},
		},
	}
	newContents = append(newContents, recentMessages...)

	geminiReq.Contents = newContents
	writeLog(fmt.Sprintf("✅ 代理侧主动会话压缩完成，消息条数重置为 %d", len(geminiReq.Contents)), logFn)
	return true
}

func callSummaryModelDirect(
	prompt string,
	summaryModel string,
	userKey string,
	userID string,
	apiKeyID string,
	httpClient *http.Client,
	settingsMgr settings.ManagerInterface,
) (string, error) {
	geminiModel := MapClientModelToGemini(summaryModel, settingsMgr.GetRelayModelMapping())

	type GeminiSummaryPart struct {
		Text string `json:"text"`
	}
	type GeminiSummaryContent struct {
		Role  string              `json:"role,omitempty"`
		Parts []GeminiSummaryPart `json:"parts"`
	}
	type GeminiSummaryReq struct {
		Contents []GeminiSummaryContent `json:"contents"`
	}

	gReq := GeminiSummaryReq{
		Contents: []GeminiSummaryContent{
			{
				Role:  "user",
				Parts: []GeminiSummaryPart{{Text: prompt}},
			},
		},
	}
	gBytes, err := json.Marshal(gReq)
	if err != nil {
		return "", err
	}

	targetURL := fmt.Sprintf("http://%s/v1beta/models/%s:generateContent", localProxyAddr, geminiModel)
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(gBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userKey)
	req.Header.Set("X-Relay-User-Id", userID)
	if apiKeyID != "" {
		req.Header.Set("X-Relay-Api-Key-Id", apiKeyID)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("summary model status %d: %s", resp.StatusCode, string(respBody))
	}

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return parseSummaryResponse(respData)
}

func parseSummaryResponse(respData []byte) (string, error) {
	var summaryText strings.Builder
	isSSE := false
	scanner := bufio.NewScanner(bytes.NewReader(respData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			isSSE = true
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataStr == "[DONE]" || dataStr == "" {
				continue
			}
			var chunk struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}
			if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
				summaryText.WriteString(chunk.Candidates[0].Content.Parts[0].Text)
			}
		}
	}

	if isSSE {
		if summaryText.Len() > 0 {
			return summaryText.String(), nil
		}
		return "", fmt.Errorf("empty summary response from SSE stream")
	}

	type Candidate struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}
	type RespStruct struct {
		Candidates []Candidate `json:"candidates"`
	}

	var aux struct {
		RespStruct
		Response *RespStruct `json:"response"`
	}

	if err := json.Unmarshal(respData, &aux); err != nil {
		return "", err
	}

	var finalResp *RespStruct
	if aux.Response != nil && len(aux.Response.Candidates) > 0 {
		finalResp = aux.Response
	} else {
		finalResp = &aux.RespStruct
	}

	if len(finalResp.Candidates) > 0 && len(finalResp.Candidates[0].Content.Parts) > 0 {
		return finalResp.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("empty response")
}
