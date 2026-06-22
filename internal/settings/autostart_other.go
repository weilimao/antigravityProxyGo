//go:build !windows
package settings

func setOSAutoStart(enabled bool) error {
	// No-op for non-Windows platforms
	return nil
}
