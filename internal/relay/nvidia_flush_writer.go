package relay

import (
	"bufio"
	"bytes"
	"net/http"
	"sync"
)

// nvidia_flush_writer.go: sseEventSink 的实时 flushWriter 实现, 从 nvidia_translate_buffer.go 抽离。
//
// flushWriter 把 Anthropic SSE 事件逐帧 flush 到客户端 TCP socket: bufio.Flush 出内部缓冲到
// http.ResponseWriter, 再 http.Flusher.Flush 推到 socket。deferred 延迟缓冲供混合模式延迟
// WriteHeader 场景: 框架帧先暂存, 首个实质内容到达时 flushDeferred 触发 WriteHeader(200)+刷头;
// 上游在实质内容前断流则 dropDeferred 丢弃暂存帧干净回 503。仅作物理搬移, 逐行等价。

type flushWriter struct {
	w       *bufio.Writer
	flusher http.Flusher
	reqID   string
	mu      sync.Mutex
	// firstByteHook 在首次向 w 写入前一次性调用(混合模式延迟 WriteHeader 场景)。
	// 用途:past-WriteHeader 的首字节到达时由 flushWriter 触发回调,保证 WriteHeader(200)
	// 先于任何响应体字节落盘。nil 时跳过。触发后置空避免重复调用。
	firstByteHook func()
	// firstUpstreamByteHook 在接受到上游响应的首个字节时一次性触发(TTFT 打点)。
	// 与 firstByteHook 的语义差异:mark 的是「上游首字时刻」,而非「客户端首字节时刻」。
	// 混合模式(deferred 延迟缓冲)下客户端首字可能在整条流 ready 后回放才落盘,已被
	// tee 用 flushDeferred 触发 firstByteHook 提前打了客户端点;而本 hook 在 openAIChatSSE
	// →AnthropicSSE 转译主循环把第一帧写入 sink 时触发,反映上游真实首字延迟,与客户端
	// 缓冲/回放逻辑解耦。nil 时跳过。触发后置空避免重复调用。
	firstUpstreamByteHook func()
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
	if f.firstUpstreamByteHook != nil {
		hook := f.firstUpstreamByteHook
		f.firstUpstreamByteHook = nil // 一次性触发,避免重复打点
		hook()
	}
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
	if f.firstUpstreamByteHook != nil {
		hook := f.firstUpstreamByteHook
		f.firstUpstreamByteHook = nil // 一次性触发,避免重复打点
		hook()
	}
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
