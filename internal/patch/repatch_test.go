package patch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockDesktopController struct {
	asarPath     string
	detectErr    error
	running      bool
	killErr      error
	launchErr    error
	killCalls    int
	launchCalls  int
	launchedAsar string
	events       []string
	stopAfter    int // 模拟调用 IsAppRunning 多少次后进程退出
	checkCount   int
}

func (m *mockDesktopController) DetectAsarPath(customPath string) (string, error) {
	if m.detectErr != nil {
		return "", m.detectErr
	}
	return m.asarPath, nil
}

func (m *mockDesktopController) IsAppRunning() bool {
	m.checkCount++
	if m.stopAfter > 0 && m.killCalls > 0 && m.checkCount >= m.stopAfter {
		m.running = false
	}
	return m.running
}

func (m *mockDesktopController) KillApp() error {
	m.killCalls++
	m.events = append(m.events, "kill")
	if m.killErr != nil {
		return m.killErr
	}
	if m.stopAfter == 0 {
		m.running = false
	}
	return nil
}

func (m *mockDesktopController) LaunchApp(asarPath string) error {
	m.launchCalls++
	m.launchedAsar = asarPath
	m.events = append(m.events, "launch")
	return m.launchErr
}

// TestPatchAsar_RefreshesStaleBakAfterUpdate 验证 Antigravity 更新覆盖 app.asar 后，
// PatchAsar 会同步用新版原始内容刷新旧版本留下的 app.asar.bak，从而在后续 RestoreAsar 时还原为新版原文。
func TestPatchAsar_RefreshesStaleBakAfterUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	asarPath := filepath.Join(tmpDir, "app.asar")
	bakPath := asarPath + ".bak"

	oldOriginalJs := "function start(env) {\n    (0, oldMod.setupNodeWrapper)(env);\n    return 'v1';\n}"
	if err := os.WriteFile(bakPath, buildMockAsar(t, oldOriginalJs), 0644); err != nil {
		t.Fatalf("failed to write stale bak: %v", err)
	}

	newOriginalJs := "function start(env) {\n    (0, newMod.setupNodeWrapper)(env);\n    return 'v2';\n}"
	newAsarBytes := buildMockAsar(t, newOriginalJs)
	if err := os.WriteFile(asarPath, newAsarBytes, 0644); err != nil {
		t.Fatalf("failed to write updated app.asar: %v", err)
	}

	var logs []string
	if err := PatchAsar(asarPath, "", func(msg string) { logs = append(logs, msg) }); err != nil {
		t.Fatalf("PatchAsar after update failed: %v", err)
	}

	patchedData, err := os.ReadFile(asarPath)
	if err != nil {
		t.Fatalf("failed to read patched app.asar: %v", err)
	}
	if !strings.Contains(string(patchedData), asarInjectedMarker) {
		t.Fatalf("expected patched app.asar to contain injected marker")
	}

	// 验证 app.asar.bak 已被刷新为新版原始内容
	refreshedBak, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("failed to read refreshed bak: %v", err)
	}
	if !strings.Contains(string(refreshedBak), "return 'v2'") || strings.Contains(string(refreshedBak), "return 'v1'") {
		t.Fatalf("expected bak to be refreshed with v2 content, got: %s", string(refreshedBak))
	}

	// 验证退出还原后得到新版原文而非旧版 v1
	if err := RestoreAsar(asarPath); err != nil {
		t.Fatalf("RestoreAsar failed: %v", err)
	}
	restoredData, err := os.ReadFile(asarPath)
	if err != nil {
		t.Fatalf("failed to read restored app.asar: %v", err)
	}
	if !strings.Contains(string(restoredData), "return 'v2'") || strings.Contains(string(restoredData), asarInjectedMarker) {
		t.Fatalf("expected restored app.asar to match clean v2, got: %s", string(restoredData))
	}
}

// TestRepatchAndRestart_RunningAppLifecycle 验证「运行中 Antigravity 更新后重新打补丁并重启」全流程：
// 严格遵循 先 KillApp -> 等待退出 -> 执行 Patch -> 自动 LaunchApp 的时序。
func TestRepatchAndRestart_RunningAppLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	asarDir := filepath.Join(tmpDir, "resources")
	if err := os.MkdirAll(asarDir, 0755); err != nil {
		t.Fatal(err)
	}
	asarPath := filepath.Join(asarDir, "app.asar")
	newOriginalJs := "function start(env) {\n    (0, a.setupNodeWrapper)(env);\n    return env;\n}"
	if err := os.WriteFile(asarPath, buildMockAsar(t, newOriginalJs), 0644); err != nil {
		t.Fatal(err)
	}

	ctrl := &mockDesktopController{
		asarPath:  asarPath,
		running:   true,
		stopAfter: 2,
	}

	var patchCalls int
	svc := NewRepatchService(ctrl, func(enable bool, appData, homeDir, caPath string, logFn func(string)) error {
		patchCalls++
		ctrl.events = append(ctrl.events, "patch")
		if !enable {
			t.Fatalf("expected enable=true during repatch")
		}
		if ctrl.running {
			t.Fatalf("patch must not execute while Antigravity is still running")
		}
		return nil
	})

	var logs []string
	res := svc.RepatchAndRestart(RepatchOptions{
		DefaultUserData: filepath.Join(tmpDir, "AppData", "Roaming", "antigravity-proxy-desktop"),
		HomeDir:         tmpDir,
		CACertPath:      filepath.Join(tmpDir, "ca.pem"),
		PollInterval:    5 * time.Millisecond,
		MaxKillWait:     200 * time.Millisecond,
		PostExitDelay:   0,
	}, func(s string) { logs = append(logs, s) })

	if !res.Success || !res.Patched || !res.Restarted || !res.WasRunning {
		t.Fatalf("unexpected repatch result: %+v", res)
	}
	if patchCalls != 1 || ctrl.killCalls != 1 || ctrl.launchCalls != 1 {
		t.Fatalf("unexpected call counts: patch=%d kill=%d launch=%d", patchCalls, ctrl.killCalls, ctrl.launchCalls)
	}
	if strings.Join(ctrl.events, "->") != "kill->patch->launch" {
		t.Fatalf("expected sequence kill->patch->launch, got %v", ctrl.events)
	}
	if ctrl.launchedAsar != asarPath {
		t.Fatalf("expected LaunchApp(%q), got %q", asarPath, ctrl.launchedAsar)
	}

	// 验证目标 app.asar 已真实被补丁覆盖
	patchedBytes, err := os.ReadFile(asarPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patchedBytes), asarInjectedMarker) {
		t.Fatalf("expected app.asar to be patched")
	}
}

// TestRepatchAndRestart_PatchErrorAbortsLaunch 验证当补丁注入失败时，绝不继续重启桌面端且真实向上报错。
func TestRepatchAndRestart_PatchErrorAbortsLaunch(t *testing.T) {
	tmpDir := t.TempDir()
	asarPath := filepath.Join(tmpDir, "app.asar")

	ctrl := &mockDesktopController{
		asarPath: asarPath,
		running:  false,
	}

	svc := NewRepatchService(ctrl, func(enable bool, appData, homeDir, caPath string, logFn func(string)) error {
		return errors.New("permission denied")
	})

	res := svc.RepatchAndRestart(RepatchOptions{
		DefaultUserData: tmpDir,
		HomeDir:         tmpDir,
		PollInterval:    5 * time.Millisecond,
		MaxKillWait:     50 * time.Millisecond,
	}, nil)

	if res.Success || res.Patched || res.Restarted {
		t.Fatalf("expected failure result when patch fails, got %+v", res)
	}
	if !strings.Contains(res.Error, "permission denied") {
		t.Fatalf("expected error to contain root cause, got %q", res.Error)
	}
	if ctrl.launchCalls != 0 {
		t.Fatalf("LaunchApp must not be called when patching fails, got %d", ctrl.launchCalls)
	}
}

// TestRepatchAndRestart_KillTimeoutAbortsPatch 验证无法退出进程超时时立即中止，避免强行覆写被占用文件。
func TestRepatchAndRestart_KillTimeoutAbortsPatch(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := &mockDesktopController{
		asarPath:  filepath.Join(tmpDir, "app.asar"),
		running:   true,
		stopAfter: 99999, // 永不退出
	}

	patchCalled := false
	svc := NewRepatchService(ctrl, func(enable bool, appData, homeDir, caPath string, logFn func(string)) error {
		patchCalled = true
		return nil
	})

	res := svc.RepatchAndRestart(RepatchOptions{
		DefaultUserData: tmpDir,
		HomeDir:         tmpDir,
		PollInterval:    5 * time.Millisecond,
		MaxKillWait:     20 * time.Millisecond,
	}, nil)

	if res.Success || res.Patched || res.Restarted || !res.WasRunning {
		t.Fatalf("expected timeout failure result, got %+v", res)
	}
	if patchCalled || ctrl.launchCalls != 0 {
		t.Fatalf("must not patch or launch after kill timeout (patchCalled=%v, launchCalls=%d)", patchCalled, ctrl.launchCalls)
	}
}
