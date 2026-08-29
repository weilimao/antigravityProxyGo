package relay

// nvidia_upstream_timing.go: NVIDIA 上游流式响应的「字节到达节奏」观测组件(P0 观测埋点)。
//
// 背景:用户反馈 NVIDIA 池「有时候首帧响应慢 / 流式输出一会快一会慢」。既有观测:
//   - TTFT(响应头维度):stats.FirstByteRecorder,见 nvidia.go handleNvidia;
//   - 拨号耗时:netutil.DialContext 的 AddNetworkLog;
//   唯独缺「流式过程中上游吐字节奏」这一维 —— 无法区分上游排队、上游解码抖动、
//   出口链路抖动。本文件以零侵入 io.Reader 包装补上这一维。
//
// 粒度说明:观测的是「网络字节到达批次」而非解析后的 SSE 帧 —— scanner 每次 Read
// 尽量攒当前已到数据,不保证一帧一批。但这恰是上游吐字节奏在网络层的最真实视图:
// 批间 gap 大 = 上游在憋字(或链路在卡)。只观测、不拦截,对转发/重试/取消零改动。

import (
	"bytes"
	"io"
	"math"
	"sort"
	"sync"
	"time"
)

// maxGapSamples 是单 attempt gap 样本上限。长流(数万 token)一次 delta 一 read,
// 样本量与读批次同阶;不设上限会以 8 字节/样本无界增长。65536 样本(约 512KB 瞬态)
// 远超日常流规模,触顶后停止采样但 reads/bytes 照计,统计退化为现状不变。
const maxGapSamples = 65536

// upstreamTimingReader 包装上游 resp.Body,打点每次 Read 返回数据的时刻。
type upstreamTimingReader struct {
	inner io.Reader
	nowFn func() time.Time // 可注入假时钟(测试确定性)

	mu        sync.Mutex // scanner 单 goroutine 读取,锁仅作防御
	started   time.Time
	firstByte time.Time
	lastRead  time.Time
	reads     int
	bytes     int64
	gaps      []time.Duration
}

// newUpstreamTimingReader 构造观测包装;inner 为 nil 时退化为空 reader(防裸奔 panic)。
func newUpstreamTimingReader(inner io.Reader) *upstreamTimingReader {
	return newUpstreamTimingReaderWithClock(inner, time.Now)
}

// newUpstreamTimingReaderWithClock 与上同,但时钟可注入 —— 测试用假时钟保证
// started/firstByte/gap 全程同一时基(构造后再替换 nowFn 会使 started 与后续打点跨时钟,差值失真)。
func newUpstreamTimingReaderWithClock(inner io.Reader, nowFn func() time.Time) *upstreamTimingReader {
	if inner == nil {
		inner = bytes.NewReader(nil)
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	r := &upstreamTimingReader{inner: inner, nowFn: nowFn}
	r.started = r.nowFn()
	r.lastRead = r.started
	return r
}

// Read 直通内部 reader,仅在拿到数据(n>0)时打点;EOF/错误不计 gap。
func (r *upstreamTimingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		now := r.nowFn()
		r.mu.Lock()
		if r.reads == 0 {
			r.firstByte = now
		} else if len(r.gaps) < maxGapSamples {
			r.gaps = append(r.gaps, now.Sub(r.lastRead))
		}
		r.reads++
		r.bytes += int64(n)
		r.lastRead = now
		r.mu.Unlock()
	}
	return n, err
}

// upstreamTimingSnapshot 是单次 attempt 的不可变统计快照。
type upstreamTimingSnapshot struct {
	Reads         int           // 有效读批次数
	Bytes         int64         // 总字节
	FirstByteWait time.Duration // started→首批数据(近似本 attempt 流式首帧延迟);零读时为 0
	GapMax        time.Duration // 相邻读批最大间隔
	GapP95        time.Duration // 相邻读批 p95 间隔(近邻秩口径)
	Elapsed       time.Duration // started→快照时刻(本 attempt 上游读总耗时)
}

// snapshot 取当前统计快照(单次排序拷贝,仅 attempt 结束调用,开销可忽略)。
func (r *upstreamTimingReader) snapshot() upstreamTimingSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := upstreamTimingSnapshot{
		Reads:   r.reads,
		Bytes:   r.bytes,
		Elapsed: r.nowFn().Sub(r.started),
	}
	if r.reads > 0 {
		s.FirstByteWait = r.firstByte.Sub(r.started)
	}
	if len(r.gaps) > 0 {
		sorted := make([]time.Duration, len(r.gaps))
		copy(sorted, r.gaps)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		s.GapMax = sorted[len(sorted)-1]
		// 近邻秩 p95:ceil(0.95N)-1,N≥1 时恒在界内。
		idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		s.GapP95 = sorted[idx]
	}
	return s
}
