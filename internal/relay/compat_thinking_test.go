package relay

import (
	"context"
	"encoding/json"
	"fmt"
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
// inboundInputTokens 透传给 handleStreamResponse 的 message_start.usage.input_tokens 估值,默认 0
// (进 handleStreamResponse 后会保底为 1);非负即保底,与生产口径一致。
func runGeminiAnthropicStream(t *testing.T, upstream string) string {
	return runGeminiAnthropicStreamWithInput(t, upstream, 0)
}

// runGeminiAnthropicStreamWithInput 同 runGeminiAnthropicStream 但可指定首帧 input_tokens 估值,
// 供「message_start.usage.input_tokens 非零估值」断言用例使用。
func runGeminiAnthropicStreamWithInput(t *testing.T, upstream string, inboundInputTokens int) string {
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
		inboundInputTokens,
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
//  1. message_start.usage.output_tokens 起始占位必须为 1;input_tokens 为入站估算值(handleStreamResponse
//     保底 1,让 Claude Code spinner 流首即显示 ↑,替代旧 0 占位);
//  2. message_delta.usage 必须双填累计值 input_tokens + output_tokens(对应上游 PromptTokenCount/CandidatesTokenCount);
//  3. stop_reason 在 message_delta.delta 内、message_stop 形态正确(协议结构不受 usage 改动影响)。
func TestGeminiAnthropicStream_UsageCompliance(t *testing.T) {
	// 正文帧 + 末帧只带 usageMetadata 的累计帧(模拟真实 Gemini 上游收尾形态)。
	upstream := geminiThoughtSSE("final answer", false) +
		geminiUsageSSE(100, 42)
	// 首帧 input_tokens 传估算值 50,验证透传与保底(≥1)生效;末帧 message_delta 仍用上游真实累计值覆盖。
	got := runGeminiAnthropicStreamWithInput(t, upstream, 50)

	// 1) message_start.usage: output_tokens 起始为 1,input_tokens 为透传估值(保底 1)
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
	// input_tokens 应为透传估值 50(≥1 保底生效),替代旧 0 占位 —— 这是 spinner 进行中能显示 ↑ 的根因修复。
	if startParsed.Message.Usage.InputTokens != 50 {
		t.Errorf("message_start.usage.input_tokens 期望 50(入站估算透传), 实际=%d",
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

// ===== 回归:thinking → tool_use 致 "Content block not found" 修复(Claude Code + gemini 池) =====
//
// 对应改动:compat.go handleStreamResponse 在 apiFormat=="anthropic" 的 tool 分支
// 原先只关 textBlockOpen、漏关 thinkingBlockOpen。Claude Code 走 antigravity/gemini 池且
// includeThoughts 注入后,模型常态输出"思考 → 工具调用":thought 分支开了 thinking 块(blockIndex 未推进),
// tool 分支不关 thinking 直接开 tool_use 覆盖原索引,末尾收尾段再见 thinkingBlockOpen==true
// 在已偏移的 blockIndex 上补发 signature_delta/content_block_stop,命中从未 start 过的索引,
// Claude Code cr[index] 查无此块 → 抛 RangeError("Content block not found")。
// 修复:tool 分支关 textBlockOpen 之前先对称关 thinkingBlockOpen(thinking_delta → signature_delta → stop)。

// geminiFunctionCallSSE 构造一帧含 functionCall 的 Gemini 上游 SSE 响应(data 行 JSON)。
func geminiFunctionCallSSE(name string, args map[string]interface{}) string {
	part := map[string]interface{}{
		"functionCall": map[string]interface{}{"name": name, "args": args},
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

// assertNoOrphanBlockStopDelta 校验转译后的 Anthropic SSE 中,每个 content_block_delta /
// content_block_stop 的 index 都在它之前存在一个相同 index 的 content_block_start。
// 这是 Claude Code 反编译出的 "Content block not found" 触发条件的逆向断言:
//   - content_block_delta 时 cr[index] 必须已由一个 content_block_start 建过(index 已被 start);
//   - content_block_stop 时同理。
// 若出现孤儿 delta/stop(index 未被任何 start 开过),对应 Claude Code 客户端必抛 "Content block not found"。
func assertNoOrphanBlockStopDelta(t *testing.T, events []sseEvent) {
	t.Helper()
	started := make(map[int]bool)
	for i, ev := range events {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
			t.Fatalf("事件 %d JSON 解析失败: %v data=%q", i, err, ev.data)
		}
		switch ev.event {
		case "content_block_start":
			idxF, _ := m["index"].(float64)
			started[int(idxF)] = true
		case "content_block_delta", "content_block_stop":
			idxF, _ := m["index"].(float64)
			idx := int(idxF)
			if !started[idx] {
				t.Fatalf("孤儿 %s index=%d:此前无 content_block_start(index=%d)建立该块,"+
					"Claude Code 会抛 \"Content block not found\"。events=%v",
					ev.event, idx, idx, eventNames(events))
			}
		}
	}
}

// TestGeminiAnthropicStream_ThinkingThenTool_ClosesThinking 主回归:上游"先思考后工具调用",
// 锁定 tool 分支补关 thinking 块后,所有 content_block_delta/stop 的 index 均有前置 start 配对,
// 不再触发 "Content block not found"。
func TestGeminiAnthropicStream_ThinkingThenTool_ClosesThinking(t *testing.T) {
	upstream := geminiThoughtSSE("先思考一下.", true) +
		geminiFunctionCallSSE("read_file", map[string]interface{}{"path": "main.go"})
	got := runGeminiAnthropicStream(t, upstream)
	events := parseSSEEvents(got)

	// 1) 核心断言:无孤儿 delta/stop(修前此断言必失败:末尾 signature_delta/stop 落在工具块之后的索引)
	assertNoOrphanBlockStopDelta(t, events)

	// 2) 序列骨架:thinking 块(idx0) → tool_use 块(idx1),thinking 块含 signature_delta + stop
	var (
		blockTypes   []string // 按 content_block_start 顺序记录块类型
		thinkingSeen bool
		toolSeen     bool
		thinkHasSig  bool
	)
	curBlockType := ""
	for _, ev := range events {
		switch ev.event {
		case "content_block_start":
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil {
				curBlockType, _ = cb["type"].(string)
				blockTypes = append(blockTypes, curBlockType)
			}
		case "content_block_delta":
			m := dataMap(t, ev)
			delta, _ := m["delta"].(map[string]interface{})
			if delta == nil {
				continue
			}
			if delta["type"] == "signature_delta" && curBlockType == "thinking" {
				thinkHasSig = true
			}
		case "content_block_stop":
			switch curBlockType {
			case "thinking":
				thinkingSeen = true
			case "tool_use":
				toolSeen = true
			}
			curBlockType = ""
		}
	}
	if !thinkingSeen {
		t.Fatalf("缺 thinking 块的 content_block_stop,blockTypes=%v", blockTypes)
	}
	if !toolSeen {
		t.Fatalf("缺 tool_use 块的 content_block_stop,blockTypes=%v", blockTypes)
	}
	if !thinkHasSig {
		t.Fatalf("thinking 块关块前必须补 signature_delta(空串占位),blockTypes=%v", blockTypes)
	}
	// 块类型须为 thinking → tool_use 顺序
	if len(blockTypes) < 2 || blockTypes[0] != "thinking" || blockTypes[1] != "tool_use" {
		t.Fatalf("块顺序期望 [thinking, tool_use],实际 %v", blockTypes)
	}

	// 3) stop_reason 为 tool_use(带工具调用)
	deltaJSON := extractMessageDeltaJSON(t, got)
	var deltaParsed struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	if err := json.Unmarshal([]byte(deltaJSON), &deltaParsed); err != nil {
		t.Fatalf("message_delta JSON 解析失败: %v raw=%s", err, deltaJSON)
	}
	if deltaParsed.Delta.StopReason != "tool_use" {
		t.Errorf("带 functionCall 的 stop_reason 期望 tool_use,实际 %q", deltaParsed.Delta.StopReason)
	}
	// 4) 不应出现收尾误发到未 start 索引的孤儿(已被 assertNoOrphanBlockStopDelta 覆盖),
	//    且 message_stop 形态正确
	if !strings.Contains(got, "event: message_stop\ndata: {\"type\":\"message_stop\"}") {
		t.Errorf("message_stop 形态应正确,sse=\n%s", got)
	}
}

// TestGeminiAnthropicStream_ThinkingTextThenTool 补强:思考 → 正文 → 工具 三段,
// 锁定三块索引各自配对完整无孤儿,顺序 [thinking, text, tool_use],stop_reason=tool_use。
func TestGeminiAnthropicStream_ThinkingTextThenTool(t *testing.T) {
	upstream := geminiThoughtSSE("推理中.", true) +
		geminiThoughtSSE("中间正文.", false) +
		geminiFunctionCallSSE("run_cmd", map[string]interface{}{"cmd": "ls"})
	got := runGeminiAnthropicStream(t, upstream)
	events := parseSSEEvents(got)

	assertNoOrphanBlockStopDelta(t, events)

	var blockTypes []string
	for _, ev := range events {
		if ev.event != "content_block_start" {
			continue
		}
		m := dataMap(t, ev)
		cb, _ := m["content_block"].(map[string]interface{})
		if cb != nil {
			if t, ok := cb["type"].(string); ok {
				blockTypes = append(blockTypes, t)
			}
		}
	}
	if len(blockTypes) != 3 ||
		blockTypes[0] != "thinking" ||
		blockTypes[1] != "text" ||
		blockTypes[2] != "tool_use" {
		t.Fatalf("块顺序期望 [thinking, text, tool_use],实际 %v", blockTypes)
	}

	deltaJSON := extractMessageDeltaJSON(t, got)
	var deltaParsed struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	if err := json.Unmarshal([]byte(deltaJSON), &deltaParsed); err != nil {
		t.Fatalf("message_delta JSON 解析失败: %v raw=%s", err, deltaJSON)
	}
	if deltaParsed.Delta.StopReason != "tool_use" {
		t.Errorf("带 functionCall 的 stop_reason 期望 tool_use,实际 %q", deltaParsed.Delta.StopReason)
	}
}

// ===== A: includeThoughts 注入回归(问题 A 根因修复) =====
//
// 对应改动:compat_translate.go GeminiThinkingConfig 加 IncludeThoughts *bool 字段,
// TranslateAnthropicToGemini/TranslateOpenAIToGemini 在思考模式开启且模型支持时注入 includeThoughts:true。
// 这是 Claude Code 走 antigravity 号池能看到思考过程的根因字段。

// TestTranslateAnthropicToGemini_InjectsIncludeThoughts 锁定:Claude Code 显式开思考
// (thinking.type=enabled + budget)时,翻译出的 Gemini 请求带 generationConfig.thinkingConfig.includeThoughts=true。
func TestTranslateAnthropicToGemini_InjectsIncludeThoughts(t *testing.T) {
	// 测试环境默认 globalEnableThinkingMode=true,确保不被外部环境干扰
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	budget := 4096
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5", // 含 sonnet 不会被 MapClientModelToGemini 当 gemini 保留,但翻译前不走映射
		MaxTokens: new(int),
		Thinking: &AnthropicThinking{
			Type:         "enabled",
			BudgetTokens: budget,
		},
	}
	*anthReq.MaxTokens = 1024

	gemReq := TranslateAnthropicToGemini(anthReq)
	if gemReq == nil || gemReq.GenerationConfig == nil || gemReq.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("应注入 ThinkingConfig,got %+v", gemReq.GenerationConfig)
	}
	tc := gemReq.GenerationConfig.ThinkingConfig
	if tc.ThinkingBudget != budget {
		t.Errorf("ThinkingBudget 期望 %d,实际 %d", budget, tc.ThinkingBudget)
	}
	if tc.IncludeThoughts == nil || *tc.IncludeThoughts != true {
		t.Errorf("IncludeThoughts 必须为 *true,实际 %v(根因修复:缺此字段上游不返 thought)", tc.IncludeThoughts)
	}
}

// TestTranslateAnthropicToGemini_DisabledNoThoughts 锁定:thinking.type=disabled 时绝不注入 includeThoughts,
// 即便全局思考模式开启(尊重客户端显式关闭)。
func TestTranslateAnthropicToGemini_DisabledNoThoughts(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	budget := 4096
	maxTok := 1024
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: &maxTok,
		Thinking: &AnthropicThinking{
			Type:         "disabled",
			BudgetTokens: budget,
		},
	}
	gemReq := TranslateAnthropicToGemini(anthReq)
	if gemReq.GenerationConfig == nil || gemReq.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("disabled 带 budget 仍应有 ThinkingConfig(budget 透传)")
	}
	if gemReq.GenerationConfig.ThinkingConfig.IncludeThoughts != nil {
		t.Errorf("disabled 时不得注入 IncludeThoughts,实际 %v", gemReq.GenerationConfig.ThinkingConfig.IncludeThoughts)
	}
}

// TestTranslateAnthropicToGemini_NonThinkingModelSkipsIncludeThoughts 锁定:非推理型模型
// 不注入 includeThoughts(避免上游 400)。geminiModelSupportsThinking 关键字:flash/pro/thinking/reasoning。
func TestTranslateAnthropicToGemini_NonThinkingModelSkipsIncludeThoughts(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	maxTok := 1024
	// "claude-3-haiku" 会被 MapClientModelToGemini 映射为 gemini-1.5-flash,但翻译函数本身用入站 model 名判定。
	// 此处直接用一个不含 flash/pro/thinking/reasoning 关键字的模型名,验证 geminiModelSupportsThinking 返回 false。
	anthReq := &AnthropicRequest{
		Model:    "gpt-3.5-turbo",
		MaxTokens: &maxTok,
	}
	gemReq := TranslateAnthropicToGemini(anthReq)
	// 非推理模型且无 thinking 字段 → 不应注入 ThinkingConfig(无 budget 无 includeThoughts)。
	if gemReq.GenerationConfig != nil && gemReq.GenerationConfig.ThinkingConfig != nil {
		if gemReq.GenerationConfig.ThinkingConfig.IncludeThoughts != nil {
			t.Errorf("非推理模型不得注入 IncludeThoughts,实际 %v", gemReq.GenerationConfig.ThinkingConfig.IncludeThoughts)
		}
	}
}

// TestTranslateOpenAIToGemini_InjectsIncludeThoughts 锁定:OpenAI Chat 入站对推理模型
// 注入 thinkingConfig 含 ThinkingBudget 与 includeThoughts:true。
func TestTranslateOpenAIToGemini_InjectsIncludeThoughts(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	maxTok := 2048
	openReq := &OpenAIRequest{
		Model:    "gemini-2.5-flash",
		MaxTokens: &maxTok,
		Messages: []OpenAIMessage{{Role: "user", Content: "hi"}},
	}
	gemReq := TranslateOpenAIToGemini(openReq)
	if gemReq.GenerationConfig == nil || gemReq.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("flash 模型应注入 ThinkingConfig")
	}
	tc := gemReq.GenerationConfig.ThinkingConfig
	if tc.ThinkingBudget != 8192 {
		t.Errorf("flash 默认 ThinkingBudget 期望 8192,实际 %d", tc.ThinkingBudget)
	}
	if tc.IncludeThoughts == nil || *tc.IncludeThoughts != true {
		t.Errorf("OpenAI 入站推理模型应注入 IncludeThoughts=true,实际 %v", tc.IncludeThoughts)
	}
}

// ===== B: responses 流式 reasoning 独立 item 收尾(问题 B 根因修复) =====
//
// 对应改动:compat.go handleStreamResponse responses 路径 reasoning 拆为独立 output_item,
// 闭包 closeResponsesReasoning 发 reasoning_text.done + content_part.done + output_item.done。
// 旧实现 thought 与正文共用 responsesMsgOpened/responsesMsgID 且硬编码 output_index 0,无 reasoning done。

// runGeminiResponsesStream 用给定的 Gemini 上游 SSE 喂入 handleStreamResponse(responses),
// 返回转译后的 SSE 文本。
func runGeminiResponsesStream(t *testing.T, upstream string) string {
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
		"responses",
		0,
		time.Unix(1700000000, 0),
		"/v1internal:streamGenerateContent",
		"req-test-resp",
	)
	return fc.Body.String()
}

// TestResponsesStream_ReasoningThenText_DoneEmitted 锁定:Codex(Antigravity 池)流式思考后跟正文,
// 下游出现独立 reasoning item 的完整事件序列:reasoning_text.delta → reasoning_text.done →
// content_part.done → output_item.done,正文 message item 占不同 output_index,且正文 done 也齐。
func TestResponsesStream_ReasoningThenText_DoneEmitted(t *testing.T) {
	upstream := geminiThoughtSSE("思考第一步.", true) +
		geminiThoughtSSE("思考结论.", true) +
		geminiThoughtSSE("最终答案.", false)
	got := runGeminiResponsesStream(t, upstream)
	events := parseSSEEvents(got)

	requireEvent(t, events, "response.created")
	requireEvent(t, events, "response.in_progress")
	requireEvent(t, events, "response.output_item.added")
	requireEvent(t, events, "response.content_part.added")
	requireEvent(t, events, "response.reasoning_text.delta")
	requireEvent(t, events, "response.reasoning_text.done")
	requireEvent(t, events, "response.output_item.done")
	requireEvent(t, events, "response.output_text.delta")
	requireEvent(t, events, "response.output_text.done")
	requireEvent(t, events, "response.completed")

	// reasoning_text.done 累积文本应包含两段思考
	var reasonDoneText string
	for _, ev := range events {
		if ev.event != "response.reasoning_text.done" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ev.data), &m); err == nil {
			if s, ok := m["text"].(string); ok {
				reasonDoneText = s
			}
		}
	}
	if !strings.Contains(reasonDoneText, "第一步") || !strings.Contains(reasonDoneText, "结论") {
		t.Fatalf("reasoning_text.done 文本累积不完整,实际=%q", reasonDoneText)
	}

	// reasoning item 的 output_index 与正文 message item 的 output_index 必须不同(独立 item)
	reasonOutIdx, textOutIdx := -1, -1
	for _, ev := range events {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
			continue
		}
		switch ev.event {
		case "response.reasoning_text.delta":
			if v, ok := m["output_index"].(float64); ok && reasonOutIdx == -1 {
				reasonOutIdx = int(v)
			}
		case "response.output_text.delta":
			if v, ok := m["output_index"].(float64); ok && textOutIdx == -1 {
				textOutIdx = int(v)
			}
		}
	}
	if reasonOutIdx == -1 || textOutIdx == -1 {
		t.Fatalf("未取到 reasoning/text 的 output_index,reason=%d text=%d", reasonOutIdx, textOutIdx)
	}
	if reasonOutIdx == textOutIdx {
		t.Fatalf("reasoning 与正文 output_index 不得相同(独立 item),reason=%d text=%d", reasonOutIdx, textOutIdx)
	}

	// reasoning item 必须收尾 output_item.done(item.type=message, content[].type=reasoning_text)
	hasReasonItemDone := false
	for _, ev := range events {
		if ev.event != "response.output_item.done" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
			continue
		}
		item, _ := m["item"].(map[string]interface{})
		if item == nil {
			continue
		}
		content, _ := item["content"].([]interface{})
		for _, c := range content {
			cp, _ := c.(map[string]interface{})
			if cp != nil && cp["type"] == "reasoning_text" {
				hasReasonItemDone = true
			}
		}
	}
	if !hasReasonItemDone {
		t.Fatalf("reasoning item 必须有 output_item.done 收尾(events=%v)", eventNames(events))
	}
}

// TestResponsesStream_ReasoningOnly_ClosesAtTail 锁定:上游只发思考无正文(断流或纯思考),
// 收尾段 closeResponsesReasoning 仍补 reasoning done 三件套,不让 reasoning item 悬空。
func TestResponsesStream_ReasoningOnly_ClosesAtTail(t *testing.T) {
	upstream := geminiThoughtSSE("纯思考无正文.", true)
	got := runGeminiResponsesStream(t, upstream)
	events := parseSSEEvents(got)
	requireEvent(t, events, "response.reasoning_text.delta")
	requireEvent(t, events, "response.reasoning_text.done")
	requireEvent(t, events, "response.output_item.done")
	requireEvent(t, events, "response.completed")
	// 不应出现 output_text.delta(无正文)
	for _, ev := range events {
		if ev.event == "response.output_text.delta" {
			t.Fatalf("纯思考流不应出现 output_text.delta")
		}
	}
}

// ===== D-compat 非流式: thought 独立 thinking/reasoning 条目 =====

// TestGeminiNormalResponse_ThoughtSeparatedAnthropic 锁定:非流式 Anthropic 路径,
// thought:true 的 part 被翻译为独立 thinking 块,置于正文 text 块之前,不混入正文。
func TestGeminiNormalResponse_ThoughtSeparatedAnthropic(t *testing.T) {
	// 构造一帧包含 thought + 正文 的非流式 Gemini 响应
	part1 := map[string]interface{}{"text": "这是思考.", "thought": true}
	part2 := map[string]interface{}{"text": "这是正文."}
	resp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{part1, part2},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	upstream := string(b)

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	fc := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	h.handleNormalResponse(
		fc,
		strings.NewReader(upstream),
		&RelaySession{},
		"gemini-2.5-flash",
		"anthropic",
		time.Unix(1700000000, 0),
		"/v1internal:generateContent",
		"req-norm-anth",
	)
	var anthResp AnthropicResponse
	if err := json.Unmarshal(fc.Body.Bytes(), &anthResp); err != nil {
		t.Fatalf("解析 Anthropic 响应失败: %v body=%s", err, fc.Body.String())
	}
	// content[0] 应为 thinking 块,content[1] 为 text 块
	if len(anthResp.Content) < 2 {
		t.Fatalf("应至少 2 个 content block(thinking+text),实际 %d: %+v", len(anthResp.Content), anthResp.Content)
	}
	if anthResp.Content[0].Type != "thinking" {
		t.Errorf("content[0] 应为 thinking,实际 %q", anthResp.Content[0].Type)
	}
	if !strings.Contains(anthResp.Content[0].Thinking, "思考") {
		t.Errorf("thinking 块文本应含\"思考\",实际 %q", anthResp.Content[0].Thinking)
	}
	if anthResp.Content[1].Type != "text" {
		t.Errorf("content[1] 应为 text,实际 %q", anthResp.Content[1].Type)
	}
	if !strings.Contains(anthResp.Content[1].Text, "正文") {
		t.Errorf("正文块文本应含\"正文\",实际 %q", anthResp.Content[1].Text)
	}
}

// TestGeminiNormalResponse_ThoughtSeparatedResponses 锁定:非流式 Responses 路径,
// thought 翻译为独立 reasoning message item(置于正文 message item 之前)。
func TestGeminiNormalResponse_ThoughtSeparatedResponses(t *testing.T) {
	part1 := map[string]interface{}{"text": "reasoning here", "thought": true}
	part2 := map[string]interface{}{"text": "final answer"}
	resp := map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role":  "model",
					"parts": []interface{}{part1, part2},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	upstream := string(b)

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	fc := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	h.handleNormalResponse(
		fc,
		strings.NewReader(upstream),
		&RelaySession{},
		"gemini-2.5-flash",
		"responses",
		time.Unix(1700000000, 0),
		"/v1internal:generateContent",
		"req-norm-resp",
	)

	var compl map[string]interface{}
	if err := json.Unmarshal(fc.Body.Bytes(), &compl); err != nil {
		t.Fatalf("解析 responses 响应失败: %v body=%s", err, fc.Body.String())
	}
	respObj, _ := compl["response"].(map[string]interface{})
	output, _ := respObj["output"].([]interface{})
	if len(output) < 2 {
		t.Fatalf("应至少 2 个 output item(reasoning+text),实际 %d", len(output))
	}
	reasonItem, _ := output[0].(map[string]interface{})
	if reasonItem["type"] != "message" {
		t.Errorf("output[0] 应为 message(reasoning),实际 %v", reasonItem["type"])
	}
	reasonContent, _ := reasonItem["content"].([]interface{})
	if len(reasonContent) == 0 {
		t.Fatalf("reasoning item content 空")
	}
	rc, _ := reasonContent[0].(map[string]interface{})
	if rc["type"] != "reasoning_text" {
		t.Errorf("output[0].content[0].type 应为 reasoning_text,实际 %v", rc["type"])
	}
	if !strings.Contains(fmt.Sprintf("%v", rc["text"]), "reasoning") {
		t.Errorf("reasoning_text 文本应含 reasoning,实际 %v", rc["text"])
	}

	textItem, _ := output[1].(map[string]interface{})
	textContent, _ := textItem["content"].([]interface{})
	tc, _ := textContent[0].(map[string]interface{})
	if tc["type"] != "output_text" {
		t.Errorf("output[1].content[0].type 应为 output_text,实际 %v", tc["type"])
	}
}
