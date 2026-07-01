// Package dialogs 封装 Wails 文件对话框，提供统一的导出/打开/目录选择入口。
//
// 设计目标：
//   - 解耦：调用方仅依赖 Dialogs 接口，无需直接引用 wailsRuntime。
//   - 一致：导出对话框默认目录锚定应用数据目录下的 exports/ 子目录，
//     避免 Wails 内部记忆上一次打开的位置。
//   - 可测：接口驱动，便于 mock。
package dialogs

import "context"

// FileFilter 描述对话框的文件过滤器。
type FileFilter struct {
	DisplayName string
	Pattern     string
}

// SaveRequest 描述一次"另存为"请求。
type SaveRequest struct {
	Title       string
	DefaultName string
	Filters     []FileFilter
	// SubDir 相对于应用数据目录的默认子目录。为空则使用 DefaultExportSubdir。
	SubDir string
}

// OpenRequest 描述一次"打开"请求。
type OpenRequest struct {
	Title   string
	Filters []FileFilter
	SubDir  string
}

// DirRequest 描述一次"选择目录"请求。
type DirRequest struct {
	Title  string
	SubDir string
}

// Dialogs 是对话框抽象接口，实现类必须保证：
//   - 用户取消时返回 ok=false 且 err=nil。
//   - 保存/打开的绝对路径统一使用 OS 原生分隔符。
type Dialogs interface {
	Save(ctx context.Context, req SaveRequest) (path string, ok bool, err error)
	Open(ctx context.Context, req OpenRequest) (path string, ok bool, err error)
	OpenDir(ctx context.Context, req DirRequest) (path string, ok bool, err error)
}

// DataDirProvider 由外部注入，用于获取应用当前的数据存储根目录。
type DataDirProvider interface {
	GetActiveDataDirectory() string
}

// LogFunc 关键日志回调，避免包内直接依赖具体日志实现。
type LogFunc func(msg string)
