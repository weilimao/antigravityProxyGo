package netutil

import (
	"net/http"
	"sync"
	"testing"
)

// fallback_proxy_test.go 覆盖兜底出站代理 transport 的构造与高并发单例复用。
// 重点验证"同一配置指纹下并发请求共享同一 transport,不每请求重建"(高并发性能约束)。

const fbHTTP = "http://1.2.3.4:8080"
const fbSOCKS5 = "socks5://1.2.3.4:1080"

func TestGetFallbackClient_EmptyAddress(t *testing.T) {
	ResetFallbackTransport()
	c, err := GetFallbackClient("", "", "")
	if err == nil {
		t.Errorf("expected error for empty address, got client %v", c)
	}
	if c != nil {
		t.Errorf("expected nil client on error, got %v", c)
	}
}

func TestGetFallbackClient_UnsupportedScheme(t *testing.T) {
	ResetFallbackTransport()
	c, err := GetFallbackClient("ftp://1.2.3.4:21", "", "")
	if err == nil {
		t.Errorf("expected error for unsupported scheme ftp, got client %v", c)
	}
	if c != nil {
		t.Errorf("expected nil client on unsupported scheme, got %v", c)
	}
}

func TestGetFallbackClient_HTTPBuildsAndReuses(t *testing.T) {
	ResetFallbackTransport()
	c1, err := GetFallbackClient(fbHTTP, "", "")
	if err != nil {
		t.Fatalf("first http client build failed: %v", err)
	}
	c2, err := GetFallbackClient(fbHTTP, "", "")
	if err != nil {
		t.Fatalf("second http client build failed: %v", err)
	}
	// 同配置指纹:复用单例 transport(两个 client 共享同一 transport 指针)。
	if c1.Transport != c2.Transport {
		t.Errorf("same http config should reuse transport: %p vs %p", c1.Transport, c2.Transport)
	}
}

func TestGetFallbackClient_SOCKS5BuildsAndReuses(t *testing.T) {
	ResetFallbackTransport()
	c1, err := GetFallbackClient(fbSOCKS5, "", "")
	if err != nil {
		t.Fatalf("socks5 client build failed: %v", err)
	}
	c2, err := GetFallbackClient(fbSOCKS5, "", "")
	if err != nil {
		t.Fatalf("second socks5 client build failed: %v", err)
	}
	if c1.Transport != c2.Transport {
		t.Errorf("same socks5 config should reuse transport: %p vs %p", c1.Transport, c2.Transport)
	}
}

func TestGetFallbackClient_ConfigChangeRebuildsTransport(t *testing.T) {
	ResetFallbackTransport()
	c1, err := GetFallbackClient(fbHTTP, "", "")
	if err != nil {
		t.Fatalf("first build failed: %v", err)
	}
	// 改地址(配置指纹变更)→ 必须重建 transport,不复用旧实例。
	c2, err := GetFallbackClient("http://5.6.7.8:9090", "", "")
	if err != nil {
		t.Fatalf("rebuild on address change failed: %v", err)
	}
	if c1.Transport == c2.Transport {
		t.Errorf("address change should rebuild transport, but got same instance %p", c1.Transport)
	}
	// 同地址但改 username 也算指纹变更 → 重建。
	c3, err := GetFallbackClient("http://5.6.7.8:9090", "alice", "secret")
	if err != nil {
		t.Fatalf("rebuild on auth change failed: %v", err)
	}
	if c2.Transport == c3.Transport {
		t.Errorf("auth change should rebuild transport, but got same instance %p", c2.Transport)
	}
}

// TestGetFallbackClient_ConcurrentSameConfigSingleBuild 验证高并发约束:
// 同一配置指纹下 50 个 goroutine 并发首触 GetFallbackClient,
// transport 只构造一次(双层检查防并发重复重建),全部共享同一 transport 指针。
func TestGetFallbackClient_ConcurrentSameConfigSingleBuild(t *testing.T) {
	ResetFallbackTransport()
	const n = 50
	transports := make([]*http.Client, n)
	ready := make(chan struct{})
	fire := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-fire // 屏障:所有 goroutine 就绪后一起 fire,制造真并发竞争构造单例。
			c, err := GetFallbackClient(fbHTTP, "", "")
			if err != nil {
				t.Errorf("goroutine %d build error: %v", idx, err)
				return
			}
			transports[idx] = c
		}(i)
	}
	close(ready)
	// 简单确认所有 goroutine 已注册(读 ready 无死锁风险后 fire)。
	close(fire)
	wg.Wait()

	// 全部 50 个并发请求必须共享同一 transport 指针(单例复用,无并发重复重建)。
	first := transports[0].Transport
	for i := 1; i < n; i++ {
		if transports[i].Transport != first {
			t.Errorf("goroutine %d transport %p != first %p (concurrent duplicate build detected)", i, transports[i].Transport, first)
		}
	}
}
