package singleinstance

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestSingleInstance_CrossProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only test")
	}

	if os.Getenv("TEST_SINGLEINSTANCE_HELPER") == "1" {
		lock, err := TryLock("test-singleinstance-crossproc")
		if err != nil {
			os.Exit(1) // 失败
		}
		defer lock.Unlock()
		os.Exit(0) // 成功
	}

	lock1, err := TryLock("test-singleinstance-crossproc")
	if err != nil {
		t.Fatalf("Failed to acquire primary lock: %v", err)
	}
	defer lock1.Unlock()

	// 启动子进程尝试获取同一个锁
	cmd := exec.Command(os.Args[0], "-test.run=TestSingleInstance_CrossProcess")
	cmd.Env = append(os.Environ(), "TEST_SINGLEINSTANCE_HELPER=1")
	err = cmd.Run()
	if err == nil {
		t.Fatal("Expected child process to fail acquiring lock, but it succeeded")
	}

	// 主进程释放后，子进程应该能获取
	lock1.Unlock()

	cmd2 := exec.Command(os.Args[0], "-test.run=TestSingleInstance_CrossProcess")
	cmd2.Env = append(os.Environ(), "TEST_SINGLEINSTANCE_HELPER=1")
	err = cmd2.Run()
	if err != nil {
		t.Fatalf("Expected child process to acquire lock after parent release, but failed: %v", err)
	}
}
