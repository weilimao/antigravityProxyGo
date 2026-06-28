package proxy

import (
	"bufio"
	"context"
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
	UserKey   string `json:"userKey"`
	Token     string `json:"token"`
	Connected bool   `json:"connected"`
	IsLocal   bool   `json:"isLocal"`
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

	resp, err := http.DefaultClient.Do(req)
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
			_, _ = http.DefaultClient.Do(req)
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

	resp, err := http.DefaultClient.Do(req)
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
func (rr *RemoteRelay) FetchAndSyncRemoteLogs(userKey string) error {
	rr.RLock()
	host := rr.config.Host
	port := rr.config.Port
	token := rr.config.Token
	rr.RUnlock()

	maxID := db.GetMaxServerLogID(userKey, "remote")
	syncURL := fmt.Sprintf("http://%s:%s/api/logs/sync?last_id=%d&limit=200", host, port, maxID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, syncURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create log sync request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("log sync request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read log sync response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("log sync failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Logs []*db.RequestLog `json:"logs"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse log sync response: %w", err)
	}

	for _, item := range data.Logs {
		item.ServerLogID = item.ID // Save remote ID to ServerLogID
		item.ID = 0                // Reset local ID for auto increment
		item.UserID = userKey
		item.Mode = "remote"
		_ = db.InsertRequestLog(item)
	}

	return nil
}

// TestConnection verifies connectivity to the remote relay server's health endpoint
func (rr *RemoteRelay) TestConnection(host, port string) error {
	healthURL := fmt.Sprintf("http://%s:%s/api/health", host, port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
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

	resp, err := http.DefaultClient.Do(req)
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

// Compile-time interface compliance check
var _ RemoteRelayInterface = (*RemoteRelay)(nil)
