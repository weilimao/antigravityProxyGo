package netutil

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-ieproxy"
	"golang.org/x/net/proxy"
)

// NewTransport returns a new http.Transport configured with system proxy auto-detection
// and insecure skip verification (since we are doing local proxy and decrypting traffic).
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy:             GetSystemProxy,
		DialContext:       DialContext, // 绑定自定义的 DialContext，实现对 SOCKS5 专属代理的完美底层拨号支持
		DisableKeepAlives: true,        // 彻底禁用连接复用与连接池，防止死连接残留卡死，强迫每次连接均动态加载最新代理配置
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
}

type ProxyConfig struct {
	FallbackPorts        string
	CustomSocks5Address  string
	CustomSocks5Enabled  bool
	CustomSocks5Username string
	CustomSocks5Password string
}

var (
	proxyConfig        ProxyConfig
	proxyConfigMu      sync.RWMutex
	cachedLocalProxy   *url.URL
	cachedLocalProxyMu sync.RWMutex
)

func init() {
	go startLocalVPNProxyDetector()
}

// UpdateConfig updates the global configuration for proxy routing.
func UpdateConfig(cfg ProxyConfig) {
	proxyConfigMu.Lock()
	proxyConfig = cfg
	proxyConfigMu.Unlock()
	
	// 立即触发一次本地代理的主动探测
	go triggerLocalProxyDetection()
}

func startLocalVPNProxyDetector() {
	triggerLocalProxyDetection()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			triggerLocalProxyDetection()
		}
	}
}

func triggerLocalProxyDetection() {
	addr := detectLocalVPNProxy()
	var proxyURL *url.URL
	if addr != "" {
		if u, err := url.Parse(addr); err == nil {
			proxyURL = u
		}
	}
	cachedLocalProxyMu.Lock()
	cachedLocalProxy = proxyURL
	cachedLocalProxyMu.Unlock()
}

// GetSystemProxy returns the system proxy configured in Windows registry, environment variables, etc.
// It prioritizes custom SOCKS5 proxy if enabled, falls back to system proxy, and then falls back to auto-detected local VPN proxy.
func GetSystemProxy(req *http.Request) (*url.URL, error) {
	proxyConfigMu.RLock()
	socks5Enabled := proxyConfig.CustomSocks5Enabled
	socks5Address := proxyConfig.CustomSocks5Address
	proxyConfigMu.RUnlock()

	// 1. 如果启用了专属 SOCKS5 代理配置，则完全绕过系统代理和自动探测，强制走此配置
	if socks5Enabled && socks5Address != "" {
		proxyConfigMu.RLock()
		user := proxyConfig.CustomSocks5Username
		pass := proxyConfig.CustomSocks5Password
		proxyConfigMu.RUnlock()

		addr := socks5Address
		if !strings.Contains(addr, "://") {
			addr = "socks5://" + addr
		}
		if u, err := url.Parse(addr); err == nil {
			if user != "" || pass != "" {
				u.User = url.UserPassword(user, pass)
			}
			return u, nil
		}
	}

	// 2. 否则，正常解析系统 IE 代理配置
	proxyURL, err := ieproxy.GetProxyFunc()(req)
	if err == nil && proxyURL != nil {
		if !isLocalProxy(proxyURL) {
			return proxyURL, nil
		}
	}

	// 3. 系统代理为空或被过滤时，Fallback 降级使用探测到的本地 VPN 代理端口
	cachedLocalProxyMu.RLock()
	localProxy := cachedLocalProxy
	cachedLocalProxyMu.RUnlock()
	if localProxy != nil {
		return localProxy, nil
	}

	return nil, nil
}

func detectLocalVPNProxy() string {
	ports := []struct {
		scheme string
		addr   string
	}{
		{"http", "127.0.0.1:7890"},   // Clash default HTTP
		{"http", "127.0.0.1:7897"},   // Clash Verge / Mihomo default HTTP
		{"http", "127.0.0.1:10809"},  // v2rayN default HTTP
		{"socks5", "127.0.0.1:10808"},// v2rayN default SOCKS
		{"socks5", "127.0.0.1:7893"},  // Clash default SOCKS
		{"socks5", "127.0.0.1:7898"},  // Clash Verge / Mihomo default SOCKS
		{"socks5", "127.0.0.1:1080"},  // General SOCKS default
	}

	// 动态解析用户在设置中自定义配置的端口
	proxyConfigMu.RLock()
	fallbackPortsStr := proxyConfig.FallbackPorts
	proxyConfigMu.RUnlock()

	var customPorts []int
	if fallbackPortsStr != "" {
		parts := strings.Split(fallbackPortsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if p, err := strconv.Atoi(part); err == nil && p > 0 && p < 65536 {
				customPorts = append(customPorts, p)
			}
		}
	}

	// 优先扫描用户配置的自定义端口
	for _, p := range customPorts {
		addr := fmt.Sprintf("127.0.0.1:%d", p)
		if isPortOpen(addr) {
			scheme := "http"
			if probePortProtocol(addr) == "socks5" {
				scheme = "socks5"
			}
			return scheme + "://" + addr
		}
	}

	// 降级扫描默认常用 VPN 端口
	for _, p := range ports {
		if isPortOpen(p.addr) {
			return p.scheme + "://" + p.addr
		}
	}
	return ""
}

func isPortOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 30*time.Millisecond)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

func probePortProtocol(addr string) string {
	conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
	if err != nil {
		return "http"
	}
	defer conn.Close()

	_, _ = conn.Write([]byte{0x05, 0x01, 0x00})
	
	_ = conn.SetReadDeadline(time.Now().Add(50*time.Millisecond))
	buf := make([]byte, 2)
	n, err := conn.Read(buf)
	if err == nil && n == 2 && buf[0] == 0x05 && buf[1] == 0x00 {
		return "socks5"
	}
	return "http"
}

func isLocalProxy(u *url.URL) bool {
	host := u.Hostname()
	port := u.Port()
	return (host == "127.0.0.1" || host == "localhost" || host == "::1") && port == "18443"
}

// DialContext dials the address using the detected system proxy (HTTP/SOCKS5),
// falling back to a direct connection if no proxy is configured or detection fails.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	start := time.Now()

	logAndReturn := func(conn net.Conn, pUsed string, err error) (net.Conn, error) {
		duration := time.Since(start).Round(time.Millisecond).String()
		status := "SUCCESS"
		if err != nil {
			status = "FAILED: " + err.Error()
		}
		if pUsed == "" {
			pUsed = "DIRECT"
		}
		AddNetworkLog(address, pUsed, duration, status)
		return conn, err
	}

	dummyReq, err := http.NewRequestWithContext(ctx, "CONNECT", "https://"+address, nil)
	if err != nil {
		var d net.Dialer
		conn, errDial := d.DialContext(ctx, network, address)
		return logAndReturn(conn, "DIRECT", errDial)
	}

	proxyURL, err := GetSystemProxy(dummyReq)
	if err != nil || proxyURL == nil {
		var d net.Dialer
		conn, errDial := d.DialContext(ctx, network, address)
		return logAndReturn(conn, "DIRECT", errDial)
	}

	// 核心保护：如果要拨号的目标地址就是代理服务器本身的 Host，强制直连，防止死循环
	if address == proxyURL.Host || strings.HasPrefix(address, proxyURL.Host+":") {
		var d net.Dialer
		conn, errDial := d.DialContext(ctx, network, address)
		return logAndReturn(conn, "DIRECT", errDial)
	}

	pStr := proxyURL.String()

	switch strings.ToLower(proxyURL.Scheme) {
	case "socks5", "socks5h":
		var proxyAuth *proxy.Auth
		if proxyURL.User != nil {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			proxyAuth = &proxy.Auth{User: username, Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, proxyAuth, proxy.Direct)
		if err != nil {
			return logAndReturn(nil, pStr, fmt.Errorf("create SOCKS5 dialer failed: %w", err))
		}
		if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
			conn, errDial := ctxDialer.DialContext(ctx, network, address)
			return logAndReturn(conn, pStr, errDial)
		}
		conn, errDial := dialer.Dial(network, address)
		return logAndReturn(conn, pStr, errDial)

	case "http", "https":
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return logAndReturn(nil, pStr, fmt.Errorf("connect to HTTP proxy %s failed: %w", proxyURL.Host, err))
		}

		// Send CONNECT request to HTTP proxy
		req, err := http.NewRequestWithContext(ctx, "CONNECT", "http://"+address, nil)
		if err != nil {
			conn.Close()
			return logAndReturn(nil, pStr, err)
		}
		req.Header.Set("Proxy-Connection", "Keep-Alive")
		req.Header.Set("Host", address)

		// Basic proxy authentication if user credentials are provided
		if proxyURL.User != nil {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			auth := username + ":" + password
			req.Header.Set("Proxy-Authorization", "Basic "+base64Encode(auth))
		}

		err = req.Write(conn)
		if err != nil {
			conn.Close()
			return logAndReturn(nil, pStr, fmt.Errorf("write CONNECT request to HTTP proxy failed: %w", err))
		}

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			conn.Close()
			return logAndReturn(nil, pStr, fmt.Errorf("read CONNECT response from HTTP proxy failed: %w", err))
		}
		resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			conn.Close()
			return logAndReturn(nil, pStr, fmt.Errorf("HTTP proxy CONNECT failed with status: %s", resp.Status))
		}

		return logAndReturn(conn, pStr, nil)

	default:
		var d net.Dialer
		conn, errDial := d.DialContext(ctx, network, address)
		return logAndReturn(conn, "DIRECT", errDial)
	}
}

func base64Encode(src string) string {
	return base64.StdEncoding.EncodeToString([]byte(src))
}

// IsLocalAddress checks if the host is a local loopback, unspecified, or local interface address.
func IsLocalAddress(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return true
	}
	// Try parsing it as an IP
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return true
		}
		return isIPLocal(ip)
	}

	// Resolve hostname
	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsUnspecified() || isIPLocal(ip) {
				return true
			}
		}
	}
	return false
}

func isIPLocal(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var localIP net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			localIP = v.IP
		case *net.IPAddr:
			localIP = v.IP
		}
		if localIP != nil && localIP.Equal(ip) {
			return true
		}
	}
	return false
}

// NetworkLog defines the structure for tracing outbound connections
type NetworkLog struct {
	Timestamp string `json:"timestamp"`
	Target    string `json:"target"`
	ProxyUsed string `json:"proxyUsed"`
	Duration  string `json:"duration"`
	Status    string `json:"status"`
}

var (
	networkLogs   []NetworkLog
	networkLogsMu sync.RWMutex
)

// AddNetworkLog appends a new outbound network routing trace
func AddNetworkLog(target, proxyUsed, duration, status string) {
	networkLogsMu.Lock()
	defer networkLogsMu.Unlock()

	log := NetworkLog{
		Timestamp: time.Now().Format("15:04:05.000"),
		Target:    target,
		ProxyUsed: proxyUsed,
		Duration:  duration,
		Status:    status,
	}

	// Keep only the latest 100 entries
	if len(networkLogs) >= 100 {
		networkLogs = networkLogs[1:]
	}
	networkLogs = append(networkLogs, log)
}

// GetNetworkLogs retrieves a copy of the recorded outbound network logs
func GetNetworkLogs() []NetworkLog {
	networkLogsMu.RLock()
	defer networkLogsMu.RUnlock()

	res := make([]NetworkLog, len(networkLogs))
	copy(res, networkLogs)
	return res
}

// GetCachedLocalProxy returns the currently detected local loopback proxy URL
func GetCachedLocalProxy() *url.URL {
	cachedLocalProxyMu.RLock()
	defer cachedLocalProxyMu.RUnlock()
	return cachedLocalProxy
}
