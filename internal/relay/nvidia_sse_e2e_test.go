package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// nvidia_sse_e2e_test.go: 端到端锁定 NIM Claude Code SSE 转换链路对一次真实上游响应的字节产出。
// 场景取自 20260828_191140 日志(用户报障现场):上游 Kimi-K3 产出 reasoning_content ×20
// + 1 帧完整 tool_calls + finish=tool_calls + usage + [DONE],代理应完整转成 Anthropic SSE。

// captureSink 捕获所有写出的事件,便于观察 emitToolCallDelta 等带真实 sink 调用时的完整事件序。
type captureSink struct{ events []string }

func (c *captureSink) writeEvent(event, data string) { c.events = append(c.events, event+"|"+data) }
func (c *captureSink) writeRaw(s string)             { c.events = append(c.events, "RAW|"+s) }
func (c *captureSink) flush()                        {}
func (c *captureSink) pingFrame()                    {}

// TestDebugDirectSinkToolUse 用 captureSink 直接捕获 openAIChatSSEToAnthropicSSEIntoPinned 的产出,
// 锁定 thinking 段 + tool_use 段都完整产出到 sink 层,无中间 silently-drop。
func TestDebugDirectSinkToolUse(t *testing.T) {
	sseBody := realUpstreamKimiToolCallSSE
	sink := &captureSink{}
	_, _, _, finishEmitted, streamTerminated, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(),
		strings.NewReader(sseBody),
		nil,
		sink, "msg_dbg", "kimi-k3", 31522, nil,
	)
	if err != nil || !finishEmitted || !streamTerminated {
		t.Fatalf("转译未正常完成: err=%v finishEmitted=%v streamTerminated=%v", err, finishEmitted, streamTerminated)
	}
	found := false
	for _, e := range sink.events {
		if strings.Contains(e, `"type":"tool_use"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("sink 里未捕获到 tool_use 块, emitToolCallDelta 未产出 content_block_start(tool_use)")
	}
}

// TestE2ERealUpstreamProducesValidAnthropicSSE 走完整两层(tee 双写 + replayFollowingInto 回放)链路,
// 模拟生产环境 writeNvidiaAnthropicStream 的一次成功流式响应,
// 校验发给客户端的最终字节流满足 Anthropic SDK 的关键不变式:
//   1) message_start 恰好 1 次, message_stop 恰好 1 次
//   2) thinking 块 index=0, tool_use 块 index>0 且迟于 thinking
//   3) tool_use id 被重写为 toolu_nv_* 格式(上游 "Bash:0" 类短 id 不透传,避免客户端判重丢调用)
//   4) message_delta 携带 stop_reason="tool_use" + 真实累计 usage
//   5) 产生至少 1 个 content_block_start(tool_use) 与配对 stop,否则客户端判工具调用被中断
func TestE2ERealUpstreamProducesValidAnthropicSSE(t *testing.T) {
	sseBody := realUpstreamKimiToolCallSSE
	var liveBuf bytes.Buffer
	liveBW := bufio.NewWriter(&liveBuf)
	liveFW := newFlushWriter("test", liveBW, nil)
	liveFW.deferredActive = true

	replay := newReplayWriter()
	tee := newTeeSink(replay, liveFW)

	in, out, cached, finishEmitted, streamTerminated, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(),
		strings.NewReader(sseBody),
		nil,
		tee,
		"msg_test_191140",
		"kimi-k3",
		31522,
		nil,
	)
	if err != nil || !finishEmitted || !streamTerminated {
		t.Fatalf("转译失败: err=%v finishEmitted=%v streamTerminated=%v", err, finishEmitted, streamTerminated)
	}
	_ = cached

	state := &liveStreamState{
		liveIdxMap:   tee.liveIdxMap,
		liveMaxIdx:   tee.liveMaxUsedIdx,
		thinkingLive: tee.liveThinkingPushed,
	}
	liveFW.flushDeferred()
	replay.replayFollowingInto(liveFW, state)
	liveBW.Flush()

	final := liveBuf.String()

	// 逐帧解析
	type frame struct{ event, data string }
	var frames []frame
	sc := bufio.NewScanner(strings.NewReader(final))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var ev, da string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			ev = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			da = strings.TrimPrefix(line, "data: ")
		} else if line == "" {
			if ev != "" && da != "" {
				frames = append(frames, frame{ev, da})
				ev, da = "", ""
			}
		}
	}
	t.Logf("产出 SSE 帧数=%d(上游 input=%d output=%d)", len(frames), in, out)
	for i, f := range frames {
		short := f.data
		if len(short) > 120 {
			short = short[:120] + "..."
		}
		t.Logf("  [%d] %s | %s", i, f.event, short)
	}

	countStart, countStop, countToolStart, countToolStop := 0, 0, 0, 0
	thinkingIdx, toolIdx := -1, -1
	var toolID string
	for _, f := range frames {
		switch f.event {
		case "message_start":
			countStart++
		case "message_stop":
			countStop++
		case "content_block_start":
			var m map[string]interface{}
			json.Unmarshal([]byte(f.data), &m)
			cb := m["content_block"].(map[string]interface{})
			idx := int(m["index"].(float64))
			switch cb["type"] {
			case "thinking":
				if thinkingIdx < 0 {
					thinkingIdx = idx
				}
			case "tool_use":
				if toolIdx < 0 {
					toolIdx = idx
					toolID, _ = cb["id"].(string)
				}
				countToolStart++
			}
		case "content_block_stop":
			// 粗判:某一个 stop 形参 index 与 tool 一致即可(多轮精确配对由其他测试覆盖)
			var m map[string]interface{}
			json.Unmarshal([]byte(f.data), &m)
			if int(m["index"].(float64)) == toolIdx {
				countToolStop++
			}
		case "message_delta":
			var m map[string]interface{}
			json.Unmarshal([]byte(f.data), &m)
			delta := m["delta"].(map[string]interface{})
			if delta["stop_reason"] != "tool_use" {
				t.Errorf("message_delta.stop_reason 应为 tool_use, got %v", delta["stop_reason"])
			}
		}
	}
	if countStart != 1 || countStop != 1 {
		t.Errorf("message_start/stop 应各 1 次, got start=%d stop=%d", countStart, countStop)
	}
	if thinkingIdx != 0 {
		t.Errorf("thinking 块 index 应为 0, got %d", thinkingIdx)
	}
	if toolIdx <= thinkingIdx {
		t.Errorf("tool_use 块 index(%d) 应晚于 thinking(%d)", toolIdx, thinkingIdx)
	}
	if countToolStart != 1 || countToolStop != 1 {
		t.Errorf("tool_use 块应 start/stop 各 1 次, got start=%d stop=%d", countToolStart, countToolStop)
	}
	if !strings.HasPrefix(toolID, "toolu_nv_") {
		t.Errorf("tool_use id 应为 toolu_nv_* 重写格式(上游 id 不透传避免冲突), got %q", toolID)
	}
}
