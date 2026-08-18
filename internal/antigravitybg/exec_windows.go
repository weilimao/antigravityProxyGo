//go:build windows

package antigravitybg

import (
	"os/exec"
	"syscall"
)

// setHideWindow 在 Windows 下彻底静默执行子进程，防止弹出任何 CMD/Console 黑色控制台窗口。
func setHideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = 0x08000000 // CREATE_NO_WINDOW
}
