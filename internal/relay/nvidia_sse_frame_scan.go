package relay

import (
	"bytes"
	"encoding/json"
)

// nvidia_sse_frame_scan.go: Anthropic SSE 帧扫描 + content_block JSON 解析/改写辅助, 从 nvidia_translate_buffer.go 抽离。
//
// scanAnthropicSSEFrames 把 replayWriter 产出的 SSE 字节流按帧切成 []anthropicSSEFrame, 供回放层逐帧分发;
// contentBlockKind/contentBlockIndex 提取块类型与 index; rewriteContentBlockIndex 把事件 data 的 index
// 改写为新值(续传重映射); deltaTypeForContentBlockDelta/deltaTextForContentBlockDelta 供 teeSink 区分
// text_delta/thinking_delta 与判断首 delta 是否携带实质正文(保底空块不触发 flushDeferred)。逐行等价。

type anthropicSSEFrame struct {
	event string
	data  string
	raw   string // 原始 SSE 文本(event: X\ndata: Y\n\n),回放时原样写出
}

// scanAnthropicSSEFrames 把 Anthropic SSE 字节流按帧切成 []anthropicSSEFrame。
// 仅用于 replayBodyInto 回放内部 buffer(格式由 replayWriter.writeEvent 固定为
// "event: <name>\ndata: <data>\n\n"),改造极轻,不依赖外部 SSE 包。
func scanAnthropicSSEFrames(raw []byte) []anthropicSSEFrame {
	frames := make([]anthropicSSEFrame, 0, 16)
	var event, data string
	rawStart := 0
	n := len(raw)
	i := 0
	for i < n {
		// 找一行结尾(\n)
		nl := n
		for j := i; j < n; j++ {
			if raw[j] == '\n' {
				nl = j
				break
			}
		}
		line := bytes.TrimRight(raw[i:nl], "\r")
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			event = string(bytes.TrimPrefix(line, []byte("event: ")))
		case bytes.HasPrefix(line, []byte("data: ")):
			data = string(bytes.TrimPrefix(line, []byte("data: ")))
		case len(line) == 0:
			// 空行 = 帧分隔,只有同时具备 event+data 才成帧
			if event != "" && data != "" {
				frames = append(frames, anthropicSSEFrame{
					event: event,
					data:  data,
					raw:   string(raw[rawStart:nl+1]) + "\n",
				})
			}
			event, data = "", ""
			rawStart = nl + 1
		}
		i = nl + 1
	}
	return frames
}

// contentBlockKind 从 content_block_start 事件的 data 中提取 content_block.type(thinking|text|tool_use)。
// 解析失败返回空串,调用方按"非思考即正文"处理。
func contentBlockKind(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	cb, _ := m["content_block"].(map[string]interface{})
	if cb == nil {
		return ""
	}
	kind, _ := cb["type"].(string)
	return kind
}

// contentBlockIndex 从 content_block_start / content_block_delta / content_block_stop 事件的 data 中
// 提取顶层 index 字段。这三类事件都带顶层 "index"(见 contentBlockStartPayload/contentBlockXxxPayload 构造),
// 解析失败返回 -1。供 teeSink/resumeSink 追踪 live 上的开块 index 与做上游→客户端 index 映射。
func contentBlockIndex(data string) int {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return -1
	}
	idx, ok := m["index"].(float64)
	if !ok {
		return -1
	}
	return int(idx)
}

// content_block JSON 改写辅助(原文件尾部)
// rewriteContentBlockIndex 把 content_block_start / content_block_delta / content_block_stop 事件 data
// 的顶层 index 字段改写为 newIdx,其余字段原样保留。解析失败则原样返回(防御:不破坏帧)。
func rewriteContentBlockIndex(data string, newIdx int) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return data
	}
	m["index"] = newIdx
	b, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(b)
}

// deltaTypeForContentBlockDelta 从 content_block_delta 事件 data 提取 delta.type。
// 返回 "" 表示解析失败或无 delta 字段。供 resumeSink 区分 text_delta(续推)与 thinking_delta/signature_delta(跳过)。
func deltaTypeForContentBlockDelta(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	delta, _ := m["delta"].(map[string]interface{})
	if delta == nil {
		return ""
	}
	t, _ := delta["type"].(string)
	return t
}

// deltaTextForContentBlockDelta 从 content_block_delta 事件 data 提取 delta.text(text_delta 的正文)。
// 供 teeSink 判断"首个 delta 是否携带实质正文":保底空块(ensureAtLeastOneBlock 发的空 text_delta)
// 的 text 为空,不应触发 flushDeferred(否则断流轮提前 WriteHeader 200、丢失 503 干净失败能力)。
// 解析失败或非 text_delta 返回 ""。
func deltaTextForContentBlockDelta(data string) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(data), &m) != nil {
		return ""
	}
	delta, _ := m["delta"].(map[string]interface{})
	if delta == nil {
		return ""
	}
	if t, _ := delta["type"].(string); t != "text_delta" {
		return ""
	}
	s, _ := delta["text"].(string)
	return s
}
