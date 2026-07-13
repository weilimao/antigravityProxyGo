//go:build !windows

package patch

// getWindowsSystemCertificates 在非 Windows 系统下返回空以满足跨平台编译
func getWindowsSystemCertificates() []byte {
	return nil
}
