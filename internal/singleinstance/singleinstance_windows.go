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
	// CreateMutex name prefix "Local\" restricts the mutex to the user session
	mutexName, err := windows.UTF16PtrFromString("Local\\" + name)
	if err != nil {
		return nil, err
	}

	// CreateMutex with bInitialOwner = true (second argument)
	handle, err := windows.CreateMutex(nil, true, mutexName)
	if err != nil {
		return nil, err
	}

	// If the mutex already exists, the bInitialOwner parameter is ignored.
	// We must try to acquire the mutex ownership with a 0-millisecond wait.
	if err == windows.ERROR_ALREADY_EXISTS {
		event, waitErr := windows.WaitForSingleObject(handle, 1000)
		if waitErr != nil {
			_ = windows.CloseHandle(handle)
			return nil, waitErr
		}
		// If wait timed out, it means another active instance owns the mutex.
		if event == WAIT_TIMEOUT || event == WAIT_FAILED {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("instance already exists")
		}
	}

	return &Lock{handle: handle}, nil
}

// Unlock releases the named mutex handle.
func (l *Lock) Unlock() {
	if l.handle != 0 {
		_ = windows.ReleaseMutex(l.handle)
		_ = windows.CloseHandle(l.handle)
		l.handle = 0
	}
}

// ShowAlreadyRunningMessage shows a native Windows message box.
func ShowAlreadyRunningMessage() {
	titlePtr, _ := windows.UTF16PtrFromString("提示")
	textPtr, _ := windows.UTF16PtrFromString("Antigravity Proxy 已经在运行中。")
	windows.MessageBox(0, textPtr, titlePtr, windows.MB_OK|windows.MB_ICONINFORMATION)
}
