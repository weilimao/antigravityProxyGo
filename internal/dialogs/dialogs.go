// Package dialogs 封装 Wails 文件对话框，提供统一的导出/打开/目录选择入口。
//
// 设计目标：
//   - 解耦：调用方仅依赖 Dialogs 接口，无需直接引用 wailsRuntime。
//   - 记忆：导出/导入/选目录各自记住用户上一次选择的目录，后续对话框默认定位
//     到该目录（替代 Wails 内部不可控的记忆，实现跨会话持久化）。
//   - 自动打开：Save 成功后自动打开/定位刚保存的文件所在文件夹。
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
//
// 语义约定：
//   - Save 成功返回有效路径后，内部应记住该文件所在目录。
//   - Open 成功返回有效路径后，内部应记住该文件所在目录。
//   - OpenDir 成功返回有效目录后，内部应记住该目录本身。
//   - RevealFile 在"导出保存文件已成功写入"后由调用方触发，
//     打开该文件所在文件夹并定位到文件。
//   - MemoizeDir 供非本包对话框（如 Debugger 目录选择）把用户选择
//     持久化到"选目录"记忆键，使其同样进入统一记忆。
type Dialogs interface {
	Save(ctx context.Context, req SaveRequest) (path string, ok bool, err error)
	Open(ctx context.Context, req OpenRequest) (path string, ok bool, err error)
	OpenDir(ctx context.Context, req DirRequest) (path string, ok bool, err error)
	RevealFile(path string)
	MemoizeDir(dir string)
}

// DirMemory 是"上次选择目录"的持久化读写接口。
// 实现须保证线程安全；Get 返回 "" 表示尚未有记忆。
type DirMemory interface {
	Get(key DirMemoryKey) string
	Set(key DirMemoryKey, dir string)
}

// DirMemoryKey 是目录记忆的唯一键。按业务域隔离，导出/导入/选目录互不覆盖。
type DirMemoryKey string

// 内置记忆键。对应 Request 的 SubDir 语义：
//   - Save   → MemExportDir  ：记录日志/账号/抓包等导出的"保存位置"
//   - Open   → MemImportDir  ：记录账号导入等"打开位置"
//   - OpenDir → MemDataDir   ：记录数据目录选择位置
const (
	MemExportDir DirMemoryKey = "export"
	MemImportDir DirMemoryKey = "import"
	MemDataDir   DirMemoryKey = "data-dir"
)

// RevealCallback 在"导出保存成功"后回调，参数为刚写入的文件完整路径。
// 由外部注入系统文件管理器能力（如 explorer /select,<path> 精确定位并选中文件）。
// 注意：参数是文件路径本身（非其所在目录），实现方据此做"定位到文件"而非仅"打开目录"。
type RevealCallback func(filePath string)

// DataDirProvider 由外部注入，用于获取应用当前的数据存储根目录。
type DataDirProvider interface {
	GetActiveDataDirectory() string
}

// LogFunc 关键日志回调，避免包内直接依赖具体日志实现。
type LogFunc func(msg string)