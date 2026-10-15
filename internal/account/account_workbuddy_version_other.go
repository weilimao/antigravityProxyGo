//go:build !windows

package account

// detectVersionFromRegistry 在非 Windows 系统上的空实现。
func detectVersionFromRegistry() string {
	return ""
}
