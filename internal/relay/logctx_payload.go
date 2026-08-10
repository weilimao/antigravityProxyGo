package relay

// logctx_payload.go: 号池三条直连链路(NVIDIA / Other / Antigravity 直连)入站请求头与请求体
// 的统一采集与脱敏,供 record*Usage 在构造 stats.RequestLog 时落地 RequestBody / RequestHeaders,
// 使前端「请求参数详情」弹窗(minus 轻量投影后的按需 GetRequestDetails 拉取)能如实展示入站
// 请求头与请求体,而非恒落入 "无请求头数据 / 无请求参数" 兜底文案。
//
// 设计要点(与 internal/proxy/helpers.go:187-217 与 handler_remote.go:268-280 同口径):
//   - 请求体:空 → nil(前端 formatRequestBody 落 "无请求参数" 兜底);非空先 JSON 解析成结构化
//     对象以便前端折叠展示,解析失败回退原始字符串;后续由 stats.TruncateRequestBody 统一截断
//     超长字段防 OOM(与 AddRequestLog / AddRequestLogInMemoryOnly 同道)。
//   - 请求头:取每个头的首值(与 proxy 既有实现一致);命中 sensitiveInboundLogHeaders 的头
//     值以 "<redacted>" 占位,避免把用户凭证(Bearer / x-api-key / 账号 API Key)写进仪表盘
//     与落库 SQLite,造成明文凭证泄露。其余头原样保留便于排查(如 X-Claude-Code-Session-Id /
//     Content-Type / User-Agent / Anthropic-Version / Anthropic-Beta 等)。
//
// 本文件仅引入 stdlib,无运行时状态,被 nvidia_response.go / router_entry.go /
// compat_v1internal.go 三个 logCtx 装配点共享。

import (
	"encoding/json"
	"net/http"
	"strings"
)

// sensitiveInboundLogHeader 判定单个入站请求头是否含敏感凭证,命中者在落库时刻脱敏。
// 大小写不敏感(与 http.Header 规范化后的 canonical key 比对)。
// 覆盖率:Authorization(Bearer / Basic 各类鉴权)、X-Api-Key(Anthropic 风格)、
// X-Goog-Api-Key(Google 风格)、Cookie(Session/凭证)、Proxy-Authorization(代理鉴权)。
// 不含 AuthProxyTokenKey 等内部头(renlay 内部注入,本代理自身可信,且非客户端原意携带)。
func sensitiveInboundLogHeader(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "authorization", "x-api-key", "x-goog-api-key", "cookie", "proxy-authorization":
		return true
	}
	return false
}

// redactedHeaderValue 是敏感头在落库时的占位值,与明文显式区分,提示读者该头存在但被脱敏。
const redactedHeaderValue = "<redacted>"

// parseInboundBodyForLog 把入站请求体字节解析为可落库展示的结构化值。
//   - 空 body → nil(触发前端 "无请求参数" 兜底文案,与现状等价);
//   - 合法 JSON → json.Unmarshal 后的 interface{}(map/slice/标量),前端可折叠展示;
//   - 非法 JSON → 原始字符串(如 multipart / 纯文本),便于至少看到内容形态。
// 后续超长字段由 stats.TruncateRequestBody 统一截断,此处只负责"能否结构化"决策。
func parseInboundBodyForLog(bodyBytes []byte) interface{} {
	if len(bodyBytes) == 0 {
		return nil
	}
	var parsed interface{}
	if err := json.Unmarshal(bodyBytes, &parsed); err == nil {
		return parsed
	}
	return string(bodyBytes)
}

// collectInboundHeadersForLog 把入站请求头采集为 {键: 值} 映射供落库展示(返回 interface{}:
// 空 → nil 真空接口, 非空 → 装箱后的 map, 与 parseInboundBodyForLog 口径对称, 使落库
// interface{} 字段为空时 marshals 到 null, 前端 formatRequestHeaders 走「无请求头数据」兜底)。
// 每个头取首值(与 internal/proxy/helpers.go:212-217 / handler_remote.go:275-280 同口径),
// 命中 sensitiveInboundLogHeader 的键值以 "<redacted>" 占位,杜绝把客户端凭证写进仪表盘与 SQLite。
func collectInboundHeadersForLog(header http.Header) interface{} {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(header))
	for k, v := range header {
		if len(v) == 0 {
			continue
		}
		if sensitiveInboundLogHeader(k) {
			out[k] = redactedHeaderValue
			continue
		}
		out[k] = v[0]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
