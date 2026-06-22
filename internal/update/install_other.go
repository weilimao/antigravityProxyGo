//go:build !windows && !darwin

package update

import "errors"

func installUpdateOS(filePath string) error {
	return errors.New("仅 Windows 和 macOS 系统支持自动安装更新")
}
