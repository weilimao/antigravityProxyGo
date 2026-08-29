package fileutil

// atomic_test.go: 磁盘安全落盘基础设施的行为锁定。
// ENOSPC 无法在单测中真实模拟(需装满 tmpfs),故:
//   - 原子性由「tmp+fsync+rename」结构性保证 + 失败路径清理断言锁定;
//   - 损坏恢复与 .bak 回退用真实文件态驱动(主坏/bak 好、双坏、全新安装)。

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomic_WriteAndOverwrite 锁定:新写/覆盖语义 + 内容一致 + 不残留临时文件。
func TestWriteFileAtomic_WriteAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	if err := WriteFileAtomic(path, []byte(`{"v":1}`), 0644); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"v":1}` {
		t.Fatalf("内容不符: %q err=%v", got, err)
	}

	// 覆盖写(reno 覆盖已存在目标是 Windows 上的关键语义)
	if err := WriteFileAtomic(path, []byte(`{"v":2}`), 0644); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != `{"v":2}` {
		t.Fatalf("覆盖后内容不符: %q", got)
	}

	// 目录内不得残留任何临时文件
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// TestWriteFileAtomicWithBak_GeneratesBackup 锁定:主文件与 .bak 双落盘,内容一致。
func TestWriteFileAtomicWithBak_GeneratesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts_x.json")
	body := []byte(`{"accounts":[{"email":"a@x.dev"}]}`)

	if err := WriteFileAtomicWithBak(path, body, 0644); err != nil {
		t.Fatalf("WithBak 写入失败: %v", err)
	}
	for _, p := range []string{path, BakPath(path)} {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(body) {
			t.Fatalf("%s 内容不符: %q err=%v", filepath.Base(p), got, err)
		}
	}
}

// TestReadJSONWithBak_RecoverFromBak 锁定:主文件 JSON 损坏,自动从 .bak 恢复(fromBak=true),
// 且主文件不被旁移(.bak 恢复成功即视为可用)。
func TestReadJSONWithBak_RecoverFromBak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts_x.json")
	good := []byte(`{"accounts":[{"email":"b@x.dev"}]}`)
	if err := os.WriteFile(path, []byte(`{"accounts":[{broken`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BakPath(path), good, 0644); err != nil {
		t.Fatal(err)
	}

	var shell struct {
		Accounts []struct {
			Email string `json:"email"`
		} `json:"accounts"`
	}
	fromBak, err := ReadJSONWithBak(path, &shell)
	if err != nil {
		t.Fatalf("应从 .bak 恢复成功: %v", err)
	}
	if !fromBak {
		t.Fatal("数据来自 .bak,fromBak 应为 true")
	}
	if len(shell.Accounts) != 1 || shell.Accounts[0].Email != "b@x.dev" {
		t.Fatalf("恢复内容不符: %+v", shell)
	}
	// 恢复成功不旁移主文件(留给下次写盘自愈)
	if _, err := os.Stat(path + ".corrupt"); err == nil {
		t.Fatal(".bak 恢复成功时不应旁移主文件")
	}
}

// TestReadJSONWithBak_BothCorrupt_Quarantines 锁定:主文件与 .bak 双坏 →
// 返回错误 + 主文件被旁移为 .corrupt(字节保留可人工抢救),不返回脏数据。
func TestReadJSONWithBak_BothCorrupt_Quarantines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"broken`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BakPath(path), []byte(`also-broken`), 0644); err != nil {
		t.Fatal(err)
	}

	var v map[string]any
	_, err := ReadJSONWithBak(path, &v)
	if err == nil {
		t.Fatal("双坏应返回错误")
	}
	// 主文件已被旁移:原路径消失,.corrupt 存在且保留原字节
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatal("双坏后主文件应被旁移(原路径不应再存在)")
	}
	corruptBytes, cerr := os.ReadFile(path + ".corrupt")
	if cerr != nil || string(corruptBytes) != `{"broken` {
		t.Fatalf(".corrupt 罪证字节不符: %q err=%v", corruptBytes, cerr)
	}
}

// TestReadJSONWithBak_FreshInstall 锁定:主文件与 .bak 均不存在(全新安装)→
// 错误可被 errors.Is(err, fs.ErrNotExist) 判定为常规缺失,不旁移、不误判损坏。
func TestReadJSONWithBak_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never.existed.json")
	var v map[string]any
	_, err := ReadJSONWithBak(path, &v)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("全新安装应返回 NotExist 族错误,实际: %v", err)
	}
	if _, serr := os.Stat(path + ".corrupt"); !os.IsNotExist(serr) {
		t.Fatal("全新安装不应产生 .corrupt 旁移文件")
	}
}

// TestQuarantineCorrupt_Idempotent 锁定:旁移可重复执行(覆盖旧 .corrupt),文件不存在时静默返回空。
func TestQuarantineCorrupt_Idempotent(t *testing.T) {
	if q := QuarantineCorrupt(filepath.Join(t.TempDir(), "ghost.json")); q != "" {
		t.Fatalf("不存在的文件旁移应返回空串,实际 %q", q)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	_ = os.WriteFile(path, []byte("first"), 0644)
	_ = os.WriteFile(path+".corrupt", []byte("stale"), 0644)
	if q := QuarantineCorrupt(path); q == "" {
		t.Fatal("存在文件旁移应返回新路径")
	}
	got, _ := os.ReadFile(path + ".corrupt")
	if string(got) != "first" {
		t.Fatalf("旧 .corrupt 应被覆盖为最新坏文件,实际 %q", got)
	}
}

// TestPrecheck_PassesOnTmpdir 锁定:正常磁盘上预检放行(不禁锢正常写盘)。
func TestPrecheck_PassesOnTmpdir(t *testing.T) {
	if err := precheckDiskSpace(filepath.Join(t.TempDir(), "a.json"), 4096); err != nil {
		t.Fatalf("正常磁盘预检应放行: %v", err)
	}
}

// TestHumanizeBytes 格式化口径锁定。
func TestHumanizeBytes(t *testing.T) {
	cases := map[uint64]string{
		512:         "512B",
		2048:        "2.0KiB",
		8 << 20:     "8.0MiB",
		3<<30 + 512: "3.0GiB",
	}
	for in, want := range cases {
		if got := humanizeBytes(in); got != want {
			t.Fatalf("humanizeBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
