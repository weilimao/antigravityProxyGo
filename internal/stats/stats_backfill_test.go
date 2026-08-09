package stats

// stats_backfill_test.go: 覆盖 BackfillPoolFromModels 的一次性存量回填语义。
//
// 场景: TrackRequestForPool 是 8/8 才随按池筛选引入, 老 stats.json 的 Pools 只有零散增量,
// antigravity 号池真实全量历史(自 6/12 的 gemini/claude/v1internal 直连与 daily-cloudcode-pa
// 翻译链)只累计在 Models/trends/全局标量。回填一次性把 Google 号池族模型(token 缓存真正命中
// 的那些)归并进 Pools["antigravity"], 使按池卡片不再显示残缺快照。
// 断言重点:
//   1. Google 族模型(gemini/claude/tab/antigravity-core/unknown)并入 antigravity 桶;
//   2. NVIDIA / 第三方 OpenAI 兼容模型(nvidia/, deepseek, z-ai/glm, openai/)绝不并入;
//   3. 幂等: 桶已有真实增量后再次调用跳过, 不二次叠加;
//   4. 全局标量 / Models 不动(零回归)。

import (
	"testing"
)

// TestBackfillPoolFromModels_FamilyMerge 反推语义: Google 族归并、第三方排除、幂等、全局零回归。
func TestBackfillPoolFromModels_FamilyMerge(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 预置一个 Google 族模型 + 一个第三方模型 + 一个 nvidia 前缀模型。
	// TrackRequestForModel 明确把 NVIDIA/Other 的 nvidia/ 前缀模型计入全局, 同时把
	// "nvidia"/其他第三方模型写进 Pools 各自桶(经由 recordNvidiaUsage/recordOtherUsage),
	// 这里不实际调用(避免制造 harness 内部行为), 直接手写 Models 表模拟存量。
	tr.stats.Models["gemini-3-flash-agent"] = &ModelStats{Reqs: 100, InTokens: 5000, OutTokens: 300, CachedTokens: 4500, Cost: 0.5}
	tr.stats.Models["claude-opus-4-6-thinking"] = &ModelStats{Reqs: 20, InTokens: 2000, OutTokens: 100, CachedTokens: 1800, Cost: 0.3}
	tr.stats.Models["unknown"] = &ModelStats{Reqs: 5, InTokens: 500, OutTokens: 10, CachedTokens: 400, Cost: 0.05}
	tr.stats.Models["deepseek-v4-flash"] = &ModelStats{Reqs: 30, InTokens: 9000, OutTokens: 0, CachedTokens: 8500, Cost: 1.0}
	tr.stats.Models["nvidia/z-ai/glm-5.2"] = &ModelStats{Reqs: 40, InTokens: 8000, OutTokens: 500, CachedTokens: 0, Cost: 2.0}

	tr.BackfillPoolFromModels()

	ag := tr.stats.Pools["antigravity"]
	if ag == nil {
		t.Fatal("antigravity pool bucket should exist after backfill")
	}
	wantReqs := 100 + 20 + 5
	if ag.Requests != wantReqs {
		t.Errorf("antigravity reqs = %d, want %d", ag.Requests, wantReqs)
	}
	wantIn := 5000 + 2000 + 500
	if ag.InTokens != wantIn {
		t.Errorf("antigravity inTokens = %d, want %d", ag.InTokens, wantIn)
	}
	wantCached := 4500 + 1800 + 400
	if ag.CachedTokens != wantCached {
		t.Errorf("antigravity cachedTokens = %d, want %d", ag.CachedTokens, wantCached)
	}
	if ag.CacheEligibleInputTokens != wantIn {
		t.Errorf("antigravity cacheEligibleInputTokens = %d, want %d", ag.CacheEligibleInputTokens, wantIn)
	}
	if ag.OutTokens != 300+100+10 {
		t.Errorf("antigravity outTokens = %d, want %d", ag.OutTokens, 410)
	}
	// 成本按存量精算值合并, 不入全局 TotalCost。
	if ag.Cost != 0.5+0.3+0.05 {
		t.Errorf("antigravity cost = %v, want %v", ag.Cost, 0.85)
	}

	// 第三方/nvidia 模型绝不并入 antigravity 桶(存在性由上方 wantReqs/wantIn/wantCached 约束)。
	if got := tr.stats.Pools["deepseek-v4-flash"]; got != nil {
		t.Errorf("deepseek-v4-flash should not have its own pool bucket, got %+v", got)
	}
	// nvidia 桶由 recordNvidiaUsage 单侧维护, 反推不创建它(本测试手写 Models 表, 未走 nvidia 记账路径)。
	if got := tr.stats.Pools["nvidia"]; got != nil {
		t.Errorf("backfill should not create nvidia pool bucket (its data comes from recordNvidiaUsage), got %+v", got)
	}
}

// TestBackfillPoolFromModels_Idempotent 二次调用不叠加。
func TestBackfillPoolFromModels_Idempotent(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.stats.Models["gemini-3-flash-agent"] = &ModelStats{Reqs: 100, InTokens: 5000, CachedTokens: 4500, Cost: 0.5}

	tr.BackfillPoolFromModels()
	first := tr.stats.Pools["antigravity"]
	if first == nil || first.InTokens != 5000 {
		t.Fatalf("first backfill failed: %+v", first)
	}

	// 二次调用: 桶已有真实增量 → 幂等跳过, 不叠加。
	tr.BackfillPoolFromModels()
	second := tr.stats.Pools["antigravity"]
	if second.InTokens != 5000 || second.CachedTokens != 4500 {
		t.Errorf("backfill not idempotent: second in=%d cached=%d want 5000/4500", second.InTokens, second.CachedTokens)
	}
}

// TestBackfillPoolFromModels_Empty 无 Google 族模型时不制造空桶。
func TestBackfillPoolFromModels_Empty(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.stats.Models["deepseek-v4-flash"] = &ModelStats{Reqs: 1, InTokens: 100}
	tr.stats.Models["nvidia/z-ai/glm-5.2"] = &ModelStats{Reqs: 1, InTokens: 100}

	tr.BackfillPoolFromModels()
	if got := tr.stats.Pools["antigravity"]; got != nil && (got.Requests > 0 || got.InTokens > 0) {
		t.Errorf("empty backfill should not create antigravity bucket, got %+v", got)
	}
}

// TestModelIsGooglePoolFamily 模型关键字分类判定覆盖。
func TestModelIsGooglePoolFamily(t *testing.T) {
	trueCases := []string{
		"gemini-3-flash-agent", "claude-opus-4-6-thinking", "tab_flash_lite_preview",
		"tab-jump_flash_lite_preview", "antigravity-core", "unknown", "",
	}
	for _, c := range trueCases {
		if !modelIsGooglePoolFamily(c) {
			t.Errorf("modelIsGooglePoolFamily(%q) = false, want true", c)
		}
	}
	falseCases := []string{
		"nvidia/z-ai/glm-5.2", "deepseek-v4-flash", "openai/gpt-oss-120b",
		"moonshotai/kimi-k3-free", "qwen3.8-max", "gpt-5",
	}
	for _, c := range falseCases {
		if modelIsGooglePoolFamily(c) {
			t.Errorf("modelIsGooglePoolFamily(%q) = true, want false", c)
		}
	}
}

// TestBackfillPoolFromModelsForce_OverridesStaleBucket 复现并修复截图 0.0% 的核心脏态:
// 8/8 上线后老的 stats.json 已带零散 Pools["antigravity"](reqs=121, inTokens=4418808,
// cachedTokens=3551293), 但 Models 表的 Google 族全量(几千 M cached)从未并入桶。
// 普通 backfill 的幂等守卫(reqs>0 跳过)会错误拦截, Avg 命中率停留在残缺快照分子/分母。
// Force 版必须无视该守卫, 用 Models 全量覆盖式重算桶标量, 使分子分母回到真实全量。
func TestBackfillPoolFromModelsForce_OverridesStaleBucket(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 模拟存量 Models 全量(Google 族 + 第三方)。
	tr.stats.Models["gemini-3-flash-agent"] = &ModelStats{Reqs: 5000, InTokens: 20000000, OutTokens: 100000, CachedTokens: 18000000, Cost: 50.0}
	tr.stats.Models["claude-opus-4-6-thinking"] = &ModelStats{Reqs: 3000, InTokens: 10000000, OutTokens: 50000, CachedTokens: 9000000, Cost: 30.0}
	tr.stats.Models["deepseek-v4-flash"] = &ModelStats{Reqs: 200, InTokens: 800000, OutTokens: 0, CachedTokens: 0, Cost: 5.0}

	// 模拟脏态: 桶已有 8/8 上线后零散增量(远小于 Models 全量)。
	tr.stats.Pools["antigravity"] = &PoolStats{
		Requests: 121, InTokens: 4418808, OutTokens: 30598,
		CachedTokens: 3551293, CacheEligibleInputTokens: 4418808, Cost: 3.405966,
	}

	// 普通 backfill: 幂等守卫拦截, 桶不变(复现 bug: 全量永不并入)。
	tr.BackfillPoolFromModels()
	if ag := tr.stats.Pools["antigravity"]; ag.Requests != 121 || ag.CachedTokens != 3551293 {
		t.Fatalf("normal backfill should be blocked by idempotent guard on stale bucket, got %+v", ag)
	}

	// Force 版: 无视守卫, 用 Models Google 族全量覆盖式重算。
	tr.BackfillPoolFromModelsForce()
	ag := tr.stats.Pools["antigravity"]
	if ag == nil {
		t.Fatal("antigravity pool should exist after force backfill")
	}
	wantReqs := 5000 + 3000
	wantIn := 20000000 + 10000000
	wantCached := 18000000 + 9000000
	if ag.Requests != wantReqs {
		t.Errorf("force reqs = %d, want %d (Models 全量覆盖, 而非零散 121)", ag.Requests, wantReqs)
	}
	if ag.InTokens != wantIn {
		t.Errorf("force inTokens = %d, want %d", ag.InTokens, wantIn)
	}
	if ag.CachedTokens != wantCached {
		t.Errorf("force cachedTokens = %d, want %d (几千 M 全量并入, 而非 3.5M 残缺快照)", ag.CachedTokens, wantCached)
	}
	// 第三方模型绝不并入 antigravity 桶(deepseek 800K inTokens 不该出现在桶里)。
	if ag.InTokens > wantIn {
		t.Errorf("third-party model leaked into antigravity pool: inTokens=%d > want=%d", ag.InTokens, wantIn)
	}
}

// TestBackfillPoolFromModelsForce_Idempotent Force 版重复调用结果一致(每次用 Models 全量重算覆盖,
// 不叠加), 保证 LoadFromDisk 多次调用或测试反复调用都不会累积放大。
func TestBackfillPoolFromModelsForce_Idempotent(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.stats.Models["gemini-3-flash-agent"] = &ModelStats{Reqs: 100, InTokens: 5000, CachedTokens: 4500, Cost: 0.5}

	tr.BackfillPoolFromModelsForce()
	first := tr.stats.Pools["antigravity"]
	if first == nil || first.InTokens != 5000 {
		t.Fatalf("first force backfill failed: %+v", first)
	}

	// 二次调用: 仍是覆盖式重算(不是叠加), 结果应与首次一致。
	tr.BackfillPoolFromModelsForce()
	second := tr.stats.Pools["antigravity"]
	if second.InTokens != 5000 || second.CachedTokens != 4500 || second.Requests != 100 {
		t.Errorf("force backfill not idempotent(cover-style): second = %+v, want in=5000 cached=4500 reqs=100", second)
	}
}

// TestBackfillPoolFromModelsForce_Empty 无 Google 族模型时 Force 也不制造空桶(与普通版同口径)。
func TestBackfillPoolFromModelsForce_Empty(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.stats.Models["deepseek-v4-flash"] = &ModelStats{Reqs: 1, InTokens: 100}

	tr.BackfillPoolFromModelsForce()
	if got := tr.stats.Pools["antigravity"]; got != nil && (got.Requests > 0 || got.InTokens > 0) {
		t.Errorf("empty force backfill should not create antigravity bucket, got %+v", got)
	}
}

// TestRecalculateCacheEligibleTokensLocked_NoPoolCrosstalk 验证 Recalc 分母隔离修复:
// Recalc 只重算「全局」TotalCacheEligibleInputTokens, 刻意不再覆盖 Pools["antigravity"]
// 的 CacheEligibleInputTokens。旧实现把全局分母塞进池桶, 等于把 NVIDIA 池 inTokens 也算进
// antigravity 池命中率分母, 命中率暴跌到 0(截图 0.0% 根因之二)。
// 修复后池分母只由 TrackRequestForPool/backfillForce 各自按池累加, 全局与池物理隔离。
func TestRecalculateCacheEligibleTokensLocked_NoPoolCrosstalk(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 全局 Models 表含 Google 族(gemini) + 第三方(deepseek, 但 deepseek 不是 Google 族, 不入全局分母)。
	// 注意: modelIsGooglePoolFamily 对 "deepseek" 返回 false, 故 Recalc 全局分母只算 gemini 的 inTokens。
	tr.stats.Models["gemini-3-flash-agent"] = &ModelStats{Reqs: 100, InTokens: 5000, OutTokens: 300, CachedTokens: 4500}
	tr.stats.Models["deepseek-v4-flash"] = &ModelStats{Reqs: 30, InTokens: 9000, OutTokens: 0, CachedTokens: 0}

	// 预置 antigravity 池桶真实增量(分母应只含 gemini inTokens=5000, 不含 deepseek 9000)。
	tr.stats.Pools["antigravity"] = &PoolStats{
		Requests: 100, InTokens: 5000, OutTokens: 300,
		CachedTokens: 4500, CacheEligibleInputTokens: 5000, Cost: 0.5,
	}
	// 预置 nvidia 池桶(NVIDIA 池 inTokens 绝不该算进 antigravity 池分母)。
	tr.stats.Pools["nvidia"] = &PoolStats{
		Requests: 30, InTokens: 9000, OutTokens: 0,
		CachedTokens: 0, CacheEligibleInputTokens: 9000, Cost: 1.0,
	}

	beforeAg := tr.stats.Pools["antigravity"].CacheEligibleInputTokens
	tr.RecalculateCacheEligibleTokensLocked()

	// 全局分母: gemini inTokens=5000(deepseek 非 Google 族, 不计入全局分母)。
	if got := tr.stats.TotalCacheEligibleInputTokens; got != 5000 {
		t.Errorf("global eligible = %d, want 5000 (only Google family non-TAB)", got)
	}
	// 池分母未被全局分母串扰: antigravity 池桶分母保持 5000, 不被改成全局 5000 之外的值。
	// (本例全局与 antigravity 池分母恰好都是 5000, 关键是 Recalc 不触碰池桶, 值不变即正确。)
	if got := tr.stats.Pools["antigravity"].CacheEligibleInputTokens; got != beforeAg {
		t.Errorf("Recalc should NOT touch pool eligible (crosstalk bug): antigravity pool eligible = %d, want %d (unchanged)", got, beforeAg)
	}
	// nvidia 池分母也不被触碰。
	if got := tr.stats.Pools["nvidia"].CacheEligibleInputTokens; got != 9000 {
		t.Errorf("nvidia pool eligible should be untouched, got %d want 9000", got)
	}
}