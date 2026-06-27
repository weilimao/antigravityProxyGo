//go:build !windows
package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func setOSAutoStart(enabled bool) error {
	if runtime.GOOS != "darwin" {
		return nil // 非 macOS 的其他非 Windows 系统保持空实现
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	plistPath := filepath.Join(plistDir, "com.antigravity.proxy.plist")

	if !enabled {
		if _, err := os.Stat(plistPath); err == nil {
			_ = os.Remove(plistPath)
		}
		return nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return err
	}

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.antigravity.proxy</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>--autostart</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>`, exePath)

	return os.WriteFile(plistPath, []byte(plistContent), 0644)
}
