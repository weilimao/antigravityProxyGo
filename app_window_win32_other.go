//go:build !windows

package main

// foregroundFallback 在非 Windows 平台是 no-op。
func (a *App) foregroundFallback() {
}
