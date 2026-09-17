//go:build windows

package singleinstance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32DLL               = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows         = user32DLL.NewProc("EnumWindows")
	procGetWindowThreadPID  = user32DLL.NewProc("GetWindowThreadProcessId")
	procShowWindowAsync     = user32DLL.NewProc("ShowWindowAsync")
	procGetWindowTextW      = user32DLL.NewProc("GetWindowTextW")
	procSetForegroundWindow = user32DLL.NewProc("SetForegroundWindow")
	procBringWindowToTop    = user32DLL.NewProc("BringWindowToTop")
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
	if handle == 0 {
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

// ActivateExistingWindow 尝试查找并激活已存在的实例主窗口。
// 成功找到并唤出窗口返回 true；若未找到窗口返回 false。
func ActivateExistingWindow() bool {
	defer func() { _ = recover() }()

	myPid := uint32(os.Getpid())

	exePath, err := os.Executable()
	if err != nil {
		return false
	}
	exeName := strings.ToLower(filepath.Base(exePath))

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	otherPids := make(map[uint32]bool)
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		pName := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if pName == exeName && entry.ProcessID != myPid {
			otherPids[entry.ProcessID] = true
		}
		err = windows.Process32Next(snapshot, &entry)
	}

	if len(otherPids) == 0 {
		return false
	}

	var targetHwnd uintptr
	cb := syscall.NewCallback(func(h uintptr, l uintptr) uintptr {
		var wpid uint32
		_, _, _ = procGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if otherPids[wpid] {
			buf := make([]uint16, 256)
			ret, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
			if ret > 0 {
				title := strings.ToLower(windows.UTF16ToString(buf))
				if strings.Contains(title, "antigravity-proxy") {
					targetHwnd = h
					return 0
				}
			}
		}
		return 1
	})

	_, _, _ = procEnumWindows.Call(cb, 0)

	if targetHwnd == 0 {
		return false
	}

	const swRestore = 9
	const swShow = 5
	_, _, _ = procShowWindowAsync.Call(targetHwnd, swRestore)
	_, _, _ = procShowWindowAsync.Call(targetHwnd, swShow)
	_, _, _ = procBringWindowToTop.Call(targetHwnd)
	_, _, _ = procSetForegroundWindow.Call(targetHwnd)

	return true
}

// ShowAlreadyRunningMessage 优先激活已有实例主窗口置顶；若无可见窗口则弹消息框兜底。
func ShowAlreadyRunningMessage() {
	if ActivateExistingWindow() {
		return
	}
	titlePtr, _ := windows.UTF16PtrFromString("提示")
	textPtr, _ := windows.UTF16PtrFromString("Antigravity Proxy 已经在运行中。")
	windows.MessageBox(0, textPtr, titlePtr, windows.MB_OK|windows.MB_ICONINFORMATION)
}
