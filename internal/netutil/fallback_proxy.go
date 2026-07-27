package netutil

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// fallback_proxy.go 提供"NVIDIA 上游蓄流重试耗尽后的兜底出站代理"独立 transport 构造与复用。
//
// 与 internal/netutil/proxy.go 的全局 GetSystemProxy 三级链(自定义→系统 IE→本地端口探测)完全解耦:
// 兜底 transport 强制走用户配置的单一代理地址(http:// 或 socks5://),不读系统代理、不回退直连,
// 避免兜底又被系统代理逻辑覆盖而死循环,也保证"换出口"语义纯粹。
//
// 性能(高并发约束):兜底 transport 按"配置级单例"复用,同一配置指纹下所有并发请求共享同一
// *http.Transport(连接池复用,无每请求 TCP/TLS 握手风暴)。配置变更时原子替换实例,旧实例交给
// GC 自然回收其在用连接,不强行 Close 中断在途请求。对齐 sharedTransport(sync.Once)+proxyConfigMu
// 的成熟范式。
//
// 仅直连 5s×5 重试全部耗尽后才会触达兜底路径(稀疏),正常请求完全不进入本文件逻辑,零额外开销。

var (
	// fallbackTransportMu 保护 currentFallbackTransport 与其配置指纹的读写并发。
	fallbackTransportMu sync.RWMutex
	// currentFallbackTransport 是当前生效的兜底 transport 单例(配置级复用)。
	currentFallbackTransport *http.Transport
	// currentFallbackSig 是生成 currentFallbackTransport 时所用配置的指纹("addr|user|pass"),
	// 用于判定配置是否变更、是否需要重建 transport。空串表示尚未构造。
	currentFallbackSig string
)

// fallbackSignature 生成兜底代理配置的指纹,供单例复用判定。三项任一变更即视为新配置需重建。
func fallbackSignature(addr, user, pass string) string {
	return addr + "|" + user + "|" + pass
}

// GetFallbackClient 返回一个绑定兜底代理的 *http.Client(流式:不设 Timeout,对齐 streamClient)。
//
// addr 形如 "socks5://host:port" 或 "http://host:port",由 URL scheme 区分协议;Username/Password 仅在
// 代理需要鉴权时填写(可空)。返回的 client 复用配置级单例 transport(同一配置指纹下共享连接池)。
//
// 错误语义:配置为空 / scheme 不支持 / URL 解析失败 / socks5 dialer 构造失败 → 返回 (nil, err),
// 调用方据此跳过兜底交给上一级 overloaded_error,不在此兜底内强行直连(兜底就是要换出口,不能回退直连)。
//
// 并发安全:双层检查(读锁快路径 + 写锁重建),同指纹并发首触只构造一次;配置变更原子替换。
func GetFallbackClient(addr, user, pass string) (*http.Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("fallback proxy address is empty")
	}
	// 补协议 scheme(用户可能只填 host:port):无 scheme 时按 http 默认处理。
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid fallback proxy address %q: %v", addr, err)
	}

	sig := fallbackSignature(addr, user, pass)

	// 快路径:读锁判定单例是否命中当前配置指纹。
	fallbackTransportMu.RLock()
	t := currentFallbackTransport
	hit := currentFallbackSig == sig && t != nil
	fallbackTransportMu.RUnlock()
	if hit {
		return &http.Client{Transport: t, Timeout: 0}, nil
	}

	// 慢路径:指纹不一致或尚未构造,加写锁重建。写锁内再查一次防并发重复重建(双层检查)。
	fallbackTransportMu.Lock()
	defer fallbackTransportMu.Unlock()
	if currentFallbackSig == sig && currentFallbackTransport != nil {
		return &http.Client{Transport: currentFallbackTransport, Timeout: 0}, nil
	}

	nt, err := buildFallbackTransport(u, user, pass)
	if err != nil {
		return nil, err
	}
	currentFallbackTransport = nt
	currentFallbackSig = sig
	return &http.Client{Transport: nt, Timeout: 0}, nil
}

// buildFallbackTransport 按代理 URL 的 scheme 构造一个强制走该代理的 *http.Transport。
// 不绑 GetSystemProxy、不设回退直连 DialContext;连接池参数对齐 NewTransport()。
func buildFallbackTransport(u *url.URL, user, pass string) (*http.Transport, error) {
	base := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if user != "" || pass != "" {
			auth = &proxy.Auth{User: user, Password: pass}
		}
		// proxy.SOCKS5 返回的 dialer 通常实现 proxy.ContextDialer,DialContext 可精确传导 ctx 取消。
		dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("build socks5 dialer for %s: %v", u.Host, err)
		}
		ctxDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			// 极少见:退化为非 ctx dialer 包装,ctx 取消无法精确传导但不影响基本拨号。
			ctxDialer = &nonCtxSocks5Dialer{dialer: dialer}
		}
		base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ctxDialer.DialContext(ctx, network, addr)
		}
		return base, nil

	case "http", "https":
		// http 代理走标准库 Transport.Proxy + ProxyConnectHeader(Basic Auth),让 Go 处理 CONNECT 隧道。
		proxyURL := &url.URL{Scheme: u.Scheme, Host: u.Host}
		if user != "" || pass != "" {
			proxyURL.User = url.UserPassword(user, pass)
		}
		base.Proxy = http.ProxyURL(proxyURL)
		return base, nil

	default:
		return nil, fmt.Errorf("unsupported fallback proxy scheme %q (use socks5:// or http://)", u.Scheme)
	}
}

// nonCtxSocks5Dialer 在 proxy.SOCKS5 未实现 proxy.ContextDialer 的极少见情况下,
// 把 Dial 包成 DialContext(ctx 取消无法精确传导,但不影响基本拨号能力)。
type nonCtxSocks5Dialer struct {
	dialer proxy.Dialer
}

func (d *nonCtxSocks5Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// 客户端取消时主动放弃阻塞中的 Dial:另起 goroutine 拨号,ctx.Done 即返回 ctx 错误。
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, e := d.dialer.Dial(network, addr)
		done <- result{c, e}
	}()
	select {
	case <-ctx.Done():
		// goroutine 仍可能在跑,但 connChannel 有缓冲不会泄漏阻塞;丢弃其结果即可。
		go func() {
			if r := <-done; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-done:
		return r.conn, r.err
	}
}

// ResetFallbackTransport 重置兜底 transport 单例(测试用,生产无需调用)。
// 它让下一次 GetFallbackClient 强制重建,便于测试覆盖配置变更场景。
func ResetFallbackTransport() {
	fallbackTransportMu.Lock()
	currentFallbackTransport = nil
	currentFallbackSig = ""
	fallbackTransportMu.Unlock()
}
