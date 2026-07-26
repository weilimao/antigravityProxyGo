package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// nvidia_stream_test.go 锁定 NVIDIA OpenAI Chat SSE → Anthropic Messages SSE 回译的两个关键修复：
//   ① content_block_start 的文本块 content_block 必须含 "text":"" 字段（对齐官方流式协议），
//      否则新版 Claude Code / Cursor 插件报 "Received content_block_delta without a current message"。
//   ② closeAll 关闭内容块时必须按 index 升序发 content_block_stop，与 content_block_start 出现次序一致。
//
// 本文件复用 nvidia_responses_test.go 中已有的 parseSSEEvents / eventNames / requireEvent / sseEvent / mustJSONString 等辅助，
// 避免在测试包内重复声明同名符号（Go 同包不允许重复声明）。

// dataMap 把某条 SSE 事件的 data 字符串解析成 map，便于按字段断言。
func dataMap(t *testing.T, ev sseEvent) map[string]interface{} {
	t.Helper()
	if ev.data == "" || ev.data == "{}" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
		t.Fatalf("解析 SSE data 失败 event=%s data=%s err=%v", ev.event, ev.data, err)
	}
	return m
}

// runAnthropicSSE 用给定上游 OpenAI Chat SSE 字节喂入 OpenAIChatSSEToAnthropicSSE，返回回译后的 SSE 文本。
func runAnthropicSSE(t *testing.T, upstream string) []sseEvent {
	t.Helper()
	var out bytes.Buffer
	bw := bufio.NewWriter(&out)
	OpenAIChatSSEToAnthropicSSE(context.Background(), strings.NewReader(upstream), nil, bw, "z-ai/glm-5.2")
	bw.Flush()
	return parseSSEEvents(out.String())
}

// textChunkLine 构造一个纯文本增量的上游 stream chunk SSE 行。
func textChunkLine(delta string) string {
	return mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"content": delta},
			},
		},
	})
}

// toolChunkLine 构造一个工具调用增量的上游 stream chunk SSE 行。
// idx 是上游 tool_calls 的 index，arguments 为入参增量片段，name 首帧带工具名。
func toolChunkLine(idx int, id, name, args string) string {
	fn := map[string]interface{}{}
	if name != "" {
		fn["name"] = name
	}
	if args != "" {
		fn["arguments"] = args
	}
	return mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index":    idx,
							"id":       id,
							"type":     "function",
							"function": fn,
						},
					},
				},
			},
		},
	})
}

// finishChunkLine 构造一个带 finish_reason 的末帧 stream chunk SSE 行。
func finishChunkLine(reason string) string {
	return mustJSONString(map[string]interface{}{
		"id":      "chatcmpl-x",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "z-ai/glm-5.2",
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": reason,
			},
		},
	})
}

// writeUpstream 把多行 SSE data 拼成上游字节流（每条 data 后加一个空行作为事件分隔）。
func writeUpstream(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		writeSSEData(&b, l)
	}
	return b.String()
}

// TestTextBlockStartHasEmptyTextField 锁定①：文本块的 content_block_start 必须含 "text":""。
// 缺该字段会被新版客户端 MessageAccumulator 判定无法建立 current text block，
// 紧接着的 content_block_delta(text_delta) 即报 "without a current message"。
func TestTextBlockStartHasEmptyTextField(t *testing.T) {
	events := runAnthropicSSE(t, writeUpstream(
		textChunkLine("Hello"),
		finishChunkLine("stop"),
	))

	// 找到首个 content_block_start
	var start sseEvent
	found := false
	for _, ev := range events {
		if ev.event == "content_block_start" {
			start = ev
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("未找到任何 content_block_start 事件，产出事件: %v", eventNames(events))
	}

	m := dataMap(t, start)
	cb, ok := m["content_block"].(map[string]interface{})
	if !ok {
		t.Fatalf("content_block_start 缺少 content_block 字段: %s", start.data)
	}
	if cb["type"] != "text" {
		t.Fatalf("content_block.type 期望 text，实得 %v", cb["type"])
	}
	// 核心①：text 字段必须存在且为空字符串，对齐官方协议
	textVal, hasText := cb["text"]
	if !hasText {
		t.Fatalf("content_block_start 文本块缺少 text 字段（根因①未修复），content_block=%v", cb)
	}
	if textVal != "" {
		t.Fatalf("content_block_start 文本块 text 期望空串，实得 %v", textVal)
	}

	// 事件序列应严格符合官方协议
	want := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	got := filterEventNames(events, want)
	if len(got) != len(want) {
		t.Fatalf("事件序列异常：期望 %v，实得 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("事件序列第 %d 位期望 %s，实得 %s", i, want[i], got[i])
		}
	}
}

// TestContentBlockStopOrderMatchesStart 锁定②：多块场景下 content_block_stop 的 index 必须升序，
// 且与 content_block_start 的 index 出现次序一致（先文本后工具、工具按上游 index 顺序）。
func TestContentBlockStopOrderMatchesStart(t *testing.T) {
	events := runAnthropicSSE(t, writeUpstream(
		// 文本增量先于工具调用
		textChunkLine("let me check"),
		// 工具调用 0：首帧带 name
		toolChunkLine(0, "call_0", "get_weather", "{\"location\""),
		// 工具调用 0：参数增量
		toolChunkLine(0, "", "", " Lecce\"}"),
		// 工具调用 1：首帧带 name，index=1
		toolChunkLine(1, "call_1", "get_time", "{\"tz\""),
		// 末帧
		finishChunkLine("tool_calls"),
	))

	// 收集所有 content_block_start 的 index（按出现次序）
	var startIdx []int
	for _, ev := range events {
		if ev.event == "content_block_start" {
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				startIdx = append(startIdx, int(v))
			}
		}
	}
	// 收集所有 content_block_stop 的 index（按出现次序）
	var stopIdx []int
	for _, ev := range events {
		if ev.event == "content_block_stop" {
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				stopIdx = append(stopIdx, int(v))
			}
		}
	}

	// 至少要有 3 个块：文本(index=0) + 工具0(index=1) + 工具1(index=2)
	if len(startIdx) != len(stopIdx) {
		t.Fatalf("content_block_start 数量(%d) 与 stop 数量(%d) 不一致", len(startIdx), len(stopIdx))
	}
	if len(startIdx) < 3 {
		t.Fatalf("期望至少 3 个内容块，实得 start=%v stop=%v", startIdx, stopIdx)
	}

	// start 的 index 序列应为递增（先 0 文本，再 1、2 工具）
	want := []int{0, 1, 2}
	if !equalIntSlice(startIdx, want) {
		t.Fatalf("content_block_start 的 index 次序期望 %v，实得 %v", want, startIdx)
	}
	// 核心②：stop 的 index 次序必须与 start 一致（升序），不能因 map 无序遍历而乱跳
	if !equalIntSlice(stopIdx, want) {
		t.Fatalf("content_block_stop 的 index 次序期望升序 %v（与 start 一致），实得 %v——closeAll 无序遍历未修复", want, stopIdx)
	}

	// stop 之前每个 index 都应已有对应 start（无"未 start 即 stop"）
	seen := map[int]bool{}
	for _, i := range startIdx {
		seen[i] = true
	}
	for _, i := range stopIdx {
		if !seen[i] {
			t.Fatalf("content_block_stop(index=%d) 没有对应的 content_block_start——客户端会判定 without a current message", i)
		}
	}
}

// TestToolOnlyNoPrefixTextStartsAtIndexZero 锁定"零前置文本、上游首帧直接调工具"形态：
// 必须输出 content_block_start index=0（不跳号），与官方协议"index 对应 content 数组位置"对齐。
// 在修复前，本会输出 index=1 的 tool_use 而跳过 index=0，违反协议连续性。
func TestToolOnlyNoPrefixTextStartsAtIndexZero(t *testing.T) {
	events := runAnthropicSSE(t, writeUpstream(
		// 首帧直接是工具调用 0，无任何文本增量
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Beijing\"}"),
		finishChunkLine("tool_calls"),
	))

	var startIdx []int
	var stopIdx []int
	for _, ev := range events {
		switch ev.event {
		case "content_block_start", "content_block_stop":
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				if ev.event == "content_block_start" {
					startIdx = append(startIdx, int(v))
				} else {
					stopIdx = append(stopIdx, int(v))
				}
			}
		}
	}
	// 唯一的 content block 是 tool_use，应占 index 0
	if !equalIntSlice(startIdx, []int{0}) {
		t.Fatalf("纯工具流 content_block_start 的 index 期望 [0]（不跳号），实得 %v", startIdx)
	}
	if !equalIntSlice(stopIdx, []int{0}) {
		t.Fatalf("纯工具流 content_block_stop 的 index 期望 [0]，实得 %v", stopIdx)
	}
	// 不得出现 index=1（跳号是协议违规）
	for _, i := range append(append([]int{}, startIdx...), stopIdx...) {
		if i >= 1 {
			t.Fatalf("纯工具流不应出现 index>=1 的块（应为 0），实得 start=%v stop=%v", startIdx, stopIdx)
		}
	}
	// 工具块结构完整
	for _, ev := range events {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb["type"] != "tool_use" {
				t.Fatalf("纯工具流首个块期望 tool_use，实得 %v", cb["type"])
			}
			if cb["name"] != "get_weather" {
				t.Fatalf("tool_use.name 期望 get_weather，实得 %v", cb["name"])
			}
		}
	}
}

// TestEmptyReplyKeepsCompleteSequence 锁定空回复兜底：上游首帧即 finish_reason、无任何增量时，
// 事件序列仍应完整有序，不应出现孤立的 content_block_delta 而无 content_block_start。
func TestEmptyReplyKeepsCompleteSequence(t *testing.T) {
	events := runAnthropicSSE(t, writeUpstream(finishChunkLine("stop")))

	// 不得出现 content_block_delta 而没有对应的 content_block_start
	startedIdx := map[int]bool{}
	for _, ev := range events {
		if ev.event == "content_block_start" {
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				startedIdx[int(v)] = true
			}
		}
		if ev.event == "content_block_delta" {
			idx := 0
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				idx = int(v)
			}
			if !startedIdx[idx] {
				t.Fatalf("出现 content_block_delta(index=%d) 但无对应 content_block_start——without a current message，事件: %v", idx, eventNames(events))
			}
		}
	}

	// 必含 message_start 与 message_stop，保证流语义闭合
	requireEvent(t, events, "message_start")
	requireEvent(t, events, "message_stop")
}

// filterEventNames 仅返回在 want 集合中的事件名，按出现次序，用于校验关键序列。
func filterEventNames(events []sseEvent, want []string) []string {
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	var names []string
	for _, ev := range events {
		if wantSet[ev.event] {
			names = append(names, ev.event)
		}
	}
	return names
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
