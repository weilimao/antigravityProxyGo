package relay

import (
	"sync"
	"time"
)

// nvidia_counter.go 实现 NVIDIA 号池"每账号最近 1 分钟请求计数盘"。
//
// 背景：原 round-robin 纯游标取模在突发高并发洪流下未必均匀，某账号可能在 1 分钟
// 窗口内承担 >40 次请求导致必然 429。本计数盘为选号提供"最近 1 分钟实际请求次数"
// 信号，选号时优先挑计数最少的账号作为起始游标，把突发洪流摊到负载最轻的账号上。
//
// 设计要点：
//   - 纯内存易失，不持久化、不落盘、不触发任何回调，重启即清零，符合"最近 1 分钟"语义。
//   - 滑动窗口实现：每账号维护一个时间戳切片，Tick/Count/SnapshotMin 均惰性清理
//     窗口外(早于 now-windowMs)的旧时间戳，避免后台清理协程。
//   - 并发安全用 sync.Mutex：NVIDIA 链路 QPS 远低于 token 校验等热路径，mutex 足够；
//     避免 sync.Map 的弱一致性导致选号抖动。锁粒度仅限 NVIDIA 选号，不与 account.Manager
//     全局锁、sessionRouter 锁嵌套。
//   - now 可注入便于测试模拟窗口过期；生产路径使用 time.Now().UnixMilli()。

// nvidiaStatsWindowMs 是滑动窗口长度：1 分钟(60000 毫秒)。
const nvidiaStatsWindowMs int64 = 60 * 1000

// nvidiaReqStats 是 NVIDIA 号池每账号最近 1 分钟请求计数的滑动窗口记录盘。
type nvidiaReqStats struct {
	mu       sync.Mutex
	windows  map[string][]int64 // accountID -> 已发生的请求时间戳(UnixMilli), 升序追加
	windowMs int64             // 窗口长度(毫秒), 默认 nvidiaStatsWindowMs, 测试可注入
	now      func() int64      // 当前时间戳(UnixMilli), 默认 time.Now().UnixMilli, 测试可注入
}

// newNvidiaReqStats 构造一个使用生产默认窗口与时钟的 NVIDIA 请求计数盘。
func newNvidiaReqStats() *nvidiaReqStats {
	return &nvidiaReqStats{
		windows:  make(map[string][]int64),
		windowMs: nvidiaStatsWindowMs,
		now:      time.Now().UnixMilli,
	}
}

// expireLocked 清理指定账号窗口外的旧时间戳。调用者必须持有 mu。
// 复用底层数组，仅移动有效前缀回数组头部并截断尾部，避免反复分配。
func (s *nvidiaReqStats) expireLocked(id string, nowMs int64) {
	stamps := s.windows[id]
	if len(stamps) == 0 {
		return
	}
	cutoff := nowMs - s.windowMs
	// stamps 升序追加, 找到第一个 >= cutoff(在窗口内)的位置
	idx := 0
	for idx < len(stamps) && stamps[idx] < cutoff {
		idx++
	}
	if idx == 0 {
		return
	}
	// 有效时间戳前移到数组头部
	valid := stamps[idx:]
	copy(stamps, valid)
	s.windows[id] = stamps[:len(valid)]
}

// Tick 记录一次该账号的请求发生时刻，并返回该账号当前窗口内(含本次)的请求计数。
func (s *nvidiaReqStats) Tick(id string) int64 {
	if id == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := s.now()
	s.expireLocked(id, nowMs)
	s.windows[id] = append(s.windows[id], nowMs)
	return int64(len(s.windows[id]))
}

// Count 只读返回该账号当前窗口内的请求计数(惰性清理, 不追加新记录)。
func (s *nvidiaReqStats) Count(id string) int64 {
	if id == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(id, s.now())
	return int64(len(s.windows[id]))
}

// SnapshotMin 批量返回多个账号当前窗口内的请求计数, 未记录的账号返回 0。
// 供选号排序使用, 一次锁内完成惰性清理, 避免逐账号加锁抖动。
func (s *nvidiaReqStats) SnapshotMin(ids []string) map[string]int64 {
	result := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return result
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := s.now()
	for _, id := range ids {
		if id == "" {
			continue
		}
		s.expireLocked(id, nowMs)
		result[id] = int64(len(s.windows[id]))
	}
	return result
}

// pickLeastCountIndex 在 active 列表中按"最少 1 分钟计数优先"返回起始候选下标集合。
// 当所有账号计数相同时(含首轮全 0), 返回全部下标, 由调用方用全局游标取模打破平局。
// 返回值 candidates 至少含 1 个下标(入参非空时)。
//
// 注意:本函数调用 SnapshotMin 期间持有 mu, 但 mu 仅限 NVIDIA 计数盘, 与外部选号锁无嵌套。
func (s *nvidiaReqStats) pickLeastCountIndex(accounts []string) (candidates []int, minCount int64) {
	snap := s.SnapshotMin(accounts)
	minCount = -1
	for i, id := range accounts {
		if id == "" {
			continue
		}
		c := snap[id]
		if minCount < 0 || c < minCount {
			minCount = c
			candidates = candidates[:0]
			candidates = append(candidates, i)
		} else if c == minCount {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 && len(accounts) > 0 {
		// 全部 id 为空的兜底:退化到全部下标, 避免选号空指针
		candidates = make([]int, len(accounts))
		for i := range accounts {
			candidates[i] = i
		}
		minCount = 0
	}
	return candidates, minCount
}
