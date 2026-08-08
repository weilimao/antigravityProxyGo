package account

import (
	"sync"
	"testing"
)

// newTestAccounts 构造 N 个最小可用账号(仅 ID 必填),供计数器过滤/排序测试用。
func newTestAccounts(ids ...string) []*Account {
	out := make([]*Account, 0, len(ids))
	for _, id := range ids {
		out = append(out, &Account{ID: id})
	}
	return out
}

func TestConcurrency_AcquireReleaseFloor(t *testing.T) {
	c := NewConcurrency()
	c.Acquire("a")
	c.Acquire("a")
	if got := c.Count("a"); got != 2 {
		t.Fatalf("Count(a) after 2 Acquire = %d, want 2", got)
	}
	c.Release("a")
	c.Release("a")
	if got := c.Count("a"); got != 0 {
		t.Fatalf("Count(a) after 2 Release = %d, want 0", got)
	}
	// 重复 release 不得产生负计数(floor 0 兜底)。
	c.Release("a")
	c.Release("a")
	if got := c.Count("a"); got != 0 {
		t.Fatalf("Count(a) after extra Release = %d, want 0 (floor guard)", got)
	}
	// 未知 id release 不应 panic 也不应写入 0 键。
	c.Release("never-acquired")
	if _, ok := c.counts["never-acquired"]; ok {
		t.Fatalf("release of unknown id leaked a 0-entry into counts map")
	}
}

func TestConcurrency_NilAndEmptySafe(t *testing.T) {
	var c *Concurrency
	// 所有方法对 nil/空串零 panic,保证 Manager 转发 nil 防御与未注入测试场景兼容。
	if got := c.Count(""); got != 0 {
		t.Fatalf("nil Count(\"\") = %d, want 0", got)
	}
	if got := c.PickUnderLimit(newTestAccounts("a", "b"), 5); got != nil {
		t.Fatalf("nil PickUnderLimit = %v, want nil", got)
	}
	if got := c.LeastLoaded(newTestAccounts("a")); got != nil {
		t.Fatalf("nil LeastLoaded = %v, want nil", got)
	}
	c2 := NewConcurrency()
	c2.Acquire("")
	c2.Release("")
	if got := c2.PickUnderLimit(nil, 5); got != nil {
		t.Fatalf("PickUnderLimit(nil,...) = %v, want nil", got)
	}
	if got := c2.LeastLoaded(nil); got != nil {
		t.Fatalf("LeastLoaded(nil) = %v, want nil", got)
	}
}

func TestConcurrency_PickUnderLimitOrderAndLimit(t *testing.T) {
	c := NewConcurrency()
	// a=3, b=1, c=0;limit=2 → 仅 b(1<2)与 c(0<2)入选,a 被过滤。
	// 保持原序:b 在 c 前则结果 [b, c]。
	accs := newTestAccounts("a", "b", "c")
	c.Acquire("a")
	c.Acquire("a")
	c.Acquire("a")
	c.Acquire("b")
	got := c.PickUnderLimit(accs, 2)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("PickUnderLimit = %v, want [b c] in original order", ids(got))
	}
	// 不修改入参切片。
	if accs[0].ID != "a" || accs[2].ID != "c" {
		t.Fatalf("PickUnderLimit mutated input slice: %v", ids(accs))
	}
	// 全满(limit=1,所有 count>=1 的过滤)→ 空切片;但 c count=0 仍入选。
	c.Release("b") // 现 b=0
	gotFull := c.PickUnderLimit(accs, 1)
	if len(gotFull) != 2 || gotFull[0].ID != "b" || gotFull[1].ID != "c" {
		t.Fatalf("PickUnderLimit limit=1 after b released = %v, want [b c]", ids(gotFull))
	}
	// limit<=0 视作不限:原样返回全部(含 a)。
	gotAll := c.PickUnderLimit(accs, 0)
	if len(gotAll) != 3 {
		t.Fatalf("PickUnderLimit limit=0 = %d, want 3 (unlimited)", len(gotAll))
	}
	// 跳过 nil 元素。
	accsWithNil := []*Account{{ID: "x"}, nil, {ID: "y"}}
	c.Release("a") // 清空 a 计数,使下面只看 x/y
	gotSkip := c.PickUnderLimit(accsWithNil, 5)
	if len(gotSkip) != 2 || gotSkip[0].ID != "x" || gotSkip[1].ID != "y" {
		t.Fatalf("PickUnderLimit with nil elem = %v, want [x y]", ids(gotSkip))
	}
}

func TestConcurrency_LeastLoadedTies(t *testing.T) {
	c := NewConcurrency()
	// 全 0 并列时取首个(候选原序稳定)。
	accs := newTestAccounts("a", "b", "c")
	if got := c.LeastLoaded(accs); got.ID != "a" {
		t.Fatalf("LeastLoaded all-zero = %s, want a (first in tie)", got.ID)
	}
	// a 加 1 → b、c 仍 0 并列,b 列于 c 前 → b。
	c.Acquire("a")
	if got := c.LeastLoaded(accs); got.ID != "b" {
		t.Fatalf("LeastLoaded after a+1 = %s, want b (first among zero-tie)", got.ID)
	}
	// b 再加 1 → 仅 c 仍 0 → c(唯一最闲)。
	c.Acquire("b")
	if got := c.LeastLoaded(accs); got.ID != "c" {
		t.Fatalf("LeastLoaded after a+1 b+1 = %s, want c (only zero)", got.ID)
	}
	// 全部加 2 后并列 → 取首个 a(并列稳定,候选原序)。
	c.Acquire("a")
	c.Acquire("b")
	c.Acquire("c")
	c.Acquire("c")
	// 现 a=2,b=2,c=2。
	if got := c.LeastLoaded(accs); got.ID != "a" {
		t.Fatalf("LeastLoaded tie a/b/c at 2 = %s, want a (first in tie)", got.ID)
	}
	// 跳过 nil;首个非 nil 即最闲。
	if got := c.LeastLoaded([]*Account{nil, {ID: "x"}}); got == nil || got.ID != "x" {
		t.Fatalf("LeastLoaded with leading nil = %v, want x", got)
	}
}

func TestConcurrency_ConcurrentAcquireRelease(t *testing.T) {
	// 100 goroutine × 1000 iter 随机 acquire/release:终态所有 counts 必须归 0。
	// 验证 floor 0 兜底下并发配对不产生负偏,且归零即删键(counts 全空)。
	c := NewConcurrency()
	const g = 100
	const iter = 1000
	var wg sync.WaitGroup
	wg.Add(g)
	for i := 0; i < g; i++ {
		go func(seed int) {
			defer wg.Done()
			// 每个 goroutine 的「净占用」必须为 0:先 acquire 一次再 release 一次。
			// 用三个固定 id 做竞争,seed 偏移避免全命中同一段临界区。
			ids := []string{"a", "b", "c"}
			for j := 0; j < iter; j++ {
				id := ids[(seed+j)%3]
				c.Acquire(id)
				c.Release(id)
			}
		}(i)
	}
	wg.Wait()
	for _, id := range []string{"a", "b", "c"} {
		if got := c.Count(id); got != 0 {
			t.Fatalf("Count(%s) after balanced concurrent ops = %d, want 0", id, got)
		}
	}
	if len(c.counts) != 0 {
		t.Fatalf("counts map not empty after all released: %v", c.counts)
	}
}

// ids 是测试辅助:把账号切片拍平成 id 字符串切片,断言失败信息可读。
func ids(accs []*Account) []string {
	out := make([]string, 0, len(accs))
	for _, a := range accs {
		if a == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, a.ID)
	}
	return out
}
