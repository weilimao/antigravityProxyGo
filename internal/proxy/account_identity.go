package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"net/http"
	"strings"
	"time"
)

// account_identity.go: 出站账号身份隔离、Project 归属对齐与冲突请求头清洗。
// 确保在“客户端单号登录（或免登）+ 后台多号池新会话分流/429 无感切号”架构下，
// 发往 Google 上游的每一笔请求都具备自洽的单账号专属 TLS 连接、专属 Project ID 与纯净请求头。

// isMockOrSharedDefaultProject 判断项目 ID 是否为本地 Mock 占位项目或共享兜底项目。
// 此类占位项目严禁被反向捕获并覆盖绑定到真实号池账号名下。
func isMockOrSharedDefaultProject(proj string) bool {
	p := strings.TrimSpace(proj)
	if p == "" {
		return true
	}
	if p == "favorable-synapse-ttvcb" || p == "expanded-palisade-stpfc" {
		return true
	}
	if strings.HasPrefix(p, "expanded-palisade-") {
		return true
	}
	return false
}

// resolvePoolAccountProject 解析 v1internal 个人/官方号池通道下当前出站账号应当使用的真实 Project ID。
// 优先级：
//  1. 当前被分配账号（poolAccount）自身配置的非占位 ProjectID；
//  2. QuotaService 通过 loadCodeAssist 为该账号（poolAccount.Email）独立探测并持久化的专属 ProjectID；
//  3. QuotaService 全局 default 存储项（非占位真实项目）；
//  4. 仅在未开启多号池负载均衡（单账号直通模式）下，允许参考客户端请求体传入的非占位 ProjectID；
//     多号池分流模式下严禁跨账号借用客户端主号私有 Project ID，防止因 IAM 权限不匹配报 403 PERMISSION_DENIED；
//  5. 账号配置的兜底 ProjectID / 官方默认兜底值 "favorable-synapse-ttvcb"。
func (sc *serveContext) resolvePoolAccountProject(poolAccount *account.Account, clientBodyProject string) string {
	if poolAccount != nil {
		if p := strings.TrimSpace(poolAccount.ProjectID); !isMockOrSharedDefaultProject(p) {
			return p
		}
		if sc.h != nil && sc.h.getStoredProject != nil && poolAccount.Email != "" {
			if stored := strings.TrimSpace(sc.h.getStoredProject(poolAccount.Email)); !isMockOrSharedDefaultProject(stored) {
				return stored
			}
		}
	}

	if sc.h != nil && sc.h.getStoredProject != nil {
		if defProj := strings.TrimSpace(sc.h.getStoredProject("default")); !isMockOrSharedDefaultProject(defProj) {
			return defProj
		}
	}

	// 仅在单账号直通模式（未开启多号池分流）下，允许参考客户端请求体传入的非占位项目
	poolEnabled := false
	if sc.h != nil && sc.h.accountMgr != nil {
		poolEnabled = sc.h.accountMgr.GetPoolMode() || sc.h.accountMgr.GetProjectPoolMode()
	}
	if !poolEnabled {
		if cp := strings.TrimSpace(clientBodyProject); !isMockOrSharedDefaultProject(cp) {
			return cp
		}
	}

	if poolAccount != nil {
		if p := strings.TrimSpace(poolAccount.ProjectID); p != "" {
			return p
		}
		if sc.h != nil && sc.h.getStoredProject != nil && poolAccount.Email != "" {
			if stored := strings.TrimSpace(sc.h.getStoredProject(poolAccount.Email)); stored != "" {
				return stored
			}
		}
	}

	if sc.h != nil && sc.h.getStoredProject != nil {
		if defProj := strings.TrimSpace(sc.h.getStoredProject("default")); defProj != "" {
			return defProj
		}
	}

	if cp := strings.TrimSpace(clientBodyProject); cp != "" && isMockOrSharedDefaultProject(cp) {
		return cp
	}

	return "favorable-synapse-ttvcb"
}

// sanitizeConflictingClientHeaders 剥离与底层 Go crypto/tls 握手特征或 Antigravity 官方客户端身份相矛盾的请求头：
//  1. Chromium / 浏览器专属客户端提示头（Sec-Ch-Ua*、Sec-Fetch-*、X-Client-Data）；
//  2. 第三方 AI SDK 特征头（X-Stainless-*、Anthropic-*、OpenAI-*、X-Claude-*、X-Codex-*）；
//  3. 客户端会话路由与反向代理转发痕迹头（X-Session-Id、X-Session-Affinity、Session-Id、Thread-Id、Turn-Id、X-Forwarded-*、Forwarded、Via、X-Real-Ip、Cf-*）。
func sanitizeConflictingClientHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	exactHeaders := []string{
		"Sec-Ch-Ua",
		"Sec-Ch-Ua-Mobile",
		"Sec-Ch-Ua-Platform",
		"Sec-Ch-Ua-Full-Version",
		"Sec-Ch-Ua-Full-Version-List",
		"Sec-Ch-Ua-Arch",
		"Sec-Ch-Ua-Bitness",
		"Sec-Ch-Ua-Model",
		"Sec-Fetch-Site",
		"Sec-Fetch-Mode",
		"Sec-Fetch-Dest",
		"Sec-Fetch-User",
		"X-Client-Data",
		"X-Session-Id",
		"X-Session-Affinity",
		"Session-Id",
		"Thread-Id",
		"Turn-Id",
		"Via",
		"Forwarded",
		"X-Real-Ip",
	}
	for _, h := range exactHeaders {
		headers.Del(h)
		headers.Del(strings.ToLower(h))
	}

	conflictingPrefixes := []string{
		"sec-ch-ua-",
		"sec-fetch-",
		"x-stainless-",
		"anthropic-",
		"openai-",
		"x-claude-",
		"x-codex-",
		"x-forwarded-",
		"cf-",
	}
	for k := range headers {
		lowerKey := strings.ToLower(strings.TrimSpace(k))
		for _, prefix := range conflictingPrefixes {
			if strings.HasPrefix(lowerKey, prefix) {
				delete(headers, k)
				break
			}
		}
	}
}

// clientForAttempt 获取本次出站请求对应的 HTTP 客户端。
// - 若 h.client 被注入了单元测试自定义 RoundTripper（非标准 *http.Transport），则优先使用 h.client 保持测试兼容；
// - 若本次请求分配了号池账号（ro.poolAccount），则返回该账号独占的 HTTP/2 连接池与 TLS Session 缓存客户端；
// - 若未分配号池账号但携带了客户端 Bearer Token（如桌面端直通 bootstrap 请求），按 Token 哈希分配独立连接池，防止与全局共享连接池串流；
// - 其余场景回退至 h.client。
func (h *ProxyHandler) clientForAttempt(ro *routeOutcome) *http.Client {
	if h.client != nil && h.client.Transport != nil {
		if _, isStdTransport := h.client.Transport.(*http.Transport); !isStdTransport {
			return h.client
		}
	}
	timeout := 10 * time.Minute
	if h.client != nil && h.client.Timeout > 0 {
		timeout = h.client.Timeout
	}
	if ro != nil && ro.poolAccount != nil {
		accountKey := strings.TrimSpace(ro.poolAccount.ID)
		if accountKey == "" {
			accountKey = strings.TrimSpace(ro.poolAccount.Email)
		}
		if accountKey != "" {
			return netutil.GetAccountClient(accountKey, timeout)
		}
	}
	if ro != nil && ro.customHeaders != nil {
		if auth := strings.TrimSpace(ro.customHeaders.Get("Authorization")); auth != "" {
			return netutil.GetAccountClient(auth, timeout)
		}
	}
	if h.client != nil {
		return h.client
	}
	return netutil.NewClient(timeout)
}
