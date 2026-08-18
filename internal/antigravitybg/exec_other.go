//go:build !windows

package antigravitybg

import (
	"os/exec"
)

// setHideWindow 非 Windows 平台的空实现。
func setHideWindow(cmd *exec.Cmd) {
	// macOS / Linux 不需要 Windows 特有的 CreationFlags
}
