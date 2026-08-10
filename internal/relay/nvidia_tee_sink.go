package relay

// nvidia_tee_sink.go: sseEventSink 的混合模式 teeSink 实现, 从 nvidia_translate_buffer.go 抽离。
//
// teeSink 把同一份 Anthropic SSE 事件双写: replay(蓄流供断流重试完整性判定与含工具回复回放) +
// live(实时推客户端 TCP socket, TTFT 最低)。分流规则: message_start/思考段/正文 text 块双写,
// tool_use 块及之后只写 replay(防错误工具调用), 尾帧只写 replay。replayOnly 时只写 replay(重试轮
// 由 resumeSink 接管)。逐行等价, 无 stdlib 依赖(全部跨符号调用走同包)。

// teeSink 是 sseEventSink 的混合模式实现:把同一份 Anthropic SSE 事件同时写给
// replay(蓄流,供断流重试完整性判定与含工具回复回放)与 live(实时推客户端 TCP socket)。
//
// 分流目标——把"正文逐块实时下发"的首字节延迟降到 TTFT,同时保留断流重试能力:
//
//	message_start          → 双写(让客户端立即进入 SSE 等待态,经 deferred 暂存)
//	思考段(thinking 块
//	content_block_start →
//	thinking_delta* →
//	signature_delta →
//	content_block_stop)    → 双写(思考逐字实时显示在客户端)
//	正文 text 块(start/delta/stop)→ 双写(正文逐字实时显示 + 蓄流兜底)
//	tool_use 块及之后所有块 → 只写 replay(含工具调用的回复整条 ready 后由 replayBodyInto 回放)
//	message_delta/stop     → 只写 replay(尾帧只在整条 ready 后由调用方决定是否送 live)
//
// tool 段为何不实时推 live:首轮推过部分 tool input_json(id=A)后断流,重试轮新 tool 块 id 可能
// 变 B,客户端持两个不同 id 半截 tool 块会让 Claude Code 按旧 id 拿残缺 JSON 发起错误工具调用
// ——功能错误比正文显示错乱严重得多。故见 tool_use 起锁定只 replay,保持现状蓄流回放。
//
// 切换时机:转译主循环保证"思考块在正文块之前完全闭合"(closeThinkingIfOpen 在开 text/tool 前必调,
// 见 nvidia_translate.go:782-784)。text 块推 live 时需追踪 live 协议态(liveBodyOpenIdx/liveMaxUsedIdx),
// 断流时供调用方拷贝进 resumeSink,在重试轮惰性补闭合 + index 重映射后续推未发正文。
//
// replayOnly=true 时一律只写 replay:用于上游断流后的重试轮(由 resumeSink 接管,tee 在重试轮不用)。
type teeSink struct {
	replay     *replayWriter
	live       *flushWriter
	toolSeen   bool // 已见 tool_use 块:此后所有块只 replay 不推 live(防错误工具调用)
	replayOnly bool // 重试轮:全部只写 replay(压住思考重复外发)
	// liveThinkingOpen 跟踪 live 上思考块是否仍处开块未闭合状态,供本轮断流判定与 resumeSink 补闭合。
	liveThinkingOpen bool
	// liveBodyOpenIdx 跟踪 live 上是否有未闭合的正文 text 块,记录其 index;-1 表已闭合或未开。
	// 断流时停在"已发 start 未发 stop"的 index,供 resumeSink 在首个正文 start 前惰性补 stop(liveBodyOpenIdx)。
	liveBodyOpenIdx int
	// liveMaxUsedIdx 记录 live 上曾发过的最大 content_block index(已闭合不复用),供 resumeSink 分配
	// 重试轮新正文块 index(liveMaxUsedIdx+1 单调递增)。包含 thinking 块的 0 与正文块 index。
	liveMaxUsedIdx int
	// liveIdxMap 记录已实时推给 live 的正文 text 块上游 index 集合(上游 block index → 客户端 index,首轮/续传均 identity 或重映射后值)。
	// 成功回放时 replayFollowingInto 据此跳过"已 live 的块",避免重复下发 start/delta/stop。
	// 仅 text 块进此表;thinking 段头由 replayFollowingInto 按类型跳过;tool 段不进 live 故不进此表。
	liveIdxMap map[int]int
	// liveThinkingPushed 标记首轮是否曾有 thinking 块实时推给 live(Once true 永不复位)。
	// 成功快照时据此设 liveStreamState.thinkingLive:retry 成功轮的 thinking 是草稿已丢弃不重发,
	// replayFollowingInto 据本字段跳过成功轮 replay 里的 thinking 头(thinking 在已 live 的 text 之前,
	// 重发会违反"思考先于正文"协议顺序,故一律跳过)。
	liveThinkingPushed bool
	// liveDeferredFlushed 标记首条实质思考内容是否已 flushDeferred(确认 200 流)。
	liveDeferredFlushed bool
}

func newTeeSink(replay *replayWriter, live *flushWriter) *teeSink {
	return &teeSink{replay: replay, live: live, liveBodyOpenIdx: -1, liveIdxMap: map[int]int{}}
}

// writeEvent 按 toolSeen/replayOnly 分流写 live+replay。
//
// 分流规则:
//   - replay 始终写(蓄流供重试判定;含工具回复整条 ready 后回放)。
//   - replayOnly(重试轮由 resumeSink 接管,tee 不用于重试轮):只写 replay。
//   - toolSeen(见过 tool_use):此后所有块只 replay,不再推 live(防错误工具调用)。
//   - 否则:thinking 段 + text 块实时推 live,delta 推 live 时刷新 liveBodyOpenIdx/liveMaxUsedIdx;
//     message_delta/message_stop 只 replay(尾帧由调用方在整条 ready 后决定,不在首轮推 live)。
func (t *teeSink) writeEvent(event, data string) {
	// replay 始终写(蓄流供重试判定与正文回放)
	t.replay.writeEvent(event, data)
	if t.replayOnly || t.live == nil {
		// 重试轮:liveThinkingOpen 保持首轮残留值不动,仅蓄流
		return
	}
	// 已锁定只 replay(见 tool_use 后):只把帧蓄流,不再实时推 live
	if t.toolSeen {
		return
	}
	// 双写分流:思考段 + 正文 text 段实时推 live,尾帧只 replay
	pushLive := false
	switch event {
	case "message_start":
		// 框架帧,经 deferred 暂存,首条实质内容到达时 flushDeferred 一并送出
		pushLive = true
	case "content_block_start":
		kind := contentBlockKind(data)
		switch kind {
		case "thinking":
			pushLive = true
			t.liveThinkingOpen = true
			t.liveThinkingPushed = true
		case "text":
			// 纯文本正文块:实时推 live + 蓄流。追踪 live 开块 index 供断流续传。
			pushLive = true
			idx := contentBlockIndex(data)
			if idx > t.liveMaxUsedIdx {
				t.liveMaxUsedIdx = idx
			}
			t.liveBodyOpenIdx = idx
			t.liveIdxMap[idx] = idx // 首轮上游 index 与客户端 index identity;成功回放时据此跳过已 live 块
		case "tool_use":
			// 见 tool_use:锁定此后只 replay(功能正确性约束)。本帧不推 live。
			t.toolSeen = true
		}
	case "content_block_delta":
		// 未锁定只 replay 时:思考段或正文 text 段的 delta 实时推 live。
		// 首条实质内容(thinking_delta 或非空 text_delta):此前 message_start 等框架帧已暂存 live.deferred,
		// 此刻 flushDeferred 触发 WriteHeader(200)+把框架帧一并送出,确认 200 流。
		// 若上游在首条实质内容前就断流,deferred 未 flush,可干净回 503。
		// 关键:保底空块的空 text_delta(ensureAtLeastOneBlock 在断流/no-content 路径补的空块)不触发 flushDeferred,
		// 否则断流轮提前 WriteHeader 200、丢失 503 干净失败能力。thinking_delta 永远非空(translator 保证),直接触发。
		dtype := deltaTypeForContentBlockDelta(data)
		isThinkingDelta := dtype == "thinking_delta" || dtype == "signature_delta"
		hasRealText := dtype == "text_delta" && deltaTextForContentBlockDelta(data) != ""
		if !isThinkingDelta && !hasRealText {
			// 空 text_delta(保底块)/未识别 delta:只蓄流不推 live,也不触发 deferred flush
			break
		}
		pushLive = true
		if !t.liveDeferredFlushed {
			t.live.flushDeferred()
			t.liveDeferredFlushed = true
		}
	case "content_block_stop":
		// 思考段 stop:推 live 并清 liveThinkingOpen;正文 text 段 stop:推 live 并清 liveBodyOpenIdx。
		// (tool 段 stop 因 toolSeen 已锁定走不到这里。)
		pushLive = true
		idx := contentBlockIndex(data)
		if t.liveThinkingOpen && idx == 0 {
			t.liveThinkingOpen = false
		}
		if t.liveBodyOpenIdx == idx {
			t.liveBodyOpenIdx = -1
		}
	}
	if pushLive {
		t.live.writeEvent(event, data)
	}
}

// writeRaw 原始 SSE 字节同步双写(转译主循环未用到 writeRaw,保留接口对称)。
func (t *teeSink) writeRaw(s string) {
	t.replay.writeRaw(s)
	if !t.replayOnly && t.live != nil && !t.toolSeen {
		t.live.writeRaw(s)
	}
}

func (t *teeSink) flush() {
	t.replay.flush()
	if !t.replayOnly && t.live != nil {
		t.live.flush()
	}
}
