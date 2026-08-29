//go:build windows

package fileutil

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// AvailableBytes 返回 path 所在卷对当前用户可用的剩余字节数。
// path 可以尚不存在:逐级向上找第一个真实存在的目录,以其所在卷根查询。
func AvailableBytes(path string) (uint64, error) {
	dir := resolveExistingDir(path)
	vol := filepath.VolumeName(dir)
	if vol == "" {
		// 相对路径等异常形态:直接对目录本身查询。
		rootPtr, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return 0, err
		}
		return queryFreeSpace(rootPtr)
	}
	root := vol + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	return queryFreeSpace(rootPtr)
}

func queryFreeSpace(root *uint16) (uint64, error) {
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(root, &freeToCaller, &total, &totalFree); err != nil {
		return 0, err
	}
	// freeToCaller 已计入配额/权限约束,比 totalFree 更贴近「我们真的还能写多少」。
	return freeToCaller, nil
}

// resolveExistingDir 从 path 逐级向上找第一个真实存在的目录;均无则退回卷根。
func resolveExistingDir(path string) string {
	p := path
	for i := 0; i < 64; i++ {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
	return path
}
