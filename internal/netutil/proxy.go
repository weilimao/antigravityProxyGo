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
	"strings"
	"time"

	"github.com/mattn/go-ieproxy"
	"golang.org/x/net/proxy"
)

// NewTransport returns a new http.Transport configured with system proxy auto-detection
// and insecure skip verification (since we are doing local proxy and decrypting traffic).
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: GetSystemProxy,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100, // 高并发核心：复用空闲连接，避免频繁关闭
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
}

// GetSystemProxy returns the system proxy configured in Windows registry, environment variables, etc.
// Proactive local VPN scanning (like scanning 7890/7897) has been removed to allow direct routing via virtual network adapter/TUN interface,
// eliminating connection establishment latency.
func GetSystemProxy(req *http.Request) (*url.URL, error) {
	// Resolve system proxy using go-ieproxy (handles PAC, WPAD, bypass lists, registry, macOS CFNetwork)
	proxyURL, err := ieproxy.GetProxyFunc()(req)
	if err == nil && proxyURL != nil {
		if !isLocalProxy(proxyURL) {
			return proxyURL, nil
		}
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

	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", p.addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return p.scheme + "://" + p.addr
		}
	}
	return ""
}

func isLocalProxy(u *url.URL) bool {
	host := u.Hostname()
	port := u.Port()
	return (host == "127.0.0.1" || host == "localhost" || host == "::1") && port == "18443"
}

// DialContext dials the address using the detected system proxy (HTTP/SOCKS5),
// falling back to a direct connection if no proxy is configured or detection fails.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dummyReq, err := http.NewRequestWithContext(ctx, "CONNECT", "https://"+address, nil)
	if err != nil {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	proxyURL, err := GetSystemProxy(dummyReq)
	if err != nil || proxyURL == nil {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	switch strings.ToLower(proxyURL.Scheme) {
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		}
		if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
			return ctxDialer.DialContext(ctx, network, address)
		}
		return dialer.Dial(network, address)

	case "http", "https":
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return nil, fmt.Errorf("connect to HTTP proxy %s failed: %w", proxyURL.Host, err)
		}

		// Send CONNECT request to HTTP proxy
		req, err := http.NewRequestWithContext(ctx, "CONNECT", "http://"+address, nil)
		if err != nil {
			conn.Close()
			return nil, err
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
			return nil, fmt.Errorf("write CONNECT request to HTTP proxy failed: %w", err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("read CONNECT response from HTTP proxy failed: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			conn.Close()
			return nil, fmt.Errorf("HTTP proxy CONNECT failed with status: %s", resp.Status)
		}

		return conn, nil

	default:
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
}

func base64Encode(src string) string {
	return base64.StdEncoding.EncodeToString([]byte(src))
}
