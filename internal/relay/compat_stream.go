package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// compat_stream.go: Gemini 上游流式 SSE -> 客户端协议(OpenAI/Anthropic/Responses)流式 SSE 回译。
// 从 compat.go 按职责拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

func (h *APICompatHandler) handleStreamResponse(
	ctx context.Context,
	w http.ResponseWriter,
	respBody io.Reader,
	userSession *RelaySession,
	clientModel string,
	geminiModel string,
	apiFormat string,
	inboundInputTokens int,
	streamIncludeUsage bool,
	startTime time.Time,
	path string,
	reqID string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "streaming not supported by server"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK) // 显式发送 200 + SSE 响应头，确保客户端立即收到
	flusher.Flush()

	scanner := bufio.NewScanner(respBody)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer，防止大型 SSE 行被截断

	// 临时生成的流 ID 和固定时间戳，确保所有 chunk 完全一致，防止严格客户端断开
	streamID := fmt.Sprintf("msg_%d", rand.Int63())
	if apiFormat == "openai" {
		streamID = fmt.Sprintf("chatcmpl-%d", rand.Int63())
	}
	createdAt := startTime.Unix()

	// 保持透传客户端原始请求的模型 ID，避免 Claude Code 等 Strict 客户端校验失败
	displayModel := clientModel
	if displayModel == "" {
		displayModel = geminiModel
	}

	// 初始化计数
	inTokens := 0
	outTokens := 0

	var err error

	// 流式状态跟踪
	blockIndex := 0
	textBlockOpen := false
	thinkingBlockOpen := false
	thinkingEmittedAny := false // thinking 块是否已实际下发过 thinking_delta;关块前据此判断是否补 signature_delta
	hasFunctionCall := false
	openAIRoleSent := false
	// lastFinishReason 记录 Gemini 末帧非空 finishReason,收尾时经 mapGeminiFinishToOpenAI 映射,
	// 不再硬编码 stop/tool_calls,支持 length(MAX_TOKENS 截断)/content_filter(安全拦截)终端态。
	lastFinishReason := ""
	// hasOpenAIToolCall / openAIToolCallIdx 用于 OpenAI 流式:每个 functionCall part 独占递增 index,
	// 替代旧硬编码 0,避免并行工具调用 index 撞车导致 arguments 归属错乱。
	var hasOpenAIToolCall bool
	openAIToolCallIdx := -1
	// thoughtsTokens 累计 Gemini 推理模型思考 token(usageMetadata.thoughtsTokenCount),
	// 末帧 usage 经 completion_tokens_details.reasoning_tokens 透出给客户端。
	thoughtsTokens := 0
	// includeUsage 标记客户端是否请求 stream_options.include_usage;为 true 时收尾发独立 usage chunk。
	// antigravity 链上游恒为 Gemini,末帧 usageMetadata 自带用量,故无需向上游注入 include_usage,
	// 仅决定是否在回译流末尾向客户端补吐 OpenAI 格式 usage chunk。
	includeUsage := streamIncludeUsage

	startInputTokens := inboundInputTokens
	if startInputTokens < 1 {
		startInputTokens = 1 // 保底 1,避免 0 让 CLI 误判上下文为空
	}

	// Anthropic 协议下，开始流时首发 message_start
	if apiFormat == "anthropic" {
		msgStart := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            streamID,
				"type":          "message",
				"role":          "assistant",
				"content":       []interface{}{},
				"model":         displayModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				// usage:首帧 input_tokens 用入站请求本地估算值(保底 1),让客户端(Claude Code spinner
				// 进行中)在流首即可显示 ↑;output_tokens 起始占位为 1(官方惯例预扣占位,与 NVIDIA 路径
				// messageStartPayload 历史一致)。真实累计值由末帧 message_delta.usage 覆盖。
				"usage": map[string]interface{}{
					"input_tokens":                startInputTokens,
					"output_tokens":               1,
					"cache_creation_input_tokens": 0,
					"cache_read_input_tokens":     0,
				},
			},
		}
		msgStartBytes, _ := json.Marshal(msgStart)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(msgStartBytes))
		flusher.Flush()
	}

	// Responses API 专用变量
	seqNum := 0
	nextSeq := func() int {
		seqNum++
		return seqNum
	}
	responsesMsgOpened := false
	responsesMsgID := fmt.Sprintf("msg_%s_0", streamID)
	responsesMsgOutIdx := 0 // 正文 message item 占用的 output_index,首次开块时由 responsesOutIdx 分配
	var responsesTextBuf strings.Builder

	// ===== Responses 协议 reasoning(思考)独立 item 状态机 =====
	// thought 与正文必须拆成各自独立的 output_item,不能共用一个条目。
	//
	// 协议形态对齐 OpenAI 官方 Responses 流式 reasoning summary 家族(与 cc-switch
	// codex_responses_sse.rs 逐字节一致),Codex CLI 只认这套事件才会在 TUI 渲染思考摘要:
	//   item.type = "reasoning",item.summary = [{type:"summary_text",text}]
	//   事件:<output_item.added> → <reasoning_summary_part.added> →
	//        <reasoning_summary_text.delta>* → <reasoning_summary_text.done> →
	//        <reasoning_summary_part.done> → <output_item.done>(done 的 item 无 status 字段)
	// 旧实现用 item.type=message + content[].type=reasoning_text + response.reasoning_text.*,
	// 属非标准自造形态,Codex 客户端不识别 → 思考增量被丢弃 → 不显示。
	//
	// output_index 分配约定(responsesOutIdx 递增计数器,开块即分配并推进):
	//   - reasoning item 开块时取 responsesOutIdx 并 ++(reasoning 先到则占 0)
	//   - 正文 message item 开块时取 responsesOutIdx 并 ++(在其之后)
	//   - 每个 function_call item 开块时取 responsesOutIdx 并 ++
	// 每类 item 用各自记录的 index 续发 delta/done,互不撞车。
	responsesReasoningOpened := false
	responsesReasoningID := fmt.Sprintf("rs_%s", streamID)
	responsesReasoningOutIdx := 0 // reasoning item 锁定的 output_index(开块时赋值)
	var responsesReasoningBuf strings.Builder

	responsesOutIdx := 0
	// closeResponsesReasoning 闭合已开启的 reasoning item:发 reasoning_summary_text.done +
	// reasoning_summary_part.done + output_item.done 三件套,清零状态。幂等(未开则不动作)。
	// 不在此推进 responsesOutIdx(index 在开块时已分配并推进),只发收尾事件。
	// done 的 reasoning item 无 status 字段(对齐 cc-switch reasoning_close_with_item)。
	closeResponsesReasoning := func() {
		if !responsesReasoningOpened {
			return
		}
		reasonsText := responsesReasoningBuf.String()
		reasonDone := map[string]interface{}{
			"type":            "response.reasoning_summary_text.done",
			"sequence_number": nextSeq(),
			"item_id":         responsesReasoningID,
			"output_index":    responsesReasoningOutIdx,
			"summary_index":   0,
			"text":            reasonsText,
		}
		rdBytes, _ := json.Marshal(reasonDone)
		fmt.Fprintf(w, "event: response.reasoning_summary_text.done\ndata: %s\n\n", string(rdBytes))

		reasonPartDone := map[string]interface{}{
			"type":            "response.reasoning_summary_part.done",
			"sequence_number": nextSeq(),
			"item_id":         responsesReasoningID,
			"output_index":    responsesReasoningOutIdx,
			"summary_index":   0,
			"part": map[string]interface{}{
				"type": "summary_text",
				"text": reasonsText,
			},
		}
		rpdBytes, _ := json.Marshal(reasonPartDone)
		fmt.Fprintf(w, "event: response.reasoning_summary_part.done\ndata: %s\n\n", string(rpdBytes))

		reasonItemDone := map[string]interface{}{
			"type":            "response.output_item.done",
			"sequence_number": nextSeq(),
			"output_index":    responsesReasoningOutIdx,
			"item": map[string]interface{}{
				"id":      responsesReasoningID,
				"type":    "reasoning",
				"summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": reasonsText}},
			},
		}
		ridBytes, _ := json.Marshal(reasonItemDone)
		fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(ridBytes))
		responsesReasoningOpened = false
	}

	// Responses 协议下，开始流时首发 response.created 和 response.in_progress
	if apiFormat == "responses" {
		createdEvt := map[string]interface{}{
			"type":            "response.created",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		createdBytes, _ := json.Marshal(createdEvt)
		fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", string(createdBytes))

		inprogEvt := map[string]interface{}{
			"type":            "response.in_progress",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
			},
		}
		inprogBytes, _ := json.Marshal(inprogEvt)
		fmt.Fprintf(w, "event: response.in_progress\ndata: %s\n\n", string(inprogBytes))
		flusher.Flush()
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			h.log("⏹️ 客户端连接已取消，中断流式响应扫描")
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data:")
		dataStr = strings.TrimSpace(dataStr)
		if dataStr == "" {
			continue
		}

		var gemResp GeminiResponse
		if err = json.Unmarshal([]byte(dataStr), &gemResp); err != nil {
			continue
		}

		// 同步更新用量
		if gemResp.UsageMetadata.PromptTokenCount > 0 {
			inTokens = gemResp.UsageMetadata.PromptTokenCount
		}
		if gemResp.UsageMetadata.CandidatesTokenCount > 0 {
			outTokens = gemResp.UsageMetadata.CandidatesTokenCount
		}
		if gemResp.UsageMetadata.ThoughtsTokenCount > 0 {
			thoughtsTokens = gemResp.UsageMetadata.ThoughtsTokenCount
		}

		if len(gemResp.Candidates) == 0 {
			continue
		}

		// 记录 Gemini 末帧非空 finishReason,收尾时经 mapGeminiFinishToOpenAI 映射为 OpenAI finish_reason。
		// Gemini 通常在最后一帧 candidates[0].finishReason 下发终止态(MAX_TOKENS/SAFETY/STOP 等)。
		if len(gemResp.Candidates) > 0 && gemResp.Candidates[0].FinishReason != "" {
			lastFinishReason = gemResp.Candidates[0].FinishReason
		}

		// 只要拿到上游 Candidate 响应，第一时间发射 OpenAI role: "assistant" 声明首包（对齐 Antigravity-Manager 逻辑）
		if apiFormat == "openai" && !openAIRoleSent {
			initChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Role: "assistant"}, FinishReason: nil},
				},
			}
			initBytes, _ := json.Marshal(initChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
			flusher.Flush()
			openAIRoleSent = true
		}

		if len(gemResp.Candidates[0].Content.Parts) == 0 {
			continue
		}

		// 遍历所有 Parts，分别处理 text、thinking 和 functionCall
		for _, part := range gemResp.Candidates[0].Content.Parts {
			cleanText := SanitizeAllThoughtSignatures(part.Text)
			if cleanText != "" {
				if part.Thought {
					// 思考过程分片处理
					if apiFormat == "anthropic" {
						if textBlockOpen {
							stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
							stopBytes, _ := json.Marshal(stopEvt)
							fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
							blockIndex++
							textBlockOpen = false
						}
						if !thinkingBlockOpen {
							startEvt := map[string]interface{}{
								"type":          "content_block_start",
								"index":         blockIndex,
								"content_block": map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""},
							}
							startBytes, _ := json.Marshal(startEvt)
							fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(startBytes))
							thinkingBlockOpen = true
							thinkingEmittedAny = false
						}
						deltaEvt := map[string]interface{}{
							"type":  "content_block_delta",
							"index": blockIndex,
							"delta": map[string]interface{}{"type": "thinking_delta", "thinking": cleanText},
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
						thinkingEmittedAny = true
					} else if apiFormat == "openai" {
						// OpenAI 流式思考走官方独立 reasoning_content 字段,不混入 delta.content,
						// 与 NVIDIA 链 (nvidia_translate_types.go ReasoningContent) 口径对齐。
						// 旧实现把思考当正文 content 推送,客户端会把思考当模型正文显示。
						chunk := OpenAIStreamChunk{
							ID:      streamID,
							Object:  "chat.completion.chunk",
							Created: createdAt,
							Model:   displayModel,
							Choices: []OpenAIStreamChoice{
								{Index: 0, Delta: OpenAIDelta{ReasoningContent: cleanText}, FinishReason: nil},
							},
						}
						chunkBytes, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
					} else if apiFormat == "responses" {
						if !responsesReasoningOpened {
							responsesReasoningOutIdx = responsesOutIdx
							responsesOutIdx++
							itemAdded := map[string]interface{}{
								"type":            "response.output_item.added",
								"sequence_number": nextSeq(),
								"output_index":    responsesReasoningOutIdx,
								"item": map[string]interface{}{
									"id":      responsesReasoningID,
									"type":    "reasoning",
									"status":  "in_progress",
									"summary": []interface{}{},
								},
							}
							itemBytes, _ := json.Marshal(itemAdded)
							fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

							partAdded := map[string]interface{}{
								"type":            "response.reasoning_summary_part.added",
								"sequence_number": nextSeq(),
								"item_id":         responsesReasoningID,
								"output_index":    responsesReasoningOutIdx,
								"summary_index":   0,
								"part": map[string]interface{}{
									"type": "summary_text",
									"text": "",
								},
							}
							partBytes, _ := json.Marshal(partAdded)
							fmt.Fprintf(w, "event: response.reasoning_summary_part.added\ndata: %s\n\n", string(partBytes))
							responsesReasoningOpened = true
						}
						responsesReasoningBuf.WriteString(cleanText)
						deltaEvt := map[string]interface{}{
							"type":            "response.reasoning_summary_text.delta",
							"sequence_number": nextSeq(),
							"item_id":         responsesReasoningID,
							"output_index":    responsesReasoningOutIdx,
							"summary_index":   0,
							"delta":           cleanText,
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: response.reasoning_summary_text.delta\ndata: %s\n\n", string(deltaBytes))
					}
					flusher.Flush()
				} else {
					// 正规 Output Text 消息文本处理
					if apiFormat == "openai" {
						if !openAIRoleSent {
							initChunk := OpenAIStreamChunk{
								ID:      streamID,
								Object:  "chat.completion.chunk",
								Created: createdAt,
								Model:   displayModel,
								Choices: []OpenAIStreamChoice{
									{Index: 0, Delta: OpenAIDelta{Role: "assistant"}, FinishReason: nil},
								},
							}
							initBytes, _ := json.Marshal(initChunk)
							fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
							flusher.Flush()
							openAIRoleSent = true
						}

						chunk := OpenAIStreamChunk{
							ID:      streamID,
							Object:  "chat.completion.chunk",
							Created: createdAt,
							Model:   displayModel,
							Choices: []OpenAIStreamChoice{
								{Index: 0, Delta: OpenAIDelta{Content: cleanText}, FinishReason: nil},
							},
						}
						chunkBytes, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
					} else if apiFormat == "responses" {
						// 进入正文 item 前,先把可能已开启的 reasoning item 闭合掉:
						// 思考与正文是两个独立 output_item,各自的 output_index 由 responsesOutIdx 分配。
						closeResponsesReasoning()
						if !responsesMsgOpened {
							responsesMsgOutIdx = responsesOutIdx
							responsesOutIdx++
							itemAdded := map[string]interface{}{
								"type":            "response.output_item.added",
								"sequence_number": nextSeq(),
								"output_index":    responsesMsgOutIdx,
								"item": map[string]interface{}{
									"id":      responsesMsgID,
									"type":    "message",
									"status":  "in_progress",
									"role":    "assistant",
									"content": []interface{}{},
								},
							}
							itemBytes, _ := json.Marshal(itemAdded)
							fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

							partAdded := map[string]interface{}{
								"type":            "response.content_part.added",
								"sequence_number": nextSeq(),
								"item_id":         responsesMsgID,
								"output_index":    responsesMsgOutIdx,
								"content_index":   0,
								"part": map[string]interface{}{
									"type": "output_text",
									"text": "",
								},
							}
							partBytes, _ := json.Marshal(partAdded)
							fmt.Fprintf(w, "event: response.content_part.added\ndata: %s\n\n", string(partBytes))
							responsesMsgOpened = true
						}
						responsesTextBuf.WriteString(cleanText)

						deltaEvt := map[string]interface{}{
							"type":            "response.output_text.delta",
							"sequence_number": nextSeq(),
							"item_id":         responsesMsgID,
							"output_index":    responsesMsgOutIdx,
							"content_index":   0,
							"delta":           cleanText,
						}
						deltaBytes, _ := json.Marshal(deltaEvt)
						fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", string(deltaBytes))
					} else { // anthropic
						if thinkingBlockOpen {
							// 关 thinking 块前补一条空串 signature_delta(Gemini 已剥真签名,发空串占位),
							// 严格对齐官方 thinking_delta → signature_delta → content_block_stop 序列。
							// 若 thinking 块从未下发过 thinking_delta(thinkingEmittedAny==false,异常只开块),
							// 则不发 signature_delta 也不发 stop —— 但此处 thinkingBlockOpen 必伴随首 delta,
							// 故仅作防御保留对称逻辑,正常路径总会执行 signature_delta。
							if thinkingEmittedAny {
								sigEvt := map[string]interface{}{
									"type":  "content_block_delta",
									"index": blockIndex,
									"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
								}
								sigBytes, _ := json.Marshal(sigEvt)
								fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
								stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
								stopBytes, _ := json.Marshal(stopEvt)
								fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
								blockIndex++
							}
							thinkingBlockOpen = false
							thinkingEmittedAny = false
						}
						// 延迟开启 text block：仅在有实际文本时才发送 content_block_start
						if !textBlockOpen {
							blockStart := map[string]interface{}{
								"type":          "content_block_start",
								"index":         blockIndex,
								"content_block": map[string]interface{}{"type": "text", "text": ""},
							}
							blockStartBytes, _ := json.Marshal(blockStart)
							fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(blockStartBytes))
							textBlockOpen = true
						}
						delta := map[string]interface{}{
							"type":  "content_block_delta",
							"index": blockIndex,
							"delta": map[string]interface{}{"type": "text_delta", "text": cleanText},
						}
						deltaBytes, _ := json.Marshal(delta)
						fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
					}
					flusher.Flush()
				}
			}

			if part.FunctionCall != nil {
				if apiFormat == "openai" {
					hasOpenAIToolCall = true
					// 每个 functionCall part 独占递增 index,替代旧硬编码 0。
					// OpenAI 官方要求同一流内每个 tool_call 按递增 index 累积 name/arguments,
					// 旧实现两个并行的 functionCall 同用 index=0 会导致第二个覆盖第一个的 name
					// 或 arguments 归属错乱。
					openAIToolCallIdx++
					toolCallIndex := openAIToolCallIdx
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					startChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   displayModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: toolCallIndex,
											ID:    callID,
											Type:  "function",
											Function: OpenAIToolCallFunction{
												Name:      part.FunctionCall.Name,
												Arguments: "",
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					startBytes, _ := json.Marshal(startChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(startBytes))

					argsChunk := OpenAIStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: createdAt,
						Model:   displayModel,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									ToolCalls: []OpenAIToolCall{
										{
											Index: toolCallIndex,
											Function: OpenAIToolCallFunction{
												Arguments: string(argsJSON),
											},
										},
									},
								},
								FinishReason: nil,
							},
						},
					}
					argsBytes, _ := json.Marshal(argsChunk)
					fmt.Fprintf(w, "data: %s\n\n", string(argsBytes))
					flusher.Flush()
				} else if apiFormat == "responses" {
					// 进入工具调用 item 前,先关掉可能还开着的 reasoning item(思考与工具不同 item);
					// 正文 message item 若已开则保持开启(一个 response 可同时含 message + function_call)。
					closeResponsesReasoning()
					callID := fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), rand.Int63n(1000))
					fcItemID := fmt.Sprintf("fc_%s", callID)
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					argsStr := string(argsJSON)

					// 每个工具调用独占一个 output_index,开块即分配并推进
					fcOutIdx := responsesOutIdx
					responsesOutIdx++

					itemAdded := map[string]interface{}{
						"type":            "response.output_item.added",
						"sequence_number": nextSeq(),
						"output_index":    fcOutIdx,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "in_progress",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": "",
						},
					}
					itemBytes, _ := json.Marshal(itemAdded)
					fmt.Fprintf(w, "event: response.output_item.added\ndata: %s\n\n", string(itemBytes))

					deltaEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.delta",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    fcOutIdx,
						"call_id":         callID,
						"delta":           argsStr,
					}
					deltaBytes, _ := json.Marshal(deltaEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.delta\ndata: %s\n\n", string(deltaBytes))

					doneEvt := map[string]interface{}{
						"type":            "response.function_call_arguments.done",
						"sequence_number": nextSeq(),
						"item_id":         fcItemID,
						"output_index":    fcOutIdx,
						"call_id":         callID,
						"arguments":       argsStr,
					}
					doneBytes, _ := json.Marshal(doneEvt)
					fmt.Fprintf(w, "event: response.function_call_arguments.done\ndata: %s\n\n", string(doneBytes))

					itemDone := map[string]interface{}{
						"type":            "response.output_item.done",
						"sequence_number": nextSeq(),
						"output_index":    fcOutIdx,
						"item": map[string]interface{}{
							"id":        fcItemID,
							"type":      "function_call",
							"status":    "completed",
							"name":      part.FunctionCall.Name,
							"call_id":   callID,
							"arguments": argsStr,
						},
					}
					itemDoneBytes, _ := json.Marshal(itemDone)
					fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemDoneBytes))
					flusher.Flush()
				} else if apiFormat == "anthropic" {
					hasFunctionCall = true

					// 先关闭未完成的 thinking block(与 text block 互斥,任一时刻至多其一开启)。
					// 旧实现只关 textBlockOpen、漏关 thinkingBlockOpen:模型"先思考再做工具调用"
					// (Claude Code + gemini 注入 includeThoughts 后的常态)时,thinking 块遗留未闭合,
					// 此处 tool_use 的 content_block_start 覆盖了 thinking 原索引(blockIndex 未推进),
					// 末尾收尾段再见 thinkingBlockOpen==true 在已偏移的 blockIndex 上补发
					// signature_delta/content_block_stop,命中从未 start 过的索引,
					// Claude Code cr[index] 查无此块 → "Content block not found"。
					// 补此闭合以对称 text 分支/末尾收尾两处的 thinking 关块逻辑。
					if thinkingBlockOpen {
						// 仅当确实下发过 thinking_delta 才补 signature_delta + stop(对齐官方
						// thinking_delta → signature_delta → content_block_stop 序列);
						// thinkingEmittedAny==false 属不可达防御(thought 分支 start 与 delta 同迭代下发),
						// 与 text 分支/收尾段守卫语义一致,不引入新行为。
						if thinkingEmittedAny {
							sigEvt := map[string]interface{}{
								"type":  "content_block_delta",
								"index": blockIndex,
								"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
							}
							sigBytes, _ := json.Marshal(sigEvt)
							fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
							stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
							stopBytes, _ := json.Marshal(stopEvt)
							fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
							blockIndex++
						}
						thinkingBlockOpen = false
						thinkingEmittedAny = false
						flusher.Flush()
					}

					// 再关闭未完成的 text block
					if textBlockOpen {
						stopEvt := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
						stopBytes, _ := json.Marshal(stopEvt)
						fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
						blockIndex++
						textBlockOpen = false
						flusher.Flush()
					}

					toolID := generateToolUseID()

					// content_block_start: tool_use
					toolStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": blockIndex,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    toolID,
							"name":  part.FunctionCall.Name,
							"input": map[string]interface{}{},
						},
					}
					toolStartBytes, _ := json.Marshal(toolStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(toolStartBytes))

					// content_block_delta: input_json_delta（一次性发完 args JSON）
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					if len(argsJSON) == 0 || string(argsJSON) == "null" {
						argsJSON = []byte("{}")
					}
					inputDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": blockIndex,
						"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": string(argsJSON)},
					}
					inputDeltaBytes, _ := json.Marshal(inputDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(inputDeltaBytes))

					// content_block_stop
					toolStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
					toolStopBytes, _ := json.Marshal(toolStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(toolStopBytes))
					blockIndex++

					flusher.Flush()
				}
			}
		}
	}

	// 发射结束帧
	if apiFormat == "openai" {
		if !openAIRoleSent && !hasOpenAIToolCall {
			initChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Role: "assistant", Content: "I have processed your request."}, FinishReason: nil},
				},
			}
			initBytes, _ := json.Marshal(initChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
			flusher.Flush()
			openAIRoleSent = true
		} else if openAIRoleSent && outTokens == 0 && !hasOpenAIToolCall {
			// 如果已经发过 role: "assistant" 初始帧，但上游因为 finishReason: OTHER 输出了 0 个 OutTokens
			// 补发非空 Content 帧，防止 Codex CLI 接收空 Content 而静默断开
			fallbackChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{
					{Index: 0, Delta: OpenAIDelta{Content: "I have processed your request."}, FinishReason: nil},
				},
			}
			fallbackBytes, _ := json.Marshal(fallbackChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(fallbackBytes))
			flusher.Flush()
		}

		// finishReason 经 Gemini->OpenAI 映射,不再硬编码 stop/tool_calls;
		// 支持 length(MAX_TOKENS 截断)/content_filter(安全拦截)等终端态,
		// 让 Claude Code/Codex 正确识别截断而非当成正常 stop。
		finishReason := mapGeminiFinishToOpenAI(lastFinishReason, hasOpenAIToolCall)
		finalChunk := OpenAIStreamChunk{
			ID:      streamID,
			Object:  "chat.completion.chunk",
			Created: createdAt,
			Model:   displayModel,
			Choices: []OpenAIStreamChoice{
				{Index: 0, Delta: OpenAIDelta{}, FinishReason: finishReason},
			},
		}
		finalBytes, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(finalBytes))

		// stream_options.include_usage:客户端显式请求时,在 finish_reason chunk 之后、[DONE] 之前,
		// 按 OpenAI 官方规范补发一个 choices 为空 + 带 usage 的独立 chunk(与 NVIDIA 链
		// nvidia_responses.go 的 include_usage 末帧语义对齐)。上游 Gemini 末帧 usageMetadata
		// 已在主循环中累计到 inTokens/outTokens/thoughtsTokens,这里直接回吐 OpenAI 格式。
		if includeUsage {
			usageChunk := OpenAIStreamChunk{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: createdAt,
				Model:   displayModel,
				Choices: []OpenAIStreamChoice{},
				Usage:   buildOpenAIUsagePtr(inTokens, outTokens, thoughtsTokens),
			}
			usageBytes, _ := json.Marshal(usageChunk)
			fmt.Fprintf(w, "data: %s\n\n", string(usageBytes))
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	} else if apiFormat == "responses" {
		// 收尾前先闭合可能仍开着的 reasoning item(若思考是最后一个 part 且后续无正文,
		// 循环内未触发 closeResponsesReasoning,在此补收尾 done 三件套)。
		closeResponsesReasoning()
		fullText := responsesTextBuf.String()
		var outputItems []interface{}

		if responsesMsgOpened {
			txtDone := map[string]interface{}{
				"type":            "response.output_text.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    responsesMsgOutIdx,
				"content_index":   0,
				"text":            fullText,
			}
			txtBytes, _ := json.Marshal(txtDone)
			fmt.Fprintf(w, "event: response.output_text.done\ndata: %s\n\n", string(txtBytes))

			partDone := map[string]interface{}{
				"type":            "response.content_part.done",
				"sequence_number": nextSeq(),
				"item_id":         responsesMsgID,
				"output_index":    responsesMsgOutIdx,
				"content_index":   0,
				"part": map[string]interface{}{
					"type": "output_text",
					"text": fullText,
				},
			}
			partBytes, _ := json.Marshal(partDone)
			fmt.Fprintf(w, "event: response.content_part.done\ndata: %s\n\n", string(partBytes))

			itemMsg := map[string]interface{}{
				"id":      responsesMsgID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": fullText}},
			}
			itemDone := map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSeq(),
				"output_index":    responsesMsgOutIdx,
				"item":            itemMsg,
			}
			itemBytes, _ := json.Marshal(itemDone)
			fmt.Fprintf(w, "event: response.output_item.done\ndata: %s\n\n", string(itemBytes))

			outputItems = append(outputItems, itemMsg)
		}

		completedEvt := map[string]interface{}{
			"type":            "response.completed",
			"sequence_number": nextSeq(),
			"response": map[string]interface{}{
				"id":         streamID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "completed",
				"usage": map[string]interface{}{
					"input_tokens":  inTokens,
					"output_tokens": outTokens,
					"total_tokens":  inTokens + outTokens,
				},
				"output": outputItems,
			},
		}
		completedBytes, _ := json.Marshal(completedEvt)
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", string(completedBytes))
	} else { // anthropic
		// 关闭未完成的 thinking block / text block
		if thinkingBlockOpen {
			// 关 thinking 块前补一条空串 signature_delta(仅当确实下发过 thinking_delta):
			// 严格对齐官方序列 thinking_delta → signature_delta → content_block_stop。
			if thinkingEmittedAny {
				sigEvt := map[string]interface{}{
					"type":  "content_block_delta",
					"index": blockIndex,
					"delta": map[string]interface{}{"type": "signature_delta", "signature": ""},
				}
				sigBytes, _ := json.Marshal(sigEvt)
				fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(sigBytes))
				blockStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
				blockStopBytes, _ := json.Marshal(blockStop)
				fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))
				blockIndex++
			}
			thinkingBlockOpen = false
			thinkingEmittedAny = false
		}
		if textBlockOpen {
			blockStop := map[string]interface{}{"type": "content_block_stop", "index": blockIndex}
			blockStopBytes, _ := json.Marshal(blockStop)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))
			textBlockOpen = false
		}

		// 兜底：如果完全没有打开过任何 content block (blockIndex == 0 && !hasFunctionCall)，给 Anthropic / Claude Code 客户端发射一个空 text content block，确保 Claude Code 不会报错
		if blockIndex == 0 && !hasFunctionCall {
			startEvt := map[string]interface{}{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]interface{}{"type": "text", "text": ""},
			}
			startBytes, _ := json.Marshal(startEvt)
			fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(startBytes))

			deltaEvt := map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{"type": "text_delta", "text": ""},
			}
			deltaBytes, _ := json.Marshal(deltaEvt)
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))

			stopEvt := map[string]interface{}{"type": "content_block_stop", "index": 0}
			stopBytes, _ := json.Marshal(stopEvt)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
		}

		stopReason := "end_turn"
		if hasFunctionCall {
			stopReason = "tool_use"
		}

		msgDelta := map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			// usage 双填 input/output：官方 cumulative 语义与 NVIDIA 链(openAIChatSSEToAnthropicSSEInto)
			// 对齐。inTokens/outTokens 取自 Gemini usageMetadata(逐帧更新,末帧权威)，
			// 让 Claude Code 客户端上下文台账拿到上游真实消耗量,而非仅 message_start 估算值。
			"usage": map[string]interface{}{
				"output_tokens": outTokens,
				"input_tokens":  inTokens,
			},
		}
		msgDeltaBytes, _ := json.Marshal(msgDelta)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", string(msgDeltaBytes))

		fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}
	flusher.Flush()

	// 记录用量统计

}
