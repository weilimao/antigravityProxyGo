//go:build !windows

package main

// app_window_win32_other.go: 非 Windows 平台的 foregroundFallback no-op stub。
//
// 设计理由:
//   showMainWindow() 的 Win32 跨线程保底仅 Windows 有意义 (Wails UI 主线程消息
//   队列耦合);macOS/Linux 的 Wails 前端用各自原生窗口工具包,无同款单队列串行
//   瓶颈。且本程序 macOS 下托盘不可用 (tray_other.go setupTray 空),
//   onBeforeClose 直接放行销毁 (app_tray.go darwin 分支返回 false),无"隐藏到
//   托盘后打不开"场景。
//
//   保留空方法是为了让 app.go 的 showMainWindow() 无 //go:build 分支地统一调用,
//   避免调用点长平台开关分支。

// foregroundFallback 在非 Windows 平台是 no-op。
// 正规路径 wailsRuntime.WindowShow 已足够,无需跨线程保底。
func (a *App) foregroundFallback() {
	// no-op
}
