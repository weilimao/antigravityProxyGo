//go:build windows

package main

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// app_window_win32_windows.go: Win32 跨线程"显示主窗口"保底实现。

var (
	modUser32Win = syscall.NewLazyDLL("user32.dll")

	procEnumWindows         = modUser32Win.NewProc("EnumWindows")
	procGetWindowThreadPID  = modUser32Win.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW      = modUser32Win.NewProc("GetWindowTextW")
	procIsIconic            = modUser32Win.NewProc("IsIconic")
	procShowWindowAsync     = modUser32Win.NewProc("ShowWindowAsync")
	procSetForegroundWindow = modUser32Win.NewProc("SetForegroundWindow")
	procBringWindowToTop    = modUser32Win.NewProc("BringWindowToTop")

	procGetCurrentProcessID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcessId")
)

const (
	swShow    = 5
	swRestore = 9
)

func (a *App) foregroundFallback() {
	defer func() { _ = recover() }()

	pidPtr, _, _ := procGetCurrentProcessID.Call()
	pid := uint32(pidPtr)
	if pid == 0 {
		return
	}
	var hwnd uintptr

	cb := syscall.NewCallback(func(h uintptr, l uintptr) uintptr {
		var wpid uint32
		_, _, _ = procGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1
		}

		// 必须严格校验窗口标题：仅匹配包含 "antigravity-proxy" 的控制台主窗口，
		// 严禁将底层无标题的隐藏消息窗口暴露到前台，彻底杜绝任务栏双窗口。
		buf := make([]uint16, 256)
		ret, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
		if ret == 0 {
			return 1
		}
		title := strings.ToLower(syscall.UTF16ToString(buf))
		if !strings.Contains(title, "antigravity-proxy") {
			return 1
		}

		hwnd = h
		return 0
	})

	for attempt := 0; attempt < 3; attempt++ {
		hwnd = 0
		_, _, _ = procEnumWindows.Call(cb, 0)
		if hwnd == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
			_, _, _ = procShowWindowAsync.Call(hwnd, swRestore)
		} else {
			_, _, _ = procShowWindowAsync.Call(hwnd, swShow)
		}

		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForegroundWindow.Call(hwnd)

		time.Sleep(200 * time.Millisecond)
	}
}
