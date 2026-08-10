package relay

// passthrough_account.go: 透传号池选号与可用性判定(pickOtherAccount Other 号池组内
// sticky/round-robin 选号 + containsFormat 组 Formats 协议包含判定 +
// isPassthroughAccountUnavailable 透传候选账号可用性判定),从 passthrough_forwarder.go
// 按职责物理拆分,同 package relay 跨文件符号自动解析,逐字等价零回归。

import (
	"strings"
	"sync/atomic"

	"antigravity-proxy/internal/account"
)

// pickOtherAccount 是 Other 号池组内选号统一入口, 按组配置的 LB 模式选号。
// 与 pickNvidiaAccount 语义对齐, 但 sessionKey 用组前缀隔离:
//   - sticky 模式: 走 sessionRouter.GetOrAssignAccount, 用 "other:{groupID}:{stickyKey}"
//     作为粘性键, 使同一会话稳定绑定到同一账号, 且不同上游组互不串扰(避免跨组共享
//     sessionRouter 时把会话错绑到别的组账号)。{stickyKey} 由调用方入口经
//     h.ensureSessionKey 注入后取 h.stickyKeyOf(userSession):客户端会话头优先
//     (Codex Session-Id → codex:UUID), 空 → UserID(按用户, 旧行为零回归)。
//   - round-robin 模式: 取活跃集第一个(维持既有轻量取法, 与 handleNvidia 单账号语义一致)。
//
// sessionRouter / userSession 缺失时回退 round-robin(取首个), 保证单测与未注入场景兼容。
func (pf *passthroughForward) pickOtherAccount(h *APICompatHandler, lbMode, groupID string, userSession *RelaySession, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}
	if lbMode == "sticky" && h != nil && h.sessionRouter != nil && userSession != nil && userSession.UserID != "" {
		stickyKey := "other:" + groupID + ":" + h.stickyKeyOf(userSession)
		return h.sessionRouter.GetOrAssignAccount(stickyKey, accounts, h.logFn)
	}

	// 真正的轮询 (Round-Robin): 按组隔离的取模偏移
	var cursor uint64
	if h != nil {
		var ptr *uint64
		if val, ok := h.otherCursors.Load(groupID); ok {
			ptr = val.(*uint64)
		} else {
			newPtr := new(uint64)
			actual, _ := h.otherCursors.LoadOrStore(groupID, newPtr)
			ptr = actual.(*uint64)
		}
		cursor = atomic.AddUint64(ptr, 1) - 1
	}
	idx := cursor % uint64(len(accounts))
	return accounts[idx]
}

// containsFormat 判定组 Formats 切片是否包含某协议(大小写不敏感)。
func containsFormat(formats []string, want string) bool {
	w := strings.ToLower(strings.TrimSpace(want))
	for _, f := range formats {
		if strings.ToLower(strings.TrimSpace(f)) == w {
			return true
		}
	}
	return false
}

// isPassthroughAccountUnavailable 判定透传候选账号是否「不可用」(跳过换号)。
// 与 handleNvidia 的 IsNvidiaAvailable 口径一致,但通用化:不限制 Provider,
// 只要 Provider 匹配且启用、有 AccessToken 且配了 BaseURL 即可用。
func isPassthroughAccountUnavailable(a *account.Account) bool {
	return a == nil || !a.Enabled || a.GetAccessToken() == "" || strings.TrimSpace(a.BaseURL) == ""
}
