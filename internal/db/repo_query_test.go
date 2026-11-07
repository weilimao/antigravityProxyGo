package db

import (
	"testing"
	"time"
)

func TestQueryAccountStatsSince_NilDB(t *testing.T) {
	prev := GlobalDB
	GlobalDB = nil
	defer func() { GlobalDB = prev }()

	res := QueryAccountStatsSince("")
	if res == nil {
		t.Fatalf("expected non-nil map when GlobalDB is nil")
	}
	if len(res) != 0 {
		t.Fatalf("expected empty map, got %d items", len(res))
	}
}

func TestQueryAccountStatsSince_Aggregation(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	now := time.Now()
	tPast := now.Add(-10 * time.Hour).Format(time.RFC3339)
	tRecent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	logs := []*RequestLog{
		// Alice 过去日志 (在 since 之前)
		{
			ReqID:        "req-1",
			Timestamp:    tPast,
			Mode:         "local",
			UserID:       "alice@example.com",
			ModelName:    "gemini-3.8-flash-high",
			InTokens:     1000,
			OutTokens:    200,
			CachedTokens: 800,
			Cost:         0.01,
		},
		// Alice 最近日志 1 (gemini-3.8-flash-high)
		{
			ReqID:        "req-2",
			Timestamp:    tRecent,
			Mode:         "local",
			UserID:       "alice@example.com",
			ModelName:    "gemini-3.8-flash-high",
			InTokens:     2000,
			OutTokens:    400,
			CachedTokens: 1500,
			Cost:         0.02,
		},
		// Alice 最近日志 2 (claude-opus-4-6)
		{
			ReqID:        "req-3",
			Timestamp:    tRecent,
			Mode:         "local",
			UserID:       "alice@example.com",
			ModelName:    "claude-opus-4-6",
			InTokens:     500,
			OutTokens:    100,
			CachedTokens: 400,
			Cost:         0.05,
		},
		// Bob 最近日志 (gemini-3.8-flash-high)
		{
			ReqID:        "req-4",
			Timestamp:    tRecent,
			Mode:         "local",
			UserID:       "bob@example.com",
			ModelName:    "gemini-3.8-flash-high",
			InTokens:     3000,
			OutTokens:    600,
			CachedTokens: 2500,
			Cost:         0.03,
		},
		// 直连最近日志 (UserID 为空)
		{
			ReqID:        "req-5",
			Timestamp:    tRecent,
			Mode:         "local",
			UserID:       "",
			ModelName:    "tab_flash_lite_preview",
			InTokens:     100,
			OutTokens:    20,
			CachedTokens: 50,
			Cost:         0.001,
		},
	}

	for _, l := range logs {
		if err := InsertRequestLog(l); err != nil {
			t.Fatalf("InsertRequestLog(%s) failed: %v", l.ReqID, err)
		}
	}

	// 1. 全量查询 (sinceISO == "")
	allRes := QueryAccountStatsSince("")
	if len(allRes) != 3 {
		t.Fatalf("expected 3 accounts (alice, bob, 直连), got %d", len(allRes))
	}
	aliceAll, ok := allRes["alice@example.com"]
	if !ok {
		t.Fatalf("expected alice in allRes")
	}
	if aliceAll.RequestCount != 3 {
		t.Errorf("alice total requests expected 3, got %d", aliceAll.RequestCount)
	}
	if aliceAll.InputTokens != 3500 {
		t.Errorf("alice total inTokens expected 3500, got %d", aliceAll.InputTokens)
	}
	if aliceAll.OutputTokens != 700 {
		t.Errorf("alice total outTokens expected 700, got %d", aliceAll.OutputTokens)
	}
	if aliceAll.CachedTokens != 2700 {
		t.Errorf("alice total cachedTokens expected 2700, got %d", aliceAll.CachedTokens)
	}
	if len(aliceAll.Models) != 2 {
		t.Errorf("alice models count expected 2, got %d", len(aliceAll.Models))
	}

	// 2. 按时间切片查询 (仅最近 2 小时)
	since2h := now.Add(-2 * time.Hour).Format(time.RFC3339)
	recentRes := QueryAccountStatsSince(since2h)
	if len(recentRes) != 3 {
		t.Fatalf("expected 3 accounts in recent, got %d", len(recentRes))
	}
	aliceRecent := recentRes["alice@example.com"]
	if aliceRecent.RequestCount != 2 {
		t.Errorf("alice recent requests expected 2, got %d", aliceRecent.RequestCount)
	}
	if aliceRecent.InputTokens != 2500 {
		t.Errorf("alice recent inTokens expected 2500, got %d", aliceRecent.InputTokens)
	}
	if aliceRecent.OutputTokens != 500 {
		t.Errorf("alice recent outTokens expected 500, got %d", aliceRecent.OutputTokens)
	}
	if aliceRecent.CachedTokens != 1900 {
		t.Errorf("alice recent cachedTokens expected 1900, got %d", aliceRecent.CachedTokens)
	}
	geminiModel := aliceRecent.Models["gemini-3.8-flash-high"]
	if geminiModel == nil || geminiModel.RequestCount != 1 || geminiModel.InputTokens != 2000 {
		t.Errorf("geminiModel stats mismatch: %+v", geminiModel)
	}
	claudeModel := aliceRecent.Models["claude-opus-4-6"]
	if claudeModel == nil || claudeModel.RequestCount != 1 || claudeModel.TotalCost != 0.05 {
		t.Errorf("claudeModel stats mismatch: %+v", claudeModel)
	}

	// 3. 校验空 UserID 转换为 "直连"
	directRecent, directOk := recentRes["直连"]
	if !directOk {
		t.Fatalf("expected '直连' account key for empty UserID")
	}
	if directRecent.RequestCount != 1 || directRecent.Models["tab_flash_lite_preview"] == nil {
		t.Errorf("direct account stats mismatch: %+v", directRecent)
	}
}
