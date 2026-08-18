package antigravitybg

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager 统一管理 Antigravity 桌面端壁纸注入、备份恢复、壁纸库与状态探测。
type Manager struct {
	mu           sync.RWMutex
	pathResolver *PathResolver
	asarHandler  *AsarHandler
	configDir    string
	cachedConfig WallpaperConfig
	gallery      []SavedWallpaper
}

// NewManager 创建 Manager 实例。
func NewManager(configDir string) *Manager {
	m := &Manager{
		pathResolver: NewPathResolver(),
		asarHandler:  NewAsarHandler(),
		configDir:    configDir,
		cachedConfig: WallpaperConfig{
			Enabled:          false,
			ImageSource:      "base64",
			Opacity:          0.35,
			Blur:             8,
			DarkOverlay:      0.3,
			GlassAlpha:       0.85,
			BackgroundFit:    "cover",
			ThemeMode:        "dark",
			SidebarTextColor:   "#f1f5f9",
			ContentTextColor:   "#ffffff",
			UserMessageBgColor:  "rgba(255, 255, 255, 0.08)",
			AgentMessageBgColor: "rgba(15, 18, 28, 0.75)",
			ComposerBgColor:     "rgba(18, 22, 34, 0.88)",
			SidebarBgColor:      "rgba(12, 15, 24, 0.85)",
			ModalBgColor:        "rgba(18, 22, 34, 0.94)",
			CodeBlockBgColor:    "rgba(10, 12, 20, 0.92)",
			AutoDarkTheme:       true,
			ColorTheme:          "Default Dark Modern",
		},
		gallery: make([]SavedWallpaper, 0),
	}
	m.loadStoredConfig()
	m.loadGallery()
	return m
}

// GetStatus 获取当前 Antigravity 桌面端状态与壁纸库摘要列表 (轻量化传输)。
func (m *Manager) GetStatus(customPath string) StatusResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := StatusResponse{
		OS:        m.pathResolver.GetOS(),
		Config:    m.cachedConfig,
		Gallery:   m.buildGallerySummariesLocked(),
		Installed: false,
		Patched:   false,
		HasBackup: false,
		IsRunning: m.pathResolver.IsAppRunning(),
	}

	asarPath, err := m.pathResolver.DetectAsarPath(customPath)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	res.Installed = true
	res.AsarPath = asarPath
	res.AppPath = m.pathResolver.ResolveAppDir(asarPath)

	bakPath := asarPath + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		res.HasBackup = true
		res.Patched = true
	}

	return res
}

// buildGallerySummariesLocked 返回剥离了巨型 Base64 字符串的轻量级壁纸摘要 (调用方需持锁)。
func (m *Manager) buildGallerySummariesLocked() []SavedWallpaper {
	out := make([]SavedWallpaper, len(m.gallery))
	for i, item := range m.gallery {
		summary := item
		// 如果是大体积 base64 数据 (> 2048 字节)，列表摘要中置空 data，仅保留 thumbnail/filePath 等元数据
		if len(summary.Data) > 2048 {
			summary.Data = ""
		}
		out[i] = summary
	}
	return out
}

// GetGallery 返回轻量壁纸库摘要列表 (秒级 IPC 传输)。
func (m *Manager) GetGallery() []SavedWallpaper {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildGallerySummariesLocked()
}

// GetFullGallery 返回包含完整原始 Data 字段的壁纸列表 (供内部及测试使用)。
func (m *Manager) GetFullGallery() []SavedWallpaper {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SavedWallpaper, len(m.gallery))
	copy(out, m.gallery)
	return out
}

// GetWallpaperData 根据壁纸 ID 按需获取完整 Base64 或 URL 数据。
func (m *Manager) GetWallpaperData(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, item := range m.gallery {
		if item.ID == id {
			if item.Data != "" {
				return item.Data, nil
			}
			if item.FilePath != "" {
				if bytes, err := os.ReadFile(item.FilePath); err == nil {
					mimeType := "image/jpeg"
					ext := strings.ToLower(filepath.Ext(item.FilePath))
					switch ext {
					case ".png":
						mimeType = "image/png"
					case ".webp":
						mimeType = "image/webp"
					case ".gif":
						mimeType = "image/gif"
					case ".mp4":
						mimeType = "video/mp4"
					case ".webm":
						mimeType = "video/webm"
					case ".mov":
						mimeType = "video/quicktime"
					}
					return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(bytes)), nil
				}
			}
			return "", fmt.Errorf("壁纸媒体数据为空且本地文件不可读")
		}
	}
	return "", fmt.Errorf("未找到对应壁纸条目: %s", id)
}

// SaveGallery 全量更新并保存壁纸库 (具备大图字段保留合并机制)。
func (m *Manager) SaveGallery(items []SavedWallpaper) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dataMap := make(map[string]SavedWallpaper, len(m.gallery))
	for _, oldItem := range m.gallery {
		dataMap[oldItem.ID] = oldItem
	}

	merged := make([]SavedWallpaper, len(items))
	for i, it := range items {
		if it.Data == "" {
			if old, ok := dataMap[it.ID]; ok {
				it.Data = old.Data
				if it.FilePath == "" {
					it.FilePath = old.FilePath
				}
			}
		}
		merged[i] = it
	}

	m.gallery = merged
	return m.saveGalleryToDisk()
}

// AddWallpaperToGallery 向壁纸库中追加或更新单张壁纸，并返回轻量摘要列表。
func (m *Manager) AddWallpaperToGallery(item SavedWallpaper) []SavedWallpaper {
	m.mu.Lock()
	defer m.mu.Unlock()

	if item.CreatedAt == 0 {
		item.CreatedAt = time.Now().UnixMilli()
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("wp_%d", item.CreatedAt)
	}

	// 避免巨型 Base64 驻留 Go 堆内存
	if item.FilePath != "" && len(item.Data) > 2048 {
		item.Data = ""
	}

	exists := false
	for i, existing := range m.gallery {
		if existing.ID == item.ID || (existing.FilePath != "" && existing.FilePath == item.FilePath) {
			m.gallery[i] = item
			exists = true
			break
		}
	}
	if !exists {
		// 插入到最前面
		m.gallery = append([]SavedWallpaper{item}, m.gallery...)
	}

	_ = m.saveGalleryToDisk()
	return m.buildGallerySummariesLocked()
}

// RemoveWallpaperFromGallery 从壁纸库中移除指定 ID 的壁纸，并返回轻量摘要列表。
func (m *Manager) RemoveWallpaperFromGallery(id string) []SavedWallpaper {
	m.mu.Lock()
	defer m.mu.Unlock()

	newGallery := make([]SavedWallpaper, 0, len(m.gallery))
	for _, item := range m.gallery {
		if item.ID != id {
			newGallery = append(newGallery, item)
		}
	}
	m.gallery = newGallery
	_ = m.saveGalleryToDisk()

	return m.buildGallerySummariesLocked()
}

// ApplyWallpaper 应用壁纸与调谐配置至 Antigravity 桌面端。
func (m *Manager) ApplyWallpaper(customPath string, cfg WallpaperConfig) ApplyResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	asarPath, err := m.pathResolver.DetectAsarPath(customPath)
	if err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("定位 Antigravity 失败: %v", err),
		}
	}

	wasRunning := m.pathResolver.IsAppRunning()
	if wasRunning {
		_ = m.pathResolver.KillApp()
		// 等待系统释放文件句柄
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if !m.pathResolver.IsAppRunning() {
				break
			}
		}
	}

	bakPath := asarPath + ".bak"
	// 首次注入前必须安全备份原版
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		if err := copyFile(asarPath, bakPath); err != nil {
			return ApplyResult{
				Success: false,
				Error:   fmt.Sprintf("备份原版 app.asar.bak 失败: %v", err),
			}
		}
	}

	// 创建临时工作目录
	tempDir, err := os.MkdirTemp("", "antigravity_bg_patch_*")
	if err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("创建临时解包目录失败: %v", err),
		}
	}
	defer os.RemoveAll(tempDir)

	// 解包 asar
	if err := m.asarHandler.ExtractAsar(asarPath, tempDir); err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("解包 app.asar 失败: %v", err),
		}
	}

	// 自动从 ImageData 中反解 FilePath (如果 FilePath 为空且 ImageData 含有 /local-media?path=)
	if strings.TrimSpace(cfg.FilePath) == "" && strings.Contains(cfg.ImageData, "/local-media?path=") {
		u, err := url.Parse(cfg.ImageData)
		if err == nil {
			q := u.Query()
			extractedPath := q.Get("path")
			if extractedPath != "" {
				cfg.FilePath = extractedPath
			}
		}
	}
	if strings.TrimSpace(cfg.FilePath) != "" {
		lowerPath := strings.ToLower(cfg.FilePath)
		if strings.HasSuffix(lowerPath, ".mp4") || strings.HasSuffix(lowerPath, ".webm") ||
			strings.HasSuffix(lowerPath, ".mov") || strings.HasSuffix(lowerPath, ".mkv") {
			cfg.MediaType = "video"
		} else {
			cfg.MediaType = "image"
		}
	}

	// 注入壁纸补丁
	if err := m.asarHandler.PatchPreload(tempDir, cfg); err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("注入 Preload 补丁失败: %v", err),
		}
	}

	// 注册原生特权媒体流协议 (支持巨型大视频无损硬件流式硬解)
	_ = m.asarHandler.PatchCustomScheme(tempDir)

	// 同步确保代理流量拦截环境变量在 languageServer.js 中生效 (共存架构)
	_ = m.asarHandler.PatchLanguageServer(tempDir)

	// 重打包 asar
	if err := m.asarHandler.PackAsar(tempDir, asarPath); err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("重新打包 app.asar 失败: %v", err),
		}
	}

	// 若开启了自动深色主题，则将 Antigravity 用户 settings.json 切换至深色模式 (例如 Default Dark Modern)
	if cfg.AutoDarkTheme {
		theme := cfg.ColorTheme
		if strings.TrimSpace(theme) == "" {
			theme = "Default Dark Modern"
		}
		_ = m.SyncThemeToSettings(theme)
	}

	// 更新并保存配置
	m.cachedConfig = cfg
	_ = m.saveStoredConfig(cfg)

	msg := "壁纸与外观调谐已成功应用到 Antigravity 桌面端！"
	if wasRunning {
		_ = m.pathResolver.LaunchApp(asarPath)
		msg = "壁纸与外观调谐已成功应用，并已自动为您重新拉起 Antigravity 呈现全新壁纸！"
	}

	return ApplyResult{
		Success: true,
		Message: msg,
	}
}

// RestoreDefault 恢复 Antigravity 出厂默认界面。
func (m *Manager) RestoreDefault(customPath string) ApplyResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	asarPath, err := m.pathResolver.DetectAsarPath(customPath)
	if err != nil {
		return ApplyResult{
			Success: false,
			Error:   fmt.Sprintf("定位 Antigravity 失败: %v", err),
		}
	}

	wasRunning := m.pathResolver.IsAppRunning()
	if wasRunning {
		_ = m.pathResolver.KillApp()
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if !m.pathResolver.IsAppRunning() {
				break
			}
		}
	}

	bakPath := asarPath + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		// 直接从备份恢复
		if err := copyFile(bakPath, asarPath); err != nil {
			return ApplyResult{
				Success: false,
				Error:   fmt.Sprintf("从备份还原 app.asar 失败: %v", err),
			}
		}
		// 删除备份标记
		_ = os.Remove(bakPath)
	} else {
		// 无备份文件时，解包并移除补丁代码
		tempDir, err := os.MkdirTemp("", "antigravity_bg_restore_*")
		if err != nil {
			return ApplyResult{
				Success: false,
				Error:   fmt.Sprintf("创建临时目录失败: %v", err),
			}
		}
		defer os.RemoveAll(tempDir)

		if err := m.asarHandler.ExtractAsar(asarPath, tempDir); err != nil {
			return ApplyResult{
				Success: false,
				Error:   fmt.Sprintf("解包 app.asar 失败: %v", err),
			}
		}

		preloadPath := filepath.Join(tempDir, "dist", "preload.js")
		if b, err := os.ReadFile(preloadPath); err == nil {
			cleaned := m.asarHandler.RemovePatchFromContent(string(b))
			_ = os.WriteFile(preloadPath, []byte(cleaned), 0644)
		}

		if err := m.asarHandler.PackAsar(tempDir, asarPath); err != nil {
			return ApplyResult{
				Success: false,
				Error:   fmt.Sprintf("重新打包 app.asar 失败: %v", err),
			}
		}
	}

	m.cachedConfig.Enabled = false
	_ = m.saveStoredConfig(m.cachedConfig)

	msg := "已成功恢复 Antigravity 出厂默认界面！"
	if wasRunning {
		_ = m.pathResolver.LaunchApp(asarPath)
		msg = "已成功恢复 Antigravity 出厂默认界面，并已重新启动！"
	}

	return ApplyResult{
		Success: true,
		Message: msg,
	}
}

// LaunchApp 拉起或重启 Antigravity 桌面端。
func (m *Manager) LaunchApp(customPath string) error {
	asarPath, err := m.pathResolver.DetectAsarPath(customPath)
	if err != nil {
		return err
	}
	return m.pathResolver.LaunchApp(asarPath)
}

func (m *Manager) loadStoredConfig() {
	if m.configDir == "" {
		return
	}
	cfgFile := filepath.Join(m.configDir, "antigravity_wallpaper.json")
	if data, err := os.ReadFile(cfgFile); err == nil {
		var cfg WallpaperConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			if cfg.FilePath != "" && len(cfg.ImageData) > 2048 {
				cfg.ImageData = ""
			}
			m.cachedConfig = cfg
		}
	}
}

func (m *Manager) saveStoredConfig(cfg WallpaperConfig) error {
	if m.configDir == "" {
		return nil
	}
	_ = os.MkdirAll(m.configDir, 0755)
	cfgFile := filepath.Join(m.configDir, "antigravity_wallpaper.json")
	// 本地媒体避免存储大体积 base64
	if cfg.FilePath != "" && len(cfg.ImageData) > 2048 {
		cfg.ImageData = ""
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgFile, data, 0644)
}

func (m *Manager) loadGallery() {
	if m.configDir == "" {
		return
	}
	galleryFile := filepath.Join(m.configDir, "antigravity_wallpapers_gallery.json")
	if data, err := os.ReadFile(galleryFile); err == nil {
		var items []SavedWallpaper
		if err := json.Unmarshal(data, &items); err == nil {
			cleaned := false
			for i := range items {
				// 自动清洗历史脏数据：若已有本地文件路径且携带大体积 Base64，清空 item.Data 避免占用 Go 堆内存
				if items[i].FilePath != "" && len(items[i].Data) > 2048 {
					items[i].Data = ""
					cleaned = true
				}
			}
			m.gallery = items
			if cleaned {
				_ = m.saveGalleryToDisk()
			}
		}
	}
}

func (m *Manager) saveGalleryToDisk() error {
	if m.configDir == "" {
		return nil
	}
	_ = os.MkdirAll(m.configDir, 0755)
	galleryFile := filepath.Join(m.configDir, "antigravity_wallpapers_gallery.json")
	data, err := json.MarshalIndent(m.gallery, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(galleryFile, data, 0644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// SyncThemeToSettings 将深色主题写入 Antigravity 用户配置文件 settings.json 与 ~/.gemini/config/config.json。
func (m *Manager) SyncThemeToSettings(themeName string) error {
	if strings.TrimSpace(themeName) == "" {
		themeName = "Default Dark Modern"
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	// 1. 同步 Antigravity 专属前端 UI 配置文件 (~/.gemini/config/config.json 中的 userSettings.themeMode = "THEME_MODE_DARK")
	if homeDir != "" {
		geminiConfigPath := filepath.Join(homeDir, ".gemini", "config", "config.json")
		var conf map[string]interface{}
		if data, err := os.ReadFile(geminiConfigPath); err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &conf)
		}
		if conf == nil {
			conf = make(map[string]interface{})
		}

		userSettings, ok := conf["userSettings"].(map[string]interface{})
		if !ok || userSettings == nil {
			userSettings = make(map[string]interface{})
			conf["userSettings"] = userSettings
		}
		userSettings["themeMode"] = "THEME_MODE_DARK"

		if out, err := json.MarshalIndent(conf, "", "  "); err == nil {
			_ = os.MkdirAll(filepath.Dir(geminiConfigPath), 0755)
			_ = os.WriteFile(geminiConfigPath, out, 0644)
		}
	}

	// 2. 同步 Antigravity IDE (VSCode Shell) settings.json
	appData := os.Getenv("APPDATA")
	var possibleSettingsPaths []string

	if appData != "" {
		possibleSettingsPaths = append(possibleSettingsPaths,
			filepath.Join(appData, "Antigravity", "User", "settings.json"),
			filepath.Join(appData, "Antigravity IDE", "User", "settings.json"),
			filepath.Join(appData, "Antigravity-Agent", "User", "settings.json"),
		)
	}

	if homeDir != "" {
		possibleSettingsPaths = append(possibleSettingsPaths,
			filepath.Join(homeDir, "AppData", "Roaming", "Antigravity", "User", "settings.json"),
			filepath.Join(homeDir, "AppData", "Roaming", "Antigravity IDE", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Antigravity", "User", "settings.json"),
			filepath.Join(homeDir, "Library", "Application Support", "Antigravity IDE", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Antigravity", "User", "settings.json"),
			filepath.Join(homeDir, ".config", "Antigravity IDE", "User", "settings.json"),
		)
	}

	for _, sPath := range possibleSettingsPaths {
		if _, err := os.Stat(sPath); os.IsNotExist(err) {
			continue
		}

		data, err := os.ReadFile(sPath)
		if err != nil {
			continue
		}

		var settings map[string]interface{}
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}

		settings["workbench.colorTheme"] = themeName

		bytesData, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			continue
		}

		_ = os.WriteFile(sPath, bytesData, 0644)
	}

	return nil
}
