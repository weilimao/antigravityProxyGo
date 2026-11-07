package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// antigravity_identity.go: Antigravity 官方协议身份指纹生成器。
// 负责生成符合官方客户端规范的十进制有符号 64 位整数 sessionId、标准 RFC 4122 UUIDv4 requestId，
// 并确保多账号池在并发分流或 429 故障转移切号时，各账号的会话标识与请求标识彻底物理隔离。

var defaultKeepAlivePrompts = []string{"ok", "hi", "yes", "hello", "ping"}

// AccountSessionID 根据账号唯一标识（acc.ID 或 acc.Email）与会话种子派生符合官方客户端格式的十进制负数 sessionId。
// 官方 Antigravity 客户端的 request.sessionId 格式为 64 位有符号负十进制整数字符串（如 "-3750763034362895579"）。
// 通过混入账号维度密钥，保证：
//  1. 同一账号在同一会话内保持恒定的 sessionId；
//  2. 不同账号即便承接同一个本地 IDE 客户端或发生 429 跨号重试，其上报给 Google 的 sessionId 也截然不同；
//  3. 严格只包含负号与十进制数字（0-9），彻底杜绝十六进制字母（a-f）导致的格式异常风控。
func AccountSessionID(accountKey, sessionSeed string) string {
	accPart := strings.TrimSpace(accountKey)
	if accPart == "" {
		accPart = "default-account"
	}
	seedPart := strings.TrimSpace(sessionSeed)
	if seedPart == "" {
		seedPart = "default-session"
	}

	sum := sha256.Sum256([]byte("antigravity-session-v1|" + accPart + "|" + seedPart))
	raw := binary.BigEndian.Uint64(sum[:8])

	// 限制在 [10^18, 9*10^18] 区间内，确保生成的负十进制整数始终为稳定的 19 位数字，与官方客户端遥测 ID 长度一致
	const minMag uint64 = 1000000000000000000 // 10^18
	const spanMag uint64 = 8000000000000000000
	mag := minMag + (raw % spanMag)
	negVal := -int64(mag)
	return strconv.FormatInt(negVal, 10)
}

// NewAntigravityUUIDv4 生成标准 RFC 4122 UUIDv4 字符串（小写 8-4-4-4-12 格式）。
func NewAntigravityUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		sum := sha256.Sum256([]byte(strconv.FormatInt(int64(binary.LittleEndian.Uint64(b[:8])), 10)))
		copy(b[:], sum[:16])
	}
	// RFC 4122 version 4 & variant 10xx
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewAntigravityRequestID 生成符合官方客户端正则规范的 requestId（形如 "agent/<uuidv4>" 或 "chat/<uuidv4>"）。
func NewAntigravityRequestID(prefix string) string {
	p := strings.Trim(strings.TrimSpace(prefix), "/")
	if p == "" {
		p = "agent"
	}
	return p + "/" + NewAntigravityUUIDv4()
}

// RandomKeepAlivePrompt 从轻量级自然词元池中随机挑选一个保活探测词，降低全号池定时唤醒的内容同质化特征。
func RandomKeepAlivePrompt() string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(defaultKeepAlivePrompts))))
	if err != nil {
		return "ok"
	}
	return defaultKeepAlivePrompts[n.Int64()]
}
