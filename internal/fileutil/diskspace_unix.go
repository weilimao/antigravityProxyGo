//go:build !windows

package fileutil

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// AvailableBytes 返回 path 所在文件系统对当前用户可用的剩余字节数。
// path 可以尚不存在:逐级向上找第一个真实存在的目录后 statfs。
func AvailableBytes(path string) (uint64, error) {
	dir := resolveExistingDir(path)
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bavail 计入非 root 配额约束,bfree 是物理剩余;取保守口径 Bavail。
	return st.Bavail * uint64(st.Bsize), nil
}

// resolveExistingDir 从 path 逐级向上找第一个真实存在的目录;均无则退回当前目录。
func resolveExistingDir(path string) string {
	p := path
	for i := 0; i < 64; i++ {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "."
		}
		p = parent
	}
	return "."
}
