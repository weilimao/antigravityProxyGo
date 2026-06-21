//go:build !windows

package tray

func setupTray(onShow func(), onQuit func()) {
	// No-op for non-Windows platforms in this implementation
}

func quitTray() {
	// No-op
}
