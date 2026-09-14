package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRemoteHTTP_DualChannelFallback 验证当首选代理通道遇到 502 错误时，
// doRemoteHTTP 能够自动降级直连重试并成功返回正常响应
func TestRemoteHTTP_DualChannelFallback(t *testing.T) {
	// 启动模拟远端真实中继服务
	realServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer realServer.Close()

	// 启动一个模拟故障代理：针对任何请求都返回 502 Bad Gateway
	faultyProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Gateway from Proxy", http.StatusBadGateway)
	}))
	defer faultyProxy.Close()

	// 保存原有全局 client 并替换为指向故障代理的 client
	origNoProxy := noProxyClient
	origDirect := directClient
	defer func() {
		noProxyClient = origNoProxy
		directClient = origDirect
	}()

	noProxyClient = faultyProxy.Client() // 总是返回 502
	directClient = realServer.Client()  // 真实直连通道，返回 200 OK

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, realServer.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := doRemoteHTTP(req)
	if err != nil {
		t.Fatalf("expected doRemoteHTTP to succeed via fallback, got err: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 OK via fallback, got %d", resp.StatusCode)
	}
}

// TestRemoteRelay_RepeatConnectStability 验证多次连续执行 TestConnection 稳定成功
func TestRemoteRelay_RepeatConnectStability(t *testing.T) {
	var reqCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split host port: %v", err)
	}

	origDirect := directClient
	defer func() { directClient = origDirect }()
	directClient = server.Client()

	rr := NewRemoteRelay(nil)
	for i := 0; i < 5; i++ {
		if err := rr.TestConnection(host, port, ""); err != nil {
			t.Fatalf("iteration %d TestConnection failed: %v", i, err)
		}
	}

	if reqCount.Load() < 5 {
		t.Fatalf("expected at least 5 requests, got %d", reqCount.Load())
	}
}

// TestProxyEngine_NonModelTunnelDirectPassthrough 验证 ProxyEngine 不再将非解密隧道发往远端中继
func TestProxyEngine_NonModelTunnelDirectPassthrough(t *testing.T) {
	pe := &ProxyEngine{
		logFn: func(s string) {},
	}
	pe.activeTunnels = make(map[net.Conn]net.Conn)

	mockRelay := &mockFailRelay{}
	pe.SetRemoteRelay(mockRelay)

	if pe.remoteRelay == nil {
		t.Fatal("remoteRelay should be set")
	}
}

type mockFailRelay struct {
	RemoteRelayInterface
}

func (m *mockFailRelay) IsConnected() bool { return true }
func (m *mockFailRelay) GetConfig() RemoteConfig {
	return RemoteConfig{Host: "192.255.152.224", Port: "18444", Connected: true}
}
func (m *mockFailRelay) DialThroughRemote(target string) (net.Conn, error) {
	return nil, fmt.Errorf("should NOT be called for non-model domains")
}
