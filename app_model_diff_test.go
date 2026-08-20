package main

import (
	"reflect"
	"testing"
)

// app_model_diff_test.go: diffRemoteAdded 纯函数单元测试。
// 覆盖:空入参、空快照(首次全增)、大小写不敏感、trim、去重、脏数据不污染、典型 diff 场景。

func TestDiffRemoteAdded_EmptyCurrent(t *testing.T) {
	// 本次全集为空 → 无新增(即使有快照)
	if got := diffRemoteAdded(nil, []string{"a", "b"}); len(got) != 0 {
		t.Errorf("empty current: want [], got %v", got)
	}
	if got := diffRemoteAdded([]string{}, []string{"a"}); len(got) != 0 {
		t.Errorf("empty current slice: want [], got %v", got)
	}
}

func TestDiffRemoteAdded_EmptySnapshot_AllNew(t *testing.T) {
	// 快照为空(首次/旧配置)→ 本次全集全部视为新增
	cur := []string{"z-ai/glm-5.2", "moonshotai/kimi-k2.5"}
	got := diffRemoteAdded(cur, nil)
	if !reflect.DeepEqual(got, cur) {
		t.Errorf("empty snapshot: want whole current %v, got %v", cur, got)
	}
	got2 := diffRemoteAdded(cur, []string{})
	if !reflect.DeepEqual(got2, cur) {
		t.Errorf("empty snapshot slice: want %v, got %v", cur, got2)
	}
}

func TestDiffRemoteAdded_CaseInsensitive(t *testing.T) {
	// 大小写不敏感比对:上次有 "Z-AI/GLM-5.2",本次 "z-ai/glm-5.2" 不算新增
	cur := []string{"z-ai/glm-5.2", "nvidia/Nemotron"}
	snap := []string{"Z-AI/GLM-5.2"} // glm 命中不新增,nemotron 新增
	got := diffRemoteAdded(cur, snap)
	if len(got) != 1 || got[0] != "nvidia/Nemotron" {
		t.Errorf("case-insensitive: want [nvidia/Nemotron], got %v", got)
	}
}

func TestDiffRemoteAdded_TrimAndDedup(t *testing.T) {
	// 入参 trim + 本次去重:脏数据(空白)、重复项不污染结果
	cur := []string{"  z-ai/glm-5.2  ", "z-ai/glm-5.2", "  ", "deepseek-ai/deepseek-v3"}
	snap := []string{"z-ai/glm-5.2"}
	got := diffRemoteAdded(cur, snap)
	// glm(去重后 1 个 + 命中快照不新增)+ deepseek(新增)→ 仅 deepseek
	if len(got) != 1 || got[0] != "deepseek-ai/deepseek-v3" {
		t.Errorf("trim/dedup: want [deepseek-ai/deepseek-v3], got %v", got)
	}
}

func TestDiffRemoteAdded_MixedDiff(t *testing.T) {
	// 典型 diff:快照 3 项,本次 5 项,其中 2 项新增、3 项保持、0 项本次少(不全在本次的快照项不影响 added,added 只看多出的)
	cur := []string{"a", "b", "c", "d", "e"}
	snap := []string{"a", "b", "c"} // d/e 新增
	got := diffRemoteAdded(cur, snap)
	if !reflect.DeepEqual(got, []string{"d", "e"}) {
		t.Errorf("mixed diff: want [d e], got %v", got)
	}
	// 上次有、本次没有的项(g)不影响 added(added 只关心本次多的);
	// 验证「快照多余项不会进入 added」:快照含本次没有的项目,added 仍只列本次多的
	cur2 := []string{"x", "y"}
	snap2 := []string{"x", "y", "z"} // z 在快照但本次没有 → 不进 added
	got2 := diffRemoteAdded(cur2, snap2)
	if len(got2) != 0 {
		t.Errorf("snapshot-only item should not appear in added: got %v", got2)
	}
}

func TestDiffRemoteAdded_ReturnsCurrentSpelling(t *testing.T) {
	// 新增项返回本次的原始大小写写法(非小写规整),便于前端原样展示
	cur := []string{"Z-AI/GLM-5.2"}
	snap := []string{"nvidia/nemotron"}
	got := diffRemoteAdded(cur, snap)
	if len(got) != 1 || got[0] != "Z-AI/GLM-5.2" {
		t.Errorf("added should preserve current spelling: want Z-AI/GLM-5.2, got %v", got)
	}
}
