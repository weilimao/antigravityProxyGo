package relay

import "time"

// FixedWindowBounds calculates the start and end of the current fixed window.
// periodHours is the window duration in hours (e.g. 5 for "5-hour quota", 168 for "7-day quota").
// Unlike a sliding window, the boundaries are deterministic and do not shift with each request.
func FixedWindowBounds(periodHours int) (windowStart time.Time, windowEnd time.Time) {
	if periodHours <= 0 {
		periodHours = 1
	}

	now := time.Now()
	// Use a fixed epoch anchor (2025-01-06 00:00 local time, a Monday) for consistent alignment.
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, now.Location())

	periodDuration := time.Duration(periodHours) * time.Hour
	elapsed := now.Sub(anchor)
	windowIndex := int64(elapsed / periodDuration)

	windowStart = anchor.Add(time.Duration(windowIndex) * periodDuration)
	windowEnd = windowStart.Add(periodDuration)

	return windowStart, windowEnd
}
