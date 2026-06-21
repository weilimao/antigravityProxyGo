package main

import (
	"context"

	"antigravity-proxy/internal/tray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// IsQuitting 返回当前应用是否正在被主动退出
func (a *App) IsQuitting() bool {
	a.isQuittingMu.RLock()
	defer a.isQuittingMu.RUnlock()
	return a.isQuitting
}

// SetQuitting 设置当前应用的退出状态
func (a *App) SetQuitting(quitting bool) {
	a.isQuittingMu.Lock()
	defer a.isQuittingMu.Unlock()
	a.isQuitting = quitting
}

// initTray 初始化并挂载系统托盘
func (a *App) initTray() {
	tray.SetupTray(
		func() {
			// 点击“显示控制面板”：显示窗口并使其获取焦点
			wailsRuntime.WindowShow(a.ctx)
		},
		func() {
			// 点击“退出代理引擎”：设置退出标志并调用退出
			a.SetQuitting(true)
			wailsRuntime.Quit(a.ctx)
		},
	)
}

// onBeforeClose 拦截窗口关闭事件。
// 如果不是主动退出，则隐藏窗口并返回 true (阻止默认关闭/销毁行为)
func (a *App) onBeforeClose(ctx context.Context) bool {
	if !a.IsQuitting() {
		wailsRuntime.WindowHide(ctx)
		return true
	}
	return false
}
