package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// compat_response.go: Gemini 上游非流式响应 -> 客户端协议(OpenAI/Anthropic/Responses)回译。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

func (h *APICompatHandler) handleNormalResponse(
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	clientModel string,
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
	// displayModel 与流式路径保持一致:优先透传客户端原始模型 ID,避免严格客户端
	// (Claude Code/Codex)校验响应 model 与请求 model 不一致而告警/断开。
	displayModel := clientModel
	if displayModel == "" {
		displayModel = geminiModel
	}

	data, err := io.ReadAll(respBody)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to read google response"})
		return
	}

	var gemResp GeminiResponse
	if err := json.Unmarshal(data, &gemResp); err != nil {
		// 可能是被强制转换成了 SSE 流式响应 (如 antigravity 强制路由至 streamGenerateContent)
		if strings.Contains(string(data), "data: ") {
			var fullText string
			var lastFinish string
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "data: ") {
					dataStr := strings.TrimPrefix(line, "data: ")
					if dataStr == "[DONE]" {
						continue
					}
					var chunk GeminiResponse
					if errChunk := json.Unmarshal([]byte(dataStr), &chunk); errChunk == nil {
						if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
							fullText += chunk.Candidates[0].Content.Parts[0].Text
						}
						// 被强制转成 SSE 的非流式路径同样记录末帧 finishReason,一并参与映射
						if len(chunk.Candidates) > 0 && chunk.Candidates[0].FinishReason != "" {
							lastFinish = chunk.Candidates[0].FinishReason
						}
						if chunk.UsageMetadata.PromptTokenCount > 0 {
							gemResp.UsageMetadata.PromptTokenCount = chunk.UsageMetadata.PromptTokenCount
						}
						if chunk.UsageMetadata.CandidatesTokenCount > 0 {
							gemResp.UsageMetadata.CandidatesTokenCount = chunk.UsageMetadata.CandidatesTokenCount
						}
						if chunk.UsageMetadata.ThoughtsTokenCount > 0 {
							gemResp.UsageMetadata.ThoughtsTokenCount = chunk.UsageMetadata.ThoughtsTokenCount
						}
					}
				}
			}
			gemResp.Candidates = []GeminiCandidate{
				{
					Content:      GeminiCandidateContent{Parts: []GeminiPart{{Text: fullText}}, Role: "model"},
					FinishReason: lastFinish,
				},
			}
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": "failed to parse google response: " + string(data)})
			return
		}
	}

	// 提取回复内容(thought 思考 / text 正文 + functionCall)与用量。
	// thought:true 的 part 文本是思考内容,必须与正文分离,不能混入 text——
	// 旧实现非流式路径忽略 part.Thought,把思考文本当正文回译,导致 Claude Code/Codex
	// 非流式请求把思考当正文显示(D)。此处把 thought 单独累积,后续按 apiFormat 独立输出。
	var contentBlocks []AnthropicContent
	var thinkingText strings.Builder
	hasFunctionCall := false
	if len(gemResp.Candidates) > 0 {
		for _, part := range gemResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				if part.Thought {
					// 思考内容单独累积,不进正文 contentBlocks
					thinkingText.WriteString(part.Text)
					continue
				}
				contentBlocks = append(contentBlocks, AnthropicContent{Type: "text", Text: part.Text})
			}
			if part.FunctionCall != nil {
				hasFunctionCall = true
				contentBlocks = append(contentBlocks, AnthropicContent{
					Type:  "tool_use",
					ID:    generateToolUseID(),
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				})
			}
		}
	}
	if len(contentBlocks) == 0 && thinkingText.Len() == 0 {
		contentBlocks = []AnthropicContent{{Type: "text", Text: ""}}
	}

	inTokens := gemResp.UsageMetadata.PromptTokenCount
	outTokens := gemResp.UsageMetadata.CandidatesTokenCount
	thoughtTokens := gemResp.UsageMetadata.ThoughtsTokenCount

	// 末帧 finishReason 经 Gemini->OpenAI 映射,不再硬编码 stop/tool_calls;
	// 映射尊重 length(MAX_TOKENS 截断)/content_filter(安全拦截)等终端态。
	finishReason := mapGeminiFinishToOpenAI("", hasFunctionCall)
	if len(gemResp.Candidates) > 0 {
		finishReason = mapGeminiFinishToOpenAI(gemResp.Candidates[0].FinishReason, hasFunctionCall)
	}

	// 根据要求的 API 格式，翻译响应包
	if apiFormat == "openai" {
		replyText := ""
		var toolCalls []OpenAIToolCall
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			} else if b.Type == "tool_use" {
				argsJSON, _ := json.Marshal(b.Input)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   b.ID,
					Type: "function",
					Function: OpenAIToolCallFunction{
						Name:      b.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		openResp := OpenAIResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", rand.Int63()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   displayModel,
			Choices: []OpenAIResponseChoice{
				{
					Index: 0,
					Message: OpenAIMessage{
						Role:             "assistant",
						Content:          replyText,
						ReasoningContent: thinkingText.String(),
						ToolCalls:        toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: buildOpenAIUsage(inTokens, outTokens, thoughtTokens),
		}
		writeJSON(w, http.StatusOK, &openResp)
	} else if apiFormat == "responses" {
		replyText := ""
		for _, b := range contentBlocks {
			if b.Type == "text" {
				replyText += b.Text
			}
		}
		respID := fmt.Sprintf("resp_%d", rand.Int63())
		// output 条目:若有思考,先插一个 reasoning message item(独立 output_index),
		// 再跟正文 message item。与流式路径 reasoning 独立 item 语义一致(B)。
		var outputItems []interface{}
		outIdx := 0
		if thinkingText.Len() > 0 {
			outputItems = append(outputItems, map[string]interface{}{
				"id":      fmt.Sprintf("msg_%s_r0", respID),
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": thinkingText.String()}},
			})
			outIdx = 1
		}
		outputItems = append(outputItems, map[string]interface{}{
			"id":      fmt.Sprintf("msg_%s_%d", respID, outIdx),
			"type":    "message",
			"status":  "completed",
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "output_text", "text": replyText}},
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":         respID,
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": outputItems,
			},
		})
	} else { // anthropic
		// 若有思考内容,在正文前插入 thinking 块(对齐流式路径 thinking_delta + 空串 signature)。
		// thinking 块的 index 在 content 数组顺序中自然处于 text 块之前,符合 Anthropic 规范。
		var finalBlocks []AnthropicContent
		if thinkingText.Len() > 0 {
			finalBlocks = append(finalBlocks, AnthropicContent{
				Type:      "thinking",
				Thinking:  thinkingText.String(),
				Signature: "",
			})
		}
		finalBlocks = append(finalBlocks, contentBlocks...)
		if len(finalBlocks) == 0 {
			finalBlocks = []AnthropicContent{{Type: "text", Text: ""}}
		}
		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}
		anthResp := AnthropicResponse{
			ID:           fmt.Sprintf("msg_%d", rand.Int63()),
			Type:         "message",
			Role:         "assistant",
			Content:      finalBlocks,
			Model:        displayModel,
			StopReason:   stopReason,
			StopSequence: nil,
			Usage: AnthropicResponseUsage{
				InputTokens:  inTokens,
				OutputTokens: outTokens,
			},
		}
		writeJSON(w, http.StatusOK, &anthResp)
	}

}
