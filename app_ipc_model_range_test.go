package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/stats"
)

// TestIPCModelRange_All_ReturnsAuthoritativeStats 验证在 'all' 范围下:
// 即使 SQLite request_logs 被剪枝淘汰只留局部数据，stats:model-range 仍权威返回
// statsTracker 中的全量历史模型数据，彻底杜绝历史模型丢失。
func TestIPCModelRange_All_ReturnsAuthoritativeStats(t *testing.T) {
	tempDir := t.TempDir()
	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	defer db.CloseDB()

	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)

	tracker := stats.NewTracker(pricingMgr)
	tracker.Init(tempDir)

	// 在 statsTracker 累积 3 个历史模型
	tracker.TrackRequest("claude-opus-4-6-thinking", 1000, 200, 500)
	tracker.TrackRequest("deepseek-ai/deepseek-v4-flash", 2000, 300, 800)
	tracker.TrackRequest("gemini-3.8-flash-high", 3000, 400, 1500)

	// 模拟 SQLite 遭 FIFO 剪枝仅剩 1 条局部记录
	prunedLog := &db.RequestLog{
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "local",
		UserID:       "user-1",
		Method:       "POST",
		Host:         "api.google.com",
		Path:         "/v1/chat",
		ModelName:    "gemini-3.8-flash-high",
		InTokens:     10,
		OutTokens:    5,
		CachedTokens: 2,
		Cost:         0.0001,
		StatusCode:   200,
	}
	if err := db.InsertRequestLog(prunedLog); err != nil {
		t.Fatalf("插入测试日志失败: %v", err)
	}

	app := &App{
		ctx:          context.Background(),
		statsTracker: tracker,
	}

	// 1. 测试明确传入 "all"
	resRaw, handled, err := app.handleIOInvokeIPC("stats:model-range", []interface{}{"all"})
	if err != nil {
		t.Fatalf("handleIOInvokeIPC 报错: %v", err)
	}
	if !handled {
		t.Fatalf("stats:model-range 未被处理")
	}

	var res struct {
		Range string `json:"range"`
		Stats struct {
			Models map[string]*stats.ModelStats `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(resRaw), &res); err != nil {
		t.Fatalf("反序列化响应失败: %v", err)
	}

	if res.Range != "all" {
		t.Fatalf("期望 range 为 all, 实际为 %s", res.Range)
	}
	if len(res.Stats.Models) != 3 {
		t.Fatalf("期望返回 3 个全量模型, 实际只返回了 %d 个: %v", len(res.Stats.Models), res.Stats.Models)
	}
	if _, ok := res.Stats.Models["claude-opus-4-6-thinking"]; !ok {
		t.Errorf("全量模型中缺失 claude-opus-4-6-thinking")
	}
	if _, ok := res.Stats.Models["deepseek-ai/deepseek-v4-flash"]; !ok {
		t.Errorf("全量模型中缺失 deepseek-ai/deepseek-v4-flash")
	}
	if _, ok := res.Stats.Models["gemini-3.8-flash-high"]; !ok {
		t.Errorf("全量模型中缺失 gemini-3.8-flash-high")
	}

	// 2. 测试传入空串 "" 同样回退为全量
	resRawEmpty, handledEmpty, errEmpty := app.handleIOInvokeIPC("stats:model-range", []interface{}{""})
	if errEmpty != nil || !handledEmpty {
		t.Fatalf("stats:model-range 空串测试失败: %v", errEmpty)
	}
	var resEmpty struct {
		Stats struct {
			Models map[string]*stats.ModelStats `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(resRawEmpty), &resEmpty); err != nil {
		t.Fatalf("反序列化空串响应失败: %v", err)
	}
	if len(resEmpty.Stats.Models) != 3 {
		t.Fatalf("空串下期望返回 3 个全量模型, 实际只返回了 %d 个", len(resEmpty.Stats.Models))
	}
}

// TestIPCModelRange_TimeWindows_QueriesDB 验证在 'today'/'3d'/'7d' 范围下:
// 正常走 SQLite request_logs 时间窗口切片聚合。
func TestIPCModelRange_TimeWindows_QueriesDB(t *testing.T) {
	tempDir := t.TempDir()
	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	defer db.CloseDB()

	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)

	tracker := stats.NewTracker(pricingMgr)
	tracker.Init(tempDir)

	now := time.Now()

	// 插入今日请求: model-today
	todayLog := &db.RequestLog{
		Timestamp:    now.Format(time.RFC3339),
		Mode:         "local",
		UserID:       "user-1",
		Method:       "POST",
		Host:         "api.today.com",
		Path:         "/v1/chat",
		ModelName:    "model-today",
		InTokens:     100,
		OutTokens:    50,
		CachedTokens: 0,
		Cost:         0.001,
		StatusCode:   200,
	}
	if err := db.InsertRequestLog(todayLog); err != nil {
		t.Fatalf("插入今日日志失败: %v", err)
	}

	// 插入 5 天前请求: model-5days-ago
	fiveDaysAgo := now.Add(-5 * 24 * time.Hour)
	pastLog := &db.RequestLog{
		Timestamp:    fiveDaysAgo.Format(time.RFC3339),
		Mode:         "local",
		UserID:       "user-1",
		Method:       "POST",
		Host:         "api.past.com",
		Path:         "/v1/chat",
		ModelName:    "model-5days-ago",
		InTokens:     200,
		OutTokens:    80,
		CachedTokens: 0,
		Cost:         0.002,
		StatusCode:   200,
	}
	if err := db.InsertRequestLog(pastLog); err != nil {
		t.Fatalf("插入过去日志失败: %v", err)
	}

	app := &App{
		ctx:          context.Background(),
		statsTracker: tracker,
	}

	// 1. 验证 'today': 只应包含 model-today, 不应包含 model-5days-ago
	resTodayRaw, _, err := app.handleIOInvokeIPC("stats:model-range", []interface{}{"today"})
	if err != nil {
		t.Fatalf("handleIOInvokeIPC today 报错: %v", err)
	}
	var resToday struct {
		Range string `json:"range"`
		Stats struct {
			Models map[string]*db.ModelStatsSummary `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(resTodayRaw), &resToday); err != nil {
		t.Fatalf("反序列化 today 响应失败: %v", err)
	}
	if _, ok := resToday.Stats.Models["model-today"]; !ok {
		t.Errorf("today 聚合应包含 model-today")
	}
	if _, ok := resToday.Stats.Models["model-5days-ago"]; ok {
		t.Errorf("today 聚合不应包含 5 天前的 model-5days-ago")
	}

	// 2. 验证 '7d': 应同时包含 model-today 和 model-5days-ago
	res7dRaw, _, err := app.handleIOInvokeIPC("stats:model-range", []interface{}{"7d"})
	if err != nil {
		t.Fatalf("handleIOInvokeIPC 7d 报错: %v", err)
	}
	var res7d struct {
		Range string `json:"range"`
		Stats struct {
			Models map[string]*db.ModelStatsSummary `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(res7dRaw), &res7d); err != nil {
		t.Fatalf("反序列化 7d 响应失败: %v", err)
	}
	if _, ok := res7d.Stats.Models["model-today"]; !ok {
		t.Errorf("7d 聚合应包含 model-today")
	}
	if _, ok := res7d.Stats.Models["model-5days-ago"]; !ok {
		t.Errorf("7d 聚合应包含 model-5days-ago")
	}
}
