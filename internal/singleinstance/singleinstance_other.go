//go:build !windows

package singleinstance

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock wraps the OS file handle used for locking.
type Lock struct {
	file *os.File
}

// TryLock attempts to acquire an exclusive lock on a lock file in the system temp directory.
func TryLock(name string) (*Lock, error) {
	lockFilePath := filepath.Join(os.TempDir(), name+".lock")
	file, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, err
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("instance already exists: %v", err)
	}

	return &Lock{file: file}, nil
}

// Unlock releases the file lock and closes the file handle.
func (l *Lock) Unlock() {
	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		_ = l.file.Close()
		l.file = nil
	}
}

// ShowAlreadyRunningMessage prints a message to stderr.
func ShowAlreadyRunningMessage() {
	_, _ = fmt.Fprintln(os.Stderr, "Antigravity Proxy is already running.")
}
