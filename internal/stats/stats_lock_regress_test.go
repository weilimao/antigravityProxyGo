package stats

import (
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

// stats_lock_regress_test.go: 回归锁定「SaveToDisk 不得持锁执行后续 IO/DB 逻辑」。
//
// 背景事故: SaveToDisk 曾经历过一次重构遗漏 t.RUnlock() —— 深拷贝后锁未释放,
// 3s 防抖定时器到点执行完 SaveToDisk 即泄漏读锁; 之后所有 TrackRequest*(写锁)
// 与 GetPayload/GetPayloadSimplified(读锁) 永久阻塞, 表现为"请求进不来 / 仪表盘全停更"。
// 本测试复现该时序: 第一次 TrackRequest → 等定时节拍落盘 → 第二次 TrackRequest 必须立即完成。

// TestSaveToDisk_DoesNotHoldLock 验证落盘后锁处于自由态: 第二次写操作在合理时间内办结。
func TestSaveToDisk_DoesNotHoldLock(t *testing.T) {
	dir := t.TempDir()
	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	// 第一次请求: 触发 scheduleSave, 3s 后定时器执行 SaveToDisk
	tr.TrackRequest("lock-model", 10, 5, 0)

	// 等待防抖节拍(3s) + SaveToDisk 执行完毕的宽裕余量
	time.Sleep(3500 * time.Millisecond)

	done := make(chan struct{}, 1)
	go func() {
		// 若 SaveToDisk 泄漏了读锁, 这里的写锁将永远拿不到
		tr.TrackRequest("lock-model", 20, 10, 0)
		done <- struct{}{}
	}()

	select {
	case <-done:
		// 通过: 锁已正确释放
	case <-time.After(2 * time.Second):
		t.Fatal("SaveToDisk 之后 TrackRequest 阻塞超过 2s: 读锁未释放(死锁回归)")
	}

	// 与写对称: 读路径也必须通畅(GetPayload 拿读锁)
	readDone := make(chan struct{}, 1)
	go func() {
		_ = tr.GetPayload(nil)
		readDone <- struct{}{}
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SaveToDisk 之后 GetPayload 阻塞超过 2s: 读锁未释放(死锁回归)")
	}
}
