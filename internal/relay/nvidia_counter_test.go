package relay

import (
	"sync"
	"testing"
)

// nvidia_counter_test.go 覆盖 NVIDIA 号池每账号 1 分钟滑动窗口计数盘的核心行为：
// 基础计数、窗口过期回落、批量快照、并发安全(-race 路径)。
// 通过注入式时钟(now/windowMs)避免真实 sleep, 保持测试快速且确定性。

// newTestNvidiaStats 构造一个时钟可控的计数盘, 便于模拟窗口过期。
// nowFn 决定当前时间(UnixMilli), 默认用 t 所在基准点。
func newTestNvidiaStats(nowFn func() int64) *nvidiaReqStats {
	if nowFn == nil {
		var ts int64
		nowFn = func() int64 { return ts }
	}
	return &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs,
		now:      nowFn,
	}
}

func TestNvidiaStats_BasicTickAndCount(t *testing.T) {
	var ts int64 = 0
	st := &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs,
		now:      func() int64 { return ts },
	}

	if got := st.Tick("a"); got != 1 {
		t.Fatalf("first Tick: want 1, got %d", got)
	}
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("second Tick: want 2, got %d", got)
	}
	if got := st.Tick("a"); got != 3 {
		t.Fatalf("third Tick: want 3, got %d", got)
	}
	if got := st.Count("a"); got != 3 {
		t.Fatalf("Count after 3 ticks: want 3, got %d", got)
	}
	// 另一账号独立计数
	if got := st.Tick("b"); got != 1 {
		t.Fatalf("Tick b: want 1, got %d", got)
	}
	if got := st.Count("b"); got != 1 {
		t.Fatalf("Count b: want 1, got %d", got)
	}
	// 空 ID 不计入
	if got := st.Tick(""); got != 0 {
		t.Fatalf("Tick empty id: want 0, got %d", got)
	}
	if got := st.Count(""); got != 0 {
		t.Fatalf("Count empty id: want 0, got %d", got)
	}
}

func TestNvidiaStats_SlidingWindowExpire(t *testing.T) {
	var ts int64 = 0
	st := &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs, // 60000ms
		now:      func() int64 { return ts },
	}

	// t=0      : Tick 一次
	ts = 0
	if got := st.Tick("a"); got != 1 {
		t.Fatalf("t=0 Tick: want 1, got %d", got)
	}
	// t=30000  : 窗口内, 计数累加
	ts = 30 * 1000
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("t=30s Tick: want 2, got %d", got)
	}
	// t=65000  : t=0 那次已过期(0 < 65000-60000=5000), 只剩 t=30000 那次 + 本次 = 2
	ts = 65 * 1000
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("t=65s Tick: want 2(window expired first stamp, kept 2nd + new), got %d", got)
	}
	// Count 确认窗口内只剩两次(t=30000 与 t=65000)
	if got := st.Count("a"); got != 2 {
		t.Fatalf("t=65s Count: want 2, got %d", got)
	}
	// t=100000 : cutoff = 100000-60000 = 40000. t=30000 已过期, 但 t=65000 仍在窗口内,
	// 故 Tick 前窗口里剩 {65000} 共 1 个, 追加本次后 = 2。
	ts = 100 * 1000
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("t=100s Tick: want 2(kept 65s + new 100s), got %d", got)
	}
	// 再推进到 t=140000 : cutoff=80000, 65000 < 80000 过期, 100000 保留. 追加后 = 2
	ts = 140 * 1000
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("t=140s Tick: want 2(kept 100s + new 140s), got %d", got)
	}
	// 推进到 t=200000 : cutoff=140000, 严格小于才剔除 -> 100000 过期, 140000 == cutoff 保留. 追加后 = 2
	ts = 200 * 1000
	if got := st.Tick("a"); got != 2 {
		t.Fatalf("t=200s Tick: want 2(kept 140s + new 200s), got %d", got)
	}
	if got := st.Count("a"); got != 2 {
		t.Fatalf("t=200s Count: want 2, got %d", got)
	}
	// 用 Count 视角验证全过期: 推进到 t=300000 (cutoff=240000), 140000 与 200000 均 < 240000 全过期
	ts = 300 * 1000
	if got := st.Count("a"); got != 0 {
		t.Fatalf("t=300s Count(all expired): want 0, got %d", got)
	}
}

func TestNvidiaStats_SnapshotMin(t *testing.T) {
	var ts int64 = 0
	st := &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs,
		now:      func() int64 { return ts },
	}

	// a=1, b=3, c=2, d 未记录=0
	st.Tick("a")
	st.Tick("b")
	st.Tick("b")
	st.Tick("b")
	st.Tick("c")
	st.Tick("c")

	snap := st.SnapshotMin([]string{"a", "b", "c", "d", ""})
	if snap["a"] != 1 {
		t.Errorf("snap a: want 1, got %d", snap["a"])
	}
	if snap["b"] != 3 {
		t.Errorf("snap b: want 3, got %d", snap["b"])
	}
	if snap["c"] != 2 {
		t.Errorf("snap c: want 2, got %d", snap["c"])
	}
	if snap["d"] != 0 {
		t.Errorf("snap d(unrecorded): want 0, got %d", snap["d"])
	}
	if _, ok := snap[""]; ok {
		t.Errorf("empty id should be skipped in snapshot")
	}
}

// TestNvidiaStats_PickLeastCountIndex 验证最少计数优先并返回候选集合,
// 首轮全 0 时应包含全部下标(平局), 单个最少时返回该单个。
func TestNvidiaStats_PickLeastCountIndex(t *testing.T) {
	var ts int64 = 0
	st := &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs,
		now:      func() int64 { return ts },
	}

	// 首轮全 0: candidates 应为 [0,1,2]
	cands, minC := st.pickLeastCountIndex([]string{"a", "b", "c"})
	if minC != 0 {
		t.Fatalf("first round minCount: want 0, got %d", minC)
	}
	if len(cands) != 3 {
		t.Fatalf("first round candidates: want 3, got %d (%v)", len(cands), cands)
	}

	// 让 b 计数为 3, a/c 仍为 0 -> 最少是 a 和 c, candidates=[0,2]
	st.Tick("b")
	st.Tick("b")
	st.Tick("b")
	cands, minC = st.pickLeastCountIndex([]string{"a", "b", "c"})
	if minC != 0 {
		t.Fatalf("after b=3 minCount: want 0, got %d", minC)
	}
	if len(cands) != 2 || cands[0] != 0 || cands[1] != 2 {
		t.Fatalf("candidates should be [0,2], got %v", cands)
	}

	// 让 a=1, c=1 -> 最少是 a 和 c(均为 1, 小于 b=3), candidates=[0,2]
	st.Tick("a")
	st.Tick("c")
	cands, minC = st.pickLeastCountIndex([]string{"a", "b", "c"})
	if minC != 1 {
		t.Fatalf("a=1,c=1 minCount: want 1, got %d", minC)
	}
	if len(cands) != 2 || cands[0] != 0 || cands[1] != 2 {
		t.Fatalf("candidates should be [0,2], got %v", cands)
	}
}

func TestNvidiaStats_ConcurrentSafe(t *testing.T) {
	st := newNvidiaReqStats()
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = st.Tick("concurrent")
		}()
	}
	wg.Wait()
	got := st.Count("concurrent")
	if got != goroutines {
		t.Fatalf("concurrent Tick: want %d, got %d", goroutines, got)
	}
}
