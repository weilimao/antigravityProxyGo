package account

import (
	"os"
	"path/filepath"
	"testing"
)

// account_antigravity_test.go: 验证 Antigravity 号池全局 Hub 客户端版本号与 User-Agent 构造器。

func TestManager_AntigravityCliVersion(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 1. 默认值: 未配置时回退 DefaultAntigravityCliVersion ("2.3.1")
	if got := m.GetAntigravityCliVersion(); got != DefaultAntigravityCliVersion {
		t.Errorf("default antigravity cli version: want %q, got %q", DefaultAntigravityCliVersion, got)
	}
	if got := m.GetAntigravityUserAgent(); got != "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)" {
		t.Errorf("default antigravity user-agent: want %q, got %q", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)", got)
	}

	// 2. 显式设置自定义版本号
	m.SetAntigravityCliVersion("2.4.0")
	if got := m.GetAntigravityCliVersion(); got != "2.4.0" {
		t.Errorf("custom antigravity cli version: want %q, got %q", "2.4.0", got)
	}
	if got := m.GetAntigravityUserAgent(); got != "antigravity/hub/2.4.0 (aidev_client; os_type=windows; arch=amd64)" {
		t.Errorf("custom antigravity user-agent: want %q, got %q", "antigravity/hub/2.4.0 (aidev_client; os_type=windows; arch=amd64)", got)
	}

	// 3. 空白与首尾空格自动规整
	m.SetAntigravityCliVersion("   ")
	if got := m.GetAntigravityCliVersion(); got != DefaultAntigravityCliVersion {
		t.Errorf("blank should fall back to default %q, got %q", DefaultAntigravityCliVersion, got)
	}

	m.SetAntigravityCliVersion("  3.0.1  ")
	if got := m.GetAntigravityCliVersion(); got != "3.0.1" {
		t.Errorf("trimmed version: want %q, got %q", "3.0.1", got)
	}
	if got := m.GetAntigravityUserAgent(); got != "antigravity/hub/3.0.1 (aidev_client; os_type=windows; arch=amd64)" {
		t.Errorf("trimmed user-agent: want %q, got %q", "antigravity/hub/3.0.1 (aidev_client; os_type=windows; arch=amd64)", got)
	}

	// 4. nil 指针防御
	var nilMgr *Manager
	if got := nilMgr.GetAntigravityCliVersion(); got != DefaultAntigravityCliVersion {
		t.Errorf("nil manager version: want %q, got %q", DefaultAntigravityCliVersion, got)
	}
	if got := nilMgr.GetAntigravityUserAgent(); got != "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)" {
		t.Errorf("nil manager user-agent: want %q, got %q", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)", got)
	}
}

func TestManager_AntigravityCliVersion_Persistence(t *testing.T) {
	tmp := t.TempDir()
	m1 := NewManager()
	m1.Init(tmp)

	m1.SetAntigravityCliVersion("2.5.0")

	// 确认落盘文件 accounts_pool.json 存在
	poolPath := filepath.Join(tmp, "accounts_pool.json")
	if _, err := os.Stat(poolPath); err != nil {
		t.Fatalf("accounts_pool.json not found: %v", err)
	}

	// 新建 Manager 实例从磁盘加载
	m2 := NewManager()
	m2.Init(tmp)

	if got := m2.GetAntigravityCliVersion(); got != "2.5.0" {
		t.Fatalf("m2 AntigravityCliVersion loaded from disk = %q, want %q", got, "2.5.0")
	}
	if got := m2.GetAntigravityUserAgent(); got != "antigravity/hub/2.5.0 (aidev_client; os_type=windows; arch=amd64)" {
		t.Fatalf("m2 AntigravityUserAgent loaded from disk = %q, want %q", got, "antigravity/hub/2.5.0 (aidev_client; os_type=windows; arch=amd64)")
	}
}
