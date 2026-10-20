package relay

import (
	"strings"
	"testing"
)

// workbuddy_ids_test.go: 锁定 X-Conversation-ID 必须为合法 UUID。
//
// 回归背景:历史上该头直接塞内部会话键(如 "claude:<uuid>" / "acc:<id>"),
// 上游渠道校验判非法,返回 400 / code 11128 Illegal API invocation from an unapproved channel。

func TestWorkBuddyConversationID_NormalizesInternalSessionKey(t *testing.T) {
	cases := []string{
		"claude:3f2a6c1e-9b7d-4c2f-8a51-0e6d9b4c7a12",
		"codex:9b7d4c2f",
		"opencode:abc123",
		"zcode:abc123",
		"acc:user-123",
		"auth:acc:user-123",
		"sock:acc:user-123",
	}

	for _, in := range cases {
		got := workbuddyConversationID(in)
		if !workbuddyUUIDPattern.MatchString(got) {
			t.Errorf("workbuddyConversationID(%q) = %q, 期望合法 UUID", in, got)
		}
		if strings.Contains(got, ":") {
			t.Errorf("workbuddyConversationID(%q) 仍含内部前缀: %q", in, got)
		}
		if strings.Contains(strings.ToLower(got), "claude") {
			t.Errorf("workbuddyConversationID(%q) 泄露客户端标识: %q", in, got)
		}
	}
}

func TestWorkBuddyConversationID_Deterministic(t *testing.T) {
	const key = "claude:3f2a6c1e-9b7d-4c2f-8a51-0e6d9b4c7a12"

	first := workbuddyConversationID(key)
	second := workbuddyConversationID(key)
	if first != second {
		t.Errorf("同一会话键应映射到同一 UUID,得到 %q 与 %q", first, second)
	}

	other := workbuddyConversationID("claude:00000000-0000-4000-8000-000000000000")
	if other == first {
		t.Errorf("不同会话键不应映射到同一 UUID: %q", first)
	}
}

func TestWorkBuddyConversationID_PassthroughBareUUID(t *testing.T) {
	const uuid = "0F8FAD5B-D9CB-469F-A165-70867728950E"

	got := workbuddyConversationID(uuid)
	if got != strings.ToLower(uuid) {
		t.Errorf("裸 UUID 应小写复用,得到 %q", got)
	}
}

func TestWorkBuddyConversationID_EmptyGeneratesRandomUUID(t *testing.T) {
	first := workbuddyConversationID("")
	if !workbuddyUUIDPattern.MatchString(first) {
		t.Fatalf("空会话键应生成合法 UUID,得到 %q", first)
	}
	second := workbuddyConversationID("   ")
	if first == second {
		t.Errorf("空会话键每次应生成不同 UUID,两次均为 %q", first)
	}
}
