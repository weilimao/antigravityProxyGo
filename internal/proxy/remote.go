package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/netutil"
)

// RemoteConfig holds the configuration for connecting to a remote relay server
type RemoteConfig struct {
	Host      string `json:"host"`
	Port      string `json:"port"`
	Path      string `json:"path"`
	UserKey   string `json:"userKey"`
	Token     string `json:"token"`
	Connected bool   `json:"connected"`
	IsLocal   bool   `json:"isLocal"`
}

// remoteCACertPool holds the trusted CA certificate pool for the remote relay server.
// When populated (after DownloadCACert), the TLS client will verify the server certificate
// against this pool instead of blindly skipping verification.
var (
	remoteCACertPool   *x509.CertPool
	remoteCACertPoolMu sync.RWMutex
)

// setRemoteCACertPool updates the global CA cert pool for remote relay TLS verification.
func setRemoteCACertPool(pool *x509.CertPool) {
	remoteCACertPoolMu.Lock()
	remoteCACertPool = pool
	remoteCACertPoolMu.Unlock()
}

// getRemoteTLSConfig returns a TLS config that trusts the remote relay's CA cert when available.
// Falls back to InsecureSkipVerify only if no CA cert has been loaded yet (e.g., initial health check).
func getRemoteTLSConfig(serverName string) *tls.Config {
	remoteCACertPoolMu.RLock()
	pool := remoteCACertPool
	remoteCACertPoolMu.RUnlock()

	if pool != nil {
		return &tls.Config{
			RootCAs:    pool,
			ServerName: serverName,
		}
	}
	// Fallback: no CA cert loaded yet, allow insecure (for initial test/health check connections)
	return &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         serverName,
	}
}

// noProxyClient is a custom HTTP client that explicitly bypasses system proxy settings.
// This is critical for local testing and health checks to prevent routing loops back into our own proxy port (18443).
var noProxyClient = &http.Client{
	Transport: &http.Transport{
		Proxy:             netutil.GetSystemProxy, // 使用我们重构的系统代理获取函数（自动规避18443，且具备本地代理探测与专属代理）
		DialContext:       netutil.DialContext,    // 改用自定义的 DialContext 以支持 SOCKS5 拨号和代理自适应
		DisableKeepAlives: true,                   // 禁用连接复用与连接池，防止中继代理死连接残留卡死
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       getRemoteTLSConfig(""),
	},
}

// RemoteRelay manages client-side connection to a remote proxy relay server
type RemoteRelay struct {
	sync.RWMutex
	config          RemoteConfig
	logFn           func(string)
	onTokenExpired  func() // called when a 401/407 is received, to trigger auto-relogin

	// 预热连接池：预先建立 TCP+TLS 连接，消除首次请求冷启动延迟
	warmPool     chan net.Conn
	warmPoolDone chan struct{}
	warmPoolMu   sync.Mutex
}

// NewRemoteRelay creates a new RemoteRelay instance
func NewRemoteRelay(logFn func(string)) *RemoteRelay {
	return &RemoteRelay{
		logFn: logFn,
	}
}

// SetOnTokenExpired registers a callback that is invoked when the token is expired (401/407 received).
func (rr *RemoteRelay) SetOnTokenExpired(fn func()) {
	rr.Lock()
	rr.onTokenExpired = fn
	rr.Unlock()
}

func (rr *RemoteRelay) buildURL(endpoint string) string {
	rr.RLock()
	config := rr.config
	rr.RUnlock()
	return buildURLWithConfig(config.Host, config.Port, config.Path, endpoint)
}

func buildURLWithConfig(host, port, path, endpoint string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, "/")
	scheme := "http"
	// 若传入的 host 含有协议前缀，则提取协议并剥除前缀，保证拼接 URL 的规范性
	if strings.HasPrefix(host, "https://") {
		scheme = "https"
		host = strings.TrimPrefix(host, "https://")
	} else if strings.HasPrefix(host, "http://") {
		scheme = "http"
		host = strings.TrimPrefix(host, "http://")
	}

	path = strings.TrimSpace(path)
	if path != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		path = strings.TrimSuffix(path, "/")
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	hostPort := host
	if port != "" {
		hostPort = host + ":" + port
	}
	return fmt.Sprintf("%s://%s%s%s", scheme, hostPort, path, endpoint)
}

// Login authenticates with the remote relay server and stores the session token
func (rr *RemoteRelay) Login(host, port, path, key, password string) error {
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if err := rr.TestConnection(host, port, path); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}

	loginURL := buildURLWithConfig(host, port, path, "/api/auth/login")
	payload := map[string]string{
		"key":      key,
		"password": password,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal login payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read login response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse login response: %w", err)
	}
	if result.Token == "" {
		return fmt.Errorf("login response missing token")
	}

	rr.Lock()
	rr.config = RemoteConfig{
		Host:      host,
		Port:      port,
		Path:      path,
		UserKey:   key,
		Token:     result.Token,
		Connected: true,
		IsLocal:   netutil.IsLocalAddress(host),
	}
	rr.Unlock()

	if rr.logFn != nil {
		rr.logFn(fmt.Sprintf("✅ Remote relay connected to %s:%s%s", host, port, path))
	}

	// 登录成功后启动连接预热池，消除后续首次请求的冷启动延迟
	rr.StartWarmPool()

	return nil
}

// Disconnect logs out from the remote relay server and clears the session
func (rr *RemoteRelay) Disconnect() {
	// 先停止预热连接池，释放所有预建连接
	rr.StopWarmPool()

	rr.Lock()
	wasConnected := rr.config.Connected
	host := rr.config.Host
	port := rr.config.Port
	path := rr.config.Path
	token := rr.config.Token
	rr.config = RemoteConfig{}
	rr.Unlock()

	if wasConnected && host != "" && token != "" {
		// Best-effort logout
		logoutURL := buildURLWithConfig(host, port, path, "/api/auth/logout")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, logoutURL, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			_, _ = noProxyClient.Do(req)
		}
	}

	if rr.logFn != nil {
		rr.logFn("🔌 Remote relay disconnected")
	}
}

// IsConnected returns whether the relay is currently connected
func (rr *RemoteRelay) IsConnected() bool {
	rr.RLock()
	defer rr.RUnlock()
	return rr.config.Connected
}

// GetConfig returns a copy of the current remote configuration
func (rr *RemoteRelay) GetConfig() RemoteConfig {
	rr.RLock()
	defer rr.RUnlock()
	return rr.config
}

// dialRelayRaw establishes a raw TCP (and optionally TLS) connection to the remote relay server,
// without sending the CONNECT request. This is used for connection pre-warming and by DialThroughRemote.
func (rr *RemoteRelay) dialRelayRaw() (net.Conn, error) {
	rr.RLock()
	rawHost := rr.config.Host
	port := rr.config.Port
	rr.RUnlock()

	// 剥离协议前缀以进行纯 TCP 物理拨号
	isHTTPS := false
	host := strings.TrimSpace(rawHost)
	host = strings.TrimSuffix(host, "/")
	if strings.HasPrefix(host, "https://") {
		isHTTPS = true
		host = strings.TrimPrefix(host, "https://")
	} else if strings.HasPrefix(host, "http://") {
		host = strings.TrimPrefix(host, "http://")
	}

	if port == "" {
		if isHTTPS {
			port = "443"
		} else if strings.HasPrefix(rawHost, "http://") {
			port = "80"
		} else {
			port = "18444"
		}
	}
	remoteAddr := net.JoinHostPort(host, port)

	// 改用 netutil.DialContext 代替 net.DialTimeout，使其能走系统/本地检测代理，避免被虚拟网卡黑洞化
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := netutil.DialContext(ctx, "tcp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to remote relay %s: %w", remoteAddr, err)
	}

	// 若远程中继使用 HTTPS，则在发送明文 CONNECT 前先完成 TLS 握手升级
	if isHTTPS {
		tlsConfig := getRemoteTLSConfig(host)
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed remote relay TLS handshake: %w", err)
		}
		conn = tlsConn
	}

	return conn, nil
}

// StartWarmPool 启动后台预热连接池。在 Login 成功后或重连后调用。
// 预热池会预建 TCP+TLS 连接到远程中继服务器，使首次请求跳过耗时的建连握手阶段。
func (rr *RemoteRelay) StartWarmPool() {
	rr.warmPoolMu.Lock()
	defer rr.warmPoolMu.Unlock()

	// 清理已有的旧池（幂等安全，支持 re-login 场景）
	rr.stopWarmPoolLocked()

	rr.warmPool = make(chan net.Conn, 3)
	rr.warmPoolDone = make(chan struct{})

	go rr.maintainWarmPool()
}

// StopWarmPool 停止预热连接池并排空所有预建连接。
func (rr *RemoteRelay) StopWarmPool() {
	rr.warmPoolMu.Lock()
	defer rr.warmPoolMu.Unlock()
	rr.stopWarmPoolLocked()
}

// stopWarmPoolLocked 内部实现，必须在持有 warmPoolMu 锁时调用。
func (rr *RemoteRelay) stopWarmPoolLocked() {
	if rr.warmPoolDone != nil {
		select {
		case <-rr.warmPoolDone:
			// 已经关闭
		default:
			close(rr.warmPoolDone)
		}
	}
	// 排空并关闭池中所有连接
	if rr.warmPool != nil {
		for {
			select {
			case conn := <-rr.warmPool:
				if conn != nil {
					conn.Close()
				}
			default:
				rr.warmPool = nil
				return
			}
		}
	}
}

// getWarmConn 非阻塞地从预热池中获取一个连接。
// 如果池为空或连接已失效，返回 nil。
func (rr *RemoteRelay) getWarmConn() net.Conn {
	rr.warmPoolMu.Lock()
	pool := rr.warmPool
	rr.warmPoolMu.Unlock()

	if pool == nil {
		return nil
	}

	select {
	case conn := <-pool:
		// 快速验证连接存活性：如果 SetDeadline 返回错误，说明底层连接已断开
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			conn.Close()
			return nil
		}
		// 重置 deadline，交由后续 CONNECT 逻辑控制超时
		_ = conn.SetDeadline(time.Time{})
		return conn
	default:
		return nil
	}
}

// replenishPool 异步补充预热连接池（在每次消耗连接后调用）。
func (rr *RemoteRelay) replenishPool() {
	rr.warmPoolMu.Lock()
	pool := rr.warmPool
	done := rr.warmPoolDone
	rr.warmPoolMu.Unlock()

	if pool == nil || done == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}

	// 仅在池未满时补充
	if len(pool) >= 2 {
		return
	}

	conn, err := rr.dialRelayRaw()
	if err != nil {
		return
	}

	select {
	case pool <- conn:
	case <-done:
		conn.Close()
	default:
		conn.Close() // 池已满
	}
}

// maintainWarmPool 后台维护预热连接池的 goroutine。
func (rr *RemoteRelay) maintainWarmPool() {
	if rr.logFn != nil {
		rr.logFn("🔥 [预热] 正在预建远程中继连接池...")
	}

	// 初始突发：预建 2 个连接
	established := 0
	for i := 0; i < 2; i++ {
		select {
		case <-rr.warmPoolDone:
			return
		default:
		}
		conn, err := rr.dialRelayRaw()
		if err != nil {
			if rr.logFn != nil {
				rr.logFn(fmt.Sprintf("⚠️ [预热] 预建连接 #%d 失败: %v", i+1, err))
			}
			continue
		}
		select {
		case rr.warmPool <- conn:
			established++
		case <-rr.warmPoolDone:
			conn.Close()
			return
		default:
			conn.Close()
		}
	}

	if rr.logFn != nil {
		rr.logFn(fmt.Sprintf("✅ [预热] 远程中继连接池已就绪 (%d 个连接已预建)", established))
	}

	// 维护循环：每 30 秒检查并补充池
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rr.warmPoolDone:
			return
		case <-ticker.C:
			rr.replenishPool()
		}
	}
}

// DialThroughRemote establishes a TCP tunnel through the remote relay server.
// It first attempts to use a pre-warmed connection from the pool to eliminate cold-start latency.
// Falls back to creating a new connection if the pool is empty or the warm connection is stale.
func (rr *RemoteRelay) DialThroughRemote(targetHostPort string) (net.Conn, error) {
	rr.RLock()
	token := rr.config.Token
	rr.RUnlock()

	// 1. 尝试从预热连接池获取已建立好的连接（非阻塞）
	conn := rr.getWarmConn()
	usedWarm := conn != nil

	// 2. 若预热池为空或获取失败，降级为即时新建连接
	if conn == nil {
		var err error
		conn, err = rr.dialRelayRaw()
		if err != nil {
			return nil, err
		}
	}

	// 3. 在已建立的连接上发送 CONNECT 请求（此时连接已就绪，仅需网络往返）
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Bearer %s\r\n\r\n", targetHostPort, targetHostPort, token)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		// 若预热连接已失效，降级新建连接重试一次
		if usedWarm {
			var err2 error
			conn, err2 = rr.dialRelayRaw()
			if err2 != nil {
				return nil, fmt.Errorf("failed to connect to remote relay: %w", err2)
			}
			if _, err3 := conn.Write([]byte(connectReq)); err3 != nil {
				conn.Close()
				return nil, fmt.Errorf("failed to send CONNECT request: %w", err3)
			}
		} else {
			return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
		}
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		conn.Close()
		// Trigger auto-relogin on token expiry (407 Proxy Auth Required)
		if resp.StatusCode == http.StatusProxyAuthRequired || resp.StatusCode == http.StatusUnauthorized {
			rr.RLock()
			cb := rr.onTokenExpired
			rr.RUnlock()
			if cb != nil {
				go cb()
			}
		}
		return nil, fmt.Errorf("remote relay CONNECT failed with status %d", resp.StatusCode)
	}

	// 4. 异步补充预热连接池
	go rr.replenishPool()

	return conn, nil
}


// FetchRemoteKeys fetches the list of API keys from the remote server
func (rr *RemoteRelay) FetchRemoteKeys() (interface{}, error) {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return nil, fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/keys")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result["keys"], nil
}

// CreateRemoteKey creates a new API key on the remote server
func (rr *RemoteRelay) CreateRemoteKey(name string) (interface{}, error) {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return nil, fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/keys")
	payload := map[string]string{"name": name}
	body, _ := json.Marshal(payload)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result["key"], nil
}

// DeleteRemoteKey deletes an API key on the remote server
func (rr *RemoteRelay) DeleteRemoteKey(id string) error {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/keys/" + id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	return nil
}

// UpdateRemoteKeyQuota updates the Gemini and Claude token quotas for a specific API Key on the remote server
func (rr *RemoteRelay) UpdateRemoteKeyQuota(id string, limitGemini, limitClaude int64) error {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/keys/update-quota")
	payload := map[string]interface{}{
		"id":                 id,
		"limitGeminiTokens":  limitGemini,
		"limitClaudeTokens":  limitClaude,
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	return nil
}

// FetchRemoteStats retrieves statistics from the remote relay server
func (rr *RemoteRelay) FetchRemoteStats() (map[string]interface{}, error) {
	rr.RLock()
	token := rr.config.Token
	rr.RUnlock()

	statsURL := rr.buildURL("/api/stats")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create stats request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stats request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read stats response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Trigger auto-relogin on token expiry
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusProxyAuthRequired {
			rr.RLock()
			cb := rr.onTokenExpired
			rr.RUnlock()
			if cb != nil {
				go cb()
			}
		}
		return nil, fmt.Errorf("stats request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse stats response: %w", err)
	}
	return data, nil
}

// FetchAndSyncRemoteLogs retrieves new logs from the remote relay and syncs them to local SQLite
// Deprecated: No longer syncing raw logs.
func (rr *RemoteRelay) FetchAndSyncRemoteLogs(userKey string) error {
	return nil
}

// FetchRemoteTrends retrieves hourly trends from the remote relay server
func (rr *RemoteRelay) FetchRemoteTrends() ([]*db.HourlyTrendSummary, error) {
	rr.RLock()
	token := rr.config.Token
	rr.RUnlock()

	trendsURL := rr.buildURL("/api/trends")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trendsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create trends request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trends request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read trends response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trends request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Trends []*db.HourlyTrendSummary `json:"trends"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse trends response: %w", err)
	}

	return data.Trends, nil
}

// TestConnection verifies connectivity to the remote relay server's health endpoint
func (rr *RemoteRelay) TestConnection(host, port, path string) error {
	if host == "localhost" {
		host = "127.0.0.1"
	}
	healthURL := buildURLWithConfig(host, port, path, "/api/health")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

// DownloadCACert downloads the remote server's CA certificate PEM and saves it to the specified path
func (rr *RemoteRelay) DownloadCACert(savePath string) error {
	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	path := rr.config.Path
	token := rr.config.Token
	rr.RUnlock()

	if port == "" {
		if strings.HasPrefix(host, "https://") {
			port = "443"
		} else if strings.HasPrefix(host, "http://") {
			port = "80"
		} else {
			port = "18444"
		}
	}

	certURL := buildURLWithConfig(host, port, path, "/api/cert")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create cert request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download CA cert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cert download returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read CA cert body: %w", err)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(savePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cert directory: %w", err)
	}

	if err := os.WriteFile(savePath, body, 0644); err != nil {
		return fmt.Errorf("failed to save CA cert: %w", err)
	}

	// Register the downloaded CA cert in the global TLS trust pool
	// so subsequent connections verify the server against this CA
	pool := x509.NewCertPool()
	if pool.AppendCertsFromPEM(body) {
		setRemoteCACertPool(pool)
		if rr.logFn != nil {
			rr.logFn("🔒 Remote CA cert loaded into TLS trust pool")
		}
	}

	if rr.logFn != nil {
		rr.logFn(fmt.Sprintf("📜 Remote CA cert saved to %s", savePath))
	}
	return nil
}

// FetchAndSaveRemoteLogDetail fetches a specific log from the remote server by req_id and saves it locally
func (rr *RemoteRelay) FetchAndSaveRemoteLogDetail(reqID string, userKey string) error {

	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	path := rr.config.Path
	token := rr.config.Token
	rr.RUnlock()

	detailURL := buildURLWithConfig(host, port, path, "/api/logs/detail?req_id="+reqID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create log detail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := noProxyClient.Do(req)
	if err != nil {
		return fmt.Errorf("log detail request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read log detail response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("log detail failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Log *db.RequestLog `json:"log"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse log detail response: %w", err)
	}

	if data.Log != nil {
		item := data.Log
		item.ServerLogID = item.ID // Save remote ID to ServerLogID
		item.ID = 0                // Reset local ID for auto increment
		item.UserID = userKey
		item.Mode = "remote"
		if err := db.InsertRequestLog(item); err != nil {
			if rr.logFn != nil {
				rr.logFn(fmt.Sprintf("⚠️ [RemoteRelay] Failed to insert remote log (reqID=%s): %v", reqID, err))
			}
		}
	}

	return nil
}

// Compile-time interface compliance check
var _ RemoteRelayInterface = (*RemoteRelay)(nil)
