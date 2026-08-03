package relay

import (
	"sync"
	"time"
)

// compat_ratelimit.go: 用户级请求速率限制器 RateLimiter。
// 从 compat.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

type RateLimiter struct {
	mu           sync.Mutex
	userRequests map[string][]time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		userRequests: make(map[string][]time.Time),
	}
}

func (l *RateLimiter) Allow(userID string, limit int) bool {
	if limit <= 0 {
		limit = 30
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	oneMinuteAgo := now.Add(-1 * time.Minute)

	reqs := l.userRequests[userID]
	var validReqs []time.Time
	for _, t := range reqs {
		if t.After(oneMinuteAgo) {
			validReqs = append(validReqs, t)
		}
	}

	if len(validReqs) >= limit {
		l.userRequests[userID] = validReqs
		return false
	}

	validReqs = append(validReqs, now)
	l.userRequests[userID] = validReqs
	return true
}


