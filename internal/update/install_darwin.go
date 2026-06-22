//go:build darwin

package update

import (
	"os/exec"
)

func installUpdateOS(filePath string) error {
	cmd := exec.Command("open", filePath)
	return cmd.Start()
}
