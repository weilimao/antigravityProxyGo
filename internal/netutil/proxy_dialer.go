package netutil

import (
	"crypto/tls"
	"net"
	"time"
)

// 本文件承载出站拨号与 TLS 的公共传输优化(P1-a):
//   1) 统一带显式超时的 Dialer —— 收紧"连接建立"阶段(含拨代理服务器)悬挂时长;
//   2) 进程级共享 TLS session cache —— 换号/换出口导致连接重建时复用 session,
//      省一次全量握手 RTT。
//
// 关键边界:Dialer.Timeout 只约束 TCP/隧道建立阶段,对已建立的流式长连接零影响
// (长流的读等待由重试链路治理,不在这里干预)。

// dialTimeout 是 TCP 建连/代理隧道建立的显式超时。
// 原实现用零值 net.Dialer{}(无 Timeout):拨向失联代理或黑洞地址时,悬挂时长由
// OS 协议栈决定(Windows SYN 重传可达 ~21s+),期间整个请求干等。15s 覆盖
// 慢代理/跨境链路建连的合理上限,又不至于让用户等 OS 默认的超长悬挂。
const dialTimeout = 15 * time.Second

// dialKeepAlive 与 http.DefaultTransport 保持一致的 30s keep-alive 探测间隔
// (零值 net.Dialer 的 keep-alive 默认 15s,探测偏密)。TLS 长流场景下该值只影响
// 空闲连接的存活探测,不影响数据通路。
const dialKeepAlive = 30 * time.Second

// sharedTLSSessionCache 是进程内所有出站 transport 共享的 TLS 客户端 session 缓存。
// NVIDIA 池换号/换出口(直连/SOCKS5/兜底代理)会频繁新建连接,无 session cache 时
// 每次都是全量 TLS 握手(多 1 个 RTT);有 cache 且上游支持 session resumption 时
// 走简短握手。LRU(64) 足够覆盖号池常用上游域名数量,内存占用约几十 KB 级。
var sharedTLSSessionCache = tls.NewLRUClientSessionCache(64)

// newTimeoutDialer 返回带显式 Timeout/KeepAlive 的 Dialer,替代散落的零值 net.Dialer。
func newTimeoutDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: dialKeepAlive,
	}
}

// socks5ForwardDialer 把 *net.Dialer 适配为 golang.org/x/net/proxy 的 Dial 签名,
// 用作 proxy.SOCKS5 的 forward dialer —— 即"客户端 → SOCKS5 服务器"这一段的拨号。
// 原来传 proxy.Direct(内部零值 Dialer,无超时);换成 newTimeoutDialer 后,
// 连代理服务器本身悬挂也受 dialTimeout 约束,可快速失败进入换号/兜底路径。
type socks5ForwardDialer struct {
	d *net.Dialer
}

// Dial 实现 proxy.Dial 接口(该接口无 ctx,net.Dialer.Dial 内部的 Background
// 上下文仍受本 Dialer 的 Timeout 字段约束,无须额外处理)。
func (s *socks5ForwardDialer) Dial(network, addr string) (net.Conn, error) {
	return s.d.Dial(network, addr)
}
