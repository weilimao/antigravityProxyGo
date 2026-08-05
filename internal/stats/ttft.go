package stats

import (
	"sync"
	"time"
)

// FirstByteRecorder 用于在「请求接入」与「首字回写」两个时刻打点,
// 计算首字响应延迟 (TTFT, Time To First Token / First Byte),
// 与端到端总耗时 (DurationMs) 语义独立。
//
// 语义约定:
//   - Start: 请求接入时刻(直连取 serveContext.startTime; NVIDIA 取 logCtx.StartTs; 远程取 handleRemote.startTime)。
//   - MarkFirstByte(): 首次向客户端写出实际内容/首字 chunk 的时刻(由各流式/非流式链路在 firstByteHook 处调用)。
//   - 未触发 MarkFirstByte() (如请求错误/中断/非流式未打点) 时, FirstByteMs(durationMs) 兜底取 durationMs。
//   - FirstByteMs ≤ durationMs 保障: 若由于时钟漂移等原因计算出的 firstByteMs > durationMs, 自动被 durationMs 截断。
type FirstByteRecorder struct {
	start        time.Time
	firstByte    time.Time
	hasFirstByte bool
	once         sync.Once
}

// NewFirstByteRecorder 创建一个指定开始时间的首字计时器
func NewFirstByteRecorder(start time.Time) *FirstByteRecorder {
	if start.IsZero() {
		start = time.Now()
	}
	return &FirstByteRecorder{
		start: start,
	}
}

// MarkFirstByte 记录首字/首块到达时刻(并发安全, sync.Once 保证仅记录首次)
func (r *FirstByteRecorder) MarkFirstByte() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.firstByte = time.Now()
		r.hasFirstByte = true
	})
}

// HasFirstByte 返回是否显式打过首字点
func (r *FirstByteRecorder) HasFirstByte() bool {
	if r == nil {
		return false
	}
	return r.hasFirstByte
}

// FirstByteMs 根据端到端总耗时 (durationMs, 毫秒) 计算首字响应延迟 (毫秒)。
// 若未触发 MarkFirstByte(), 兜底返回 durationMs (端到端总耗时)。
// 若计算结果 > durationMs, 兜底截断为 durationMs。
// 若计算结果 < 0, 兜底为 0。
func (r *FirstByteRecorder) FirstByteMs(durationMs int64) int64 {
	if r == nil || !r.hasFirstByte {
		if durationMs < 0 {
			return 0
		}
		return durationMs
	}
	ms := r.firstByte.Sub(r.start).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if durationMs >= 0 && ms > durationMs {
		ms = durationMs
	}
	return ms
}
