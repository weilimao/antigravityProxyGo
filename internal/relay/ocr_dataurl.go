package relay

// ocr_dataurl.go —— Data URL(data:image/...;base64,...)解析。
//
// 三层分离架构的 L1 入口前置:Anthropic image block 的 source.data 按协议本就是纯 base64
// (不带 data: 前缀),不会有 Data URL 形态。但 OpenAI 风格 image_url.url 字段会以 data: 前缀
// 传入(见 compat_translate_translate.go:173 既有解析,本函数做防御性兜底/独立复用)。
// 把 Data URL 解析放 L1 入口,任一协议适配层(L2)遇到疑似 data: 前缀的字符串统一调本函数,
// 避免各 L2 各写一份正则。

import (
	"encoding/base64"
	"strings"
)

// parseDataURL 解析 data:image/<sub>;base64,<payload> 形态的 Data URL。
// 成功返回 (b64Std, mime, true):b64Std 是标准 base64(payload 无需再转);
// mime 形如 "image/png"。
// 非 Data URL / 缺 mime / 非 base64 编码 → ("", "", false)。
func parseDataURL(s string) (b64Std, mime string, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "data:") {
		return "", "", false
	}
	rest := s[len("data:"):]
	// 仅认 ;base64, 形态, 非 base64 的 data:url(如 data:image/svg+xml,<...>)不支持。
	semi := strings.Index(rest, ";")
	comma := strings.Index(rest, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return "", "", false
	}
	mime = rest[:semi]
	if !strings.HasPrefix(mime, "image/") {
		return "", "", false
	}
	enc := rest[semi+1 : comma]
	if enc != "base64" {
		return "", "", false
	}
	payload := rest[comma+1:]
	if payload == "" {
		return "", "", false
	}
	// 校验 payload 是合法 base64 并标准化为标准 padding 输出,避免后续 sha256 / OCR 入参奇异。
	dec, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", "", false
	}
	return base64.StdEncoding.EncodeToString(dec), mime, true
}

// looksLikeHTTPURL 判断 s 是否以 http:// 或 https:// 开头(大小写不敏感)。
// 供 L1 入口在 b64 / data URL / http URL 三态间快速分流,避免把 URL 当 base64 喂给 OCR。
func looksLikeHTTPURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(strings.ToLower(s), "http://") || strings.HasPrefix(strings.ToLower(s), "https://")
}
