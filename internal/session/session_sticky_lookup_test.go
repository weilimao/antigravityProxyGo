package session

import (
	"testing"

	"antigravity-proxy/internal/account"
)

func TestFindStickyAccount_SiblingAndDirectSession(t *testing.T) {
	router := NewRouter()
	accHigh := &account.Account{ID: "acc-high", Email: "high@example.com"}
	accLow := &account.Account{ID: "acc-low", Email: "low@example.com"}
	all := []*account.Account{accHigh, accLow}

	// 1. 尚无任何绑定 -> 返回 nil
	if got := router.FindStickyAccount("auth:acc:4dd23a15deadbeef", all); got != nil {
		t.Fatalf("expected nil before any binding, got %s", got.ID)
	}

	// 2. 绑定主任务子会话 auth:acc:4dd23a15deadbeef:conv1 到 acc-high
	router.GetOrAssignAccount("auth:acc:4dd23a15deadbeef:conv1", []*account.Account{accHigh}, nil)

	// 2a. 伴生请求（subSuffix==""）应命中同客户端活跃兄弟账号 acc-high
	if got := router.FindStickyAccount("auth:acc:4dd23a15deadbeef", all); got == nil || got.ID != "acc-high" {
		t.Fatalf("companion request expected acc-high via sibling affinity, got %+v", got)
	}

	// 2b. 直接以相同子会话请求也应命中 acc-high
	if got := router.FindStickyAccount("auth:acc:4dd23a15deadbeef:conv1", all); got == nil || got.ID != "acc-high" {
		t.Fatalf("direct session request expected acc-high, got %+v", got)
	}

	// 2c. 独立的新子会话 conv2 尚未绑定 -> 返回 nil（允许触发低阶优先收敛）
	if got := router.FindStickyAccount("auth:acc:4dd23a15deadbeef:conv2", all); got != nil {
		t.Fatalf("unrelated child session conv2 expected nil, got %s", got.ID)
	}

	// 3. 若 acc-high 不在候选集中（例如已冷却或超并发）-> 返回 nil
	if got := router.FindStickyAccount("auth:acc:4dd23a15deadbeef", []*account.Account{accLow}); got != nil {
		t.Fatalf("expected nil when bound account is absent from candidates, got %s", got.ID)
	}
}
