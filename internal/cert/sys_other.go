//go:build !windows

package cert

import (
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {
	// No-op for non-Windows platforms (HideWindow field does not exist)
}
