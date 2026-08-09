package stats

import (
	"testing"
)

// TestIsTabModel 验证 TAB 代码补全模型识别逻辑
func TestIsTabModel(t *testing.T) {
	tabModels := []string{
		"tab_flash_lite_preview",
		"tab_jump_flash_lite_preview",
		"models/tab_flash_lite_preview",
		"TAB_FLASH_LITE_PREVIEW",
		"tab-jump",
		"tab",
	}
	for _, m := range tabModels {
		if !IsTabModel(m) {
			t.Errorf("IsTabModel(%q) = false, want true", m)
		}
	}

	nonTabModels := []string{
		"gemini-3-flash",
		"claude-opus-4-6-thinking",
		"antigravity-core",
		"models/gemini-2.5-flash",
		"z-ai/glm-5.2",
	}
	for _, m := range nonTabModels {
		if IsTabModel(m) {
			t.Errorf("IsTabModel(%q) = true, want false", m)
		}
	}
}

// TestTrackRequest_TabModelExcluded 验证实时请求中 TAB 模型不增加缓存命中率分母 TotalCacheEligibleInputTokens
func TestTrackRequest_TabModelExcluded(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 1. 发起普通 Gemini 请求
	tr.TrackRequest("gemini-3-flash", 1000, 200, 500)
	if tr.stats.TotalInputTokens != 1000 {
		t.Errorf("TotalInputTokens = %d, want 1000", tr.stats.TotalInputTokens)
	}
	if tr.stats.TotalCacheEligibleInputTokens != 1000 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 1000", tr.stats.TotalCacheEligibleInputTokens)
	}

	// 2. 发起 TAB 代码补全请求 (应该增加 TotalInputTokens，但不增加 TotalCacheEligibleInputTokens)
	tr.TrackRequest("tab_flash_lite_preview", 3000, 100, 0)
	if tr.stats.TotalInputTokens != 4000 {
		t.Errorf("TotalInputTokens = %d, want 4000", tr.stats.TotalInputTokens)
	}
	if tr.stats.TotalCacheEligibleInputTokens != 1000 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 1000 (TAB request input tokens should be excluded)", tr.stats.TotalCacheEligibleInputTokens)
	}

	// 3. 发起第二个 TAB 补全请求 (tab_jump)
	tr.TrackRequest("tab_jump_flash_lite_preview", 2000, 50, 0)
	if tr.stats.TotalInputTokens != 6000 {
		t.Errorf("TotalInputTokens = %d, want 6000", tr.stats.TotalInputTokens)
	}
	if tr.stats.TotalCacheEligibleInputTokens != 1000 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 1000 (tab_jump request input tokens should be excluded)", tr.stats.TotalCacheEligibleInputTokens)
	}
}

// TestTrackRequestForPool_TabModelExcluded 验证按池记账中 TAB 模型不增加 CacheEligibleInputTokens
func TestTrackRequestForPool_TabModelExcluded(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	tr.TrackRequestForPool("gemini-3-flash", 1000, 200, 500, "antigravity")
	tr.TrackRequestForPool("tab_flash_lite_preview", 3000, 100, 0, "antigravity")

	ag := tr.stats.Pools["antigravity"]
	if ag == nil {
		t.Fatal("antigravity pool bucket should exist")
	}
	if ag.InTokens != 4000 {
		t.Errorf("antigravity pool InTokens = %d, want 4000", ag.InTokens)
	}
	if ag.CacheEligibleInputTokens != 1000 {
		t.Errorf("antigravity pool CacheEligibleInputTokens = %d, want 1000 (TAB input tokens excluded)", ag.CacheEligibleInputTokens)
	}
}

// TestRecalculateCacheEligibleTokens_OneTimeMigration 验证历史存量重算精算功能（单次迁移）。
//
// 修复后口径(关键变更): Recalc 只重算「全局」TotalCacheEligibleInputTokens, 刻意不再覆盖
// Pools["antigravity"].CacheEligibleInputTokens。旧实现把全局分母塞进池桶, 等于把 NVIDIA 池
// inTokens 也算进 antigravity 命中率分母, 命中率暴跌到 0(截图 0.0% 根因之二)。
// 修复后池分母只由 TrackRequestForPool/backfillForce 各自按池累加, 全局与池物理隔离。
// 故本用例改为: 全局分母被修正为 70000(仅 gemini+claude, 排除 TAB 70 万), 池分母保持不变(770000,
// 由 TrackRequestForPool 累加, Recalc 不触碰)。
func TestRecalculateCacheEligibleTokens_OneTimeMigration(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 模拟存量 Models 表 (包含被旧版本写上的全局/池分母污染值)
	tr.stats.Models["gemini-3-flash"] = &ModelStats{Reqs: 50, InTokens: 50000, OutTokens: 5000, CachedTokens: 30000}
	tr.stats.Models["claude-opus-4-6-thinking"] = &ModelStats{Reqs: 10, InTokens: 20000, OutTokens: 2000, CachedTokens: 15000}
	tr.stats.Models["tab_flash_lite_preview"] = &ModelStats{Reqs: 500, InTokens: 500000, OutTokens: 10000, CachedTokens: 0}
	tr.stats.Models["tab_jump_flash_lite_preview"] = &ModelStats{Reqs: 200, InTokens: 200000, OutTokens: 5000, CachedTokens: 0}

	// 模拟旧版的脏分母数据 (包含 TAB 的 70 万 Token)
	tr.stats.TotalCacheEligibleInputTokens = 770000
	tr.stats.Pools = map[string]*PoolStats{
		"antigravity": {
			Requests:                 760,
			InTokens:                 770000,
			OutTokens:                22000,
			CachedTokens:             45000,
			CacheEligibleInputTokens: 770000,
		},
	}

	tr.Lock()
	tr.RecalculateCacheEligibleTokensLocked()
	tr.Unlock()

	wantEligible := 50000 + 20000 // 仅 gemini + claude，排除 TAB 的 70 万
	if tr.stats.TotalCacheEligibleInputTokens != wantEligible {
		t.Errorf("Recalculate TotalCacheEligibleInputTokens = %d, want %d", tr.stats.TotalCacheEligibleInputTokens, wantEligible)
	}
	ag := tr.stats.Pools["antigravity"]
	// 修复后: 池分母未被 Recalc 触碰, 保持旧值 770000(由 TrackRequestForPool 累加, Recalc 不串扰)。
	// 旧实现错误地把它改成 wantEligible(70000), 等于把全局分母塞进池桶 → 池命中率分母被全量放大 → 0%。
	if ag.CacheEligibleInputTokens != 770000 {
		t.Errorf("Recalculate should NOT touch pool eligible (crosstalk bug): Pool CacheEligibleInputTokens = %d, want 770000 (unchanged, pool denom isolated from global)", ag.CacheEligibleInputTokens)
	}

	// 验证通用指标 InTokens / Requests / CachedTokens 保持不受影响
	if ag.InTokens != 770000 {
		t.Errorf("Pool InTokens = %d, want 770000", ag.InTokens)
	}
	if ag.CachedTokens != 45000 {
		t.Errorf("Pool CachedTokens = %d, want 45000", ag.CachedTokens)
	}
}
