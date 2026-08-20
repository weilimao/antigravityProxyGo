package main

import "strings"

// app_model_diff.go: 「上次远端拉取到的上游模型全集」与「本次全集」的 diff 纯函数。
//
// 供两类 IPC 复用:
//   - settings:get-nvidia-preferred-models(NVIDIA 专属模型清单弹窗)
//   - relay:fetch-channel-models(中继模型映射面板)
// 两处都在拉取成功后算「本轮新增 = 本次全集 − 上次快照」,并把本次全集整体覆盖落盘为下次的快照基准。
// 「失效」判定不在此处——由前端用「已选/已配 − 本次远端全集」现场算出(更实时、省一次 IPC),故本文件只管 added。

// diffRemoteAdded 返回「本次远端全集 current 中、上次快照 snapshot 里没有的模型 id」(本轮新增)。
// 口径:大小写不敏感比对(与前端既有 existingTargetSet/containsSet 同口径,防止大小写差异误判新增)。
// snapshot 为空(首次拉取/旧配置无快照)时,本次全集全部视为新增(典型起步场景:把整张上游清单都标为新增)。
// 入参与快照均逐项 trim+去重规整后比较,脏数据(空串/重复)不污染结果。
func diffRemoteAdded(current []string, snapshot []string) []string {
	if len(current) == 0 {
		return []string{}
	}
	// 旧快照建立「小写全集」索引,一次建好;空快照则索引为空,本次全集全部命中 added。
	snapSet := make(map[string]struct{}, len(snapshot))
	for _, s := range snapshot {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" {
			continue
		}
		snapSet[k] = struct{}{}
	}
	seen := make(map[string]struct{}, len(current))
	added := make([]string, 0)
	for _, c := range current {
		k := strings.ToLower(strings.TrimSpace(c))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, was := snapSet[k]; !was {
			added = append(added, c)
		}
	}
	return added
}
