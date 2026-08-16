package relay

import (
	"testing"

	"antigravity-proxy/internal/settings"
)

// mockMutexSettings 模拟各种代理开关状态的 Settings
type mockMutexSettings struct {
	settings.ManagerInterface
	dedicatedAddr    string
	dedicatedEnabled bool
	workerURL        string
	workerEnabled    bool
}

func (m *mockMutexSettings) GetNvidiaDedicatedProxyAddress() string  { return m.dedicatedAddr }
func (m *mockMutexSettings) GetNvidiaDedicatedProxyEnabled() bool    { return m.dedicatedEnabled && m.dedicatedAddr != "" }
func (m *mockMutexSettings) GetNvidiaDedicatedProxyUsername() string { return "" }
func (m *mockMutexSettings) GetNvidiaDedicatedProxyPassword() string { return "" }
func (m *mockMutexSettings) GetNvidiaWorkerProxyURL() string          { return m.workerURL }
func (m *mockMutexSettings) IsNvidiaWorkerProxyEnabled() bool {
	return m.workerEnabled && m.workerURL != ""
}

func TestNvidiaProxy_AntiChainingAndMutex(t *testing.T) {
	// 场景 1: 仅开启专属 SOCKS5
	h1 := &APICompatHandler{
		settingsMgr: &mockMutexSettings{
			dedicatedAddr:    "socks5://127.0.0.1:7891",
			dedicatedEnabled: true,
			workerURL:        "https://my-worker.workers.dev",
			workerEnabled:    false,
		},
	}
	if !h1.isNvidiaDedicatedProxyEnabledSafe() {
		t.Errorf("专属代理应当开启")
	}
	if h1.isNvidiaWorkerProxyEnabledSafe() {
		t.Errorf("Worker 代理应当关闭")
	}

	// 场景 2: 仅开启 Worker 代理出口
	h2 := &APICompatHandler{
		settingsMgr: &mockMutexSettings{
			dedicatedAddr:    "socks5://127.0.0.1:7891",
			dedicatedEnabled: false,
			workerURL:        "https://my-worker.workers.dev",
			workerEnabled:    true,
		},
	}
	if h2.isNvidiaDedicatedProxyEnabledSafe() {
		t.Errorf("专属代理应当关闭")
	}
	if !h2.isNvidiaWorkerProxyEnabledSafe() {
		t.Errorf("Worker 代理应当开启")
	}

	// 场景 3: 两者在配置中同时为 true（防套娃兜底测试）
	h3 := &APICompatHandler{
		settingsMgr: &mockMutexSettings{
			dedicatedAddr:    "socks5://127.0.0.1:7891",
			dedicatedEnabled: true,
			workerURL:        "https://my-worker.workers.dev",
			workerEnabled:    true,
		},
	}
	// 此时若专属代理开启，baseURL 构造逻辑中必须被保护为官方 BaseURL，绝不套用 Worker URL
	useWorker := !h3.isNvidiaDedicatedProxyEnabledSafe() && h3.isNvidiaWorkerProxyEnabledSafe()
	if useWorker {
		t.Errorf("防套娃保护失效: 当专属代理开启时，绝不能套用 Worker 代理出口 URL")
	}
}
