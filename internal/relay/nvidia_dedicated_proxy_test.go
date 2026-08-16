package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-proxy/internal/netutil"
)

// mockDedicatedSettings 模拟专属代理配置的 Settings 实现
type mockDedicatedSettings struct {
	addr    string
	enabled bool
	user    string
	pass    string
}

func (m *mockDedicatedSettings) GetNvidiaDedicatedProxyAddress() string  { return m.addr }
func (m *mockDedicatedSettings) GetNvidiaDedicatedProxyEnabled() bool    { return m.enabled && m.addr != "" }
func (m *mockDedicatedSettings) GetNvidiaDedicatedProxyUsername() string { return m.user }
func (m *mockDedicatedSettings) GetNvidiaDedicatedProxyPassword() string { return m.pass }

func TestNvidiaDedicatedProxy_ClientCreationAndReuse(t *testing.T) {
	// 1. 无效地址报错
	_, err := netutil.GetNvidiaDedicatedClient("", "", "")
	if err == nil {
		t.Fatalf("空地址应当报错")
	}

	// 2. 正常 SOCKS5 地址构造与单例复用
	c1, err1 := netutil.GetNvidiaDedicatedClient("socks5://127.0.0.1:7891", "", "")
	if err1 != nil {
		t.Fatalf("构造 SOCKS5 client 失败: %v", err1)
	}
	if c1 == nil || c1.Transport == nil {
		t.Fatalf("client 或 transport 为空")
	}

	c2, err2 := netutil.GetNvidiaDedicatedClient("socks5://127.0.0.1:7891", "", "")
	if err2 != nil {
		t.Fatalf("二次获取 client 失败: %v", err2)
	}

	// 同一配置指纹必须复用同一 Transport 单例（共享连接池）
	if c1.Transport != c2.Transport {
		t.Errorf("相同配置应当复用相同的 Transport 单例以共享 TCP 连接池")
	}

	// 3. 配置变更后生成新 Transport
	c3, err3 := netutil.GetNvidiaDedicatedClient("http://127.0.0.1:8888", "user", "pass")
	if err3 != nil {
		t.Fatalf("构造 HTTP client 失败: %v", err3)
	}
	if c3.Transport == c1.Transport {
		t.Errorf("不同配置应当生成新的 Transport 单例")
	}
}

func TestNvidiaDedicatedProxy_HTTPForward(t *testing.T) {
	// 启动一个 mock upstream HTTP 服务器
	upstreamReceived := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	// 构造走该 mock 地址为代理的 client 并测试
	client, err := netutil.GetNvidiaDedicatedClient(ts.URL, "", "")
	if err != nil {
		t.Fatalf("构造 client 失败: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if !upstreamReceived {
		t.Errorf("mock 代理服务器应当接收到请求")
	}
}
