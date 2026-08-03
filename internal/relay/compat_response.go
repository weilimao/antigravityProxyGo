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
	geminiModel string,
	apiFormat string,
	startTime time.Time,
	path string,
	reqID string,
) {
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
						if chunk.UsageMetadata.PromptTokenCount > 0 {
							gemResp.UsageMetadata.PromptTokenCount = chunk.UsageMetadata.PromptTokenCount
						}
						if chunk.UsageMetadata.CandidatesTokenCount > 0 {
							gemResp.UsageMetadata.CandidatesTokenCount = chunk.UsageMetadata.CandidatesTokenCount
						}
					}
				}
			}
			gemResp.Candidates = []GeminiCandidate{
				{Content: GeminiCandidateContent{Parts: []GeminiPart{{Text: fullText}}, Role: "model"}},
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
		finishReason := "stop"
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}
		openResp := OpenAIResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", rand.Int63()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   geminiModel,
			Choices: []OpenAIResponseChoice{
				{
					Index: 0,
					Message: OpenAIMessage{
						Role:      "assistant",
						Content:   replyText,
						ToolCalls: toolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: OpenAIResponseUsage{
				PromptTokens:     inTokens,
				CompletionTokens: outTokens,
				TotalTokens:      inTokens + outTokens,
			},
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
			Model:        geminiModel,
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


