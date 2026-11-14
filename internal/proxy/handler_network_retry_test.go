package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type mockTimeoutNetErr struct {
	msg     string
	timeout bool
}

func (e *mockTimeoutNetErr) Error() string   { return e.msg }
func (e *mockTimeoutNetErr) Timeout() bool   { return e.timeout }
func (e *mockTimeoutNetErr) Temporary() bool { return true }

// TestIsTransientNetworkErr_Unit 验证传输层瞬时网络异常分类函数的准确性。
func TestIsTransientNetworkErr_Unit(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"url.Error wrapping EOF", &url.Error{Op: "Post", URL: "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", Err: io.EOF}, true},
		{"connection reset", errors.New("read tcp 127.0.0.1:12345->127.0.0.1:7890: wsarecv: An existing connection was forcibly closed by the remote host; connection reset by peer"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"tls handshake timeout", errors.New("net/http: TLS handshake timeout"), true},
		{"net.Error timeout", &mockTimeoutNetErr{msg: "i/o timeout", timeout: true}, true},
		{"dns no such host", errors.New("dial tcp: lookup invalid.example.com: no such host"), false},
		{"context canceled", context.Canceled, false},
		{"business sentinel", errors.New("QUOTA_EXHAUSTED"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientNetworkErr(tc.err); got != tc.want {
				t.Fatalf("isTransientNetworkErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestProxyHandler_DirectTelemetryEOFTransparentRetry 验证直连/外围端点（如 /v1internal:listExperiments）
// 遇到陈旧连接 EOF 时，在 forwardForAttempt 转发层原地驱逐坏连接并透明重发成功，不触发直连失败报错。
func TestProxyHandler_DirectTelemetryEOFTransparentRetry(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc := &account.Account{
		ID:          "acc-exp-1",
		Email:       "weilimao666@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.pool_token_1",
		Enabled:     true,
	}
	accMgr.AddAccount(acc)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var calls int32
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cnt := atomic.AddInt32(&calls, 1)
		if cnt == 1 {
			return nil, io.EOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"experiments":[]}`)),
		}, nil
	})

	var logMu sync.Mutex
	var logs []string
	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) {
			logMu.Lock()
			logs = append(logs, s)
			logMu.Unlock()
			t.Logf("[ProxyLog] %s", s)
		},
		nil,
		nil,
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 2 },
		func() int { return 0 },
		func() int64 { return 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	handler.client = &http.Client{Transport: mockTransport}

	req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:listExperiments", strings.NewReader(`{"metadata":{}}`))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ya29.client_oauth_token")
	req.Header.Set("User-Agent", "antigravity/2.31.2")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on listExperiments after transparent EOF retry, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if gotCalls := atomic.LoadInt32(&calls); gotCalls != 2 {
		t.Fatalf("expected 2 transport calls (1 stale EOF + 1 transparent retry), got %d", gotCalls)
	}
	logMu.Lock()
	for _, line := range logs {
		if strings.Contains(line, "❌ [直连模式] 尝试失败") {
			logMu.Unlock()
			t.Fatalf("unexpected direct failure log on transient EOF: %s", line)
		}
	}
	logMu.Unlock()
}

// TestProxyHandler_EOFRetrySameAccountSuccess 验证负载均衡模式下当转发层重试后仍遇 EOF 时，
// 能进入重试循环自动恢复流式响应，且不误挂账号冷静期。
func TestProxyHandler_EOFRetrySameAccountSuccess(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc := &account.Account{
		ID:          "acc-eof-1",
		Email:       "weilimao666@gmail.com",
		Provider:    "antigravity",
		AccessToken: "token-eof-1",
		Enabled:     true,
	}
	accMgr.AddAccount(acc)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var calls int32
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cnt := atomic.AddInt32(&calls, 1)
		// 前 2 次（第 1 轮 attempt 的初始请求 + 转发层透明重试）均返回 EOF，第 3 次（第 2 轮 attempt）成功
		if cnt <= 2 {
			return nil, io.EOF
		}
		sseBody := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseBody)),
		}, nil
	})

	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyLog] %s", s) },
		nil,
		nil,
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 2 },
		func() int { return 0 },
		func() int64 { return 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	handler.client = &http.Client{Transport: mockTransport}

	body := []byte(`{"model":"gemini-3.8-flash-high","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(body))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.0.0")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 after EOF retry, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"text":"ok"`) {
		t.Fatalf("expected SSE body after retry, got %s", rec.Body.String())
	}
	if gotCalls := atomic.LoadInt32(&calls); gotCalls != 3 {
		t.Fatalf("expected 3 transport calls (2 in attempt#1 + 1 in attempt#2), got %d", gotCalls)
	}
	updatedAcc := accMgr.GetAccountByID(acc.ID)
	if updatedAcc != nil && updatedAcc.CooldownUntil > 0 {
		t.Fatalf("expected account cooldown not to be set on transient EOF, got %d", updatedAcc.CooldownUntil)
	}
}

// TestProxyHandler_EOFRetryExhaustedReturns502 验证当所有重试次数耗尽仍遭遇 EOF 时，
// 返回 502 Bad Gateway（而非伪造 429 配额耗尽），且统计记录状态码为 502。
func TestProxyHandler_EOFRetryExhaustedReturns502(t *testing.T) {
	tempDir := t.TempDir()

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	acc := &account.Account{
		ID:          "acc-eof-2",
		Email:       "lmwei168@gmail.com",
		Provider:    "antigravity",
		AccessToken: "token-eof-2",
		Enabled:     true,
	}
	accMgr.AddAccount(acc)

	sessionRouter := session.NewRouter()
	sessionRouter.Init(tempDir)
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var calls int32
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, io.EOF
	})

	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyLog] %s", s) },
		nil,
		nil,
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 1 },
		func() int { return 0 },
		func() int64 { return 1024 * 1024 },
		func() int { return 30 },
		nil,
		nil,
	)
	handler.client = &http.Client{Transport: mockTransport}

	body := []byte(`{"model":"gemini-3.8-flash-high","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(body))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.0.0")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502 when EOF retries exhausted, got %d, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "RESOURCE_EXHAUSTED") {
		t.Fatalf("must not return fake 429 RESOURCE_EXHAUSTED on network EOF, got %s", rec.Body.String())
	}
	if gotCalls := atomic.LoadInt32(&calls); gotCalls != 4 {
		t.Fatalf("expected 4 total transport calls (2 attempts * 2 calls each), got %d", gotCalls)
	}
}
