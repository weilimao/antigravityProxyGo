package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
	// 桥接通信必须直连网关，显式禁用 Proxy 以免受系统环境变量 HTTP_PROXY / HTTPS_PROXY (如本地 18443) 劫持
	tr := &http.Transport{
		Proxy: nil,
	}
	return &RelayBridgeService{
		client: &http.Client{
			Transport: tr,
			Timeout:   10 * time.Second,
		},
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

func (s *RelayBridgeService) isSyncEnabled() bool {
	cfg := config.GlobalConfig
	if cfg == nil {
		return false
	}
	if cfg.Gateway.SyncEnabled {
		return true
	}
	if strings.TrimSpace(cfg.Gateway.GatewayURL) != "" {
		return true
	}
	return false
}

// SyncUserToRelay 桥接同步注册或激活 18444 Relay 用户
func (s *RelayBridgeService) SyncUserToRelay(username, password, remark string, planExpireAt ...int64) error {
	if !s.isSyncEnabled() {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is empty")
	}

	var expireAt int64
	if len(planExpireAt) > 0 {
		expireAt = planExpireAt[0]
	}

	url := fmt.Sprintf("%s/api/admin/users/sync", s.getGatewayURL())
	payload := map[string]interface{}{
		"username":     username,
		"password":     password,
		"remark":       remark,
		"planExpireAt": expireAt,
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

// SyncUserExpireToRelay 桥接同步用户的套餐到期时间至 18444 Relay 网关
func (s *RelayBridgeService) SyncUserExpireToRelay(username string, expireAt int64) error {
	if !s.isSyncEnabled() {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is empty")
	}

	url := fmt.Sprintf("%s/api/admin/users/expire", s.getGatewayURL())
	payload := map[string]interface{}{
		"username": username,
		"expireAt": expireAt,
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

// CreateKeyOnRelay 桥接在 18444 Relay 服务端为指定用户生成受控 API Key（支持可选的总限额 limitTokens 与到期时间 planExpireAt）
func (s *RelayBridgeService) CreateKeyOnRelay(username, name, customKey string, allowedModels []string, limitTokensAndExpire ...int64) (*RelayCreatedKey, error) {
	if !s.isSyncEnabled() {
		return &RelayCreatedKey{
			ID:            "local-" + customKey,
			Name:          name,
			Key:           customKey,
			AllowedModels: allowedModels,
			CreatedAt:     time.Now(),
		}, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	var totalLimit int64
	var planExpireAt int64
	if len(limitTokensAndExpire) > 0 && limitTokensAndExpire[0] > 0 {
		totalLimit = limitTokensAndExpire[0]
	}
	if len(limitTokensAndExpire) > 1 {
		planExpireAt = limitTokensAndExpire[1]
	}

	url := fmt.Sprintf("%s/api/admin/keys/create", s.getGatewayURL())
	payload := map[string]interface{}{
		"username":      username,
		"name":          name,
		"key":           customKey,
		"allowedModels": allowedModels,
		"limitTokens":   totalLimit,
		"planExpireAt":  planExpireAt,
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
	if !s.isSyncEnabled() {
		return nil
	}
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

// FetchUserKeysUsage 桥接从 18444 Relay 服务端拉取指定用户全部 API Key 的最新 Token 消耗
func (s *RelayBridgeService) FetchUserKeysUsage(username string) (map[string]int64, error) {
	if !s.isSyncEnabled() {
		return make(map[string]int64), nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	url := fmt.Sprintf("%s/api/admin/users/keys-usage?username=%s", s.getGatewayURL(), username)
	fmt.Printf("🌐 [FetchUserKeysUsage] Requesting %s ...\n", url)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
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
		Success bool             `json:"success"`
		Usages  map[string]int64 `json:"usages"`
		Error   string           `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode relay response failed: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("relay gateway error: %s", res.Error)
	}

	return res.Usages, nil
}

// ResetUserKeysUsage 桥接请求 18444 Relay 服务端重置指定用户全部 API Key 的已消耗 Token 计数器
func (s *RelayBridgeService) ResetUserKeysUsage(username string) error {
	if !s.isSyncEnabled() {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is empty")
	}

	url := fmt.Sprintf("%s/api/admin/users/keys-usage/reset?username=%s", s.getGatewayURL(), username)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
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

type AdminLogItem struct {
	ID              int64   `json:"id"`
	ReqID           string  `json:"reqId"`
	Timestamp       string  `json:"timestamp"`
	Method          string  `json:"method"`
	Host            string  `json:"host"`
	Path            string  `json:"path"`
	SessionID       string  `json:"sessionId"`
	Model           string  `json:"model"`
	Account         string  `json:"account"`
	InTokens        int     `json:"inTokens"`
	OutTokens       int     `json:"outTokens"`
	CachedTokens    int     `json:"cachedTokens"`
	Cost            float64 `json:"cost"`
	InputCost       float64 `json:"inputCost"`
	OutputCost      float64 `json:"outputCost"`
	CachedCost      float64 `json:"cachedCost"`
	FirstByteMs     int64   `json:"firstByteMs"`
	DurationMs      int64   `json:"durationMs"`
	CacheStatus     string  `json:"cacheStatus"`
	StatusCode      int     `json:"statusCode"`
	Family          string  `json:"family"`
	ReasoningEffort string  `json:"reasoningEffort"`
	RequestBody     string  `json:"requestBody,omitempty"`
	RequestHeaders  string  `json:"requestHeaders,omitempty"`
}

type AdminLogSummary struct {
	TotalRequests     int     `json:"totalRequests"`
	TotalInputTokens  int64   `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalCachedTokens int64   `json:"totalCachedTokens"`
	InputTokens       int64   `json:"inputTokens"`
	OutputTokens      int64   `json:"outputTokens"`
	CachedTokens      int64   `json:"cachedTokens"`
	TotalCost         float64 `json:"totalCost"`
	InputCost         float64 `json:"inputCost"`
	OutputCost        float64 `json:"outputCost"`
	CachedCost        float64 `json:"cachedCost"`
	CacheHitRate      float64 `json:"cacheHitRate"`
}

type ModelPerfStat struct {
	Model       string  `json:"model"`
	Count       int     `json:"count"`
	AvgDuration float64 `json:"avgDurationMs"`
	AvgTTFT     float64 `json:"avgTtftMs"`
}

type AdminLogsResult struct {
	Success   bool            `json:"success"`
	Total     int             `json:"total"`
	Page      int             `json:"page"`
	PageSize  int             `json:"pageSize"`
	Summary   AdminLogSummary `json:"summary"`
	ModelPerf []ModelPerfStat `json:"modelPerf"`
	List      []AdminLogItem  `json:"list"`
	Accounts  []string        `json:"accounts"`
}

// FetchLogs 桥接从 18444 Relay 服务端拉取请求命中日志
func (s *RelayBridgeService) FetchLogs(username, status, search string, page, pageSize int) (*AdminLogsResult, error) {
	baseURL := s.getGatewayURL()
	params := url.Values{}
	if username != "" {
		params.Set("username", username)
	}
	if status != "" {
		params.Set("status", status)
	}
	if search != "" {
		params.Set("search", search)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		params.Set("pageSize", strconv.Itoa(pageSize))
	}

	fullURL := fmt.Sprintf("%s/api/admin/logs?%s", baseURL, params.Encode())
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.getAdminKey())

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request relay gateway failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay gateway returned status %d", resp.StatusCode)
	}

	var res AdminLogsResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode relay response failed: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("relay gateway returned failure")
	}

	// 互通兼容 totalInputTokens 与 inputTokens 两套别名字段
	if res.Summary.InputTokens == 0 && res.Summary.TotalInputTokens > 0 {
		res.Summary.InputTokens = res.Summary.TotalInputTokens
	}
	if res.Summary.OutputTokens == 0 && res.Summary.TotalOutputTokens > 0 {
		res.Summary.OutputTokens = res.Summary.TotalOutputTokens
	}
	if res.Summary.CachedTokens == 0 && res.Summary.TotalCachedTokens > 0 {
		res.Summary.CachedTokens = res.Summary.TotalCachedTokens
	}
	if res.Summary.TotalInputTokens == 0 && res.Summary.InputTokens > 0 {
		res.Summary.TotalInputTokens = res.Summary.InputTokens
	}
	if res.Summary.TotalOutputTokens == 0 && res.Summary.OutputTokens > 0 {
		res.Summary.TotalOutputTokens = res.Summary.OutputTokens
	}
	if res.Summary.TotalCachedTokens == 0 && res.Summary.CachedTokens > 0 {
		res.Summary.TotalCachedTokens = res.Summary.CachedTokens
	}

	return &res, nil
}

// FetchLogDetail 桥接从 18444 Relay 服务端拉取单条请求报文详情
func (s *RelayBridgeService) FetchLogDetail(reqID, id string) (interface{}, error) {
	baseURL := s.getGatewayURL()
	params := url.Values{}
	if reqID != "" {
		params.Set("req_id", reqID)
	}
	if id != "" {
		params.Set("id", id)
	}

	fullURL := fmt.Sprintf("%s/api/admin/logs/detail?%s", baseURL, params.Encode())
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
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
		Success bool        `json:"success"`
		Log     interface{} `json:"log"`
		Error   string      `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode relay response failed: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("relay gateway error: %s", res.Error)
	}

	return res.Log, nil
}

// FetchLogAccounts 桥接从 18444 Relay 服务端拉取全部账号列表
func (s *RelayBridgeService) FetchLogAccounts() ([]string, error) {
	fullURL := fmt.Sprintf("%s/api/admin/logs/accounts", s.getGatewayURL())
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
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
		Success  bool     `json:"success"`
		Accounts []string `json:"accounts"`
		Error    string   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode relay response failed: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("relay gateway error: %s", res.Error)
	}

	return res.Accounts, nil
}

