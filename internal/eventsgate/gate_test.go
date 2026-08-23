package eventsgate

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 1s 内同名事件仅派发 1 次(首次直发),后续合并为"最新一帧"
func TestThrottleSameName(t *testing.T) {
	var mu sync.Mutex
	emitted := make([]any, 0)
	gate := New(func(name string, p any) {
		mu.Lock()
		emitted = append(emitted, p)
		mu.Unlock()
	}, func() bool { return true }, WithMinInterval(100*time.Millisecond))

	// 第一次直发
	gate.Emit("stats", 1)
	// 短间隔内 5 次连续 Emit
	for i := 2; i <= 6; i++ {
		gate.Emit("stats", i)
	}

	// 等节流窗口结束
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// 期望:第 1 次直发 = 1;中间 5 次合并为最后一次 = 6
	if len(emitted) != 2 {
		t.Fatalf("expected 2 emissions (first + last merged), got %d (%v)", len(emitted), emitted)
	}
	if emitted[0] != 1 {
		t.Fatalf("first emission should be payload 1, got %v", emitted[0])
	}
	if emitted[1] != 6 {
		t.Fatalf("merged emission should be latest payload 6, got %v", emitted[1])
	}
}

// 不可见时直接丢弃
func TestInvisibleDrop(t *testing.T) {
	var count int32
	visible := false
	gate := New(func(name string, p any) {
		atomic.AddInt32(&count, 1)
	}, func() bool { return visible })

	gate.Emit("stats", "x")
	gate.Emit("stats", "y")
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&count) != 0 {
		t.Fatalf("expected 0 emissions while invisible, got %d", count)
	}
}

// AlwaysKeep 即便不可见也派发
func TestAlwaysKeepBypassesInvisible(t *testing.T) {
	var count int32
	visible := false
	gate := New(func(name string, p any) {
		atomic.AddInt32(&count, 1)
	}, func() bool { return visible }, WithAlwaysKeep("critical"))

	gate.Emit("critical", "x")
	if atomic.LoadInt32(&count) != 1 {
		t.Fatalf("alwaysKeep should be emitted, got %d", count)
	}
}

// 不同事件名独立节流互不影响
func TestIndependentNames(t *testing.T) {
	var statsCount, logsCount int32
	gate := New(func(name string, p any) {
		switch name {
		case "stats":
			atomic.AddInt32(&statsCount, 1)
		case "logs":
			atomic.AddInt32(&logsCount, 1)
		}
	}, func() bool { return true }, WithMinInterval(80*time.Millisecond))

	gate.Emit("stats", 1)
	gate.Emit("logs", 1)
	// 不同名,各自直发

	if atomic.LoadInt32(&statsCount) != 1 || atomic.LoadInt32(&logsCount) != 1 {
		t.Fatalf("independent names should both fire, got stats=%d logs=%d", statsCount, logsCount)
	}
}

// Flush 强制派发所有挂起
func TestFlush(t *testing.T) {
	var mu sync.Mutex
	var lastPayload any
	gate := New(func(name string, p any) {
		mu.Lock()
		lastPayload = p
		mu.Unlock()
	}, func() bool { return true }, WithMinInterval(time.Hour)) // 极长间隔,只靠 Flush

	gate.Emit("stats", "first")
	gate.Emit("stats", "second")
	gate.Emit("stats", "third")

	gate.Flush()
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if lastPayload != "third" {
		t.Fatalf("expected flushed payload=third, got %v", lastPayload)
	}
}

// 1000 并发 Emit 无数据竞争,最终派发次数应 << 1000
func TestConcurrentEmitSafe(t *testing.T) {
	var count int32
	gate := New(func(name string, p any) {
		atomic.AddInt32(&count, 1)
	}, func() bool { return true }, WithMinInterval(50*time.Millisecond))

	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			gate.Emit("stats", v)
		}(i)
	}
	wg.Wait()

	time.Sleep(200 * time.Millisecond)
	got := atomic.LoadInt32(&count)
	if got == 0 {
		t.Fatal("no emissions at all")
	}
	if got > 30 {
		t.Fatalf("expected throttle to collapse 1000 emits to a handful, got %d", got)
	}
}
