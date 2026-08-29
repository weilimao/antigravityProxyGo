package stats

import (
	"sync"
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

// stats_persist_coupling_test.go: 锁定「UI 通知与落盘节拍解耦」+「深拷贝落盘与并发写入无死锁/无 panic」两条不变式。
//
// 背景:
//   1. scheduleSave 回调在 timer 到点后先 SaveToDisk 再入 onPayloadUpdate,导致
//      拉长落盘节拍会直接让 UI 停更(过往"落盘驱动 UI"的耦合);改为 Unlock 后
//      直接通知 onPayloadUpdate,UI 立即刷新,与磁盘节拍独立。
//   2. usage/packet 曾把 UsageState / pc.packets 的引用直接交出去做 Marshal,
//      数据仍指向共享底层 map/slice;SaveToDisk 释放锁后另一 goroutine 同时
//      RecordUsage / SavePacket 就跑出 race/致 panic。现改深拷贝,测试加并发锁。
//   3. stats 落盘节拍由 3s 提到 10s,Prune 从此周期解耦到独立 5min goroutine,
//      对外行为仅崩溃丢窗从 3s → 10s(聚合镜像级丢失,明细由 request_logs 即时落库兜底)。

// TestTrackRequest_OnPayloadUpdateFiresImmediately 验证 TrackRequest 返回时
// onPayloadUpdate 已被调用(不等 10s 落盘节拍)且未死锁/无 panic。
// 这是「10s 落盘节拍」语义下指标卡仍秒级刷新的关键安全锁。
func TestTrackRequest_OnPayloadUpdateFiresImmediately(t *testing.T) {
	dir := t.TempDir()
	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	var mu sync.Mutex
	called := 0
	cb := make(chan struct{}, 8)
	tr.SetOnPayloadUpdate(func() {
		mu.Lock()
		called++
		mu.Unlock()
		select {
		case cb <- struct{}{}:
		default:
		}
	})

	tr.TrackRequest("im-mediate-model", 10, 5, 0)

	select {
	case <-cb:
		// 解锁后立即通知,与 10s 落盘节拍完全解耦
	case <-time.After(500 * time.Millisecond):
		t.Fatal("onPayloadUpdate 未被立即触发: UI 通知仍被落盘节拍阻塞(耦合回归)")
	}

	mu.Lock()
	got := called
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected onPayloadUpdate called exactly once, got %d", got)
	}
}

// TestUsage_ConcurrentRecordAndSave 验证 usage RecordUsage 与 SaveToDisk 并发安全。
// 新阀:track 大量并发读写(8 producer × 200 次)与手动 SaveToDisk 交替,
// 任何 race/底层映射共享都会在这个压力下扩出来(允许无 -race 时偶发误报)。
func TestUsage_ConcurrentRecordAndSave(t *testing.T) {
	dir := t.TempDir()
	ut := NewUsageTracker(pricing.NewManager())
	ut.Init(dir)

	const producers = 8
	const rounds = 200

	done := make(chan struct{}, producers)
	for p := 0; p < producers; p++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			acc := &AccountMeta{
				ID:       "acc-conc",
				Email:    "conc@x.com",
				Provider: "nvidia",
			}
			for i := 0; i < rounds; i++ {
				ut.RecordUsage(UsageSample{
					ModelName:   "conc-model",
					InTokens:    10,
					OutTokens:   5,
					Timestamp:   "2026-01-01T00:00:00+08:00",
					Account:     acc,
				})
			}
		}(p)
	}

	// 与生产端交织:泻入持久化线程从 SaveToDisk 深挖 usage.json 快照
	for i := 0; i < 50; i++ {
		ut.SaveToDisk()
		time.Sleep(5 * time.Millisecond)
	}

	for i := 0; i < producers; i++ {
		<-done
	}
}

// TestPacket_ConcurrentSaveAndRecord 验证 packet SavePacket 与 SaveToDisk 并发安全。
// 重构前 SaveToDisk 拿读锁后把内部切片头交出去,释放锁后 Marshal;
// SavePacket 如果再 append 同一底层数组,会造成脏快照甚至 panic。
func TestPacket_ConcurrentSaveAndRecord(t *testing.T) {
	dir := t.TempDir()
	pc := NewPacketCapturer(nil, nil, func() bool { return true })
	pc.Init(dir)

	const producers = 4
	const rounds = 100

	done := make(chan struct{}, producers)
	for p := 0; p < producers; p++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < rounds; i++ {
				pc.SavePacket("POST", "conc.test", "/v1/test",
					map[string][]string{"Content-Type": {"application/json"}},
					[]byte(`{"req":1}`),
					map[string][]string{"Content-Type": {"application/json"}},
					[]byte(`{"res":1}`),
					200,
				)
			}
		}(p)
	}

	for i := 0; i < 40; i++ {
		pc.SaveToDisk()
		time.Sleep(3 * time.Millisecond)
	}

	for i := 0; i < producers; i++ {
		<-done
	}

	// 快照语义:最终内存最近 50 条,文件落盘不应阻塞主流程
	pkts := pc.GetPackets()
	if len(pkts) == 0 {
		t.Fatal("expected packets to be captured during concurrent save")
	}
	if len(pkts) > 50 {
		t.Fatalf("expected ≤50 packets after capture+truncate, got %d", len(pkts))
	}
}
