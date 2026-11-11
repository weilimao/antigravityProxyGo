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

func newTestPoolRetryHandler(
	t *testing.T,
	accounts []*account.Account,
	maxRetries int,
	quotaFetch func(*account.Account) (*account.QuotaResult, error),
	transport http.RoundTripper,
	settingsMgr settings.ManagerInterface,
) (*ProxyHandler, *account.Manager) {
	t.Helper()
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)

	pricingMgr := pricing.NewManager()
	h := NewProxyHandler(
		accMgr,
		sessionRouter,
		stats.NewTracker(pricingMgr),
		stats.NewUsageTracker(pricingMgr),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(s string) { t.Log(s) },
		quotaFetch,
		nil,
		nil,
		nil,
		func() int { return maxRetries },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	h.SettingsMgr = settingsMgr
	h.client.Transport = transport
	return h, accMgr
}

// TestPoolErrorRetry_RetrySameAccountExhaustedSwitchesAccount 验证配置 RetryCodes 时，
// 原号同号重试用完之后会自动冻结原号并切换至号池下一个可用账号完成请求。
func TestPoolErrorRetry_RetrySameAccountExhaustedSwitchesAccount(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)
	_ = settingsMgr.SetPoolErrorRetry(settings.PoolErrorRetryConfig{
		Enabled:            true,
		RetryCodes:         []int{502},
		SwitchCooldownSecs: 60,
		MaxRetries:         3,
	})

	acc1 := &account.Account{ID: "acc-1", Email: "user1@gmail.com", Provider: "antigravity", AccessToken: "token-acc-1", Enabled: true}
	acc2 := &account.Account{ID: "acc-2", Email: "user2@gmail.com", Provider: "antigravity", AccessToken: "token-acc-2", Enabled: true}

	var mu sync.Mutex
	var tokens []string
	var firstToken string

	transport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		tok := req.Header.Get("Authorization")
		mu.Lock()
		tokens = append(tokens, tok)
		if firstToken == "" {
			firstToken = tok
		}
		isFirst := (tok == firstToken)
		mu.Unlock()

		if isFirst {
			return &http.Response{
				StatusCode: 502,
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad gateway"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"switched ok"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h, _ := newTestPoolRetryHandler(t, []*account.Account{acc1, acc2}, 20, nil, transport, settingsMgr)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:generateContent", bytes.NewBufferString(`{"contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 after switching on exhausted same-account retries, got %d body=%s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 4 {
		t.Fatalf("expected 4 attempts (3 on first account + 1 switched), got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != tokens[1] || tokens[1] != tokens[2] {
		t.Fatalf("expected first 3 attempts on same account, got %v", tokens)
	}
	if tokens[3] == tokens[0] {
		t.Fatalf("expected 4th attempt to switch to second account, got %v", tokens)
	}
}

// TestPoolErrorRetry_ZeroCooldownSwitchesWithinRequest 验证 SwitchCooldownSecs=0 时
// 仅在本次请求内绕过粘性路由切换下一账号，不将失败账号写入全局冷静期。
func TestPoolErrorRetry_ZeroCooldownSwitchesWithinRequest(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)
	_ = settingsMgr.SetPoolErrorRetry(settings.PoolErrorRetryConfig{
		Enabled:            true,
		SwitchCodes:        []int{500},
		SwitchCooldownSecs: 0,
		MaxRetries:         3,
	})

	acc1 := &account.Account{ID: "acc-1", Email: "user1@gmail.com", Provider: "antigravity", AccessToken: "token-acc-1", Enabled: true}
	acc2 := &account.Account{ID: "acc-2", Email: "user2@gmail.com", Provider: "antigravity", AccessToken: "token-acc-2", Enabled: true}

	var mu sync.Mutex
	var tokens []string
	var firstToken string

	transport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		tok := req.Header.Get("Authorization")
		mu.Lock()
		tokens = append(tokens, tok)
		if firstToken == "" {
			firstToken = tok
		}
		isFirst := (tok == firstToken)
		mu.Unlock()

		if isFirst {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader(`{"error":"server error"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"zero cooldown ok"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h, accMgr := newTestPoolRetryHandler(t, []*account.Account{acc1, acc2}, 20, nil, transport, settingsMgr)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:generateContent", bytes.NewBufferString(`{"contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 after zero-cooldown switch, got %d body=%s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		mu.Unlock()
		t.Fatalf("expected 2 distinct account attempts, got %v", tokens)
	}
	firstTok := tokens[0]
	mu.Unlock()

	firstID := "acc-1"
	if strings.Contains(firstTok, "token-acc-2") {
		firstID = "acc-2"
	}
	failedAcc := accMgr.GetAccountByID(firstID)
	if failedAcc.CooldownUntil != 0 || len(failedAcc.Cooldowns) != 0 {
		t.Errorf("expected zero global cooldown for %s when SwitchCooldownSecs=0, got until=%d map=%v", firstID, failedAcc.CooldownUntil, failedAcc.Cooldowns)
	}
}

// TestGlobalRetry_503CapacityExhaustedSwitchesAfterThreshold 验证全局默认模式下（未启用自定义策略），
// 即使账号自身配额充足（quotaFetch 每次解除冷却），连续遇到 503 MODEL_CAPACITY_EXHAUSTED 达阈值后仍会自动切号。
func TestGlobalRetry_503CapacityExhaustedSwitchesAfterThreshold(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir) // PoolErrorRetry.Enabled == false

	acc1 := &account.Account{ID: "acc-1", Email: "user1@gmail.com", Provider: "antigravity", AccessToken: "token-acc-1", Enabled: true}
	acc2 := &account.Account{ID: "acc-2", Email: "user2@gmail.com", Provider: "antigravity", AccessToken: "token-acc-2", Enabled: true}

	quotaFetch := func(a *account.Account) (*account.QuotaResult, error) {
		return &account.QuotaResult{
			Tier: "Pro",
			Buckets: []account.QuotaBucket{
				{Group: "Gemini Models", ModelID: "Weekly Limit", RemainingFraction: 0.8, RemainPercent: 80},
			},
		}, nil
	}

	var mu sync.Mutex
	var tokens []string
	var firstToken string

	transport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		tok := req.Header.Get("Authorization")
		mu.Lock()
		tokens = append(tokens, tok)
		if firstToken == "" {
			firstToken = tok
		}
		isFirst := (tok == firstToken)
		mu.Unlock()

		if isFirst {
			return &http.Response{
				StatusCode: 503,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":503,"message":"No capacity available","status":"UNAVAILABLE","details":[{"reason":"MODEL_CAPACITY_EXHAUSTED"}]}}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"capacity switch ok"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h, _ := newTestPoolRetryHandler(t, []*account.Account{acc1, acc2}, 3, quotaFetch, transport, settingsMgr)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:streamGenerateContent", bytes.NewBufferString(`{"model":"gemini-3.8-flash-high","contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 after switching from 503 MODEL_CAPACITY_EXHAUSTED account, got %d body=%s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 4 {
		t.Fatalf("expected 4 attempts (3 on first account + 1 switched), got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != tokens[1] || tokens[1] != tokens[2] {
		t.Fatalf("expected first 3 attempts on first account, got %v", tokens)
	}
	if tokens[3] == tokens[0] {
		t.Fatalf("expected 4th attempt to switch to second account, got %v", tokens)
	}
}

// TestGlobalRetry_ServerErrorThresholdWithMaxRetries3Switches 验证全局 MaxRetries=3 且中途已切过 1 次号时，
// 新账号连续遇到普通 503 SERVER_ERROR 也会压缩单号上限并在耗尽总重试前切到第 3 个可用账号。
func TestGlobalRetry_ServerErrorThresholdWithMaxRetries3Switches(t *testing.T) {
	tempDir := t.TempDir()
	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)

	acc1 := &account.Account{ID: "acc-1", Email: "user1@gmail.com", Provider: "antigravity", AccessToken: "token-acc-1", Enabled: true}
	acc2 := &account.Account{ID: "acc-2", Email: "user2@gmail.com", Provider: "antigravity", AccessToken: "token-acc-2", Enabled: true}
	acc3 := &account.Account{ID: "acc-3", Email: "user3@gmail.com", Provider: "antigravity", AccessToken: "token-acc-3", Enabled: true}

	var mu sync.Mutex
	var tokens []string
	var firstToken, secondToken string

	transport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		tok := req.Header.Get("Authorization")
		mu.Lock()
		tokens = append(tokens, tok)
		if firstToken == "" {
			firstToken = tok
		} else if tok != firstToken && secondToken == "" {
			secondToken = tok
		}
		isFirst := (tok == firstToken)
		isSecond := (tok == secondToken)
		mu.Unlock()

		// 第 1 个号直接 429 配额耗尽切号，第 2 个号持续返回普通 503 SERVER_ERROR，第 3 个号成功 200
		if isFirst {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"daily quota limit exceeded"}}`)),
				Header:     make(http.Header),
			}, nil
		}
		if isSecond {
			return &http.Response{
				StatusCode: 503,
				Body:       io.NopCloser(strings.NewReader(`{"error":"upstream temporarily unavailable"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"third account ok"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h, _ := newTestPoolRetryHandler(t, []*account.Account{acc1, acc2, acc3}, 3, nil, transport, settingsMgr)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8045/v1internal:generateContent", bytes.NewBufferString(`{"contents":[]}`))
	req = req.WithContext(context.Background())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 on 3rd account within maxRetries=3, got %d body=%s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 4 || tokens[3] == tokens[0] || tokens[3] == tokens[1] {
		t.Fatalf("expected 4th attempt on distinct 3rd account, got %v", tokens)
	}
}
