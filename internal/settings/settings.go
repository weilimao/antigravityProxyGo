package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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

type Config struct {
	DataDirectory   string `json:"dataDirectory"`
	EnableSystemLog bool   `json:"enableSystemLog"`
	IsInterceptMode bool   `json:"isInterceptMode"`
	AutoStart       bool   `json:"autoStart"`
	SilentStart     bool   `json:"silentStart"`
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
		DataDirectory:   "",
		EnableSystemLog: false,
		IsInterceptMode: false,
		AutoStart:       false,
		SilentStart:     false,
	}

	m.loadConfig()
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

	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}

	m.config = parsed

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
