package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"gorm.io/gorm"
)

type SettingService struct {
	gatewaySync *GatewaySyncService
}

func NewSettingService() *SettingService {
	return &SettingService{
		gatewaySync: NewGatewaySyncService(),
	}
}

// GetOCRModels 读取当前生效的 OCR 多模型竞速候选列表
func (s *SettingService) GetOCRModels() ([]string, error) {
	var setting model.Setting
	err := database.DB.Where("`key` = ?", "ocr_models").First(&setting).Error
	if err == nil && strings.TrimSpace(setting.Value) != "" {
		var list []string
		if jsonErr := json.Unmarshal([]byte(setting.Value), &list); jsonErr == nil {
			return list, nil
		}
	}

	// 兼容旧单值 ocr_model
	single, err := s.GetOCRModel()
	if err != nil {
		return []string{}, err
	}
	if single != "" {
		return []string{single}, nil
	}
	return []string{}, nil
}

// GetOCRModel 读取当前生效的首选 OCR 降级模型(单值兼容)
func (s *SettingService) GetOCRModel() (string, error) {
	var setting model.Setting
	err := database.DB.Where("`key` = ?", "ocr_model").First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(setting.Value), nil
}

// SetOCRModels 持久化保存多模型 OCR 竞速候选池，并同步热推网关
func (s *SettingService) SetOCRModels(models []string) error {
	cleaned := make([]string, 0, len(models))
	seen := make(map[string]struct{})
	for _, raw := range models {
		t := strings.TrimSpace(raw)
		if t != "" {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				cleaned = append(cleaned, t)
			}
		}
	}

	primary := ""
	if len(cleaned) > 0 {
		primary = cleaned[0]
	}

	jsonData, err := json.Marshal(cleaned)
	if err != nil {
		return fmt.Errorf("序列化 OCR 模型列表失败: %w", err)
	}

	// 1. 持久化 ocr_models 列表
	modelsSetting := model.Setting{
		Key:         "ocr_models",
		Value:       string(jsonData),
		Description: "全局 OCR 图片自愈降级模型候选池(并发竞速模式)",
		UpdatedAt:   time.Now(),
	}
	if err := database.DB.Save(&modelsSetting).Error; err != nil {
		return err
	}

	// 2. 兼容持久化 ocr_model 单值
	modelSetting := model.Setting{
		Key:         "ocr_model",
		Value:       primary,
		Description: "全局 OCR 入站图片自愈降级首选模型",
		UpdatedAt:   time.Now(),
	}
	if err := database.DB.Save(&modelSetting).Error; err != nil {
		return err
	}

	// 3. 热同步推送远端 18444 服务端网关
	if s.gatewaySync != nil {
		_ = s.gatewaySync.SyncOcrModelToGateway(primary, cleaned)
	}
	return nil
}

// SetOCRModel 持久化保存单 OCR 降级模型 (兼容旧接口)
func (s *SettingService) SetOCRModel(modelName string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return s.SetOCRModels([]string{})
	}
	return s.SetOCRModels([]string{modelName})
}

// PullOCRModelFromGateway 从网关拉取最新生效的 OCR 模型候选池并落库
func (s *SettingService) PullOCRModelFromGateway() (string, []string, error) {
	if s.gatewaySync == nil {
		return "", nil, fmt.Errorf("网关同步服务未初始化")
	}
	primary, models, err := s.gatewaySync.GetGatewayOcrModel()
	if err != nil {
		return "", nil, err
	}

	if len(models) > 0 {
		_ = s.SetOCRModels(models)
		return primary, models, nil
	} else if primary != "" {
		_ = s.SetOCRModels([]string{primary})
		return primary, []string{primary}, nil
	}

	_ = s.SetOCRModels([]string{})
	return "", []string{}, nil
}

// GetModelMappings 获取全局中继模型映射表
func (s *SettingService) GetModelMappings() ([]model.ModelMappingEntry, error) {
	var setting model.Setting
	err := database.DB.Where("`key` = ?", "model_mappings").First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []model.ModelMappingEntry{}, nil
		}
		return nil, err
	}

	var mappings []model.ModelMappingEntry
	if err := json.Unmarshal([]byte(setting.Value), &mappings); err != nil {
		return []model.ModelMappingEntry{}, nil
	}
	return mappings, nil
}

// SetModelMappings 持久化保存全局中继模型映射表
func (s *SettingService) SetModelMappings(mappings []model.ModelMappingEntry) error {
	data, err := json.Marshal(mappings)
	if err != nil {
		return fmt.Errorf("序列化模型映射失败: %w", err)
	}

	setting := model.Setting{
		Key:         "model_mappings",
		Value:       string(data),
		Description: "中继模型映射与号池路由规则",
		UpdatedAt:   time.Now(),
	}
	return database.DB.Save(&setting).Error
}

// PullModelMappingsFromGateway 从远端 18444 服务端网关拉取最新模型映射并同步落库 SQLite
func (s *SettingService) PullModelMappingsFromGateway() ([]model.ModelMappingEntry, error) {
	if s.gatewaySync == nil {
		return nil, fmt.Errorf("网关同步服务未初始化")
	}
	mappings, err := s.gatewaySync.GetGatewayModelMappings()
	if err != nil {
		return nil, err
	}
	if len(mappings) > 0 {
		_ = s.SetModelMappings(mappings)
	}
	return mappings, nil
}

// GetMappingClientModels 提取图二配置中所有非空的入站展示模型名（ClientModel），供套餐模块直接复用
func (s *SettingService) GetMappingClientModels() ([]string, error) {
	mappings, err := s.GetModelMappings()
	if err != nil || len(mappings) == 0 {
		mappings, _ = s.PullModelMappingsFromGateway()
	}
	if len(mappings) == 0 {
		return []string{}, nil
	}

	set := make(map[string]struct{})
	set["auto"] = struct{}{}
	for _, m := range mappings {
		cm := strings.TrimSpace(m.ClientModel)
		if cm != "" {
			set[cm] = struct{}{}
		}
	}

	res := make([]string, 0, len(set))
	for m := range set {
		res = append(res, m)
	}
	sort.Strings(res)
	return res, nil
}

// GetAutoRacingConfig 读取全局默认 Auto 竞速配置
func (s *SettingService) GetAutoRacingConfig() (*model.AutoRacingConfig, error) {
	var setting model.Setting
	err := database.DB.Where("`key` = ?", "auto_racing_config").First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.AutoRacingConfig{
				Enabled:          false,
				CandidateModels:  []string{},
				UseBenchmarkPool: false,
			}, nil
		}
		return nil, err
	}

	var cfg model.AutoRacingConfig
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return &model.AutoRacingConfig{
			Enabled:          false,
			CandidateModels:  []string{},
			UseBenchmarkPool: false,
		}, nil
	}
	return &cfg, nil
}

// SetAutoRacingConfig 保存全局默认 Auto 竞速配置，并在启用时热同步至现网 Go Relay 网关
func (s *SettingService) SetAutoRacingConfig(cfg *model.AutoRacingConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	setting := model.Setting{
		Key:         "auto_racing_config",
		Value:       string(data),
		Description: "全局 Auto 并发竞速默认规则",
		UpdatedAt:   time.Now(),
	}
	if err := database.DB.Save(&setting).Error; err != nil {
		return err
	}

	// 同步推送到远端网关（若已配置并启用同步）
	if s.gatewaySync != nil {
		_ = s.gatewaySync.SyncAutoConfigToGateway(cfg)
	}
	return nil
}

// PullAutoConfigFromGateway 从现网 Go Relay 服务端网关直接拉取最新配置并落库
func (s *SettingService) PullAutoConfigFromGateway() (*model.AutoRacingConfig, error) {
	if s.gatewaySync == nil {
		return nil, fmt.Errorf("网关同步服务未初始化")
	}
	cfg, err := s.gatewaySync.GetGatewayAutoConfig()
	if err != nil {
		return nil, err
	}
	_ = s.SetAutoRacingConfig(cfg)
	return cfg, nil
}

// GetUserAutoConfig 读取指定用户的私有专属 Auto 竞速配置
func (s *SettingService) GetUserAutoConfig(userID uint) (*model.AutoRacingConfig, error) {
	userKey := fmt.Sprintf("user_auto_config_%d", userID)
	var setting model.Setting
	err := database.DB.Where("`key` = ?", userKey).First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 回退全局默认配置
			return s.GetAutoRacingConfig()
		}
		return nil, err
	}

	var cfg model.AutoRacingConfig
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return s.GetAutoRacingConfig()
	}
	return &cfg, nil
}

// SetUserAutoConfig 保存指定用户的私有专属 Auto 竞速配置
func (s *SettingService) SetUserAutoConfig(userID uint, cfg *model.AutoRacingConfig) error {
	userKey := fmt.Sprintf("user_auto_config_%d", userID)
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	setting := model.Setting{
		Key:         userKey,
		Value:       string(data),
		Description: fmt.Sprintf("用户 %d 专属 Auto 竞速规则", userID),
		UpdatedAt:   time.Now(),
	}
	return database.DB.Save(&setting).Error
}

// GetAvailableModels 汇总当前系统所有已开放/已映射的模型列表，优先请求 Go Relay 服务端网关真实模型
func (s *SettingService) GetAvailableModels() ([]string, error) {
	set := make(map[string]struct{})

	// 1. 优先从现网 Go Relay 网关服务动态拉取真实模型（包含各号池、上游及映射模型）
	if s.gatewaySync != nil {
		if gwModels, err := s.gatewaySync.FetchGatewayModels(); err == nil && len(gwModels) > 0 {
			for _, m := range gwModels {
				m = strings.TrimSpace(m)
				if m != "" {
					set[m] = struct{}{}
				}
			}
		}
	}

	// 2. 汇总本地模型映射 (仅管理员真实配置的模型，杜绝硬编码假模型)
	mappings, _ := s.GetModelMappings()
	for _, m := range mappings {
		if m.ClientModel != "" {
			set[m.ClientModel] = struct{}{}
		}
		if m.TargetModel != "" && !strings.Contains(m.TargetModel, "/") {
			set[m.TargetModel] = struct{}{}
		}
	}

	// 3. 汇总全局 Auto 并发竞速配置中的全部候选模型
	if autoCfg, err := s.GetAutoRacingConfig(); err == nil && autoCfg != nil {
		for _, m := range autoCfg.CandidateModels {
			m = strings.TrimSpace(m)
			if m != "" {
				set[m] = struct{}{}
			}
		}
	}

	res := make([]string, 0, len(set))
	for m := range set {
		res = append(res, m)
	}
	sort.Strings(res)
	return res, nil
}
