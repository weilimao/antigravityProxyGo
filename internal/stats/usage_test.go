package stats

import (
	"os"
	"testing"

	"antigravity-proxy/internal/pricing"
)

func TestUsageTracker_GetPayload_MergeDuplicateAccounts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "usage_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := pricing.NewManager()
	ut := NewUsageTracker(pm)
	ut.Init(tempDir)

	// Simulate two account sessions/records with the same Email + Provider but different AccountIDs (e.g. after deletion and re-add)
	acc1 := &AccountMeta{
		ID:       "id-old-timestamp",
		Email:    "test@example.com",
		Provider: "antigravity",
	}

	acc2 := &AccountMeta{
		ID:       "id-new-timestamp",
		Email:    "test@example.com",
		Provider: "antigravity",
	}

	time1 := "2026-06-26T20:00:00+08:00"
	time2 := "2026-06-26T21:00:00+08:00"

	// Record usage for old session
	ut.RecordUsage(UsageSample{
		ModelName:    "gemini-1.5-pro",
		InTokens:     1000,
		OutTokens:    500,
		CachedTokens: 200,
		Timestamp:    time1,
		Account:      acc1,
	})

	// Record usage for new session
	ut.RecordUsage(UsageSample{
		ModelName:    "gemini-1.5-pro",
		InTokens:     2000,
		OutTokens:    1000,
		CachedTokens: 400,
		Timestamp:    time2,
		Account:      acc2,
	})

	// Get payload
	payload := ut.GetPayload()
	state, ok := payload.(UsageState)
	if !ok {
		t.Fatalf("expected UsageState from GetPayload, got %T", payload)
	}

	// Verify that the accounts were merged
	if len(state.Accounts) != 1 {
		t.Errorf("expected 1 merged account in payload, got %d", len(state.Accounts))
	}

	// The key should be strings.ToLower(email) + ":" + strings.ToLower(provider)
	mergedKey := "test@example.com:antigravity"
	merged, exists := state.Accounts[mergedKey]
	if !exists {
		t.Fatalf("expected merged account with key %q to exist", mergedKey)
	}

	// Verify stats accumulation
	expectedReqCount := 2
	if merged.RequestCount != expectedReqCount {
		t.Errorf("expected RequestCount %d, got %d", expectedReqCount, merged.RequestCount)
	}

	expectedInTokens := 3000
	if merged.InputTokens != expectedInTokens {
		t.Errorf("expected InputTokens %d, got %d", expectedInTokens, merged.InputTokens)
	}

	expectedOutTokens := 1500
	if merged.OutputTokens != expectedOutTokens {
		t.Errorf("expected OutputTokens %d, got %d", expectedOutTokens, merged.OutputTokens)
	}

	expectedCachedTokens := 600
	if merged.CachedTokens != expectedCachedTokens {
		t.Errorf("expected CachedTokens %d, got %d", expectedCachedTokens, merged.CachedTokens)
	}

	// Verify time tracking picks the newer one
	if merged.LastUsedAt != time2 {
		t.Errorf("expected LastUsedAt to be %q, got %q", time2, merged.LastUsedAt)
	}

	// Verify model level stats are also merged
	modelStat, ok := merged.Models["gemini-1.5-pro"]
	if !ok {
		t.Fatalf("expected model stats for gemini-1.5-pro to exist")
	}

	if modelStat.RequestCount != 2 {
		t.Errorf("expected model RequestCount 2, got %d", modelStat.RequestCount)
	}
	if modelStat.InputTokens != 3000 {
		t.Errorf("expected model InputTokens 3000, got %d", modelStat.InputTokens)
	}
}

// TestUsageTracker_RenameAccountByID_ReflectsNewLabelAndReusesBucket 验证改名联动核心契约:
// 「使用详情」桶按账号 ID 归集,改名仅同步展示名副本,
// 不重建/不分裂桶、不产生重复桶、不新增请求即让 GetPayload 反映新名。
func TestUsageTracker_RenameAccountByID_ReflectsNewLabelAndReusesBucket(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "usage_rename_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := pricing.NewManager()
	ut := NewUsageTracker(pm)
	ut.Init(tempDir)

	acc := &AccountMeta{
		ID:       "acc-001",
		Email:    "old-name@x.com",
		Provider: "nvidia",
	}
	ut.RecordUsage(UsageSample{
		ModelName:    "moonshotai/kimi-k2.5",
		InTokens:     1000,
		OutTokens:    500,
		CachedTokens: 0,
		Timestamp:    "2026-08-09T10:00:00+08:00",
		Account:      acc,
	})

	// 改名:before 命中 old-name 的桶, after 同步为新名。
	ut.RenameAccountByID("acc-001", "new-name@x.com")

	payload := ut.GetPayload()
	state, ok := payload.(UsageState)
	if !ok {
		t.Fatalf("expected UsageState from GetPayload, got %T", payload)
	}

	// 1) 不分裂/不重复:改名后仍只有一个桶(mergeKey 由 email+provider 推,改名前后唯一邮箱各成一键,故总数为 1)。
	if len(state.Accounts) != 1 {
		t.Fatalf("expected 1 account bucket after rename, got %d", len(state.Accounts))
	}

	// 2) 展示名即时刷新为新名,旧名桶消失。
	oldKey := "old-name@x.com:nvidia"
	if _, exists := state.Accounts[oldKey]; exists {
		t.Errorf("old label bucket should be gone after rename, but key %q still present", oldKey)
	}
	newKey := "new-name@x.com:nvidia"
	bucket, exists := state.Accounts[newKey]
	if !exists {
		t.Fatalf("expected bucket keyed by new label %q to exist", newKey)
	}
	if bucket.Email != "new-name@x.com" {
		t.Errorf("expected Email to be new label, got %q", bucket.Email)
	}

	// 3) Token/成本数值零漂移:只改展示名副本,不动任何用量数字。
	if bucket.RequestCount != 1 {
		t.Errorf("expected RequestCount 1 (unchanged), got %d", bucket.RequestCount)
	}
	if bucket.InputTokens != 1000 {
		t.Errorf("expected InputTokens 1000 (unchanged), got %d", bucket.InputTokens)
	}
	if bucket.OutputTokens != 500 {
		t.Errorf("expected OutputTokens 500 (unchanged), got %d", bucket.OutputTokens)
	}

	// 4) 模型级子统计同样保留(改名不破坏模型聚合)。
	if _, ok := bucket.Models["moonshotai/kimi-k2.5"]; !ok {
		t.Errorf("expected model sub-stats to survive rename, but it is missing")
	}
}

// TestUsageTracker_RenameAccountByID_NoOpOnMissingBucket 验证目标桶不存在(该账号从未产生用量)时静默 no-op,不 panic。
func TestUsageTracker_RenameAccountByID_NoOpOnMissingBucket(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "usage_rename_missing_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := pricing.NewManager()
	ut := NewUsageTracker(pm)
	ut.Init(tempDir)

	before := ut.GetPayload()
	ut.RenameAccountByID("never-existed", "whatever@x.com")
	after := ut.GetPayload()

	beforeState := before.(UsageState)
	afterState := after.(UsageState)
	if len(afterState.Accounts) != len(beforeState.Accounts) {
		t.Errorf("rename on missing bucket should not change account count: before=%d after=%d",
			len(beforeState.Accounts), len(afterState.Accounts))
	}
}

// TestUsageTracker_RenameAccountByID_NoOpOnEmptyArgs 验证空 accountID 或空 newLabel 时不做变更(调用方须传权威最终名)。
func TestUsageTracker_RenameAccountByID_NoOpOnEmptyArgs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "usage_rename_empty_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := pricing.NewManager()
	ut := NewUsageTracker(pm)
	ut.Init(tempDir)

	acc := &AccountMeta{ID: "acc-empty", Email: "keep@x.com", Provider: "nvidia"}
	ut.RecordUsage(UsageSample{
		ModelName: "moonshotai/kimi-k2.5",
		InTokens:  10, OutTokens: 5, Timestamp: "2026-08-09T10:00:00+08:00", Account: acc,
	})

	// 空 newLabel:不应改名。
	ut.RenameAccountByID("acc-empty", "")
	// 空 accountID:不应改名。
	ut.RenameAccountByID("", "new@x.com")

	state := ut.GetPayload().(UsageState)
	bucket, exists := state.Accounts["keep@x.com:nvidia"]
	if !exists {
		t.Fatalf("expected original bucket keyed by keep@x.com to remain after empty-arg no-op")
	}
	if bucket.Email != "keep@x.com" {
		t.Errorf("Email should be unchanged after empty-arg no-op, got %q", bucket.Email)
	}
}

