package service

import (
	"encoding/json"
	"testing"
)

func TestAdminLogSummary_DualPopulation(t *testing.T) {
	rawGatewayJSON := `{
		"totalRequests": 179,
		"totalInputTokens": 3645246,
		"totalOutputTokens": 55894,
		"totalCachedTokens": 1144320,
		"totalCost": 0.739559,
		"cacheHitRate": 31.4
	}`

	var summary AdminLogSummary
	if err := json.Unmarshal([]byte(rawGatewayJSON), &summary); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if summary.InputTokens == 0 && summary.TotalInputTokens > 0 {
		summary.InputTokens = summary.TotalInputTokens
	}
	if summary.OutputTokens == 0 && summary.TotalOutputTokens > 0 {
		summary.OutputTokens = summary.TotalOutputTokens
	}
	if summary.CachedTokens == 0 && summary.TotalCachedTokens > 0 {
		summary.CachedTokens = summary.TotalCachedTokens
	}

	if summary.InputTokens != 3645246 {
		t.Errorf("expected InputTokens 3645246, got %d", summary.InputTokens)
	}
	if summary.OutputTokens != 55894 {
		t.Errorf("expected OutputTokens 55894, got %d", summary.OutputTokens)
	}
	if summary.CachedTokens != 1144320 {
		t.Errorf("expected CachedTokens 1144320, got %d", summary.CachedTokens)
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map error: %v", err)
	}

	if m["totalInputTokens"] != float64(3645246) {
		t.Errorf("missing or incorrect totalInputTokens: %v", m["totalInputTokens"])
	}
	if m["inputTokens"] != float64(3645246) {
		t.Errorf("missing or incorrect inputTokens: %v", m["inputTokens"])
	}
	if m["totalOutputTokens"] != float64(55894) {
		t.Errorf("missing or incorrect totalOutputTokens: %v", m["totalOutputTokens"])
	}
	if m["outputTokens"] != float64(55894) {
		t.Errorf("missing or incorrect outputTokens: %v", m["outputTokens"])
	}
	if m["totalCachedTokens"] != float64(1144320) {
		t.Errorf("missing or incorrect totalCachedTokens: %v", m["totalCachedTokens"])
	}
	if m["cachedTokens"] != float64(1144320) {
		t.Errorf("missing or incorrect cachedTokens: %v", m["cachedTokens"])
	}
}
