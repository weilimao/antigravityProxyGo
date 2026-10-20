package relay

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"antigravity-proxy/internal/account"
)

// workbuddy_ids.go: 生成发往 WorkBuddy 上游的会话标识。
//
// 背景(400 / code 11128 Illegal API invocation from an unapproved channel):
// 上游 /v2/chat/completions 对 X-Conversation-ID 有严格的 UUID 格式要求,官方客户端
// 恒定发送标准 UUID。历史实现直接把内部会话键塞进该头,而内部会话键带客户端来源前缀
// (形如 "claude:<uuid>"、"opencode:<uuid>"、"acc:<id>"、"auth:acc:<id>"),导致:
//  1. 格式非法,上游会话/渠道绑定校验直接判失败;
//  2. "claude:" 这类前缀本身就是"非官方客户端"的最强指纹,明文暴露给上游风控。
//
// 这里统一把任意会话键规整为 UUID:已是裸 UUID 的原样复用,否则用 SHA-256 派生确定性 UUID,
// 保证同一会话始终映射到同一个上游会话 ID(会话粘性语义不变)。

// workbuddyUUIDPattern 匹配标准 8-4-4-4-12 形式的 UUID。
var workbuddyUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// formatWorkBuddyUUID 把 16 字节按 8-4-4-4-12 格式化为 UUID 字符串。
func formatWorkBuddyUUID(b [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// workbuddyConversationID 把内部会话键规整为上游要求的 UUID 形式。
//   - 空键:随机生成(与官方客户端首次建会话行为一致)。
//   - 裸 UUID:小写复用。
//   - 其它(含内部前缀):SHA-256 派生确定性 UUID v4 形态,前缀不会外泄。
func workbuddyConversationID(sessionKey string) string {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return account.GenerateWorkBuddyUUID()
	}
	if workbuddyUUIDPattern.MatchString(key) {
		return strings.ToLower(key)
	}

	sum := sha256.Sum256([]byte(key))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return formatWorkBuddyUUID(b)
}
