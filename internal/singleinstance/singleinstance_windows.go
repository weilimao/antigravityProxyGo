//go:build windows

package singleinstance

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// Lock wraps the Windows handle for the application named mutex.
type Lock struct {
	handle windows.Handle
}

const (
	WAIT_OBJECT_0  = 0x00000000
	WAIT_ABANDONED = 0x00000080
	WAIT_TIMEOUT   = 0x00000102
	WAIT_FAILED    = 0xFFFFFFFF
)

// TryLock attempts to acquire a named mutex to ensure only one instance runs.
func TryLock(name string) (*Lock, error) {
	mutexName, err := windows.UTF16PtrFromString("Local\\" + name)
	if err != nil {
		return nil, err
	}

	// bInitialOwner=true: 若本进程是创建者则立刻获得锁。
	handle, err := windows.CreateMutex(nil, true, mutexName)
	if err != nil {
		return nil, err
	}

	// 已存在:等待最多 1s。原实现即 1s,保持原状;若超时仍占用则报错。
	if err == windows.ERROR_ALREADY_EXISTS {
		event, waitErr := windows.WaitForSingleObject(handle, 1000)
		if waitErr != nil {
			_ = windows.CloseHandle(handle)
			return nil, waitErr
		}
		if event == WAIT_TIMEOUT || event == WAIT_FAILED {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("instance already exists")
		}
	}

	return &Lock{handle: handle}, nil
}

// Unlock 释放 mutex 句柄。
func (l *Lock) Unlock() {
	if l.handle != 0 {
		_ = windows.ReleaseMutex(l.handle)
		_ = windows.CloseHandle(l.handle)
		l.handle = 0
	}
}

// ShowAlreadyRunningMessage 恢复原实现:Windows 原生消息框提示已有实例,
// 由调用方 (main.go) 决定是否退出。不主动激活已有实例窗口。
func ShowAlreadyRunningMessage() {
	titlePtr, _ := windows.UTF16PtrFromString("提示")
	textPtr, _ := windows.UTF16PtrFromString("Antigravity Proxy 已经在运行中。")
	windows.MessageBox(0, textPtr, titlePtr, windows.MB_OK|windows.MB_ICONINFORMATION)
}
