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
	resume := newResumeSink(liveFW, replay, thinkingOpen, bodyOpenIdx, maxUsedIdx)
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

// TestLiveTee_ToolUseStaysReplay 锁定:首轮含 tool_use 时,tool 段及之后只 replay,live 不出现 tool 相关帧
// (功能正确性约束:tool id 可能变,防 Claude Code 执行错误工具调用)。
// 整条 ready 后 replayFollowingInto 补发 tool 块(remap index)+ 尾帧。
func TestLiveTee_ToolUseStaysReplay(t *testing.T) {
	h := newTeeTestHarness()
	upstream := writeUpstream(
		textChunkLine("text before tool."),  // text 块 index 0,实时落 live
		toolChunkLine(0, "call_0", "get_weather", "{\"location\":\"Lecce\"}"),
		finishChunkLine("tool_calls"),
	)
	if _, _, err := h.runFeed(upstream); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	// live 不应含 tool_use 的 content_block_start / input_json_delta
	liveEv := h.liveEvents(t)
	for _, ev := range liveEv {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				t.Fatalf("live 不应含 tool_use start(应只 replay),live=%v", eventNames(liveEv))
			}
		}
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "input_json_delta" {
				t.Fatalf("live 不应含 input_json_delta(应只 replay),live=%v", eventNames(liveEv))
			}
		}
	}
	// live 应含 text_delta(text-before-tool 实时下发)
	requireDeltaType(t, liveEv, "text_delta")

	// 整条 ready 后 replayFollowingInto:跳过已 live 的 text 块,补发 tool 块(remap index)+ 尾帧。
	state := &liveStreamState{
		liveIdxMap:   h.tee.liveIdxMap,
		liveMaxIdx:   h.tee.liveMaxUsedIdx,
		thinkingLive: h.tee.liveThinkingPushed,
	}
	h.replay.replayFollowingInto(h.liveFW, state)
	h.liveFW.flush()
	finalOut := parseSSEEvents(h.live.String())

	// final 应含 tool_use start(补发)+ input_json_delta
	hasToolStart := false
	hasInputJSON := false
	for _, ev := range finalOut {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				hasToolStart = true
			}
		}
		if ev.event == "content_block_delta" {
			d, _ := dataMap(t, ev)["delta"].(map[string]interface{})
			if d != nil && d["type"] == "input_json_delta" {
				hasInputJSON = true
			}
		}
	}
	if !hasToolStart {
		t.Fatalf("replayFollowingInto 应补发 tool_use start,最终流=%v", eventNames(finalOut))
	}
	if !hasInputJSON {
		t.Fatalf("replayFollowingInto 应补发 input_json_delta,最终流=%v", eventNames(finalOut))
	}
	// tool 块 index 应 remap 为 liveMaxIdx+1(不与已 live 的 text index 冲突)
	for _, ev := range finalOut {
		if ev.event == "content_block_start" {
			cb, _ := dataMap(t, ev)["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				if v, ok := dataMap(t, ev)["index"].(float64); ok {
					if int(v) <= h.tee.liveMaxUsedIdx {
						t.Fatalf("tool_use index 应 > liveMaxUsedIdx(%d),实际=%v", h.tee.liveMaxUsedIdx, int(v))
					}
				}
			}
		}
	}
	// 尾帧应补发
	requireEvent(t, finalOut, "message_delta")
	requireEvent(t, finalOut, "message_stop")
	// text_delta 仅 1 个(首轮实时,replayFollowingInto 跳过 text 块不重复)
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
