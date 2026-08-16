package netutil

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// dedicated_proxy.go 提供号池专属出站代理 (如 NVIDIA SOCKS5/HTTP 专属代理出口) 独立 Transport 构造与复用。
//
// 架构设计：
// 1. 与全局 GetSystemProxy 完全解耦：专属代理 Transport 强制走配置的独立出口 (http:// 或 socks5://)，
//    不读系统代理、不走 TUN 虚拟网卡默认路由，实现物理级号池出站分流。
// 2. 高性能连接池复用：专属 Transport 按配置指纹单例复用，同一端口配置下所有并发请求共享同一
//    *http.Transport (HTTP/2 TCP Keep-Alive 连接池复用，0 额外握手延迟)。
// 3. 错误安全降级：配置无效时返回明确 error，调用方可据此日志提示并安全回退默认 client。

var (
	// nvidiaDedicatedTransportMu 保护 currentNvidiaDedicatedTransport 读写并发
	nvidiaDedicatedTransportMu sync.RWMutex
	// currentNvidiaDedicatedTransport 是当前生效的 NVIDIA 专属 transport 单例
	currentNvidiaDedicatedTransport *http.Transport
	// currentNvidiaDedicatedSig 是当前配置指纹 ("addr|user|pass")
	currentNvidiaDedicatedSig string
)

// nvidiaDedicatedSignature 生成专属代理配置指纹
func nvidiaDedicatedSignature(addr, user, pass string) string {
	return addr + "|" + user + "|" + pass
}

// GetNvidiaDedicatedClient 返回一个绑定 NVIDIA 专属出口代理的 *http.Client (流式：Timeout=0)
func GetNvidiaDedicatedClient(addr, user, pass string) (*http.Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("nvidia dedicated proxy address is empty")
	}
	if !strings.Contains(addr, "://") {
		addr = "socks5://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid nvidia dedicated proxy address %q: %v", addr, err)
	}

	sig := nvidiaDedicatedSignature(addr, user, pass)

	// 读锁快路径
	nvidiaDedicatedTransportMu.RLock()
	t := currentNvidiaDedicatedTransport
	hit := currentNvidiaDedicatedSig == sig && t != nil
	nvidiaDedicatedTransportMu.RUnlock()
	if hit {
		return &http.Client{Transport: t, Timeout: 0}, nil
	}

	// 写锁慢路径 (双检锁)
	nvidiaDedicatedTransportMu.Lock()
	defer nvidiaDedicatedTransportMu.Unlock()
	if currentNvidiaDedicatedSig == sig && currentNvidiaDedicatedTransport != nil {
		return &http.Client{Transport: currentNvidiaDedicatedTransport, Timeout: 0}, nil
	}

	nt, err := buildFallbackTransport(u, user, pass)
	if err != nil {
		return nil, err
	}
	currentNvidiaDedicatedTransport = nt
	currentNvidiaDedicatedSig = sig
	return &http.Client{Transport: nt, Timeout: 0}, nil
}
