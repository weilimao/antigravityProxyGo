// Package proxy 网络监听器(netwatch):周期性探测本机网络连通性,
// 当系统从"断网"恢复到"联网"时,触发代理引擎连接池重置与远程中继自动重连,
// 从根本修复"网络断开后程序废了、再联网也无法使用中继服务"的问题。
//
// 设计要点:
//   - 探测目标为公网 DNS 根服务器(20.205.243.162 等)与 1.1.1.1,避开国内 GFW 抖动。
//     采用 TCP 拨号到 53 端口(短连接),不依赖 DNS 解析。
//   - 默认 60s 探测一次,连续 1 次失败即判定离线,连续 1 次成功即判定恢复,
//     避免慢抖动频繁误报触发重连风暴。
//   - 网络恢复后仅触发一次回调(边沿触发,非电平),避免重复 Login。
//   - 全程不持有任何业务锁,仅通过回调间接驱动 ResetConnections / RemoteRelay.Login。
package proxy

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// NetWatch 网络连通性监听器。
type NetWatch struct {
	probeTargets []string        // 探测目标地址(host:port)
	interval     time.Duration   // 探测周期
	probeTimeout time.Duration   // 单次探测超时

	onRecover func() // 网络从断→通时触发的恢复回调(边沿触发)

	running atomic.Bool
	stopCh  chan struct{}
	wg      sync.WaitGroup

	lastOnline atomic.Bool
}

// NewNetWatch 构造一个网络监听器。
// onRecover:网络从离线恢复到在线时调用(仅在边沿跳变时触发一次)。
func NewNetWatch(onRecover func()) *NetWatch {
	return &NetWatch{
		// 探测目标:外网 DNS / 公共 IP 的 53 端口(TCP)。
		// 20.205.243.162 = Github, 1.1.1.1 = Cloudflare DNS,
		// 8.8.8.8 = Google DNS, 223.5.5.5 = 阿里 DNS。
		// 任一可达即视为在线,降低单点抖动误判。
		probeTargets: []string{
			"1.1.1.1:53",
			"8.8.8.8:53",
			"223.5.5.5:53",
			"20.205.243.162:53",
		},
		interval:     60 * time.Second,
		probeTimeout: 5 * time.Second,
		onRecover:    onRecover,
	}
}

// Start 启动网络监听后台 goroutine。幂等:重复调用安全。
func (w *NetWatch) Start() {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	w.stopCh = make(chan struct{})

	// 启动即立即探测一次,建立初始在线状态基线
	w.lastOnline.Store(w.probeOnce())

	w.wg.Add(1)
	go w.loop()
}

// Stop 停止网络监听并等待 goroutine 退出。幂等:重复调用安全。
func (w *NetWatch) Stop() {
	if !w.running.CompareAndSwap(true, false) {
		return
	}
	close(w.stopCh)
	w.wg.Wait()
}

// IsOnline 返回最近一次探测的在线状态。
func (w *NetWatch) IsOnline() bool {
	return w.lastOnline.Load()
}

// loop 后台探测循环。每 interval 周期探测一次,
// 当状态从离线跳变到在线时触发 onRecover 回调。
func (w *NetWatch) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			online := w.probeOnce()
			// 边沿触发:仅在"离线→在线"跳变时回调一次
			if online && !w.lastOnline.Load() {
				w.lastOnline.Store(true)
				if w.onRecover != nil {
					// 异步执行恢复回调,避免阻塞探测循环;
					// 恢复回调内会做 Login / ResetConnections 等较重操作
					go w.onRecover()
				}
				continue
			}
			w.lastOnline.Store(online)
		}
	}
}

// probeOnce 并发探测所有目标,任一可达即返回 true(在线)。
// 全部不可达返回 false(离线)。单个探测超时由 probeTimeout 控制。
func (w *NetWatch) probeOnce() bool {
	type result struct{ ok bool }

	ctx, cancel := context.WithTimeout(context.Background(), w.probeTimeout)
	defer cancel()

	resultCh := make(chan result, len(w.probeTargets))
	for _, target := range w.probeTargets {
		go func(addr string) {
			d := net.Dialer{Timeout: w.probeTimeout}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err == nil {
				_ = conn.Close()
				resultCh <- result{ok: true}
				return
			}
			resultCh <- result{ok: false}
		}(target)
	}

	for i := 0; i < len(w.probeTargets); i++ {
		select {
		case <-ctx.Done():
			// 整体超时,剩余未返回视为失败
			return false
		case r := <-resultCh:
			if r.ok {
				// 任一可达即在线,无需等待剩余探测
				return true
			}
		}
	}
	return false
}
