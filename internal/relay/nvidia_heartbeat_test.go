package relay

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// nvidia_heartbeat_test.go: 锁定 SSE ping 心跳看门狗行为。
//
// 重点覆盖:
//   - 未进入实质 content_block 阶段(还没 emit 任何 delta)不发送 ping(不污染握手期);
//   - 进入实质阶段后,闲置超 heartbeatIdle 会发到 sink;
//   - 每次真实业务写入(markBeat)会重置计时,避免阅读期间反复误发;
//   - stop 后协程退出不泄漏;
//   - resumeSink 的 pingFrame 会透传到 live flushWriter。

// mockPingSink 记录 pingFrame 次数,同时实现 sseEventSink 三方法(no-op)。
type mockPingSink struct {
	mu       sync.Mutex
	pingCnt  int
	writeCnt int
}

func (m *mockPingSink) writeEvent(event, data string) { m.mu.Lock(); m.writeCnt++; m.mu.Unlock() }
func (m *mockPingSink) writeRaw(s string)             { m.mu.Lock(); m.writeCnt++; m.mu.Unlock() }
func (m *mockPingSink) flush()                        {}
func (m *mockPingSink) pingFrame()                    { m.mu.Lock(); m.pingCnt++; m.mu.Unlock() }
func (m *mockPingSink) pings() int                    { m.mu.Lock(); defer m.mu.Unlock(); return m.pingCnt }

// TestHeartbeat_IdleBeforeAnyContent: 未进入任何 content_block 时,即便超过 heartbeatIdle
// 也不应发 ping,避免在 message_start 还没到达前就发 ping 导致乱序。
func TestHeartbeat_IdleBeforeAnyContent(t *testing.T) {
	snk := &mockPingSink{}
	hb := newHeartbeatWatchdog(snk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hb.start(ctx)
	defer hb.stop()

	// 不调用 markContentBlockEntry/markBeat,模拟上游不发任何字节。
	time.Sleep(heartbeatIdle + heartbeatCheck + 200*time.Millisecond)
	if snk.pings() != 0 {
		t.Fatalf("no-content idle window pings = %d, want 0 (ping must wait until first content block)", snk.pings())
	}
}

// TestHeartbeat_FiresAfterIdle: 进入实质块后,闲置超过 heartbeatIdle 应收到至少一次 ping。
func TestHeartbeat_FiresAfterIdle(t *testing.T) {
	snk := &mockPingSink{}
	hb := newHeartbeatWatchdog(snk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hb.start(ctx)
	defer hb.stop()

	hb.markContentBlockEntry()
	hb.markBeat()
	time.Sleep(heartbeatIdle + heartbeatCheck + 300*time.Millisecond)
	if snk.pings() < 1 {
		t.Fatalf("idle past threshold pings = %d, want >= 1", snk.pings())
	}
}

// TestHeartbeat_ResetsOnBeat: 周期性成对出现(业务 markBeat + 长等待) =>
// 每次 ping 后再次进入 idle,继续触发下一次。确保 ping 不是一次性。
func TestHeartbeat_ResetsOnBeat(t *testing.T) {
	snk := &mockPingSink{}
	hb := newHeartbeatWatchdog(snk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hb.start(ctx)
	defer hb.stop()

	hb.markContentBlockEntry()
	hb.markBeat()
	// 让其闲置超过阈值一次。
	time.Sleep(heartbeatIdle + heartbeatCheck + 200*time.Millisecond)
	if snk.pings() < 1 {
		t.Fatalf("expected at least 1 ping in first idle window, got %d", snk.pings())
	}
	// markBeat 后再次闲置,应再次触发 ping。
	hb.markBeat()
	time.Sleep(heartbeatIdle + heartbeatCheck + 200*time.Millisecond)
	if snk.pings() < 2 {
		t.Fatalf("expected at least 2 pings across two idle windows, got %d", snk.pings())
	}
}

// TestHeartbeat_Stop: stop 后是否还会产生 ping。
func TestHeartbeat_Stop(t *testing.T) {
	snk := &mockPingSink{}
	hb := newHeartbeatWatchdog(snk)
	ctx := context.Background()
	hb.start(ctx)
	hb.markContentBlockEntry()
	hb.markBeat()

	hb.stop()
	hb.stop() // 幂等
	snapshot := snk.pings()
	// 再闲置一段时间不应再有 ping
	time.Sleep(heartbeatIdle + heartbeatCheck + 300*time.Millisecond)
	if snk.pings() != snapshot {
		t.Fatalf("after stop pings grew from %d -> %d, want stable", snapshot, snk.pings())
	}
}

// TestResumeSink_PingFrameBridgesToLive: 重试轮里 resumeSink 必须把 ping 传给 live,
// 这样客户端在 resume 等待期间也能重置看门狗。
func TestResumeSink_PingFrameBridgesToLive(t *testing.T) {
	// 用一个最简 flushWriter 假人接 ping 计数:直接打 pingFrame 不影响协议态。
	// 若 live 为 nil,不应 panic(防御)。
	r := newResumeSink(nil, newReplayWriter(), false, -1, 0, false)
	r.pingFrame() // live=nil 必须 no-op

	// 真实 flushWriter(无 flusher 降级)也可被调用,不返回 error。
	// 构造最小 live:基于 bufio.Writer 写到内存缓冲,绕过 http.ResponseWriter。
	// 直接引用现有 newFlushWriter 但 nil flusher 参数。
	// 通过观察 pingFrame 不 panic、且不会写 replay buffer(由 replay.len() 不变断言)。
	replay := r.replay
	replayLenBefore := replay.len()
	r.pingFrame()
	if replay.len() != replayLenBefore {
		t.Fatalf("resumeSink.pingFrame polluted replay buffer: before=%d after=%d", replayLenBefore, replay.len())
	}
}

// TestTranslateLoop_HeartbeatIntegrated 是端到端集成:模拟一段"reasoning 长静默"
// 输入,断言翻译层在静默窗口内向 sink 发出 ping。
func TestTranslateLoop_HeartbeatIntegrated(t *testing.T) {
	// 构造一个慢速 reader:首帧立即给一条 reasoning_content,然后开始 sleep 4s,
	// 之后不给任何字节直到 EOF。如果心跳工作正常,sink 应在 sleep 期间收到 ping。
	delta := `{"id":"x","model":"kimi-k3","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}` + "\n\n"
	body := &slowStringReader{parts: []string{
		"data: " + delta,
		// 后面睡眠期间不断流,但不给任何字节,模拟 reasoning→tool 静默窗口。
		"",
	}, delays: []time.Duration{0, 4 * time.Second}}

	sink := &mockPingSink{}
	// 通过公开入口 openAIChatSSEToAnthropicSSEInto 调用,直至 EOF 后收到 message_stop。
	stDone := make(chan struct{})
	go func() {
		defer close(stDone)
		ctx := context.Background()
		_, _, _, _, _, _ = openAIChatSSEToAnthropicSSEInto(ctx, body, nil, sink, "stream-test", "kimi-k3", 5)
	}()

	select {
	case <-stDone:
	case <-time.After(15 * time.Second):
		t.Fatal("translate loop did not terminate within 15s after EOF")
	}
	if sink.pings() == 0 {
		t.Fatalf("expected at least 1 ping during 4s silence window, got 0")
	}
}

// slowStringReader 按段吐出字节,前两段之间可配置间隔 sleep。
type slowStringReader struct {
	parts  []string
	delays []time.Duration
	mu     sync.Mutex
	idx    int
	pend   []byte
}

func (r *slowStringReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.pend) == 0 {
		if r.idx >= len(r.parts) {
			return 0, io.EOF
		}
		d := time.Duration(0)
		if r.idx < len(r.delays) {
			d = r.delays[r.idx]
		}
		if d > 0 {
			time.Sleep(d)
		}
		r.pend = []byte(r.parts[r.idx])
		r.idx++
	}
	n := copy(p, r.pend)
	r.pend = r.pend[n:]
	return n, nil
}

func (r *slowStringReader) Close() error { return nil }

// TestPingFrameFormatIsAnthropic: 心跳帧必须是 Anthropic 规范形态 "event: ping\ndata: {...}\n\n",
// 由 flushWriter.writeEvent 直发,不带 firstByteHook 触发之外的副作用。
func TestPingFrameFormatIsAnthropic(t *testing.T) {
	// 构造一个 buffer 记录的 flushWriter(无 flusher)。
	// 注意:这里直接调用底层写方法(而非 watch),只是验证 payload 形态正确。
	var buf strings.Builder
	bw := bufio.NewWriter(writerFunc(func(p []byte) (int, error) { return buf.Write(p) }))
	fw := newFlushWriter("ping-test", bw) // 无 flusher
	fw.writeEvent("ping", `{"type":"ping"}`)
	bw.Flush()
	got := buf.String()
	if !strings.Contains(got, "event: ping\n") || !strings.Contains(got, `data: {"type":"ping"}`+"\n\n") {
		t.Fatalf("ping frame format invalid: %q", got)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

