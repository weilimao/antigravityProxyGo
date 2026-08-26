package settings

import (
	"os"
	"testing"
)

// TestSettings_GrokWorkerProxy 锁定 Grok 号池 Worker 出口代理的 Get/Set/IsEnabled/落盘往返。
// 对仗 TestSettings_NvidiaWorkerProxy,验证启用判定需 URL+开关双满足(防误配置)。
func TestSettings_GrokWorkerProxy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_grok_worker_proxy_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 初始状态:未配置 URL,应返回 disabled。
	if mgr.IsGrokWorkerProxyEnabled() {
		t.Errorf("Expected IsGrokWorkerProxyEnabled == false initially")
	}
	if mgr.GetGrokWorkerProxyURL() != "" {
		t.Errorf("Expected empty URL initially, got %s", mgr.GetGrokWorkerProxyURL())
	}

	// 仅开开关但 URL 为空:不应视为启用(防误配置导致上游落空)。
	if err := mgr.SetGrokWorkerProxyEnabled(true); err != nil {
		t.Fatalf("SetGrokWorkerProxyEnabled failed: %v", err)
	}
	if mgr.IsGrokWorkerProxyEnabled() {
		t.Errorf("Expected disabled when enabled=true but URL is empty")
	}

	// 配置 URL 后才真正激活。
	testURL := "https://my-grok.workers.dev"
	if err := mgr.SetGrokWorkerProxyURL(testURL); err != nil {
		t.Fatalf("SetGrokWorkerProxyURL failed: %v", err)
	}
	if mgr.GetGrokWorkerProxyURL() != testURL {
		t.Errorf("Expected %s, got %s", testURL, mgr.GetGrokWorkerProxyURL())
	}
	if !mgr.IsGrokWorkerProxyEnabled() {
		t.Errorf("Expected IsGrokWorkerProxyEnabled == true after URL set")
	}

	// 关掉开关:URL 仍在但不再激活。
	if err := mgr.SetGrokWorkerProxyEnabled(false); err != nil {
		t.Fatalf("SetGrokWorkerProxyEnabled(false) failed: %v", err)
	}
	if mgr.IsGrokWorkerProxyEnabled() {
		t.Errorf("Expected disabled after toggle off")
	}

	// 重新加载验证落盘往返。
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	if mgr2.GetGrokWorkerProxyURL() != testURL {
		t.Errorf("Expected reloaded URL %s, got %s", testURL, mgr2.GetGrokWorkerProxyURL())
	}
	if mgr2.IsGrokWorkerProxyEnabled() {
		t.Errorf("Expected reloaded IsGrokWorkerProxyEnabled == false (toggle was off)")
	}
}
