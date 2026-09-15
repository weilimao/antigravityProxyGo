package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"antigravity-web-platform/internal/config"
)

type RelayCreatedKey struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Key           string    `json:"key"`
	AllowedModels []string  `json:"allowedModels"`
	CreatedAt     time.Time `json:"createdAt"`
}

type RelayBridgeService struct {
	client *http.Client
}

func NewRelayBridgeService() *RelayBridgeService {
	return &RelayBridgeService{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *RelayBridgeService) getGatewayURL() string {
	cfg := config.GlobalConfig
	url := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	if url == "" {
		url = "http://127.0.0.1:18444"
	}
	return url
}

func (s *RelayBridgeService) getAdminKey() string {
	cfg := config.GlobalConfig
	key := strings.TrimSpace(cfg.Gateway.AdminKey)
	if key == "" {
		key = "sk-ant-admin"
	}
	return key
}

// SyncUserToRelay 桥接同步注册或激活 18444 Relay 用户
func (s *RelayBridgeService) SyncUserToRelay(username, password, remark string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is empty")
	}

	url := fmt.Sprintf("%s/api/admin/users/sync", s.getGatewayURL())
	payload := map[string]string{
		"username": username,
		"password": password,
		"remark":   remark,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.getAdminKey())

	resp, err := s.client.Do(req)
	if err != nil {
		// 网关若未启动或网络抖动，返回错误供上层记录，但不致命阻断本地注册
		return fmt.Errorf("connect to relay gateway failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay gateway returned status %d", resp.StatusCode)
	}
	return nil
}

// CreateKeyOnRelay 桥接在 18444 Relay 服务端为指定用户生成受控 API Key
func (s *RelayBridgeService) CreateKeyOnRelay(username, name, customKey string, allowedModels []string) (*RelayCreatedKey, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	url := fmt.Sprintf("%s/api/admin/keys/create", s.getGatewayURL())
	payload := map[string]interface{}{
		"username":      username,
		"name":          name,
		"key":           customKey,
		"allowedModels": allowedModels,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.getAdminKey())

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request relay gateway failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay gateway returned status %d", resp.StatusCode)
	}

	var res struct {
		Success bool            `json:"success"`
		Key     RelayCreatedKey `json:"key"`
		Error   string          `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode relay gateway response failed: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("relay gateway error: %s", res.Error)
	}

	return &res.Key, nil
}

// DeleteKeyOnRelay 桥接在 18444 Relay 服务端删除指定 API Key
func (s *RelayBridgeService) DeleteKeyOnRelay(username, keyStr string) error {
	username = strings.TrimSpace(username)
	keyStr = strings.TrimSpace(keyStr)
	if username == "" || keyStr == "" {
		return nil
	}

	url := fmt.Sprintf("%s/api/admin/keys/delete", s.getGatewayURL())
	payload := map[string]string{
		"username": username,
		"key":      keyStr,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.getAdminKey())

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request relay gateway failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay gateway returned status %d", resp.StatusCode)
	}
	return nil
}
