package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// nvidia_translate_buffer.go: sseEventSink 接口 + flushWriter/replayWriter/teeSink/resumeSink 蓄流实现 + 帧扫描/改写辅助。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// watchCancel 监听 ctx 取消：一旦 ctx 被撤销(客户端主动断开 / 请求超时),
// 立即 Close 上游 resp.Body,使阻塞在 bufio.Scanner.Scan() 上的读循环以
// "read on closed body" 错误立即返回,从而跳出逐帧回写的主循环。
//
// 这是 NVIDIA 流式链路"取消即断"的唯一可靠触发点 —— 不依赖下游写错检测
// (存在竞态:客户端断开时若 scanner 正好在两帧之间阻塞读,写错不会触发)。
// ctx.Done() 由 net/http 在客户端 TCP 半关闭时确定性触发,无竞态。
//
// 对齐谷歌链路 handler.go:1131-1140 的 cancelChan 监听协程,但抽成可复用 helper,
// 供 NVIDIA 三条流式回写路径(Anthropic / Responses / OpenAI 透传)统一接入。
//
// 返回 stop 函数:defer 调用以释放监听 goroutine,避免泄漏。
//   body 为上游响应体;调用方负责保证只在 ctx 取消时由本 helper 触发 Close,
//   正常流式读完时 scanner 先返回 EOF,主循环退出后 defer stop() 释放 goroutine,
//   body 的最终 Close 仍由各路径既有 defer resp.Body.Close() 负责。
func watchCancel(ctx context.Context, body io.ReadCloser) (stop func()) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// 客户端已断开:主动切断上游连接,让阻塞的 Scan() 立即返回
			_ = body.Close()
		case <-stopped:
			// 正常收尾:主循环已退出,无需 Close(由既有的 defer resp.Body.Close() 兜底)
		}
		close(done)
	}()
	return func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
		<-done
	}
}

// sseEventSink 抽象 SSE 事件写入目标。两类实现:
//   - flushWriter:边读上游边把 Anthropic SSE 事件逐帧 flush 到客户端 TCP socket(实时流式);
//   - replayWriter:把整条转译结果蓄流进内存 bytes.Buffer,供上游断流重试场景攒全量再回放。
//
// 抽象该接口使 OpenAIChatSSEToAnthropicSSE 的转译逻辑与"写往哪里"解耦:
// 蓄流回放链路(writeNvidiaAnthropicStream)先用 replayWriter 在内存攒出完整 Anthropic SSE,
// 断流可丢弃本次 buffer 原账号重拉上游(≤5×5s),整条 ready 后再把 buffer 逐帧 flush 给客户端,
// 客户端在重试期间未收到任何字节,不会出现"半截内容冲突"。
type sseEventSink interface {
	writeEvent(event, data string)
	writeRaw(s string)
	flush()
}

type flushWriter struct {
	w       *bufio.Writer
	flusher http.Flusher
	reqID   string
	mu      sync.Mutex
	// firstByteHook 在首次向 w 写入前一次性调用(混合模式延迟 WriteHeader 场景)。
	// 用途:past-WriteHeader 的首字节到达时由 flushWriter 触发回调,保证 WriteHeader(200)
	// 先于任何响应体字节落盘。nil 时跳过。触发后置空避免重复调用。
	firstByteHook func()
	// deferred 延迟缓冲:混合模式延迟 WriteHeader 场景下,在"实质内容首字"到达前,
	// 框架帧(message_start + thinking 块 content_block_start)先进 deferred 暂存,不落盘不 flusher、
	// 也不触发 WriteHeader。首个实质内容(thinking_delta 或回放正文首帧)到达时调 flushDeferred 触发
	// firstByteHook(WriteHeader 200 + 刷头)+ 把 deferred 字节顺序写 w + flusher,转入直写模式。
	// 若上游在实质内容前就断流重试耗尽,dropDeferred 丢弃暂存帧,回写 503,不污染客户端流。
	// deferredActive=false(默认)时 writeEvent/writeRaw 直接走直写路径,行为与改动前一致(零回归)。
	deferred       bytes.Buffer
	deferredActive bool
}

// newFlushWriter 创建 flushWriter。若 flusher 非 nil, writeEvent/writeRaw/flush 会在 bufio.Flush
// 之后调 http.Flusher.Flush(), 把字节真正推到 TCP socket, 实现逐帧实时递送给客户端。
func newFlushWriter(reqID string, w *bufio.Writer, flusher ...http.Flusher) *flushWriter {
	fw := &flushWriter{w: w, reqID: reqID}
	if len(flusher) > 0 {
		fw.flusher = flusher[0]
	}
	return fw
}

// writeEvent 写一帧 Anthropic SSE。deferredActive 期间该帧进 deferred 暂存,不落盘;
// 否则触发 firstByteHook(首次)后直写 w + flusher。
func (f *flushWriter) writeEvent(event, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferredActive {
		writeSSEFrame(&f.deferred, event, data)
		return
	}
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil // 一次性触发,避免重复 WriteHeader
		hook()
	}
	writeSSEFrame(f.w, event, data)
	f.w.Flush() // 出 bufio 内部缓冲 → http.ResponseWriter
	if f.flusher != nil {
		f.flusher.Flush() // 出 http.ResponseWriter → socket
	}
}

func (f *flushWriter) writeRaw(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferredActive {
		f.deferred.WriteString(s)
		return
	}
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil
		hook()
	}
	f.w.WriteString(s)
	f.w.Flush()
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

// flushDeferred 把暂存的 deferred 字节一次性落盘:先触发 firstByteHook(WriteHeader 200 + 刷头),
// 再把 deferred 写 w + flusher,然后关闭延迟模式转入直写。幂等:多次调用只首次落盘 deferred。
// 用于混合模式首个实质内容(thinking_delta 或回放正文首帧)到达时确认 200 流,把其前的框架帧一并送出。
func (f *flushWriter) flushDeferred() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.deferredActive {
		return // 未进入延迟模式,无需处理
	}
	f.deferredActive = false
	if f.firstByteHook != nil {
		hook := f.firstByteHook
		f.firstByteHook = nil
		hook() // WriteHeader(200) + flusher.Flush 刷头
	}
	if f.deferred.Len() > 0 {
		f.w.Write(f.deferred.Bytes())
		f.w.Flush()
		if f.flusher != nil {
			f.flusher.Flush()
		}
		f.deferred.Reset()
	}
}

// dropDeferred 丢弃暂存的框架帧并关闭延迟模式,供上游在实质内容前断流重试耗尽时回 503 使用:
// 客户端从未收到任何字节(message_start 等框架帧未落盘),故可干净回写 503 overloaded_error。
func (f *flushWriter) dropDeferred() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deferredActive = false
	f.deferred.Reset()
	f.firstByteHook = nil
}

func (f *flushWriter) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deferredActive {
		return // 延迟模式:不刷盘,等 flushDeferred 转实写
	}
	f.w.Flush()
	if f.flusher != nil {
		f.flusher.Flush()
	}
}

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
	mu sync.Mutex
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
//   - tool_use 块:从未 live,remap 到 liveMaxIdx+1 后整块回放(防错误工具调用约束要求 tool 段走 replay)。
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
			if kind == "text" {
				if _, alreadyLive := prevState.liveIdxMap[upIdx]; alreadyLive {
					// 已 live 的 text 块:整块跳过(start/delta/stop 全跳),记录"跳过态"避免 stop 误回放
					remap[upIdx] = -1 // -1 哨兵:后续 delta/stop 见 -1 即跳过
					continue
				}
				// 尾随 text:尚未 live,remap 后回放
				ci := nextIdx
				remap[upIdx] = ci
				nextIdx++
				liveSink.writeEvent("content_block_start", rewriteContentBlockIndex(f.data, ci))
				continue
			}
			// tool_use:从未 live,remap 后整块回放
			if kind == "tool_use" {
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
	replay      *replayWriter
	live        *flushWriter
	toolSeen    bool // 已见 tool_use 块:此后所有块只 replay 不推 live(防错误工具调用)
	replayOnly  bool // 重试轮:全部只写 replay(压住思考重复外发)
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

// resumeSink 是重试轮的 sseEventSink:把上游(经 openAIChatSSEToAnthropicSSEInto 再次转译)
// 产的 Anthropic SSE 事件,按首轮客户端已收到的协议态(liveThinkingOpen/liveBodyOpenIdx/liveMaxUsedIdx)
// 过滤后实时推给 live,实现"续传不重发":
//
//   - message_start 全跳(首轮已发,不能重复发)。
//   - 思考段(content_block_start thinking / thinking_delta / signature_delta / content_block_stop thinking)
//     全跳——首轮实时推到客户端的思考是草稿,重试轮不重发(避免重复 message_start 外的 index 冲突)。
//   - tool_use 段:与首轮一样不实时推 live(只随 replayWriter 蓄流,功能正确性约束)。重试轮若上游又生成
//     tool_use,本 sink 把 tool_use 帧只写 replay 不推 live。但含工具回复的整条回放由调用方在成功时
//     replayBodyInto 处理(同首轮含工具链路)。纯 text 回复的重试轮不会有 tool_use。
//   - 正文 text 块:首个正文 content_block_start 到达时惰性补闭合客户端残留的未闭合块(先思考→再正文),
//     然后用 liveMaxUsedIdx+1 开新块(index 重映射),后续 text_delta/stop 改写 index 后实时推 live。
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
	// 这三个字段在 pending-轮内不被直接改写,而由 pend* 镜像字段在提交时回填(见下)。
	liveMaxUsedIdx   int
	liveThinkingOpen bool
	liveBodyOpenIdx  int

	// 本轮运行期态(reset 每轮清零):
	closedDangling   bool        // 惰性补闭合标志:首个正文 start/tool_use 前补一次;reset 复位
	toolSeen         bool        // 见过 tool_use:此后所有帧只 replay 不推 live
	messageStartSeen bool
	stopSent         bool
	indexMap         map[int]int // 本轮"上游 idx → 客户端 idx"重映射(成功快照回传给 replayFollowingInto)
	pending          bytes.Buffer // 本轮待提交给 live 的字节(补闭合帧 + 重映射正文 start/delta/stop);断流轮 reset 丢弃
	// pend* 是跨轮持久态的本轮镜像:轮内分配/补闭合改写 pend*,提交时回填到 liveMaxUsedIdx 等。
	// 失败轮 reset 后 pend* 重新从持久态初始化,故失败轮的 index 分配/块开闭全被丢弃,客户端态零变更。
	pendMaxIdx     int  // 本轮已分配的最大 index(从 liveMaxUsedIdx 起步)
	pendBodyOpenIdx int // 本轮 pending 中当前未闭合正文块 index(-1 表无)
	pendThinkingOpen bool // 本轮提交后 liveThinkingOpen 的目标值
}

func newResumeSink(live *flushWriter, replay *replayWriter, thinkingOpen bool, bodyOpenIdx, maxUsedIdx int) *resumeSink {
	return &resumeSink{
		live:             live,
		replay:           replay,
		liveMaxUsedIdx:   maxUsedIdx,
		liveThinkingOpen: thinkingOpen,
		liveBodyOpenIdx:  bodyOpenIdx,
		indexMap:         map[int]int{},
	}
}

// reset 跨重试轮复用前的复位:清本轮运行期态(pending/indexMap/closedDangling/toolSeen/尾帧标志 +
// pend* 镜像回退到持久值),保留 liveMaxUsedIdx/liveThinkingOpen/liveBodyOpenIdx——它们反映"截至上一轮
// 成功提交,客户端 live 上的协议态",本轮据此惰性补闭合并分配新块 index。
// 失败轮(未到 message_stop)的 pending/index 分配随 reset 全部丢弃,客户端态零变更。
func (r *resumeSink) reset() {
	r.indexMap = map[int]int{}
	r.closedDangling = false
	r.toolSeen = false
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
			// 工具块本身不实时推 live(功能正确性约束);锁定此后只 replay。
			// 但仍需惰性补闭合客户端残留的未闭合块(首轮实时推的思考/正文 text 若未闭合断流),
			// closeDanglingBlocks 在首个正文 start 或首个 tool_use start 时都会执行(幂等,只补一次)。
			r.closeDanglingBlocks()
			r.toolSeen = true
			return
		}
		// text 块:惰性补闭合 → 新 index 映射 → 改写 index 后写 pending(提交时落 live)
		if r.toolSeen {
			return
		}
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
		if r.toolSeen {
			return
		}
		// 思考段的 delta(thinking_delta/signature_delta)全跳;正文 text_delta 改写 index 后写 pending
		if deltaTypeForContentBlockDelta(data) != "text_delta" {
			return
		}
		upIdx := contentBlockIndex(data)
		newIdx, ok := r.indexMap[upIdx]
		if !ok {
			// 无对应开块的 delta:防御丢弃(不应出现——text 块必先有 start 建 map)
			return
		}
		writePendingEvent(&r.pending, "content_block_delta", rewriteContentBlockIndex(data, newIdx))
		return
	case "content_block_stop":
		if r.toolSeen {
			return
		}
		upIdx := contentBlockIndex(data)
		// 思考段 stop(index 0 且 indexMap 无该 idx):全跳
		newIdx, isBody := r.indexMap[upIdx]
		if !isBody {
			return
		}
		if r.pendBodyOpenIdx == newIdx {
			r.pendBodyOpenIdx = -1 // 本轮正文块已闭合,清回 -1
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

