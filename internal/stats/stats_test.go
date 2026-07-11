package stats

import (
	"testing"
	"time"

	"antigravity-proxy/internal/pricing"
)

func TestAddRequestLogInMemoryOnly(t *testing.T) {
	pm := pricing.NewManager()
	tracker := NewTracker(pm)
	tracker.persistPath = "" // Prevent file serialization during testing

	log := &RequestLog{
		ID:           "test_req_1",
		Timestamp:    time.Now().Format("01/02 15:04:05"),
		Method:       "POST",
		Host:         "api.openai.com",
		Path:         "/v1/chat/completions/generateContent", // satisfying isRealModel (contains generatecontent)
		Model:        "gemini-3.5-flash",
		Account:      "user_123",
		InTokens:     100,
		OutTokens:    50,
		CachedTokens: 20,
		StatusCode:   200,
		DurationMs:   250,
	}

	tracker.AddRequestLogInMemoryOnly(log)

	tracker.RLock()
	defer tracker.RUnlock()

	if len(tracker.requests) != 1 {
		t.Fatalf("expected 1 request in memory, got %d", len(tracker.requests))
	}

	saved := tracker.requests[0]
	if saved.ID != "test_req_1" {
		t.Errorf("expected ID 'test_req_1', got '%s'", saved.ID)
	}
	if saved.Account != "user_123" {
		t.Errorf("expected Account 'user_123', got '%s'", saved.Account)
	}
	if saved.InTokens != 100 || saved.OutTokens != 50 || saved.CachedTokens != 20 {
		t.Errorf("tokens mismatch")
	}
}
