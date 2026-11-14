package main

import (
	"testing"

	"antigravity-proxy/internal/account"
)

// TestHandleAccountsSendIPC_PoolToggle 验证五个负载均衡总开关(antigravity/project/nvidia/other/grok)
// 通过 IPCSend 触发后能正确更新内存状态与活跃通道、持久化至 accounts_pool.json，
// 且 emitAccountsRes() 在无窗口上下文时安全返回不 panic。
func TestHandleAccountsSendIPC_PoolToggle(t *testing.T) {
	dir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(dir)
	a := &App{accountMgr: mgr}

	// 1. 开启 Antigravity 负载均衡
	a.IPCSend("pool:toggle", `[true]`)
	if !mgr.GetPoolMode() {
		t.Fatalf("pool:toggle true: want GetPoolMode()=true, got false")
	}
	if got := mgr.GetActiveChannel(); got != "antigravity" {
		t.Fatalf("pool:toggle true: want activeChannel=antigravity, got %q", got)
	}

	// 2. 切换至 Project 负载均衡（互斥关闭 antigravity）
	a.IPCSend("pool:toggle-project", `[true]`)
	if !mgr.GetProjectPoolMode() || mgr.GetPoolMode() {
		t.Fatalf("pool:toggle-project true: want project=true & pool=false, got project=%v pool=%v",
			mgr.GetProjectPoolMode(), mgr.GetPoolMode())
	}
	if got := mgr.GetActiveChannel(); got != "project" {
		t.Fatalf("pool:toggle-project true: want activeChannel=project, got %q", got)
	}

	// 3. 切换至 NVIDIA / Other / Grok 负载均衡
	a.IPCSend("pool:toggle-nvidia", `[true]`)
	if !mgr.GetNvidiaPoolMode() || mgr.GetProjectPoolMode() {
		t.Fatalf("pool:toggle-nvidia true: want nvidia=true & project=false")
	}
	a.IPCSend("pool:toggle-other", `[true]`)
	if !mgr.GetOtherPoolMode() || mgr.GetNvidiaPoolMode() {
		t.Fatalf("pool:toggle-other true: want other=true & nvidia=false")
	}
	a.IPCSend("pool:toggle-grok", `[true]`)
	if !mgr.GetGrokPoolMode() || mgr.GetOtherPoolMode() {
		t.Fatalf("pool:toggle-grok true: want grok=true & other=false")
	}

	// 4. 切回 Antigravity 负载均衡并关闭再开启，校验持久化磁盘状态
	a.IPCSend("pool:toggle", `[false]`)
	a.IPCSend("pool:toggle", `[true]`)

	reloaded := account.NewManager()
	reloaded.Init(dir)
	if !reloaded.GetPoolMode() || reloaded.GetActiveChannel() != "antigravity" {
		t.Fatalf("persisted state after pool:toggle true: want poolMode=true channel=antigravity, got poolMode=%v channel=%q",
			reloaded.GetPoolMode(), reloaded.GetActiveChannel())
	}
}
