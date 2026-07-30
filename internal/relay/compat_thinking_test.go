package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// compat_thinking_test.go 锁定 Gemini 直连路径(handleStreamResponse, apiFormat=anthropic)
// 对 part.Thought 的 thinking 块输出,验证关 thinking 块前补一条空串 signature_delta,
// 严格对齐官方序列 thinking_delta → signature_delta → content_block_stop → text 块。

// geminiThoughtSSE 构造一帧含 thought:true 的 Gemini 上游 SSE 响应(data 行 JSON)。
func geminiThoughtSSE(text string, thought bool) string {
	part := map[string]interface{}{"text": text}
	if thought {
		part["thought"] = true
	}
	resp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{part},
				},
				"finishReason": "STOP",
			},
		},
	}
	b, _ := json.Marshal(resp)
	return "data: " + string(b) + "\n\n"
}

// runGeminiAnthropicStream 用给定的 Gemini 上游 SSE 喂入 handleStreamResponse(anthropic),
// 返回转译后的 SSE 文本(flushCounter 内嵌 *httptest.ResponseRecorder 同时实现 http.Flusher)。
func runGeminiAnthropicStream(t *testing.T, upstream string) string {
	t.Helper()
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	fc := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	h.handleStreamResponse(
		context.Background(),
		fc,
		strings.NewReader(upstream),
		&RelaySession{},
		"gemini-2.5-flash",
		"gemini-2.5-flash",
		"anthropic",
		time.Unix(1700000000, 0),
		"/v1internal:streamGenerateContent",
		"req-test",
	)
	return fc.Body.String()
}

// TestGeminiThoughtEmitsThinkingAndSignatureDelta 锁定 Gemini thought → Anthropic 完整序列:
// content_block_start(thinking) → thinking_delta → signature_delta(空) → content_block_stop。
func TestGeminiThoughtEmitsThinkingAndSignatureDelta(t *testing.T) {
	upstream := geminiThoughtSSE("Let me compute.", true) + geminiThoughtSSE("done thinking.", true)
	got := runGeminiAnthropicStream(t, upstream)
	events := parseSSEEvents(got)

	requireEvent(t, events, "message_start")
	requireEvent(t, events, "content_block_start")
	requireEvent(t, events, "content_block_delta")
	requireEvent(t, events, "content_block_stop")
	requireEvent(t, events, "message_delta")
	requireEvent(t, events, "message_stop")

	// 断言存在 thinking_delta 与 signature_delta 两类 delta,且 signature 为空串
	var hasThinkingDelta, hasSignatureDelta bool
	var thinkingText string
	for _, ev := range events {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta == nil {
			continue
		}
		switch delta["type"] {
		case "thinking_delta":
			hasThinkingDelta = true
			if s, ok := delta["thinking"].(string); ok {
				thinkingText += s
			}
		case "signature_delta":
			hasSignatureDelta = true
			if delta["signature"] != "" {
				t.Fatalf("Gemini signature_delta 必须为空串占位,实际=%v", delta["signature"])
			}
		case "text_delta":
			t.Fatalf("纯 thought 流不应出现 text_delta,events=%v", eventNames(events))
		}
	}
	if !hasThinkingDelta {
		t.Fatalf("缺少 thinking_delta 事件,events=%v", eventNames(events))
	}
	if !hasSignatureDelta {
		t.Fatalf("Gemini 路径关 thinking 块前必须补一条空串 signature_delta,events=%v", eventNames(events))
	}
	if !strings.Contains(thinkingText, "compute") {
		t.Fatalf("thinking_delta 文本累积不完整,实际=%q", thinkingText)
	}
}

// TestGeminiThoughtThenTextCorrectOrder 锁定 thought 在前、正文在后,
// signature_delta+stop 夹在思考与正文之间。
func TestGeminiThoughtThenTextCorrectOrder(t *testing.T) {
	// 一次 thought + 一次正文(分两帧 Gemini 上游)
	upstream := geminiThoughtSSE("reasoning here", true) + geminiThoughtSSE("final answer", false)
	got := runGeminiAnthropicStream(t, upstream)
	events := parseSSEEvents(got)

	// 预期:key 节点出现顺序
	// message_start → cbs(thinking) → cbd(thinking_delta) → cbd(signature_delta) → cbs_stop
	// → cbs(text) → cbd(text_delta) → cbs_stop → message_delta → message_stop
	want := []string{
		"message_start",
		"content_block_start",  // thinking
		"content_block_delta", // thinking_delta
		"content_block_delta", // signature_delta
		"content_block_stop",
		"content_block_start", // text
		"content_block_delta", // text_delta
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	gotNames := eventNames(events)
	if len(gotNames) < len(want) {
		t.Fatalf("事件数不足 want=%v got=%v", want, gotNames)
	}
	// 逐个比对关键骨架(允许中间有额外 thinking_delta 分片,但骨架子序列必须匹配)
	j := 0
	for _, w := range want {
		found := false
		for ; j < len(gotNames); j++ {
			if gotNames[j] == w {
				found = true
				j++
				break
			}
		}
		if !found {
			t.Fatalf("未按预期顺序找到 %q,剩余 got=%v", w, gotNames[min(j, len(gotNames)-1):])
		}
	}
}

// TestGeminiNoThoughtKeepsNoSignatureDelta 锁定无 thought 的纯正文路径不出现 thinking/signature。
func TestGeminiNoThoughtKeepsNoSignatureDelta(t *testing.T) {
	upstream := geminiThoughtSSE("just text", false)
	got := runGeminiAnthropicStream(t, upstream)
	events := parseSSEEvents(got)
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Fatalf("无 thought 流不应开 thinking 块")
			}
		}
		if ev.event == "content_block_delta" {
			dm := dataMap(t, ev)
			delta, _ := dm["delta"].(map[string]interface{})
			if delta != nil && (delta["type"] == "thinking_delta" || delta["type"] == "signature_delta") {
				t.Fatalf("无 thought 流不应发 thinking_delta/signature_delta")
			}
		}
	}
	requireEvent(t, events, "content_block_start")
	requireEvent(t, events, "message_stop")
}

// ===== Gemini 路径 usage 对齐 Anthropic 官方流式规范的回归锁定 =====
//
// 对应改动:compat.go handleStreamResponse(anthropic) 两处 usage 修正——
//   - message_start.usage.output_tokens 起始占位为 1(官方惯例,与 NVIDIA 路径一致);
//   - message_delta.usage 补齐 input_tokens(累计输入),与 output_tokens 双填,
//     官方明确 message_delta 的 token 计数为累计值(cumulative)。
// 对照基线:nvidia_test.go TestOpenAIChatSSEToAnthropicSSE_AnthropicUsageCompliance。

// geminiUsageSSE 构造一帧带 usageMetadata(不含 candidates)的 Gemini 上游 SSE。
// 真实 NIM/Gemini 上游常在正文流末尾发一帧只含 usageMetadata 的帧,本测试据此喂累计 in/out。
func geminiUsageSSE(promptTokens, candidatesTokens int) string {
	resp := map[string]interface{}{
		"usageMetadata": map[string]interface{}{
			"promptTokenCount":     promptTokens,
			"candidatesTokenCount": candidatesTokens,
		},
	}
	b, _ := json.Marshal(resp)
	return "data: " + string(b) + "\n\n"
}

// extractMessageDeltaJSON 从转译后的 SSE 文本里切出 message_delta 事件的 data 行 JSON。
func extractMessageDeltaJSON(t *testing.T, sse string) string {
	t.Helper()
	idx := strings.Index(sse, "event: message_delta\n")
	if idx < 0 {
		t.Fatalf("缺少 message_delta 事件, sse=\n%s", sse)
	}
	dataIdx := strings.Index(sse[idx:], "data: ")
	if dataIdx < 0 {
		t.Fatalf("message_delta 缺 data 行, sse=\n%s", sse)
	}
	dataStart := idx + dataIdx + len("data: ")
	dataEnd := strings.Index(sse[dataStart:], "\n")
	if dataEnd < 0 {
		t.Fatalf("message_delta data 行未闭合, sse=\n%s", sse)
	}
	return sse[dataStart : dataStart+dataEnd]
}

// extractMessageStartJSON 从转译后的 SSE 文本里切出 message_start 事件的 data 行 JSON。
func extractMessageStartJSON(t *testing.T, sse string) string {
	t.Helper()
	idx := strings.Index(sse, "event: message_start\n")
	if idx < 0 {
		t.Fatalf("缺少 message_start 事件, sse=\n%s", sse)
	}
	dataIdx := strings.Index(sse[idx:], "data: ")
	if dataIdx < 0 {
		t.Fatalf("message_start 缺 data 行, sse=\n%s", sse)
	}
	dataStart := idx + dataIdx + len("data: ")
	dataEnd := strings.Index(sse[dataStart:], "\n")
	if dataEnd < 0 {
		t.Fatalf("message_start data 行未闭合, sse=\n%s", sse)
	}
	return sse[dataStart : dataStart+dataEnd]
}

// TestGeminiAnthropicStream_UsageCompliance 锁定 Gemini 直连路径 message_start / message_delta
// 的 usage 负载严格对齐 Anthropic 官方流式规范:
//  1. message_start.usage.output_tokens 起始占位必须为 1(input_tokens 此阶段为 0);
//  2. message_delta.usage 必须双填累计值 input_tokens + output_tokens(对应上游 PromptTokenCount/CandidatesTokenCount);
//  3. stop_reason 在 message_delta.delta 内、message_stop 形态正确(协议结构不受 usage 改动影响)。
func TestGeminiAnthropicStream_UsageCompliance(t *testing.T) {
	// 正文帧 + 末帧只带 usageMetadata 的累计帧(模拟真实 Gemini 上游收尾形态)。
	upstream := geminiThoughtSSE("final answer", false) +
		geminiUsageSSE(100, 42)
	got := runGeminiAnthropicStream(t, upstream)

	// 1) message_start.usage: output_tokens 起始为 1,input_tokens 为 0
	startJSON := extractMessageStartJSON(t, got)
	var startParsed struct {
		Type    string `json:"type"`
		Message struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(startJSON), &startParsed); err != nil {
		t.Fatalf("message_start JSON 解析失败: %v, raw=%s", err, startJSON)
	}
	if startParsed.Type != "message_start" {
		t.Errorf("message_start.type 期望 message_start, 实际=%q", startParsed.Type)
	}
	if startParsed.Message.Usage.OutputTokens != 1 {
		t.Errorf("message_start.usage.output_tokens 期望 1(官方惯例起始占位), 实际=%d",
			startParsed.Message.Usage.OutputTokens)
	}
	if startParsed.Message.Usage.InputTokens != 0 {
		t.Errorf("message_start.usage.input_tokens 期望 0(起始无累计), 实际=%d",
			startParsed.Message.Usage.InputTokens)
	}

	// 2) message_delta.usage: 双填累计值 input_tokens=100, output_tokens=42
	deltaJSON := extractMessageDeltaJSON(t, got)
	var deltaParsed struct {
		Type  string `json:"type"`
		Delta struct {
			StopReason   string  `json:"stop_reason"`
			StopSequence *string `json:"stop_sequence"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(deltaJSON), &deltaParsed); err != nil {
		t.Fatalf("message_delta JSON 解析失败: %v, raw=%s", err, deltaJSON)
	}
	if deltaParsed.Type != "message_delta" {
		t.Errorf("message_delta.type 期望 message_delta, 实际=%q", deltaParsed.Type)
	}
	if deltaParsed.Usage.InputTokens != 100 {
		t.Errorf("message_delta.usage.input_tokens 期望 100(累计真实值 PromptTokenCount), 实际=%d",
			deltaParsed.Usage.InputTokens)
	}
	if deltaParsed.Usage.OutputTokens != 42 {
		t.Errorf("message_delta.usage.output_tokens 期望 42(累计真实值 CandidatesTokenCount), 实际=%d",
			deltaParsed.Usage.OutputTokens)
	}
	// stop_reason 在 delta 内、收尾 end_turn(纯正文流无 function call)
	if deltaParsed.Delta.StopReason != "end_turn" {
		t.Errorf("message_delta.delta.stop_reason 期望 end_turn, 实际=%q", deltaParsed.Delta.StopReason)
	}
	// 3) message_stop 形态正确(usage 改动不应破坏协议结构)
	if !strings.Contains(got, "event: message_stop\ndata: {\"type\":\"message_stop\"}") {
		t.Errorf("message_stop 形态应为 {\"type\":\"message_stop\"}, sse=\n%s", got)
	}
}

// TestGeminiAnthropicStream_UsageCompliance_ToolUseStopReason 互补锁定:带 function call 时
// usage 双填仍生效,且 stop_reason 切 tool_use,确保 usage 修正不干扰 stop_reason 语义。
func TestGeminiAnthropicStream_UsageCompliance_ToolUseStopReason(t *testing.T) {
	// 一帧含 functionCall 的 Gemini 上游 + 末帧 usageMetadata
	part := map[string]interface{}{
		"functionCall": map[string]interface{}{"name": "get_weather", "args": map[string]interface{}{"loc": "NYC"}},
	}
	resp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{part},
				},
				"finishReason": "STOP",
			},
		},
	}
	b, _ := json.Marshal(resp)
	upstream := "data: " + string(b) + "\n\n" + geminiUsageSSE(50, 8)
	got := runGeminiAnthropicStream(t, upstream)

	deltaJSON := extractMessageDeltaJSON(t, got)
	var deltaParsed struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(deltaJSON), &deltaParsed); err != nil {
		t.Fatalf("message_delta JSON 解析失败: %v, raw=%s", err, deltaJSON)
	}
	if deltaParsed.Delta.StopReason != "tool_use" {
		t.Errorf("带 functionCall 的 stop_reason 期望 tool_use, 实际=%q", deltaParsed.Delta.StopReason)
	}
	if deltaParsed.Usage.InputTokens != 50 || deltaParsed.Usage.OutputTokens != 8 {
		t.Errorf("tool 路径 usage 双填期望 (50,8), 实际=(%d,%d)",
			deltaParsed.Usage.InputTokens, deltaParsed.Usage.OutputTokens)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
