package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/netutil"
)

// ContextKey is the type for context keys used in the proxy package
type ContextKey string

// RelayUserCtxKey is the context key for relay user ID
const RelayUserCtxKey ContextKey = "relayUserID"

// RelayAPIKeyCtxKey is the context key for relay API key ID
const RelayAPIKeyCtxKey ContextKey = "relayAPIKeyID"

// MetadataConn wraps a net.Conn with additional metadata for relay user tracking
type MetadataConn struct {
	net.Conn
	RelayUserID   string
	RelayAPIKeyID string
}

// RemoteRelayInterface defines the interface for remote proxy relay
type RemoteRelayInterface interface {
	IsConnected() bool
	DialThroughRemote(targetHostPort string) (net.Conn, error)
	GetConfig() RemoteConfig
	FetchAndSaveRemoteLogDetail(reqID string, userKey string) error
}

type MitmListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func NewMitmListener() *MitmListener {
	return &MitmListener{
		conns:  make(chan net.Conn, 2048), // 提升高并发连接缓冲队列大小至 2048
		closed: make(chan struct{}),
	}
}

func (l *MitmListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, io.EOF
	}
}

func (l *MitmListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *MitmListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18443}
}

type ProxyEngine struct {
	sync.Mutex
	certMgr               *CertManager
	handler               *ProxyHandler
	mitmListener          *MitmListener
	mitmServer            *http.Server
	proxyServer           *http.Server
	activeTunnels         map[net.Conn]net.Conn // clientConn -> remoteConn
	activeTunnelsMu       sync.Mutex
	isRunning             bool
	isInterceptMode       bool
	logFn                 func(string)
	stateCallback         func(bool)
	remoteRelay           RemoteRelayInterface // 远程代理中继 (客户端模式)
	relaySSRFBlock        bool
	relayPortBlock        bool
	relayDomainFilter     bool
	relayDomainWhitelist  []string
	relaySecurityMu       sync.RWMutex
}

func NewProxyEngine(handler *ProxyHandler, logFn func(string), stateCallback func(bool)) *ProxyEngine {
	pe := &ProxyEngine{
		certMgr:       NewCertManager(),
		handler:       handler,
		activeTunnels: make(map[net.Conn]net.Conn),
		logFn:         logFn,
		stateCallback: stateCallback,
	}

	if handler != nil {
		handler.getRemoteRelay = func() RemoteRelayInterface {
			pe.Lock()
			defer pe.Unlock()
			return pe.remoteRelay
		}
	}

	return pe
}

func (pe *ProxyEngine) UpdateSecurityRules(ssrfBlock, portBlock, domainFilter bool, whitelist []string) {
	pe.relaySecurityMu.Lock()
	pe.relaySSRFBlock = ssrfBlock
	pe.relayPortBlock = portBlock
	pe.relayDomainFilter = domainFilter
	pe.relayDomainWhitelist = whitelist
	pe.relaySecurityMu.Unlock()
}

func (pe *ProxyEngine) validateTargetSafety(targetHostPort string) error {
	pe.relaySecurityMu.RLock()
	defer pe.relaySecurityMu.RUnlock()

	host, portStr, err := net.SplitHostPort(targetHostPort)
	if err != nil {
		host = targetHostPort
		portStr = "443"
	}

	// 1. 端口安全检查
	if pe.relayPortBlock {
		if portStr != "443" && portStr != "80" {
			return fmt.Errorf("端口 %s 已被安全策略限制，中继服务器仅放行 80/443", portStr)
		}
	}

	// 2. SSRF 拦截（回环及私有网段 IP 禁连）
	if pe.relaySSRFBlock {
		ips, err := net.LookupIP(host)
		if err == nil {
			for _, ip := range ips {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
					return fmt.Errorf("目标地址 %s 解析出的 IP %s 属于内部私有网段，已被拒绝连接", host, ip.String())
				}
			}
		}
	}

	// 3. 目标域名白名单校验
	if pe.relayDomainFilter {
		matched := false
		for _, pattern := range pe.relayDomainWhitelist {
			if matchDomainPattern(host, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("目标域名 %s 不在白名单允许范围内，连接已被拒绝", host)
		}
	}

	return nil
}

func matchDomainPattern(domain, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == "*.*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // e.g. ".googleapis.com"
		return strings.HasSuffix(domain, suffix) || domain == pattern[2:]
	}
	return domain == pattern
}

func (pe *ProxyEngine) SetMode(mode bool) {
	pe.Lock()
	pe.isInterceptMode = mode
	pe.Unlock()

	if pe.stateCallback != nil {
		pe.stateCallback(mode)
	}

	// Terminate active passthrough tunnels to force clients to reconnect
	pe.activeTunnelsMu.Lock()
	tunnelsCount := len(pe.activeTunnels)
	if tunnelsCount > 0 {
		pe.logFn(fmt.Sprintf("🔄 Mode changed. Closing %d active passthrough tunnels to force client reconnection...", tunnelsCount))
		for client, remote := range pe.activeTunnels {
			_ = client.Close()
			_ = remote.Close()
		}
		pe.activeTunnels = make(map[net.Conn]net.Conn)
	}
	pe.activeTunnelsMu.Unlock()
}

func (pe *ProxyEngine) SetRemoteRelay(relay RemoteRelayInterface) {
	pe.Lock()
	pe.remoteRelay = relay
	pe.Unlock()

	// Terminate active passthrough tunnels to force clients to reconnect
	pe.activeTunnelsMu.Lock()
	tunnelsCount := len(pe.activeTunnels)
	if tunnelsCount > 0 {
		pe.logFn(fmt.Sprintf("🔄 Remote route changed. Closing %d active passthrough tunnels to force client reconnection...", tunnelsCount))
		for client, remote := range pe.activeTunnels {
			_ = client.Close()
			_ = remote.Close()
		}
		pe.activeTunnels = make(map[net.Conn]net.Conn)
	}
	pe.activeTunnelsMu.Unlock()
}

func (pe *ProxyEngine) IsInterceptMode() bool {
	pe.Lock()
	defer pe.Unlock()
	return pe.isInterceptMode
}

func (pe *ProxyEngine) Start(dataDir string) error {
	pe.Lock()
	defer pe.Unlock()

	if pe.isRunning {
		return nil
	}

	// Initialize CertManager
	caCertPath := filepath.Join(dataDir, "certs", "certs", "ca.pem")
	caKeyPath := filepath.Join(dataDir, "certs", "keys", "ca.private.key")
	err := pe.certMgr.Init(caCertPath, caKeyPath)
	if err != nil {
		return fmt.Errorf("初始化证书管理器失败: %v", err)
	}

	// 1. Start Decrypted HTTP server
	pe.mitmListener = NewMitmListener()
	pe.mitmServer = &http.Server{
		Handler: pe.handler,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if mc, ok := c.(*MetadataConn); ok {
				if mc.RelayUserID != "" {
					ctx = context.WithValue(ctx, RelayUserCtxKey, mc.RelayUserID)
				}
				if mc.RelayAPIKeyID != "" {
					ctx = context.WithValue(ctx, RelayAPIKeyCtxKey, mc.RelayAPIKeyID)
				}
			}
			return ctx
		},
	}
	go func() {
		_ = pe.mitmServer.Serve(pe.mitmListener)
	}()

	// 2. Start Main HTTP Proxy Server
	pe.proxyServer = &http.Server{
		Handler: pe,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:18443")
	if err != nil {
		pe.mitmListener.Close()
		pe.mitmServer.Close()
		return fmt.Errorf("无法绑定代理接口 127.0.0.1:18443: %v", err)
	}

	go func() {
		_ = pe.proxyServer.Serve(listener)
	}()

	pe.isRunning = true
	pe.logFn("🚀 Decrypting Proxy Server running on port 18443")

	return nil
}

func (pe *ProxyEngine) Stop() {
	pe.Lock()
	defer pe.Unlock()

	if !pe.isRunning {
		return
	}

	// Close all passthrough tunnels
	pe.activeTunnelsMu.Lock()
	for client, remote := range pe.activeTunnels {
		_ = client.Close()
		_ = remote.Close()
	}
	pe.activeTunnels = make(map[net.Conn]net.Conn)
	pe.activeTunnelsMu.Unlock()

	if pe.mitmListener != nil {
		pe.mitmListener.Close()
	}
	if pe.mitmServer != nil {
		pe.mitmServer.Close()
	}
	if pe.proxyServer != nil {
		pe.proxyServer.Close()
	}

	pe.isRunning = false
	pe.logFn("🛑 Proxy Server stopped.")
}

func (pe *ProxyEngine) dialWithProxy(address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return netutil.DialContext(ctx, "tcp", address)
}

func (pe *ProxyEngine) ReloadCertificates(dataDir string) error {
	pe.Lock()
	defer pe.Unlock()

	caCertPath := filepath.Join(dataDir, "certs", "certs", "ca.pem")
	caKeyPath := filepath.Join(dataDir, "certs", "keys", "ca.private.key")
	return pe.certMgr.Init(caCertPath, caKeyPath)
}

func (pe *ProxyEngine) ResetConnections() {
	pe.logFn("🔌 检测到系统从休眠中唤醒，正在重置本地连接池与活跃隧道...")
	pe.activeTunnelsMu.Lock()
	tunnelsCount := len(pe.activeTunnels)
	if tunnelsCount > 0 {
		pe.logFn(fmt.Sprintf("🧹 Closing %d active passthrough tunnels...", tunnelsCount))
		for client, remote := range pe.activeTunnels {
			_ = client.Close()
			_ = remote.Close()
		}
		pe.activeTunnels = make(map[net.Conn]net.Conn)
	}
	pe.activeTunnelsMu.Unlock()
	pe.logFn("✅ 全局 HTTP/HTTPS 代理连接已全部重置。")
}

func (pe *ProxyEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		pe.handleConnect(w, r)
		return
	}

	// Standard HTTP proxying logic
	pe.handler.ServeHTTP(w, r)
}

func (pe *ProxyEngine) handleConnect(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hostAndPort := r.URL.Path
	if hostAndPort == "" {
		hostAndPort = r.Host
	}

	hostParts := strings.Split(hostAndPort, ":")
	host := hostParts[0]

	// 提取中继用户ID
	relayUserID, _ := r.Context().Value(RelayUserCtxKey).(string)

	// 如果来自中继用户的连接（即中继服务端特征），执行网络安全拦截检查
	if relayUserID != "" {
		if errSec := pe.validateTargetSafety(hostAndPort); errSec != nil {
			pe.logFn(fmt.Sprintf("🛡️ [中继安全拦截] 拒绝中继用户 %s 连接到 %s: %v", relayUserID, hostAndPort, errSec))
			clientConn.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
			clientConn.Close()
			return
		}
	}

	isTargetHost := strings.Contains(host, "generativelanguage.googleapis.com") || strings.Contains(host, "cloudcode-pa.googleapis.com") || strings.Contains(host, "cloudaicompanion.googleapis.com") || strings.Contains(host, "aiplatform.googleapis.com")

	pe.Lock()
	shouldDecrypt := pe.isInterceptMode && isTargetHost
	pe.Unlock()

	pe.logFn(fmt.Sprintf("🔍 Host: %s | Decrypt: %v | UA: %s", host, shouldDecrypt, r.Header.Get("User-Agent")))

	if !shouldDecrypt {
		// 远程模式优先级最高：所有无需解密的请求通过远程代理中继
		if pe.remoteRelay != nil && pe.remoteRelay.IsConnected() {
			isLocalRelayLoop := false
			if relayUserID != "" {
				conf := pe.remoteRelay.GetConfig()
				if conf.IsLocal {
					isLocalRelayLoop = true
				}
			}

			if !isLocalRelayLoop {
				remoteConn, errDial := pe.remoteRelay.DialThroughRemote(hostAndPort)
				if errDial != nil {
					pe.logFn(fmt.Sprintf("❌ Remote relay failed for %s: %v", hostAndPort, errDial))
					_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					_ = clientConn.Close()
					return
				}
				_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				pe.setupBidirectionalTunnel(clientConn, remoteConn)
				return
			}
		}

		// Passthrough Tunnel (直接本地中转)
		remoteConn, errDial := pe.dialWithProxy(hostAndPort)
		if errDial != nil {
			pe.logFn(fmt.Sprintf("❌ CONNECT %s Dial error: %v", hostAndPort, errDial))
			clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			clientConn.Close()
			return
		}

		_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		pe.setupBidirectionalTunnel(clientConn, remoteConn)
		return
	}

	// MITM Decryption
	cert, errCert := pe.certMgr.GetCertificate(host)
	if errCert != nil {
		pe.logFn(fmt.Sprintf("❌ Dynamic certificate generation failed for %s: %v", host, errCert))
		clientConn.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n\r\n"))
		clientConn.Close()
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}

	tlsClientConn := tls.Server(clientConn, tlsConfig)
	errHandshake := tlsClientConn.Handshake()
	if errHandshake != nil {
		tlsClientConn.Close()
		return
	}

	var connToEnqueue net.Conn = tlsClientConn
	if relayUserID != "" {
		relayAPIKeyID, _ := r.Context().Value(RelayAPIKeyCtxKey).(string)
		connToEnqueue = &MetadataConn{
			Conn:          tlsClientConn,
			RelayUserID:   relayUserID,
			RelayAPIKeyID: relayAPIKeyID,
		}
	}

	// Enqueue to decrypted HTTP Server
	select {
	case pe.mitmListener.conns <- connToEnqueue:
	case <-time.After(3 * time.Second): // 允许排队等待最长 3 秒，避免高并发下瞬间溢出丢弃
		// Timeout, close connection
		tlsClientConn.Close()
	}
}

// setupBidirectionalTunnel establishes a bidirectional data tunnel between two connections
func (pe *ProxyEngine) setupBidirectionalTunnel(clientConn, remoteConn net.Conn) {
	pe.activeTunnelsMu.Lock()
	pe.activeTunnels[clientConn] = remoteConn
	pe.activeTunnelsMu.Unlock()

	cleanup := func() {
		pe.activeTunnelsMu.Lock()
		delete(pe.activeTunnels, clientConn)
		pe.activeTunnelsMu.Unlock()
		_ = clientConn.Close()
		_ = remoteConn.Close()
	}

	go func() {
		defer cleanup()
		_, _ = io.Copy(remoteConn, clientConn)
	}()
	go func() {
		defer cleanup()
		_, _ = io.Copy(clientConn, remoteConn)
	}()
}
