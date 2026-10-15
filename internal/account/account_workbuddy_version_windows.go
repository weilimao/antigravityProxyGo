//go:build windows

package account

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// detectVersionFromRegistry 扫描 Windows 注册表中的已安装软件列表寻找 WorkBuddy。
func detectVersionFromRegistry() string {
	roots := []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE}
	subKeyPath := `Software\Microsoft\Windows\CurrentVersion\Uninstall`

	highest := ""
	for _, root := range roots {
		k, err := registry.OpenKey(root, subKeyPath, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		subKeys, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}

		for _, sub := range subKeys {
			itemKey, err := registry.OpenKey(root, subKeyPath+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			displayName, _, _ := itemKey.GetStringValue("DisplayName")
			displayVersion, _, _ := itemKey.GetStringValue("DisplayVersion")
			itemKey.Close()

			nameLower := strings.ToLower(displayName)
			if strings.Contains(nameLower, "workbuddy") || strings.Contains(nameLower, "codebuddy") {
				ver := strings.TrimSpace(displayVersion)
				if ver != "" && compareSemver(ver, highest) > 0 {
					highest = ver
				}
			}
		}
	}

	return highest
}
