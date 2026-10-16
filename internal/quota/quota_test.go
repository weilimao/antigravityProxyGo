package quota

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

func TestFetchQuota_Diagnostic(t *testing.T) {
	acc := &account.Account{
		ID:           "test-id",
		Email:        "weilimao96@gmail.com",
		AccessToken:  "invalid-access-token-to-force-refresh",
		RefreshToken: "dummy-refresh-token",
		Provider:     "antigravity",
	}

	t.Log("=== [单元测试诊断] 启动 FetchQuota 并测定连通性 ===")

	q := NewQuotaService()
	q.Init(t.TempDir())

	am := NewAuthManager(nil)

	done := make(chan bool)
	go func() {
		// 这里的 RefreshToken 会尝试访问 oauth2.googleapis.com
		_, err := q.FetchQuota(acc, am.RefreshToken, func(id, token string) {})
		t.Logf("[DEBUG] FetchQuota 最终执行完毕，结果/错误: %v", err)
		done <- true
	}()

	select {
	case <-done:
		t.Log("=== [单元测试诊断] 执行完成 ===")
	case <-time.After(25 * time.Second):
		t.Fatal("=== [单元测试诊断] 超时 (25秒)：发生了严重的挂起/死锁 ===")
	}
}

func TestMergeQuotaBuckets_UpstreamMissingGemini(t *testing.T) {
	// 模拟上游 retrieveUserQuotaSummary 仅返回 Claude and GPT models (上游新行为)
	summaryJSON := []byte(`{
		"groups": [
			{
				"displayName": "Claude and GPT models",
				"buckets": [
					{
						"bucketId": "3p-weekly",
						"displayName": "Weekly Limit Remaining",
						"remainingFraction": 0.9999924,
						"resetTime": "2026-10-19T14:52:34Z"
					},
					{
						"bucketId": "3p-5h",
						"displayName": "Five Hour Limit Remaining",
						"remainingFraction": 1.0,
						"resetTime": "2026-10-16T08:47:18Z"
					}
				]
			}
		]
	}`)

	// 模拟 retrieveUserQuota 返回具体的模型列表，其中 Gemini 剩余 0.95136 (95%)
	detailJSON := []byte(`{
		"buckets": [
			{
				"modelId": "claude-sonnet-4-6",
				"remainingFraction": 1.0,
				"resetTime": "2026-10-16T08:48:58Z",
				"tokenType": "WTUS"
			},
			{
				"modelId": "gemini-2.5-flash",
				"remainingFraction": 0.95136,
				"resetTime": "2026-10-16T07:29:50Z",
				"tokenType": "WTUS"
			},
			{
				"modelId": "gemini-3-flash",
				"remainingFraction": 0.95136,
				"resetTime": "2026-10-16T07:29:50Z",
				"tokenType": "WTUS"
			}
		]
	}`)

	summaryBuckets := ParseUserQuotaSummary(summaryJSON)
	if len(summaryBuckets) != 2 {
		t.Fatalf("expected 2 summary buckets, got %d", len(summaryBuckets))
	}
	if HasGeminiGroup(summaryBuckets) {
		t.Fatal("expected summary buckets to NOT have Gemini group")
	}

	merged := MergeQuotaBuckets(summaryBuckets, detailJSON)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged buckets (2 Gemini + 2 Claude), got %d", len(merged))
	}

	// 验证 Gemini 置顶且数值精确
	var geminiWeekly, gemini5H, claudeWeekly, claude5H *account.QuotaBucket
	for i := range merged {
		b := &merged[i]
		if b.Group == "Gemini Models" {
			if b.ModelID == "Weekly Limit Remaining" {
				geminiWeekly = b
			} else if b.ModelID == "Five Hour Limit Remaining" {
				gemini5H = b
			}
		} else if b.Group == "Claude and GPT models" {
			if b.ModelID == "Weekly Limit Remaining" {
				claudeWeekly = b
			} else if b.ModelID == "Five Hour Limit Remaining" {
				claude5H = b
			}
		}
	}

	if geminiWeekly == nil || geminiWeekly.RemainPercent != 100 {
		t.Fatalf("invalid geminiWeekly: %+v", geminiWeekly)
	}
	if gemini5H == nil || gemini5H.RemainPercent != 95 || gemini5H.ResetTime != "2026-10-16T07:29:50Z" {
		t.Fatalf("invalid gemini5H: %+v", gemini5H)
	}
	if claudeWeekly == nil || claudeWeekly.RemainPercent != 100 {
		t.Fatalf("invalid claudeWeekly: %+v", claudeWeekly)
	}
	if claude5H == nil || claude5H.RemainPercent != 100 {
		t.Fatalf("invalid claude5H: %+v", claude5H)
	}
}

func TestMergeQuotaBuckets_BothSummaryExist(t *testing.T) {
	// 模拟如果上游同时返回 Gemini 和 Claude 两组 (向后兼容)
	summaryJSON := []byte(`{
		"groups": [
			{
				"displayName": "Gemini Models",
				"buckets": [
					{
						"displayName": "Weekly Limit",
						"remainingFraction": 0.88
					},
					{
						"displayName": "Five Hour Limit",
						"remainingFraction": 0.60,
						"resetTime": "2026-10-16T05:00:00Z"
					}
				]
			},
			{
				"displayName": "Claude and GPT models",
				"buckets": [
					{
						"displayName": "Weekly Limit",
						"remainingFraction": 1.0
					}
				]
			}
		]
	}`)

	summaryBuckets := ParseUserQuotaSummary(summaryJSON)
	if !HasGeminiGroup(summaryBuckets) {
		t.Fatal("expected summary buckets to have Gemini group")
	}

	merged := MergeQuotaBuckets(summaryBuckets, nil)
	if len(merged) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(merged))
	}
}

func TestRetrieveUserQuota_RealUpstreamLiveTest(t *testing.T) {
	// 读取本机的真实账号以测试真实上游双端点协同
	dataPath := "D:\\antigravityProxy\\data\\accounts_antigravity.json"
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		t.Skip("local real account data not available, skip live probe")
		return
	}

	var data struct {
		Accounts []struct {
			Email       string `json:"email"`
			AccessToken string `json:"access_token"`
			ProjectID   string `json:"projectId"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || len(data.Accounts) == 0 {
		t.Skip("no accounts in local json, skip live probe")
		return
	}

	acc := data.Accounts[0]
	token := acc.AccessToken
	if token == "" {
		t.Skip("empty token, skip live probe")
		return
	}

	t.Logf("=== 使用真实账号 %s 验证最新上游配额拉取 ===", acc.Email)
	buckets, err := retrieveUserQuota(token, "aicode-consumers", true)
	if err != nil {
		t.Fatalf("retrieveUserQuota failed: %v", err)
	}

	t.Logf("配额桶数量: %d", len(buckets))
	hasGemini := false
	hasClaude := false
	for _, b := range buckets {
		t.Logf("  [%s] %s -> %d%% (ResetTime: %s)", b.Group, b.ModelID, b.RemainPercent, b.ResetTime)
		if b.Group == "Gemini Models" {
			hasGemini = true
		}
		if b.Group == "Claude and GPT models" {
			hasClaude = true
		}
	}

	if !hasGemini {
		t.Fatal("expected Gemini Models group in live quota result")
	}
	if !hasClaude {
		t.Fatal("expected Claude and GPT models group in live quota result")
	}
}


