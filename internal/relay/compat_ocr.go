package relay

// compat_ocr.go: OCR 相关的包级共享辅助函数。
//
// 历史背景:本文件原承载 OCR 引擎全部实现(getOcrModel / ocrImageViaLocalGemini /
// ocrImageCacheOnlyLookup / ocrImageViaLocalGeminiUncached / ocrOwnerKey / ocrSessionDisplay)。
// 三层抽离重构后,引擎实现已搬入 ocr_engine.go(receiver 改为 *OCRService)。
// 本文件仅保留两个仍被号池入口层(nvidia.go / nvidia_response.go)与引擎层(ocr_engine.go)
// 跨文件共享的包级辅助函数,无运行时状态。

import (
	"strings"
)

// ocrOwnerKey 返回 OCR 缓存键的首维隔离键:会话级 sessionKey 优先,空则回退 UserKey。
// 抽出便于 OCRService.OcrImage / OcrImageCacheOnlyLookup 共享一致的取键口径。
// 设计:sessionKey 非空 → 按会话隔离(同用户多会话不共享缓存,语义更准且与日志会话 ID 对齐);
//      sessionKey 空 → 回退 UserKey(单测与未传 sessionKey 的旧调用,行为不变)。
func ocrOwnerKey(userSession *RelaySession) string {
	if userSession == nil {
		return ""
	}
	if strings.TrimSpace(userSession.SessionKey) != "" {
		return userSession.SessionKey
	}
	return userSession.UserKey
}

// ocrSessionDisplay 返回日志里展示的会话 ID:优先 userSession.SessionKey(由 handleNvidia 入口
// 经 ExtractSessionKey + auth:acc: 前缀算出,与 antigravity 号池链路同款口径),空则回退 UserID,
// 再空则 "-"。供 NVIDIA 选号/降级日志透出"哪个会话在打",便于排查同用户多会话的缓存隔离与配额归属。
//
// 注意:本函数虽以 ocr 前缀命名(历史沿袭),实为号池链路共享的会话展示工具,
// 被 nvidia.go(选号/降级日志)、nvidia_response.go(recordNvidiaUsage 的 logCtx.SessionID)、
// 以及 OCR 降级链路共同引用,故留在本包级辅助文件而非 OCRService 方法。
func ocrSessionDisplay(userSession *RelaySession) string {
	if userSession == nil {
		return "-"
	}
	if k := strings.TrimSpace(userSession.SessionKey); k != "" {
		return k
	}
	if u := strings.TrimSpace(userSession.UserID); u != "" {
		return u
	}
	return "-"
}
