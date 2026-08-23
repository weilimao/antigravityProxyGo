package relay

import (
	"bytes"
)

// nvidia_resume_sink.go: sseEventSink 的重试轮 resumeSink 实现, 从 nvidia_translate_buffer.go 抽离。
//
// resumeSink 把上游再次转译产的 Anthropic SSE 按首轮客户端已收协议态过滤后实时推 live, 实现
// "续传不重发": message_start/思考段全跳, 正文 text 块惰性补闭合残留未闭合块(closeDanglingBlocks)+
// 用 liveMaxUsedIdx+1 开新块(index 重映射), message_delta/stop 只蓄流不推 live(由调用方整条 ready
// 后经 replayFollowingInto 补发)。pending 字节提交(commitPending)时才落 live, 失败轮随 reset 丢弃,
// 客户端态零变更。逐行等价。

// resumeSink 是重试轮的 sseEventSink:把上游(经 openAIChatSSEToAnthropicSSEInto 再次转译)
// 产的 Anthropic SSE 事件,按首轮客户端已收到的协议态(liveThinkingOpen/liveBodyOpenIdx/liveMaxUsedIdx)
// 过滤后实时推给 live,实现"续传不重发":
//
//   - message_start 全跳(首轮已发,不能重复发)。
//   - 思考段(content_block_start thinking / thinking_delta / signature_delta / content_block_stop thinking)
//     全跳——首轮实时推到客户端的思考是草稿,重试轮不重发(避免重复 message_start 外的 index 冲突)。
//   - tool_use 段:实时推 live(经 pending 缓冲,提交时落盘)。断流重试的 tool ID 一致性由
//     sseBlockStates.pinnedToolIDs 在翻译层强制保证——重试轮复用首轮 tool_use ID,客户端不会因 ID 变化
//     拿半截旧 JSON 执行错误工具调用。
//   - 正文 text 块:首个正文 content_block_start 到达时惰性补闭合客户端残留的未闭合块(先思考→再正文),
//     然后用 liveMaxUsedIdx+1 开新块(index 重映射),后续 text_delta/stop 改写 index 后经 pending 推 live。
//   - message_delta/message_stop:直接推 live(首轮不推两尾帧,故不重复)。
//
// 同步写 replay:供 pull 的完整性判定(finishEmitted||streamTerminated 需看 replay 是否收到 finish_reason)。
// replay 与 live 双写时,先写 replay(原样)再过滤/重映射后写 live。
//
// 设计依据见 plan twinkly-sniffing-lemon.md 核心技术结论:(B) 假闭合旧块+开新块+从头发新正文是 Anthropic
// 协议下"续传不重发"的唯一合法解,客户端可见"草稿段(已闭合)+重启段"两段相邻文字,无 index 冲突、SDK 不报错。
type resumeSink struct {
	live   *flushWriter
	replay *replayWriter

	// 跨轮持久态(只在成功提交时推进;失败轮 reset 后回退到上一轮提交值):
	//   - liveMaxUsedIdx:客户端 live 上已用过的最大 index(已闭合不复用),重试轮新块从此+1 分配。
	//   - liveThinkingOpen:live 上 index 0 thinking 块是否仍开未闭合(首轮断流时由 tee 拷入;
	//     实际首轮 translator closeAll 多已闭合它故常为 false,保留作补闭合兜底)。
	//   - liveBodyOpenIdx:live 上残留未闭合正文块 index(-1 表无);同上多已被 closeAll 闭合故常 -1。
	//   - liveThinkingPushed:客户端 live 上是否"曾有过"思考块被实时推过(首轮 tee.liveThinkingPushed 透传,
	//     Once true 永不复位,跨重试轮/跨周期保留)。重试/续传成功时回填进 liveStreamState.thinkingLive,
	//     供 replayFollowingInto 判定是否跳过成功轮 replay 里的思考头:首轮思考草稿已 live 则跳过(重发会
	//     违反"index 单调不复用"+"思考先于正文",触发客户端 SDK "Mismatched content block type ... thinking")。
	// 这四个字段在 pending-轮内不被直接改写,而由 pend* 镜像字段在提交时回填(见下)。
	liveMaxUsedIdx     int
	liveThinkingOpen   bool
	liveBodyOpenIdx    int
	liveThinkingPushed bool
	// liveToolUpIdxs 首轮 tee 的 tool_use 块上游 index 集合(上游 block index → 客户端 index)。
	// 重试/续传轮见到该集合内的 tool_use start/delta/stop,整块跳过不推 live——客户端首轮已实时
	// 收到该 tool 的完整闭环(content_block_start + content_block_stop,即便 arguments 可能是半截,
	// 但 closeAll 会发 stop,协议合规)。若不跳过,resumeSink 会先用 closeDanglingBlocks 补 stop 关掉
	// 首轮那个半截 tool 块、再开新 index 新块(id 被 pinnedToolIDs 复用),导致同一 message 内同一
	// tool_use.id 出现在两个 index 上,客户端 SDK 会报 "tool_use ids must be unique"。
	// 由调用方在构造后通过 setLiveToolUpIdxs 注入(从首轮 tee 拷贝)。
	liveToolUpIdxs map[int]int
	// skippedToolUpIdxs 本轮已被跳过的 tool_use 上游 index 集合(start 命中跳过即记录),
	// content_block_delta/stop 据此判断"是否属于已跳过的 tool 块",是则同步跳过。
	// reset 时保留(跨轮单调,与 liveToolUpIdxs 一致)。
	skippedToolUpIdxs map[int]bool

	// 本轮运行期态(reset 每轮清零):
	closedDangling   bool // 惰性补闭合标志:首个正文 start/tool_use 前补一次;reset 复位
	messageStartSeen bool
	stopSent         bool
	indexMap         map[int]int  // 本轮"上游 idx → 客户端 idx"重映射(成功快照回传给 replayFollowingInto)
	pending          bytes.Buffer // 本轮待提交给 live 的字节(补闭合帧 + 重映射正文/tool start/delta/stop);断流轮 reset 丢弃
	// pend* 是跨轮持久态的本轮镜像:轮内分配/补闭合改写 pend*,提交时回填到 liveMaxUsedIdx 等。
	// 失败轮 reset 后 pend* 重新从持久态初始化,故失败轮的 index 分配/块开闭全被丢弃,客户端态零变更。
	pendMaxIdx       int  // 本轮已分配的最大 index(从 liveMaxUsedIdx 起步)
	pendBodyOpenIdx  int  // 本轮 pending 中当前未闭合正文块 index(-1 表无)
	pendThinkingOpen bool // 本轮提交后 liveThinkingOpen 的目标值
}

// newResumeSink 构造重试轮 sink。thinkingOpen/bodyOpenIdx/maxUsedIdx 为首轮断流时 tee 的 live 残留态
// (惰性补闭合与新块 index 分配起点);thinkingPushed 为首轮 tee.liveThinkingPushed(客户端 live 是否曾推过
// 思考块,Once true 永不复位),重试/续传成功时透传进 liveStreamState.thinkingLive 供 replayFollowingInto
// 决定是否跳过成功轮 replay 思考头。跨重试轮复用时 reset 保留 thinkingPushed 不复位。
//
// 构造后调用方需按需调 setLiveToolUpIdxs 注入"首轮已实时推 live 的 tool_use 上游 index 集合",
// 供重试轮跳过这些 tool 块(start/delta/stop 整块不推 live,见该字段注释)——防"同一 tool_use.id
// 在同一 message 内出现在两个不同 index"的协议违规。
func newResumeSink(live *flushWriter, replay *replayWriter, thinkingOpen bool, bodyOpenIdx, maxUsedIdx int, thinkingPushed bool) *resumeSink {
	return &resumeSink{
		live:               live,
		replay:             replay,
		liveMaxUsedIdx:     maxUsedIdx,
		liveThinkingOpen:   thinkingOpen,
		liveBodyOpenIdx:    bodyOpenIdx,
		liveThinkingPushed: thinkingPushed,
		indexMap:           map[int]int{},
		liveToolUpIdxs:     map[int]int{},
		skippedToolUpIdxs:  map[int]bool{},
	}
}

// setLiveToolUpIdxs 注入首轮 tee 已实时推 live 的 tool_use 上游 index 集合(独占拷贝,不再共享底层 map)。
// 应在首个非首轮 attempt 进入本 sink 前调用;空/nil 时等价于"首轮未实时推过任何 tool"(行为回退到旧路径)。
func (r *resumeSink) setLiveToolUpIdxs(src map[int]int) {
	if r.liveToolUpIdxs == nil {
		r.liveToolUpIdxs = map[int]int{}
	}
	for k, v := range src {
		r.liveToolUpIdxs[k] = v
	}
}

// reset 跨重试轮复用前的复位:清本轮运行期态(pending/indexMap/closedDangling/尾帧标志 +
// pend* 镜像回退到持久值),保留 liveMaxUsedIdx/liveThinkingOpen/liveBodyOpenIdx——它们反映"截至上一轮
// 成功提交,客户端 live 上的协议态",本轮据此惰性补闭合并分配新块 index。
// 失败轮(未到 message_stop)的 pending/index 分配随 reset 全部丢弃,客户端态零变更。
func (r *resumeSink) reset() {
	r.indexMap = map[int]int{}
	r.closedDangling = false
	r.messageStartSeen = false
	r.stopSent = false
	r.pending.Reset()
	r.pendMaxIdx = r.liveMaxUsedIdx
	r.pendBodyOpenIdx = r.liveBodyOpenIdx
	r.pendThinkingOpen = r.liveThinkingOpen
}

// closeDanglingBlocks 惰性补闭合客户端仍未闭合的块:先 thinking(0)→再正文(pendBodyOpenIdx),
// 顺序与 writeNvidiaAnthropicStream 补闭合一致,保证思考块在正文块之前完全闭合。
// 仅在本重试轮首次见到正文 content_block_start / tool_use start 时调一次(closedDangling 守门)。
// 补闭合帧写进 pending(断流轮会随 reset 丢弃,不污染 live)。
func (r *resumeSink) closeDanglingBlocks() {
	if r.closedDangling {
		return
	}
	r.closedDangling = true
	// 先补闭合残留的 thinking 块(index 0):发空串 signature_delta + content_block_stop(0)
	if r.pendThinkingOpen {
		writePendingEvent(&r.pending, "content_block_delta", contentBlockSignatureDeltaPayload(0, ""))
		writePendingEvent(&r.pending, "content_block_stop", contentBlockStopPayload(0))
		r.pendThinkingOpen = false
	}
	// 再补闭合残留的正文块(pendBodyOpenIdx):只发 content_block_stop(正文块无 signature_delta)
	if r.pendBodyOpenIdx >= 0 {
		writePendingEvent(&r.pending, "content_block_stop", contentBlockStopPayload(r.pendBodyOpenIdx))
		r.pendBodyOpenIdx = -1
	}
}

// writeEvent 按帧类型过滤后写 replay + pending(提交时才落 live)。replay 始终原样写。
func (r *resumeSink) writeEvent(event, data string) {
	// replay 始终原样写(供 pull 完整性判定;成功快照时 replayFollowingInto 回放未 live 段)
	r.replay.writeEvent(event, data)

	switch event {
	case "message_start":
		// 首轮已发,重试轮全跳
		r.messageStartSeen = true
		return
	case "content_block_start":
		kind := contentBlockKind(data)
		if kind == "thinking" {
			// 思考段全跳(首轮思考草稿不重发)
			return
		}
		if kind == "tool_use" {
			// 工具块:实时推 live(经 pending)。先判定"是否首轮已实时推过 live 的同一 tool 上游块":
			upIdx := contentBlockIndex(data)
			if upIdx < 0 {
				upIdx = 0
			}
			if _, alreadyLive := r.liveToolUpIdxs[upIdx]; alreadyLive {
				// 协议正确性关键路径:首轮已把该 tool 块实时推 live 且被 closeAll 完整闭环(start+stop)。
				// 重试轮必须整块跳过该 tool 的 start/delta/stop——否则 resumeSink 会:
				//   ① closeDanglingBlocks 补 stop 关掉首轮的半截块(该块首轮已 closeAll,不应再补);
				//   ② pendMaxIdx+1 开新 index 新块,id 被 pinnedToolIDs 复用成同一个 toolu_xxx;
				//   → 客户端在同一 message 内见到同一 tool_use.id 但不同 index 的两个块,触发 SDK
				//     "tool_use ids must be unique" 报错。
				// 此处标记 skippedToolUpIdxs,后续 delta/stop 同步跳过(不进 indexMap,不进 pending)。
				r.skippedToolUpIdxs[upIdx] = true
				return
			}
			// 新 tool(首轮未实时推过):惰性补闭合 → 新 index 映射 → 改写 index 后写 pending(提交时落 live)。
			// tool ID 一致性由翻译层 pinnedToolIDs 强制保证,无需再锁 replay。
			r.closeDanglingBlocks()
			newIdx := r.pendMaxIdx + 1
			r.indexMap[upIdx] = newIdx
			r.pendMaxIdx = newIdx
			r.pendBodyOpenIdx = newIdx // tool 块也占用 bodyOpenIdx(stop 时清 -1)
			writePendingEvent(&r.pending, "content_block_start", rewriteContentBlockIndex(data, newIdx))
			return
		}
		// text 块:惰性补闭合 → 新 index 映射 → 改写 index 后写 pending(提交时落 live)
		r.closeDanglingBlocks()
		upIdx := contentBlockIndex(data)
		if upIdx < 0 {
			upIdx = 0
		}
		newIdx := r.pendMaxIdx + 1
		r.indexMap[upIdx] = newIdx
		r.pendMaxIdx = newIdx
		r.pendBodyOpenIdx = newIdx // 记录本轮新开正文块 index
		writePendingEvent(&r.pending, "content_block_start", rewriteContentBlockIndex(data, newIdx))
		return
	case "content_block_delta":
		dtype := deltaTypeForContentBlockDelta(data)
		// 思考段的 delta(thinking_delta/signature_delta)全跳
		if dtype == "thinking_delta" || dtype == "signature_delta" {
			return
		}
		upIdx := contentBlockIndex(data)
		// 首轮已 live 的 tool 块,start 同步跳过(见 writeEvent content_block_start/tool_use 分支),
		// 其 delta 也必须跳过——否则客户端会见"无对应 start 的孤儿 input_json_delta"。
		if r.skippedToolUpIdxs[upIdx] {
			return
		}
		newIdx, ok := r.indexMap[upIdx]
		if !ok {
			// 无对应开块的 delta:防御丢弃(不应出现——text/tool 块必先有 start 建 map)
			return
		}
		writePendingEvent(&r.pending, "content_block_delta", rewriteContentBlockIndex(data, newIdx))
		return
	case "content_block_stop":
		upIdx := contentBlockIndex(data)
		// 首轮已 live 的 tool 块 start 已跳过,其 stop 也同步跳过(避免重复 stop、避免 indexMap 查不到时
		// 误落到"非 body"分支而漏跳过)。
		if r.skippedToolUpIdxs[upIdx] {
			return
		}
		// 思考段 stop(indexMap 无该 idx):全跳
		newIdx, isBody := r.indexMap[upIdx]
		if !isBody {
			return
		}
		if r.pendBodyOpenIdx == newIdx {
			r.pendBodyOpenIdx = -1 // 本轮块已闭合,清回 -1
		}
		writePendingEvent(&r.pending, "content_block_stop", rewriteContentBlockIndex(data, newIdx))
		return
	case "message_delta":
		// 尾帧只蓄流不推 live:本轮可能仍会断流(上游未给 finish_reason 即断),若把 message_delta 推 live,
		// 客户端流的语义尾帧就提前落地,后续重试轮再续推正文会违反"流已结束不能再有块"。尾帧统一由
		// 调用方(writeNvidiaAnthropicStream 整体成功后经 replayFollowingInto)一次性补发给 live。
		return
	case "message_stop":
		// 整条 ready 信号之一(但 translator 在上游 error chunk 断流路径也会无条件补 message_stop,
		// 故 message_stop 不能单独作为提交依据——提交由 pull 在确认 finishEmitted||streamTerminated 后
		// 显式调 commitPending() 完成)。幂等(stopSent)防重入。
		if r.stopSent {
			return
		}
		r.stopSent = true
		return
	}
	// 未识别的事件类型:默认不推 live(防御),replay 已写
}

// commitPending 由 pull 在确认本轮整条 ready(sseErr==nil && (finishEmitted||streamTerminated))后显式调用:
// 把 pending 字节一次性刷给 live(滚滚落盘为"重启段"),并把本轮 pend* 镜像回填到持久态(liveMaxUsedIdx 等),
// 供下一轮/调用方快照使用。失败轮(未确认完整)不调此方法,pending 随 reset 丢弃,持久态不变。
func (r *resumeSink) commitPending() {
	if r.pending.Len() > 0 && r.live != nil {
		r.live.writeRaw(r.pending.String())
		r.pending.Reset()
	}
	r.liveMaxUsedIdx = r.pendMaxIdx
	r.liveThinkingOpen = r.pendThinkingOpen
	r.liveBodyOpenIdx = r.pendBodyOpenIdx
}

// writeRaw 原始 SSE 字节:重试轮转译主循环未用到 writeRaw,保留接口对称,只写 replay。
func (r *resumeSink) writeRaw(s string) {
	r.replay.writeRaw(s)
}

func (r *resumeSink) flush() {
	r.replay.flush()
	if r.live != nil {
		r.live.flush()
	}
}

// pingFrame 在 resumeSink 语义下:重试轮里客户端的 inactivity 看门狗依然在跑,
// 必须让心跳真实到达 live(仅 live,不入 replay)。它与 content_block_* 的写入顺序无关,
// 不重映射 index,也不进 pending,因为它是协议外的保活帧。
func (r *resumeSink) pingFrame() {
	if r.live != nil {
		r.live.pingFrame()
	}
}
