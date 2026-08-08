package main

import (
	"testing"

	"antigravity-proxy/internal/account"
)

// app_ipc_max_concurrency_test.go 锁定 IPCSend 收到前端 set-max-concurrency 后,
// 真正调用对应 Set*MaxConcurrency 并持久化(落 accounts.json),而非静默丢弃。
// 对齐既有 app_ipc_other_lb_test.go 的 TestIPCSend_OtherSetLbMode 范式,覆盖四条新通道。

// TestIPCSend_NvidiaSetMaxConcurrency 验证 nvidia:set-max-concurrency 落盘 + Get 回读一致。
func TestIPCSend_NvidiaSetMaxConcurrency(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)

	// 模拟前端 ipcRenderer.send('nvidia:set-max-concurrency', [7])
	a.IPCSend("nvidia:set-max-concurrency", `[7]`)
	if got := a.accountMgr.GetNvidiaMaxConcurrency(); got != 7 {
		t.Fatalf("nvidia:set-max-concurrency: want 7 persisted, got %d", got)
	}

	// 0 = 未配置 → Get 回退默认 10。
	a.IPCSend("nvidia:set-max-concurrency", `[0]`)
	if got := a.accountMgr.GetNvidiaMaxConcurrency(); got != 10 {
		t.Fatalf("nvidia:set-max-concurrency(0): want fallback 10, got %d", got)
	}
}

// TestIPCSend_AntigravitySetMaxConcurrency 验证 antigravity:set-max-concurrency 落盘。
func TestIPCSend_AntigravitySetMaxConcurrency(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)

	a.IPCSend("antigravity:set-max-concurrency", `[15]`)
	if got := a.accountMgr.GetAntigravityMaxConcurrency(); got != 15 {
		t.Fatalf("antigravity:set-max-concurrency: want 15 persisted, got %d", got)
	}
}

// TestIPCSend_ProjectSetMaxConcurrency 验证 project:set-max-concurrency 落盘。
func TestIPCSend_ProjectSetMaxConcurrency(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)

	a.IPCSend("project:set-max-concurrency", `[20]`)
	if got := a.accountMgr.GetProjectMaxConcurrency(); got != 20 {
		t.Fatalf("project:set-max-concurrency: want 20 persisted, got %d", got)
	}
}

// TestIPCSend_OtherSetMaxConcurrency 验证 other:set-max-concurrency(groupID,value) 落盘 +
// GetOtherGroups 回显该组并发上限(前端切回 Tab 的数据源)。负数被规整为 0 → 回退默认 10。
func TestIPCSend_OtherSetMaxConcurrency(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)

	// 准备一个 Other 组(首号建组)。
	_, err := a.accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID: "aliyun", GroupName: "阿里云", BaseURL: "https://api.example.com/v1",
		APIKey: "k1", Formats: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("add other account: %v", err)
	}

	// 模拟前端 ipcRenderer.send('other:set-max-concurrency', ['aliyun', 5])
	a.IPCSend("other:set-max-concurrency", `["aliyun",5]`)
	if got := a.accountMgr.GetOtherMaxConcurrency("aliyun"); got != 5 {
		t.Fatalf("other:set-max-concurrency: want 5 persisted, got %d", got)
	}
	// GetOtherGroups 回显该组并发上限(前端切回号池时回填 input 的数据源)。
	groups := a.accountMgr.GetOtherGroups()
	if len(groups) != 1 || groups[0].MaxConcurrency != 5 {
		t.Fatalf("GetOtherGroups echo MaxConcurrency: want 1 group with 5, got %+v", groups)
	}

	// 负数被规整为 0 → Get 回退默认 10,回显字段亦统一对齐为 10(与 NVIDIA/Antigravity/Project 一致)。
	a.IPCSend("other:set-max-concurrency", `["aliyun",-3]`)
	if got := a.accountMgr.GetOtherMaxConcurrency("aliyun"); got != 10 {
		t.Fatalf("other:set-max-concurrency(-3): want fallback 10, got %d", got)
	}
	for _, g := range a.accountMgr.GetOtherGroups() {
		if g.GroupID == "aliyun" && g.MaxConcurrency != 10 {
			t.Fatalf("GetOtherGroups echo after Set(-3): want fallback 10, got %d", g.MaxConcurrency)
		}
	}
}

// TestIPCSend_OtherSetMaxConcurrency_GroupIDNormalization 验证 Other 组 groupID 走小写键规范化:
// 前端传 "OpenAI"(混合大小写),GetOtherMaxConcurrency("openai") 应能读到同值。
func TestIPCSend_OtherSetMaxConcurrency_GroupIDNormalization(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)

	_, err := a.accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID: "openai", GroupName: "OpenAI 上游", BaseURL: "https://api.openai.com/v1",
		APIKey: "sk-test", Formats: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("add other account: %v", err)
	}

	// 前端传大写 GroupID,后端按小写规范化落盘。
	a.IPCSend("other:set-max-concurrency", `["OpenAI",8]`)
	if got := a.accountMgr.GetOtherMaxConcurrency("openai"); got != 8 {
		t.Fatalf("groupID normalization: GetOtherMaxConcurrency(openai) = %d, want 8", got)
	}
}
