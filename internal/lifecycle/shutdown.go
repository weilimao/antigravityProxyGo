// Package lifecycle shutdown.go — 应用退出阶段任务并发协调器
//
// 设计背景:
//   原 shutdown() 在主线程串行同步执行 10+ 步清理(tray/scheduler/monitor/relay/
//   proxyEngine/session.SaveToDisk/account.Stop*/PatchAll/corelog/sigcache/diagserver),
//   任一步遇网络/IO/调度阻塞都会拖死整个退出链路,叠加 wails.Run 的退出回调本身
//   是同步等待的,直接导致"右键退出后任务管理器残留、双击无反应、不能隐藏"。
//
// 治理策略:
//   - 关闭动作拆分:阶段1(并发停所有"接收新工作"端点) + 阶段2(串行落盘/收尾)
//   - 每个任务独立 context.WithTimeout,单任务超时不再拖累其他任务
//   - 协调器本身还有整体 Budget,超时即为"该跑跑、该丢丢",由外层 5s
//     watchdog 兜底 os.Exit(0)
//   - 任何子任务 panic 仅记录,不向上传播
package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Task 单个退出任务。
type Task struct {
	// Name 仅用于日志/调试。
	Name string
	// Timeout 该任务的最长允许执行时长;0 表示用 DefaultTaskTimeout。
	Timeout time.Duration
	// Run 实际退出逻辑。ctx 已带超时,任务自己应当监听 ctx.Done()。
	// 返回 error 不影响其他任务,仅用于记录。
	Run func(ctx context.Context) error
}

// Coordinator 关闭协调器。零值即可用。
type Coordinator struct {
	// OverallBudget 整体预算,超过即放弃剩余未完成任务。0 表示用 DefaultOverallBudget。
	OverallBudget time.Duration
	// Logf 可选日志钩子;nil 则静默。
	Logf func(format string, args ...interface{})
}

const (
	// DefaultTaskTimeout 单任务默认超时:大部分停 server/停 ticker 应在 1s 内完成。
	DefaultTaskTimeout = 1500 * time.Millisecond
	// DefaultOverallBudget 默认整体预算:留 2s 给 main 末尾的 os.Exit(0) 兜底(5s 看门狗内)。
	DefaultOverallBudget = 3 * time.Second
)

// Run 并发执行所有任务,整体不超过 Budget。阻塞直到全部完成或预算耗尽。
// 返回未能按时完成的任务名列表(供外层记录,不会因此让进程滞留)。
func (c *Coordinator) Run(ctx context.Context, tasks []Task) []string {
	if len(tasks) == 0 {
		return nil
	}
	budget := c.OverallBudget
	if budget <= 0 {
		budget = DefaultOverallBudget
	}
	overallCtx, overallCancel := context.WithTimeout(ctx, budget)
	defer overallCancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	unfinished := make([]string, 0, len(tasks))
	doneSet := make(map[string]bool, len(tasks))

	for _, t := range tasks {
		t := t // capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					c.logf("lifecycle: task %q panic: %v", t.Name, r)
				}
				// 标记完成
				mu.Lock()
				doneSet[t.Name] = true
				mu.Unlock()
			}()

			to := t.Timeout
			if to <= 0 {
				to = DefaultTaskTimeout
			}
			tctx, cancel := context.WithTimeout(overallCtx, to)
			defer cancel()

			startAt := time.Now()
			// 任务自身在独立 goroutine 内执行,以便监听 tctx.Done 提前返回
			runDone := make(chan error, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						runDone <- fmt.Errorf("panic: %v", r)
					}
				}()
				runDone <- t.Run(tctx)
			}()

			select {
			case err := <-runDone:
				if err != nil {
					c.logf("lifecycle: task %q err in %s: %v", t.Name, time.Since(startAt), err)
				} else {
					c.logf("lifecycle: task %q ok in %s", t.Name, time.Since(startAt))
				}
			case <-tctx.Done():
				c.logf("lifecycle: task %q TIMEOUT after %s", t.Name, time.Since(startAt))
			}
		}()
	}

	// 等所有 goroutine 退出 或 整体预算耗尽
	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()

	select {
	case <-allDone:
		return nil
	case <-overallCtx.Done():
		mu.Lock()
		for _, t := range tasks {
			if !doneSet[t.Name] {
				unfinished = append(unfinished, t.Name)
			}
		}
		mu.Unlock()
		return unfinished
	}
}

func (c *Coordinator) logf(format string, args ...interface{}) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}
