package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	UserKey   string `json:"userKey"`
	Token     string `json:"token"`
	Connected bool   `json:"connected"`
	IsLocal   bool   `json:"isLocal"`
}

// noProxyClient is a custom HTTP client that explicitly bypasses system proxy settings.
// This is critical for local testing and health checks to prevent routing loops back into our own proxy port (18443).
var noProxyClient = &http.Client{
	Transport: &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			proxyURL, err := http.ProxyFromEnvironment(req)
			if err != nil || proxyURL == nil {
				return nil, err
			}
			host := proxyURL.Hostname()
			port := proxyURL.Port()
			// 仅当系统代理指向本程序的拦截端口 18443 时，才予以绕过直连，防止本地测试死循环
			if (host == "127.0.0.1" || host == "localhost" || host == "::1") && port == "18443" {
				return nil, nil
			}
			return proxyURL, nil
		},
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// RemoteRelay manages client-side connection to a remote proxy relay server
type RemoteRelay struct {
	sync.RWMutex
	config RemoteConfig
	logFn  func(string)
}

// NewRemoteRelay creates a new RemoteRelay instance
func NewRemoteRelay(logFn func(string)) *RemoteRelay {
	return &RemoteRelay{
		logFn: logFn,
	}
}

// Login authenticates with the remote relay server and stores the session token
func (rr *RemoteRelay) Login(host, port, key, password string) error {
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if err := rr.TestConnection(host, port); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}

	loginURL := fmt.Sprintf("http://%s:%s/api/auth/login", host, port)
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
		UserKey:   key,
		Token:     result.Token,
		Connected: true,
		IsLocal:   netutil.IsLocalAddress(host),
	}
	rr.Unlock()

	if rr.logFn != nil {
		rr.logFn(fmt.Sprintf("✅ Remote relay connected to %s:%s", host, port))
	}
	return nil
}

// Disconnect logs out from the remote relay server and clears the session
func (rr *RemoteRelay) Disconnect() {
	rr.Lock()
	wasConnected := rr.config.Connected
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.config = RemoteConfig{}
	rr.Unlock()

	if wasConnected && host != "" && token != "" {
		// Best-effort logout
		logoutURL := fmt.Sprintf("http://%s:%s/api/auth/logout", host, port)
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

// DialThroughRemote establishes a TCP tunnel through the remote relay server
func (rr *RemoteRelay) DialThroughRemote(targetHostPort string) (net.Conn, error) {
	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.RUnlock()

	if port == "" {
		port = "18444"
	}
	remoteAddr := net.JoinHostPort(host, port)

	conn, err := net.DialTimeout("tcp", remoteAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to remote relay %s: %w", remoteAddr, err)
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Bearer %s\r\n\r\n", targetHostPort, targetHostPort, token)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send CONNECT request: %w", err)
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
		return nil, fmt.Errorf("remote relay CONNECT failed with status %d", resp.StatusCode)
	}

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

	url := fmt.Sprintf("http://%s:%s/api/keys", config.Host, config.Port)
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

	url := fmt.Sprintf("http://%s:%s/api/keys", config.Host, config.Port)
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

	url := fmt.Sprintf("http://%s:%s/api/keys/%s", config.Host, config.Port, id)
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

// FetchRemoteStats retrieves statistics from the remote relay server
func (rr *RemoteRelay) FetchRemoteStats() (map[string]interface{}, error) {
	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.RUnlock()

	statsURL := fmt.Sprintf("http://%s:%s/api/stats", host, port)
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
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.RUnlock()

	trendsURL := fmt.Sprintf("http://%s:%s/api/trends", host, port)
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
func (rr *RemoteRelay) TestConnection(host, port string) error {
	if host == "localhost" {
		host = "127.0.0.1"
	}
	healthURL := fmt.Sprintf("http://%s:%s/api/health", host, port)
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
	rr.RUnlock()

	if port == "" {
		port = "18444"
	}

	certURL := fmt.Sprintf("http://%s:%s/api/cert", host, port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create cert request: %w", err)
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

	if rr.logFn != nil {
		rr.logFn(fmt.Sprintf("📜 Remote CA cert saved to %s", savePath))
	}
	return nil
}

// FetchAndSaveRemoteLogDetail fetches a specific log from the remote server by req_id and saves it locally
func (rr *RemoteRelay) FetchAndSaveRemoteLogDetail(reqID string, userKey string) error {
	if f, err := os.OpenFile(`B:\antigravityProxy\data\debug.log`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		f.WriteString(fmt.Sprintf("[%s] FetchAndSaveRemoteLogDetail start: reqID=%s, userKey=%s\n", time.Now().Format(time.RFC3339), reqID, userKey))
		f.Close()
	}

	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.RUnlock()

	detailURL := fmt.Sprintf("http://%s:%s/api/logs/detail?req_id=%s", host, port, reqID)
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
		dbErr := db.InsertRequestLog(item)
		if f, err := os.OpenFile(`B:\antigravityProxy\data\debug.log`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			f.WriteString(fmt.Sprintf("[%s] FetchAndSaveRemoteLogDetail: Log fetched successfully, InsertRequestLog error: %v\n", time.Now().Format(time.RFC3339), dbErr))
			f.Close()
		}
	} else {
		if f, err := os.OpenFile(`B:\antigravityProxy\data\debug.log`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			f.WriteString(fmt.Sprintf("[%s] FetchAndSaveRemoteLogDetail: data.Log is nil!\n", time.Now().Format(time.RFC3339)))
			f.Close()
		}
	}

	return nil
}

// Compile-time interface compliance check
var _ RemoteRelayInterface = (*RemoteRelay)(nil)
