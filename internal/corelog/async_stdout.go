// Package corelog 提供进程级的异步、永不阻塞的日志输出设施。
//
// 设计目标：
//   - 请求处理 goroutine 绝不因日志写入被阻塞。即便 stdout 下游
//     (Wails 转发管道 / 控制台) 停止消费，业务 goroutine 也不会卡在
//     fmt.Println / fmt.Printf 上，从而避免“几十秒后整体卡死、
//     接口全部阻塞、日志停止滚动”的级联阻塞。
//   - 单一消费者 goroutine 顺序消费缓冲队列，保证日志行序稳定。
//   - 缓冲队满时丢弃最旧条目并记录丢弃计数，绝不回压调用方。
package corelog

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// 缓冲队列容量。按峰值流量估算:每个请求最多产生约 20~40 条日志,
// 并发 ~200 请求约 8000 条,翻倍预留冗余。溢出时丢弃最旧条目而非阻塞。
const queueCapacity = 16384

var (
	once      sync.Once
	queue     chan string
	dropped   atomic.Uint64 // 因队列满被丢弃的日志行数
	startedMu sync.Mutex
	stopped   atomic.Bool
	stopCh    chan struct{}
)

// ensureStarted 惰性启动单消费者 goroutine。
// 使用 sync.Once 保证整个进程只启动一次,即便多次调用也安全。
func ensureStarted() {
	once.Do(func() {
		queue = make(chan string, queueCapacity)
		stopCh = make(chan struct{})
		go consumer()
	})
}

// consumer 是唯一的消费者 goroutine,顺序消费 queue 并写入 os.Stdout。
// 当 stopCh 关闭时,排空残留日志后退出,避免日志截断。
func consumer() {
	for {
		select {
		case line := <-queue:
			_, _ = os.Stdout.WriteString(line)
			_, _ = os.Stdout.WriteString("\n")
		case <-stopCh:
			// 排空残留,保证退出前已投递的日志尽量写完
			for {
				select {
				case line := <-queue:
					_, _ = os.Stdout.WriteString(line)
					_, _ = os.Stdout.WriteString("\n")
				default:
					return
				}
			}
		}
	}
}

// EnqueueLine 向异步队列投递一行日志文本(不含换行)。
// 永不阻塞调用方:队列满时丢弃最旧条目并累加 dropped 计数。
// 使用 select-default 实现非阻塞投递。
func EnqueueLine(line string) {
	if stopped.Load() {
		// 进程关闭流程已启动:忽略新日志,避免写已关闭 channel
		return
	}
	ensureStarted()
	select {
	case queue <- line:
	default:
		// 队列满:逐出最旧一条腾位置,确保最新日志不丢
		select {
		case <-queue:
			dropped.Add(1)
		default:
		}
		select {
		case queue <- line:
		default:
			dropped.Add(1)
		}
	}
}

// Printf 是 fmt.Printf 的异步、永不阻塞等价物。格式化后整行投递。
// 返回写入字节数为格式化长度(语义近似,主要用于兼容已有调用签名)。
func Printf(format string, args ...interface{}) {
	EnqueueLine(fmt.Sprintf(format, args...))
}

// Println 是 fmt.Println 的异步、永不阻塞等价物。
func Println(args ...interface{}) {
	EnqueueLine(fmt.Sprintln(args...))
}

// Stop 停止消费者 goroutine,排空残留日志。
// 安全可多次调用。用于程序关闭流程。
func Stop() {
	if !stopped.CompareAndSwap(false, true) {
		return
	}
	ensureStarted()
	close(stopCh)
}

// Dropped 返回累计被丢弃的日志行数,供诊断与监控使用。
func Dropped() uint64 {
	return dropped.Load()
}
