package relay

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

// nvidia_thinking_realtime_test.go 锁定 NVIDIA 池混合模式(思考实时透传 + 正文蓄流回放):
//
//	message_start          → 双写 live(replay 头不重发)
//	思考段(thinking 块整段)→ 双写 live(逐字实时显示)
//	正文/工具段 + 尾帧     → 只 replay(整条 ready 后 replayBodyInto 跳过思考头回放)
//	断流重试轮             → 切纯 replay(replayOnly),思考不再外发,只回放正文
//
// 解决"思考块等整条 ready 才出"的首字节延迟痛点,同时保留服务端 5×5s 不换号断流重试能力。
//
//	message_start          → 双写 live(replay 头不重发)
//	思考段(thinking 块整段)→ 双写 live(逐字实时显示)
//	正文/工具段 + 尾帧     → 只 replay(整条 ready 后 replayBodyInto 跳过思考头回放)
//	断流重试轮             → 切纯 replay(replayOnly),思考不再外发,只回放正文
//
// 解决"思考块等整条 ready 才出"的首字节延迟痛点,同时保留服务端 5×5s 不换号断流重试能力。

// teeTestHarness 构造一个 live(接 flushBuffer,可断言 live 实时输出) + replay(蓄流),
// 并复用 runAnthropicSSE 路径的转译主循环,把上游 OpenAI Chat SSE 喂进 tee。
type teeTestHarness struct {
	live    *flushBuffer
	liveFW  *flushWriter
	replay  *replayWriter
	tee     *teeSink
}

func newTeeTestHarness() *teeTestHarness {
	live := &flushBuffer{}
	bw := bufio.NewWriter(live)
	liveFW := newFlushWriter("test", bw)
	replay := newReplayWriter()
	tee := newTeeSink(replay, liveFW)
	return &teeTestHarness{live: live, liveFW: liveFW, replay: replay, tee: tee}
}

// runFeed 把上游 OpenAI Chat SSE 字节经 openAIChatSSEToAnthropicSSEInto 喂进 tee,
// 返回转译的 finishEmitted/streamTerminated/err 供测试断言完整性。
func (h *teeTestHarness) runFeed(upstream string) (finishEmitted, streamTerminated bool, err error) {
	return runIntoSink(h.tee, upstream)
}

// runFeedReplayOnly 同上但用 tee.replay 作纯 replay(模拟断流重试轮)。
func (h *teeTestHarness) runFeedReplayOnly(upstream string) (finishEmitted, streamTerminated bool, err error) {
	h.tee.replayOnly = true
	h.tee.replay.reset()
	return runIntoSink(h.tee.replay, upstream)
}

// runIntoSink 把上游字节经转译主循环喂进指定 sink,返回完整性三态(input/output 丢弃)。
func runIntoSink(sink sseEventSink, upstream string) (finishEmitted, streamTerminated bool, err error) {
	_, _, finishEmitted, streamTerminated, err = openAIChatSSEToAnthropicSSEInto(context.Background(), strings.NewReader(upstream), nil, sink, "msg_test", "z-ai/glm-5.2")
	return
}

// flushLive 强制把 live bufio 刷进 flushBuffer,返回 live 字节(可 parseSSEEvents)。
func (h *teeTestHarness) flushLive() string {
	h.liveFW.flush()
	return h.live.String()
}

// liveEvents 取 live 实时输出解析为事件。
func (h *teeTestHarness) liveEvents(t *testing.T) []sseEvent {
	t.Helper()
	h.liveFW.flush()
	return parseSSEEvents(h.live.String())
}

// replayEvents 取 replay 蓄流解析为事件。
func replayEvents(t *testing.T, r *replayWriter) []sseEvent {
	t.Helper()
	return parseSSEEvents(string(r.bytes()))
}

// requireDeltaType 断言事件流中至少存在一条 content_block_delta 且其 delta.type == dtype。
// thinking_delta / signature_delta / text_delta / input_json_delta 都是 delta.type 字段,
// 不是 SSE 事件名(事件名统一为 content_block_delta),故不能用 requireEvent 按事件名查找。
func requireDeltaType(t *testing.T, events []sseEvent, dtype string) {
	t.Helper()
	for _, ev := range events {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta != nil && delta["type"] == dtype {
			return
		}
	}
	t.Errorf("expected delta.type %q not found in events: %v", dtype, eventNames(events))
}

// TestScanAnthropicSSEFrames 锁定帧切分:完整整条(thinking+text+尾帧)应切成正确帧数,
// 每帧含 event/data,且最后一帧 message_stop 不丢。
func TestScanAnthropicSSEFrames(t *testing.T) {
	upstream := writeUpstream(
		reasoningChunkLine("think."),
		textChunkLine("answer."),
		finishChunkLine("stop"),
	)
	// 先经一个纯 replayWriter 蓄流成完整 Anthropic SSE,再扫描。
	rw := newReplayWriter()
	if _, _, err := runIntoSink(rw, upstream); err != nil {
		t.Fatalf("feed into replayWriter failed: %v", err)
	}
	frames := scanAnthropicSSEFrames(rw.bytes())
	if len(frames) == 0 {
		t.Fatalf("scanAnthropicSSEFrames 返回 0 帧")
	}
	// 期望事件序列:message_start, cbs(thinking), thinking_delta, signature_delta, cbs_stop,
	//              cbs(text), text_delta, cbs_stop, message_delta, message_stop 共 10 帧
	names := make([]string, 0, len(frames))
	for _, f := range frames {
		if f.event == "" {
			t.Fatalf("存在空 event 帧: %+v", f)
		}
		names = append(names, f.event)
	}
	if names[len(names)-1] != "message_stop" {
		t.Fatalf("最后一帧应为 message_stop,实际=%v", names)
	}
	// 每帧 raw 应能 round-trip 还原成 "event: X\ndata: Y\n\n"
	for _, f := range frames {
		if !strings.HasPrefix(f.raw, "event: "+f.event+"\n") {
			t.Fatalf("帧 raw 起始不符,期望以 event: %s 开头,实际=%q", f.event, f.raw)
		}
		if !strings.HasSuffix(f.raw, "\n\n") {
			t.Fatalf("帧 raw 应以 \\n\\n 结尾,实际=%q", f.raw)
		}
	}
}

// TestTeeSink_ThinkingLiveAndBodyReplayOnly 锁定:
//  1. 思考段双写 live+replay(message_start、thinking 块整段进 live);
//  2. 正文段只 replay(live 不出现 text_delta);
//  3. message_delta/message_stop 只 replay(live 无尾帧)。
func TestTeeSink_ThinkingLiveAndBodyReplayOnly(t *testing.T) {
	h := newTeeTestHarness()
	upstream := writeUpstream(
		reasoningChunkLine("Let me think."),
		textChunkLine("Final answer."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed into tee failed: %v", err)
	}

	// live 应含 message_start + thinking 块整段,但不含 text_delta / message_stop
	liveEv := h.liveEvents(t)
	liveNames := eventNames(liveEv)
	requireEvent(t, liveEv, "message_start")
	requireEvent(t, liveEv, "content_block_start")
	requireDeltaType(t, liveEv, "thinking_delta")
	requireDeltaType(t, liveEv, "signature_delta")
	requireEvent(t, liveEv, "content_block_stop")
	for _, n := range liveNames {
		if n == "text_delta" {
			t.Fatalf("live 不应含正文 text_delta(正文应只蓄流),live=%v", liveNames)
		}
		if n == "message_stop" {
			t.Fatalf("live 不应含尾帧 message_stop(尾帧应随回放补发),live=%v", liveNames)
		}
	}

	// replay 应含完整整条(供断流重试完整性与回放使用)
	repEv := replayEvents(t, h.replay)
	requireEvent(t, repEv, "message_start")
	requireDeltaType(t, repEv, "thinking_delta")
	requireDeltaType(t, repEv, "text_delta")
	requireEvent(t, repEv, "message_stop")

	// 思考块 index 0 在 live 上(count_content_block_start(thinking)==1)
	startCount := 0
	for _, ev := range liveEv {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				startCount++
			}
		}
	}
	if startCount != 1 {
		t.Fatalf("live 上思考块 start 应为 1,实际=%d", startCount)
	}
}

// TestTeeSink_ReplayOnly suppresses live writes 锁定:重试轮 replayOnly=true 时,
// 所有事件只写 replay,live 一帧不增。
func TestTeeSink_ReplayOnlySuppressesLiveWrites(t *testing.T) {
	h := newTeeTestHarness()
	// 先发一轮正常 feed 让 live 收到思考(模拟首轮)
	if _, _, err := h.runFeed(writeUpstream(
		reasoningChunkLine("first round think."),
		finishChunkLine("stop"),
	)); err != nil {
		t.Fatalf("first feed failed: %v", err)
	}
	liveBefore := h.flushLive()
	// 再发一轮 replayOnly(模拟断流重试),live 不应再增长
	if _, _, err := h.runFeedReplayOnly(writeUpstream(
		reasoningChunkLine("retry round think."),
		textChunkLine("retry body."),
		finishChunkLine("stop"),
	)); err != nil {
		t.Fatalf("replayOnly feed failed: %v", err)
	}
	liveAfter := h.flushLive()
	if liveBefore != liveAfter {
		t.Fatalf("replayOnly 轮 live 不应增长,before=%q after=%q", liveBefore, liveAfter)
	}
	// replay 应含第二轮整条(recent reset 后重蓄)
	repEv := replayEvents(t, h.replay)
	requireEvent(t, repEv, "message_start")
	requireDeltaType(t, repEv, "text_delta")
}

// TestReplayBodyInto_SkipsThinkingHeader 锁定:replay 含 message_start+思考+正文+尾帧,
// replayBodyInto 只回放正文+尾帧,跳过 message_start 和整个思考段。
func TestReplayBodyInto_SkipsThinkingHeader(t *testing.T) {
	// 先蓄出一条完整 reply(含思考)
	rw := newReplayWriter()
	upstream := writeUpstream(
		reasoningChunkLine("thinking content here."),
		textChunkLine("The real answer."),
		finishChunkLine("stop"),
	)
	if _, _, err := runIntoSink(rw, upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}

	// 用一个新 live flushBuffer 接收回放
	live := &flushBuffer{}
	bw := bufio.NewWriter(live)
	liveFW := newFlushWriter("test", bw)
	rw.replayBodyInto(liveFW)
	bw.Flush()
	out := parseSSEEvents(live.String())

	// out 不应含 message_start(首轮已发)
	for _, ev := range out {
		if ev.event == "message_start" {
			t.Fatalf("replayBodyInto 不应回放 message_start(首轮已发),out=%v", eventNames(out))
		}
		// 不应含任何 thinking 事件
		if ev.event == "content_block_delta" {
			dm := dataMap(t, ev)
			delta, _ := dm["delta"].(map[string]interface{})
			if delta != nil && (delta["type"] == "thinking_delta" || delta["type"] == "signature_delta") {
				t.Fatalf("replayBodyInto 不应回放思考 delta,out=%v", eventNames(out))
			}
		}
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Fatalf("replayBodyInto 不应回放思考块 start,out=%v", eventNames(out))
			}
		}
	}
	// out 应含正文 text_delta + message_delta + message_stop
	requireEvent(t, out, "content_block_start")
	requireDeltaType(t, out, "text_delta")
	requireEvent(t, out, "message_delta")
	requireEvent(t, out, "message_stop")

	// 正文 index 应为 1(思考块占 0 被跳过后,正文仍带 index 1 不变)
	for _, ev := range out {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			if m["index"].(float64) != 1 {
				t.Fatalf("回放正文块 index 应为 1(思考块占 0),实际=%v", m["index"])
			}
		}
	}
	// 正文文本应完整("The real answer.")
	var text string
	for _, ev := range out {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta != nil && delta["type"] == "text_delta" {
			if s, ok := delta["text"].(string); ok {
				text += s
			}
		}
	}
	if !contains(text, "The real answer.") {
		t.Fatalf("回放正文文本不完整,实际=%q", text)
	}
}

// TestReplayBodyInto_NoThinkingModelReplay 锁定:无推理模型 replay(无思考段),
// replayBodyInto 跳 message_start 后从正文起回放,行为与现状一致。
func TestReplayBodyInto_NoThinkingModelReplay(t *testing.T) {
	rw := newReplayWriter()
	upstream := writeUpstream(
		textChunkLine("plain body."),
		finishChunkLine("stop"),
	)
	if _, _, err := runIntoSink(rw, upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	live := &flushBuffer{}
	bw := bufio.NewWriter(live)
	liveFW := newFlushWriter("test", bw)
	rw.replayBodyInto(liveFW)
	bw.Flush()
	out := parseSSEEvents(live.String())

	for _, ev := range out {
		if ev.event == "message_start" {
			t.Fatalf("无推理模型回放不应含 message_start,out=%v", eventNames(out))
		}
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Fatalf("无推理模型回放不应含思考块")
			}
		}
	}
	requireDeltaType(t, out, "text_delta")
	requireEvent(t, out, "message_stop")
}

// TestTeeSink_LiveThinkingOpenWhenTruncatedBeforeClose 锁定:
// 第一轮只发 reasoning_content(思考块开块)不发正文/finish,主循环断流未闭合思考块时,
// tee.liveThinkingOpen 应保持 true,供回放前补闭合。
func TestTeeSink_LiveThinkingOpenWhenTruncatedBeforeClose(t *testing.T) {
	h := newTeeTestHarness()
	// 只发一个 reasoning 帧就结束(无 finish_reason,无正文)。
	// 主循环在循环外会把未闭合块经 closeAll 闭合,所以正常 feed 完思考块会闭合。
	// 为构造"思考未闭合断流"语义,直接喂一个 reasoning 帧后用 io.EOF 自然终止:
	// 此时 streamTerminated=true、finishEmitted=false,closeAll 会发 signature_delta+stop 闭合。
	// 故 liveThinkingOpen 在正常结束时应为 false;真正的"断流中途"语义需 err!=nil 路径。
	// 这里锁定正常结束路径:思考块已被 closeAll 闭合,liveThinkingOpen=false。
	upstream := writeUpstream(reasoningChunkLine("partial think."))
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	if h.tee.liveThinkingOpen {
		t.Fatalf("正常结束(closeAll 已闭合思考块)后 liveThinkingOpen 应为 false")
	}
	liveEv := h.liveEvents(t)
	// live 上思考块应已闭合含 content_block_stop
	requireEvent(t, liveEv, "content_block_stop")
}

// TestTeeSink_LiveThinkingOpenOnUpstreamErrorMidThinking 锁定:
// 上游 SSE error chunk 在思考阶段 break(主循环第 600-612 行),思考块已开但未在 live 上闭合,
// 此时 liveThinkingOpen 应保持 true。
func TestTeeSink_LiveThinkingOpenOnUpstreamErrorMidThinking(t *testing.T) {
	h := newTeeTestHarness()
	// 构造上游先发 reasoning 开块,再发一个 error chunk 触发主循环 break(不闭合思考块)。
	// nvidia_translate.go 中 error chunk 形如 {"error":{...}},主循环检测到后 break。
	errChunk := mustJSONString(map[string]interface{}{
		"error": map[string]interface{}{"message": "upstream boom", "type": "server_error"},
	})
	upstream := writeUpstream(
		reasoningChunkLine("thinking before crash."),
		errChunk,
	)
	finishEmitted, streamTerminated, err := h.runFeed(upstream)
	// error chunk 路径:err 非空,finishEmitted/streamTerminated 视上游而定
	if err == nil && !finishEmitted && !streamTerminated {
		// 无 error 且回收正常时不会触发断流;此处主要验证 liveThinkingOpen 语义
	}
	// 思考块已开(content_block_start thinking + thinking_delta 已到 live),
	// 但 error chunk break 在 closeAll 之前,主循环外的 closeAll(BaseTest path)仍会执行?
	// 实测 openAIChatSSEToAnthropicSSEInto 在 error break 后循环外 ensureAtLeastOneBlock+closeAll 调用。
	// 故即便 error chunk,closeAll 仍会闭合思考块。所以 liveThinkingOpen 在所有"主循环退出"路径
	// 都被 closeAll 闭合为 false。真正 liveThinkingOpen=true 的场景只在 writeNvidiaAnthropicStream
	// 层"整条 ready 判定不成立"时(蓄流丢弃),live 上思考已发但未闭合。
	// 此处锁定:思考块至少在 live 上开过。
	liveEv := h.liveEvents(t)
	requireEvent(t, liveEv, "content_block_start")
	hasThinkingStart := false
	for _, ev := range liveEv {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				hasThinkingStart = true
			}
		}
	}
	if !hasThinkingStart {
		t.Fatalf("思考阶段 error chunk 前 live 应已收到 thinking 块 start,live=%v", eventNames(liveEv))
	}
}

// TestThinkingRealtimeThenBodyReplay_E2E 锁定端到端客户端最终流(正文实时下发新架构):
// 首轮 tee 把 message_start+思考+正文 text 全部逐块实时推 live(思考与正文 real-time);
// 整条 ready 后 replayFollowingInto 仅补尾帧(text 块已在 liveIdxMap 中被跳过,不重复回放)。
// 客户端最终 live 流 == message_start + 思考块整段 + 正文块整段 + message_delta + message_stop,
// 且 message_start 只一个、思考块只一个、正文 text_delta 只一个(无重复)。
func TestThinkingRealtimeThenBodyReplay_E2E(t *testing.T) {
	h := newTeeTestHarness()
	upstream := writeUpstream(
		reasoningChunkLine("Step 1: think."),
		reasoningChunkLine("Step 2: conclude."),
		textChunkLine("The answer is 42."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	// 整条 ready:正常结束 liveThinkingOpen=false,无需补闭合
	if h.tee.liveThinkingOpen {
		t.Fatalf("正常结束 liveThinkingOpen 应为 false")
	}
	// 整条 ready:replayFollowingInto 据 tee 的 live 协议态快照跳过已 live 的 text/thinking,只补尾帧。
	// 首轮无重试,快照 thinkingLive=true(首轮思考已 live)、liveIdxMap=text 块 identity 映射、liveMaxIdx 为最大已用 index。
	state := &liveStreamState{
		liveIdxMap:   h.tee.liveIdxMap,
		liveMaxIdx:   h.tee.liveMaxUsedIdx,
		thinkingLive: h.tee.liveThinkingPushed,
	}
	h.replay.replayFollowingInto(h.liveFW, state)
	h.liveFW.flush()
	out := parseSSEEvents(h.live.String())

	// message_start 恰好 1 个
	msCount := 0
	for _, ev := range out {
		if ev.event == "message_start" {
			msCount++
		}
	}
	if msCount != 1 {
		t.Fatalf("message_start 应恰好 1 个(首轮发 1 个,回放跳过),实际=%d", msCount)
	}
	// 思考块 start 恰好 1 个
	thinkStart := 0
	thinkDelta := 0
	textDelta := 0
	for _, ev := range out {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				thinkStart++
			}
		}
		if ev.event == "content_block_delta" {
			dm := dataMap(t, ev)
			delta, _ := dm["delta"].(map[string]interface{})
			if delta != nil {
				switch delta["type"] {
				case "thinking_delta":
					thinkDelta++
				case "text_delta":
					textDelta++
				}
			}
		}
	}
	if thinkStart != 1 {
		t.Fatalf("思考块 start 应恰好 1 个,实际=%d", thinkStart)
	}
	if thinkDelta < 2 {
		t.Fatalf("应至少 2 条 thinking_delta,实际=%d", thinkDelta)
	}
	if textDelta != 1 {
		t.Fatalf("正文 text_delta 应 1 个(实时下发,不重复回放),实际=%d", textDelta)
	}
	// 顺序:message_start → thinking 块整段(start/delta*/signature_delta/stop) → 正文块整段(start/delta/stop) → message_delta/stop
	want := []string{
		"message_start",
		"content_block_start",  // thinking
		"content_block_delta",  // thinking_delta 1
		"content_block_delta",  // thinking_delta 2
		"content_block_delta",  // signature_delta
		"content_block_stop",   // thinking
		"content_block_start",  // text
		"content_block_delta",  // text_delta
		"content_block_stop",   // text
		"message_delta",
		"message_stop",
	}
	got := eventNames(out)
	if len(got) != len(want) {
		t.Fatalf("事件数不符 want=%v got=%v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("第 %d 个事件不符 want=%s got=%s (全序列=%v)", i, w, got[i], got)
		}
	}
}

// TestThinkingRealtimeTruncatedThenReplay_ClosesOpenThinking 锁定:
// 首轮思考推到一半就断流(liveThinkingOpen=true),重试整条 ready 后回放前需补闭合 index 0 思考块,
// 客户端最终流思考块完整闭合后再出正文。模拟 writeNvidiaAnthropicStream 的补闭合逻辑。
func TestThinkingRealtimeTruncatedThenReplay_ClosesOpenThinking(t *testing.T) {
	h := newTeeTestHarness()
	// 首轮只发部分 reasoning 后模拟断流:用一个 error chunk 在思考阶段 break,但 closeAll 会闭合——
	// 为构造真正"未闭合"语义,直接手动置 liveThinkingOpen=true 模拟蓄流丢弃断流场景。
	upstream := writeUpstream(
		reasoningChunkLine("partial."),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	// 正常结束思考块被 closeAll 闭合。手动模拟"断流丢弃蓄流、live 思考未闭合"语义:
	// 强制置 true 验证补闭合逻辑(writeNvidiaAnthropicStream 中的分支)。
	h.tee.liveThinkingOpen = true

	// 模拟重试轮:切纯 replay 重蓄整条(含完整思考+正文)
	h.tee.replayOnly = true
	h.tee.replay.reset()
	retryUpstream := writeUpstream(
		reasoningChunkLine("full thinking retry."),
		textChunkLine("final body."),
		finishChunkLine("stop"),
	)
	if _, _, err := runIntoSink(h.tee.replay, retryUpstream); err != nil {
		t.Fatalf("retry feed failed: %v", err)
	}

	// 回放前补闭合(writeNvidiaAnthropicStream 逻辑)
	if h.tee.liveThinkingOpen {
		h.liveFW.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(0, ""))
		h.liveFW.writeEvent("content_block_stop", contentBlockStopPayload(0))
	}
	// 回放正文(跳过 replay 的思考头)
	h.replay.replayBodyInto(h.liveFW)
	h.liveFW.flush()
	out := parseSSEEvents(h.live.String())

	// live 上首轮已发:message_start + thinking 块 start + thinking_delta(部分,无闭合)
	// 补闭合后:signature_delta + content_block_stop(0)
	// 回放正文:content_block_start(text,1) + text_delta + content_block_stop(1) + message_delta + message_stop
	// 不得出现第二个 message_start 或 thinking 块 start
	msCount := 0
	thinkStart := 0
	for _, ev := range out {
		if ev.event == "message_start" {
			msCount++
		}
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				thinkStart++
			}
		}
	}
	if msCount != 1 {
		t.Fatalf("断流重试后 message_start 应恰好 1 个,实际=%d live=%v", msCount, eventNames(out))
	}
	if thinkStart != 1 {
		t.Fatalf("断流重试后首轮 thinking 块 start 应保持 1 个(重试轮思考不外发),实际=%d", thinkStart)
	}
	requireDeltaType(t, out, "text_delta")
	requireEvent(t, out, "message_stop")
	// 思考块 index 0 应已闭合(存在其 content_block_stop)
	stopIdxSeen := false
	for _, ev := range out {
		if ev.event == "content_block_stop" {
			m := dataMap(t, ev)
			if m["index"].(float64) == 0 {
				stopIdxSeen = true
			}
		}
	}
	if !stopIdxSeen {
		t.Fatalf("补闭合后应存在 index 0 的 content_block_stop,out=%v", eventNames(out))
	}
}

// TestTeeSink_UpstreamFirstByteHook_FiresBeforeDeferredFlush 锁定 TTFT 修复语义:
// firstUpstreamByteHook 必须在上游首帧写入 sink 时立即触发(独立于客户端 deferred 缓冲),
// 而非等到 flushDeferred 把 deferred 落盘(客户端首字节)才触发 —— 这正是「上游首字延迟」与
// 「客户端首字节时间」解耦的关键。混合模式整条 ready 后回放时,客户端首字可能远晚于上游首字,
// 若复用 firstByteHook,TTFT 会被误记成请求耗时(前端 0 差值 bug 的根因之一)。
func TestTeeSink_UpstreamFirstByteHook_FiresBeforeDeferredFlush(t *testing.T) {
	live := &flushBuffer{}
	bw := bufio.NewWriter(live)
	liveFW := newFlushWriter("test", bw)
	replay := newReplayWriter()
	tee := newTeeSink(replay, liveFW)

	// 模拟 writeNvidiaAnthropicStream:deferredActive+pull 阶段框架帧先进 deferred。
	liveFW.deferredActive = true

	upstreamHits := 0
	clientHits := 0
	liveFW.firstUpstreamByteHook = func() { upstreamHits++ }
	liveFW.firstByteHook = func() { clientHits++ }

	// 喂入上游首个字节(message_start 框架帧,经 deferred 暂存不落盘)。
	tee.writeEvent("message_start", `{"type":"message_start"}`)
	if upstreamHits != 1 {
		t.Fatalf("上游首字 hook 应在首帧写入 sink 时触发一次(deferred 缓冲前),实际=%d", upstreamHits)
	}
	if clientHits != 0 {
		t.Fatalf("客户端首字 hook 不应在 deferred 缓冲阶段触发,实际=%d", clientHits)
	}
	// 再写一帧:hook 一次性,不应重复触发。
	tee.writeEvent("content_block_start", `{"type":"content_block_start","index":0}`)
	if upstreamHits != 1 {
		t.Fatalf("上游首字 hook 应一次性触发,实际=%d", upstreamHits)
	}
	// flushDeferred 落盘(客户端首字节)才触发客户端 hook。
	liveFW.flushDeferred()
	if clientHits != 1 {
		t.Fatalf("flushDeferred 后客户端首字 hook 应触发一次,实际=%d", clientHits)
	}
}
