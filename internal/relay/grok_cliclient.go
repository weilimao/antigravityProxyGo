package relay

// grok_cliclient.go: 对齐 CLIProxyAPI2/internal/runtime/executor/xai_executor.go:1135-1147
// (applyXAIChatHeaders) 的 Grok CLI 身份头注入逻辑, 为本项目发往 cli-chat-proxy.grok.com
// 上游的 Grok 请求(对话 + 模型列表)带上官方 Grok CLI 身份头, 规避上游版本闸门:
//
//   426 Upgrade Required
//   {"error":"Your Grok CLI version (none) is outdated. Please update to version
//    0.1.202 or later via `grok update` or the installation documentation."}
//
// 上游 chat-proxy 通过 x-grok-client-version 头读 CLI 版本, 读不到即判 "(none)",
// 低于下限 0.1.202 直接回 426。本项目此前转发 Grok 上游只设 Content-Type/Authorization/Accept
// 三头(grok.go 对话与模型列表两处均为裸头), 故被拦。
//
// 注入的头(头名与取值语义严格对齐 CLIProxyAPI2 xai_executor.go:65-69):
//   - X-XAI-Token-Auth:      xai-grok-cli            (身份标识, 固定值)
//   - x-grok-client-version: <accountMgr.GetGrokCliVersion()>  (版本闸门判定字段)
//   - User-Agent:            xai-grok-workspace/<version>
//
// 与 CLIProxyAPI2 的关键差异(本项目需求):版本号不是硬编码常量, 而是号池全局可配配置项
// (accountMgr.GetGrokCliVersion, 默认 DefaultGrokCliVersion="1.0.0", 用户可在 Grok 号池
// 「负载均衡」区覆盖)。host 判定守卫保留 —— 仅当 BaseURL 命中 cli-chat-proxy.grok.com
// 才注入身份头, 不污染走 api.x.ai/v1 或自建反代的原生 API Key 路径。

import (
	"net/http"
	"strings"

	"antigravity-proxy/internal/account"
)

// grokChatProxyHost 是 Grok CLI chat-proxy 上游的 host(去 scheme 与 path 后的裸 host)。
// 上游版本闸门仅在此 host 生效; api.x.ai/v1 等官方 API 端点不经此闸门。
const grokChatProxyHost = "cli-chat-proxy.grok.com"

// grokIsCLIChatProxyBaseURL 判定 BaseURL 的 host 是否为 Grok CLI chat-proxy 端点。
// 裁剪 scheme 与 path, 容 https://cli-chat-proxy.grok.com/v1 与无 /v1 两种写法
// (BaseURL 在调用方已做 TrimRight("/"), 但保留 path 容忍以防御外部直传)。
func grokIsCLIChatProxyBaseURL(baseURL string) bool {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return false
	}
	// 去 scheme(http:// / https://)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// 去 path(首个 / 之后)
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return strings.EqualFold(s, grokChatProxyHost)
}

// applyGrokCLIHeaders 在上游请求注入 Grok CLI 身份头(仅当 BaseURL 命中 chat-proxy 时)。
//   - version 由号池全局配置 accountMgr.GetGrokCliVersion 传入, 已在 Get 层回退默认 DefaultGrokCliVersion;
//     此处再做一道兜底(TrimSpace 后空串 → 回退默认), 确保 x-grok-client-version 永不空
//     (上游 "(none)" 报错正是此头漏值导致, 这是本修复的红线)。
//   - 非 chat-proxy 的号(api.x.ai/v1 / 自建反代)不注入 —— 不污染原生 API Key 路径。
//   - Content-Type / Authorization / Accept 由调用方(grok.go 对话/模型列表)自行设置,
//     本函数只补身份头, 不重复设默认头。
func applyGrokCLIHeaders(r *http.Request, baseURL, version string) {
	if r == nil || !grokIsCLIChatProxyBaseURL(baseURL) {
		return
	}
	v := strings.TrimSpace(version)
	if v == "" {
		v = account.DefaultGrokCliVersion
	}
	r.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	r.Header.Set("x-grok-client-version", v)
	r.Header.Set("User-Agent", "xai-grok-workspace/"+v)
}
