package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestPoolErrorRetry_SwitchAccount 验证当遇到配置在 SwitchCodes 中的状态码时，直接切换到号池下一个账号
func TestPoolErrorRetry_SwitchAccount(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc1 := &account.Account{
		ID:          "acc-1",
		Email:       "user1@gmail.com",
		Provider:    "antigravity",
		AccessToken: "token-acc-1",
		Enabled:     true,
	}
	acc2 := &account.Account{
		ID:          "acc-2",
		Email:       "user2@gmail.com",
		Provider:    "antigravity",
		AccessToken: "token-acc-2",
		Enabled:     true,
	}
	accMgr.AddAccount(acc1)
	accMgr.AddAccount(acc2)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)

	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)
	_ = settingsMgr.SetPoolErrorRetry(settings.PoolErrorRetryConfig{
		Enabled:            true,
		SwitchCodes:        []int{500},
		SwitchCooldownSecs: 60,
		MaxRetries:         3,
	})

	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var reqCount int32
	var mu sync.Mutex
	var authTokens []string
	var firstToken string

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cnt := atomic.AddInt32(&reqCount, 1)
		token := req.Header.Get("Authorization")

		mu.Lock()
		authTokens = append(authTokens, token)
		if firstToken == "" {
			firstToken = token
		}
		isFirstAcc := (token == firstToken)
		mu.Unlock()

		// 首次分配的账号遇到 500 报错，切换账号后成功返回 200
		if cnt == 1 || isFirstAcc {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader(`{"error":"internal server error"}`)),
				Header:     make(http.Header),
			}, nil
		}

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"success from next account"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Log(s) },
		nil,
		nil,
		nil,
		nil,
		func() int { return 3 },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	h.SettingsMgr = settingsMgr
	h.client.Transport = mockTransport

	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:generateContent", bytes.NewBufferString(`{"contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200 after failover switch, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if atomic.LoadInt32(&reqCount) < 2 {
		t.Fatalf("expected at least 2 attempts (switch to next account), got %d", reqCount)
	}

	mu.Lock()
	tokensCopy := make([]string, len(authTokens))
	copy(tokensCopy, authTokens)
	mu.Unlock()

	// 验证第 1 次尝试和第 2 次尝试使用了不同的账号
	if len(tokensCopy) < 2 || tokensCopy[0] == tokensCopy[1] {
		t.Fatalf("expected switch to a different account, but tokens were: %v", tokensCopy)
	}

	// 验证首次失败的账号被正确设置了冷静期
	var firstAccID string
	if strings.Contains(tokensCopy[0], "token-acc-1") {
		firstAccID = "acc-1"
	} else {
		firstAccID = "acc-2"
	}
	failedAcc := accMgr.GetAccountByID(firstAccID)
	if failedAcc.CooldownUntil == 0 && len(failedAcc.Cooldowns) == 0 {
		t.Errorf("expected failed account %s to be in cooldown, but cooldown was 0", firstAccID)
	}
}

// TestPoolErrorRetry_RetrySameAccount 验证当遇到配置在 RetryCodes 中的状态码时，直接在当前账号重试
func TestPoolErrorRetry_RetrySameAccount(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc1 := &account.Account{
		ID:          "acc-1",
		Email:       "user1@gmail.com",
		Provider:    "antigravity",
		AccessToken: "token-acc-1",
		Enabled:     true,
	}
	accMgr.AddAccount(acc1)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)

	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)
	_ = settingsMgr.SetPoolErrorRetry(settings.PoolErrorRetryConfig{
		Enabled:    true,
		RetryCodes: []int{502},
		MaxRetries: 3,
	})

	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var attemptCount int32
	var mu sync.Mutex
	var authTokens []string

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cnt := atomic.AddInt32(&attemptCount, 1)
		token := req.Header.Get("Authorization")

		mu.Lock()
		authTokens = append(authTokens, token)
		mu.Unlock()

		// 第 1 次返回 502，第 2 次成功返回 200
		if cnt == 1 {
			return &http.Response{
				StatusCode: 502,
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad gateway"}`)),
				Header:     make(http.Header),
			}, nil
		}

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"recovered on retry"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(string) {},
		nil,
		nil,
		nil,
		nil,
		func() int { return 3 },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	h.SettingsMgr = settingsMgr
	h.client.Transport = mockTransport

	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:generateContent", bytes.NewBufferString(`{"contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200 after retry on same account, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if atomic.LoadInt32(&attemptCount) != 2 {
		t.Fatalf("expected 2 attempts, got %d", attemptCount)
	}

	mu.Lock()
	tokensCopy := make([]string, len(authTokens))
	copy(tokensCopy, authTokens)
	mu.Unlock()

	// 验证两次尝试都在同一个账号 acc-1 上
	for _, tok := range tokensCopy {
		if !strings.Contains(tok, "token-acc-1") {
			t.Errorf("expected attempt on token-acc-1, got %s", tok)
		}
	}

	// 验证 acc-1 未被挂冷静期
	refreshedAcc1 := accMgr.GetAccountByID("acc-1")
	if refreshedAcc1.CooldownUntil != 0 {
		t.Errorf("expected acc-1 not to be in cooldown, but got cooldown %d", refreshedAcc1.CooldownUntil)
	}
}
