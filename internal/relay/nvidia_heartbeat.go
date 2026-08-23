package relay

import (
	"context"
	"sync"
	"time"
)

// nvidia_heartbeat.go: NVIDIA Anthropic 流式链路上的 SSE ping 心跳看门狗。
//
// 设计背景:
//
//	Claude Code SDK 在「tool_use 期间长时间收不到任何字节」时会判定流被中断,
//	启动重试/恢复流程,并把当前 goal 标为 "not yet met… continuing"。
//	OpenCode / 其它客户端没有这个秒级看门狗,所以同一上游表现"正常"。
//
//	kimi-k3 在 NVIDIA NIM 上推理阶段 + tool 段会天然出现 10s+ 静默窗口
//	(实测 reasoning 结束到 tool_start 之间出现 11s 的空窗;并且 tool 参数生成
//	也可能分段停顿),必须主动注入合法空帧重置 Claude Code 的看门狗。
//
// 做法:
//
//	由翻译主循环(openAIChatSSEToAnthropicSSEIntoPinned)在每次「真实业务帧」写入时
//	调用本模块 MarkActivity() 记录时间戳。一旦距上次活动时间超过 heartbeatIdle,
//	看门狗即调用 sink.pingFrame() 发出 Anthropic 规范定义的空 ping 事件,
//	Claude Code 收到 ping 后重置其内部 inactivity timer,不会再判定「工具被中断」。
//
// pingFrame 不进 replay(见 replayWriter.pingFrame 的 no-op 实现),只用于 live,
// 因此蓄流缓冲 / 完整性判定逻辑零侵入。

const (
	// heartbeatIdle 是判定「上游长时间未产出新字节」的闲置阈值。
	// 实测 kimi-k3 reasoning→tool_start 窗口约 11s,故选 3s,
	// 即便出现数个连续 slivered thinking_delta 也足以触发心跳。
	heartbeatIdle = 3 * time.Second
	// heartbeatCheck 是看门狗采样周期。要比 idle 短以保证至少 idle 秒内能触发。
	heartbeatCheck = 1 * time.Second
)

// heartbeatWatchdog 以 pingFrame 形式向 sink 注入 SSE ping 空帧。
// 仅在「翻译层已经进入一个或多个 content_block(thinking/text/tool_use)」后才活跃;
// 上游连首个 delta 都没下发的纯握手期不注入,避免先发 ping 再发 message_start 的乱序。
type heartbeatWatchdog struct {
	sink      sseEventSink
	mu        sync.Mutex
	lastAt    time.Time
	active    bool // 是否已进入"有实质 content_block"阶段
	stopCh    chan struct{}
	stoppedCh chan struct{}

	// 测试钩子(可选)
	nowFn  func() time.Time
	pingCb func() // 每次真正发出 ping 时触发(仅测试用)
}

// newHeartbeatWatchdog 构造看门狗。sink 不可为 nil。
// 调用 Start() 才会真正跑起;翻译主循环以 defer watchdog.Stop() 释放。
func newHeartbeatWatchdog(sink sseEventSink) *heartbeatWatchdog {
	return &heartbeatWatchdog{
		sink:      sink,
		lastAt:    time.Now(),
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// markContentBlockEntry 通知看门狗「已进入实质 content_block 阶段」。
// 只应在翻译主循环里调用:第一次 emitTextDelta / emitThinkingDelta / emitToolCallDelta
// 真正 writeEvent 后调用一次,之后重复调用无副作用。
func (w *heartbeatWatchdog) markContentBlockEntry() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		w.active = true
		w.lastAt = w.now()
	}
}

// markBeat 在每次 sink 真实写入新数据(thinking_delta/text_delta/input_json_delta)
// 后被调用,刷新"最后活动时间"。简单对齐所有 sink 入口,不必逐一 hook。
func (w *heartbeatWatchdog) markBeat() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active {
		w.lastAt = w.now()
	}
}

// start 启动看门狗协程,周期检查是否超时空闲。
func (w *heartbeatWatchdog) start(ctx context.Context) {
	if w == nil {
		return
	}
	go func() {
		defer close(w.stoppedCh)
		ticker := time.NewTicker(heartbeatCheck)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.maybePing()
			}
		}
	}()
}

// stop 停掉看门狗(幂等)。翻译函数返回前必须调用,否则协程泄漏。
func (w *heartbeatWatchdog) stop() {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
	<-w.stoppedCh
}

// maybePing 决定此刻是否需要注入一帧 ping。
func (w *heartbeatWatchdog) maybePing() {
	w.mu.Lock()
	if !w.active {
		w.mu.Unlock()
		return
	}
	idle := w.now().Sub(w.lastAt)
	w.mu.Unlock()
	if idle < heartbeatIdle {
		return
	}
	// 先把 lastAt 拨到当前,然后再发起写。顺序反过来会出现"上一帧刚写就立刻又发 ping"
	// 的世纪时间戳竞争:其它 goroutine 此时也可能在写 sink。
	w.markBeat()
	// 真实把 ping 写出去。
	w.sink.pingFrame()
	if w.pingCb != nil {
		w.pingCb()
	}
}

// now 返回当前时间,测试时可用 nowFn 替换。
func (w *heartbeatWatchdog) now() time.Time {
	if w.nowFn != nil {
		return w.nowFn()
	}
	return time.Now()
}
