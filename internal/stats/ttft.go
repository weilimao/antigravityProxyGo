package stats

import (
	"sync"
	"time"
)

// FirstByteRecorder 用于在「请求接入」与「首字回写」两个时刻打点,
// 计算首字响应延迟 (TTFT, Time To First Token / First Byte),
// 与流式耗时 (StreamDurationMs, 第一帧→流结束) 语义独立。
//
// 语义约定:
//   - Start: 请求接入时刻(直连取 serveContext.startTime; NVIDIA 取 logCtx.StartTs; 远程取 handleRemote.startTime)。
//   - MarkFirstByte(): 首次向客户端写出实际内容/首字 chunk 的时刻(由各流式/非流式链路在 firstByteHook 处调用)。
//   - 未触发 MarkFirstByte() (如请求错误/中断/非流式未打点) 时, FirstByteMs(durationMs) 兜底取 durationMs。
//   - FirstByteMs ≤ durationMs 保障: 若由于时钟漂移等原因计算出的 firstByteMs > durationMs, 自动被 durationMs 截断。
//   - StreamDurationMs(end) 返回「首帧→end」的流式耗时, 即请求日志「耗时」列的语义(不含 TTFT);
//     未打点时兜底为端到端 end.Sub(start), 与改造前 DurationMs 数值一致。
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

// StreamDurationMs 返回「首帧 → end 结束时刻」的流式耗时(毫秒)。
// 这是请求日志「耗时」列的正确口径: 不含 TTFT(请求→首帧), 即 耗时 = 第一帧 → 流结束。
//   - 已触发 MarkFirstByte(): 返回 end.Sub(firstByte), 即第一帧到结束时刻的间隔。
//     若流在 <1ms 极短时间内完成 (如单包短响应), 保底返回 1ms 避免毫秒向下截断为 0。
//   - 未触发 MarkFirstByte()(请求错误/中断/非流式未打点): 兜底返回端到端 end.Sub(start),
//     与改造前 DurationMs 数值一致, 避免信息丢失。若耗时 <1ms 同样保底返回 1ms。
//   - nil receiver: 防御性兜底返回 0(无法计算)。
//   - 结束时刻早于首帧/开始时刻 (时钟漂移/异常): 截断为 0。
func (r *FirstByteRecorder) StreamDurationMs(end time.Time) int64 {
	if r == nil {
		return 0
	}
	if r.hasFirstByte {
		if end.Before(r.firstByte) {
			return 0
		}
		ms := end.Sub(r.firstByte).Milliseconds()
		if ms <= 0 {
			return 1
		}
		return ms
	}
	if !r.start.IsZero() {
		if end.Before(r.start) {
			return 0
		}
		ms := end.Sub(r.start).Milliseconds()
		if ms <= 0 {
			return 1
		}
		return ms
	}
	return 0
}
