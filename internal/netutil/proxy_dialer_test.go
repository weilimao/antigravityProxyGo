package netutil

import (
	"crypto/tls"
	"net/url"
	"testing"
	"time"
)

// proxy_dialer_test.go 锁定 P1-a(传输层快赢)的两个不变量:
//   1) 出站 Dialer 携带显式 Timeout/KeepAlive(杜绝零值 Dialer 的 OS 默认分钟级悬挂);
//   2) 主传输与兜底传输共用同一进程级 TLS session cache(换出口重建连接省握手 RTT)。
// 全部纯字段断言,不触网,毫秒级完成。

// TestNewTimeoutDialer_ExplicitBounds 锁定超时/保活参数,防后续重构回退为零值。
// 语义关键在于:Timeout 只约束"连接建立"阶段,对已建立长流的读路径零影响。
func TestNewTimeoutDialer_ExplicitBounds(t *testing.T) {
	d := newTimeoutDialer()
	if d == nil {
		t.Fatal("newTimeoutDialer 不应返回 nil")
	}
	if d.Timeout != 15*time.Second {
		t.Errorf("Timeout 期望 15s,实际 %v", d.Timeout)
	}
	if d.KeepAlive != 30*time.Second {
		t.Errorf("KeepAlive 期望 30s(对齐 http.DefaultTransport),实际 %v", d.KeepAlive)
	}
}

// TestNewTransport_TLSSessionCacheShared 锁定 NewTransport 启用共享 session cache。
func TestNewTransport_TLSSessionCacheShared(t *testing.T) {
	tr := NewTransport()
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig 不应为 nil")
	}
	if tr.TLSClientConfig.ClientSessionCache == nil {
		t.Fatal("ClientSessionCache 不应为 nil")
	}
	if tr.TLSClientConfig.ClientSessionCache != tls.ClientSessionCache(sharedTLSSessionCache) {
		t.Error("NewTransport 应共享包级 sharedTLSSessionCache,而非每 transport 各自新建")
	}
}

// TestFallbackTransport_TLSSessionCacheShared 锁定兜底 transport 与主传输共用同一 session cache。
func TestFallbackTransport_TLSSessionCacheShared(t *testing.T) {
	u, err := url.Parse(fbSOCKS5)
	if err != nil {
		t.Fatalf("parse %q: %v", fbSOCKS5, err)
	}
	ft, err := buildFallbackTransport(u, "", "")
	if err != nil {
		t.Fatalf("buildFallbackTransport: %v", err)
	}
	if ft.TLSClientConfig == nil || ft.TLSClientConfig.ClientSessionCache == nil {
		t.Fatal("兜底 transport 的 ClientSessionCache 不应为 nil")
	}
	if ft.TLSClientConfig.ClientSessionCache != tls.ClientSessionCache(sharedTLSSessionCache) {
		t.Error("兜底 transport 应与主传输共享 sharedTLSSessionCache")
	}
}

// TestSocks5ForwardDialer_ImplementsProxyDial 锁定适配器类型可满足 proxy.Dial 签名
// (x/net/proxy.SOCKS5 的 forward 参数形态),且内部挂的是带超时 Dialer。
func TestSocks5ForwardDialer_ImplementsProxyDial(t *testing.T) {
	fwd := &socks5ForwardDialer{d: newTimeoutDialer()}
	if fwd.d == nil || fwd.d.Timeout != 15*time.Second {
		t.Errorf("forward dialer 内部应为 15s 超时 Dialer,实际 %+v", fwd.d)
	}
}
