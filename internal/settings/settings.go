package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"antigravity-proxy/internal/netutil"
)

const configFileName = "config.json"

var dataFiles = []string{
	"accounts.json",
	"stats.json",
	"usage.json",
	"pricing.json",
	"captured_packets.json",
}

var dataDirs = []string{
	"certs",
}

type ModelMappingEntry struct {
	ClientModel string `json:"clientModel"`
	TargetModel string `json:"targetModel"`
	Expose      bool   `json:"expose"`
}

type Config struct {
	DataDirectory   string `json:"dataDirectory"`
	EnableSystemLog bool   `json:"enableSystemLog"`
	IsInterceptMode bool   `json:"isInterceptMode"`
	AutoStart       bool   `json:"autoStart"`
	SilentStart     bool   `json:"silentStart"`
	MaxRetries      int    `json:"maxRetries"`
	MaxRetryDelay   int    `json:"maxRetryDelay"`
	RelayEnabled    bool   `json:"relayEnabled"`
	RelayPort       string `json:"relayPort"`
	RemoteHost      string `json:"remoteHost"`
	RemotePort      string `json:"remotePort"`
	RemotePath      string `json:"remotePath"`
	RemoteKey       string `json:"remoteKey"`
	RemotePassword       string   `json:"remotePassword"`
	RemoteEnabled        bool     `json:"remoteEnabled"`
	RelaySSRFBlock       bool     `json:"relaySSRFBlock"`
	RelayPortBlock       bool     `json:"relayPortBlock"`
	RelayDomainFilter    bool     `json:"relayDomainFilter"`
	RelayDomainWhitelist []string `json:"relayDomainWhitelist"`
	RelayModelMapping    []ModelMappingEntry `json:"relayModelMapping"`
	EnablePacketCapture  bool   `json:"enablePacketCapture"`
	FallbackProxyPorts   string `json:"fallbackProxyPorts"`
	CustomSocks5Address  string `json:"customSocks5Address"`
	CustomSocks5Enabled  bool   `json:"customSocks5Enabled"`
	CustomSocks5Username string `json:"customSocks5Username"`
	CustomSocks5Password string `json:"customSocks5Password"`
	Language             string `json:"language"`
	MaxRequestBodyMB     int    `json:"maxRequestBodyMB"`
	RequestTimeout       int    `json:"requestTimeout"`
}

func GetDefaultModelMappings() []ModelMappingEntry {
	return []ModelMappingEntry{
		{ClientModel: "gemini-3-flash-agent", TargetModel: "gemini-3-flash-agent", Expose: true},
		{ClientModel: "gemini-2.5-flash-thinking", TargetModel: "gemini-2.5-flash-thinking", Expose: true},
		{ClientModel: "gemini-2.5-pro", TargetModel: "gemini-2.5-pro", Expose: true},
		{ClientModel: "gemini-2.0-flash-thinking-exp-01-21", TargetModel: "gemini-2.0-flash-thinking-exp-01-21", Expose: true},
		{ClientModel: "gemini-2.0-flash-lite-preview-02-05", TargetModel: "gemini-2.0-flash-lite-preview-02-05", Expose: true},
		{ClientModel: "gemini-2.0-pro-exp-02-05", TargetModel: "gemini-2.0-pro-exp-02-05", Expose: true},
		{ClientModel: "gemini-2.0-flash-thinking-exp", TargetModel: "gemini-2.0-flash-thinking-exp", Expose: true},
		{ClientModel: "gemini-2.0-flash-exp", TargetModel: "gemini-2.0-flash-exp", Expose: true},
		{ClientModel: "gemini-1.5-pro-latest", TargetModel: "gemini-1.5-pro", Expose: true},
		{ClientModel: "gemini-1.5-flash-latest", TargetModel: "gemini-1.5-flash", Expose: true},
		{ClientModel: "gemini-1.5-pro-exp-0827", TargetModel: "gemini-1.5-pro-exp-0827", Expose: true},

		{ClientModel: "gemini-2.0-flash-thinking-exp-1219", TargetModel: "gemini-2.0-flash-thinking-exp-1219", Expose: true},
		{ClientModel: "gemini-exp-1206", TargetModel: "gemini-exp-1206", Expose: true},
		{ClientModel: "gemini-exp-1121", TargetModel: "gemini-exp-1121", Expose: true},
		{ClientModel: "gemini-exp-1114", TargetModel: "gemini-exp-1114", Expose: true},
		{ClientModel: "gemini-1.5-pro-exp-0801", TargetModel: "gemini-1.5-pro-exp-0801", Expose: true},
		{ClientModel: "gemini-1.5-pro-002", TargetModel: "gemini-1.5-pro-002", Expose: true},
		{ClientModel: "gemini-1.5-pro-001", TargetModel: "gemini-1.5-pro-001", Expose: true},
		{ClientModel: "gemini-1.5-flash-002", TargetModel: "gemini-1.5-flash-002", Expose: true},
		{ClientModel: "gemini-1.5-flash-001", TargetModel: "gemini-1.5-flash-001", Expose: true},
		{ClientModel: "gemini-1.5-flash-8b", TargetModel: "gemini-1.5-flash-8b", Expose: true},
		{ClientModel: "text-embedding-004", TargetModel: "text-embedding-004", Expose: true},
		{ClientModel: "text-embedding-003", TargetModel: "text-embedding-003", Expose: true},

		{ClientModel: "gemini-1.5-flash-exp-0827", TargetModel: "gemini-1.5-flash-exp-0827", Expose: true},
		{ClientModel: "gemini-1.5-flash-8b-exp-0827", TargetModel: "gemini-1.5-flash-8b-exp-0827", Expose: true},
		{ClientModel: "learnlm-1.5-pro-experimental", TargetModel: "learnlm-1.5-pro-experimental", Expose: true},
		{ClientModel: "gemini-1.0-pro", TargetModel: "gemini-1.0-pro", Expose: true},
		{ClientModel: "aqa", TargetModel: "aqa", Expose: true},
		{ClientModel: "gemini-3.5-flash-low", TargetModel: "gemini-3.5-flash-low", Expose: true},
		{ClientModel: "gemini-pro-agent", TargetModel: "gemini-pro-agent", Expose: true},
		{ClientModel: "claude-sonnet-4-6", TargetModel: "claude-sonnet-4-6", Expose: true},
		{ClientModel: "claude-opus-4-6-thinking", TargetModel: "claude-opus-4-6-thinking", Expose: true},
		{ClientModel: "gemini-3-flash", TargetModel: "gemini-3-flash", Expose: true},
		{ClientModel: "tab_flash_lite_preview", TargetModel: "tab_flash_lite_preview", Expose: true},
		{ClientModel: "gemini-3.5-flash-extra-low", TargetModel: "gemini-3.5-flash-extra-low", Expose: true},
		{ClientModel: "tab_jump_flash_lite_preview", TargetModel: "tab_jump_flash_lite_preview", Expose: true},
		{ClientModel: "gemini-3.1-flash-lite", TargetModel: "gemini-3.1-flash-lite", Expose: true},
		{ClientModel: "gemini-3.1-pro-low", TargetModel: "gemini-3.1-pro-low", Expose: true},
		{ClientModel: "gemini-2.5-flash", TargetModel: "gemini-2.5-flash", Expose: true},
		{ClientModel: "gemini-2.5-flash-lite", TargetModel: "gemini-2.5-flash-lite", Expose: true},
		{ClientModel: "gemini-3.5-flash", TargetModel: "gemini-3.5-flash", Expose: true},
		{ClientModel: "gemini-3.1-pro-preview", TargetModel: "gemini-3.1-pro-preview", Expose: true},
		{ClientModel: "gemini-3-flash-preview", TargetModel: "gemini-3-flash-preview", Expose: true},
		{ClientModel: "gpt-cos-120b-medium", TargetModel: "gpt-cos-120b-medium", Expose: true},
		{ClientModel: "gemini-1.5-pro", TargetModel: "gemini-1.5-pro", Expose: true},
		{ClientModel: "gemini-1.5-flash", TargetModel: "gemini-1.5-flash", Expose: true},
		{ClientModel: "gemini-2.0-flash", TargetModel: "gemini-2.0-flash", Expose: true},
		{ClientModel: "gemini-2.0-pro-exp-02-05", TargetModel: "gemini-2.0-pro-exp-02-05", Expose: true},

		{ClientModel: "claude-3-5-sonnet", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "claude-3-opus", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "claude-3-haiku", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "claude-3-5-haiku", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "gpt-4o", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-4-turbo", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-4", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-3.5", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "o1-mini", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "o1-pro", TargetModel: "gemini-2.0-flash", Expose: false},
		{ClientModel: "o1-preview", TargetModel: "gemini-2.0-flash", Expose: false},
	}
}

type Manager struct {
	sync.RWMutex
	defaultUserDataPath string
	activeDataDirectory string
	config              Config
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Init(defaultPath string) {
	m.Lock()
	defer m.Unlock()

	m.defaultUserDataPath = defaultPath
	m.activeDataDirectory = defaultPath
	m.config = Config{
		DataDirectory:        "",
		EnableSystemLog:      false,
		IsInterceptMode:      false,
		AutoStart:            false,
		SilentStart:          false,
		MaxRetries:           20,
		MaxRetryDelay:        10,
		RelaySSRFBlock:       true,
		RelayPortBlock:       true,
		RelayDomainFilter:    false,
		RelayDomainWhitelist: []string{"*.googleapis.com", "*.google.com", "*.anthropic.com", "*.openai.com"},
		RelayModelMapping:    GetDefaultModelMappings(),
		EnablePacketCapture:  true,
		FallbackProxyPorts:   "",
		CustomSocks5Address:  "",
		CustomSocks5Enabled:  false,
		CustomSocks5Username: "",
		CustomSocks5Password: "",
		Language:             "zh",
		RequestTimeout:       300,
	}

	m.loadConfig()
	netutil.UpdateConfig(netutil.ProxyConfig{
		FallbackPorts:        m.config.FallbackProxyPorts,
		CustomSocks5Address:  m.config.CustomSocks5Address,
		CustomSocks5Enabled:  m.config.CustomSocks5Enabled,
		CustomSocks5Username: m.config.CustomSocks5Username,
		CustomSocks5Password: m.config.CustomSocks5Password,
	})
}

func (m *Manager) loadConfig() {
	configPath := filepath.Join(m.defaultUserDataPath, configFileName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	// 自动剥离 Windows 文本编辑器及 PowerShell 写入时可能携带的 UTF-8 BOM 头 (0xEF 0xBB 0xBF)
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	parsed := Config{
		EnablePacketCapture: true,
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}

	if parsed.MaxRetries <= 0 {
		parsed.MaxRetries = 20
	}

	if parsed.MaxRetryDelay <= 0 {
		parsed.MaxRetryDelay = 10
	}

	if parsed.RequestTimeout <= 0 {
		parsed.RequestTimeout = 300
	}

	m.config = parsed

	defaults := GetDefaultModelMappings()
	existingMap := make(map[string]bool)
	for _, entry := range m.config.RelayModelMapping {
		existingMap[entry.ClientModel] = true
	}

	modified := false
	if len(m.config.RelayModelMapping) == 0 {
		m.config.RelayModelMapping = defaults
		modified = true
	} else {
		for _, def := range defaults {
			if !existingMap[def.ClientModel] {
				m.config.RelayModelMapping = append(m.config.RelayModelMapping, def)
				modified = true
			}
		}
	}

	if modified {
		_ = m.SaveConfig()
	}

	if parsed.DataDirectory != "" {
		if _, err := os.Stat(parsed.DataDirectory); err == nil {
			m.activeDataDirectory = parsed.DataDirectory
		}
	}
}

func (m *Manager) SaveConfig() error {
	configPath := filepath.Join(m.defaultUserDataPath, configFileName)
	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile(configPath, data, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) GetActiveDataDirectory() string {
	m.RLock()
	defer m.RUnlock()
	return m.activeDataDirectory
}

func (m *Manager) GetDefaultUserDataPath() string {
	m.RLock()
	defer m.RUnlock()
	return m.defaultUserDataPath
}

func (m *Manager) GetEnableSystemLog() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.EnableSystemLog
}

func (m *Manager) SetEnableSystemLog(enable bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.EnableSystemLog = enable
	return m.SaveConfig()
}

func (m *Manager) GetIsInterceptMode() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.IsInterceptMode
}

func (m *Manager) SetIsInterceptMode(mode bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.IsInterceptMode = mode
	return m.SaveConfig()
}

func (m *Manager) GetAutoStart() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.AutoStart
}

func (m *Manager) SetAutoStart(enabled bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.AutoStart = enabled
	if err := m.SaveConfig(); err != nil {
		return err
	}
	return setOSAutoStart(enabled)
}

func (m *Manager) GetSilentStart() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.SilentStart
}

func (m *Manager) SetSilentStart(enabled bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.SilentStart = enabled
	return m.SaveConfig()
}

func (m *Manager) GetRelayEnabled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.RelayEnabled
}

func (m *Manager) SetRelayEnabled(enabled bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayEnabled = enabled
	return m.SaveConfig()
}

func (m *Manager) GetRelayPort() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RelayPort
}

func (m *Manager) SetRelayPort(port string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayPort = port
	return m.SaveConfig()
}

func (m *Manager) GetRemoteHost() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemoteHost
}

func (m *Manager) SetRemoteHost(host string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemoteHost = host
	return m.SaveConfig()
}

func (m *Manager) GetRemotePath() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemotePath
}

func (m *Manager) SetRemotePath(path string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemotePath = path
	return m.SaveConfig()
}

func (m *Manager) GetRemotePort() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemotePort
}

func (m *Manager) SetRemotePort(port string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemotePort = port
	return m.SaveConfig()
}

func (m *Manager) GetRemoteKey() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemoteKey
}

func (m *Manager) SetRemoteKey(key string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemoteKey = key
	return m.SaveConfig()
}

func (m *Manager) GetRemotePassword() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemotePassword
}

func (m *Manager) SetRemotePassword(pwd string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemotePassword = pwd
	return m.SaveConfig()
}

func (m *Manager) GetRemoteEnabled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.RemoteEnabled
}

func (m *Manager) SetRemoteEnabled(enabled bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.RemoteEnabled = enabled
	return m.SaveConfig()
}

func (m *Manager) GetRelaySSRFBlock() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.RelaySSRFBlock
}

func (m *Manager) SetRelaySSRFBlock(val bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelaySSRFBlock = val
	return m.SaveConfig()
}

func (m *Manager) GetRelayPortBlock() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.RelayPortBlock
}

func (m *Manager) SetRelayPortBlock(val bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayPortBlock = val
	return m.SaveConfig()
}

func (m *Manager) GetRelayDomainFilter() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.RelayDomainFilter
}

func (m *Manager) SetRelayDomainFilter(val bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayDomainFilter = val
	return m.SaveConfig()
}

func (m *Manager) GetRelayDomainWhitelist() []string {
	m.RLock()
	defer m.RUnlock()
	if m.config.RelayDomainWhitelist == nil {
		return []string{}
	}
	return m.config.RelayDomainWhitelist
}

func (m *Manager) SetRelayDomainWhitelist(val []string) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayDomainWhitelist = val
	return m.SaveConfig()
}

func (m *Manager) GetRelayModelMapping() []ModelMappingEntry {
	m.RLock()
	defer m.RUnlock()
	if len(m.config.RelayModelMapping) == 0 {
		return GetDefaultModelMappings()
	}
	return m.config.RelayModelMapping
}

func (m *Manager) SetRelayModelMapping(val []ModelMappingEntry) error {
	m.Lock()
	defer m.Unlock()
	m.config.RelayModelMapping = val
	return m.SaveConfig()
}

// MigrateData 迁移配置目录与数据，使用回调解耦代理和补丁模块
func (m *Manager) MigrateData(
	targetPath string,
	progressCallback func(step string, status string),
	stopProxy func(),
	restartProxy func(),
	patchAll func(string) error,
	redirectPaths func(string),
) error {
	m.RLock()
	currentDir := m.activeDataDirectory
	defaultDir := m.defaultUserDataPath
	m.RUnlock()

	resolvedTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	resolvedCurrent, err := filepath.Abs(currentDir)
	if err != nil {
		return err
	}

	if resolvedTarget == resolvedCurrent {
		return nil
	}

	// 1. 创建目标目录并测试写入权限
	err = os.MkdirAll(resolvedTarget, 0755)
	if err != nil {
		return fmt.Errorf("无法创建目标目录，权限不足或路径无效: %v", err)
	}

	progressCallback("stop-proxy", "正在停止代理服务器...")
	if stopProxy != nil {
		stopProxy()
	}

	progressCallback("migrate-files", "正在复制数据文件与证书 (请勿关闭软件)...")
	copiedItems := make([]struct {
		Path     string
		IsDir    bool
		Original string
	}, 0)

	rollback := func() {
		for _, item := range copiedItems {
			_ = os.RemoveAll(item.Path)
		}
	}

	// 2. 复制文件
	for _, file := range dataFiles {
		srcFile := filepath.Join(resolvedCurrent, file)
		destFile := filepath.Join(resolvedTarget, file)
		if _, err := os.Stat(srcFile); err == nil {
			err = copyFile(srcFile, destFile)
			if err != nil {
				rollback()
				return fmt.Errorf("复制文件失败: %s -> %s, %v", srcFile, destFile, err)
			}
			copiedItems = append(copiedItems, struct {
				Path     string
				IsDir    bool
				Original string
			}{destFile, false, srcFile})
		}
	}

	// 3. 复制子目录
	for _, dir := range dataDirs {
		srcDir := filepath.Join(resolvedCurrent, dir)
		destDir := filepath.Join(resolvedTarget, dir)
		if _, err := os.Stat(srcDir); err == nil {
			err = copyDir(srcDir, destDir)
			if err != nil {
				rollback()
				return fmt.Errorf("复制目录失败: %s -> %s, %v", srcDir, destDir, err)
			}
			copiedItems = append(copiedItems, struct {
				Path     string
				IsDir    bool
				Original string
			}{destDir, true, srcDir})
		}
	}

	// 4. 验证文件完整性
	for _, item := range copiedItems {
		if _, err := os.Stat(item.Path); os.IsNotExist(err) {
			rollback()
			return fmt.Errorf("文件校验失败，未能在目标位置找到已迁移的项: %s", filepath.Base(item.Path))
		}
	}

	// 5. 更新内存状态与持久化
	isTargetDefault := resolvedTarget == filepath.Clean(defaultDir)
	var newCustomPath string
	if !isTargetDefault {
		newCustomPath = resolvedTarget
	}

	m.Lock()
	m.config.DataDirectory = newCustomPath
	err = m.SaveConfig()
	if err != nil {
		m.Unlock()
		rollback()
		return fmt.Errorf("无法保存配置文件: %v", err)
	}

	m.activeDataDirectory = resolvedTarget
	m.Unlock()

	progressCallback("update-paths", "正在重定向数据服务工作路径...")
	if redirectPaths != nil {
		redirectPaths(resolvedTarget)
	}

	progressCallback("patch-externals", "正在更新外部编辑器代理补丁...")
	if patchAll != nil {
		caPemPath := filepath.Join(resolvedTarget, "certs", "certs", "ca.pem")
		err = patchAll(caPemPath)
		if err != nil {
			// 打印警告日志，补丁失败一般不作为阻断迁移的致命错误
			fmt.Printf("[Migration Warning] Failed to patch externals: %v\n", err)
		}
	}

	progressCallback("restart-proxy", "正在重新启动代理服务器...")
	if restartProxy != nil {
		restartProxy()
	}

	// 6. 清理旧文件
	for _, item := range copiedItems {
		_ = os.RemoveAll(item.Original)
	}

	progressCallback("success", "🎉 迁移成功！数据已妥善转移并重定向。")
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return nil
}

func copyDir(src string, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	err = os.MkdirAll(dst, srcInfo.Mode())
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			err = copyFile(srcPath, dstPath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) MigrateDataSync(
	targetPath string,
	stopProxy func(),
	restartProxy func(),
	patchAll func(string) error,
	redirectPaths func(string),
) error {
	noopProgress := func(step string, status string) {}
	return m.MigrateData(targetPath, noopProgress, stopProxy, restartProxy, patchAll, redirectPaths)
}

// EnsureConfigExists 确保默认数据文件夹和 config.json 存在
func EnsureConfigExists(defaultPath string) (string, error) {
	err := os.MkdirAll(defaultPath, 0755)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(defaultPath, configFileName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		defaultConfig := Config{
			DataDirectory:   "",
			EnableSystemLog: false,
			IsInterceptMode: false,
			AutoStart:       false,
			SilentStart:     false,
			RelayEnabled:    false,
			RelayPort:       "18444",
			RemoteHost:      "",
			RemotePort:      "",
			RemotePath:      "",
			RemoteKey:       "",
			RemotePassword:  "",
			RemoteEnabled:        false,
			RelaySSRFBlock:       true,
			RelayPortBlock:       true,
			RelayDomainFilter:    false,
			RelayDomainWhitelist: []string{"*.googleapis.com", "*.google.com", "*.anthropic.com", "*.openai.com"},
			RelayModelMapping:    GetDefaultModelMappings(),
			EnablePacketCapture:  true,
			FallbackProxyPorts:   "",
			CustomSocks5Address:  "",
			CustomSocks5Enabled:  false,
			RequestTimeout:       300,
		}
		data, err := json.MarshalIndent(defaultConfig, "", "  ")
		if err != nil {
			return "", err
		}
		err = os.WriteFile(configPath, data, 0644)
		if err != nil {
			return "", err
		}
	}
	return configPath, nil
}
func (m *Manager) GetMaxRetries() int {
	m.RLock()
	defer m.RUnlock()
	if m.config.MaxRetries <= 0 {
		return 20
	}
	return m.config.MaxRetries
}

func (m *Manager) SetMaxRetries(retries int) error {
	m.Lock()
	defer m.Unlock()
	if retries <= 0 {
		retries = 20
	}
	m.config.MaxRetries = retries
	return m.SaveConfig()
}

func (m *Manager) GetMaxRetryDelay() int {
	m.RLock()
	defer m.RUnlock()
	if m.config.MaxRetryDelay <= 0 {
		return 10
	}
	return m.config.MaxRetryDelay
}

func (m *Manager) SetMaxRetryDelay(delay int) error {
	m.Lock()
	defer m.Unlock()
	if delay <= 0 {
		delay = 10
	}
	m.config.MaxRetryDelay = delay
	return m.SaveConfig()
}

func (m *Manager) GetEnablePacketCapture() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.EnablePacketCapture
}

func (m *Manager) SetEnablePacketCapture(enable bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.EnablePacketCapture = enable
	return m.SaveConfig()
}

func (m *Manager) GetFallbackProxyPorts() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.FallbackProxyPorts
}

func (m *Manager) SetFallbackProxyPorts(val string) error {
	m.Lock()
	m.config.FallbackProxyPorts = val
	err := m.SaveConfig()
	m.Unlock()
	if err == nil {
		m.updateNetutilConfig()
	}
	return err
}

func (m *Manager) GetCustomSocks5Address() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.CustomSocks5Address
}

func (m *Manager) SetCustomSocks5Address(val string) error {
	m.Lock()
	m.config.CustomSocks5Address = val
	err := m.SaveConfig()
	m.Unlock()
	if err == nil {
		m.updateNetutilConfig()
	}
	return err
}

func (m *Manager) GetCustomSocks5Enabled() bool {
	m.RLock()
	defer m.RUnlock()
	return m.config.CustomSocks5Enabled
}

func (m *Manager) SetCustomSocks5Enabled(val bool) error {
	m.Lock()
	m.config.CustomSocks5Enabled = val
	err := m.SaveConfig()
	m.Unlock()
	if err == nil {
		m.updateNetutilConfig()
	}
	return err
}

func (m *Manager) GetCustomSocks5Username() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.CustomSocks5Username
}

func (m *Manager) SetCustomSocks5Username(val string) error {
	m.Lock()
	m.config.CustomSocks5Username = val
	err := m.SaveConfig()
	m.Unlock()
	if err == nil {
		m.updateNetutilConfig()
	}
	return err
}

func (m *Manager) GetCustomSocks5Password() string {
	m.RLock()
	defer m.RUnlock()
	return m.config.CustomSocks5Password
}

func (m *Manager) SetCustomSocks5Password(val string) error {
	m.Lock()
	m.config.CustomSocks5Password = val
	err := m.SaveConfig()
	m.Unlock()
	if err == nil {
		m.updateNetutilConfig()
	}
	return err
}

func (m *Manager) updateNetutilConfig() {
	m.RLock()
	ports := m.config.FallbackProxyPorts
	socks5Addr := m.config.CustomSocks5Address
	socks5Enabled := m.config.CustomSocks5Enabled
	socks5User := m.config.CustomSocks5Username
	socks5Pass := m.config.CustomSocks5Password
	m.RUnlock()

	netutil.UpdateConfig(netutil.ProxyConfig{
		FallbackPorts:        ports,
		CustomSocks5Address:  socks5Addr,
		CustomSocks5Enabled:  socks5Enabled,
		CustomSocks5Username: socks5User,
		CustomSocks5Password: socks5Pass,
	})
}

func (m *Manager) GetLanguage() string {
	m.RLock()
	defer m.RUnlock()
	if m.config.Language == "" {
		return "zh"
	}
	return m.config.Language
}

func (m *Manager) SetLanguage(lang string) error {
	m.Lock()
	m.config.Language = lang
	err := m.SaveConfig()
	m.Unlock()
	return err
}

// GetMaxRequestBodyMB 返回请求体大小限制（MB），默认 50MB
func (m *Manager) GetMaxRequestBodyMB() int {
	m.RLock()
	defer m.RUnlock()
	if m.config.MaxRequestBodyMB <= 0 {
		return 50
	}
	return m.config.MaxRequestBodyMB
}

func (m *Manager) SetMaxRequestBodyMB(mb int) error {
	m.Lock()
	defer m.Unlock()
	if mb <= 0 {
		mb = 50
	}
	m.config.MaxRequestBodyMB = mb
	return m.SaveConfig()
}

func (m *Manager) GetRequestTimeout() int {
	m.RLock()
	defer m.RUnlock()
	if m.config.RequestTimeout <= 0 {
		return 300
	}
	return m.config.RequestTimeout
}

func (m *Manager) SetRequestTimeout(timeout int) error {
	m.Lock()
	defer m.Unlock()
	if timeout <= 0 {
		timeout = 300
	}
	m.config.RequestTimeout = timeout
	return m.SaveConfig()
}

type ManagerInterface interface {
	Init(defaultPath string)
	GetActiveDataDirectory() string
	GetDefaultUserDataPath() string
	GetEnableSystemLog() bool
	SetEnableSystemLog(enable bool) error
	GetIsInterceptMode() bool
	SetIsInterceptMode(mode bool) error
	GetAutoStart() bool
	SetAutoStart(enabled bool) error
	GetSilentStart() bool
	SetSilentStart(enabled bool) error
	GetMaxRetries() int
	SetMaxRetries(retries int) error
	GetMaxRetryDelay() int
	SetMaxRetryDelay(delay int) error
	GetRelayEnabled() bool
	SetRelayEnabled(enabled bool) error
	GetRelayPort() string
	SetRelayPort(port string) error
	GetRemoteHost() string
	SetRemoteHost(host string) error
	GetRemotePath() string
	SetRemotePath(path string) error
	GetRemotePort() string
	SetRemotePort(port string) error
	GetRemoteKey() string
	SetRemoteKey(key string) error
	GetRemotePassword() string
	SetRemotePassword(pwd string) error
	GetRemoteEnabled() bool
	SetRemoteEnabled(enabled bool) error
	GetRelaySSRFBlock() bool
	SetRelaySSRFBlock(val bool) error
	GetRelayPortBlock() bool
	SetRelayPortBlock(val bool) error
	GetRelayDomainFilter() bool
	SetRelayDomainFilter(val bool) error
	GetRelayDomainWhitelist() []string
	SetRelayDomainWhitelist(val []string) error
	GetRelayModelMapping() []ModelMappingEntry
	SetRelayModelMapping(val []ModelMappingEntry) error
	GetEnablePacketCapture() bool
	SetEnablePacketCapture(enable bool) error
	GetFallbackProxyPorts() string
	SetFallbackProxyPorts(val string) error
	GetCustomSocks5Address() string
	SetCustomSocks5Address(val string) error
	GetCustomSocks5Enabled() bool
	SetCustomSocks5Enabled(val bool) error
	GetCustomSocks5Username() string
	SetCustomSocks5Username(val string) error
	GetCustomSocks5Password() string
	SetCustomSocks5Password(val string) error
	GetLanguage() string
	SetLanguage(lang string) error
	GetRequestTimeout() int
	SetRequestTimeout(timeout int) error
	SaveConfig() error
	MigrateData(
		targetPath string,
		progressCallback func(step string, status string),
		stopProxy func(),
		restartProxy func(),
		patchAll func(string) error,
		redirectPaths func(string),
	) error
}
var _ ManagerInterface = (*Manager)(nil)
