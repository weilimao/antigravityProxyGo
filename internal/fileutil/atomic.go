// Package fileutil 提供「磁盘写满也不丢数据」的落盘基础设施。
//
// 事故背景:核心数据文件(账号分区/settings 配置)此前用 os.WriteFile 直落盘,
// 写满磁盘(ENOSPC)时文件被截成残缺字节;下次启动读到坏 JSON → 内存回退默认值 →
// 后续正常保存把默认值盖回破损文件,用户数据永久湮灭。
//
// 本包提供三层防线:
//  1. WriteFileAtomic:同目录 tmp + 全量写校验 + fsync + rename。rename 同卷原子,
//     要么旧文件要么新文件,绝无半截态(Windows 上 Go 的 os.Rename 自带
//     MOVEFILE_REPLACE_EXISTING,覆盖安全)。ENOSPC 只损害 tmp,原文件分毫未动。
//  2. WriteFileAtomicWithBak + ReadBytesWithBak / ReadJSONWithBak:不可再生数据
//     (账号/配置)多一份 .bak 快照;主文件损坏时自动从 .bak 恢复,双毁则
//     QuarantineCorrupt 把坏文件旁移为 .corrupt 保留罪证,绝不用空数据覆盖。
//  3. 写前磁盘空间预检(P2):剩余空间不足时返回 ErrInsufficientSpace 快速失败,
//     连 tmp 都不建 —— 防御性保险丝;真正的兜底始终是原子写本身。
package fileutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// ErrInsufficientSpace 磁盘剩余空间不足以安全落盘(预检拦截)时返回,
// 调用方可用 errors.Is 特判,给出「未落盘」语义的用户可见告警。
var ErrInsufficientSpace = errors.New("insufficient disk space")

// minFreeReserveBytes 落盘所需的安全边:写完目标文件后磁盘至少还应剩这么多,
// 避免把磁盘真正写到 0(系统/其他组件也需要喘息空间)。
const minFreeReserveBytes = 8 << 20 // 8 MiB

// WriteFileAtomic 原子写入 path:先写同目录临时文件并 fsync,再 rename 覆盖目标。
// 任一步失败都不会损害已存在的目标文件;失败时清理临时文件。
// 写前做磁盘空间预检(需要 len(data) + 安全边),不足时返回 ErrInsufficientSpace。
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := precheckDiskSpace(path, int64(len(data))); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			// 失败路径:关闭并清走临时文件,不留残渣。
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if n, werr := tmp.Write(data); werr != nil || n != len(data) {
		if werr == nil {
			werr = fmt.Errorf("short write: %d/%d bytes", n, len(data))
		}
		return fmt.Errorf("write temp for %s: %w", path, werr)
	}
	if serr := tmp.Sync(); serr != nil {
		return fmt.Errorf("fsync temp for %s: %w", path, serr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("close temp for %s: %w", path, cerr)
	}
	// CreateTemp 权限恒为 0600;保持与旧 os.WriteFile(path, data, 0644) 一致的最终权限。
	// Windows 上 Chmod 对只读位以外基本 no-op,忽略其错误。
	_ = os.Chmod(tmpName, perm)
	if rerr := os.Rename(tmpName, path); rerr != nil {
		return fmt.Errorf("rename temp to %s: %w", path, rerr)
	}
	done = true // tmp 已被 rename 接管,defer 不再清理
	syncDirBestEffort(dir)
	return nil
}

// WriteFileAtomicWithBak 原子写入 path,成功后把同一份内容 best-effort 落到 path+".bak"。
// 仅用于「不可再生」的用户数据(账号分区、settings 配置);可重建数据请用 WriteFileAtomic。
// .bak 写失败不阻塞主流程(主文件已经原子落盘),返回 nil。
func WriteFileAtomicWithBak(path string, data []byte, perm fs.FileMode) error {
	// 预检按「主文件 + .bak 双份」计价,避免主写成功而 bak 把磁盘顶穿的边界态。
	if err := precheckDiskSpace(path, 2*int64(len(data))); err != nil {
		return err
	}
	if err := WriteFileAtomic(path, data, perm); err != nil {
		return err
	}
	if berr := WriteFileAtomic(BakPath(path), data, perm); berr != nil {
		fmt.Printf("[fileutil] 写入 %s 的 .bak 快照失败(主文件已安全落盘): %v\n", path, berr)
	}
	return nil
}

// BakPath 返回 path 的备份快照路径。
func BakPath(path string) string { return path + ".bak" }

// ReadBytesWithBak 依次尝试读主文件与 .bak 快照;任一成功即返回其字节。
// 两者皆不可读时返回主文件的原始错误(含 os.ErrNotExist,便于调用方区分「全新安装」)。
func ReadBytesWithBak(path string) (data []byte, fromBak bool, err error) {
	data, err = os.ReadFile(path)
	if err == nil {
		return data, false, nil
	}
	bakData, berr := os.ReadFile(BakPath(path))
	if berr == nil {
		return bakData, true, nil
	}
	return nil, false, err
}

// ReadJSONWithBak 读取并 JSON 解析文件;主文件不可读或解析失败时回退 .bak 快照重试。
// 双侧均失败且主文件存在(=内容确实损坏)时,调用 QuarantineCorrupt 把主文件旁移为
// .corrupt(保留字节供人工恢复),杜绝后续「空数据覆盖损坏文件」的丢失放大器。
// 返回 fromBak=true 表示本次数据来自 .bak(调用方宜打告警日志)。
// 全新安装(主与 .bak 均不存在)返回的 error 满足 errors.Is(err, fs.ErrNotExist)。
func ReadJSONWithBak(path string, out interface{}) (fromBak bool, err error) {
	mainData, mErr := os.ReadFile(path)
	if mErr == nil {
		if uErr := json.Unmarshal(mainData, out); uErr == nil {
			return false, nil
		} else {
			mErr = fmt.Errorf("解析 %s 失败: %w", path, uErr)
		}
	}
	bakData, bErr := os.ReadFile(BakPath(path))
	if bErr == nil {
		if uErr := json.Unmarshal(bakData, out); uErr == nil {
			return true, nil
		}
	}
	// 双侧都不可用。全新安装(两个文件都不存在)按常规 not-exist 透传,不当损坏处理。
	if os.IsNotExist(mErr) && os.IsNotExist(bErr) {
		return false, mErr
	}
	if !os.IsNotExist(mErr) {
		// 主文件存在但坏了 → 旁移罪证,防止空数据回写湮灭字节。
		if q := QuarantineCorrupt(path); q != "" {
			return false, fmt.Errorf("%v;主文件已旁移为 %s(可人工恢复)", mErr, q)
		}
	}
	return false, mErr
}

// QuarantineCorrupt 把损坏文件旁移为 path+".corrupt"(已存在则覆盖),返回旁移后路径;
// 文件不存在或旁移失败返回空串。语义:保留损坏字节供人工取证,同时让下一轮读写从干净起点开始。
func QuarantineCorrupt(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	dst := path + ".corrupt"
	_ = os.Remove(dst)
	if err := os.Rename(path, dst); err != nil {
		return ""
	}
	return dst
}

// precheckDiskSpace 写前预检:path 所在盘剩余空间 < needBytes + 安全边时返回
// ErrInsufficientSpace。查询失败(特殊挂载/无权限)时放行 —— 预检只是保险丝,
// 原子写本身才是最后防线,绝不因预检不可用而阻拦落盘。
func precheckDiskSpace(path string, needBytes int64) error {
	avail, err := AvailableBytes(filepath.Dir(path))
	if err != nil {
		return nil
	}
	need := uint64(needBytes) + minFreeReserveBytes
	if avail < need {
		return fmt.Errorf("%w: 目标盘剩余 %s,安全落盘需要 %s(含 8MiB 安全边)", ErrInsufficientSpace,
			humanizeBytes(avail), humanizeBytes(need))
	}
	return nil
}

// syncDirBestEffort 在 unix 上 fsync 目录,把 rename 的元数据一并刷盘;
// Windows 不支持打开目录句柄做 Sync(NTFS rename 元数据已具原子性),直接跳过。
func syncDirBestEffort(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// humanizeBytes 把字节数格式化为人类可读(MB 量级够告警与日志阅读用)。
func humanizeBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit && exp < 5; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
