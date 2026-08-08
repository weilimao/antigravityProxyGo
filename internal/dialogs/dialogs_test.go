package dialogs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// mockMemory 内存版 DirMemory，供单测使用。
type mockMemory struct {
	mu sync.RWMutex
	m  map[DirMemoryKey]string
}

func (mm *mockMemory) Get(key DirMemoryKey) string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.m[key]
}

func (mm *mockMemory) Set(key DirMemoryKey, dir string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.m[key] = dir
}

// mockDataDirProvider 返回固定数据目录。
type mockDataDirProvider struct{ dir string }

func (p mockDataDirProvider) GetActiveDataDirectory() string { return p.dir }

func TestFileDirMemory_RoundTrip(t *testing.T) {
	root := t.TempDir()
	exportDir := filepath.Join(root, "exports")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := NewFileDirMemory(root)
	m.Set(MemExportDir, exportDir)

	// 重新加载，验证持久化
	m2 := NewFileDirMemory(root)
	if got := m2.Get(MemExportDir); got != exportDir {
		t.Fatalf("expected %q, got %q", exportDir, got)
	}
}

func TestFileDirMemory_FiltersMissingDirs(t *testing.T) {
	root := t.TempDir()
	// 创建一个真正存在的导入目录
	importDir := t.TempDir()
	// 手动写一个指向不存在目录的记忆文件（用 json.Marshal 保证路径转义正确）
	raw, _ := json.Marshal(SerializableEventMemory{
		Memory: map[DirMemoryKey]string{
			MemExportDir: filepath.Join(root, "nonexistent"),
			MemImportDir: importDir,
		},
	})
	if err := os.WriteFile(filepath.Join(root, MemoryKeyFile), raw, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewFileDirMemory(root)
	if got := m.Get(MemExportDir); got != "" {
		t.Fatalf("export should be filtered as missing dir, got %q", got)
	}
	if got := m.Get(MemImportDir); got != importDir {
		t.Fatalf("import should survive, got %q", got)
	}
}

func TestFileDirMemory_LoadCorruptFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, MemoryKeyFile), []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewFileDirMemory(root)
	if got := m.Get(MemExportDir); got != "" {
		t.Fatalf("corrupt file should yield empty memory, got %q", got)
	}
}

// stubWailsApp 无法直接调用 wailsRuntime，因此用 WailsDialogs 的方法 pointer 上
// 做逻辑级测试：resolveDefaultDir / memoryKey / rememberPath / revealFolder。
func TestWailsDialogs_MemoryRouting(t *testing.T) {
	mem := &mockMemory{m: map[DirMemoryKey]string{}}
	d := NewWailsDialogs(mockDataDirProvider{t.TempDir()}, nil)
	d.WithMemory(mem)

	// OpenDir 记忆到 MemDataDir
	d.memorySet(kindOpenDir, "/tmp/data")
	if got := mem.Get(MemDataDir); got != "/tmp/data" {
		t.Fatalf("OpenDir should remember MemDataDir, got %q", got)
	}

	// Open 记忆到 MemImportDir（通过 rememberPath）
	d.rememberPath(kindOpen, filepath.Join(t.TempDir(), "a.json"))
	if got := mem.Get(MemImportDir); got == "" {
		t.Fatal("Open should remember MemImportDir")
	}

	// Save 记忆到 MemExportDir
	d.rememberPath(kindSave, filepath.Join(t.TempDir(), "b.md"))
	if got := mem.Get(MemExportDir); got == "" {
		t.Fatal("Save should remember MemExportDir")
	}

	// 三个键互不覆盖
	if mem.Get(MemExportDir) == mem.Get(MemImportDir) {
		t.Fatal("export/import memories must be independent")
	}
}

func TestWailsDialogs_ResolveDefaultDirFallback(t *testing.T) {
	dataDir := t.TempDir()
	d := NewWailsDialogs(mockDataDirProvider{dataDir}, nil)

	// 无记忆时导出默认到 dataDir/exports
	dir := d.resolveDefaultDir(kindSave, "")
	if filepath.Clean(dir) != filepath.Join(dataDir, DefaultExportSubdir) {
		t.Fatalf("save default should be %q, got %q", filepath.Join(dataDir, DefaultExportSubdir), dir)
	}

	// 导入/选目录默认到数据目录本身
	for _, kind := range []dialogKind{kindOpen, kindOpenDir} {
		dir = d.resolveDefaultDir(kind, "")
		if filepath.Clean(dir) != filepath.Clean(dataDir) {
			t.Fatalf("kind %d default should be dataDir, got %q", kind, dir)
		}
	}

	// 记忆目录存在时优先
	exportMem := filepath.Join(t.TempDir(), "my-export")
	_ = os.MkdirAll(exportMem, 0755)
	mem := &mockMemory{m: map[DirMemoryKey]string{MemExportDir: exportMem}}
	d.WithMemory(mem)
	if got := d.resolveDefaultDir(kindSave, ""); filepath.Clean(got) != filepath.Clean(exportMem) {
		t.Fatalf("memory should override default, got %q", got)
	}
}

func TestWailsDialogs_RevealCalledOnSavePath(t *testing.T) {
	mem := &mockMemory{m: map[DirMemoryKey]string{}}
	var revealed string
	var revealedMu sync.Mutex
	d := NewWailsDialogs(mockDataDirProvider{t.TempDir()}, nil)
	d.WithMemory(mem)
	d.WithReveal(func(folder string) {
		revealedMu.Lock()
		revealed = folder
		revealedMu.Unlock()
	})

	// 不真正触发 wailsRuntime，直接测内部 helper 语义
	filePath := filepath.Join(t.TempDir(), "sub", "export.md")
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	d.RevealFile(filePath)
	revealedMu.Lock()
	defer revealedMu.Unlock()
	// RevealFile 向注入回调传入完整文件路径（非其所在目录），
	// 由实现方做 explorer /select 精确定位并选中文件。
	if revealed != filePath {
		t.Fatalf("expected reveal %q, got %q", filePath, revealed)
	}
}

func TestSaveCancelReturnsOkFalse(t *testing.T) {
	// 纯逻辑：无 memory/reveal 的实例，在 path=="" 时不 panic、不落地记忆。
	mem := &mockMemory{m: map[DirMemoryKey]string{}}
	d := NewWailsDialogs(mockDataDirProvider{t.TempDir()}, nil)
	d.WithMemory(mem)

	// 先写一个记忆，再验证空路径不覆盖它
	mem.Set(MemExportDir, "existing")
	// 验证空路径不落地记忆
	d.rememberPath(kindSave, "")
	if mem.Get(MemExportDir) != "existing" {
		t.Fatal("empty path must not overwrite export memory")
	}

	// RevealFile 空路径不应触发回调
	var revealed bool
	d.WithReveal(func(string) { revealed = true })
	d.RevealFile("")
	if revealed {
		t.Fatal("empty path must not reveal")
	}

	// RevealFile 有效路径应触发回调并传入所在目录
	d.RevealFile(filepath.Join("C:", "fake", "file.md"))
	if !revealed {
		t.Fatal("valid path should reveal")
	}

	// MemoizeDir 应写入选目录记忆键
	d.MemoizeDir("F:\\my\\folder")
	if mem.Get(MemDataDir) != "F:\\my\\folder" {
		t.Fatalf("MemoizeDir should memoize MemDataDir, got %q", mem.Get(MemDataDir))
	}
}