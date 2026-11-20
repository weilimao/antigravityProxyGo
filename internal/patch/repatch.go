package patch

import (
	"fmt"
	"sync"
	"time"
)

// DesktopProcessController 抽象 Antigravity 桌面端的安装探测与进程启停控制，
// 以便通过依赖注入与外部进程管理解耦并支持可重复的单元测试。
type DesktopProcessController interface {
	DetectAsarPath(customPath string) (string, error)
	IsAppRunning() bool
	KillApp() error
	LaunchApp(asarPath string) error
}

// PatchAllFunc 抽象全量补丁注入回调签名。
type PatchAllFunc func(enable bool, appData, homeDir, caPath string, logCallback func(string)) error

// RepatchOptions 描述一次「重新打补丁并重启 Antigravity 桌面端」所需的运行参数。
type RepatchOptions struct {
	DefaultUserData string
	HomeDir         string
	CACertPath      string
	CustomAppPath   string
	PollInterval    time.Duration
	MaxKillWait     time.Duration
	PostExitDelay   time.Duration
}

// RepatchResult 为面向调用端的结构化执行结果。
type RepatchResult struct {
	Success    bool   `json:"success"`
	Patched    bool   `json:"patched"`
	Restarted  bool   `json:"restarted"`
	WasRunning bool   `json:"wasRunning"`
	AsarPath   string `json:"asarPath,omitempty"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}

// RepatchService 负责串行编排「关闭旧桌面端 -> 重新打补丁 -> 自动拉起桌面端」的完整闭环。
type RepatchService struct {
	mu      sync.Mutex
	ctrl    DesktopProcessController
	patchFn PatchAllFunc
}

// NewRepatchService 创建注入好进程控制器与补丁执行器的 RepatchService。
func NewRepatchService(ctrl DesktopProcessController, patchFn PatchAllFunc) *RepatchService {
	if patchFn == nil {
		patchFn = PatchAll
	}
	return &RepatchService{
		ctrl:    ctrl,
		patchFn: patchFn,
	}
}

// RepatchAndRestart 先探测并关闭正在运行的 Antigravity 桌面端以释放文件锁，
// 再重新注入全量代理补丁，最后自动重启 Antigravity 桌面端。
func (s *RepatchService) RepatchAndRestart(opts RepatchOptions, logCallback func(string)) RepatchResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if logCallback == nil {
		logCallback = func(string) {}
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 200 * time.Millisecond
	}
	if opts.MaxKillWait <= 0 {
		opts.MaxKillWait = 5 * time.Second
	}
	if opts.PostExitDelay < 0 {
		opts.PostExitDelay = 0
	}

	var asarPath string
	if s.ctrl != nil {
		if detected, err := s.ctrl.DetectAsarPath(opts.CustomAppPath); err == nil {
			asarPath = detected
		}
	}

	wasRunning := false
	if s.ctrl != nil && s.ctrl.IsAppRunning() {
		wasRunning = true
		logCallback("🔄 [重新打补丁] 检测到 Antigravity 桌面端正在运行，正在关闭以释放文件锁...")
		if err := s.ctrl.KillApp(); err != nil {
			errMsg := fmt.Sprintf("关闭 Antigravity 桌面端失败: %v", err)
			logCallback("❌ [重新打补丁] " + errMsg)
			return RepatchResult{
				Success:    false,
				WasRunning: true,
				AsarPath:   asarPath,
				Error:      errMsg,
			}
		}

		if !s.waitUntilStopped(opts.PollInterval, opts.MaxKillWait) {
			errMsg := "等待 Antigravity 桌面端进程退出超时，请手动关闭后重试"
			logCallback("❌ [重新打补丁] " + errMsg)
			return RepatchResult{
				Success:    false,
				WasRunning: true,
				AsarPath:   asarPath,
				Error:      errMsg,
			}
		}

		// 等待操作系统释放 app.asar / exe 文件句柄
		if opts.PostExitDelay > 0 {
			time.Sleep(opts.PostExitDelay)
		}
	}

	logCallback("⚙️ [重新打补丁] 正在重新注入代理补丁与环境配置...")
	if err := s.patchFn(true, opts.DefaultUserData, opts.HomeDir, opts.CACertPath, logCallback); err != nil {
		errMsg := fmt.Sprintf("重新打补丁失败: %v", err)
		logCallback("❌ [重新打补丁] " + errMsg)
		return RepatchResult{
			Success:    false,
			Patched:    false,
			WasRunning: wasRunning,
			AsarPath:   asarPath,
			Error:      errMsg,
		}
	}

	// 若桌面端安装于自定义或非默认路径，兜底对 DetectAsarPath 命中的 app.asar 再执行一次幂等注入
	if asarPath != "" {
		if err := PatchAsar(asarPath, opts.CACertPath, logCallback); err != nil {
			errMsg := fmt.Sprintf("对目标 app.asar 打补丁失败: %v", err)
			logCallback("❌ [重新打补丁] " + errMsg)
			return RepatchResult{
				Success:    false,
				Patched:    false,
				WasRunning: wasRunning,
				AsarPath:   asarPath,
				Error:      errMsg,
			}
		}
	}

	if s.ctrl == nil || asarPath == "" {
		msg := "代理配置与 CLI 补丁已重新注入（未检测到 Antigravity 桌面端安装目录，已跳过桌面端重启）"
		logCallback("✅ [重新打补丁] " + msg)
		return RepatchResult{
			Success:    true,
			Patched:    true,
			Restarted:  false,
			WasRunning: wasRunning,
			Message:    msg,
		}
	}

	logCallback("🚀 [重新打补丁] 补丁注入完成，正在自动重启 Antigravity 桌面端...")
	if err := s.ctrl.LaunchApp(asarPath); err != nil {
		errMsg := fmt.Sprintf("补丁已成功注入，但自动启动 Antigravity 桌面端失败: %v", err)
		logCallback("⚠️ [重新打补丁] " + errMsg)
		return RepatchResult{
			Success:    false,
			Patched:    true,
			Restarted:  false,
			WasRunning: wasRunning,
			AsarPath:   asarPath,
			Error:      errMsg,
		}
	}

	msg := "补丁已重新注入，并已自动重启 Antigravity 桌面端！"
	logCallback("✅ [重新打补丁] " + msg)
	return RepatchResult{
		Success:    true,
		Patched:    true,
		Restarted:  true,
		WasRunning: wasRunning,
		AsarPath:   asarPath,
		Message:    msg,
	}
}

func (s *RepatchService) waitUntilStopped(interval, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for {
		if !s.ctrl.IsAppRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
