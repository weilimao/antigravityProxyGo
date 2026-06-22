//go:build windows
package settings

import (
	"os"
	"golang.org/x/sys/windows/registry"
)

const registryKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const registryValueName = "AntigravityProxy"

func setOSAutoStart(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if enabled {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		// Register current executable with --autostart flag
		return k.SetStringValue(registryValueName, `"`+exePath+`" --autostart`)
	} else {
		// Ignore error if it doesn't exist
		_ = k.DeleteValue(registryValueName)
		return nil
	}
}
