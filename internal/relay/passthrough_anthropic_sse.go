package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// passthrough_anthropic_sse.go: Other 号池「入站 OpenAI Chat + 上游 Anthropic 原生端点」场景的 SSE 响应回译。
//
// 把上游 Anthropic Messages SSE 事件流实时重写为 OpenAI Chat Completions SSE 事件流。
// 对偶 nvidia_translate_sse.go 的 OpenAIChatSSEToAnthropicSSE(反向)。
//
// 仅在 passthroughForward.upstreamFormat=="anthropic" 且入站协议为 OpenAI Chat / Responses 时调用。

// anthropicSSEToOpenAIChatSSEInto 是 AnthropicSSEToOpenAIChatSSE 的实现,接收 io.Reader/io.Writer 后能风格。
// 逐行扫描上游 SSE,按 Anthropic 事件类型翻译为 OpenAI Chat SSE chunk。
func anthropicSSEToOpenAIChatSSEInto(reader io.Reader, writer io.Writer, model string) (input, output int, err error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	chunkID := fmt.Sprintf("chatcmpl-other-%d", nowNano())
	var pendingToolName string
	var pendingToolID string
	var toolArgBuf strings.Builder
	toolIndex := -1
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		// Anthropic SSE 事件结构:{type:"...", ...其它字段}
		var ev struct {
			Type         string          `json:"type"`
			Message      json.RawMessage `json:"message,omitempty"`
			Index        *int            `json:"index,omitempty"`
			ContentBlock json.RawMessage `json:"content_block,omitempty"`
			Delta        json.RawMessage `json:"delta,omitempty"`
			Usage        json.RawMessage `json:"usage,omitempty"`
		}
		if json.Unmarshal([]byte(data), &ev) == nil && ev.Type != "" {
			switch ev.Type {
			case "message_start":
				// 发首个 chunk:role=assistant,空 content
				firstChunk := OpenAIChatStreamChunk{
					ID: chunkID, Object: "chat.completion.chunk", Model: model,
					Choices: []OpenAIChatStreamChoice{{
						Index: 0,
						Delta: OpenAIChatDelta{Role: "assistant", Content: ""},
					}},
				}
				writeSSEChunk(writer, firstChunk)
			case "content_block_start":
				// 解析 content_block.type:text → 文本块开始;tool_use → 工具块开始(记下 id/name/index)。
				var cb struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				_ = json.Unmarshal(ev.ContentBlock, &cb)
				if cb.Type == "tool_use" {
					toolIndex++
					pendingToolID = cb.ID
					pendingToolName = cb.Name
					toolArgBuf.Reset()
				}
			case "content_block_delta":
				// 解析 delta.type:text_delta → 拼正文;input_json_delta → 拼 tool args;
				// thinking_delta → 推 reasoning_content 增量(对齐 OpenAI 官方 reasoning_content 流式字段,
				// 对偶 nvidia_translate_sse.go 的反向 emitThinkingDelta);signature_delta → 跳过(OpenAI 无对应)。
				var d struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
				}
				_ = json.Unmarshal(ev.Delta, &d)
				if d.Type == "text_delta" && d.Text != "" {
					chunk := OpenAIChatStreamChunk{
						ID: chunkID, Object: "chat.completion.chunk", Model: model,
						Choices: []OpenAIChatStreamChoice{{
							Index: 0,
							Delta: OpenAIChatDelta{Content: d.Text},
						}},
					}
					writeSSEChunk(writer, chunk)
				} else if d.Type == "thinking_delta" && d.Thinking != "" {
					// 思考增量 → OpenAI delta.reasoning_content。守非空避免发空 reasoning chunk。
					chunk := OpenAIChatStreamChunk{
						ID: chunkID, Object: "chat.completion.chunk", Model: model,
						Choices: []OpenAIChatStreamChoice{{
							Index: 0,
							Delta: OpenAIChatDelta{ReasoningContent: d.Thinking},
						}},
					}
					writeSSEChunk(writer, chunk)
				} else if d.Type == "signature_delta" {
					// OpenAI 无思考签名字段,跳过(对齐 nvidia_translate_sse.go 关 thinking 块时 signature_delta 占位空串语义)。
				} else if d.Type == "input_json_delta" && d.PartialJSON != "" {
					toolArgBuf.WriteString(d.PartialJSON)
				}
			case "content_block_stop":
				// 若有积攒的 tool_use 块,发一个 tool_calls delta 增量。
				if pendingToolName != "" {
					toolCall := ChatToolCall{
						Index: toolIndex,
						ID:    pendingToolID,
						Type:  "function",
						Function: ChatToolCallFunction{
							Name:      pendingToolName,
							Arguments: toolArgBuf.String(),
						},
					}
					chunk := OpenAIChatStreamChunk{
						ID: chunkID, Object: "chat.completion.chunk", Model: model,
						Choices: []OpenAIChatStreamChoice{{
							Index: 0,
							Delta: OpenAIChatDelta{ToolCalls: []ChatToolCall{toolCall}},
						}},
					}
					writeSSEChunk(writer, chunk)
					pendingToolName = ""
					pendingToolID = ""
					toolArgBuf.Reset()
				}
			case "message_delta":
				// 解析 delta.usage(input/output tokens)与 delta.stop_reason → 写 finish chunk + usage。
				var d struct {
					StopReason string                 `json:"stop_reason"`
					Usage      AnthropicResponseUsage `json:"usage"`
				}
				_ = json.Unmarshal(ev.Delta, &d)
				output = d.Usage.OutputTokens
				finish := anthropicStopToOpenAIFinish(d.StopReason)
				chunk := OpenAIChatStreamChunk{
					ID: chunkID, Object: "chat.completion.chunk", Model: model,
					Choices: []OpenAIChatStreamChoice{{
						Index:        0,
						Delta:        OpenAIChatDelta{},
						FinishReason: finish,
					}},
				}
				writeSSEChunkWithUsage(writer, chunk, d.Usage.InputTokens, d.Usage.OutputTokens)
			case "message_stop":
				// OpenAI 协议权威终止符 [DONE]。
				_, _ = writer.Write([]byte("data: [DONE]\n\n"))
				return input, output, nil
			}
		}
	}

	// 流异常结束(未收到 message_stop),补一个 finish + [DONE] 尾帧兜底,避免客户端卡等。
	finishChunk := OpenAIChatStreamChunk{
		ID: chunkID, Object: "chat.completion.chunk", Model: model,
		Choices: []OpenAIChatStreamChoice{{
			Index:        0,
			Delta:        OpenAIChatDelta{},
			FinishReason: "stop",
		}},
	}
	writeSSEChunk(writer, finishChunk)
	_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	return input, output, err
}

// writeSSEChunk 把一个 OpenAIChatStreamChunk 序列化为 SSE data 行写出。
func writeSSEChunk(w io.Writer, chunk OpenAIChatStreamChunk) {
	b, _ := json.Marshal(chunk)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}

// writeSSEChunkWithUsage 写带 usage 的 chunk(OpenAI 流式末尾独立 usage chunk 形态:
// choices 为空数组 + usage 对象),供客户端回收 token 计费。
func writeSSEChunkWithUsage(w io.Writer, chunk OpenAIChatStreamChunk, prompt, completion int) {
	// 先写 finish chunk(choices 非空)
	writeSSEChunk(w, chunk)
	// 再写独立 usage chunk(choices 空 + usage)
	usageChunk := struct {
		ID      string          `json:"id"`
		Object  string          `json:"object"`
		Model   string          `json:"model"`
		Choices []interface{}   `json:"choices"`
		Usage   OpenAIChatUsage `json:"usage"`
	}{
		ID: chunk.ID, Object: "chat.completion.chunk", Model: chunk.Model,
		Choices: []interface{}{},
		Usage:   OpenAIChatUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion},
	}
	b, _ := json.Marshal(usageChunk)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}

// nowNano 返回当前纳秒时间戳,用于生成 chunk ID。
func nowNano() int64 {
	return time.Now().UnixNano()
}
