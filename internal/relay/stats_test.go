package relay

import (
	"testing"

	"antigravity-proxy/internal/pricing"
)

// TestRecordUsage_TotalCacheEligibleInputTokens_NvidiaExcluded 验证中继端统计的缓存命中率分母
// 累加器 RelayUserStats.TotalCacheEligibleInputTokens 口径: 仅当模型名不带 "nvidia/" 前缀
// (gemini/claude 链路) 时累加 inputTokens; NVIDIA 号池(modelName 带 "nvidia/" 前缀)排除,
// 避免上游 OpenAI Chat 协议无 cache 的请求稀释远端 /api/stats 下发的命中率分母。
//
// 落点对应: recordNvidiaUsage → StatsTracker.RecordUsage, NVIDIA 模型名在 nvidia_usage.go
// 装配处统一加 "nvidia/" 前缀(prefixedModel); gemini/claude 链路经 app.go proxyHandler 回调
// 喂入本方法时模型名不含该前缀。判定依据即 strings.HasPrefix(modelName, "nvidia/")。
func TestRecordUsage_TotalCacheEligibleInputTokens_NvidiaExcluded(t *testing.T) {
	st := NewStatsTracker(pricing.NewManager())
	st.persistPath = "" // Prevent file serialization during testing

	// gemini 链路: input=200, cached=10 → 分母累加 200
	st.RecordUsage(RelaySample{
		ReqID:       "gemini-1",
		UserID:      "u-gemini",
		ModelName:   "gemini-3.5-flash",
		InTokens:    200,
		OutTokens:   100,
		CachedTokens: 10,
		StatusCode:  200,
	})
	// NVIDIA 链路(带 nvidia/ 前缀): input=500, cached=0 → 分母不应累加
	st.RecordUsage(RelaySample{
		ReqID:       "nv-1",
		UserID:      "u-nvidia",
		ModelName:   "nvidia/z-ai/glm-5.2",
		InTokens:    500,
		OutTokens:   250,
		CachedTokens: 0,
		StatusCode:  200,
	})

	st.RLock()
	defer st.RUnlock()

	// gemini 用户: 分母 = 200
	gBucket, ok := st.users["u-gemini"]
	if !ok {
		t.Fatal("gemini user bucket missing")
	}
	if gBucket.TotalCacheEligibleInputTokens != 200 {
		t.Errorf("gemini TotalCacheEligibleInputTokens = %d, want 200", gBucket.TotalCacheEligibleInputTokens)
	}
	if gBucket.TotalInputTokens != 200 {
		t.Errorf("gemini TotalInputTokens = %d, want 200", gBucket.TotalInputTokens)
	}

	// NVIDIA 用户: 分母 = 0(排除), 总输入仍累加 500
	nvBucket, ok := st.users["u-nvidia"]
	if !ok {
		t.Fatal("nvidia user bucket missing")
	}
	if nvBucket.TotalCacheEligibleInputTokens != 0 {
		t.Errorf("nvidia TotalCacheEligibleInputTokens = %d, want 0 (NVIDIA excluded from cache-eligible denom)",
			nvBucket.TotalCacheEligibleInputTokens)
	}
	if nvBucket.TotalInputTokens != 500 {
		t.Errorf("nvidia TotalInputTokens = %d, want 500", nvBucket.TotalInputTokens)
	}
}

// TestRecordUsage_TotalCacheEligibleInputTokens_AccumulatesGemini 验证多次 gemini 请求
// 在同一用户桶内正确累加分母, 与 TotalInputTokens 同步递增(口径一致, 只差 NVIDIA 排除)。
func TestRecordUsage_TotalCacheEligibleInputTokens_AccumulatesGemini(t *testing.T) {
	st := NewStatsTracker(pricing.NewManager())
	st.persistPath = ""

	st.RecordUsage(RelaySample{ReqID: "g1", UserID: "u", ModelName: "gemini-3.5-flash", InTokens: 200, OutTokens: 50, CachedTokens: 20, StatusCode: 200})
	st.RecordUsage(RelaySample{ReqID: "g2", UserID: "u", ModelName: "claude-opus-4", InTokens: 100, OutTokens: 30, CachedTokens: 0, StatusCode: 200})

	st.RLock()
	defer st.RUnlock()

	bucket, ok := st.users["u"]
	if !ok {
		t.Fatal("user bucket missing")
	}
	// 两次 gemini/claude 累加: 200 + 100 = 300, 与 TotalInputTokens 等值(本用例无 NVIDIA)。
	if bucket.TotalCacheEligibleInputTokens != 300 {
		t.Errorf("TotalCacheEligibleInputTokens = %d, want 300", bucket.TotalCacheEligibleInputTokens)
	}
	if bucket.TotalInputTokens != 300 {
		t.Errorf("TotalInputTokens = %d, want 300", bucket.TotalInputTokens)
	}
}
