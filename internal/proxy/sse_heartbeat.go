package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// defaultSSEHeartbeatInterval 保持与既有行为一致的 10s 空闲心跳周期。
	defaultSSEHeartbeatInterval = 10 * time.Second
	// defaultMaxSSEFrameBufferBytes 单条未闭合 SSE 事件的最大缓冲上限（256KB）。
	// 常规 SSE 事件均远小于此上限，可在帧边界完整后再一次性下发；
	// 极端超大单帧超限时将退化为透传，并在闭合前暂停心跳注入，兼顾延迟、内存与帧完整性。
	defaultMaxSSEFrameBufferBytes = 256 * 1024
)

var (
	sseHeartbeatPayload = []byte(": keep-alive (antigravity-proxy heartbeat)\n\n")
	sseDelimLF          = []byte("\n\n")
	sseDelimCRLF        = []byte("\r\n\r\n")
)

// sseBoundaryTracker 追踪已实际写往客户端的字节流是否处于完整的 SSE 事件边界（\n\n 或 \r\n\r\n）。
// 仅保留末尾最多 4 字节上下文，支持任意分包长度下的跨 chunk 分隔符闭合识别。
type sseBoundaryTracker struct {
	tail     [4]byte
	tailLen  int
	hasBytes bool
}

// Advance 更新已下发到下游的字节流尾状态。
func (t *sseBoundaryTracker) Advance(p []byte) {
	if len(p) == 0 {
		return
	}
	t.hasBytes = true
	if len(p) >= 4 {
		copy(t.tail[:], p[len(p)-4:])
		t.tailLen = 4
		return
	}
	var merged [8]byte
	copy(merged[:t.tailLen], t.tail[:t.tailLen])
	copy(merged[t.tailLen:], p)
	total := t.tailLen + len(p)
	if total > 4 {
		copy(t.tail[:], merged[total-4:total])
		t.tailLen = 4
		return
	}
	copy(t.tail[:total], merged[:total])
	t.tailLen = total
}

// Tail 返回当前保留的最近最多 4 个已下发字节，用于检测跨透传块边界的闭合分隔符。
func (t *sseBoundaryTracker) Tail() []byte {
	return t.tail[:t.tailLen]
}

// AtBoundary 判断当前下游字节流是否安全处于可插入 SSE 注释心跳的边界。
// 首包尚未下发（hasBytes == false）或累计下游字节恰结束于 \n\n / \r\n\r\n 时返回 true。
func (t *sseBoundaryTracker) AtBoundary() bool {
	if !t.hasBytes {
		return true
	}
	if t.tailLen >= 2 && t.tail[t.tailLen-2] == '\n' && t.tail[t.tailLen-1] == '\n' {
		return true
	}
	if t.tailLen == 4 &&
		t.tail[0] == '\r' && t.tail[1] == '\n' &&
		t.tail[2] == '\r' && t.tail[3] == '\n' {
		return true
	}
	return false
}

// findLastSSEFrameBoundary 返回 buf 中最后一个完整 SSE 事件分隔符（\n\n 或 \r\n\r\n）结束后的偏移。
// 若 buf 中尚无任何完整分隔符，则返回 0。
func findLastSSEFrameBoundary(buf []byte) int {
	best := 0
	if idx := bytes.LastIndex(buf, sseDelimLF); idx >= 0 {
		if end := idx + len(sseDelimLF); end > best {
			best = end
		}
	}
	if idx := bytes.LastIndex(buf, sseDelimCRLF); idx >= 0 {
		if end := idx + len(sseDelimCRLF); end > best {
			best = end
		}
	}
	return best
}

// findCrossBoundaryClosurePrefix 当下游曾因超大事件直写而停留在非边界状态时，
// 检查 pending 开头的最多 3 个字节能否与 tail 拼接出完整的 \n\n 或 \r\n\r\n 闭合分隔符。
// 返回需要立刻下发以闭合前置帧的前缀长度（1..3），无法立即闭合则返回 0。
func findCrossBoundaryClosurePrefix(tail, pending []byte) int {
	if len(tail) == 0 || len(pending) == 0 {
		return 0
	}
	prefixLen := len(pending)
	if prefixLen > 3 {
		prefixLen = 3
	}
	var joined [7]byte
	copy(joined[:], tail)
	copy(joined[len(tail):], pending[:prefixLen])
	window := joined[:len(tail)+prefixLen]

	bestEnd := 0
	if idx := bytes.Index(window, sseDelimLF); idx >= 0 {
		end := idx + len(sseDelimLF)
		if end > len(tail) {
			bestEnd = end
		}
	}
	if idx := bytes.Index(window, sseDelimCRLF); idx >= 0 {
		end := idx + len(sseDelimCRLF)
		if end > len(tail) && (bestEnd == 0 || end < bestEnd) {
			bestEnd = end
		}
	}
	if bestEnd <= len(tail) {
		return 0
	}
	return bestEnd - len(tail)
}

// sseStreamWriterConfig 定义 sseStreamWriter 的可注入依赖与参数。
type sseStreamWriterConfig struct {
	writer            io.Writer
	flusher           http.Flusher
	heartbeatInterval time.Duration
	maxBufferBytes    int
	onPayloadEmitted  func([]byte)
	nowFn             func() time.Time
}

// sseStreamWriter 为 SSE 流式响应提供帧边界感知写入与并发安全的心跳保活控制。
// 核心契约：
//  1. 普通 SSE 事件在缓冲区凑齐 \n\n 或 \r\n\r\n 完整边界后再原子下发并 Flush；
//  2. 未发送任何业务字节前或下游始终停在上一帧完整边界时，按周期写入 `: keep-alive...\n\n`；
//  3. 若单条未闭合事件超过 maxBufferBytes 触发保护性透传，则在下一个闭合边界到达前暂停心跳注入，
//     彻底根除心跳注释截断半截 JSON 导致的 `proto: unexpected EOF`。
type sseStreamWriter struct {
	writer            io.Writer
	flusher           http.Flusher
	heartbeatInterval time.Duration
	maxBufferBytes    int
	onPayloadEmitted  func([]byte)
	nowFn             func() time.Time

	mu          sync.Mutex
	pending     []byte
	tracker     sseBoundaryTracker
	lastWriteAt time.Time
	stopped     bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newSSEStreamWriter(cfg sseStreamWriterConfig) *sseStreamWriter {
	interval := cfg.heartbeatInterval
	if interval <= 0 {
		interval = defaultSSEHeartbeatInterval
	}
	maxBuf := cfg.maxBufferBytes
	if maxBuf <= 0 {
		maxBuf = defaultMaxSSEFrameBufferBytes
	}
	nowFn := cfg.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	return &sseStreamWriter{
		writer:            cfg.writer,
		flusher:           cfg.flusher,
		heartbeatInterval: interval,
		maxBufferBytes:    maxBuf,
		onPayloadEmitted:  cfg.onPayloadEmitted,
		nowFn:             nowFn,
		lastWriteAt:       nowFn(),
		stopCh:            make(chan struct{}),
	}
}

// Start 启动后台心跳协程，ctx 结束或调用 Stop 时安全退出。
func (w *sseStreamWriter) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	w.wg.Add(1)
	go w.heartbeatLoop(ctx)
}

// Stop 停止后台心跳协程并等待其退出，保证无残留并发写入。
func (w *sseStreamWriter) Stop() {
	w.mu.Lock()
	alreadyStopped := w.stopped
	w.stopped = true
	w.mu.Unlock()

	if !alreadyStopped {
		close(w.stopCh)
	}
	w.wg.Wait()
}

func (w *sseStreamWriter) heartbeatLoop(ctx context.Context) {
	defer w.wg.Done()

	timer := time.NewTimer(w.heartbeatInterval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			nextWait, keepGoing := w.trySendHeartbeat()
			if !keepGoing {
				return
			}
			timer.Reset(nextWait)
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (w *sseStreamWriter) trySendHeartbeat() (time.Duration, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return 0, false
	}

	elapsed := w.nowFn().Sub(w.lastWriteAt)
	if elapsed < w.heartbeatInterval {
		remaining := w.heartbeatInterval - elapsed
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		return remaining, true
	}

	if !w.tracker.AtBoundary() {
		// 下游正处于超大事件透传后的未闭合帧中途：暂停本次心跳，避免截断 JSON。
		return w.heartbeatInterval, true
	}

	_, err := w.writer.Write(sseHeartbeatPayload)
	if err != nil {
		return 0, false
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	w.lastWriteAt = w.nowFn()
	return w.heartbeatInterval, true
}

// WriteUpstreamChunk 接收上游读取的原始流字节块，按 SSE 帧边界切分并安全写出完整事件。
// 若向下游写入失败（客户端断连），返回 error。
func (w *sseStreamWriter) WriteUpstreamChunk(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	var emittedChunks [][]byte

	w.mu.Lock()
	w.pending = append(w.pending, chunk...)

	// 若前置超大透传帧正等待跨块分隔符收尾，先仅放出闭合该帧所需的 1..3 字节。
	if !w.tracker.AtBoundary() {
		if closeLen := findCrossBoundaryClosurePrefix(w.tracker.Tail(), w.pending); closeLen > 0 {
			head := append([]byte(nil), w.pending[:closeLen]...)
			if err := w.writeDownstreamLocked(head); err != nil {
				w.mu.Unlock()
				return err
			}
			emittedChunks = append(emittedChunks, head)
			w.shiftPendingLocked(closeLen)
		}
	}

	// 优先把所有已抵达的完整 SSE 事件一次性切出下发，半帧留待下次 chunk 补齐。
	if splitEnd := findLastSSEFrameBoundary(w.pending); splitEnd > 0 {
		complete := append([]byte(nil), w.pending[:splitEnd]...)
		if err := w.writeDownstreamLocked(complete); err != nil {
			w.mu.Unlock()
			return err
		}
		emittedChunks = append(emittedChunks, complete)
		w.shiftPendingLocked(splitEnd)
	}

	// 若剩余半帧仍未见完整分隔符且超出 256KB 上限，触发透传降级并置为非边界态暂停心跳。
	if len(w.pending) > w.maxBufferBytes {
		overflow := append([]byte(nil), w.pending...)
		if err := w.writeDownstreamLocked(overflow); err != nil {
			w.mu.Unlock()
			return err
		}
		emittedChunks = append(emittedChunks, overflow)
		w.pending = w.pending[:0]
	}
	w.mu.Unlock()

	if w.onPayloadEmitted != nil {
		for _, emitted := range emittedChunks {
			w.onPayloadEmitted(emitted)
		}
	}
	return nil
}

// FlushTrailing 在上游流结束前停止心跳并刷出缓冲区中尚未以分隔符结尾的残留字节。
func (w *sseStreamWriter) FlushTrailing() error {
	w.Stop()

	var trailing []byte

	w.mu.Lock()
	if len(w.pending) > 0 {
		trailing = append([]byte(nil), w.pending...)
		if err := w.writeDownstreamLocked(trailing); err != nil {
			w.pending = w.pending[:0]
			w.mu.Unlock()
			return err
		}
		w.pending = w.pending[:0]
	}
	w.mu.Unlock()

	if len(trailing) > 0 && w.onPayloadEmitted != nil {
		w.onPayloadEmitted(trailing)
	}
	return nil
}

func (w *sseStreamWriter) writeDownstreamLocked(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if _, err := w.writer.Write(data); err != nil {
		return err
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	w.tracker.Advance(data)
	w.lastWriteAt = w.nowFn()
	return nil
}

func (w *sseStreamWriter) shiftPendingLocked(consumed int) {
	if consumed <= 0 {
		return
	}
	if consumed >= len(w.pending) {
		w.pending = w.pending[:0]
		return
	}
	rem := len(w.pending) - consumed
	copy(w.pending[:rem], w.pending[consumed:])
	w.pending = w.pending[:rem]
}
