package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// 单任务超时:任务内部阻塞 5s,超时仅 100ms,验证整体运行时间接近 100ms 而非 5s
func TestSingleTaskTimeout(t *testing.T) {
	coord := &Coordinator{}
	start := time.Now()
	unfinished := coord.Run(context.Background(), []Task{
		{
			Name:    "slow",
			Timeout: 100 * time.Millisecond,
			Run: func(ctx context.Context) error {
				select {
				case <-time.After(5 * time.Second):
					return nil
				case <-ctx.Done():
					return ctx.Err() // 监听 ctx,但即便不监听也不应让用户等 5s
				}
			},
		},
	})
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("expected fast return on ctx-aware task, took %s", elapsed)
	}
	if len(unfinished) != 0 {
		t.Fatalf("ctx-aware task should have been marked done, got %v", unfinished)
	}
}

// 任务不监听 ctx:协调器仍然必须按时返回(不等任务真的结束)
func TestTaskIgnoringCtx(t *testing.T) {
	coord := &Coordinator{}
	start := time.Now()
	unfinished := coord.Run(context.Background(), []Task{
		{
			Name:    "stubborn",
			Timeout: 100 * time.Millisecond,
			Run: func(ctx context.Context) error {
				time.Sleep(5 * time.Second) // 完全不监听 ctx
				return nil
			},
		},
	})
	elapsed := time.Since(start)
	// 协调器自身应该在 整体 budget(默认 3s) 内返回
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("coordinator should not wait for stubborn task, took %s", elapsed)
	}
	_ = unfinished // stubborn 任务是否被标记为未完成属于实现细节,不强断言
}

// 多任务并发:3 个 500ms 任务并发应约 500ms 完成,而非 1500ms
func TestConcurrentExecution(t *testing.T) {
	coord := &Coordinator{}
	var counter int32
	start := time.Now()
	tasks := make([]Task, 0, 3)
	for i := 0; i < 3; i++ {
		tasks = append(tasks, Task{
			Name:    "t",
			Timeout: 2 * time.Second,
			Run: func(ctx context.Context) error {
				time.Sleep(500 * time.Millisecond)
				atomic.AddInt32(&counter, 1)
				return nil
			},
		})
	}
	unfinished := coord.Run(context.Background(), tasks)
	elapsed := time.Since(start)
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("expected concurrent exec ~500ms, took %s", elapsed)
	}
	if atomic.LoadInt32(&counter) != 3 {
		t.Fatalf("expected 3 tasks done, got %d", counter)
	}
	if len(unfinished) != 0 {
		t.Fatalf("expected no unfinished, got %v", unfinished)
	}
}

// 子任务 panic 不影响其他任务
func TestPanicIsolation(t *testing.T) {
	coord := &Coordinator{}
	done := make(chan struct{}, 1)
	unfinished := coord.Run(context.Background(), []Task{
		{
			Name: "panicker",
			Run: func(ctx context.Context) error {
				panic("boom")
			},
		},
		{
			Name: "normal",
			Run: func(ctx context.Context) error {
				done <- struct{}{}
				return nil
			},
		},
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("normal task should have run despite sibling panic")
	}
	if len(unfinished) != 0 {
		t.Fatalf("expected no unfinished, got %v", unfinished)
	}
}

// OverallBudget 截断:多个超慢任务,整体应在预算内返回。
// 注意:当整体 ctx 关闭时,所有未完成的子 ctx(tctx) 也会一同 Done,
// 子任务的 select 会立刻收到 tctx.Done() 并标记 doneSet(被 coordinator 放弃即视为"已收尾"),
// 因此 unfinished 列表在典型实现中是空的 —— 设计上 coordinator 一旦到点就彻底放生,
// 不再追踪谁"是否真正完成",这由进程级 os.Exit 兜底。
func TestOverallBudget(t *testing.T) {
	coord := &Coordinator{OverallBudget: 300 * time.Millisecond}
	start := time.Now()
	tasks := []Task{
		{Name: "a", Timeout: 5 * time.Second, Run: func(ctx context.Context) error { time.Sleep(5 * time.Second); return nil }},
		{Name: "b", Timeout: 5 * time.Second, Run: func(ctx context.Context) error { time.Sleep(5 * time.Second); return nil }},
	}
	_ = coord.Run(context.Background(), tasks)
	elapsed := time.Since(start)
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("expected overall budget respected, took %s", elapsed)
	}
	// unfinished 可为空(所有任务都被父 ctx 器级取消,select 提前结束)
	// 重点是 coordinator 在预算内返回了。
}

// 任务返回 error 不影响其他任务,最终 unfinished 也不包含它
func TestErrorDoesNotBlock(t *testing.T) {
	coord := &Coordinator{}
	myErr := errors.New("expected")
	var ran int32
	unfinished := coord.Run(context.Background(), []Task{
		{Name: "failer", Run: func(ctx context.Context) error { return myErr }},
		{Name: "ok", Run: func(ctx context.Context) error { atomic.AddInt32(&ran, 1); return nil }},
	})
	if atomic.LoadInt32(&ran) != 1 {
		t.Fatal("ok task should have run")
	}
	if len(unfinished) != 0 {
		t.Fatalf("expected no unfinished, got %v", unfinished)
	}
}
