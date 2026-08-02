// Package diagserver 提供进程级运行时诊断 HTTP 端点 (仅限本机回环)。
//
// 用途：当程序出现"几十秒后整体卡死、CPU 静止、日志停滚、接口全阻塞"
// 这类死等型阻塞时，通过 pprof 抓取全部 goroutine 调用栈现场，
// 精确定位阻塞点 (channel / 锁 / IO)，避免靠代码推断反复试错。
//
// 安全性：监听地址固定为 127.0.0.1，仅本机可访问，不暴露到网络。
//
// 使用方式 (卡死现场)：
//
//	curl http://127.0.0.1:18765/goroutines            # 全部 goroutine 调用栈
//	curl http://127.0.0.1:18765/debug/pprof/goroutine?debug=2
//	curl http://127.0.0.1:18765/_ping                  # 健康检查
package diagserver

import (
	"context"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultAddr 固定本机回环地址 + 不常用端口，降低与既有服务冲突概率。
	DefaultAddr = "127.0.0.1:18765"
)

var (
	startOnce sync.Once
	started   atomic.Bool

	// srvMu 保护 srv / listener,供 Stop 安全关闭已启动的诊断服务。
	srvMu     sync.Mutex
	srv       *http.Server
	listener  net.Listener
)

// Start 启动 pprof 诊断 HTTP 服务。幂等：重复调用安全。
// 启动失败仅静默返回，绝不影响主程序运行。
func Start() {
	startOnce.Do(func() {
		mux := http.NewServeMux()
		// 挂载完整 pprof 端点
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		// 便捷别名：根路径直接返回 goroutine 全栈，便于快速抓取
		mux.HandleFunc("/goroutines", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			pprof.Handler("goroutine").ServeHTTP(w, r)
		})
		// 健康检查，用于确认诊断端是否存活
		mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("ok"))
		})

		ln, err := net.Listen("tcp", DefaultAddr)
		if err != nil {
			// 端口被占用或不可用：静默放弃，不影响主流程
			return
		}
		srvMu.Lock()
		listener = ln
		srv = &http.Server{Handler: mux}
		srvMu.Unlock()
		go func() {
			started.Store(true)
			_ = srv.Serve(ln)
		}()
	})
}

// Stop 关闭诊断 HTTP 服务并释放监听端口。幂等安全，可多次调用。
// 用于程序关闭流程，避免 pprof 诊断 goroutine 持 listener 句柄导致进程退出后端口残留。
func Stop() {
	srvMu.Lock()
	defer srvMu.Unlock()
	if srv != nil {
		// Shutdown 超时 3s,避免长期阻塞 shutdown 主路径
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		srv = nil
	}
	if listener != nil {
		_ = listener.Close()
		listener = nil
	}
}

// RuntimeStarted 返回诊断服务是否已成功启动，供上层日志使用。
func RuntimeStarted() bool {
	return started.Load()
}
