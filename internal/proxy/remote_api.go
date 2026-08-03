package proxy

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"antigravity-proxy/internal/db"
)

// remote_api.go: 从 remote.go 按职责拆分而出,REST API 方法集合。
// 仅作物理搬移,逻辑与原文件逐行等价;同包内共享符号。

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

