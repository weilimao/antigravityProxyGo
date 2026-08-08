package account

import "sync"

// concurrency.go: 单账号在途并发计数器组件。
//
// 设计动机:各号池选号长期只按「冷却池 + LB 模式」分发,不感知「在途并发」,
// 多个并发请求会同时钉死同一账号(尤其 sticky 模式同会话粘连),瞬时撞上游 429/5xx 后被拉黑,
// 触发整池雪崩。本组件为四条号池链路提供「单账号最大并发数」语义:请求打到某账号起算占 1 个槽,
// 本次请求结束(上游响应体读尽/流结束/失败取消)即释放;选号时先把超过上限的账号过滤掉,
// 全满则取「当前并发最少的号」允许超额并打日志降级,绝不硬拒 503。
//
// 实例归属:由 Manager 单实例持有(NewManager 初始化),relay 的 APICompatHandler.accountMgr
// 与 proxy 的 ProxyHandler.accountMgr 本就同一引用,天然共享同一份在途计数。
//
// 并发安全:独立 sync.Mutex,不嵌套 Manager.RWMutex。选号热路径先在 Manager.RLock 内取回
// available 切片(释放 Manager.RLock)后才调用 Acquire/Filter,两锁不嵌套,无死锁风险。
// counts 为纯内存易失:重启清零即真实状态(进程死亡无在途请求),正确语义,无需持久化。
//
// floor 0 兜底:Release 对已归零或未知 id 计数取 max(0, v-1),防止一次 release 多减导致负偏
// (panic 风险路径下 acquire/release 配对偶发错位时的最后防线)。
type Concurrency struct {
	mu     sync.Mutex
	counts map[string]int // accountID -> 在途并发数
}

// NewConcurrency 构造一个空的在途并发计数器。counts 延迟到首次 Acquire 才分配也无妨,
// 但这里预分配避免热路径下 map 第一次写入触发扩容抖动。
func NewConcurrency() *Concurrency {
	return &Concurrency{counts: make(map[string]int)}
}

// Acquire 为该账号占用一个在途并发槽(counts[id]++)。无条件占用:调用方应在选号确认后调用。
func (c *Concurrency) Acquire(id string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	c.counts[id]++
	c.mu.Unlock()
}

// Release 释放该账号一个在途并发槽。floor 0 防负:即便双重 release 或未知 id 也绝不产生负计数。
// 负计数会导致 PickUnderLimit/LeastLoaded 误把溢出账号当最闲,从源头杜绝。
func (c *Concurrency) Release(id string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	if v, ok := c.counts[id]; ok && v > 0 {
		c.counts[id] = v - 1
		if c.counts[id] == 0 {
			// 归零即删键,避免 map 无界增长(账号曾用后又删除时残留 0 项);
			// 同时让 PickUnderLimit 内 count<limit 判定对未占用账号取 0 命中,无需特殊兜底。
			delete(c.counts, id)
		}
	}
	c.mu.Unlock()
}

// Count 返回该账号当前在途并发数(只读,nil 安全返回 0)。
// 供调试/日志/测试断言使用,不参与选号热路径决策。
func (c *Concurrency) Count(id string) int {
	if c == nil || id == "" {
		return 0
	}
	c.mu.Lock()
	v := c.counts[id]
	c.mu.Unlock()
	return v
}

// PickUnderLimit 从 candidates 返回在途并发数 < limit 的子集,保持原序。
// limit<=0(含 0/负数,语义=未配置回退默认时的哨兵)视作「不限」,原样返回全部候选,
// 与 Get*MaxConcurrency 的「<=0 视作默认 10」在调用方已规整为正数后,本哨兵仅作防御兜底。
// 返回新切片(可能为空),不修改入参切片;入参为空或 nil 返回 nil。
func (c *Concurrency) PickUnderLimit(candidates []*Account, limit int) []*Account {
	if c == nil || len(candidates) == 0 {
		return nil
	}
	if limit <= 0 {
		// 不限:原样返回全部(copy 避免调用方误改原切片)。
		out := make([]*Account, len(candidates))
		copy(out, candidates)
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*Account
	for _, a := range candidates {
		if a == nil {
			continue
		}
		if c.counts[a.ID] < limit {
			out = append(out, a)
		}
	}
	return out
}

// LeastLoaded 返回 candidates 中当前在途并发数最小者;并列取首个(保持候选原序的稳定选择,
// 对齐 nvidiaStats.pickLeastCountIndex 最少计数优先哲学)。空入参或全 nil 返回 nil。
// 用途:PickUnderLimit 返回空(全满)时由调用方做「超额降级」——挑并发最少的号允许超额,
// 并日志标注,绝不硬拒 503。
func (c *Concurrency) LeastLoaded(candidates []*Account) *Account {
	if c == nil || len(candidates) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var best *Account
	bestCount := 0
	for _, a := range candidates {
		if a == nil {
			continue
		}
		cnt := c.counts[a.ID]
		if best == nil || cnt < bestCount {
			best = a
			bestCount = cnt
		}
	}
	return best
}
