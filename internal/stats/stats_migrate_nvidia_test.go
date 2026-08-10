package stats

import (
	"math"
	"testing"
)

// nvidiaAgg 构ModelAggregate 便捷构造。
func nvidiaAgg(model string, reqs, in, out int) ModelAggregate {
	return ModelAggregate{Model: model, Reqs: reqs, InTokens: in, OutTokens: out, Cost: roundCost(float64(reqs) * 0.1)}
}

func TestFindModelBackfills_OnlyPositiveDiff(t *testing.T) {
	models := map[string]*ModelStats{
		"z-ai/glm-5.2":                  {Reqs: 16655, InTokens: 100, OutTokens: 10, Cost: roundCost(16655 * 0.1)},
		"deepseek-ai/deepseek-v4-flash": {Reqs: 175, InTokens: 50, OutTokens: 5, Cost: roundCost(175 * 0.1)},
	}
	usage := []ModelAggregate{
		nvidiaAgg("z-ai/glm-5.2", 22311, 200, 20),                        // diff +5656
		nvidiaAgg("deepseek-ai/deepseek-v4-flash", 175, 50, 5),           // 已同步 → 略过
		nvidiaAgg("minimaxai/minimax-m3", 19, 100, 4),                    // 全新 → 全额
		nvidiaAgg("deepseek-ai/deepseek-v4-pro", 37, 30, 3),              // diff +37
		nvidiaAgg("deepseek-ai/zero-usage", 0, 0, 0),                     // 零波形 → 略过
	}
	bf := FindModelBackfills(models, usage)

	got := map[string]ModelAggregate{}
	for _, b := range bf {
		got[b.Model] = b
	}

	// 应有 3 条: glm +5656, minimax +19, pro +37; flash 已同步不产生、zero 忽略。
	if len(bf) != 3 {
		t.Fatalf("want 3 backfills, got %d: %+v", len(bf), bf)
	}
	if got["z-ai/glm-5.2"].Reqs != 5656 {
		t.Errorf("glm diff reqs = %d, want 5656", got["z-ai/glm-5.2"].Reqs)
	}
	if got["minimaxai/minimax-m3"].Reqs != 19 {
		t.Errorf("minimax diff reqs = %d, want 19", got["minimaxai/minimax-m3"].Reqs)
	}
	if got["deepseek-ai/deepseek-v4-pro"].Reqs != 37 {
		t.Errorf("pro diff reqs = %d, want 37", got["deepseek-ai/deepseek-v4-pro"].Reqs)
	}
}

func TestApplyModelBackfillsLocked_ModelsScalarsPool(t *testing.T) {
	tr := NewTracker(nil)
	tr.stats.Models = map[string]*ModelStats{}
	tr.stats.Pools = map[string]*PoolStats{}

	bf := []ModelAggregate{
		nvidiaAgg("z-ai/glm-5.2", 5656, 470000000, 1000000),
		nvidiaAgg("minimaxai/minimax-m3", 19, 1894787, 4292),
	}
	tr.ApplyModelBackfillsLocked(bf)

	// Models 表
	glm := tr.stats.Models["z-ai/glm-5.2"]
	if glm == nil || glm.Reqs != 5656 || glm.InTokens != 470000000 {
		t.Fatalf("glm model entry wrong: %+v", glm)
	}
	m3 := tr.stats.Models["minimaxai/minimax-m3"]
	if m3 == nil || m3.Reqs != 19 {
		t.Fatalf("m3 model entry wrong: %+v", m3)
	}

	// 全局标量
	if tr.stats.TotalRequests != 5656+19 {
		t.Errorf("TotalRequests = %d, want %d", tr.stats.TotalRequests, 5675)
	}
	if tr.stats.TotalInputTokens != 470000000+1894787 {
		t.Errorf("TotalInputTokens = %d", tr.stats.TotalInputTokens)
	}
	// 非 TAB → 全进分母
	if tr.stats.TotalCacheEligibleInputTokens != 470000000+1894787 {
		t.Errorf("TotalCacheEligible = %d", tr.stats.TotalCacheEligibleInputTokens)
	}
	// 成本百万位圆整
	wantCost := roundCost(5656*0.1 + 19*0.1)
	if math.Abs(tr.stats.TotalCost-wantCost) > 1e-6 {
		t.Errorf("TotalCost = %v, want %v", tr.stats.TotalCost, wantCost)
	}

	// Pools["nvidia"]
	np := tr.stats.Pools["nvidia"]
	if np == nil {
		t.Fatal("nvidia pool missing")
	}
	if np.Requests != 5675 || np.InTokens != 470000000+1894787 {
		t.Errorf("nvidia pool = %+v", np)
	}
	if np.CacheEligibleInputTokens != 470000000+1894787 {
		t.Errorf("nvidia pool eligible = %d", np.CacheEligibleInputTokens)
	}
}

func TestApplyModelBackfillsLocked_Idempotent(t *testing.T) {
	tr := NewTracker(nil)
	tr.stats.Models = map[string]*ModelStats{}
	tr.stats.Pools = map[string]*PoolStats{}
	bf := []ModelAggregate{nvidiaAgg("z-ai/glm-5.2", 5656, 100, 10)}

	// 第一次应用
	tr.ApplyModelBackfillsLocked(bf)
	after1Reqs := tr.stats.Models["z-ai/glm-5.2"].Reqs
	after1Total := tr.stats.TotalRequests
	after1Pool := tr.stats.Pools["nvidia"].Requests

	// 第二次: FindModelBackfills 应返回空(diff=0), 不再重复累加
	second := FindModelBackfills(tr.stats.Models, bf)
	if len(second) != 0 {
		t.Fatalf("second pass should find no backfills, got %+v", second)
	}
	tr.ApplyModelBackfillsLocked(second)

	if tr.stats.Models["z-ai/glm-5.2"].Reqs != after1Reqs {
		t.Errorf("idempotency broken: model reqs %d -> %d", after1Reqs, tr.stats.Models["z-ai/glm-5.2"].Reqs)
	}
	if tr.stats.TotalRequests != after1Total {
		t.Errorf("idempotency broken: total %d -> %d", after1Total, tr.stats.TotalRequests)
	}
	if tr.stats.Pools["nvidia"].Requests != after1Pool {
		t.Errorf("idempotency broken: pool %d -> %d", after1Pool, tr.stats.Pools["nvidia"].Requests)
	}
}

func TestApplyModelBackfillsLocked_TabExcludesEligible(t *testing.T) {
	tr := NewTracker(nil)
	tr.stats.Models = map[string]*ModelStats{}
	tr.stats.Pools = map[string]*PoolStats{}
	// tab_ 前缀模型 → 不进命中率分母
	bf := []ModelAggregate{nvidiaAgg("tab_foo_model", 10, 5000, 100)}
	tr.ApplyModelBackfillsLocked(bf)

	if tr.stats.TotalRequests != 10 {
		t.Errorf("TotalRequests = %d", tr.stats.TotalRequests)
	}
	if tr.stats.TotalCacheEligibleInputTokens != 0 {
		t.Errorf("eligible should be 0 for TAB, got %d", tr.stats.TotalCacheEligibleInputTokens)
	}
	if tr.stats.Pools["nvidia"].CacheEligibleInputTokens != 0 {
		t.Errorf("pool eligible should be 0 for TAB, got %d", tr.stats.Pools["nvidia"].CacheEligibleInputTokens)
	}
	// 但 token 与 reqs 仍进模型表/总标量
	if tr.stats.Models["tab_foo_model"].InTokens != 5000 {
		t.Errorf("model inTokens = %d", tr.stats.Models["tab_foo_model"].InTokens)
	}
}

func TestIsNvidiaPrefixedModel(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"z-ai/glm-5.2", true},
		{"deepseek-ai/deepseek-v4-flash", true},
		{"minimaxai/minimax-m3", true},
		{"deepseek-v4-flash", false}, // 裸名 = OTHER 池共享键, 不并入
		{"qwen3.8-max", false},
		{"moonshotai/kimi-k3-free", true},
		{"models/gemini-2.5-flash", false}, // 路径式前缀, 非厂商命名空间
		{"gemini-2.5-flash", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isNvidiaPrefixedModel(c.name); got != c.want {
			t.Errorf("isNvidiaPrefixedModel(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBackfillNvidiaModelsFromUsage_SkipsBareNames(t *testing.T) {
	tr := NewTracker(nil)
	tr.stats.Models = map[string]*ModelStats{}
	tr.stats.Pools = map[string]*PoolStats{}
	// OTHER 池裸名 deepseek-v4-flash 与 NVIDIA 前缀名各占一条, 裸名不得并入
	usage := []ModelAggregate{
		nvidiaAgg("deepseek-ai/deepseek-v4-flash", 590, 100, 10),
		nvidiaAgg("deepseek-v4-flash", 1625, 200, 20),
	}
	tr.BackfillNvidiaModelsFromUsage(usage)

	if _, ok := tr.stats.Models["deepseek-v4-flash"]; ok {
		t.Error("bare-name OTHER model must NOT be created by nvidia backfill")
	}
	if tr.stats.Models["deepseek-ai/deepseek-v4-flash"] == nil {
		t.Error("prefixed nvidia model should be backfilled")
	}
	if tr.stats.TotalRequests != 590 {
		t.Errorf("TotalRequests = %d, want 590 (bare name excluded)", tr.stats.TotalRequests)
	}
}

func TestBackfillNvidiaModelsFromUsage_FlagGateOnce(t *testing.T) {
	tr := NewTracker(nil)
	tr.stats.Models = map[string]*ModelStats{}
	tr.stats.Pools = map[string]*PoolStats{}
	usage := []ModelAggregate{nvidiaAgg("z-ai/glm-5.2", 100, 10, 1)}

	tr.BackfillNvidiaModelsFromUsage(usage)
	if !tr.stats.NvidiaUsageBackfillDone {
		t.Fatal("flag should be set after first backfill")
	}
	before := tr.stats.Models["z-ai/glm-5.2"].Reqs

	// 再次调用(携带更大 usage)也应被标志拦截, 不重复累加
	bigger := []ModelAggregate{nvidiaAgg("z-ai/glm-5.2", 1000, 100, 10)}
	tr.BackfillNvidiaModelsFromUsage(bigger)
	if tr.stats.Models["z-ai/glm-5.2"].Reqs != before {
		t.Errorf("flag gate failed: reqs %d -> %d", before, tr.stats.Models["z-ai/glm-5.2"].Reqs)
	}
}

func TestAggregateNvidiaAccounts_OnlyProviderNvidia(t *testing.T) {
	accounts := map[string]*AccountUsage{
		"nv-1": {Provider: "nvidia", Models: map[string]*ModelUsage{
			"z-ai/glm-5.2": {TokenStats: TokenStats{RequestCount: 10, InputTokens: 10, TotalCost: 1.0}},
		}},
		"nv-2": {Provider: "nvidia", Models: map[string]*ModelUsage{
			"z-ai/glm-5.2": {TokenStats: TokenStats{RequestCount: 20, InputTokens: 20, TotalCost: 2.0}},
		}},
		"other-1": {Provider: "other", Models: map[string]*ModelUsage{
			"deepseek-v4-flash": {TokenStats: TokenStats{RequestCount: 999, InputTokens: 999, TotalCost: 9.0}},
		}},
		"direct": {Provider: "direct", Models: map[string]*ModelUsage{
			"gemini-2.5-flash": {TokenStats: TokenStats{RequestCount: 5, InputTokens: 5, TotalCost: 0.5}},
		}},
	}
	agg := aggregateNvidiaAccounts(accounts)
	if len(agg) != 1 {
		t.Fatalf("want 1 aggregate (glm-5.2), got %d: %+v", len(agg), agg)
	}
	if agg[0].Reqs != 30 || agg[0].InTokens != 30 {
		t.Errorf("cross-account merge wrong: %+v", agg[0])
	}
	// OTHER/direct 账号不得混入
	for _, a := range agg {
		if a.Model == "deepseek-v4-flash" || a.Model == "gemini-2.5-flash" {
			t.Errorf("non-nvidia account leaked into aggregate: %+v", a)
		}
	}
}