package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/model"
)

type GatewaySyncService struct{}

func NewGatewaySyncService() *GatewaySyncService {
	return &GatewaySyncService{}
}

// CheckGatewayHealth 探测现网已部署 Go Relay 网关健康状态
func (s *GatewaySyncService) CheckGatewayHealth() (bool, error) {
	cfg := config.GlobalConfig
	url := fmt.Sprintf("%s/api/health", strings.TrimRight(cfg.Gateway.GatewayURL, "/"))

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// SyncModelMappingsToGateway 将当前 Web 平台的模型映射配置推送到现网 Go 网关
func (s *GatewaySyncService) SyncModelMappingsToGateway(mappings []model.ModelMappingEntry) error {
	cfg := config.GlobalConfig
	if !cfg.Gateway.SyncEnabled {
		return nil
	}

	url := fmt.Sprintf("%s/api/models/mapping", strings.TrimRight(cfg.Gateway.GatewayURL, "/"))
	body := map[string]interface{}{
		"mappings": mappings,
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Gateway.AdminKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Gateway.AdminKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("推送模型映射至网关失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("网关响应异常状态码: %d", resp.StatusCode)
	}

	return nil
}

// FetchGatewayModels 从现网 Go Relay 服务端或本地桌面端配置提取全部大模型列表（支持 HTTP + 本地双通道自愈）
func (s *GatewaySyncService) FetchGatewayModels() ([]string, error) {
	cfg := config.GlobalConfig
	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	modelSet := make(map[string]struct{})

	// 1. 通道一：通过 HTTP 探测现网 Go Relay 网关端点 (/route/v1/models 与 /v1/models)
	if baseURL != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		token := strings.TrimSpace(cfg.Gateway.AdminKey)
		if token == "" {
			token = "sk-ant-admin"
		}

		endpoints := []string{"/route/v1/models", "/v1/models"}
		for _, ep := range endpoints {
			modelsURL := fmt.Sprintf("%s%s", baseURL, ep)
			req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
			if err != nil {
				continue
			}
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			defer resp.Body.Close()

			// 若 token 校验失败，尝试用官方直通 key 重试一次
			if resp.StatusCode == http.StatusUnauthorized && token != "sk-ant-admin" {
				reqRetry, err := http.NewRequest(http.MethodGet, modelsURL, nil)
				if err == nil {
					reqRetry.Header.Set("Authorization", "Bearer sk-ant-admin")
					if respRetry, err := client.Do(reqRetry); err == nil {
						defer respRetry.Body.Close()
						if respRetry.StatusCode == http.StatusOK {
							resp = respRetry
						}
					}
				}
			}

			if resp.StatusCode == http.StatusOK {
				var res struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if json.NewDecoder(resp.Body).Decode(&res) == nil {
					for _, item := range res.Data {
						id := strings.TrimSpace(item.ID)
						if id != "" {
							modelSet[id] = struct{}{}
						}
					}
				}
			}
			// 若当前端点已获取到模型，无需重复请求备用端点
			if len(modelSet) > 0 {
				break
			}
		}

		// 尝试从网关模型映射接口 /api/models/mapping 提取补充
		mappingURL := fmt.Sprintf("%s/api/models/mapping", baseURL)
		reqMap, err := http.NewRequest(http.MethodGet, mappingURL, nil)
		if err == nil {
			reqMap.Header.Set("Authorization", "Bearer "+token)
			if respMap, err := client.Do(reqMap); err == nil {
				defer respMap.Body.Close()
				if respMap.StatusCode == http.StatusOK {
					var res struct {
						Success  bool `json:"success"`
						Mappings []struct {
							ClientModel     string   `json:"clientModel"`
							TargetModel     string   `json:"targetModel"`
							CandidateModels []string `json:"candidateModels"`
						} `json:"mappings"`
					}
					if json.NewDecoder(respMap.Body).Decode(&res) == nil && res.Success {
						for _, m := range res.Mappings {
							if m.ClientModel != "" {
								modelSet[m.ClientModel] = struct{}{}
							}
							if m.TargetModel != "" && !strings.Contains(m.TargetModel, "/") {
								modelSet[m.TargetModel] = struct{}{}
							}
							for _, c := range m.CandidateModels {
								c = strings.TrimSpace(c)
								if c != "" {
									modelSet[c] = struct{}{}
								}
							}
						}
					}
				}
			}
		}
	}

	// 2. 通道二：本地桌面端/服务端持久化配置自动探测与融合
	// 覆盖 Windows APPDATA 与 Linux/macOS 用户目录下的 config.json
	var searchPaths []string
	if appData := os.Getenv("APPDATA"); appData != "" {
		searchPaths = append(searchPaths, filepath.Join(appData, "antigravity-proxy-desktop", "config.json"))
		searchPaths = append(searchPaths, filepath.Join(appData, "antigravity-proxy", "config.json"))
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		searchPaths = append(searchPaths, filepath.Join(userProfile, ".config", "antigravity-proxy", "config.json"))
	}
	if home := os.Getenv("HOME"); home != "" {
		searchPaths = append(searchPaths, filepath.Join(home, ".config", "antigravity-proxy", "config.json"))
		searchPaths = append(searchPaths, filepath.Join(home, ".config", "antigravity-proxy-desktop", "config.json"))
	}

	for _, p := range searchPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var localCfg struct {
			RelayModelMapping []struct {
				ClientModel     string   `json:"clientModel"`
				TargetModel     string   `json:"targetModel"`
				CandidateModels []string `json:"candidateModels"`
			} `json:"relayModelMapping"`
			RelayChannelModelsSnapshot map[string][]string `json:"relayChannelModelsSnapshot"`
		}
		if json.Unmarshal(data, &localCfg) == nil {
			for _, entry := range localCfg.RelayModelMapping {
				if entry.ClientModel != "" {
					modelSet[entry.ClientModel] = struct{}{}
				}
				if entry.TargetModel != "" && !strings.Contains(entry.TargetModel, "/") {
					modelSet[entry.TargetModel] = struct{}{}
				}
				for _, c := range entry.CandidateModels {
					c = strings.TrimSpace(c)
					if c != "" {
						modelSet[c] = struct{}{}
					}
				}
			}
			for _, snapList := range localCfg.RelayChannelModelsSnapshot {
				for _, sm := range snapList {
					sm = strings.TrimSpace(sm)
					if sm != "" {
						modelSet[sm] = struct{}{}
					}
				}
			}
		}
	}

	if len(modelSet) == 0 {
		return nil, fmt.Errorf("未能从网关或本地配置获取到有效模型")
	}

	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	return models, nil
}

// GetGatewayAutoConfig 从现网 Go Relay 网关拉取最新的 Auto 竞速配置
func (s *GatewaySyncService) GetGatewayAutoConfig() (*model.AutoRacingConfig, error) {
	cfg := config.GlobalConfig
	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	token := strings.TrimSpace(cfg.Gateway.AdminKey)
	if token == "" {
		token = "sk-ant-admin"
	}

	if baseURL != "" {
		url := fmt.Sprintf("%s/api/models/auto-config", baseURL)
		client := &http.Client{Timeout: 3 * time.Second}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var res struct {
						Success bool                   `json:"success"`
						Config  model.AutoRacingConfig `json:"config"`
					}
					if json.NewDecoder(resp.Body).Decode(&res) == nil && res.Success {
						if len(res.Config.CandidateModels) > 0 {
							return &res.Config, nil
						}
					}
				}
			}
		}
	}

	return &model.AutoRacingConfig{
		Enabled:          false,
		CandidateModels:  []string{},
		UseBenchmarkPool: false,
	}, nil
}

func containsString(arr []string, target string) bool {
	for _, s := range arr {
		if s == target {
			return true
		}
	}
	return false
}

// SyncAutoConfigToGateway 将当前 Web 平台的 Auto 竞速配置推送到现网 Go 网关
func (s *GatewaySyncService) SyncAutoConfigToGateway(autoCfg *model.AutoRacingConfig) error {
	cfg := config.GlobalConfig
	if !cfg.Gateway.SyncEnabled {
		return nil
	}

	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	if baseURL == "" {
		return nil
	}

	url := fmt.Sprintf("%s/api/models/auto-config", baseURL)
	body := map[string]interface{}{
		"config": autoCfg,
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Gateway.AdminKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Gateway.AdminKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("推送 Auto 竞速配置至网关失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("网关响应异常状态码: %d", resp.StatusCode)
	}

	return nil
}

// GetGatewayModelMappings 从远端 18444 服务端网关拉取当前生效的模型映射配置
func (s *GatewaySyncService) GetGatewayModelMappings() ([]model.ModelMappingEntry, error) {
	cfg := config.GlobalConfig
	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("未配置网关服务地址")
	}

	url := fmt.Sprintf("%s/api/models/mapping", baseURL)
	token := strings.TrimSpace(cfg.Gateway.AdminKey)
	if token == "" {
		token = "sk-ant-admin"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求网关模型映射失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网关响应异常状态码: %d", resp.StatusCode)
	}

	var res struct {
		Success  bool                     `json:"success"`
		Mappings []model.ModelMappingEntry `json:"mappings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("解析网关模型映射响应失败: %w", err)
	}

	return res.Mappings, nil
}

// GetGatewayOcrModel 从远端 18444 服务端网关拉取当前生效的 OCR 降级模型
func (s *GatewaySyncService) GetGatewayOcrModel() (string, []string, error) {
	cfg := config.GlobalConfig
	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	if baseURL == "" {
		return "", nil, fmt.Errorf("未配置网关服务地址")
	}

	url := fmt.Sprintf("%s/api/admin/settings/ocr", baseURL)
	token := strings.TrimSpace(cfg.Gateway.AdminKey)
	if token == "" {
		token = "sk-ant-admin"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("请求网关 OCR 模型失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", nil, fmt.Errorf("远端服务端网关尚未支持 OCR 降级管理接口(404)，请更新或重启 18444 网关服务至最新版本")
		}
		return "", nil, fmt.Errorf("网关响应异常状态码: %d", resp.StatusCode)
	}

	var res struct {
		Success   bool     `json:"success"`
		OcrModel  string   `json:"ocrModel"`
		OcrModels []string `json:"ocrModels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", nil, fmt.Errorf("解析网关 OCR 响应失败: %w", err)
	}

	return res.OcrModel, res.OcrModels, nil
}

// SyncOcrModelToGateway 将 OCR 降级模型配置推送到远端 18444 服务端网关热重载
func (s *GatewaySyncService) SyncOcrModelToGateway(ocrModel string, ocrModels []string) error {
	cfg := config.GlobalConfig
	if !cfg.Gateway.SyncEnabled {
		return nil
	}

	baseURL := strings.TrimRight(cfg.Gateway.GatewayURL, "/")
	if baseURL == "" {
		return nil
	}

	url := fmt.Sprintf("%s/api/admin/settings/ocr", baseURL)
	body := map[string]interface{}{
		"ocrModel":  ocrModel,
		"ocrModels": ocrModels,
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	token := strings.TrimSpace(cfg.Gateway.AdminKey)
	if token == "" {
		token = "sk-ant-admin"
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("推送 OCR 模型至网关失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("远端服务端网关尚未支持 OCR 热同步接口(404)，请更新或重启 18444 网关服务至最新版本")
		}
		return fmt.Errorf("网关响应异常状态码: %d", resp.StatusCode)
	}

	return nil
}

