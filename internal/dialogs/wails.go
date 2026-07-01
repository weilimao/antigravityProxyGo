package dialogs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// WailsDialogs 是 Dialogs 接口的 Wails 实现。依赖通过构造函数注入。
type WailsDialogs struct {
	dirProvider DataDirProvider
	logFn       LogFunc
}

// NewWailsDialogs 构造函数注入依赖，禁止内部 new 依赖项。
func NewWailsDialogs(dirProvider DataDirProvider, logFn LogFunc) *WailsDialogs {
	if logFn == nil {
		logFn = func(string) {}
	}
	return &WailsDialogs{dirProvider: dirProvider, logFn: logFn}
}

// resolveDefaultDir 计算并确保默认目录存在。
// 优先级：请求指定 SubDir > DefaultExportSubdir。
// 若数据目录不可用，回退到用户主目录，最后回退到 os.TempDir。
func (d *WailsDialogs) resolveDefaultDir(subDir string) string {
	if subDir == "" {
		subDir = DefaultExportSubdir
	}

	var base string
	if d.dirProvider != nil {
		base = d.dirProvider.GetActiveDataDirectory()
	}
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = home
		} else {
			base = os.TempDir()
		}
	}

	dir := filepath.Join(base, subDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		d.logFn(fmt.Sprintf("⚠️ [Dialogs] 默认导出目录创建失败，回退到临时目录: %v", err))
		return os.TempDir()
	}
	return dir
}

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
func (d *WailsDialogs) Save(ctx context.Context, req SaveRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	path, err := wailsRuntime.SaveFileDialog(ctx, wailsRuntime.SaveDialogOptions{
		Title:            req.Title,
		DefaultDirectory: d.resolveDefaultDir(req.SubDir),
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
	return path, true, nil
}

// Open 触发"打开文件"对话框。
func (d *WailsDialogs) Open(ctx context.Context, req OpenRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	path, err := wailsRuntime.OpenFileDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title:            req.Title,
		DefaultDirectory: d.resolveDefaultDir(req.SubDir),
		Filters:          toWailsFilters(req.Filters),
	})
	if err != nil {
		d.logFn(fmt.Sprintf("❌ [Dialogs] 打开对话框失败: %v", err))
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}

// OpenDir 触发"选择目录"对话框。注意：OpenDirectoryDialog 不强制 DefaultDirectory，
// 但为保持一致体验仍尝试指定。
func (d *WailsDialogs) OpenDir(ctx context.Context, req DirRequest) (string, bool, error) {
	if ctx == nil {
		return "", false, errors.New("dialogs: nil context")
	}
	opts := wailsRuntime.OpenDialogOptions{Title: req.Title}
	if req.SubDir != "" {
		opts.DefaultDirectory = d.resolveDefaultDir(req.SubDir)
	}
	path, err := wailsRuntime.OpenDirectoryDialog(ctx, opts)
	if err != nil {
		d.logFn(fmt.Sprintf("❌ [Dialogs] 目录选择对话框失败: %v", err))
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}
