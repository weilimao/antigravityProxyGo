package settings

import (
	"os"
	"testing"
)

// TestSettings_AntigravityWorkerProxy 锁定 Antigravity 号池 Worker 出口代理的 Get/Set/IsEnabled/落盘往返。
// 对仗 TestSettings_NvidiaWorkerProxy / TestSettings_GrokWorkerProxy,验证启用判定需 URL+开关双满足。
func TestSettings_AntigravityWorkerProxy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_antigravity_worker_proxy_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 初始状态:未配置,应返回 disabled。
	if mgr.IsAntigravityWorkerProxyEnabled() {
		t.Errorf("Expected IsAntigravityWorkerProxyEnabled == false initially")
	}
	if mgr.GetAntigravityWorkerProxyURL() != "" {
		t.Errorf("Expected empty URL initially, got %s", mgr.GetAntigravityWorkerProxyURL())
	}

	// 仅开开关但 URL 为空:不应视为启用。
	if err := mgr.SetAntigravityWorkerProxyEnabled(true); err != nil {
		t.Fatalf("SetAntigravityWorkerProxyEnabled failed: %v", err)
	}
	if mgr.IsAntigravityWorkerProxyEnabled() {
		t.Errorf("Expected disabled when enabled=true but URL is empty")
	}

	// 配置 URL 后才真正激活。
	testURL := "https://my-antigravity.workers.dev"
	if err := mgr.SetAntigravityWorkerProxyURL(testURL); err != nil {
		t.Fatalf("SetAntigravityWorkerProxyURL failed: %v", err)
	}
	if mgr.GetAntigravityWorkerProxyURL() != testURL {
		t.Errorf("Expected %s, got %s", testURL, mgr.GetAntigravityWorkerProxyURL())
	}
	if !mgr.IsAntigravityWorkerProxyEnabled() {
		t.Errorf("Expected IsAntigravityWorkerProxyEnabled == true after URL set")
	}

	// 重新加载验证落盘往返。
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	if mgr2.GetAntigravityWorkerProxyURL() != testURL {
		t.Errorf("Expected reloaded URL %s, got %s", testURL, mgr2.GetAntigravityWorkerProxyURL())
	}
	if !mgr2.IsAntigravityWorkerProxyEnabled() {
		t.Errorf("Expected reloaded IsAntigravityWorkerProxyEnabled == true")
	}
}
