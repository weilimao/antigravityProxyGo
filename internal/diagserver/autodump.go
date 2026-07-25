// Package diagserver autodump.go — 周期性 goroutine 自动快照
//
// 设计背景:
//   程序出现"几十秒固定卡死、CPU 静止、请求全部进不去"的死等型阻塞时,
//   手动 curl pprof 端点往往也连不上(端点 goroutine 同样被阻塞链卡死)。
//   因此本模块在后台独立 goroutine 中,按固定周期把全部 goroutine
//   调用栈直接写入本地磁盘文件,卡死后离线取证即可定位真凶行号。
//
// 工作策略:
//   - 每采样周期抓一次 runtime goroutine 全栈
//   - 默认每次都写(卡死现场要的是最末态),采用滚动覆盖:仅保留最近
//     N 份快照于固定目录,grep 友好的纯文本
//   - 单独一个采样 goroutine,不依赖任何被卡死的消费者,不向任何
//     channel 投递,因此它自身几乎不可能被同一个阻塞点拖死
//   - 写入失败静默忽略,绝不影响主程序
package diagserver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
)

const (
	sampleInterval  = 5 * time.Second // 采样周期:5 秒
	keepSnapshots   = 6               // 滚动保留最近 6 份快照 = 最近 30 秒现场
	snapshotNameFmt = "goroutines_%02d.txt"
)

var (
	autodumpOnce sync.Once
)

// StartAutoDump 在后台启动周期性 goroutine 快照写入。
// dumpDir 为快照文件目录;自动创建。幂等:重复调用安全。
// dir 为空则放弃 (不写)。即使采样协程 panic 也不影响主流程。
func StartAutoDump(dir string) {
	if dir == "" {
		return
	}
	autodumpOnce.Do(func() {
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		if mkErr := os.MkdirAll(abs, 0755); mkErr != nil {
			// 目录创建失败则放弃,避免后台死循环
			return
		}
		go autodumpLoop(abs)
	})
}

// autodumpLoop 周期采样 goroutine 全栈并滚动写入。
func autodumpLoop(dir string) {
	// 用独立 recover 兜底:即便 runtime 异常也绝不把进程拖崩
	defer func() {
		_ = recover()
	}()

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	idx := 0
	for range ticker.C {
		writeSnapshot(dir, &idx)
	}
}

// writeSnapshot 抓取一次 goroutine 全栈并写入第 idx 份快照文件 (滚动覆盖)。
func writeSnapshot(dir string, idx *int) {
	// pprof.Lookup("goroutine") 返回 *pprof.Profile;WriteTo 写出
	// debug=1 纯文本、人类可读的 goroutine 列表与调用栈。
	p := pprof.Lookup("goroutine")
	if p == nil {
		return
	}

	// 先收集到内存 buffer,再一次性写盘,避免半写入文件被取证时显示截断
	runtime.GC() // 触发一次 GC,使 goroutine 状态更稳定可读 (可选)

	// header 提供时间戳与 goroutine 计数,便于翻阅
	buf := make([]byte, 0, 64*1024)
	buf = append(buf, []byte(fmt.Sprintf(
		"=== autodump %s | goroutines=%d | gomaxprocs=%d\n",
		time.Now().Format("2006-01-02 15:04:05.000"), runtime.NumGoroutine(), runtime.GOMAXPROCS(0)))...)

	// 用 pprof 输出全栈;debug=2 给完整调用栈 + 阻塞原因 (chan send/recv、semacquire 等)
	buf = append(buf, []byte("\n")...)
	_ = p.WriteTo(&byteWriter{buf: &buf}, 1)

	fname := filepath.Join(dir, fmt.Sprintf(snapshotNameFmt, *idx%keepSnapshots))
	// 先写临时文件再 rename,降低写过程中被取证读到截断内容的几率
	tmp := fname + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, fname)
	*idx++

	// 同时写一份 "latest" 别名,方便优先查看最末态
	latest := filepath.Join(dir, "goroutines_LATEST.txt")
	tmpLatest := latest + ".tmp"
	_ = os.WriteFile(tmpLatest, buf, 0644)
	_ = os.Rename(tmpLatest, latest)
}

// byteWriter 仅实现 io.Writer,持有 *[]byte 以最小开销追加。
type byteWriter struct {
	buf *[]byte
}

func (w *byteWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// SnapshotDirFor 根据应用数据目录计算快照存放目录。
// 与其它运行时产物隔离,放在 dataDir/diag 下。
func SnapshotDirFor(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "diag")
}

// Suppress unused linter noise
var _ = strconv.Itoa
