package stats

// stats_pool_test.go: 覆盖 TrackRequestForPool 的按池/组分流累加、PoolKeyForProvider 映射、
// 与既有 Track* 的口径隔离(全局标量不被污染)、SaveToDisk/LoadFromDisk 的 Pools 字段往返与旧 JSON 兜底。
//
// 设计意图: 各池/组分子分母独立累加互不串扰(命中: user 决策2), 全局口径零回归。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

func newTestTrackerWithNoPersist(t *testing.T) *Tracker {
	t.Helper()
	pm := pricing.NewManager()
	tr := NewTracker(pm)
	tr.persistPath = "" // 防止测试触发落盘定时器
	return tr
}

// TestPoolKeyForProvider_Mapping 表驱动覆盖所有 provider 字面量到池 key 的映射。
func TestPoolKeyForProvider_Mapping(t *testing.T) {
	cases := []struct {
		provider string
		groupID string
		want     string
	}{
		{"nvidia", "", "nvidia"},
		{"NVIDIA", "", "nvidia"}, // 大小写不敏感
		{"other", "openai", "other:openai"},
		{"other", " DeepSeek ", "other:deepseek"}, // TrimSpace + ToLower 规整
		{"other", "DEEPSEEK", "other:deepseek"},    // ToLower 规整
		{"other", "", otherUnknownGroupKey},        // GroupID 缺失兜底
		{"other", "   ", otherUnknownGroupKey},      // GroupID 全空白兜底
		{"antigravity", "", "antigravity"},
		{"project", "", "antigravity"}, // project 归 antigravity 桶
		{"google", "", "antigravity"},
		{"gcp", "", "antigravity"},
		{"gemini-cli", "", "antigravity"},
		{"", "", "antigravity"}, // 空 provider(直连无 poolAccount) 归默认链路
	}
	for _, c := range cases {
		got := PoolKeyForProvider(c.provider, c.groupID)
		if got != c.want {
			t.Errorf("PoolKeyForProvider(%q, %q) = %q, want %q", c.provider, c.groupID, got, c.want)
		}
	}
}

// TestIsOtherPoolKey 与 OtherGroupIDFromKey 辅助判定。
func TestIsOtherPoolKey_AndGroupIDFromKey(t *testing.T) {
	if !IsOtherPoolKey("other:openai") {
		t.Error("IsOtherPoolKey(other:openai) want true")
	}
	if !IsOtherPoolKey(otherUnknownGroupKey) {
		t.Error("IsOtherPoolKey(other:__unknown__) want true")
	}
	if IsOtherPoolKey("nvidia") || IsOtherPoolKey("antigravity") {
		t.Error("IsOtherPoolKey on non-other want false")
	}
	if OtherGroupIDFromKey("other:openai") != "openai" {
		t.Errorf("OtherGroupIDFromKey want openai")
	}
	if OtherGroupIDFromKey("nvidia") != "" {
		t.Errorf("OtherGroupIDFromKey(nvidia) want empty")
	}
}

// TestTrackRequestForPool_AccruesByPool 验证单池累加正确性(分子/分母/计数/cost)。
func TestTrackRequestForPool_AccruesByPool(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	tr.TrackRequestForPool("gemini-2.5-flash", in200, out100, cached10, "antigravity")

	pools := tr.GetPoolStatsCopy()
	ps, ok := pools["antigravity"]
	if !ok {
		t.Fatal("missing antigravity pool bucket")
	}
	if ps.Requests != 1 || ps.InTokens != in200 || ps.OutTokens != out100 || ps.CachedTokens != cached10 {
		t.Errorf("pool scalar mismatch: %+v", ps)
	}
	if ps.CacheEligibleInputTokens != in200 {
		t.Errorf("CacheEligibleInputTokens = %d, want %d (分母累加)", ps.CacheEligibleInputTokens, in200)
	}
	// 命中率 = 10/200 = 5%
	if got := hitRatePercent(ps); got != 5.0 {
		t.Errorf("hit rate = %.2f%%, want 5.0%%", got)
	}
}

// TestTrackRequestForPool_ThreePools_NoCrossContaminate 验证三池分流互不串扰(决策2核心)。
func TestTrackRequestForPool_ThreePools_NoCrossContaminate(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// antigravity: gemini 直连, cached=10
	tr.TrackRequestForPool("gemini-2.5-flash", 200, 100, 10, "antigravity")
	// nvidia: cached 恒 0
	tr.TrackRequestForPool("z-ai/glm-5.2", 500, 250, 0, "nvidia")
	// other:deepseek: 上游回报 cached=30
	tr.TrackRequestForPool("deepseek-chat", 400, 200, 30, "other:deepseek")

	pools := tr.GetPoolStatsCopy()

	if ps := pools["antigravity"]; ps == nil || ps.CachedTokens != 10 || ps.CacheEligibleInputTokens != 200 {
		t.Errorf("antigravity bucket wrong: %+v", ps)
	}
	if ps := pools["nvidia"]; ps == nil || ps.CachedTokens != 0 || ps.CacheEligibleInputTokens != 500 {
		t.Errorf("nvidia bucket wrong: %+v", ps)
	}
	if ps := pools["other:deepseek"]; ps == nil || ps.CachedTokens != 30 || ps.CacheEligibleInputTokens != 400 {
		t.Errorf("other:deepseek bucket wrong: %+v", ps)
	}

	// 三池分子各自独立, 互不串扰
	if got, want := hitRatePercent(pools["antigravity"]), 5.0; got != want {
		t.Errorf("antigravity hit rate = %.2f%%, want %.2f%%", got, want)
	}
	if got, want := hitRatePercent(pools["nvidia"]), 0.0; got != want {
		t.Errorf("nvidia hit rate = %.2f%%, want %.2f%%(恒0)", got, want)
	}
	if got, want := hitRatePercent(pools["other:deepseek"]), 7.5; got != want {
		t.Errorf("other:deepseek hit rate = %.2f%%, want %.2f%%", got, want)
	}

	// 确认 nvidia 桶也有 inTokens 累加(分母照算, 只是分子恒0)
	if pools["nvidia"].InTokens != 500 {
		t.Errorf("nvidia InTokens = %d, want 500", pools["nvidia"].InTokens)
	}
}

// TestTrackRequestForPool_GlobalScalarsUnchanged 验证新累加点不污染全局标量(零回归核心)。
// 既有 Track* 写全局, TrackRequestForPool 只写 Pools, 二者并行。
func TestTrackRequestForPool_GlobalScalarsUnchanged(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 先走既有 TrackRequest(写全局)
	tr.TrackRequest("gemini-3.5-flash", 200, 100, 10)
	// 再追加 TrackRequestForPool(只写 Pools)
	tr.TrackRequestForPool("gemini-3.5-flash", 200, 100, 10, "antigravity")

	tr.RLock()
	defer tr.RUnlock()

	// 全局标量应只反映 TrackRequest 那次(不被 Pools 累加二次污染)
	if tr.stats.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1 (Pools 不应翻倍全局)", tr.stats.TotalRequests)
	}
	if tr.stats.TotalInputTokens != 200 {
		t.Errorf("TotalInputTokens = %d, want 200", tr.stats.TotalInputTokens)
	}
	if tr.stats.TotalCachedTokens != 10 {
		t.Errorf("TotalCachedTokens = %d, want 10", tr.stats.TotalCachedTokens)
	}
	if tr.stats.TotalCacheEligibleInputTokens != 200 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 200", tr.stats.TotalCacheEligibleInputTokens)
	}

	// 池口径应独立记录这 1 次
	ps := tr.stats.Pools["antigravity"]
	if ps == nil || ps.Requests != 1 || ps.CachedTokens != 10 || ps.CacheEligibleInputTokens != 200 {
		t.Errorf("Pools[antigravity] wrong: %+v", ps)
	}
}

// TestTrackRequestForPool_GroupID_Passthrough_Other 验证 Provider==other + GroupID 透传 → other:<gid> 桶。
func TestTrackRequestForPool_GroupID_Passthrough_Other(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)

	// 同组两次请求应累加到同一桶, 不同组分桶
	keyOpenAI := PoolKeyForProvider("other", "openai")
	keyDS := PoolKeyForProvider("other", "DEEPSEEK") // 大写经规整到 deepseek
	if keyOpenAI != "other:openai" || keyDS != "other:deepseek" {
		t.Fatalf("key mapping: %q %q", keyOpenAI, keyDS)
	}

	tr.TrackRequestForPool("gpt-4o", 1000, 200, 20, keyOpenAI)
	tr.TrackRequestForPool("gpt-4o", 500, 100, 10, keyOpenAI)
	tr.TrackRequestForPool("deepseek-chat", 300, 50, 0, keyDS)

	pools := tr.GetPoolStatsCopy()
	if ps := pools["other:openai"]; ps == nil || ps.Requests != 2 || ps.CachedTokens != 30 || ps.CacheEligibleInputTokens != 1500 {
		t.Errorf("other:openai wrong: %+v", ps)
	}
	if ps := pools["other:deepseek"]; ps == nil || ps.Requests != 1 || ps.CachedTokens != 0 || ps.CacheEligibleInputTokens != 300 {
		t.Errorf("other:deepseek wrong: %+v", ps)
	}
}

// TestTrackRequestForPool_NegativeTokensClamped 负值规整(防御性)。
func TestTrackRequestForPool_NegativeTokensClamped(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.TrackRequestForPool("gemini-2.5-flash", -10, -5, -3, "antigravity")

	pools := tr.GetPoolStatsCopy()
	if ps := pools["antigravity"]; ps == nil ||
		ps.InTokens != 0 || ps.OutTokens != 0 || ps.CachedTokens != 0 || ps.CacheEligibleInputTokens != 0 {
		t.Errorf("negative tokens not clamped: %+v", ps)
	}
}

// TestTrackRequestForPool_EmptyPoolKeyDefaultsAntigravity 空 poolKey 兜底归 antigravity。
func TestTrackRequestForPool_EmptyPoolKeyDefaultsAntigravity(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.TrackRequestForPool("gemini-2.5-flash", 100, 50, 5, "")

	pools := tr.GetPoolStatsCopy()
	if ps := pools["antigravity"]; ps == nil || ps.Requests != 1 {
		t.Errorf("empty poolKey should default to antigravity, got: %+v", pools)
	}
}

// TestGetPayload_PoolsProjectedDown 验证 GetPayload 深拷贝 Pools 下行, 且与内部 map 不共享指针。
func TestGetPayload_PoolsProjectedDown(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.TrackRequestForPool("gemini-2.5-flash", 200, 100, 10, "antigravity")
	tr.TrackRequestForPool("z-ai/glm-5.2", 500, 250, 0, "nvidia")

	payload := tr.GetPayload(nil)
	statsObj, ok := payload["stats"].(GlobalStats)
	if !ok {
		t.Fatalf("payload stats wrong type: %T", payload["stats"])
	}
	if statsObj.Pools == nil {
		t.Fatal("payload Pools nil")
	}
	if ps := statsObj.Pools["antigravity"]; ps == nil || ps.CachedTokens != 10 {
		t.Errorf("payload Pools[antigravity] wrong: %+v", ps)
	}
	if ps := statsObj.Pools["nvidia"]; ps == nil || ps.InTokens != 500 {
		t.Errorf("payload Pools[nvidia] wrong: %+v", ps)
	}

	// 不共享指针: 改动 payload 不应影响内部
	statsObj.Pools["antigravity"].CachedTokens = 999
	tr.RLock()
	internal := tr.stats.Pools["antigravity"].CachedTokens
	tr.RUnlock()
	if internal == 999 {
		t.Error("GetPayload leaked internal PoolStats pointer (should be deep copy)")
	}
}

// TestGetPayloadSimplified_PoolsProjectedDown 验证简化热路径也带 Pools。
func TestGetPayloadSimplified_PoolsProjectedDown(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	tr.TrackRequestForPool("gemini-2.5-flash", 200, 100, 10, "antigravity")

	payload := tr.GetPayloadSimplified(nil)
	statsObj, ok := payload["stats"].(GlobalStats)
	if !ok {
		t.Fatalf("simplified stats wrong type: %T", payload["stats"])
	}
	if statsObj.Pools == nil || statsObj.Pools["antigravity"] == nil {
		t.Error("simplified payload missing Pools")
	}
}

// TestSaveLoad_PoolsRoundtrip 验证 SaveToDisk→LoadFromDisk 的 Pools 字段完整往返。
func TestSaveLoad_PoolsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	pm := pricing.NewManager()
	tr := NewTracker(pm)
	tr.Init(dir) // 设 persistPath 并 LoadFromDisk(空)
	tr.TrackRequestForPool("gemini-2.5-flash", 200, 100, 10, "antigravity")
	tr.TrackRequestForPool("z-ai/glm-5.2", 500, 250, 0, "nvidia")
	tr.TrackRequestForPool("deepseek-chat", 400, 200, 30, "other:deepseek")
	tr.SaveToDisk()

	tr2 := NewTracker(pm)
	tr2.Init(dir) // 从磁盘读回

	pools := tr2.GetPoolStatsCopy()
	if len(pools) != 3 {
		t.Fatalf("after reload want 3 pools, got %d: %+v", len(pools), pools)
	}
	if ps := pools["antigravity"]; ps == nil || ps.CachedTokens != 10 || ps.CacheEligibleInputTokens != 200 {
		t.Errorf("reload antigravity wrong: %+v", ps)
	}
	if ps := pools["nvidia"]; ps == nil || ps.InTokens != 500 {
		t.Errorf("reload nvidia wrong: %+v", ps)
	}
	if ps := pools["other:deepseek"]; ps == nil || ps.CachedTokens != 30 {
		t.Errorf("reload other:deepseek wrong: %+v", ps)
	}
}

// TestLoadFromDisk_OldStatsJSON_NoPoolsNoCrash 验证旧 stats.json 无 pools 字段时兜底为空 map, 不崩溃,
// 且旧全局标量正常读回(零回归)。
func TestLoadFromDisk_OldStatsJSON_NoPoolsNoCrash(t *testing.T) {
	dir := t.TempDir()
	// 手写一份旧格式 stats.json(无 pools 字段)
	oldStats := map[string]interface{}{
		"stats": map[string]interface{}{
			"totalRequests":                 42,
			"totalInputTokens":               1000,
			"totalOutputTokens":              500,
			"totalCachedTokens":               100,
			"totalCacheEligibleInputTokens":   1000,
			"totalCost":                       0.5,
			"models": map[string]interface{}{},
		},
		"trends": []interface{}{},
	}
	raw, _ := json.Marshal(oldStats)
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}

	tr := NewTracker(pricing.NewManager())
	tr.Init(dir)

	tr.RLock()
	if tr.stats.Pools == nil {
		t.Error("Pools should be non-nil empty map after loading old json")
	}
	if len(tr.stats.Pools) != 0 {
		t.Errorf("Pools should be empty, got %d", len(tr.stats.Pools))
	}
	if tr.stats.TotalRequests != 42 {
		t.Errorf("old TotalRequests not restored: %d", tr.stats.TotalRequests)
	}
	if tr.stats.TotalCachedTokens != 100 {
		t.Errorf("old TotalCachedTokens not restored: %d", tr.stats.TotalCachedTokens)
	}
	tr.RUnlock()

	// 兜底后继续 TrackRequestForPool 应正常累加(不 panic on nil map)
	tr.TrackRequestForPool("gemini-2.5-flash", 100, 50, 5, "antigravity")
	if ps := tr.GetPoolStatsCopy()["antigravity"]; ps == nil || ps.Requests != 1 {
		t.Errorf("post-bucket accrue failed: %+v", ps)
	}
}

// TestTrackRequestForPool_ConcurrentSafe 并发调用不 panic(验证锁正确)。
func TestTrackRequestForPool_ConcurrentSafe(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t)
	const goroutines = 50
	const each = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			key := PoolKeyForProvider("other", "grp")
			for j := 0; j < each; j++ {
				tr.TrackRequestForPool("deepseek-chat", 10, 5, 1, key)
			}
		}(i)
	}
	wg.Wait()

	ps := tr.GetPoolStatsCopy()["other:grp"]
	if ps == nil {
		t.Fatal("missing other:grp")
	}
	if ps.Requests != goroutines*each {
		t.Errorf("concurrent Requests = %d, want %d", ps.Requests, goroutines*each)
	}
	if ps.CachedTokens != goroutines*each {
		t.Errorf("concurrent CachedTokens = %d, want %d", ps.CachedTokens, goroutines*each)
	}
}

// TestTrackRequestForPool_NoScheduleSavePanicsWithEmptyPath persistPath="" 时 scheduleSave
// 内部 time.AfterFunc 仍会调度 SaveToDisk(空 path 直接 return),不会 panic。
func TestTrackRequestForPool_NoScheduleSavePanicsWithEmptyPath(t *testing.T) {
	tr := newTestTrackerWithNoPersist(t) // persistPath = ""
	for i := 0; i < 10; i++ {
		tr.TrackRequestForPool("gemini-2.5-flash", 10, 5, 1, "antigravity")
	}
	// 给 AfterFunc 一点触发窗口(不强制等待, 仅验证不 panic)
	time.Sleep(50 * time.Millisecond)
	if ps := tr.GetPoolStatsCopy()["antigravity"]; ps == nil || ps.Requests != 10 {
		t.Errorf("accrue under empty persistPath wrong: %+v", ps)
	}
}

// --- helpers ---

const (
	in200    = 200
	out100   = 100
	cached10 = 10
)

func hitRatePercent(ps *PoolStats) float64 {
	if ps == nil || ps.CacheEligibleInputTokens <= 0 {
		return 0
	}
	return float64(ps.CachedTokens) / float64(ps.CacheEligibleInputTokens) * 100.0
}

// 防御 unused import(strings 在某些 helper 未用时仍可能被引用, 显式保留以示规整来源)。
var _ = strings.TrimSpace
