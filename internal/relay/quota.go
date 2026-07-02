package relay

import (
	"antigravity-proxy/internal/db"
	"time"
)

// GetActiveWindow calculates the active window usage for a user and quota type.
// If isRequest is true, it starts a new window if the current one is expired or doesn't exist.
func GetActiveWindow(userID string, familyKeyword string, quotaType string, periodHours float64, isRequest bool) (int64, string, error) {
	if periodHours <= 0 {
		return 0, "", nil
	}
	periodDuration := time.Duration(periodHours * float64(time.Hour))
	now := time.Now()

	windowStartStr, _ := db.GetQuotaWindowStart(userID, quotaType)
	
	// Fallback/Migration: If windowStartStr is empty, try to find the oldest request in the last periodDuration
	if windowStartStr == "" {
		fallbackSince := now.Add(-periodDuration).Format(time.RFC3339)
		if oldestStr, err := db.GetOldestRequestTimestampSince(userID, familyKeyword, fallbackSince); err == nil && oldestStr != "" {
			windowStartStr = oldestStr
			// Migrate the discovered window start into the quota_windows table
			_ = db.SetQuotaWindowStart(userID, quotaType, windowStartStr)
		}
	}
	
	var windowStart time.Time
	var resetAt time.Time
	
	if windowStartStr != "" {
		if parsedStart, err := time.Parse(time.RFC3339, windowStartStr); err == nil {
			windowStart = parsedStart
			resetAt = windowStart.Add(periodDuration)
		}
	}
	
	// If there's no valid window_start, or the reset_at has already passed, the window has expired.
	if windowStart.IsZero() || !now.Before(resetAt) {
		if !isRequest {
			// Not a real request, so we don't start a window. Quota is full (0 used).
			return 0, "", nil
		}
		// Start a new window
		windowStart = now
		resetAt = windowStart.Add(periodDuration)
		_ = db.SetQuotaWindowStart(userID, quotaType, windowStart.Format(time.RFC3339))
	}
	
	since := windowStart.Format(time.RFC3339)
	usedTokens, err := db.GetTokensForUserModelFamilySince(userID, familyKeyword, since)
	if err != nil {
		return 0, "", err
	}
	
	return usedTokens, resetAt.Format(time.RFC3339), nil
}
