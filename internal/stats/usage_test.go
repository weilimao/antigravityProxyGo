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
