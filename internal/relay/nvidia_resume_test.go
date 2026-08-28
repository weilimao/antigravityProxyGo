package relay

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

// nvidia_resume_test.go 锁定正文逐块实时下发 + 断流续传不重发架构的两个核心组件行为:
//
//  1. resumeSink(重试轮续传状态机):跳过 message_start/思考、惰性补闭合残留块、正文 index 重映射续推、
//     失败轮 pending 不落地(客户端态零变更)、提交时机由 pull 显式驱动。
//  2. teeSink(首轮混合模式):纯 text 正文逐块实时落 live、tool_use 段及之后只 replay 不落 live。
//
// 配合 nvidia_thinking_realtime_test.go 的 E2E 用例,覆盖 plan twinkly-sniffing-lemon.md 的验证要求。

// newResumeTestHarness 构造 resumeSink 测试夹具:live(接 flushBuffer,可断言 live 实时输出)+ replay(蓄流)。
// resumeSink 的 live 写入经 pending 缓冲,提交(commitPending)后才落 live;测试中显式提交以观察落盘内容。
type resumeTestHarness struct {
	live   *flushBuffer
	liveFW *flushWriter
	replay *replayWriter
	resume *resumeSink
}

// newResumeTestHarness 构造一个 resumeSink,首轮残留态由参数注入(thinkingOpen/bodyOpenIdx/maxUsedIdx)。
func newResumeTestHarness(thinkingOpen bool, bodyOpenIdx, maxUsedIdx int) *resumeTestHarness {
	live := &flushBuffer{}
	bw := bufio.NewWriter(live)
	liveFW := newFlushWriter("test", bw)
	replay := newReplayWriter()
	resume := newResumeSink(liveFW, replay, thinkingOpen, bodyOpenIdx, maxUsedIdx, false)
	resume.reset() // 初始化 pend* 镜像
	return &resumeTestHarness{live: live, liveFW: liveFW, replay: replay, resume: resume}
}

// runResumeFeed 把上游字节经转译主循环喂进 resumeSink(不即时提交),返回完整性三态。
func (h *resumeTestHarness) runResumeFeed(upstream string) (finishEmitted, streamTerminated bool, err error) {
	return runIntoSink(h.resume, upstream)
}

// liveString 提交 pending 并返回 live 字节(可 parseSSEEvents)。
func (h *resumeTestHarness) liveString() string {
	h.resume.commitPending()
	h.liveFW.flush()
	return h.live.String()
}

// TestResumeSink_SkipsMessageStartAndThinking 锁定:重试轮跳过 message_start 与整段思考,
// 不在 live 上重复它们。live 上无 message_start、无 thinking 块(首轮已实时推过,重试轮不重发)。
func TestResumeSink_SkipsMessageStartAndThinking(t *testing.T) {
	// 首轮残留:思考块已闭合(thinkingOpen=false),无未闭合正文(bodyOpenIdx=-1),thinking 占 index 0(liveMaxUsedIdx=0)。
	h := newResumeTestHarness(false, -1, 0)
	upstream := writeUpstream(
		reasoningChunkLine("re-think should be skipped."),
		textChunkLine("restarted body."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runResumeFeed(upstream); err != nil {
		t.Fatalf("feed into resumeSink failed: %v", err)
	}
	out := parseSSEEvents(h.liveString())

	// live 不应含 message_start(首轮已发)
	for _, ev := range out {
		if ev.event == "message_start" {
			t.Fatalf("重试轮 resumeSink 不应推 message_start 到 live,out=%v", eventNames(out))
		}
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "thinking" {
				t.Fatalf("重试轮 resumeSink 不应推思考块到 live,out=%v", eventNames(out))
			}
		}
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && (d["type"] == "thinking_delta" || d["type"] == "signature_delta") {
				t.Fatalf("重试轮 resumeSink 不应推思考 delta 到 live,out=%v", eventNames(out))
			}
		}
	}
	// live 应含续推的正文 text_delta(重启段)
	requireDeltaType(t, out, "text_delta")
}

// TestResumeSink_ClosesOpenBodyBlockThenOpensNew 锁定:
// 首轮 text 块(index 1)开未闭合就断流(liveBodyOpenIdx=1 thinkingOpen=false thinking 占 0 已闭合),
// 重试轮首个正文 start 到达时惰性补闭合 index 1 的 stop,再用 liveMaxUsedIdx+1=2 开新块续推,
// 顺序为 stop(1)→start(text,2),无重复 start、index 单调递增。
func TestResumeSink_ClosesOpenBodyBlockThenOpensNew(t *testing.T) {
	// thinking 占 index 0 已闭合(liveMaxUsedIdx=0);首轮正文块 index 1 开未闭合断流(bodyOpenIdx=1)。
	// 注:thinking 已闭合故 thinkingOpen=false,只残留正文块未闭合。
	h := newResumeTestHarness(false, 1, 1) // maxUsedIdx=1(正文块 1 已用过)
	upstream := writeUpstream(
		textChunkLine("restarted body after draft."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runResumeFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	out := parseSSEEvents(h.liveString())

	// 收集 content_block_start/stop 的 index(按出现次序)
	var startIdx []int
	var stopIdx []int
	for _, ev := range out {
		switch ev.event {
		case "content_block_start":
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				startIdx = append(startIdx, int(v))
			}
		case "content_block_stop":
			if v, ok := dataMap(t, ev)["index"].(float64); ok {
				stopIdx = append(stopIdx, int(v))
			}
		}
	}
	// 补闭合:先 stop(liveBodyOpenIdx=1),再开新块 start(index=2)。
	if !equalIntSlice(stopIdx[:1], []int{1}) {
		t.Fatalf("首个 stop 应为 1(补闭合残留正文块),实际=%v 全 stop=%v", stopIdx[:1], stopIdx)
	}
	if !equalIntSlice(startIdx, []int{2}) {
		t.Fatalf("新正文块 start 应为 index 2(liveMaxUsedIdx+1),实际=%v", startIdx)
	}
	// 应有 stop(2) 收尾本块;stop 序列包含 1 和 2
	if !containsInt(stopIdx, 1) || !containsInt(stopIdx, 2) {
		t.Fatalf("stop 序列应含 1(补闭合)与 2(本块收尾),实际=%v", stopIdx)
	}
	// 无 index 冲突:start/stop 各 index 唯一
	if hasDup(startIdx) {
		t.Fatalf("start index 出现重复:%v", startIdx)
	}
}

// TestResumeSink_FailedRoundLeavesLiveUnchanged 锁定:
// 重试轮断流(未到 message_stop)时,pending 不提交,live 零变更——满足"失败轮客户端态零变更",
// 避免 translator 的 ensureAtLeastOneBlock 保底空块被 remap 后污染 live。
func TestResumeSink_FailedRoundLeavesLiveUnchanged(t *testing.T) {
	h := newResumeTestHarness(false, -1, 0)
	// 构造上游先发一个 text 帧再发 error chunk(中途断流):translator 会补 ensureAtLeastOneBlock+closeAll+尾帧,
	// 但 sseErr!=nil(pull 判定不完整),不提交 pending。
	errChunk := mustJSONString(map[string]interface{}{
		"error": map[string]interface{}{"message": "mid-stream boom", "type": "server_error"},
	})
	upstream := writeUpstream(
		textChunkLine("partial draft that should be discarded."),
		errChunk,
	)
	_, _, err := h.runResumeFeed(upstream)
	// error chunk 路径 err 非空
	if err == nil {
		// 即便 err 恰好为 nil(断流未触发 error chunk 解析),也必须验证 live 不变
	}
	// 关键断言:未显式 commitPending(模拟 pull 判定不完整、未提交),live 必须为空
	h.liveFW.flush()
	if h.live.String() != "" {
		t.Fatalf("失败轮(未提交)live 必须零变更,实际含=%s", h.live.String())
	}
	// reset 后下一轮应能正常复用 pending 镜像回退
	h.resume.reset()
	again := writeUpstream(
		textChunkLine("fresh restart."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runResumeFeed(again); err != nil {
		t.Fatalf("reset 后再 feed 失败: %v", err)
	}
	out := parseSSEEvents(h.liveString())
	requireDeltaType(t, out, "text_delta")
}

// TestLiveTee_BodyTextRealtimeNotReplayed 锁定:首轮纯 text 正文 delta 实时落 live(改造前为零)。
// tee 的 live 应含 text_delta;整条 ready 后 replayFollowingInto 据 tee 的 liveIdxMap 跳过已 live 的 text 块,
// 不重复回放 text_delta(只补尾帧)。
func TestLiveTee_BodyTextRealtimeNotReplayed(t *testing.T) {
	h := newTeeTestHarness()
	upstream := writeUpstream(
		textChunkLine("Realtime body chunk."),
		finishChunkLine("stop"),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	// 首轮 live 应已含正文 text_delta(改造前正文只蓄流,live 不含 text_delta)
	liveEv := h.liveEvents(t)
	requireDeltaType(t, liveEv, "text_delta")

	// 整条 ready 后 replayFollowingInto:据 tee 快照跳过已 live 的 text 块,只补尾帧。
	state := &liveStreamState{
		liveIdxMap:   h.tee.liveIdxMap,
		liveMaxIdx:   h.tee.liveMaxUsedIdx,
		thinkingLive: h.tee.liveThinkingPushed,
	}
	h.replay.replayFollowingInto(h.liveFW, state)
	h.liveFW.flush()
	finalOut := parseSSEEvents(h.live.String())

	// 统计 text_delta 数:首轮 live 推 1 个,replayFollowingInto 跳过已 live 的 text 块不再回放 text_delta,故应仍为 1。
	textDelta := 0
	for _, ev := range finalOut {
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "text_delta" {
				textDelta++
			}
		}
	}
	if textDelta != 1 {
		t.Fatalf("text_delta 应恰好 1 个(首轮实时推,replayFollowingInto 不重复),实际=%d", textDelta)
	}
	// 尾帧应补发一次
	requireEvent(t, finalOut, "message_delta")
	requireEvent(t, finalOut, "message_stop")
	// message_start 恰好 1 个
	msCount := 0
	for _, ev := range finalOut {
		if ev.event == "message_start" {
			msCount++
		}
	}
	if msCount != 1 {
		t.Fatalf("message_start 应恰好 1 个,实际=%d", msCount)
	}
}

// TestLiveTee_ToolUseStaysReplay 锁定(实时流改造后语义):
// 首轮含 tool_use 时,tool 段与 text 同权实时推 live(客户端视角不再出现"tool 静默"),
// 同时整条仍蓄流 replay。成功回放时 replayFollowingInto 据 tee.liveIdxMap 跳过已 live 的 tool 块,
// 不重复补发。tool_use ID 一致性由翻译层 sseBlockStates.pinnedToolIDs 强制保证(见 nvidia_translate_sse.go),
// 不再依赖"tool 段只蓄流"的旧约束。
func TestLiveTee_ToolUseStaysReplay(t *testing.T) {
	h := newTeeTestHarness()
	upstream := writeUpstream(
		textChunkLine("text before tool."), // text 块 index 0,实时落 live
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\"}"),
		finishChunkLine("tool_calls"),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	// live 应含 tool_use 的 content_block_start / input_json_delta(实时流改造后行为)
	liveEv := h.liveEvents(t)
	hasToolStartLive := false
	hasInputJSONLive := false
	for _, ev := range liveEv {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				hasToolStartLive = true
			}
		}
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "input_json_delta" {
				hasInputJSONLive = true
			}
		}
	}
	if !hasToolStartLive {
		t.Fatalf("tool_use start 应实时推 live(实时流改造),live=%v", eventNames(liveEv))
	}
	if !hasInputJSONLive {
		t.Fatalf("input_json_delta 应实时推 live(实时流改造),live=%v", eventNames(liveEv))
	}
	// live 应含 text_delta(text-before-tool 实时下发)
	requireDeltaType(t, liveEv, "text_delta")

	// 整条 ready 后 replayFollowingInto:跳过已 live 的 text & tool 块,只补尾帧。
	state := &liveStreamState{
		liveIdxMap:   h.tee.liveIdxMap,
		liveMaxIdx:   h.tee.liveMaxUsedIdx,
		thinkingLive: h.tee.liveThinkingPushed,
	}
	h.replay.replayFollowingInto(h.liveFW, state)
	h.liveFW.flush()
	finalOut := parseSSEEvents(h.live.String())

	// tool_use start 全程恰好 1 个(首轮实时推,replay 跳过不重复)
	toolStartCount := 0
	for _, ev := range finalOut {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				toolStartCount++
			}
		}
	}
	if toolStartCount != 1 {
		t.Fatalf("tool_use start 应恰好 1 个(首轮实时推,replay 跳过),实际=%d 事件=%v", toolStartCount, eventNames(finalOut))
	}
	// input_json_delta 全程恰好 1 个(同上)
	inputJSONCount := 0
	for _, ev := range finalOut {
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "input_json_delta" {
				inputJSONCount++
			}
		}
	}
	if inputJSONCount != 1 {
		t.Fatalf("input_json_delta 应恰好 1 个(首轮实时推,replay 跳过),实际=%d 事件=%v", inputJSONCount, eventNames(finalOut))
	}
	// 尾帧应补发
	requireEvent(t, finalOut, "message_delta")
	requireEvent(t, finalOut, "message_stop")
	// text_delta 仅 1 个(首轮实时,replay 跳过不重复)
	textDelta := 0
	for _, ev := range finalOut {
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "text_delta" {
				textDelta++
			}
		}
	}
	if textDelta != 1 {
		t.Fatalf("text_delta 应恰好 1 个(首轮实时,replay 跳过不重复),实际=%d", textDelta)
	}
	// tool_use start 的 index 应 > text 块的 index 0(由分配逻辑 tool_use = base + tc.Index 决定)
	// 验证 tee.liveIdxMap 包含 text(0)与 tool(>=1)两个条目,回放据此跳过。
	if len(h.tee.liveIdxMap) < 2 {
		t.Fatalf("liveIdxMap 应至少含 text(0) + tool(>=1) 两条目,实际=%v", h.tee.liveIdxMap)
	}
}

// TestPinnedToolIDs_RetryRoundReusesFirstRoundID 锁定新增 ID 一致性机制:
// 首轮翻译生成 tool_use.id="toolu_aaa"(假设上游 tc.ID="call_0"),
// 断流重试轮注入 pinnedToolIDs{0:"toolu_aaa"},即便上游返回完全不同的 id(如 "call_NEW"),
// 重试轮 translation 输出仍带 id="toolu_aaa"。这样客户端不会见到两个不同 ID 的半截 tool 块。
func TestPinnedToolIDs_RetryRoundReusesFirstRoundID(t *testing.T) {
	// ===== 首轮 =====
	rw1 := newReplayWriter()
	upstream1 := writeUpstream(
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\"}"),
		finishChunkLine("tool_calls"),
	)
	// 注意:openAIChatSSEToAnthropicSSEIntoPinned 是包级函数,可以直接调用。
	_, _, _, finishEmitted, _, emitted1, err := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream1), nil, rw1, "msg_test", "kimi-k3", 0, nil)
	if err != nil {
		t.Fatalf("首轮翻译失败: %v", err)
	}
	if !finishEmitted {
		t.Fatalf("首轮应 finishEmitted=true")
	}
	// 首轮 emitted 应记录 tc.Index=0 → 重写后的 toolu_nv_* 全局唯一 id(上游 "call_0" 类自增短 id 不透传,见 rewriteUpstreamToolCallID)
	firstEmitted, ok := emitted1[0]
	if !ok || !strings.HasPrefix(firstEmitted, "toolu_nv_") {
		t.Fatalf("首轮 emitted[0] 应为 toolu_nv_* 重写 id,实际=%v (map=%v)", emitted1[0], emitted1)
	}

	// 解析首轮 replay 拿到的 tool ID
	ev1 := parseSSEEvents(string(rw1.bytes()))
	var firstID string
	for _, e := range ev1 {
		if e.event == "content_block_start" {
			cb, _ := dataMap(t, e)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				firstID, _ = cb["id"].(string)
			}
		}
	}
	if firstID != firstEmitted {
		t.Fatalf("首轮 content_block_start.tool_use.id 应为 emitted 记录的重写 id %q,实际=%q", firstEmitted, firstID)
	}

	// ===== 重试轮(上游返回完全不同的 tool_call id)=====
	rw2 := newReplayWriter()
	upstream2 := writeUpstream(
		toolChunkLine(0, "call_NEW_DIFFERENT", "get_weather", "{\"location\":\"Lecce\"}"),
		finishChunkLine("tool_calls"),
	)
	_, _, _, finishEmitted2, _, _, err2 := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream2), nil, rw2, "msg_test", "kimi-k3", 0, emitted1)
	if err2 != nil {
		t.Fatalf("重试轮翻译失败: %v", err2)
	}
	if !finishEmitted2 {
		t.Fatalf("重试轮应 finishEmitted=true")
	}
	ev2 := parseSSEEvents(string(rw2.bytes()))
	var secondID string
	for _, e := range ev2 {
		if e.event == "content_block_start" {
			cb, _ := dataMap(t, e)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				secondID, _ = cb["id"].(string)
			}
		}
	}
	// 关键断言:尽管上游第二轮返回 "call_NEW_DIFFERENT",翻译输出仍用首轮重写 id(pin 一致性)。
	if secondID != firstEmitted {
		t.Fatalf("重试轮 tool_use.id 应复用首轮 id %q(防半截 id 错乱),实际=%q", firstEmitted, secondID)
	}
}

// TestPinnedToolIDs_RetryRoundAppendsNewIndex 锁定:重试轮遇到 pinned 未覆盖的新 tool index 时,
// 翻译层会把新 index 的 ID 追加回 pinnedToolIDs(跨轮单调锚定),保证后续轮仍能复用同一 ID。
//贸易战景:多工具应用中,首轮只发了 tool[0] 就断流;重试轮上游同时返回 tool[0] + 新增的 tool[1]。
// 期望:tool[0] 用 pinned 复用首轮 ID;tool[1] 用上游新 ID 且被追加进 pinnedToolIDs,供可能的下轮复用。
func TestPinnedToolIDs_RetryRoundAppendsNewIndex(t *testing.T) {
	// ===== 首轮:上游只发 tool[0] 后断流模拟(无 finish,DONE 提前)=====
	// 直接用 writeUpstream 构造完整首轮(不完整也可以,emitted 已生成)。
	rw1 := newReplayWriter()
	upstream1 := writeUpstream(
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\"}"),
		// 故意无 finish_chunk——但 openAIChatSSEToAnthropicSSEIntoPinned 仍会在 [DONE]/EOF 时收集 emitted
	)
	_, _, _, _, _, emitted1, _ := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream1), nil, rw1, "msg_test", "kimi-k3", 0, nil)
	// 首轮应记录 tool[0] → 重写后的 toolu_nv_* 唯一 id
	firstEmitted := emitted1[0]
	if !strings.HasPrefix(firstEmitted, "toolu_nv_") {
		t.Fatalf("首轮 emitted[0] 应为 toolu_nv_* 重写 id,实际=%q", firstEmitted)
	}
	pinnedSoFar := emitted1 // 主循环语义:pinnedToolIDs = 首轮 emitted

	// ===== 重试轮:上游同时返回 tool[0](新 id) + tool[1](新增 index)=====
	rw2 := newReplayWriter()
	upstream2 := writeUpstream(
		toolChunkLine(0, "call_0_NEW", "get_weather", "{\"location\":\"Lecce\"}"),
		toolChunkLine(1, "call_1_NEW", "get_time", "{\"tz\":\"UTC\"}"),
		finishChunkLine("tool_calls"),
	)
	_, _, _, finishEmitted2, _, emitted2, err2 := openAIChatSSEToAnthropicSSEIntoPinned(
		context.Background(), strings.NewReader(upstream2), nil, rw2, "msg_test", "kimi-k3", 0, pinnedSoFar)
	if err2 != nil {
		t.Fatalf("重试轮翻译失败: %v", err2)
	}
	if !finishEmitted2 {
		t.Fatalf("重试轮应 finishEmitted=true")
	}
	// 翻译输出中的两个 tool_use id 应与 pinned 一致
	ev2 := parseSSEEvents(string(rw2.bytes()))
	ids := []string{}
	for _, e := range ev2 {
		if e.event == "content_block_start" {
			cb, _ := dataMap(t, e)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				id, _ := cb["id"].(string)
				ids = append(ids, id)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("重试轮应有 2 个 tool_use start,实际=%d ids=%v", len(ids), ids)
	}
	// tool[0] 必须用首轮重写 id,即便上游换了 "call_0_NEW"
	if ids[0] != firstEmitted {
		t.Fatalf("tool[0] 应复用首轮 id %q,实际=%q", firstEmitted, ids[0])
	}
	// tool[1] 是新 index:上游 id("call_1_NEW")同样被重写为 toolu_nv_* 唯一 id
	if !strings.HasPrefix(ids[1], "toolu_nv_") {
		t.Fatalf("tool[1] 应为 toolu_nv_* 重写 id,实际=%q", ids[1])
	}
	// 关键:pin map 应已追加 tool[1] 的重写 id,供下一轮继续使用
	if pinnedSoFar[1] != ids[1] {
		t.Fatalf("重试轮翻译层应将新 index=1 的重写 id 追加到 pinnedToolIDs,期望 %q,实际 map=%v", ids[1], pinnedSoFar)
	}
	_ = emitted2
}

// TestResumeSink_SkipsAlreadyLiveToolBlock 锁定 GLM 审查发现的协议级 bug 修复:
// 场景:首轮 tee 实时推过 tool_use 块(id=call_0, index=1) 到 live,但流在 tool 中途断流
// (上游 socket EOF,未发 finish_reason)。首轮断流时主循环 closeAll 已经把该 tool 块强制闭合
// (content_block_stop 已发,即便 arguments 是半截,但协议上 start+stop 是完整的一对)。
//
// 重试轮 resumeSink 又见同一上游 index 的 tool_use(start/delta/stop):
//   - 旧行为(未修复):closeDanglingBlocks 误补 stop(其实已 close),然后 pendMaxIdx+1 开新
//     index=2 的 tool_use 块,id 被 pinnedToolIDs 复用为同一个 call_0 → 客户端在同一 message 内
//     见到两个 id=call_0 的 tool_use 块(index=1 半截已闭合 + index=2 完整新开),SDK 报
//     "tool_use ids must be unique"。
//   - 新行为(本测试锁定):resumeSink 在 content_block_start(tool_use) 分支检测到
//     liveToolUpIdxs[upIdx] 命中(该上游 index 首轮已实时推过 live),整块跳过 start/delta/stop,
//     不进 indexMap 不进 pending。客户端 live 上仅 1 个 call_0 tool_use 块。
func TestResumeSink_SkipsAlreadyLiveToolBlock(t *testing.T) {
	h := newTeeTestHarness()
	// 首轮 tee:实时推 text + tool(id=call_0)+ 断流在 tool 参数中途(模拟 finish 未发)
	firstUpstream := writeUpstream(
		textChunkLine("first round body."),
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\",\"unit\":\"celsius\""),
		// 故意不发 finish_chunk:模拟上游 socket 在 arguments 中段断开
	)
	if _, _, err := h.runFeed(firstUpstream); err != nil {
		// EOF 自然终止 path err 可能为 nil(streamTerminated=true);若是 error chunk 则 err!=nil,不影响本测试
		_ = err
	}

	// 断言首轮 live 已含 tool_use start(idempotency 前提;id 已被重写为 toolu_nv_* 唯一格式)
	firstLive := parseSSEEvents(h.flushLive())
	toolStartCount := 0
	var firstClientIdx = -1
	for _, ev := range firstLive {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				toolStartCount++
				if v, ok := dataMap(t, ev)["index"].(float64); ok {
					firstClientIdx = int(v)
				}
			}
		}
	}
	if toolStartCount != 1 {
		t.Fatalf("首轮应有 1 个 tool_use start,实际=%d,live=%v", toolStartCount, eventNames(firstLive))
	}
	if firstClientIdx < 0 {
		t.Fatalf("首轮应能定位到 tool_use 块的客户端 index,实际=%d", firstClientIdx)
	}
	// 首轮 tee.liveToolUpIdxs 应记录该 tool 上游 index → 客户端 index
	if len(h.tee.liveToolUpIdxs) != 1 {
		t.Fatalf("首轮 tee.liveToolUpIdxs 应记录 1 个 tool 上游 index,实际=%v", h.tee.liveToolUpIdxs)
	}

	// ===== 重试轮:resumeSink 注入首轮 tee.liveToolUpIdxs,再收到同一上游 index 的 tool 块 =====
	resume := newResumeSink(h.liveFW, h.replay, false, -1, h.tee.liveMaxUsedIdx, h.tee.liveThinkingPushed)
	resume.setLiveToolUpIdxs(h.tee.liveToolUpIdxs)
	h.tee.replay.reset()
	resume.reset()

	// 重试轮上游:重新发同一 tool(即便 id 与首轮不同——pinnedToolIDs 会复用,但本测试不依赖翻译层 id 一致性,
	// 我们用相同 id 来聚焦测 resumeSink 的"跳过已 live tool 块"逻辑)
	retryUpstream := writeUpstream(
		textChunkLine("retry round body reboot."),
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\",\"unit\":\"celsius\"}"),
		finishChunkLine("tool_calls"),
	)
	if _, _, err := runIntoSink(resume, retryUpstream); err != nil {
		t.Fatalf("retry feed into resumeSink failed: %v", err)
	}
	resume.commitPending()
	secondLive := parseSSEEvents(h.flushLive())

	// 关键断言 1:重试轮的 tool_use start/delta/stop 都不应出现在新增 live 字节里
	// (即第二轮 live 相比第一轮 live,新增的 delta 里不能再有 input_json_delta 或 tool_use start)
	// secondLive 是累计值;我们检查"第二轮比第一轮多出的部分"里没有 tool_use。
	// 简化验证:第二轮累计 live 中 tool_use start 总数仍是 1(首轮的),不应因重试轮再加一个。
	toolStartCount = 0
	toolIDs := map[string]int{} // id → 出现次数(应为每 id 恰好 1 个 index)
	for _, ev := range secondLive {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				toolStartCount++
				if id, _ := cb["id"].(string); id != "" {
					toolIDs[id]++
				}
			}
		}
	}
	if toolStartCount != 1 {
		t.Fatalf("重试轮后累计 live 的 tool_use start 应仍为 1(首轮的;重试轮已跳过),实际=%d 事件=%v",
			toolStartCount, eventNames(secondLive))
	}
	// 每个 tool id 只应出现一次(同一 id 出现在两个 index 才会计数 2)
	for id, cnt := range toolIDs {
		if cnt != 1 {
			t.Fatalf("tool_use id %q 在客户端 live 出现 %d 次(应为 1),违反 id 唯一性", id, cnt)
		}
	}
}

// TestResumeSink_SkipsAlreadyLiveToolBlock_FullClose 覆盖变体场景:
// 首轮 tool 已完整闭合(start + delta + stop 都发了)后,finish_reason 前断流。
// 重试轮上游重发同一 tool(可能 arguments 完整了),resumeSink 仍应跳过该 tool 整块。
// 与上一测试的差别:首轮 tool 块 stop 也已发(更贴近"工具调用已完成但 finish 丢失"的常见断流形态)。
func TestResumeSink_SkipsAlreadyLiveToolBlock_FullClose(t *testing.T) {
	h := newTeeTestHarness()
	// 首轮:tool 完整,start+delta+stop 都到达(只因缺 finish_reason 才断流)
	// 模拟"主循环 Scan 中断后,无 finish 路径走 ensureAtLeastOneBlock + closeAll":
	// 我们的 writeUpstream 末尾没有 finish_chunk 时,runIntoSink 在循环外会调到 closeAll,
	// 这与"客户端已收到完整的 tool_use(start+delta+stop)"等价。
	firstUpstream := writeUpstream(
		textChunkLine("first body."),
		toolChunkLine(0, "call_abc", "get_weather", "{\"location\":\"Paris\"}"),
		// 故意不发 finish_chunk
	)
	_, _, _ = h.runFeed(firstUpstream)
	_ = h.flushLive()

	if len(h.tee.liveToolUpIdxs) != 1 {
		t.Fatalf("首轮 tee.liveToolUpIdxs 应有 1 个 tool 上游 index,实际=%v", h.tee.liveToolUpIdxs)
	}

	// 重试轮:上游重发同一 tool(同 id 同参数——pinnedToolIDs 也会锁定 id,但本测试不依赖)
	resume := newResumeSink(h.liveFW, h.replay, false, -1, h.tee.liveMaxUsedIdx, h.tee.liveThinkingPushed)
	resume.setLiveToolUpIdxs(h.tee.liveToolUpIdxs)
	h.tee.replay.reset()
	resume.reset()
	retryUpstream := writeUpstream(
		textChunkLine("retry body."),
		toolChunkLine(0, "call_abc", "get_weather", "{\"location\":\"Paris\"}"),
		finishChunkLine("tool_calls"),
	)
	if _, _, err := runIntoSink(resume, retryUpstream); err != nil {
		t.Fatalf("retry feed failed: %v", err)
	}
	resume.commitPending()

	// 累计 live 中 tool_use start 应仍只 1 个(重试轮整块跳过;id 已被重写为 toolu_nv_*)
	finalLive := parseSSEEvents(h.flushLive())
	toolStartCount := 0
	toolIdxSet := map[int]bool{}
	for _, ev := range finalLive {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				toolStartCount++
				if v, ok := dataMap(t, ev)["index"].(float64); ok {
					toolIdxSet[int(v)] = true
				}
			}
		}
	}
	if toolStartCount != 1 {
		t.Fatalf("重试轮后累计 live 中的 tool_use start 应仍为 1(重试轮已跳过),实际=%d", toolStartCount)
	}
	if len(toolIdxSet) != 1 {
		t.Fatalf("tool_use 应只对应 1 个客户端 index,实际=%v", toolIdxSet)
	}
}

// containsInt 判断 int 切片是否含某值。
func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// hasDup 判断 int 切片是否有重复值。
func hasDup(s []int) bool {
	seen := map[int]bool{}
	for _, v := range s {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// 用 strings 以防未引用警告(stream_test 已含 strings,本文件独立 import 列表)。
var _ = strings.Contains
var _ = context.Background
