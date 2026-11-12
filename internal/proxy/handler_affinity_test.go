package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

// TestProxyHandler_CompanionAffinityConvergence 验证：
// 1. 客户端发起主任务代码生成请求 (3.8-flash-high + conversationId) 命中账号 A。
// 2. 客户端紧接着发起伴随安全审查/监控请求 (3.5-flash-lite + agent_monitoring 无 conversationId)，
//    必须 100% 亲和收敛至账号 A，绝不能被负载均衡分流到账号池中的其他空闲账号 (如账号 B / 账号 C)，
//    彻底杜绝跨账号 Trajectory 轨迹与私有代码片段泄露引发的连环风控。
func TestProxyHandler_CompanionAffinityConvergence(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc1 := &account.Account{
		ID:          "acc-1",
		Email:       "account1@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-account-1",
		ProjectID:   "proj-1",
		Enabled:     true,
	}
	acc2 := &account.Account{
		ID:          "acc-2",
		Email:       "account2@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-account-2",
		ProjectID:   "proj-2",
		Enabled:     true,
	}
	acc3 := &account.Account{
		ID:          "acc-3",
		Email:       "account3@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-account-3",
		ProjectID:   "proj-3",
		Enabled:     true,
	}
	accMgr.AddAccount(acc1)
	accMgr.AddAccount(acc2)
	accMgr.AddAccount(acc3)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)

	pricingMgr := pricing.NewManager()
	pricingMgr.Init(tempDir)
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var capturedAuth string
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedAuth = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)),
		}, nil
	})

	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyHandler Log] %s", s) },
		nil,
		nil,
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 0 },
		func() int { return 1 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 },
		nil,
		nil,
	)
	handler.SettingsMgr = settings.NewManager()
	handler.client = &http.Client{Transport: mockTransport}

	clientTokenA := "local-antigravity-client-token-AAA"

	// 1. 发起主干任务请求 (gemini-3.8-flash-high 带 conversationId)
	mainReqBody := `{"model":"gemini-3.8-flash-high","conversationId":"44e60633-1a26-4685-8581-e4fd22efed1d","contents":[{"parts":[{"text":"package main"}]}]}`
	req1 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(mainReqBody))
	req1.Header.Set("Authorization", "Bearer "+clientTokenA)
	req1.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec1 := httptest.NewRecorder()

	handler.ServeHTTP(rec1, req1)
	tokenMainA := capturedAuth
	if tokenMainA == "" {
		t.Fatalf("expected outgoing Authorization header for main request, got empty")
	}

	// 2. 发起伴生监控审查请求 (gemini-3.5-flash-lite + agent_monitoring)
	companionReqBody := `{"model":"gemini-3.5-flash-lite","requestType":"agent_monitoring","requestId":"agent_monitoring/75899def-79c8-4120-b1a2-e4636e74dea8","contents":[{"parts":[{"text":"Safety review for package main"}]}]}`
	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(companionReqBody))
	req2.Header.Set("Authorization", "Bearer "+clientTokenA)
	req2.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)
	tokenCompanionA := capturedAuth

	// 核心断言: 伴生审查请求必须与主任务账号 100% 相同！
	if tokenCompanionA != tokenMainA {
		t.Fatalf("CRITICAL SECURITY VULNERABILITY: Companion flash-lite request routed to different account! Got %q, want %q", tokenCompanionA, tokenMainA)
	}

	// 3. 客户端 A 发起第二个独立对话，必须负载均衡打散到其他空闲账号，不能全部薅在同一个号上
	time.Sleep(5 * time.Millisecond)
	conv2ReqBody := `{"model":"gemini-3.8-flash-high","conversationId":"conv2222-9999-4685-8581-e4fd22efed1d","contents":[{"parts":[{"text":"second chat test"}]}]}`
	req3 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(conv2ReqBody))
	req3.Header.Set("Authorization", "Bearer "+clientTokenA)
	req3.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec3 := httptest.NewRecorder()

	handler.ServeHTTP(rec3, req3)
	tokenConv2 := capturedAuth
	if tokenConv2 == tokenMainA {
		t.Fatalf("LOAD BALANCING FAILURE: second conversation got pinned to same account %q instead of spreading to idle accounts", tokenMainA)
	}

	// 紧接着为对话 2 发起伴生监控审查请求，必须动态切往对话 2 的账号 tokenConv2
	req3Companion := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(companionReqBody))
	req3Companion.Header.Set("Authorization", "Bearer "+clientTokenA)
	req3Companion.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec3Comp := httptest.NewRecorder()

	handler.ServeHTTP(rec3Comp, req3Companion)
	tokenCompanion2 := capturedAuth
	if tokenCompanion2 != tokenConv2 {
		t.Fatalf("companion request after conv2 mismatch: got %q, want %q", tokenCompanion2, tokenConv2)
	}

	// 4. 发起另一个独立的客户端 B 请求 (不同的 Client Token)
	clientTokenB := "local-antigravity-client-token-BBB"
	mainBReqBody := `{"model":"gemini-3.8-flash-high","conversationId":"clientB-1111-4685-8581-e4fd22efed1d","contents":[{"parts":[{"text":"client B code"}]}]}`
	req4 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(mainBReqBody))
	req4.Header.Set("Authorization", "Bearer "+clientTokenB)
	req4.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec4 := httptest.NewRecorder()

	handler.ServeHTTP(rec4, req4)
	tokenMainB := capturedAuth

	// 客户端 B 的伴生请求必须收敛至客户端 B 自己的账号
	req5 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(companionReqBody))
	req5.Header.Set("Authorization", "Bearer "+clientTokenB)
	req5.Header.Set("User-Agent", "antigravity/hub/2.31.0 (aidev_client; os_type=windows; arch=amd64; cl=1017595969)")
	rec5 := httptest.NewRecorder()

	handler.ServeHTTP(rec5, req5)
	tokenCompanionB := capturedAuth

	if tokenCompanionB != tokenMainB {
		t.Fatalf("client B companion token mismatch: got %q, want %q", tokenCompanionB, tokenMainB)
	}
}
