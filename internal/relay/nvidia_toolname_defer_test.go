package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// nvidia_toolname_defer_test.go 锁定 OpenAI→Anthropic 流式回译的 tool_use 名字延迟开块策略
// (20260901 修复:bitdeer/DeepSeek-V4-Flash 等部分 OpenAI 兼容上游把工具名放在后续分片下发,
// 首帧 function.name 为空;旧实现首帧立即开块且从不补齐名字,客户端收到 "name":"" 的
// tool_use 块被判定 invalid tool call —— ZCode 报 "tool name is empty",
// reason=invalid_request, retryable=false, 整轮作废)。
//
// 核心不变式:
//   1) 名字后到:content_block_start 必须延迟到名字已知的那一帧才发出,name 非空;
//      开块前收到的 arguments 分片按原序 flush,不丢不乱
//   2) 名字永不到达:该工具块整体丢弃,不发 start/delta/stop,流仍完整闭合
//      (对齐空 thinking 块的丢弃守卫),客户端不再收到无名工具块

// TestE2EToolNameInLaterChunk 名字在后续分片才到达:先收到的参数分片须先缓存,
// 名字到达帧补发 start 并按原序 flush,后续参数分片直传,拼接结果完整。
func TestE2EToolNameInLaterChunk(t *testing.T) {
	upstream := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"\",\"arguments\":\"{\\\"city\\\":\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"get_weather\",\"arguments\":\"\\\"SF\\\"}\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: [DONE]\n\n"

	sink := &captureSink{}
	_, _, _, finishEmitted, streamTerminated, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream), nil, sink, "msg_tn", "k", 100, nil)
	if err != nil || !finishEmitted || !streamTerminated {
		t.Fatalf("转译未正常完成: err=%v finish=%v term=%v", err, finishEmitted, streamTerminated)
	}

	// 不变式1a:恰好一个 tool_use 开块,名字非空,全程无 "name":""
	startIdx, deltaIdx := -1, -1
	starts := 0
	var partial strings.Builder
	for i, e := range sink.events {
		ev, data, _ := strings.Cut(e, "|")
		switch {
		case ev == "content_block_start" && strings.Contains(data, `"tool_use"`):
			starts++
			startIdx = i
			if !strings.Contains(data, `"name":"get_weather"`) {
				t.Fatalf("tool_use 开块名字应为 get_weather, got %s", data)
			}
		case ev == "content_block_delta" && strings.Contains(data, "input_json_delta"):
			if deltaIdx < 0 {
				deltaIdx = i
			}
			var payload struct {
				Delta struct {
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				t.Fatalf("input_json_delta 解析失败: %v, data=%s", err, data)
			}
			partial.WriteString(payload.Delta.PartialJSON)
		}
		if strings.Contains(data, `"name":""`) {
			t.Fatalf("流中出现空名工具块: %s", data)
		}
	}
	if starts != 1 {
		t.Fatalf("tool_use 开块应恰好 1 次, got %d, events=%v", starts, sink.events)
	}
	if startIdx < 0 || deltaIdx < 0 || startIdx > deltaIdx {
		t.Fatalf("content_block_start 必须先于 input_json_delta: start=%d delta=%d", startIdx, deltaIdx)
	}
	if got := partial.String(); got != `{"city":"SF"}` {
		t.Fatalf("参数分片拼接不完整: got %q, want %q", got, `{"city":"SF"}`)
	}
	// 不变式1b:stop_reason 保持 tool_use,客户端照常驱动工具执行
	last := sink.events[len(sink.events)-2]
	if !strings.Contains(last, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop_reason 应为 tool_use, got %s", last)
	}
}

// TestE2EToolNameNeverArrives 名字直到流结束都没到达:该工具块整体丢弃,
// 不发 tool_use start/delta/stop;流仍完整闭合(空文本块兜底 + message_stop)。
func TestE2EToolNameNeverArrives(t *testing.T) {
	upstream := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"\",\"arguments\":\"{\\\"a\\\":1}\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: [DONE]\n\n"

	sink := &captureSink{}
	_, _, _, _, _, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream), nil, sink, "msg_tn2", "k", 100, nil)
	if err != nil {
		t.Fatalf("转译异常: %v", err)
	}
	hasToolStart := false
	hasInputDelta := false
	hasTextFallback := false
	hasMessageStop := false
	stopReason := ""
	for _, e := range sink.events {
		ev, data, _ := strings.Cut(e, "|")
		switch {
		case ev == "content_block_start" && strings.Contains(data, `"tool_use"`):
			hasToolStart = true
		case ev == "content_block_delta" && strings.Contains(data, "input_json_delta"):
			hasInputDelta = true
		case ev == "content_block_start" && strings.Contains(data, `"text"`):
			hasTextFallback = true
		case ev == "message_stop":
			hasMessageStop = true
		case ev == "message_delta":
			var payload struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
			}
			if jsonErr := json.Unmarshal([]byte(data), &payload); jsonErr == nil {
				stopReason = payload.Delta.StopReason
			}
		}
	}
	if hasToolStart || hasInputDelta {
		t.Fatalf("无名工具块应整体丢弃, 不应出现 tool_use 开块或 input_json_delta, events=%v", sink.events)
	}
	if !hasTextFallback || !hasMessageStop {
		t.Fatalf("流应完整闭合(空文本块兜底 + message_stop), events=%v", sink.events)
	}
	// 无名块被丢弃后,消息里没有任何可执行的 tool_use 块,stop_reason 必须降级为 end_turn:
	// 硬报 tool_use 会让客户端进入"该执行工具却没有调用"的异常路径
	// (ZCode: "Tool call ended without a terminal event")。
	if stopReason != "end_turn" {
		t.Fatalf("无名块丢弃后 stop_reason 应为 end_turn, got %q, events=%v", stopReason, sink.events)
	}
}

// TestE2EToolNameDeferredAfterText 名字迟到期间上游插发 text 分片:
// 延迟开块的 tool_use 必须在开块时刻才分配 Anthropic index(排在已发出的 text 块之后),
// 保证 index 单调递增、text 块先闭合再开 tool 块,不产生非单调 index / 块重叠。
func TestE2EToolNameDeferredAfterText(t *testing.T) {
	upstream := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"\",\"arguments\":\"{\\\"command\\\":\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"我先找到这段逻辑\"},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"Bash\",\"arguments\":\"\\\"ls\\\"}\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: [DONE]\n\n"

	sink := &captureSink{}
	_, _, _, _, _, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream), nil, sink, "msg_tn3", "k", 100, nil)
	if err != nil {
		t.Fatalf("转译异常: %v", err)
	}

	textIdx, toolIdx := -1, -1
	textStopPos, toolStartPos := -1, -1
	var partial strings.Builder
	for i, e := range sink.events {
		ev, data, _ := strings.Cut(e, "|")
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal([]byte(data), &payload)
		switch {
		case ev == "content_block_start" && payload.ContentBlock.Type == "text":
			textIdx = payload.Index
		case ev == "content_block_start" && payload.ContentBlock.Type == "tool_use":
			toolIdx = payload.Index
			toolStartPos = i
			if payload.ContentBlock.Name != "Bash" {
				t.Fatalf("tool_use 名字应为 Bash, got %q", payload.ContentBlock.Name)
			}
		case ev == "content_block_stop" && payload.Index == textIdx:
			textStopPos = i
		case ev == "content_block_delta" && payload.Delta.PartialJSON != "":
			partial.WriteString(payload.Delta.PartialJSON)
		}
	}
	if textIdx < 0 || toolIdx < 0 {
		t.Fatalf("应有 text 与 tool_use 两个块, textIdx=%d toolIdx=%d, events=%v", textIdx, toolIdx, sink.events)
	}
	// 不变式:tool_use 的 index 必须大于已先发出的 text 块 index(开块时刻分配,单调递增)。
	if toolIdx <= textIdx {
		t.Fatalf("tool_use index(%d) 应大于 text index(%d) —— 延迟开块不得回填旧 index", toolIdx, textIdx)
	}
	// 不变式:text 块完全闭合(content_block_stop)先于 tool_use 开块,块严格串行不重叠。
	if !(textStopPos >= 0 && textStopPos < toolStartPos) {
		t.Fatalf("text 块 stop(pos=%d) 必须先于 tool_use start(pos=%d), events=%v", textStopPos, toolStartPos, sink.events)
	}
	if got := partial.String(); got != `{"command":"ls"}` {
		t.Fatalf("参数分片拼接不完整: got %q, want %q", got, `{"command":"ls"}`)
	}
}
