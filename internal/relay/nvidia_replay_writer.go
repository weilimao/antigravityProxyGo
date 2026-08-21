package relay

import (
	"bytes"
	"sync"
)

// nvidia_replay_writer.go: sseEventSink 的蓄流 replayWriter 实现 + 回放入口, 从 nvidia_translate_buffer.go 抽离。
//
// replayWriter 把 Anthropic SSE 事件按帧原样写进内存 bytes.Buffer 不接触 socket, 供上游断流重试
// 链路在整条 SSE 攒齐前先持留 buffer, 断流可丢弃重拉上游, ready 后再回放。replayBodyInto 回放
// "正文/工具段+尾帧"跳过 message_start 与思考段; replayFollowingInto 据 prevState 跳过已 live
// 的 text 块、remap 未 live 的块, 实现续传不重发。writePendingEvent 供 resumeSink 攒 pending。
// liveStreamState 封装客户端 live 已下发协议态, 供 replayFollowingInto 决定跳过/补发。逐行等价。

// replayWriter 是 sseEventSink 的蓄流实现:把 Anthropic SSE 事件按帧原样写进内存 bytes.Buffer,
// 不接触任何 socket。供上游断流重试链路(writeNvidiaAnthropicStream)在整条上游 SSE
// 攒齐之前先把转译结果持留在 buffer,断流可丢弃本次 buffer 重拉上游,ready 后再回放给客户端。
//
// 写入格式与 flushWriter.writeEvent 完全一致(event:/data:/空行),保证回放时客户端拿到的
// SSE 字节流与"边读边写"链路逐字节等价,行为零差异。
//
// 帧与帧之间不复用 bufio,直接写 bytes.Buffer;所有写操作加锁,防止并发乱序
// (虽然当前转译链路单协程顺序写,加锁为防御性,与 flushWriter 对齐)。
type replayWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newReplayWriter() *replayWriter {
	return &replayWriter{}
}

// writeEvent 写一帧 Anthropic SSE: event: <name>\n data: <data>\n\n。
func (r *replayWriter) writeEvent(event, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	writeSSEFrame(&r.buf, event, data)
}

// writePendingEvent 把一帧 Anthropic SSE(event: <name>\n data: <data>\n\n)追加进给定 buffer,
// 不落 live。resumeSink 用它把本轮待提交的补闭合帧 + 重映射正文帧攒进 pending,
// 仅在 message_stop(整条 ready)提交时一次性刷给 live;断流轮 pending 随 reset 丢弃。
func writePendingEvent(buf *bytes.Buffer, event, data string) {
	writeSSEFrame(buf, event, data)
}

// writeRaw 写原始 SSE 字节(如末尾 data: [DONE]\n\n 兼容 OpenAI 透传语义),原样直灌 buffer。
func (r *replayWriter) writeRaw(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.WriteString(s)
}

// flush 在蓄流语义下为空操作:真正的 flush 发生在回放给 flushWriter 那一刻,
// 这里保留方法以满足 sseEventSink 接口契约。
func (r *replayWriter) flush() {}

// bytes 返回已蓄流的完整 SSE 字节切片(只读视图),供回放层逐帧 flush 给客户端。
func (r *replayWriter) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Bytes()
}

// len 返回已蓄流字节数,供上层做超大流保护判定(超过阈值则退回边读边写,避免无界内存)。
func (r *replayWriter) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Len()
}

// reset 清空已蓄流内容(保留 buffer 容量)。混合模式下重试轮开始前丢弃首轮未完整蓄流,
// 换用纯 replay 重新蓄整条上游内容时调用。
func (r *replayWriter) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Reset()
}

// replayBodyInto 把本 replay buffer 中的"正文/工具段 + 尾帧"回放到 live 客户端 sink,
// 跳过开头的 message_start 与整个思考段(它们在首轮已通过 teeSink 实时推给 live)。
// 供混合模式(writeNvidiaAnthropicStream)在整条 ready 后把正文回放给客户端使用。
//
// 帧序约定(由 openAIChatSSEToAnthropicSSEInto 产出):
//
//	message_start
//	[content_block_start(thinking) → thinking_delta* → signature_delta → content_block_stop]  // 思考段,可缺省
//	content_block_start(text|tool_use) → ... → content_block_stop                              // 正文/工具段
//	message_delta
//	message_stop
//
// 回放策略:逐帧扫描,message_start 永不回放(首轮已发);遇思考段从其 content_block_start(thinking)
// 起整段跳到配对 content_block_stop 之后;首个正文/工具 content_block_start 起的所有帧(含 text/tool
// delta、停块、message_delta、message_stop)原样回放给 live。
//
// liveSink 期望为 flushWriter;replayBodyInto 不锁 liveSink,调用方需保证此时无其它写者并发。
func (r *replayWriter) replayBodyInto(liveSink sseEventSink) {
	r.mu.Lock()
	raw := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	frames := scanAnthropicSSEFrames(raw)
	skippingThinking := false
	bodyStarted := false
	for _, f := range frames {
		switch f.event {
		case "message_start":
			// 首帧已在首轮实时发过,不重发
			continue
		case "content_block_start":
			if !bodyStarted {
				kind := contentBlockKind(f.data)
				if kind == "thinking" {
					// 思考段开头:跳过直到它的 content_block_stop
					skippingThinking = true
					continue
				}
				if kind == "text" || kind == "tool_use" {
					bodyStarted = true
				}
			}
		case "content_block_stop":
			if skippingThinking {
				// 配对到思考块的 stop,跳过本帧后结束跳过状态
				skippingThinking = false
				continue
			}
		}
		if skippingThinking {
			continue
		}
		liveSink.writeRaw(f.raw)
	}
	liveSink.flush()
}

// liveStreamState 封装"截至本次成功判定时,客户端 live 上已经下发的内容块协议态",
// 供 replayFollowingInto 决定哪些块已 live(跳过不重发)、哪些块尚未 live(需 remap 后补发),
// 以及分配新块 index 的起点。它由 teeSink/resumeSink 在 pull 成功时落地的运行期态快照构造。
//
// 字段:
//   - liveIdxMap:上游 block index → 客户端实际 index 的映射,仅含"已实时推给 live 的正文 text 块"。
//     首轮 identity(上游 idx==客户端 idx);续传轮为 resumeSink 重映射后的客户端 idx。
//     replayFollowingInto 据此跳过已 live 的 text 块(整块 start/delta/stop 全跳),避免重复下发。
//   - liveMaxIdx:客户端已用过的最大 index(单调),replayFollowingInto 给"尚未 live 的块"(tool_use/
//     尾随 text)分配 liveMaxIdx+1 起新 client index,保证不与已 live 的 text 块 index 冲突。
//   - thinkingLive:首轮/续传轮 thinking 段是否已实时推 live(决定 replayFollowingInto 是否跳过思考头)。
type liveStreamState struct {
	liveIdxMap   map[int]int
	liveMaxIdx   int
	thinkingLive bool
}

// replayFollowingInto 是混合模式正文实时下发架构的成功回放入口:把成功轮 replay 缓冲里
// "尚未实时下发给 live 的内容"补发给 liveSink,并补发尾帧(message_delta/message_stop),
// 实现整条流的完整闭合。与旧 replayBodyInto 的根本区别:
//   - replayBodyInto 假设正文从未 live,整段正文+尾帧从头回放 → 会重复已 live 的 text 块(草稿段)。
//   - replayFollowingInto 据 prevState.liveIdxMap 跳过"已 live 的 text 块"(start/delta/stop 全跳),
//     只补发"尚未 live 的块"(tool_use、thinking 后的 text、尾随 text 等)并 remap 其 index 到
//     liveMaxIdx+1(避免与已 live 的 text 块 index 冲突),最后补发 message_delta+message_stop 尾帧。
//
// 跳过规则(按 replay 帧序):
//   - message_start:全跳(首轮已发)。
//   - thinking 段(content_block_start thinking / thinking_delta / signature_delta / 配对的 stop):
//     prevState.thinkingLive==true 时全跳;false 时(thinking 未 live,如无推理模型或首轮 thinking 未到)
//     原样回放。
//   - text 块:上游 index 在 prevState.liveIdxMap 中 → 整块(start/delta/stop)全跳(已 live);
//     否则(尾随 text,或无推理模型首轮 text 未推 live 的兜底)remap 到 liveMaxIdx+1 后回放。
//   - tool_use 块:与 text 同权——上游 index 在 prevState.liveIdxMap 中 → 整块全跳(已实时推 live);
//     否则 remap 到 liveMaxIdx+1 后回放。断流重试的 tool ID 一致性由 sseBlockStates.pinnedToolIDs
//     在翻译层强制保证(重试轮复用首轮 tool_use ID),不再依赖"tool 段只蓄流"的旧约束。
//   - message_delta/message_stop:补发一次(取 replay 尾帧数据,stop_reason/usage 与成功轮一致)。
//
// 该函数统一服务纯 text 回复(只补尾帧)、含 tool_use 回复(补 tool 块+尾帧)、续传成功(补未 live 段+尾帧)
// 三种形态,无需调用方分支判断——调用方只需在 pull 整体成功后调用一次。
func (r *replayWriter) replayFollowingInto(liveSink sseEventSink, prevState *liveStreamState) {
	r.mu.Lock()
	raw := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	frames := scanAnthropicSSEFrames(raw)
	// 跳过思考头的状态机:遇到 thinking start 进入跳过,配对到它的 stop 后退出(跳过整段思考头)。
	skippingThinking := false
	// 新块 index 分配:从 prevState.liveMaxIdx+1 起递增,给"尚未 live 的块"(tool/尾随 text)用。
	nextIdx := prevState.liveMaxIdx + 1
	// 本回放轮内"上游 idx → 客户端 idx"重映射(仅给未 live 的块;已 live 的块全跳不进此表)。
	remap := map[int]int{}
	tailEmitted := false
	for _, f := range frames {
		switch f.event {
		case "message_start":
			continue // 首轮已发
		case "content_block_start":
			kind := contentBlockKind(f.data)
			if kind == "thinking" {
				if prevState.thinkingLive {
					skippingThinking = true
					continue
				}
				// thinking 未 live:原样回放(无推理模型或首轮 thinking 未到的兜底)
				liveSink.writeRaw(f.raw)
				continue
			}
			if skippingThinking {
				// 仍在思考头跳过区间内却遇到非 thinking start:不应出现,防御性退出跳过
				skippingThinking = false
			}
			upIdx := contentBlockIndex(f.data)
			if kind == "text" || kind == "tool_use" {
				if _, alreadyLive := prevState.liveIdxMap[upIdx]; alreadyLive {
					// 已 live 的 text/tool 块:整块跳过(start/delta/stop 全跳),记录"跳过态"避免 stop 误回放
					remap[upIdx] = -1 // -1 哨兵:后续 delta/stop 见 -1 即跳过
					continue
				}
				// 尾随 text/tool:尚未 live,remap 后回放
				ci := nextIdx
				remap[upIdx] = ci
				nextIdx++
				liveSink.writeEvent("content_block_start", rewriteContentBlockIndex(f.data, ci))
				continue
			}
			// 未知 kind:原样回放(防御,不应出现)
			liveSink.writeRaw(f.raw)
		case "content_block_delta":
			if skippingThinking {
				continue // 思考头区间内 delta 全跳
			}
			upIdx := contentBlockIndex(f.data)
			ci, mapped := remap[upIdx]
			if mapped && ci == -1 {
				continue // 已 live 的 text 块 delta:跳过
			}
			if !mapped {
				// 无映射:可能是存量 thinking_delta/signature_delta(已被 thinking 头逻辑覆盖)或异常 delta。
				// thinking 未 live 路径已原样回放其 start,此处 delta 也需原样跟回放。
				liveSink.writeRaw(f.raw)
				continue
			}
			liveSink.writeEvent("content_block_delta", rewriteContentBlockIndex(f.data, ci))
		case "content_block_stop":
			if skippingThinking {
				// 配对到思考块的 stop:结束跳过区间
				skippingThinking = false
				continue
			}
			upIdx := contentBlockIndex(f.data)
			ci, mapped := remap[upIdx]
			if mapped && ci == -1 {
				continue // 已 live 的 text 块 stop:跳过
			}
			if !mapped {
				liveSink.writeRaw(f.raw)
				continue
			}
			liveSink.writeEvent("content_block_stop", rewriteContentBlockIndex(f.data, ci))
		case "message_delta":
			if tailEmitted {
				continue
			}
			tailEmitted = true
			liveSink.writeRaw(f.raw)
		case "message_stop":
			liveSink.writeRaw(f.raw)
		default:
			liveSink.writeRaw(f.raw)
		}
	}
	liveSink.flush()
}
