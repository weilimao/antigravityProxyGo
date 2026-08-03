package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"antigravity-proxy/internal/netutil"
)

// Manager 管理应用配置的内存态与磁盘持久化。
// 拆分自原 settings.go:结构体定义、加载/保存、迁移、热下推等基础设施集中于此;
// 字段级 Get/Set 访问器见 settings_accessors.go,泛型读写见 settings_generic.go。
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
		FallbackProxyAddress:  "",
		FallbackProxyEnabled:  false,
		FallbackProxyUsername: "",
		FallbackProxyPassword: "",
		Language:             "zh",
		RequestTimeout:       300,
		EnableCustomCompression: true,
		MaxTokensThreshold:      100000,
		CompressionStrategy:     "summarize",
		SummaryModel:            "gemini-2.5-flash-lite",
		KeepRecentTurns:         5,
		OcrModel:                DefaultOcrModel,
		NvidiaCompressEnabled:         true,
		NvidiaCompressThresholdTokens: 80000,
		NvidiaCompressKeepToolResults: 4,
		CustomModelOverrideEnabled:    false,
		CustomModelOverrideID:         "",
		CustomThinkingOverrideEnabled: false,
		CustomThinkingSupports:        false,
		CustomThinkingBudget:          0,
		CustomThinkingMinBudget:       32,
		CustomMaxOutputTokens:         65536,
		ReasoningAsText:               false,
		EnableThinkingMode:            true,
		EnableDebuggerMode:            false,
		DebuggerLogPath:               "logs/debugger",
	}

	m.loadConfig()
	netutil.UpdateConfig(netutil.ProxyConfig{
		FallbackPorts:        m.config.FallbackProxyPorts,
		CustomSocks5Address:  m.config.CustomSocks5Address,
		CustomSocks5Enabled:  m.config.CustomSocks5Enabled,
		CustomSocks5Username: m.config.CustomSocks5Username,
		CustomSocks5Password: m.config.CustomSocks5Password,
		FallbackProxyAddress:  m.config.FallbackProxyAddress,
		FallbackProxyEnabled:  m.config.FallbackProxyEnabled,
		FallbackProxyUsername: m.config.FallbackProxyUsername,
		FallbackProxyPassword: m.config.FallbackProxyPassword,
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
		EnablePacketCapture:     true,
		EnableCustomCompression: true,
		MaxTokensThreshold:      100000,
		CompressionStrategy:     "summarize",
		SummaryModel:            "gemini-2.5-flash-lite",
		KeepRecentTurns:         5,
		OcrModel:                DefaultOcrModel,
		NvidiaCompressEnabled:         true,
		NvidiaCompressThresholdTokens: 80000,
		NvidiaCompressKeepToolResults: 4,
		EnableThinkingMode:      true,
		EnableDebuggerMode:      false,
		DebuggerLogPath:         "logs/debugger",
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}

	// Detect which security fields are explicitly set in the config file.
	// If a security field is missing from an older config, apply secure defaults
	// rather than the Go zero value (false).
	needSave := false
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err == nil {
		if _, exists := rawMap["relaySSRFBlock"]; !exists {
			parsed.RelaySSRFBlock = true
		}
		if _, exists := rawMap["relayPortBlock"]; !exists {
			parsed.RelayPortBlock = true
		}
		if _, exists := rawMap["relayDomainWhitelist"]; !exists {
			parsed.RelayDomainWhitelist = []string{"*.googleapis.com", "*.google.com", "*.anthropic.com", "*.openai.com"}
		}
		if _, exists := rawMap["customThinkingSupports"]; !exists {
			parsed.CustomThinkingSupports = false
		}
		if !parsed.EnableThinkingMode {
			parsed.EnableThinkingMode = true
			needSave = true
		}
		if _, exists := rawMap["enableDebuggerMode"]; !exists {
			parsed.EnableDebuggerMode = false
		}
	}

	if parsed.DebuggerLogPath == "" {
		parsed.DebuggerLogPath = "logs/debugger"
	}

	// 旧配置文件无 ocrModel 字段 → 兜底默认,避免升级后 OCR 链路回首版硬编码语义漂移。
	if strings.TrimSpace(parsed.OcrModel) == "" {
		parsed.OcrModel = DefaultOcrModel
	}

	if parsed.CustomThinkingMinBudget <= 0 {
		parsed.CustomThinkingMinBudget = 32
	}
	if parsed.CustomMaxOutputTokens <= 0 {
		parsed.CustomMaxOutputTokens = 65536
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

	deletedMap := make(map[string]bool)
	for _, name := range m.config.DeletedModelMappings {
		deletedMap[name] = true
	}

	modified := false
	if len(m.config.RelayModelMapping) == 0 && len(m.config.DeletedModelMappings) == 0 {
		m.config.RelayModelMapping = defaults
		modified = true
	} else {
		for _, def := range defaults {
			if !existingMap[def.ClientModel] && !deletedMap[def.ClientModel] {
				m.config.RelayModelMapping = append(m.config.RelayModelMapping, def)
				modified = true
			}
		}
	}

	if modified || needSave {
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

// updateNetutilConfig 把代理相关配置热下推到 netutil 运行时。
// 由各代理类 Setter(SetFallbackProxy*/SetCustomSocks5*)在落盘成功后调用,
// 调用方已在写锁释放后进入此函数,此处仅取读锁快照字段后下推。
func (m *Manager) updateNetutilConfig() {
	m.RLock()
	ports := m.config.FallbackProxyPorts
	socks5Addr := m.config.CustomSocks5Address
	socks5Enabled := m.config.CustomSocks5Enabled
	socks5User := m.config.CustomSocks5Username
	socks5Pass := m.config.CustomSocks5Password
	fbAddr := m.config.FallbackProxyAddress
	fbEnabled := m.config.FallbackProxyEnabled
	fbUser := m.config.FallbackProxyUsername
	fbPass := m.config.FallbackProxyPassword
	m.RUnlock()

	netutil.UpdateConfig(netutil.ProxyConfig{
		FallbackPorts:        ports,
		CustomSocks5Address:  socks5Addr,
		CustomSocks5Enabled:  socks5Enabled,
		CustomSocks5Username: socks5User,
		CustomSocks5Password: socks5Pass,
		FallbackProxyAddress:  fbAddr,
		FallbackProxyEnabled:  fbEnabled,
		FallbackProxyUsername: fbUser,
		FallbackProxyPassword: fbPass,
	})
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
			FallbackProxyAddress:  "",
			FallbackProxyEnabled:  false,
			FallbackProxyUsername: "",
			FallbackProxyPassword: "",
			RequestTimeout:       300,
			OcrModel:             DefaultOcrModel,
			EnableThinkingMode:   true,
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
