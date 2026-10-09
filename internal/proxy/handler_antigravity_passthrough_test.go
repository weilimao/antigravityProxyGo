package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockRoundTripperFunc func(req *http.Request) (*http.Response, error)

func (f mockRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestAntigravityPassthroughVsRelaySpoof 验证：
// 1. 原生 Antigravity 客户端发起的请求（relayUserID == ""）：
//    保留原本的 User-Agent 与原始请求体，不修改、不篡改、只做透传（仅替换账号池 Token）。
// 2. 来自 18444 端口第三方平台调用的请求（relayUserID != ""）：
//    使用账号池配置的 Hub 版本构造 User-Agent 伪装，并剥离内部中继头。
func TestAntigravityPassthroughVsRelaySpoof(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化账号管理器与依赖桩
	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetAntigravityCliVersion("2.21.1")

	poolAccount := &account.Account{
		ID:          "acc-antigravity-1",
		Email:       "test-antigravity@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.test-pool-access-token",
		ProjectID:   "favorable-synapse-ttvcb",
		Enabled:     true,
	}
	accMgr.AddAccount(poolAccount)

	sessionRouter := session.NewRouter()
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	// 2. 构造可捕获上游出站请求的 Mock RoundTripper
	var capturedUA string
	var capturedAuth string
	var capturedCustomHeader string
	var capturedBody []byte
	var capturedRelayHdr string

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedUA = req.Header.Get("User-Agent")
		capturedAuth = req.Header.Get("Authorization")
		capturedCustomHeader = req.Header.Get("X-Client-Custom-Header")
		capturedRelayHdr = req.Header.Get("X-Relay-User-Id")
		var errRead error
		capturedBody, errRead = io.ReadAll(req.Body)
		if errRead != nil {
			t.Errorf("mock upstream failed to read body: %v", errRead)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"upstream response"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	// 3. 构造 ProxyHandler 并将出站客户端 Transport 指向 mockTransport
	h := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyHandler Log] %s", s) }, // logFn
		nil,                                                  // quotaFetch
		nil,                                                  // tokenRefresh
		func(s1, s2 string) {},                               // setCapturedProject
		func(s string) string { return "" },                  // getStoredProject
		func() int { return 0 },                              // getMaxRetries
		func() int { return 1 },                              // getMaxRetryDelay
		func() int64 { return 10 * 1024 * 1024 },             // maxRequestBodyBytes
		func() int { return 5 },                              // getRequestTimeout
		nil,                                                  // relayStatsCallback
		nil,                                                  // relayQuotaCheck
	)
	h.client = &http.Client{Transport: mockTransport}

	// =========================================================================
	// 场景 A: 原生 Antigravity 官方客户端请求 (relayUserID == "")
	// =========================================================================
	t.Run("NativeAntigravityRequest_PassthroughHeadersAndBody", func(t *testing.T) {
		nativeUA := "antigravity/ide/2.8.4 (windows; amd64)"
		rawPayload := []byte(`{"enabledCreditTypes":["GOOGLE_ONE_AI"],"model":"gemini-3.5-flash-high","project":"favorable-synapse-ttvcb","request":{"contents":[{"parts":[{"text":"native prompt without tampering"}]}]}}`)

		// 模拟直接打到本地解密代理的请求 (无 X-Relay-User-Id，Context 无 RelayUserCtxKey)
		req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(rawPayload))
		req.Host = "daily-cloudcode-pa.googleapis.com"
		req.Header.Set("User-Agent", nativeUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-old-token")
		req.Header.Set("X-Client-Custom-Header", "native-client-secret")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 核心断言 1: User-Agent 必须保持原生，绝不能被篡改为 Hub 版本
		if capturedUA != nativeUA {
			t.Errorf("User-Agent mismatch for native Antigravity! Expected native %q, got %q", nativeUA, capturedUA)
		}

		// 核心断言 2: 原生请求头中的自定义 Header 必须保留透传
		if capturedCustomHeader != "native-client-secret" {
			t.Errorf("Expected X-Client-Custom-Header to be preserved, got %q", capturedCustomHeader)
		}

		// 核心断言 3: Authorization 成功替换为分配的号池账号 Access Token
		expectedAuth := "Bearer " + poolAccount.AccessToken
		if capturedAuth != expectedAuth {
			t.Errorf("Authorization mismatch! Expected %q, got %q", expectedAuth, capturedAuth)
		}

		// 核心断言 4: 请求体字节流必须 100% 原始透传，不得被重写或篡改
		if !bytes.Equal(capturedBody, rawPayload) {
			t.Errorf("Request body was modified during passthrough!\nExpected:\n%s\nGot:\n%s", string(rawPayload), string(capturedBody))
		}
	})

	// =========================================================================
	// 场景 B: 来自 18444 端口第三方平台调用的请求 (relayUserID != "")
	// =========================================================================
	t.Run("Relay18444ThirdPartyRequest_SpoofsUserAgentWithHubVersion", func(t *testing.T) {
		relayPayload := []byte(`{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":"relay prompt"}]}`)

		req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(relayPayload))
		req.Host = "daily-cloudcode-pa.googleapis.com"
		// 携带 18444 中继特征头，且客户端 UA 带有通用 HTTP 客户端特征
		req.Header.Set("User-Agent", "Go-http-client/1.1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer client-temp-key")
		req.Header.Set("X-Relay-User-Id", "relay-third-party-user-42")

		// 注入 Context 强化识别
		ctx := context.WithValue(req.Context(), RelayUserCtxKey, "relay-third-party-user-42")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 核心断言 1: 18444 第三方中继调用必须正确伪装为号池配置的 Hub 版本
		expectedHubUA := "antigravity/hub/2.21.1 (aidev_client; os_type=windows; arch=amd64)"
		if capturedUA != expectedHubUA {
			t.Errorf("User-Agent mismatch for relay request! Expected %q, got %q", expectedHubUA, capturedUA)
		}

		// 核心断言 2: 内部中继头必须被彻底剥离，防止暴露给上游
		if capturedRelayHdr != "" {
			t.Errorf("Expected X-Relay-User-Id to be stripped for upstream, but got %q", capturedRelayHdr)
		}
	})
}
