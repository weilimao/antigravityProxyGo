package main

import (
	"context"
	"os"
	"runtime"
	"time"

	"antigravity-proxy/internal/tray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// quitWatchdogStarted 防止重复启动退出看门狗
var quitWatchdogStarted = false

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
			// 点击“显示控制面板”：显示窗口并使其获取焦点，使用 goroutine 异步执行以避免阻塞托盘自身的事件协程
			go func() {
				wailsRuntime.WindowShow(a.ctx)
				a.SetWindowVisible(true)
			}()
		},
		func() {
			// 点击“退出代理引擎”：设置退出标志并异步调用退出，避免阻塞托盘自身的事件协程
			a.SetQuitting(true)

			// 立即隐藏窗口,避免退出过程中窗口停留在"无响应"假死态
			wailsRuntime.WindowHide(a.ctx)

			// 异步发起 Wails 退出流程
			go wailsRuntime.Quit(a.ctx)

			// 进程级硬退出看门狗兜底:
			// wailsRuntime.Quit → OnShutdown 是异步链路,其中 shutdown() 内有
			// PatchAll(false) 文件 IO、scheduler.Stop()、proxyEngine.Stop() 等多个
			// 同步阻塞点,叠加问题①的死链路网络,极易导致 wails.Run 永不返回、
			//  main.go 末尾的 os.Exit(0) 兜底到不了 → 进程在任务管理器里残留不消失。
			// 此处启动一个独立 goroutine,若 5 秒内 wails.Run 仍未返回,
			// 直接 os.Exit(0) 强制终结进程,确保托盘"退出"一定能真正退出。
			if !quitWatchdogStarted {
				quitWatchdogStarted = true
				go func() {
					time.Sleep(5 * time.Second)
					// 进程末尾的 os.Exit(0) 应已执行;若仍运行至此,说明退出链路卡死。
					os.Exit(0)
				}()
			}
		},
	)
}

// onBeforeClose 拦截窗口关闭事件。
// 如果不是主动退出，则隐藏窗口并返回 true (阻止默认关闭/销毁行为)
func (a *App) onBeforeClose(ctx context.Context) bool {
	// 在 macOS 下，由于托盘不可用，窗口关闭时直接允许销毁并退出程序，避免软件“失联”在后台
	if runtime.GOOS == "darwin" {
		return false
	}
	if !a.IsQuitting() {
		wailsRuntime.WindowHide(ctx)
		a.SetWindowVisible(false)
		return true
	}
	return false
}
