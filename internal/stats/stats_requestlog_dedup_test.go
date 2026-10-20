package stats

import (
	"fmt"
	"testing"
)

// mockPricingMgr 构造测试计费管理器
type dummyPricingMgr struct{}

func (d *dummyPricingMgr) CalculateCost(model string, in, out, cached int) float64 {
	return 0.01
}
func (d *dummyPricingMgr) GetPricingForModel(model string) struct{ Input, Output, Cached float64 } {
	return struct{ Input, Output, Cached float64 }{Input: 1.0, Output: 2.0, Cached: 0.1}
}

// TestTracker_DedupSameID 验证同一 reqID 重复入库时不会产生重复条目，且元数据能正确增量合并
func TestTracker_DedupSameID(t *testing.T) {
	tr := &Tracker{
		requests: make([]*RequestLog, 0),
	}

	reqID := fmt.Sprintf("req_test_dedup_%d", 1001)

	// 第 1 次推入: 客户端壳层产生的日志(Account 为空，SessionID 为 remote_session)
	log1 := &RequestLog{
		ID:           reqID,
		Path:         "/v1internal:streamGenerateContent",
		Model:        "gemini-3.8-flash-medium",
		SessionID:    "remote_session",
		Account:      "",
		InTokens:     50000,
		OutTokens:    100,
		CachedTokens: 40000,
		CacheStatus:  "HIT",
	}

	tr.Lock()
	isNew1 := tr.mergeOrPrependLocked(log1)
	tr.Unlock()

	if !isNew1 {
		t.Fatalf("first insert should be new")
	}
	if len(tr.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(tr.requests))
	}
	if !tr.HasRequest(reqID) {
		t.Fatalf("HasRequest(%s) should return true", reqID)
	}

	// 第 2 次推入: 服务端核心执行后产生的更详实元数据(包含真实账号与真实 sessionKey)
	log2 := &RequestLog{
		ID:           reqID,
		Path:         "/v1internal:streamGenerateContent",
		Model:        "gemini-3.8-flash-medium",
		SessionID:    "auth:acc:832fd78b16",
		Account:      "test-account@gmail.com",
		InTokens:     50000,
		OutTokens:    100,
		CachedTokens: 40000,
		CacheStatus:  "HIT",
	}

	tr.Lock()
	isNew2 := tr.mergeOrPrependLocked(log2)
	tr.Unlock()

	if isNew2 {
		t.Fatalf("second insert with same ID should not be considered new")
	}
	if len(tr.requests) != 1 {
		t.Fatalf("expected requests length to remain 1 after duplicate, got %d", len(tr.requests))
	}

	// 验证合并后的详实字段
	merged := tr.requests[0]
	if merged.Account != "test-account@gmail.com" {
		t.Fatalf("expected Account to be updated to 'test-account@gmail.com', got %q", merged.Account)
	}
	if merged.SessionID != "auth:acc:832fd78b16" {
		t.Fatalf("expected SessionID to be updated from 'remote_session' to 'auth:acc:832fd78b16', got %q", merged.SessionID)
	}
	if merged.CacheStatus != "HIT" {
		t.Fatalf("expected CacheStatus 'HIT', got %q", merged.CacheStatus)
	}
}
