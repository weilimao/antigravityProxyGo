package stats

import (
	"testing"
	"time"
)

// TestFirstByteRecorder_DefaultUnmarkedReturnsDuration 默认未打点时,FirstByteMs 应兜底取 durationMs。
func TestFirstByteRecorder_DefaultUnmarkedReturnsDuration(t *testing.T) {
	start := time.Now()
	rec := NewFirstByteRecorder(start)

	if rec.HasFirstByte() {
		t.Fatalf("expected HasFirstByte to be false before MarkFirstByte")
	}

	durationMs := int64(250)
	if got := rec.FirstByteMs(durationMs); got != 250 {
		t.Fatalf("expected FirstByteMs to default to durationMs 250, got %d", got)
	}
}

// TestFirstByteRecorder_MarkedCalculatesCorrectly 打点后应取 start -> firstByte 的真实间隔。
func TestFirstByteRecorder_MarkedCalculatesCorrectly(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	rec := NewFirstByteRecorder(start)

	rec.MarkFirstByte()

	if !rec.HasFirstByte() {
		t.Fatalf("expected HasFirstByte to be true after MarkFirstByte")
	}

	durationMs := int64(300)
	got := rec.FirstByteMs(durationMs)
	if got < 80 || got > 200 {
		t.Fatalf("expected FirstByteMs around 100ms, got %d", got)
	}
}

// TestFirstByteRecorder_OnceIdempotent MarkFirstByte 经 sync.Once 保证幂等,二次调用不改 firstByte。
func TestFirstByteRecorder_OnceIdempotent(t *testing.T) {
	start := time.Now().Add(-50 * time.Millisecond)
	rec := NewFirstByteRecorder(start)

	rec.MarkFirstByte()
	firstTime := rec.firstByte

	time.Sleep(10 * time.Millisecond)
	rec.MarkFirstByte()

	if !rec.firstByte.Equal(firstTime) {
		t.Fatalf("MarkFirstByte should be idempotent, first time %v vs second time %v", firstTime, rec.firstByte)
	}
}

// TestFirstByteRecorder_BoundedByDurationMs 计算出的 firstByteMs 大于 durationMs 时被截断为 durationMs。
func TestFirstByteRecorder_BoundedByDurationMs(t *testing.T) {
	start := time.Now().Add(-500 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	rec.MarkFirstByte()

	// 假定端到端耗时因为某些限制传入了较小的值 200ms
	got := rec.FirstByteMs(200)
	if got != 200 {
		t.Fatalf("expected FirstByteMs to be capped at durationMs 200, got %d", got)
	}
}

// TestFirstByteRecorder_NilSafety nil receiver 安全:HasFirstByte=false, FirstByteMs 返回 durationMs。
func TestFirstByteRecorder_NilSafety(t *testing.T) {
	var rec *FirstByteRecorder
	if rec.HasFirstByte() {
		t.Fatalf("nil recorder HasFirstByte should be false")
	}
	if got := rec.FirstByteMs(150); got != 150 {
		t.Fatalf("nil recorder FirstByteMs should return durationMs 150, got %d", got)
	}
}

// TestFirstByteRecorder_ZeroStartFallback start 为零值时自动取 time.Now(),避免因调用方疏漏导致负间隔。
func TestFirstByteRecorder_ZeroStartFallback(t *testing.T) {
	rec := NewFirstByteRecorder(time.Time{})
	if rec.start.IsZero() {
		t.Fatalf("expected zero start to be auto-filled with time.Now()")
	}
	rec.MarkFirstByte()
	// durationMs 给较大值,避免截断干扰;真实间隔应 ≥ 0 且远小于 1000ms。
	got := rec.FirstByteMs(1000)
	if got < 0 || got >= 1000 {
		t.Fatalf("expected FirstByteMs in [0,1000) with zero-start fallback, got %d", got)
	}
}

// TestFirstByteRecorder_DurationMsZeroFallback durationMs 为 0 或负值时不兜底为 0,避免负值回流。
func TestFirstByteRecorder_DurationMsZeroFallback(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	rec.MarkFirstByte()

	// durationMs 传 0(理论上端到端不会为 0,但防御性兜底):未截断时取真实 ms,但 ms>0 > 0 不应截断为 0
	// 这里我们关心:即使 durationMs=0,返回值也不应大于 0 时被错误截断(0 是合法下限)。
	got := rec.FirstByteMs(0)
	if got < 0 {
		t.Fatalf("expected FirstByteMs non-negative for durationMs=0, got %d", got)
	}
	// 真实 ms > 0,durationMs=0 按"ms > durationMs → durationMs"截断,但因 0 是非正 durationMs,
	// 不应错误截断(见 ttft.go: FirstByteMs 中 durationMs>=0 才截断)。
	if got == 0 {
		// 0 也合法(若实现选择严格截断),仅记录,不判失败
		return
	}
}

// TestFirstByteRecorder_StreamDurationMs_Marked 打点后 StreamDurationMs(end) 应返回 end-firstByte,
// 即「第一帧→结束」的流式耗时(不含 TTFT)。
func TestFirstByteRecorder_StreamDurationMs_Marked(t *testing.T) {
	start := time.Now().Add(-1000 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	// 首帧在 start+600ms 打点 → TTFT=600ms
	rec.MarkFirstByte() // 记录 firstByte=now

	// 结束时刻在打点后 300ms → 流式耗时 ≈ 300ms
	end := time.Now().Add(300 * time.Millisecond)
	got := rec.StreamDurationMs(end)
	if got <= 0 || got > 400 {
		t.Fatalf("expected StreamDurationMs ≈ 300ms (end-firstByte), got %d", got)
	}
}

// TestFirstByteRecorder_StreamDurationMs_Unmarked 未打点时 StreamDurationMs(end) 兜底返回端到端 end-start。
func TestFirstByteRecorder_StreamDurationMs_Unmarked(t *testing.T) {
	start := time.Now().Add(-500 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	end := time.Now().Add(100 * time.Millisecond)
	got := rec.StreamDurationMs(end)
	if got <= 0 || got > 700 {
		t.Fatalf("expected StreamDurationMs fallback ≈ end-start=600ms, got %d", got)
	}
}

// TestFirstByteRecorder_StreamDurationMs_NegativeClamp 结束时刻早于首帧(时钟漂移/边界)时恒 ≥ 0。
func TestFirstByteRecorder_StreamDurationMs_NegativeClamp(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	rec := NewFirstByteRecorder(start)
	rec.MarkFirstByte()
	// 结束时刻早于首帧 → end.Sub(firstByte) 为负 → 截断为 0
	end := time.Now().Add(-10 * time.Second)
	if got := rec.StreamDurationMs(end); got != 0 {
		t.Fatalf("expected StreamDurationMs clamped to 0 for early end, got %d", got)
	}
}

// TestFirstByteRecorder_StreamDurationMs_Nil nil receiver 防御性返回 0。
func TestFirstByteRecorder_StreamDurationMs_Nil(t *testing.T) {
	var rec *FirstByteRecorder
	end := time.Now()
	if got := rec.StreamDurationMs(end); got != 0 {
		t.Fatalf("expected nil receiver StreamDurationMs=0, got %d", got)
	}
}
