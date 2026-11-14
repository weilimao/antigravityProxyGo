package netutil

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// account_client.go: 按账号物理隔离 HTTP/2 连接池与 TLS Session Ticket 缓存。
// 防止多账号轮询、新会话分流或 429 切号重试时，不同 Google 账号的 OAuth Bearer Token
// 复用同一条底层 HTTP/2 TCP/TLS 连接或同一个 TLS Resumption Session Ticket 而触发 GFE 关联风控。

var (
	accountTransportsMu sync.RWMutex
	accountTransports   = make(map[string]*http.Transport)
)

var knownModuleTransportPrefixes = []string{
	"relay-stream:",
	"relay:",
	"modelfetch:",
	"trigger:",
	"aigen:",
	"packet:",
	"quota:",
}

// NormalizeAccountTransportKey 归一化各模块传入的账号连接池 Key。
// 剥离 "relay:" / "modelfetch:" / "trigger:" / "quota:" 等模块级前缀，
// 使同一账号在对话、配额查询、模型探测、保活唤醒及 Token 刷新时复用同一条 HTTP/2 连接与 TLS Session 缓存，
// 避免单账号同时向 Google 打开多条平行 TCP+TLS 连接的异常指纹，并根除按动态 Token 建池导致的句柄泄漏。
func NormalizeAccountTransportKey(accountKey string) string {
	key := strings.TrimSpace(accountKey)
	if key == "" {
		return ""
	}
	for _, prefix := range knownModuleTransportPrefixes {
		if strings.HasPrefix(strings.ToLower(key), prefix) {
			key = strings.TrimSpace(key[len(prefix):])
			break
		}
	}
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		rawTok := strings.TrimSpace(key[7:])
		if rawTok == "" {
			return ""
		}
		sum := sha256.Sum256([]byte(rawTok))
		return "tok-" + hex.EncodeToString(sum[:8])
	}
	return key
}

// NewIsolatedTransport 创建严格对齐官方 Antigravity (language_server.exe) CreateCloudCodeHTTPClient 指纹的独立 http.Transport 实例。
// 逆向结论（codeassistclient.CreateCloudCodeHTTPClient @ 0x20f6c60）：
//  1. 官方客户端构造 &http.Transport{Proxy: ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: ...}} 时未设置 ForceAttemptHTTP2，
//     在 Go 标准库 net/http.(*Transport).protocols 中因 TLSClientConfig != nil 默认仅启用 HTTP/1.1，且 onceSetNextProtoDefaults 直接早返不注入 "h2"，
//     因此官方 TLS ClientHello 不包含 ALPN 扩展（0x0010）；
//  2. 官方客户端 TLSClientConfig.ClientSessionCache 为 nil，在 crypto/tls.(*Conn).loadSession 中不设置 ticketSupported 与 pskModes，
//     因此官方 TLS ClientHello 不包含 session_ticket（0x0023）、psk_key_exchange_modes（0x002d）及 pre_shared_key（0x0029）扩展；
//  3. 每个账号拥有物理隔离的 *http.Transport 实例与独立的 HTTP/1.1 Keep-Alive 连接池，绝不跨账号复用 TCP/TLS 连接。
func NewIsolatedTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 GetSystemProxy,
		DialContext:           DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ClientSessionCache: nil,
		},
	}
}

// GetAccountTransport 获取或创建指定账号键（account.ID 或 account.Email）专属的 *http.Transport。
// 若 accountKey 为空，则回退至全局共享 Transport。
func GetAccountTransport(accountKey string) *http.Transport {
	key := NormalizeAccountTransportKey(accountKey)
	if key == "" {
		return getSharedTransport()
	}

	accountTransportsMu.RLock()
	tr, exists := accountTransports[key]
	accountTransportsMu.RUnlock()
	if exists && tr != nil {
		return tr
	}

	accountTransportsMu.Lock()
	defer accountTransportsMu.Unlock()
	if tr, exists = accountTransports[key]; exists && tr != nil {
		return tr
	}
	// 防御性淘汰：若因临时 Token 键导致连接池数量超过阈值，优先回收 tok- 临时键的空闲连接
	if len(accountTransports) >= 256 {
		for k, oldTr := range accountTransports {
			if strings.HasPrefix(k, "tok-") {
				delete(accountTransports, k)
				if oldTr != nil {
					oldTr.CloseIdleConnections()
				}
			}
		}
	}
	tr = NewIsolatedTransport()
	accountTransports[key] = tr
	return tr
}

// GetAccountClient 返回绑定了指定账号专属连接池与独立 TLS Session 缓存的 *http.Client。
func GetAccountClient(accountKey string, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: GetAccountTransport(accountKey),
		Timeout:   timeout,
	}
}

// CloseAccountIdleConnections 仅关闭指定账号专属连接池中的空闲连接（保留 Transport 实例及 TLS 配置）。
// 适用于遇到传输层 EOF / connection reset 时驱逐可能残留的半关闭 TCP 连接，避免下次重试复用坏连接。
func CloseAccountIdleConnections(accountKey string) {
	key := NormalizeAccountTransportKey(accountKey)
	if key == "" {
		if sharedTransport != nil {
			sharedTransport.CloseIdleConnections()
		}
		return
	}
	accountTransportsMu.RLock()
	tr, exists := accountTransports[key]
	accountTransportsMu.RUnlock()
	if exists && tr != nil {
		tr.CloseIdleConnections()
	}
}

// CloseAccountTransport 关闭并移除指定账号的专属空闲连接池。
func CloseAccountTransport(accountKey string) {
	key := NormalizeAccountTransportKey(accountKey)
	if key == "" {
		return
	}
	accountTransportsMu.Lock()
	tr, exists := accountTransports[key]
	if exists {
		delete(accountTransports, key)
	}
	accountTransportsMu.Unlock()

	if tr != nil {
		tr.CloseIdleConnections()
	}
}

// ResetAllAccountTransports 关闭并清空所有账号的专属连接池及全局共享连接池的空闲连接。
// 供网络断线恢复（NetWatch）或代理重置时统一调用。
func ResetAllAccountTransports() {
	accountTransportsMu.Lock()
	oldMap := accountTransports
	accountTransports = make(map[string]*http.Transport)
	accountTransportsMu.Unlock()

	for _, tr := range oldMap {
		if tr != nil {
			tr.CloseIdleConnections()
		}
	}

	if sharedTransport != nil {
		sharedTransport.CloseIdleConnections()
	}
}
