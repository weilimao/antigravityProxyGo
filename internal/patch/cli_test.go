package patch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cliNames 返回当前平台下 agy 可执行文件与重命名备份的文件名
func cliNames() (exeName, realExeName string) {
	if runtime.GOOS == "windows" {
		return "agy.exe", "agy_real.exe"
	}
	return "agy", "agy_real"
}

// setupCliSandbox 造一个带假 agy 可执行文件(>1MB，模拟真实二进制)的 bin 目录
func setupCliSandbox(t *testing.T) (binDir, appData, homeDir, caPath string) {
	t.Helper()
	root := t.TempDir()
	exeName, _ := cliNames()

	if runtime.GOOS == "windows" {
		localAppData := filepath.Join(root, "AppData", "Local")
		t.Setenv("LOCALAPPDATA", localAppData)
		appData = filepath.Join(root, "AppData", "Roaming", "antigravity-proxy-desktop")
		binDir = filepath.Join(localAppData, "agy", "bin")
	} else {
		appData = filepath.Join(root, "app-support")
	}
	homeDir = filepath.Join(root, "home")
	if runtime.GOOS != "windows" {
		binDir = filepath.Join(homeDir, ".gemini", "antigravity-cli", "bin")
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	// >1MB 的假二进制，满足 HijackCli 对真实 exe 的体积判定
	fakeBin := make([]byte, 1024*1024+16)
	copy(fakeBin, "FAKE-AGY-BINARY")
	if err := os.WriteFile(filepath.Join(binDir, exeName), fakeBin, 0755); err != nil {
		t.Fatal(err)
	}

	certDir := filepath.Join(root, "certs")
	if err := os.MkdirAll(certDir, 0755); err != nil {
		t.Fatal(err)
	}
	caPath = filepath.Join(certDir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return binDir, appData, homeDir, caPath
}

func TestHijackCliEnableGeneratesSelfHealingWrappers(t *testing.T) {
	binDir, appData, homeDir, caPath := setupCliSandbox(t)
	exeName, realExeName := cliNames()

	HijackCli(true, appData, homeDir, caPath, func(string) {})

	if _, err := os.Stat(filepath.Join(binDir, realExeName)); err != nil {
		t.Fatalf("expected %s to exist after hijack: %v", realExeName, err)
	}
	if _, err := os.Stat(filepath.Join(binDir, exeName)); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be renamed away, stat err=%v", exeName, err)
	}

	batBytes, err := os.ReadFile(filepath.Join(binDir, "agy.bat"))
	if err != nil {
		t.Fatalf("agy.bat not generated: %v", err)
	}
	bat := string(batBytes)
	for _, want := range []string{
		"if errorlevel 1 goto restore",
		"BeginConnect('127.0.0.1',18443",
		"move /y \"%~dp0agy_real",
		"restoring original agy CLI",
		"exit /b %ERRORLEVEL%",
		"\r\n", // 必须保持 CRLF，供 cmd 解析
	} {
		if !strings.Contains(bat, want) {
			t.Errorf("agy.bat missing %q", want)
		}
	}
	// 自愈分支要删除 Git Bash 用的 sh wrapper
	if !strings.Contains(bat, "del /f /q \"%~dp0agy\"") {
		t.Errorf("agy.bat should delete the sh wrapper on self-heal")
	}

	shBytes, err := os.ReadFile(filepath.Join(binDir, "agy"))
	if err != nil {
		t.Fatalf("sh wrapper not generated: %v", err)
	}
	sh := string(shBytes)
	for _, want := range []string{
		"(exec 3<>/dev/tcp/127.0.0.1/18443) 2>/dev/null", // /dev/tcp 必须用 bash 规范的斜杠形式，冒号形式在 Git Bash 下静默失败
		"mv \"$BASE/$REAL\" \"$BASE/$ORIG\"",
		"exec \"$BASE/$ORIG\" \"$@\"",
		"exec \"$BASE/$REAL\" \"$@\"",
		"restoring original agy CLI",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("sh wrapper missing %q", want)
		}
	}
	// 批处理里不应残留未展开的 fmt 占位符
	if strings.Contains(bat, "%s") {
		t.Errorf("agy.bat contains unexpanded %%s placeholder")
	}
}

func TestHijackCliDisableRestoresOriginal(t *testing.T) {
	binDir, appData, homeDir, caPath := setupCliSandbox(t)
	exeName, _ := cliNames()

	HijackCli(true, appData, homeDir, caPath, func(string) {})
	HijackCli(false, appData, homeDir, caPath, func(string) {})

	restored, err := os.ReadFile(filepath.Join(binDir, exeName))
	if err != nil {
		t.Fatalf("original %s not restored: %v", exeName, err)
	}
	if len(restored) != 1024*1024+16 {
		t.Fatalf("restored binary size mismatch: %d", len(restored))
	}
	if _, err := os.Stat(filepath.Join(binDir, "agy.bat")); !os.IsNotExist(err) {
		t.Errorf("expected agy.bat removed after restore, err=%v", err)
	}
}

// TestHijackCliReHijackAfterWrapperSelfHeal 模拟 wrapper 已自愈(agy.exe 恢复、备份消失)
// 后应用重启再次 PatchAll(true)，要求能干净地重新完成劫持闭环
func TestHijackCliReHijackAfterWrapperSelfHeal(t *testing.T) {
	binDir, appData, homeDir, caPath := setupCliSandbox(t)
	exeName, realExeName := cliNames()

	HijackCli(true, appData, homeDir, caPath, func(string) {})

	// 模拟 wrapper 自愈：real -> exe，删除 wrapper
	if err := os.Rename(filepath.Join(binDir, realExeName), filepath.Join(binDir, exeName)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(binDir, "agy.bat"))
	if runtime.GOOS == "windows" {
		_ = os.Remove(filepath.Join(binDir, "agy"))
	}

	HijackCli(true, appData, homeDir, caPath, func(string) {})

	if _, err := os.Stat(filepath.Join(binDir, realExeName)); err != nil {
		t.Fatalf("re-hijack failed to recreate %s: %v", realExeName, err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "agy.bat")); err != nil {
		t.Fatalf("re-hijack failed to rewrite agy.bat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "agy")); err != nil {
		t.Fatalf("re-hijack failed to rewrite sh wrapper: %v", err)
	}
}

func TestBuildShWrapperContentPlatformAdaptation(t *testing.T) {
	sh := buildShWrapperContent("http://127.0.0.1:18443", "agy", "agy_real")
	if !strings.Contains(sh, `if [ -f "$BASE/agy_real.exe" ]; then`) {
		t.Error("sh wrapper should detect Windows .exe layout")
	}
	if !strings.Contains(sh, `ORIG="agy.exe"`) {
		t.Error("sh wrapper should rename back to agy.exe on Windows")
	}
	// Windows 下恢复后要删除仍在运行的 sh 脚本自身；Unix 下 mv 已原位替换，不可删
	if !strings.Contains(sh, `if [ "$ORIG" != "agy" ]; then`) {
		t.Error("sh wrapper must only delete $BASE/agy on the Windows path")
	}
}
