package account

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// account_antigravity_test.go: 验证 Antigravity 号池全局 Hub 客户端版本号与 User-Agent 构造器。

func TestManager_AntigravityCliVersion(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 1. 默认值: 未配置时回退 DefaultAntigravityCliVersion ("2.29.1") 与 DefaultAntigravityBuildCL ("1015941558")
	if got := m.GetAntigravityCliVersion(); got != DefaultAntigravityCliVersion {
		t.Errorf("default antigravity cli version: want %q, got %q", DefaultAntigravityCliVersion, got)
	}
	if got := m.GetAntigravityBuildCL(); got != DefaultAntigravityBuildCL {
		t.Errorf("default antigravity build CL: want %q, got %q", DefaultAntigravityBuildCL, got)
	}
	defaultUA := FormatAntigravityUserAgent(DefaultAntigravityCliVersion)
	if got := m.GetAntigravityUserAgent(); got != defaultUA {
		t.Errorf("default antigravity user-agent: want %q, got %q", defaultUA, got)
	}

	// 2. 显式设置自定义版本号
	m.SetAntigravityCliVersion("2.30.0")
	if got := m.GetAntigravityCliVersion(); got != "2.30.0" {
		t.Errorf("custom antigravity cli version: want %q, got %q", "2.30.0", got)
	}
	want230 := FormatAntigravityUserAgentWithCL("2.30.0", DefaultAntigravityBuildCL)
	if got := m.GetAntigravityUserAgent(); got != want230 {
		t.Errorf("custom antigravity user-agent: want %q, got %q", want230, got)
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

	// 4. nil 指针防御
	var nilMgr *Manager
	if got := nilMgr.GetAntigravityCliVersion(); got != DefaultAntigravityCliVersion {
		t.Errorf("nil manager version: want %q, got %q", DefaultAntigravityCliVersion, got)
	}
	if got := nilMgr.GetAntigravityBuildCL(); got != DefaultAntigravityBuildCL {
		t.Errorf("nil manager build CL: want %q, got %q", DefaultAntigravityBuildCL, got)
	}
	if got := nilMgr.GetAntigravityUserAgent(); got != defaultUA {
		t.Errorf("nil manager user-agent: want %q, got %q", defaultUA, got)
	}
}

func TestManager_AntigravityCliVersion_Persistence(t *testing.T) {
	tmp := t.TempDir()
	m1 := NewManager()
	m1.Init(tmp)

	m1.SetAntigravityCliVersion("2.30.0")
	m1.SetAntigravityBuildCL("1020000000")

	// 确认落盘文件 accounts_pool.json 存在
	poolPath := filepath.Join(tmp, "accounts_pool.json")
	if _, err := os.Stat(poolPath); err != nil {
		t.Fatalf("accounts_pool.json not found: %v", err)
	}

	// 新建 Manager 实例从磁盘加载
	m2 := NewManager()
	m2.Init(tmp)

	if got := m2.GetAntigravityCliVersion(); got != "2.30.0" {
		t.Fatalf("m2 AntigravityCliVersion loaded from disk = %q, want %q", got, "2.30.0")
	}
	if got := m2.GetAntigravityBuildCL(); got != "1020000000" {
		t.Fatalf("m2 AntigravityBuildCL loaded from disk = %q, want %q", got, "1020000000")
	}
	wantUA := FormatAntigravityUserAgentWithCL("2.30.0", "1020000000")
	if got := m2.GetAntigravityUserAgent(); got != wantUA {
		t.Fatalf("m2 AntigravityUserAgent loaded from disk = %q, want %q", got, wantUA)
	}
}

func TestManager_UpdateAntigravityVersionAndCL(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	var hookCalled bool
	var hookedVer, hookedCL string
	m.SetOnAntigravityVersionUpdated(func(ver, cl string) {
		hookCalled = true
		hookedVer = ver
		hookedCL = cl
	})

	// 1. 更新版本与 CL
	updated := m.UpdateAntigravityVersionAndCL("2.29.1", "1015941558")
	// 初始默认就是 2.29.1 和 1015941558 时返回 false (无需重复写盘)
	if updated {
		t.Errorf("expected false when values match default, got true")
	}

	// 2. 模拟新版本嗅探触发更新
	updated = m.UpdateAntigravityVersionAndCL("2.29.2", "1016888999")
	if !updated {
		t.Fatalf("expected true when updating to new version, got false")
	}
	if !hookCalled {
		t.Errorf("hook should be called on update")
	}
	if hookedVer != "2.29.2" || hookedCL != "1016888999" {
		t.Errorf("hook values mismatch: ver=%s, cl=%s", hookedVer, hookedCL)
	}

	// 3. 重复调用相同版本应幂等跳过
	hookCalled = false
	updatedAgain := m.UpdateAntigravityVersionAndCL("2.29.2", "1016888999")
	if updatedAgain {
		t.Errorf("duplicate update should return false")
	}
	if hookCalled {
		t.Errorf("duplicate update should not fire hook")
	}

	// 4. 并发更新竞争安全测试
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m.UpdateAntigravityVersionAndCL("2.29.2", "1016888999")
			_ = m.GetAntigravityUserAgent()
		}(i)
	}
	wg.Wait()
}
