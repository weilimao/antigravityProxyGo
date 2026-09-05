package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

func TestRemoteControl_IsGoogleOAuthToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected bool
	}{
		{
			name:     "valid google oauth bearer token with prefix",
			header:   "Bearer ya29.a0AXooCguTestToken123456",
			expected: true,
		},
		{
			name:     "valid google oauth bearer token without extra prefix",
			header:   "Bearer ya29.xxxx",
			expected: true,
		},
		{
			name:     "raw token without Bearer prefix",
			header:   "ya29.direct-token-test",
			expected: true,
		},
		{
			name:     "relay api key",
			header:   "Bearer sk-ant-9cd5fdbf9bf4109539272cf14ea643bb",
			expected: false,
		},
		{
			name:     "empty authorization header",
			header:   "",
			expected: false,
		},
		{
			name:     "random jwt token",
			header:   "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDcSemACt8x4iTMCda8Yhe3iZaWbvV5XKSTbuAn0M",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGoogleOAuthToken(tt.header)
			if got != tt.expected {
				t.Errorf("isGoogleOAuthToken(%q) = %v; expected %v", tt.header, got, tt.expected)
			}
		})
	}
}

func TestRemoteControl_BuildMockExperimentsResponse(t *testing.T) {
	raw := buildMockExperimentsResponse()
	var resp struct {
		ExperimentIDs []int `json:"experimentIds"`
		Flags         []struct {
			Name        string `json:"name"`
			BoolValue   bool   `json:"boolValue"`
			StringValue string `json:"stringValue"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("failed to unmarshal mock experiments response: %v", err)
	}

	var foundRemoteControl bool
	var foundWebchannelHost bool
	for _, flag := range resp.Flags {
		if flag.Name == "remote-control-setting-enabled" {
			foundRemoteControl = true
			if !flag.BoolValue {
				t.Errorf("expected remote-control-setting-enabled to be true")
			}
		}
		if flag.Name == "remote-control-proxy-server-url" {
			foundWebchannelHost = true
			if flag.StringValue != "jetski-webchannel.googleapis.com:443" {
				t.Errorf("expected remote-control-proxy-server-url to be jetski-webchannel.googleapis.com:443, got %s", flag.StringValue)
			}
		}
	}

	if !foundRemoteControl {
		t.Errorf("missing remote-control-setting-enabled flag in mock experiments")
	}
	if !foundWebchannelHost {
		t.Errorf("missing remote-control-proxy-server-url flag in mock experiments")
	}
}

func TestRemoteControl_BuildMockUserInfoResponse(t *testing.T) {
	raw := buildMockUserInfoResponse()
	var resp struct {
		RegionCode   string                 `json:"regionCode"`
		UserSettings map[string]interface{} `json:"userSettings"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("failed to unmarshal mock user info response: %v", err)
	}

	if resp.UserSettings == nil {
		t.Fatalf("expected non-nil userSettings")
	}
	if v, ok := resp.UserSettings["remoteControlEnabled"].(bool); !ok || !v {
		t.Errorf("expected userSettings.remoteControlEnabled to be true, got %v", resp.UserSettings["remoteControlEnabled"])
	}
}

func setupTestProxyHandlerForV1Internal() *ProxyHandler {
	accMgr := account.NewManager()
	// Add dummy raw account so hasLocalAccounts is true
	accMgr.AddAccount(&account.Account{
		ID:          "acc-1",
		Email:       "test@example.com",
		AccessToken: "test-token",
		Provider:    "antigravity",
		ScopeType:   "antigravity",
		Enabled:     true,
		ProjectID:   "test-proj",
		Cooldowns:   map[string]int64{},
	})

	sessRouter := session.NewRouter()
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	handler := NewProxyHandler(
		accMgr,
		sessRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) {},
		nil,               // quotaFetch
		nil,               // tokenRefresh
		func(s1, s2 string) {},
		func(s string) string { return "" },
		func() int { return 0 },
		func() int { return 1 },
		func() int64 { return 1024 * 1024 },
		func() int { return 30 },
		nil, nil,
	)
	return handler
}

func TestProxyHandler_V1Internal_NoAuthMockFallback(t *testing.T) {
	handler := setupTestProxyHandlerForV1Internal()

	// 1. Test listExperiments without Google auth
	reqExp := httptest.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:listExperiments", strings.NewReader(`{}`))
	recExp := httptest.NewRecorder()

	handler.ServeHTTP(recExp, reqExp)

	if recExp.Code != http.StatusOK {
		t.Fatalf("expected status 200 for listExperiments, got %d", recExp.Code)
	}
	bodyExp := recExp.Body.String()
	if !strings.Contains(bodyExp, "remote-control-setting-enabled") {
		t.Errorf("expected response to contain remote-control-setting-enabled, got %s", bodyExp)
	}
	if !strings.Contains(bodyExp, "jetski-webchannel.googleapis.com:443") {
		t.Errorf("expected response to contain jetski-webchannel.googleapis.com:443, got %s", bodyExp)
	}

	// 2. Test fetchUserInfo without Google auth
	reqUser := httptest.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchUserInfo", strings.NewReader(`{}`))
	recUser := httptest.NewRecorder()

	handler.ServeHTTP(recUser, reqUser)

	if recUser.Code != http.StatusOK {
		t.Fatalf("expected status 200 for fetchUserInfo, got %d", recUser.Code)
	}
	bodyUser := recUser.Body.String()
	if !strings.Contains(bodyUser, "remoteControlEnabled") {
		t.Errorf("expected response to contain remoteControlEnabled, got %s", bodyUser)
	}

	// 3. Test fetchAdminControls without Google auth
	reqAdmin := httptest.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAdminControls", strings.NewReader(`{}`))
	recAdmin := httptest.NewRecorder()

	handler.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("expected status 200 for fetchAdminControls, got %d", recAdmin.Code)
	}
	if recAdmin.Body.String() != "{}" {
		t.Errorf("expected {} for fetchAdminControls, got %s", recAdmin.Body.String())
	}
}

func TestProxyHandler_V1Internal_GoogleAuthPassthrough(t *testing.T) {
	// Mock an upstream server simulating daily-cloudcode-pa.googleapis.com
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ya29.valid-oauth-token") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upstreamSuccess":true,"flags":[{"name":"upstream-flag","boolValue":true}]}`))
	}))
	defer upstream.Close()

	handler := setupTestProxyHandlerForV1Internal()

	srvURL, _ := url.Parse(upstream.URL)
	srvClient := upstream.Client()
	transport := srvClient.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, srvURL.Host)
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	handler.client = srvClient

	req := httptest.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:listExperiments", strings.NewReader(`{}`))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("Authorization", "Bearer ya29.valid-oauth-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatalf("expected request to pass through to upstream, but upstream was never reached")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 from upstream passthrough, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstreamSuccess") {
		t.Errorf("expected upstream response, got %s", rec.Body.String())
	}
}
