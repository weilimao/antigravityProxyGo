package relay

// session_key.go: 全链路四号池统一会话键注入器与 sticky 选号键解析器。
//
// 历史背景:本仓库有四条号池选号链路(NVIDIA / Grok / Other 透传 / v1Internal),
//   每条在 sticky LB 模式下都要把「会话键」喂给 session.Router.GetOrAssignAccount 做
//   哈希粘性绑定；同时四条链路的入口都要给 userSession.SessionKey 注入一个隔离值,
//   供 OCR 缓存等「按会话隔离」特性共享同一口径。
//   此前 NVIDIA/Grok 把「注入块」各写了一份内联代码(优先 X-Claude-Code-Session-Id 头
//   → 回退 ExtractSessionKey + auth:acc:/sock:acc: 前缀),Other/v1Internal 完全没注入；
//   且 sticky 选号键四处都写死 userSession.UserID(按用户),从未消费已注入的 SessionKey,
//   导致同一用户的多个 Codex 会话全挤到一个上游账号。
//
// 本文件把两件事收敛为两个共享方法,首次让 Codex 的 Session-Id 头(经 ExtractClientSessionHeader)
// 进入全链路 sticky 键:
//   - ensureSessionKey: 幂等地给 userSession.SessionKey 注入隔离键(客户端头优先,ExtractSessionKey 兜底)。
//   - stickyKeyOf     : 解析 sticky 选号键(SessionKey 非空用之,空回退 UserID,保持旧行为零回归)。
//
// 不改 session.Router.ExtractSessionKey / ExtractClientSessionHeader 的契约,二者并列由本文件编排。

import (
	"net/http"
	"strings"
)

// ensureSessionKey 幂等地为 userSession 注入会话级隔离键(SessionKey 字段)。
//
//   注入规则(与 nvidia.go / grok.go 既有内联块逐行等价,并扩展到 Other/v1Internal 入口):
//   1. userSession==nil 或 SessionKey 已非空 → 直接返回(幂等:不覆盖调用方已显式注入的值)。
//   2. 优先 sessionRouter.ExtractClientSessionHeader:命中客户端原生会话头
//      (X-Claude-Code-Session-Id / Session-Id / Thread-Id)→ 直接赋 "claude:UUID" / "codex:UUID",
//      不再套 auth:acc:/sock:acc: 前缀(与 NVIDIA Claude 路径 "claude:<UUID>" 测试断言口径一致)。
//      Claude Code 走 X-Api-Key 不带 Bearer,原 ExtractSessionKey 会兜底 sock 分支使会话隔离失效;
//      Codex 走 Bearer 但其 Session-Id 头此前从未被读取——本步让两者都拿到细粒度会话键。
//   3. 客户端头未命中 → 回退 ExtractSessionKey(r, body) 并按其前缀套通道前缀:
//        "auth:xxx" → "auth:acc:xxx";  "sock:xxx" → "sock:acc:xxx";  其余非空 → "acc:xxx"。
//      适用于用 Authorization: Bearer 的脚本/SDK 直调(无客户端会话头)。
//   4. 两条都未命中 → SessionKey 保持空,OCR 缓存等回退按 UserKey 隔离,与旧行为一致。
//
// sessionRouter==nil(单测未注入)且无客户端头时跳过注入;OCR 缓存回退 UserKey 隔离。
func (h *APICompatHandler) ensureSessionKey(s *RelaySession, r *http.Request, body []byte) {
	if s == nil {
		return
	}
	if strings.TrimSpace(s.SessionKey) != "" {
		return // 幂等:已由调用方或上游入口显式注入
	}
	if h.sessionRouter != nil {
		if hdr := h.sessionRouter.ExtractClientSessionHeader(r); hdr != "" {
			s.SessionKey = hdr // claude:UUID / codex:UUID
			return
		}
		rawKey := h.sessionRouter.ExtractSessionKey(r, body)
		switch {
		case strings.HasPrefix(rawKey, "auth:"):
			s.SessionKey = "auth:acc:" + strings.TrimPrefix(rawKey, "auth:")
		case strings.HasPrefix(rawKey, "sock:"):
			s.SessionKey = "sock:acc:" + strings.TrimPrefix(rawKey, "sock:")
		case rawKey != "":
			s.SessionKey = "acc:" + rawKey
		}
	}
}

// stickyKeyOf 解析 sticky 选号键:SessionKey 非空即用之,否则回退 UserID。
//
//   - 旧行为:四处选号点写死 userSession.UserID(按用户粘性),同一用户的多个客户端会话
//     全挤到同一上游账号,sticky 失去会话粒度。
//   - 新行为:登录链路先经 ensureSessionKey 注入 SessionKey(客户端头优先 → 按会话粘性);
//     本方法把 sticky 键从 UserID 切到 SessionKey,使同一 Codex 用户的不同会话散到不同号;
//     非客户端头路径(脚本 SDK)SessionKey 为空时回退 UserID,保持旧行为零回归。
//   - userSession==nil 防御返回空串(下游 GetOrAssignAccount 对空键仍有兜底哈希)。
func (h *APICompatHandler) stickyKeyOf(s *RelaySession) string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.SessionKey) != "" {
		return s.SessionKey
	}
	return s.UserID
}
