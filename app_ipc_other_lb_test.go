package main

import (
	"testing"

	"antigravity-proxy/internal/account"
)

// TestIPCSend_OtherSetLbMode 验证 IPCSend 收到前端 send 的 other:set-lb-mode 后,
// 会真正调用 SetOtherLBMode 并持久化,而非静默丢弃。
// 回归依据:此前 IPCSend 缺 other:set-lb-mode 分支,前端 ipcRenderer.send 走 send 通道
// 被静默吞掉,导致 Other 粘性负载均衡从未落盘、切回号池回退轮询。
func TestIPCSend_OtherSetLbMode(t *testing.T) {
	a := &App{accountMgr: account.NewManager()}
	dir := t.TempDir()
	a.accountMgr.Init(dir)
	// 准备一个 Other 组
	_, err := a.accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID: "aliyun", GroupName: "阿里云", BaseURL: "https://api.example.com/v1",
		APIKey: "k1", Formats: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// 模拟前端 ipcRenderer.send('other:set-lb-mode', ['aliyun', 'sticky'])
	a.IPCSend("other:set-lb-mode", `["aliyun","sticky"]`)
	// 断言:SetOtherLBMode 真正被调用
	if got := a.accountMgr.GetOtherLBMode("aliyun"); got != "sticky" {
		t.Fatalf("IPCSend other:set-lb-mode: want sticky, got %q", got)
	}
	// 断言:GetOtherGroups 回显 sticky(前端切回号池的数据源)
	groups := a.accountMgr.GetOtherGroups()
	if len(groups) != 1 || groups[0].LbMode != "sticky" {
		t.Fatalf("GetOtherGroups echo: want 1 group sticky, got %+v", groups)
	}
}