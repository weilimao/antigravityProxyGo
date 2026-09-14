package externalconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// AgentProfile 描述一个外部 Agent 的配置元信息。
type AgentProfile struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	ConfigPath  string `json:"configPath"`
	FileExists  bool   `json:"fileExists"`
}

// Manager 管理多个外部 Agent 的配置文件读写。
// 后端只做文件 I/O：读 JSON 字符串返回前端，前端写 JSON 字符串后端落盘。
// 若配置文件为 .toml 格式，后端自动在 TOML 与 JSON 之间透明双向转换。
// 后端不感知任何业务结构，扩展新 Agent 只需 RegisterAgent 一行。
type Manager struct {
	mu     sync.RWMutex
	agents map[string]*AgentProfile
}

func NewManager() *Manager {
	return &Manager{
		agents: make(map[string]*AgentProfile),
	}
}

// RegisterAgent 注册一个外部 Agent 配置。
func (m *Manager) RegisterAgent(id, displayName, configPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile := &AgentProfile{
		ID:          id,
		DisplayName: displayName,
		ConfigPath:  configPath,
	}
	if _, err := os.Stat(configPath); err == nil {
		profile.FileExists = true
	}
	m.agents[id] = profile
}

// ListAgents 返回所有已注册 Agent。
func (m *Manager) ListAgents() []AgentProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]AgentProfile, 0, len(m.agents))
	for _, p := range m.agents {
		result = append(result, *p)
	}
	return result
}

// GetAgent 返回指定 Agent 的 Profile。
func (m *Manager) GetAgent(id string) (*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", id)
	}
	// 重新检测文件存在性
	profile := *p
	if _, err := os.Stat(p.ConfigPath); err == nil {
		profile.FileExists = true
	} else {
		profile.FileExists = false
	}
	return &profile, nil
}

// ReadConfig 读取指定 Agent 的配置文件原始 JSON 字符串。
// 若配置文件为 .toml，会自动将其解析并转换为标准 JSON 字符串返回。
// 文件不存在时返回空 JSON 对象 "{}"。
func (m *Manager) ReadConfig(agentID string) (string, error) {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(profile.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "{}", nil
		}
		return "", fmt.Errorf("failed to read config file: %w", err)
	}

	// 剥离 UTF-8 BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	// 若为 TOML 文件，转换为 JSON 返回前端
	if filepath.Ext(profile.ConfigPath) == ".toml" {
		var tomlData map[string]interface{}
		if err := toml.Unmarshal(data, &tomlData); err != nil {
			return "", fmt.Errorf("failed to parse TOML config: %w", err)
		}
		jsonBytes, err := json.MarshalIndent(tomlData, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to convert TOML to JSON: %w", err)
		}
		return string(jsonBytes), nil
	}

	// 若为 claude-code，从 ~/.claude.json 聚合全局 mcpServers（若 settings.json 中未配置）
	if agentID == "claude-code" {
		var settingsMap map[string]interface{}
		_ = json.Unmarshal([]byte(data), &settingsMap)
		if settingsMap == nil {
			settingsMap = make(map[string]interface{})
		}

		if _, has := settingsMap["mcpServers"]; !has {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				claudeJsonPath := filepath.Join(homeDir, ".claude.json")
				if cData, cErr := os.ReadFile(claudeJsonPath); cErr == nil {
					var rootData map[string]interface{}
					if json.Unmarshal(cData, &rootData) == nil {
						if mcpServers, ok := rootData["mcpServers"]; ok && mcpServers != nil {
							settingsMap["mcpServers"] = mcpServers
						}
					}
				}
			}
		}

		jsonBytes, err := json.MarshalIndent(settingsMap, "", "  ")
		if err == nil {
			return string(jsonBytes), nil
		}
	}

	return string(data), nil
}

// WriteConfig 写入指定 Agent 的配置。
// 若配置文件为 .toml，会将前端传入的 JSON 数据序列化为 TOML 后落盘。
// 若为 claude-code 且包含 mcpServers，同步更新 ~/.claude.json 中的 mcpServers 节点。
// 写前自动创建 .bak 备份；目录不存在时自动创建。
func (m *Manager) WriteConfig(agentID, jsonStr string) error {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return err
	}

	// 校验 JSON 合法性
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawMap); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	configPath := profile.ConfigPath

	// 确保目录存在
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	var writeData []byte
	if filepath.Ext(configPath) == ".toml" {
		tomlBytes, err := toml.Marshal(rawMap)
		if err != nil {
			return fmt.Errorf("failed to convert JSON to TOML: %w", err)
		}
		writeData = tomlBytes
	} else {
		writeData = []byte(jsonStr)
	}

	// 写前备份（仅当原文件存在时）
	if _, err := os.Stat(configPath); err == nil {
		bakPath := configPath + ".bak"
		origData, readErr := os.ReadFile(configPath)
		if readErr == nil && len(origData) > 0 {
			_ = os.WriteFile(bakPath, origData, 0644)
		}
	}

	// 写入文件
	if err := os.WriteFile(configPath, writeData, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// 若为 claude-code 且包含 mcpServers，同步更新 ~/.claude.json 中的全局 mcpServers 节点
	if agentID == "claude-code" {
		if mcpServers, has := rawMap["mcpServers"]; has && mcpServers != nil {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				claudeJsonPath := filepath.Join(homeDir, ".claude.json")
				if cData, cErr := os.ReadFile(claudeJsonPath); cErr == nil {
					var rootData map[string]interface{}
					if json.Unmarshal(cData, &rootData) == nil {
						rootData["mcpServers"] = mcpServers
						// 备份 ~/.claude.json
						_ = os.WriteFile(claudeJsonPath+".bak", cData, 0644)
						if newCData, mErr := json.MarshalIndent(rootData, "", "  "); mErr == nil {
							_ = os.WriteFile(claudeJsonPath, newCData, 0644)
						}
					}
				}
			}
		}
	}

	return nil
}

// RestoreBackup 从 .bak 恢复原始配置文件。
func (m *Manager) RestoreBackup(agentID string) error {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return err
	}

	bakPath := profile.ConfigPath + ".bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		return fmt.Errorf("backup file not found or unreadable: %w", err)
	}

	if err := os.WriteFile(profile.ConfigPath, bakData, 0644); err != nil {
		return fmt.Errorf("failed to restore config from backup: %w", err)
	}

	return nil
}

// ReloadConfig 从磁盘重新读取配置文件。
func (m *Manager) ReloadConfig(agentID string) (string, error) {
	return m.ReadConfig(agentID)
}

// ModelCatalogEntry 描述 catalog 文件中单个模型的 slug 和展示名称。
type ModelCatalogEntry struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// ReadModelCatalog 读取指定 Agent 的模型 catalog 文件并返回所有模型 slug 列表。
// 仅对通过 model_catalog_json 字段声明了外部 catalog 的 Agent 有效（如 Codex）。
// 对未声明 catalog 的 Agent 返回空列表（非错误），向前兼容。
func (m *Manager) ReadModelCatalog(agentID string) ([]ModelCatalogEntry, error) {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(profile.ConfigPath)
	if err != nil {
		return nil, nil
	}

	// 剥离 UTF-8 BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	// 解析配置文件，提取 model_catalog_json 字段值
	var configRoot map[string]interface{}
	if filepath.Ext(profile.ConfigPath) == ".toml" {
		if err := toml.Unmarshal(data, &configRoot); err != nil {
			return nil, nil
		}
	} else {
		if err := json.Unmarshal(data, &configRoot); err != nil {
			return nil, nil
		}
	}

	catalogFileName, ok := configRoot["model_catalog_json"].(string)
	if !ok || catalogFileName == "" {
		return nil, nil
	}

	// catalog 文件路径 = 配置文件所在目录 + catalog 文件名
	catalogPath := filepath.Join(filepath.Dir(profile.ConfigPath), catalogFileName)
	catalogData, err := os.ReadFile(catalogPath)
	if err != nil {
		return nil, nil
	}

	if len(catalogData) >= 3 && catalogData[0] == 0xEF && catalogData[1] == 0xBB && catalogData[2] == 0xBF {
		catalogData = catalogData[3:]
	}

	var catalogRoot struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(catalogData, &catalogRoot); err != nil {
		return nil, nil
	}

	result := make([]ModelCatalogEntry, 0, len(catalogRoot.Models))
	for _, m := range catalogRoot.Models {
		if m.Slug != "" {
			result = append(result, ModelCatalogEntry{
				Slug:        m.Slug,
				DisplayName: m.DisplayName,
			})
		}
	}
	return result, nil
}

// resolveModelCatalogPath 解析指定 Agent 配置文件中 model_catalog_json 字段指向的 catalog 绝对路径。
// 若 Agent 未声明 catalog 或文件不可读，返回空字符串。
func (m *Manager) resolveModelCatalogPath(agentID string) string {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return ""
	}

	data, _ := os.ReadFile(profile.ConfigPath)
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	var configRoot map[string]interface{}
	if filepath.Ext(profile.ConfigPath) == ".toml" {
		if toml.Unmarshal(data, &configRoot) != nil {
			return ""
		}
	} else {
		if json.Unmarshal(data, &configRoot) != nil {
			return ""
		}
	}

	catalogFileName, ok := configRoot["model_catalog_json"].(string)
	if !ok || catalogFileName == "" {
		return ""
	}

	return filepath.Join(filepath.Dir(profile.ConfigPath), catalogFileName)
}

// ReadModelCatalogFull 读取指定 Agent 的 catalog 文件完整 JSON 字符串返回前端。
// 文件不存在或未声明 catalog 时返回空 JSON 对象 "{}"（非错误），向前兼容。
func (m *Manager) ReadModelCatalogFull(agentID string) (string, error) {
	catalogPath := m.resolveModelCatalogPath(agentID)
	if catalogPath == "" {
		return "{}", nil
	}

	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return "{}", nil
	}

	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	return string(data), nil
}

// WriteModelCatalogFull 将前端传入的 JSON 字符串写入指定 Agent 的 catalog 文件。
// 写前自动创建 .bak 备份；若未声明 catalog 路径，返回错误。
func (m *Manager) WriteModelCatalogFull(agentID, jsonStr string) error {
	catalogPath := m.resolveModelCatalogPath(agentID)
	if catalogPath == "" {
		return fmt.Errorf("agent %s has no model_catalog_json field declared, cannot write catalog", agentID)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawMap); err != nil {
		return fmt.Errorf("invalid catalog JSON: %w", err)
	}

	// 防御护栏：检测 JS 端对象序列化损坏标志 "[object Object]"，命中即拒绝写入，
	// 避免坏 catalog 落盘导致 codex-cli 拒绝加载配置。
	if containsCorruptedObjectString(rawMap) {
		return fmt.Errorf(`catalog JSON contains corrupted "[object Object]" string values, refusing to write`)
	}

	if _, err := os.Stat(catalogPath); err == nil {
		bakPath := catalogPath + ".bak"
		origData, readErr := os.ReadFile(catalogPath)
		if readErr == nil && len(origData) > 0 {
			_ = os.WriteFile(bakPath, origData, 0644)
		}
	}

	if err := os.WriteFile(catalogPath, []byte(jsonStr), 0644); err != nil {
		return fmt.Errorf("failed to write catalog file: %w", err)
	}
	return nil
}

// containsCorruptedObjectString 递归检查 JSON 值中是否存在字面量 "[object Object]" 字符串。
// 该字符串是 JS 端把对象误序列化（String(obj) / Array.join）产生的损坏标志。
func containsCorruptedObjectString(v interface{}) bool {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) == "[object Object]"
	case []interface{}:
		for _, el := range t {
			if containsCorruptedObjectString(el) {
				return true
			}
		}
	case map[string]interface{}:
		for _, val := range t {
			if containsCorruptedObjectString(val) {
				return true
			}
		}
	}
	return false
}

// resolveAuthPath 解析指定 Agent 认证文件（如 Codex 的 auth.json）的绝对路径。
// 目前对 codex 生效（位于其 configPath 同目录下的 auth.json）。对其他 Agent 返回空字符串。
func (m *Manager) resolveAuthPath(agentID string) string {
	profile, err := m.GetAgent(agentID)
	if err != nil {
		return ""
	}
	if agentID == "codex" {
		return filepath.Join(filepath.Dir(profile.ConfigPath), "auth.json")
	}
	return ""
}

// ReadAuthFull 读取指定 Agent 的认证文件完整 JSON 字符串返回前端。
// 文件不存在或 Agent 无单独 auth 文件时返回空 JSON 对象 "{}"（非错误）。
func (m *Manager) ReadAuthFull(agentID string) (string, error) {
	authPath := m.resolveAuthPath(agentID)
	if authPath == "" {
		return "{}", nil
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		return "{}", nil
	}

	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	return string(data), nil
}

// WriteAuthFull 将前端传入的 JSON 字符串写入指定 Agent 的认证文件（如 Codex 的 auth.json）。
// 写前自动创建 .bak 备份；若未声明 auth 路径，返回错误。
func (m *Manager) WriteAuthFull(agentID, jsonStr string) error {
	authPath := m.resolveAuthPath(agentID)
	if authPath == "" {
		return fmt.Errorf("agent %s has no auth file defined, cannot write auth", agentID)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawMap); err != nil {
		return fmt.Errorf("invalid auth JSON: %w", err)
	}

	if _, err := os.Stat(authPath); err == nil {
		bakPath := authPath + ".bak"
		origData, readErr := os.ReadFile(authPath)
		if readErr == nil && len(origData) > 0 {
			_ = os.WriteFile(bakPath, origData, 0644)
		}
	}

	if err := os.WriteFile(authPath, []byte(jsonStr), 0644); err != nil {
		return fmt.Errorf("failed to write auth file: %w", err)
	}
	return nil
}

