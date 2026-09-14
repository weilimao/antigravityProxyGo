package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

func TestApp_Monitor_RemoteIsolation(t *testing.T) {
	tempDir := t.TempDir()

	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)

	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)

	localTracker := stats.NewTracker(pricingMgr)
	localTracker.Init(tempDir)

	// 本地记录 5 条请求，模拟本地大盘已有历史数据
	for i := 0; i < 5; i++ {
		localTracker.TrackRequest("gemini-3.8-flash-high", 1000, 200, 500)
	}

	app := &App{
		pricingMgr:   pricingMgr,
		settingsMgr:  settingsMgr,
		statsTracker: localTracker,
		usageTracker: stats.NewUsageTracker(pricingMgr),
	}
	app.usageTracker.Init(tempDir)

	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	// 测试 1: 在纯本地单机模式下，getStatsPayload 必须准确返回本地的 5 条请求
	localPayload := app.getStatsPayload(false)
	lStats, ok := localPayload["stats"].(stats.GlobalStats)
	if !ok || lStats.TotalRequests != 5 {
		t.Fatalf("expected local stats to have 5 requests, got %+v", localPayload["stats"])
	}

	// 测试 2: 切换至服务器模式，远端中继服务返回 0 请求
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/stats" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"totalRequests": 0,
				"totalInputTokens": 0,
				"models": map[string]interface{}{},
			})
			return
		}
		if r.URL.Path == "/api/trends" {
			_ = json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(mockServer.Close)

	serverURL, _ := url.Parse(mockServer.URL)
	app.remoteRelay = proxy.NewRemoteRelay(nil)
	app.remoteRelay.SetConfigForTest(proxy.RemoteConfig{
		Connected: true,
		Host:      serverURL.Hostname(),
		Port:      serverURL.Port(),
		Token:     "test-token",
		UserKey:   "admin",
	})

	remotePayload := app.getStatsPayload(false)
	rStats, ok := remotePayload["stats"].(stats.GlobalStats)
	if !ok {
		t.Fatalf("expected GlobalStats in remotePayload, got %+v", remotePayload["stats"])
	}

	// 核心断言：远程大盘必须真实反映远端数据（0），绝对严禁被本地 5 条历史请求冒名顶替！
	if rStats.TotalRequests != 0 {
		t.Fatalf("expected remote stats totalRequests=0, but got %d (local data leaked into remote dashboard!)", rStats.TotalRequests)
	}

	// 核心断言：远程趋势图必须为空，绝不能混入本地 SQLite 或本地 tracker 的折线图！
	rTrends, ok := remotePayload["trends"].([]*stats.HourlyTrend)
	if !ok || len(rTrends) != 0 {
		t.Fatalf("expected empty remote trends, got %+v", remotePayload["trends"])
	}

	rNvidiaTrends, ok := remotePayload["nvidiaTrends"].([]*stats.HourlyTrend)
	if !ok || len(rNvidiaTrends) != 0 {
		t.Fatalf("expected empty remote nvidiaTrends, got %+v", remotePayload["nvidiaTrends"])
	}
}
