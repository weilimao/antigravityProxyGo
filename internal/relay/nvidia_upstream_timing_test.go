package relay

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// nvidia_upstream_timing_test.go 用注入假时钟确定性验证 P0 上游节奏观测:
// 首字节等待/gap max/p95/字节数必须可由固定脚本推演的理论值逐位对上,
// 全程不触真实网络、不 sleep。

// chunkReader 按脚本逐段吐出数据,读完返回 io.EOF(模拟上游 SSE 分块到达)。
// 约束:下游读取缓冲必须足以容纳单 chunk(测试用 io.ReadAll / 足量 p)。
type chunkReader struct {
	chunks [][]byte
	idx    int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.idx >= len(c.chunks) {
		return 0, io.EOF
	}
	b := c.chunks[c.idx]
	c.idx++
	if len(p) < len(b) {
		// 防御:缓冲不足时本测试件不截断语义,直接报错暴露用法错误。
		return 0, io.ErrShortBuffer
	}
	return copy(p, b), nil
}

// fakeClock 可手动推进的单调时钟。
type fakeClock struct{ cur time.Time }

func (f *fakeClock) now() time.Time          { return f.cur }
func (f *fakeClock) advance(d time.Duration) { f.cur = f.cur.Add(d) }

// mkChunks 构造 n 个单字节 chunk,便于按"读批次"精确控制。
func mkChunks(n int) [][]byte {
	chunks := make([][]byte, n)
	for i := range chunks {
		chunks[i] = []byte{byte('a' + i%26)}
	}
	return chunks
}

// TestUpstreamTimingReader_PassthroughIntegrity 锁定零失真:观测包装不得吞改任何字节。
func TestUpstreamTimingReader_PassthroughIntegrity(t *testing.T) {
	payload := []byte("data: {\"delta\":{\"content\":\"hello\"}}\n\ndata: [DONE]\n\n")
	tr := newUpstreamTimingReader(bytes.NewReader(payload))
	out, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("字节失真\nwant %q\ngot  %q", payload, out)
	}
	if tr.snapshot().Bytes != int64(len(payload)) {
		t.Fatalf("字节统计不符: want %d got %d", len(payload), tr.snapshot().Bytes)
	}
}

// TestUpstreamTimingReader_FirstByteAndGaps 脚本化三次到达,断言首字节等待与 gap 统计。
func TestUpstreamTimingReader_FirstByteAndGaps(t *testing.T) {
	clk := &fakeClock{cur: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	cr := &chunkReader{chunks: mkChunks(3)}
	tr := newUpstreamTimingReaderWithClock(cr, clk.now)

	buf := make([]byte, 64)
	// 上游到达: +3s 首批 → +1200ms 第二批 → +50ms 第三批
	clk.advance(3 * time.Second)
	if n, err := tr.Read(buf); err != nil || n != 1 {
		t.Fatalf("read1: n=%d err=%v", n, err)
	}
	clk.advance(1200 * time.Millisecond)
	if _, err := tr.Read(buf); err != nil {
		t.Fatalf("read2: %v", err)
	}
	clk.advance(50 * time.Millisecond)
	if _, err := tr.Read(buf); err != nil {
		t.Fatalf("read3: %v", err)
	}

	s := tr.snapshot()
	if s.Reads != 3 || s.Bytes != 3 {
		t.Fatalf("Reads/Bytes 期望 3/3,实际 %d/%d", s.Reads, s.Bytes)
	}
	if s.FirstByteWait != 3*time.Second {
		t.Fatalf("首字节等待期望 3s,实际 %v", s.FirstByteWait)
	}
	// gaps = [1200ms, 50ms]:max=1200ms;两样本近邻秩 p95 = max
	if s.GapMax != 1200*time.Millisecond {
		t.Fatalf("GapMax 期望 1.2s,实际 %v", s.GapMax)
	}
	if s.GapP95 != 1200*time.Millisecond {
		t.Fatalf("p95(两样本)期望等于 max 1.2s,实际 %v", s.GapP95)
	}
	if s.Elapsed != 4250*time.Millisecond {
		t.Fatalf("Elapsed 期望 4.25s,实际 %v", s.Elapsed)
	}
}

// TestUpstreamTimingReader_ZeroReads 锁定空流快照全零(不 panic、不除零)。
func TestUpstreamTimingReader_ZeroReads(t *testing.T) {
	clk := &fakeClock{cur: time.Now()}
	tr := newUpstreamTimingReaderWithClock(&chunkReader{}, clk.now)
	clk.advance(2 * time.Second)
	s := tr.snapshot()
	if s.Reads != 0 || s.Bytes != 0 || s.FirstByteWait != 0 || s.GapMax != 0 || s.GapP95 != 0 {
		t.Fatalf("空流快照应全零(Elapsed 除外),实际 %+v", s)
	}
	if s.Elapsed != 2*time.Second {
		t.Fatalf("Elapsed 期望 2s,实际 %v", s.Elapsed)
	}
}

// TestUpstreamTimingReader_P95NearestRank 锁定 p95 近邻秩口径:20 个递升 gap(1ms..20ms)。
func TestUpstreamTimingReader_P95NearestRank(t *testing.T) {
	clk := &fakeClock{cur: time.Now()}
	tr := newUpstreamTimingReaderWithClock(&chunkReader{chunks: mkChunks(21)}, clk.now)

	buf := make([]byte, 8)
	if _, err := tr.Read(buf); err != nil { // 首批,不产生 gap
		t.Fatalf("read1: %v", err)
	}
	for i := 1; i <= 20; i++ {
		clk.advance(time.Duration(i) * time.Millisecond) // gap_i = i ms
		if _, err := tr.Read(buf); err != nil {
			t.Fatalf("read%d: %v", i+1, err)
		}
	}
	s := tr.snapshot()
	if s.GapMax != 20*time.Millisecond {
		t.Fatalf("GapMax 期望 20ms,实际 %v", s.GapMax)
	}
	// 近邻秩:ceil(0.95*20)-1 = 18 → 排序后第 19 个 = 19ms
	if s.GapP95 != 19*time.Millisecond {
		t.Fatalf("p95 期望 19ms,实际 %v", s.GapP95)
	}
}

// TestUpstreamTimingReader_NilInnerSafe 锁定 nil inner 退化为空流,不 panic。
func TestUpstreamTimingReader_NilInnerSafe(t *testing.T) {
	tr := newUpstreamTimingReader(nil)
	buf := make([]byte, 16)
	n, err := tr.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("nil inner 应立返 EOF,实际 n=%d err=%v", n, err)
	}
	if s := tr.snapshot(); s.Reads != 0 {
		t.Fatalf("nil inner 快照 Reads 应 0,实际 %+v", s)
	}
}

// TestUpstreamTimingReader_GapSampleCap 锁定 gap 样本上限:超长流不无限涨内存,
// 触顶后 reads/bytes 照计、gaps 定格在 maxGapSamples。
func TestUpstreamTimingReader_GapSampleCap(t *testing.T) {
	const total = maxGapSamples + 5000
	tr := newUpstreamTimingReader(&chunkReader{chunks: mkChunks(total + 1)})
	buf := make([]byte, 8)
	for i := 0; i <= total; i++ {
		if _, err := tr.Read(buf); err != nil {
			t.Fatalf("read%d: %v", i, err)
		}
	}
	tr.mu.Lock()
	gapLen := len(tr.gaps)
	tr.mu.Unlock()
	if gapLen != maxGapSamples {
		t.Fatalf("gaps 应定格 %d,实际 %d", maxGapSamples, gapLen)
	}
	if s := tr.snapshot(); s.Reads != total+1 || s.Bytes != int64(total+1) {
		t.Fatalf("reads/bytes 应照计 %d,实际 %+v", total+1, s)
	}
}
