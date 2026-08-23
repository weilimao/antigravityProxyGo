// Package eventsgate — Wails 事件节流派发门
//
// 设计背景:
//   原代码中 AddLog → EventsEmit("logs:batch")、SetOnPayloadUpdate →
//   EventsEmit("stats-updated")、accountMgr 回调 → EventsEmit("accounts*")
//   等所有事件最终都会 w32.PostMessage(wmInvokeCallback) 投到主线程
//   LockOSThread UI 消息队列。当主线程被 WebView2 COM 回调占用、加上代理
//   高峰期日志/统计事件每秒可能上百次,主线程消息队列会被迅速塞满,
//   进一步更难腾出空处理 WindowShow/Hide/Close,形成"卡死体感"。
//
// 治理策略:
//   - 同名事件节流: 默认 1s 内最多派发 1 次;期间到达的新事件覆盖
//     旧 payload,到点一次性派发"最新一帧"。语义对齐 stats/logs 这类
//     "订阅最新状态"而非"逐条历史"的消费场景。
//   - 不可见即丢弃: 窗口不可见时默认不重投,等下次可见后由
//     SetWindowVisible(true) 主动触发一次补偿(已在 app.go 实现)。
//   - AlwaysKeep 名单: 对于"窗口隐藏也必须送达"的关键事件(罕见),
//     可在 New 时通过 WithAlwaysKeep 标注,绕开"不可见丢弃"规则。
//
// API:
//   gate := eventsgate.New(isVisibleFn)
//   gate.Emit("stats-updated", payload)
//   gate.Flush()   // 进程退出前手动冲刷待发事件(可选)
//
// 并发安全。
package eventsgate

import (
	"sync"
	"time"
)

const (
	// DefaultMinInterval 同名事件最小派发间隔
	DefaultMinInterval = 1 * time.Second
)

// EmitFunc 真正的派发通道。由 main 包注入,通常为:
//   func(name string, payload any) { wailsRuntime.EventsEmit(ctx, name, payload) }
// 必须**异步、非阻塞**;Gate 不对此再做 go。
type EmitFunc func(name string, payload any)

// IsVisibleFn 返回主窗口当前是否可见。返回 false 时,非 AlwaysKeep 事件直接丢弃。
type IsVisibleFn func() bool

// Gate 节流派发门。
type Gate struct {
	emit        EmitFunc
	isVisible   IsVisibleFn
	minInterval time.Duration

	mu          sync.Mutex
	lastSentAt  map[string]time.Time
	pending     map[string]pendingEvent
	alwaysKeep  map[string]bool
}

type pendingEvent struct {
	payload any
	timer   *time.Timer
}

// Option 构造选项
type Option func(*Gate)

// WithMinInterval 覆盖默认 1s 节流
func WithMinInterval(d time.Duration) Option {
	return func(g *Gate) { g.minInterval = d }
}

// WithAlwaysKeep 标注指定事件名:即便窗口不可见也强制派发
func WithAlwaysKeep(names ...string) Option {
	return func(g *Gate) {
		for _, n := range names {
			g.alwaysKeep[n] = true
		}
	}
}

// New 创建节流派发门。emit 与 isVisible 都不许为 nil(宁可传入保守默认)。
func New(emit EmitFunc, isVisible IsVisibleFn, opts ...Option) *Gate {
	if emit == nil {
		emit = func(string, any) {}
	}
	if isVisible == nil {
		isVisible = func() bool { return true } // 缺省认为可见,不丢帧
	}
	g := &Gate{
		emit:        emit,
		isVisible:   isVisible,
		minInterval: DefaultMinInterval,
		lastSentAt:  make(map[string]time.Time),
		pending:     make(map[string]pendingEvent),
		alwaysKeep:  make(map[string]bool),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Emit 节流派发:name 事件 1s 内多次调用,仅第一次直发,其余合并为"最新一帧"在窗口期结束时发送。
// 不可见 + 非 AlwaysKeep 的事件直接丢弃。
func (g *Gate) Emit(name string, payload any) {
	// 不可见且非强保 → 直接丢
	if !g.alwaysKeep[name] && !g.isVisible() {
		return
	}

	g.mu.Lock()
	now := time.Now()
	last, seen := g.lastSentAt[name]
	elapsed := now.Sub(last)

	sendNow := func() {
		g.lastSentAt[name] = now
		g.mu.Unlock()
		g.emit(name, payload)
	}

	if !seen || elapsed >= g.minInterval {
		// 立即通道
		sendNow()
		return
	}

	// 需要合并:更新 payload,挂/重置 timer
	if p, ok := g.pending[name]; ok && p.timer != nil {
		p.timer.Stop()
	}
	remaining := g.minInterval - elapsed
	timer := time.AfterFunc(remaining, func() {
		g.flushPending(name)
	})
	g.pending[name] = pendingEvent{payload: payload, timer: timer}
	g.mu.Unlock()
}

// flushPending 由 timer 触发,真正派发延期事件
func (g *Gate) flushPending(name string) {
	g.mu.Lock()
	p, ok := g.pending[name]
	if ok {
		delete(g.pending, name)
		g.lastSentAt[name] = time.Now()
	}
	g.mu.Unlock()
	if ok {
		// 再次校验可见性:可能在等待窗口内被隐藏
		if !g.alwaysKeep[name] && !g.isVisible() {
			return
		}
		g.emit(name, p.payload)
	}
}

// Flush 立即冲刷所有挂起的延期事件(用于进程退出前最后一次同步)。不阻塞。
func (g *Gate) Flush() {
	g.mu.Lock()
	names := make([]string, 0, len(g.pending))
	for n := range g.pending {
		names = append(names, n)
	}
	g.mu.Unlock()
	for _, n := range names {
		g.flushPending(n)
	}
}
