//go:build windows

package update

import (
	"fmt"
	"syscall"
	"unsafe"
)

func installUpdateOS(filePath string) error {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW := shell32.NewProc("ShellExecuteW")

	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	filePtr, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return err
	}
	paramsPtr, err := syscall.UTF16PtrFromString("/S")
	if err != nil {
		return err
	}

	// SW_SHOW = 5
	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(filePtr)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0,
		5,
	)

	// ShellExecute returns a value greater than 32 on success.
	if ret <= 32 {
		if err != nil {
			return err
		}
		return fmt.Errorf("ShellExecute failed with code %d", ret)
	}
	return nil
}
