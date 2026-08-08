package dialogs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// dialogKind 标识对话框业务域，决定记忆键与无记忆时的默认目录。
type dialogKind int

const (
	kindSave dialogKind = iota
	kindOpen
	kindOpenDir
)

// WailsDialogs 是 Dialogs 接口的 Wails 实现。依赖通过构造函数注入。
type WailsDialogs struct {
	dirProvider DataDirProvider
	memory      DirMemory
	revealFn    RevealCallback
	logFn       LogFunc
}

// NewWailsDialogs 构造函数注入依赖，禁止内部 new 依赖项。
func NewWailsDialogs(dirProvider DataDirProvider, logFn LogFunc) *WailsDialogs {
	if logFn == nil {
		logFn = func(string) {}
	}
	return &WailsDialogs{dirProvider: dirProvider, logFn: logFn}
}

// WithMemory 注入目录记忆读写实现。未注入时对话框不具备跨会话目录记忆，
// 全部回退到默认目录（保持旧行为）。
func (d *WailsDialogs) WithMemory(memory DirMemory) *WailsDialogs {
	d.memory = memory
	return d
}

// WithReveal 注入"打开文件夹定位文件"回调。由 decovealFile 调用。
func (d *WailsDialogs) WithReveal(revealFn RevealCallback) *WailsDialogs {
	d.revealFn = revealFn
	return d
}

// memoryKey 返回该对话框业务域对应的目录记忆键。
// 导出/导入/选目录各自记忆，互不覆盖。
func (d *WailsDialogs) memoryKey(kind dialogKind) DirMemoryKey {
	switch kind {
	case kindSave:
		return MemExportDir
	case kindOpen:
		return MemImportDir
	default:
		return MemDataDir
	}
}

// defaultSubDir 返回该对话框业务域无记忆时的默认子目录：
//   - 导出 → exports（应用数据目录下，保持既有组装路径）
//   - 导入 → 空（默认定位到数据目录本身）
//   - 选目录 → 空（默认定位到数据目录本身）
func (d *WailsDialogs) defaultSubDir(kind dialogKind) string {
	if kind == kindSave {
		return DefaultExportSubdir
	}
	return ""
}

// baseDir 返回数据根目录，含主目录/临时目录回退。
func (d *WailsDialogs) baseDir() string {
	if d.dirProvider != nil {
		if b := d.dirProvider.GetActiveDataDirectory(); b != "" {
			return b
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return os.TempDir()
}

// ensureDir 确保 base/subDir（subDir 为空则用 base）存在并返回完整路径。
func (d *WailsDialogs) ensureDir(base, subDir string) string {
	if subDir == "" {
		return base
	}
	dir := filepath.Join(base, subDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		d.logFn(fmt.Sprintf("⚠️ [Dialogs] 默认目录创建失败，回退到临时目录: %v", err))
		return os.TempDir()
	}
	return dir
}

// resolveDefaultDir 计算某业务域对话框的默认目录：
//   1. 记忆目录（仍存在）优先
//   2. 请求显式 SubDir
//   3. 业务域默认子目录
func (d *WailsDialogs) resolveDefaultDir(kind dialogKind, subDir string) string {
	if key := d.memoryKey(kind); d.memory != nil {
		if mem := d.memory.Get(key); mem != "" {
			if info, err := os.Stat(mem); err == nil && info.IsDir() {
				return mem
			}
		}
	}

	if subDir != "" {
		return d.ensureDir(d.baseDir(), subDir)
	}
	return d.ensureDir(d.baseDir(), d.defaultSubDir(kind))
}

// rememberPath 将对话框返回路径所在的目录写入记忆。
func (d *WailsDialogs) rememberPath(kind dialogKind, path string) {
	if d.memory == nil || path == "" {
		return
	}
	d.memory.Set(d.memoryKey(kind), filepath.Dir(path))
}

// RevealFile 打开该文件所在文件夹并定位/选中该文件（由调用方在文件写入成功后触发）。
// 向 revealFn 传入完整文件路径，由注入实现做 explorer /select 等精确定位。
func (d *WailsDialogs) RevealFile(path string) {
	if path == "" {
		return
	}
	if d.revealFn == nil {
		return
	}
	d.revealFn(path)
}

// MemoizeDir 将外部选择的目录写入"选目录"记忆键，供非本包对话框复用统一目录记忆。
func (d *WailsDialogs) MemoizeDir(dir string) {
	d.memorySet(kindOpenDir, dir)
}

// memorySet 目录选择成功后把目录本身写入记忆。
func (d *WailsDialogs) memorySet(kind dialogKind, dir string) {
	if d.memory == nil || dir == "" {
		return
	}
	d.memory.Set(d.memoryKey(kind), dir)
}

// toWailsFilters 将内部 FileFilter 列表转换为 Wails 过滤器。
func toWailsFilters(filters []FileFilter) []wailsRuntime.FileFilter {
	if len(filters) == 0 {
		return nil
	}
	out := make([]wailsRuntime.FileFilter, 0, len(filters))
	for _, f := range filters {
		out = append(out, wailsRuntime.FileFilter{DisplayName: f.DisplayName, Pattern: f.Pattern})
	}
	return out
}

// Save 触发"另存为"对话框。用户取消返回 ok=false, err=nil。
// 成功后记忆所选文件所在目录（自动打开由调用方在写盘后调 decovealFile）。
func (d *WailsDialogs) Save(ctx context.Context, req SaveRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	path, err := wailsRuntime.SaveFileDialog(ctx, wailsRuntime.SaveDialogOptions{
		Title:            req.Title,
		DefaultDirectory: d.resolveDefaultDir(kindSave, req.SubDir),
		DefaultFilename:  req.DefaultName,
		Filters:          toWailsFilters(req.Filters),
	})
	if err != nil {
		d.logFn(fmt.Sprintf("❌ [Dialogs] 保存对话框失败: %v", err))
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}

	d.rememberPath(kindSave, path)
	return path, true, nil
}

// Open 触发"打开文件"对话框。成功选择后记忆该文件所在目录。
func (d *WailsDialogs) Open(ctx context.Context, req OpenRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	path, err := wailsRuntime.OpenFileDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title:            req.Title,
		DefaultDirectory: d.resolveDefaultDir(kindOpen, req.SubDir),
		Filters:          toWailsFilters(req.Filters),
	})
	if err != nil {
		d.logFn(fmt.Sprintf("❌ [Dialogs] 打开对话框失败: %v", err))
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}

	d.rememberPath(kindOpen, path)
	return path, true, nil
}

// OpenDir 触发"选择目录"对话框。成功选择后记忆该目录本身。
func (d *WailsDialogs) OpenDir(ctx context.Context, req DirRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	path, err := wailsRuntime.OpenDirectoryDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title:            req.Title,
		DefaultDirectory: d.resolveDefaultDir(kindOpenDir, req.SubDir),
	})
	if err != nil {
		d.logFn(fmt.Sprintf("❌ [Dialogs] 目录选择对话框失败: %v", err))
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}

	d.memorySet(kindOpenDir, path)
	return path, true, nil
}