//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

// app_window_win32_windows.go: Win32 跨线程"显示主窗口"保底实现。
//
// 设计背景:
//   Wails Windows 前端的所有窗口操作 (WindowShow / WindowExecJS / EventsEmit)
//   都走 f.mainWindow.Invoke(func(){...}) → w32.PostMessage(wmInvokeCallback),
//   落到同一个 LockOSThread 的主 UI 线程消息队列串行执行。当主线程正被
//   WebView2 的 COM 回调 (processRequest / NavigationCompleted / 初始化)
//   占用时,正规 ShowWindow 的 Invoke 闭包排队出不来 → 用户看到"点打开没反应"。
//
//   本文件提供一条完全不依赖主 UI 线程消息队列的保底唤醒路径:
//   ShowWindowAsync 是 Win32 异步版,不阻塞调用线程,由内核替目标窗口
//   投递 WM_SHOW/WM_RESTORE,绕开主线程 Invoke 队列。即便主线程被 COM
//   占用,窗口仍能被内核唤醒可见。
//
// 复用约定:
//   仅由 showMainWindow() 的保底 goroutine 调用,本身不再 go。
//   recover 兜底,任何 Win32 调用失败均静默退出,绝不影响主流程。
//
// 选址理由:
//   放在 main 包而非 internal/tray,是因为它服务的是 App 主窗口,
//   被 app_tray.go (托盘 onShow) 与 app_lifecycle.go (domReady) 共用,
//   而非托盘自身职责;与 app.go 的 SetWindowVisible 同域更内聚。

var (
	// user32 进程级单例:本文件与 internal/tray/tray_windows.go 各自持有
	// 独立 LazyDLL 实例,Go 层互不共享,但底层都绑同一 user32.dll 模块句柄,
	// 不冲突、不泄漏。
	modUser32Win = syscall.NewLazyDLL("user32.dll")

	procEnumWindows         = modUser32Win.NewProc("EnumWindows")
	procGetWindowThreadPID  = modUser32Win.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible     = modUser32Win.NewProc("IsWindowVisible")
	procIsIconic            = modUser32Win.NewProc("IsIconic")
	procShowWindowAsync     = modUser32Win.NewProc("ShowWindowAsync") // 关键:异步,不阻塞调用线程
	procSetForegroundWindow = modUser32Win.NewProc("SetForegroundWindow")

	// kernel32 取本进程 PID。
	procGetCurrentProcessID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcessId")
)

// Win32 ShowWindow 命令常量 (与 w32 包一致,本文件自带避免跨包依赖)。
const (
	swShow    = 5
	swRestore = 9
)

// foregroundFallback 在 goroutine 里枚举本进程的主可见窗口并唤醒到前台。
//
// 不缓存 HWND:每次实时 EnumWindows 按 PID 匹配 (用户确认)。
// 理由:无状态、无 domReady 时序耦合、不依赖 wails 暴露 HWND;
//       代价是每次多 1~2ms 查询,保底路径本就偶发触发,可接受。
//
// 流程:
//  1. EnumWindows 遍历所有顶层窗口,回调内按"同 PID + 可见"命中第一个即停。
//  2. 若最小化 (IsIconic) → ShowWindowAsync(SW_RESTORE);否则 ShowWindowAsync(SW_SHOW)。
//  3. SetForegroundWindow 唤到前台。
//  4. 重复 3 次 (每次间隔 200ms) 以应对 explorer 前台锁定时序退化。
//
// 安全性:
//   - syscall.NewCallback 回调在 EnumWindows 同步执行,只读闭包变量 hwnd,
//     无并发问题。
//   - 全程 recover,任何调用失败静默退出。
//   - ShowWindowAsync 是内核异步投递,不阻塞调用线程,不依赖目标窗口线程
//     的消息队列即时响应——这正是绕开"主线程队列被占"的关键。
func (a *App) foregroundFallback() {
	defer func() { _ = recover() }()

	// 取本进程 PID 用于窗口归属匹配。
	pidPtr, _, _ := procGetCurrentProcessID.Call()
	pid := uint32(pidPtr)
	if pid == 0 {
		return
	}
	// 命中的目标窗口句柄;回调通过闭包写入。
	var hwnd uintptr

	// EnumWindows 回调:返回 1 继续,返回 0 停止。
	// 仅在同进程且可见的顶层窗口中取第一个命中。
	cb := syscall.NewCallback(func(h uintptr, l uintptr) uintptr {
		var wpid uint32
		_, _, _ = procGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // continue
		}
		vis, _, _ := procIsWindowVisible.Call(h)
		if vis == 0 {
			return 1 // continue
		}
		hwnd = h
		return 0 // stop at first match
	})

	// 反复尝试 3 次,每次重新枚举(窗口可能在前一次 ShowWindowAsync 后才变可见)。
	for attempt := 0; attempt < 3; attempt++ {
		hwnd = 0
		_, _, _ = procEnumWindows.Call(cb, 0)
		if hwnd == 0 {
			// 本进程尚无可见顶层窗口(可能正规路径还没执行到),
			// 等待后重试。
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// 最小化则 SW_RESTORE 还原并激活,否则 SW_SHOW 显示。
		if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
			_, _, _ = procShowWindowAsync.Call(hwnd, swRestore)
		} else {
			_, _, _ = procShowWindowAsync.Call(hwnd, swShow)
		}

		// 唤到前台。即使因前台锁定规则退化为"只闪不激活",
		// SW_SHOW/RESTORE 已使窗口可见,用户可见即可点。
		_, _, _ = procSetForegroundWindow.Call(hwnd)

		// 间隔 200ms 再试一次,直至窗口稳定前台化或尝试耗尽。
		// (前台化成功与否无稳定探测 API,固定 3 次尝试即可。)
		time.Sleep(200 * time.Millisecond)
	}
}
