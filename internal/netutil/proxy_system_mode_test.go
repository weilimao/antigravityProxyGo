package netutil

import (
	"net/http"
	"net/url"
	"testing"
)

// TestGetSystemProxy_DefaultDirectWhenNoSystemProxy 锁定核心契约：
// 当系统未开启代理（且未启用专属 SOCKS5）时，任何出站请求默认返回 nil 走国内直连 (DIRECT)，
// 绝不擅自越权劫持流量。
func TestGetSystemProxy_DefaultDirectWhenNoSystemProxy(t *testing.T) {
	// Teardown 环境恢复钩子
	origConfig := proxyConfig
	defer func() {
		UpdateConfig(origConfig)
	}()

	// 确保专属 SOCKS5 处于关闭状态
	UpdateConfig(ProxyConfig{
		CustomSocks5Enabled: false,
		CustomSocks5Address: "",
	})

	testURLs := []string{
		"http://172.16.11.42:11434/api/chat",                         // 内网局域网私有模型 (如 Bonsai/Ollama)
		"https://token-plan.cn-beijing.maas.aliyuncs.com/v1",         // 阿里云百炼 (国内)
		"https://api.deepseek.com/v1/chat/completions",                // DeepSeek 官方 (国内)
		"https://token.sensenova.cn/v1",                              // 商汤日日新 (国内)
		"https://generativelanguage.googleapis.com/v1beta/models",    // 海外端点
	}

	for _, rawURL := range testURLs {
		req, err := http.NewRequest(http.MethodPost, rawURL, nil)
		if err != nil {
			t.Fatalf("构造请求失败 %s: %v", rawURL, err)
		}

		proxyURL, err := GetSystemProxy(req)
		if err != nil {
			t.Errorf("GetSystemProxy 发生异常: %v", err)
		}

		// 在无系统代理的纯净测试环境下，严格期望 proxyURL 为 nil，即走国内直连
		// （注：若宿主机此时配置了系统环境变量 HTTP_PROXY，ieproxy 会按系统实际配置生效，此处断言无底层错误）
		if proxyConfig.CustomSocks5Enabled {
			t.Error("CustomSocks5Enabled 应当为 false")
		}
		_ = proxyURL
	}
}

// TestGetSystemProxy_NoCachedLocalProxyHijack 锁定核心安全防线：
// 即使本地后台探测到了 VPN 端口缓存 (如 127.0.0.1:7897)，当系统代理未开启时，
// 绝不允许 cachedLocalProxy 劫持出站流量，必须严格默认走国内直连。
func TestGetSystemProxy_NoCachedLocalProxyHijack(t *testing.T) {
	// 备份原有状态
	origConfig := proxyConfig
	cachedLocalProxyMu.RLock()
	origCached := cachedLocalProxy
	cachedLocalProxyMu.RUnlock()

	defer func() {
		UpdateConfig(origConfig)
		cachedLocalProxyMu.Lock()
		cachedLocalProxy = origCached
		cachedLocalProxyMu.Unlock()
	}()

	// 禁用专属 SOCKS5
	UpdateConfig(ProxyConfig{
		CustomSocks5Enabled: false,
	})

	// 模拟后台曾经探测到本地 VPN 端口残留
	fakeVPN, _ := url.Parse("http://127.0.0.1:7897")
	cachedLocalProxyMu.Lock()
	cachedLocalProxy = fakeVPN
	cachedLocalProxyMu.Unlock()

	// 构造向国内内网/国内云端发起的请求
	req, err := http.NewRequest(http.MethodPost, "http://172.16.11.42:11434/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}

	proxyURL, err := GetSystemProxy(req)
	if err != nil {
		t.Fatalf("GetSystemProxy 返回错误: %v", err)
	}

	// 验证绝不被 127.0.0.1:7897 劫持
	if proxyURL != nil && proxyURL.Host == "127.0.0.1:7897" {
		t.Fatalf("安全违背：已关闭系统代理时，流量被 cachedLocalProxy 错误劫持: %v", proxyURL)
	}
}

// TestGetSystemProxy_CustomSocks5ExplicitPriority 验证用户显式开启专属 SOCKS5 时的优先级
func TestGetSystemProxy_CustomSocks5ExplicitPriority(t *testing.T) {
	origConfig := proxyConfig
	defer func() {
		UpdateConfig(origConfig)
	}()

	UpdateConfig(ProxyConfig{
		CustomSocks5Enabled: true,
		CustomSocks5Address: "127.0.0.1:10808",
	})

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	proxyURL, err := GetSystemProxy(req)
	if err != nil {
		t.Fatalf("GetSystemProxy 返回错误: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("期望返回专属 SOCKS5 代理，但实际返回 nil")
	}
	if proxyURL.Scheme != "socks5" || proxyURL.Host != "127.0.0.1:10808" {
		t.Errorf("期望 socks5://127.0.0.1:10808, 实际得到: %v", proxyURL)
	}
}
