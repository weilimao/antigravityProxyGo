package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchRemoteFullSync 从远端服务器拉取完整配置文件集
func (rr *RemoteRelay) FetchRemoteFullSync() (map[string]string, error) {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return nil, fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/sync/full")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)

	resp, err := doRemoteHTTP(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Success bool              `json:"success"`
		Files   map[string]string `json:"files"`
		Error   string            `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Success {
		return nil, fmt.Errorf("remote sync failed: %s", result.Error)
	}
	return result.Files, nil
}

// PushRemoteSync 向远端服务器推送单个配置文件内容
func (rr *RemoteRelay) PushRemoteSync(filename, content string) error {
	rr.RLock()
	config := rr.config
	rr.RUnlock()

	if !config.Connected {
		return fmt.Errorf("not connected to remote relay")
	}

	url := rr.buildURL("/api/sync/push")
	payload := map[string]string{
		"filename": filename,
		"content":  content,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := doRemoteHTTP(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("remote push failed: %s", result.Error)
	}
	return nil
}
