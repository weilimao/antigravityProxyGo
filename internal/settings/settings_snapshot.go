package settings

import "strings"

// settings_snapshot.go: 「上次远端拉取到的上游模型全集」快照读写。
//
// 用途:前端两处「获取上游模型」入口(NVIDIA 专属弹窗 + 中继模型映射面板)在每次拉取成功后,
// 把本次远端全集落盘为 snapshot,下次再拉取时与本次全集做 diff,提示本轮「新增」模型。
// 「失效」判定不依赖 snapshot,而由前端直接用「已选/已配 − 本次远端全集」现场算出,故本文件只管 snapshot 自身。
//
// 锁语义:均在 setSetting 泛型写锁内完成 trim/去重/副本,与既有 []string/map 字段(NvidiaPreferredModels、
// otherMaxConcurrency)同口径,无新增竞态。Get 一律返回副本,防外部改动内存态。

// GetNvidiaPreferredModelsSnapshot 返回上次成功拉取的 NVIDIA 上游模型全集快照(副本)。
// nil/空时返回空切片(非 nil),与 GetNvidiaPreferredModels 口径一致。
func (m *Manager) GetNvidiaPreferredModelsSnapshot() []string {
	return getSetting(m, func(c *Config) []string {
		if c.NvidiaPreferredModelsSnapshot == nil {
			return []string{}
		}
		out := make([]string, len(c.NvidiaPreferredModelsSnapshot))
		copy(out, c.NvidiaPreferredModelsSnapshot)
		return out
	})
}

// SetNvidiaPreferredModelsSnapshot 落盘本次远端全集(整体覆盖,非累积语义)。
// 入参做 trim + 去重规整,与 SetNvidiaPreferredModels 同款口径,避免脏数据(空串/重复)污染快照。
func (m *Manager) SetNvidiaPreferredModelsSnapshot(val []string) error {
	return setSetting(m, func(c *Config, v []string) {
		seen := make(map[string]bool)
		cleaned := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			cleaned = append(cleaned, item)
		}
		c.NvidiaPreferredModelsSnapshot = cleaned
	}, val)
}

// GetRelayChannelModelsSnapshot 返回按 channel 维度的全局上游模型全集快照(深拷贝副本)。
// nil/空时返回空 map(非 nil);调用方按 channel 取值前应判 ok,未命中的 channel 返回空切片语义。
// 返回深拷贝保证外部修改不影响内存态(map 不可简单 copy,需逐键复制切片)。
func (m *Manager) GetRelayChannelModelsSnapshot() map[string][]string {
	return getSetting(m, func(c *Config) map[string][]string {
		out := make(map[string][]string, len(c.RelayChannelModelsSnapshot))
		for ch, list := range c.RelayChannelModelsSnapshot {
			cp := make([]string, len(list))
			copy(cp, list)
			out[ch] = cp
		}
		return out
	})
}

// SetRelayChannelModelsSnapshot 落盘某 channel 的本次远端全集(整体覆盖该键,非累积;不影响其它 channel)。
// channel 做 trim + lowercase 规整(键规范化,与其他号池 map 字段同口径);val 做 trim + 去重。
// channel 为空时为 no-op(避免写空键污染)。channel 经闭包从外层捕获,T 仍取 []string 以复用 setSetting。
func (m *Manager) SetRelayChannelModelsSnapshot(channel string, val []string) error {
	ch := strings.ToLower(strings.TrimSpace(channel))
	return setSetting(m, func(c *Config, v []string) {
		if ch == "" {
			return
		}
		// 懒初始化 map,避免对旧配置(字段 nil)写入触发 panic。
		if c.RelayChannelModelsSnapshot == nil {
			c.RelayChannelModelsSnapshot = make(map[string][]string)
		}
		seen := make(map[string]bool)
		cleaned := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			cleaned = append(cleaned, item)
		}
		c.RelayChannelModelsSnapshot[ch] = cleaned
	}, val)
}
