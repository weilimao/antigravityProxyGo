package antigravitybg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// PathResolver 负责跨平台解析 Antigravity 桌面端的安装路径、asar 位置及进程状态。
type PathResolver struct{}

// NewPathResolver 创建路径解析器。
func NewPathResolver() *PathResolver {
	return &PathResolver{}
}

// GetOS 返回当前运行的操作系统名称 ("darwin", "windows", "linux")。
func (r *PathResolver) GetOS() string {
	return runtime.GOOS
}

// DetectAsarPath 自动探测或根据自定义路径解析 Antigravity 的 app.asar 绝对路径。
func (r *PathResolver) DetectAsarPath(customPath string) (string, error) {
	if customPath != "" {
		cleaned := filepath.Clean(customPath)
		// 如果直接传入 app.asar 文件
		if strings.HasSuffix(strings.ToLower(cleaned), "app.asar") {
			if _, err := os.Stat(cleaned); err == nil {
				return cleaned, nil
			}
		}
		// 如果传入的是目录 (如 macOS .app 或 Windows 安装目录)
		candidates := r.getCandidatesForDir(cleaned)
		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				return cand, nil
			}
		}
	}

	// 自动探测系统默认路径
	defaultCandidates := r.getDefaultCandidates()
	for _, cand := range defaultCandidates {
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}

	return "", fmt.Errorf("未找到 Antigravity 桌面端安装目录 (OS: %s)", runtime.GOOS)
}

// ResolveAppDir 根据 asar 路径解析 Antigravity 的应用根目录。
func (r *PathResolver) ResolveAppDir(asarPath string) string {
	if asarPath == "" {
		return ""
	}
	cleaned := filepath.Clean(asarPath)
	if runtime.GOOS == "darwin" {
		// /Applications/Antigravity.app/Contents/Resources/app.asar -> /Applications/Antigravity.app
		idx := strings.Index(cleaned, ".app")
		if idx != -1 {
			return cleaned[:idx+4]
		}
	}
	// Windows / Linux: .../Programs/antigravity/resources/app.asar -> .../Programs/antigravity
	resourcesDir := filepath.Dir(cleaned)
	if strings.EqualFold(filepath.Base(resourcesDir), "resources") {
		return filepath.Dir(resourcesDir)
	}
	return filepath.Dir(cleaned)
}

// getCandidatesForDir 获取针对某一目录的候选 asar 路径。
func (r *PathResolver) getCandidatesForDir(dir string) []string {
	if runtime.GOOS == "darwin" {
		return []string{
			filepath.Join(dir, "Contents", "Resources", "app.asar"),
			filepath.Join(dir, "Resources", "app.asar"),
			filepath.Join(dir, "resources", "app.asar"),
			filepath.Join(dir, "app.asar"),
		}
	}
	return []string{
		filepath.Join(dir, "resources", "app.asar"),
		filepath.Join(dir, "Resources", "app.asar"),
		filepath.Join(dir, "app.asar"),
	}
}

// getDefaultCandidates 获取当前操作系统的默认候选安装路径。
func (r *PathResolver) getDefaultCandidates() []string {
	candidates := make([]string, 0)

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Antigravity.app/Contents/Resources/app.asar",
			"/Applications/Antigravity IDE.app/Contents/Resources/app.asar",
		)
		if home := os.Getenv("HOME"); home != "" {
			candidates = append(candidates,
				filepath.Join(home, "Applications", "Antigravity.app", "Contents", "Resources", "app.asar"),
				filepath.Join(home, "Applications", "Antigravity IDE.app", "Contents", "Resources", "app.asar"),
			)
		}
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Programs", "antigravity", "resources", "app.asar"),
				filepath.Join(localAppData, "Programs", "Antigravity", "resources", "app.asar"),
				filepath.Join(localAppData, "Programs", "Antigravity IDE", "resources", "app.asar"),
				filepath.Join(localAppData, "antigravity", "resources", "app.asar"),
			)
		}
		if progFiles := os.Getenv("ProgramFiles"); progFiles != "" {
			candidates = append(candidates,
				filepath.Join(progFiles, "antigravity", "resources", "app.asar"),
				filepath.Join(progFiles, "Antigravity", "resources", "app.asar"),
				filepath.Join(progFiles, "Antigravity IDE", "resources", "app.asar"),
			)
		}
		if progFilesX86 := os.Getenv("ProgramFiles(x86)"); progFilesX86 != "" {
			candidates = append(candidates,
				filepath.Join(progFilesX86, "antigravity", "resources", "app.asar"),
				filepath.Join(progFilesX86, "Antigravity", "resources", "app.asar"),
			)
		}
	default:
		// Linux
		if home := os.Getenv("HOME"); home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "share", "antigravity", "resources", "app.asar"),
				"/opt/antigravity/resources/app.asar",
				"/usr/lib/antigravity/resources/app.asar",
			)
		}
	}

	return candidates
}

// IsAppRunning 跨平台检测 Antigravity 是否正在运行。
func (r *PathResolver) IsAppRunning() bool {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pgrep", "-i", "antigravity")
		setHideWindow(cmd)
		out, err := cmd.Output()
		return err == nil && len(strings.TrimSpace(string(out))) > 0
	case "windows":
		cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq Antigravity.exe", "/NH")
		setHideWindow(cmd)
		out, err := cmd.Output()
		if err == nil {
			str := string(out)
			return strings.Contains(strings.ToLower(str), "antigravity.exe")
		}
		return false
	default:
		cmd := exec.Command("pgrep", "-f", "antigravity")
		setHideWindow(cmd)
		out, err := cmd.Output()
		return err == nil && len(strings.TrimSpace(string(out))) > 0
	}
}

// KillApp 跨平台安全结束 Antigravity 进程以释放文件锁。
func (r *PathResolver) KillApp() error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pkill", "-i", "antigravity")
		setHideWindow(cmd)
		_ = cmd.Run()
	case "windows":
		cmd := exec.Command("taskkill", "/F", "/IM", "Antigravity.exe")
		setHideWindow(cmd)
		_ = cmd.Run()
	default:
		cmd := exec.Command("pkill", "-f", "antigravity")
		setHideWindow(cmd)
		_ = cmd.Run()
	}
	return nil
}

// LaunchApp 跨平台启动/拉起 Antigravity 桌面端。
func (r *PathResolver) LaunchApp(asarPath string) error {
	appDir := r.ResolveAppDir(asarPath)

	switch runtime.GOOS {
	case "darwin":
		var cmd *exec.Cmd
		if strings.HasSuffix(appDir, ".app") {
			cmd = exec.Command("open", appDir)
		} else {
			cmd = exec.Command("open", "-a", "Antigravity")
		}
		setHideWindow(cmd)
		return cmd.Start()
	case "windows":
		exePath := filepath.Join(appDir, "Antigravity.exe")
		if _, err := os.Stat(exePath); err == nil {
			cmd := exec.Command("cmd", "/c", "start", "", exePath)
			setHideWindow(cmd)
			return cmd.Start()
		}
		return fmt.Errorf("未找到可执行文件: %s", exePath)
	default:
		cmd := exec.Command("antigravity")
		setHideWindow(cmd)
		return cmd.Start()
	}
}
