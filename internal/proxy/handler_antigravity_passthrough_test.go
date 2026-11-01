package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"bytes"
	"context"
	"encoding/json"
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
	var capturedHost string
	var capturedRelayHdr string

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedHost = req.URL.Host
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

	// =========================================================================
	// 场景 C: 新版 IDE 插件调用 (VS Code 等，relayUserID == "") 且号池账号具备专属项目
	// =========================================================================
	t.Run("NewIDEPluginRequest_HasCustomProject_SpoofsUserAgentAndRepairsProject", func(t *testing.T) {
		pluginUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Code/1.95.0"
		// 客户端请求未提供 project 字段
		pluginPayload := []byte(`{"model":"gemini-3.5-flash-high","request":{"contents":[{"parts":[{"text":"hello from vscode plugin"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:generateContent", bytes.NewReader(pluginPayload))
		req.Host = "127.0.0.1:18443"
		req.Header.Set("User-Agent", pluginUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 核心断言 1: 新版 IDE 插件请求必须自动伪装为合法 Antigravity Hub UA，规避上游拦截
		expectedHubUA := "antigravity/hub/2.21.1 (aidev_client; os_type=windows; arch=amd64)"
		if capturedUA != expectedHubUA {
			t.Errorf("User-Agent mismatch for new IDE plugin! Expected %q, got %q", expectedHubUA, capturedUA)
		}

		// 核心断言 2: 客户端缺失的 project 字段必须被自动修复并注入账号池项目 ID
		if !strings.Contains(string(capturedBody), `"project":"favorable-synapse-ttvcb"`) {
			t.Errorf("Expected injected project ID 'favorable-synapse-ttvcb' in payload, got: %s", string(capturedBody))
		}
	})

	// =========================================================================
	// =========================================================================
	// 场景 D: 新版 IDE 插件调用 (relayUserID == "") 且号池账号无专属项目 -> 保持 daily-cloudcode-pa 通道并自动兜底官方项目 ID
	// =========================================================================
	t.Run("NewIDEPluginRequest_NoCustomProject_MaintainsDailyCloudCodeWithDefaultProject", func(t *testing.T) {
		// 创建无专属项目的独立 Handler
		noProjectAccMgr := account.NewManager()
		noProjectAccMgr.Init(t.TempDir())
		noProjectAccMgr.SetAntigravityCliVersion("2.21.1")
		noProjectAccount := &account.Account{
			ID:          "acc-antigravity-no-proj",
			Email:       "no-project@gmail.com",
			Provider:    "antigravity",
			AccessToken: "ya29.test-no-project-token",
			ProjectID:   "", // 无自定义项目
			Enabled:     true,
		}
		noProjectAccMgr.AddAccount(noProjectAccount)

		var capturedHost string
		var capturedPath string
		var capturedUA string
		var capturedBody []byte

		mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			capturedHost = req.Host
			capturedPath = req.URL.Path
			capturedUA = req.Header.Get("User-Agent")
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"fallback response"}]}}]}`)),
				Header:     make(http.Header),
			}, nil
		})

		fallbackHandler := NewProxyHandler(
			noProjectAccMgr,
			session.NewRouter(),
			stats.NewTracker(pricing.NewManager()),
			stats.NewUsageTracker(pricing.NewManager()),
			stats.NewRetryErrorLogger(),
			stats.NewPacketCapturer(nil, nil, func() bool { return false }),
			func(s string) { t.Logf("[Fallback Handler Log] %s", s) },
			nil, nil,
			func(s1, s2 string) {},
			func(s string) string { return "" }, // 没有存储的项目
			func() int { return 0 },
			func() int { return 1 },
			func() int64 { return 10 * 1024 * 1024 },
			func() int { return 5 },
			nil, nil,
		)
		fallbackHandler.client = &http.Client{Transport: mockTransport}

		pluginUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Code/1.95.0"
		v1internalPayload := []byte(`{"model":"gemini-3.5-flash-high","request":{"contents":[{"parts":[{"text":"plugin prompt for personal account"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:generateContent", bytes.NewReader(v1internalPayload))
		req.Host = "127.0.0.1:18443"
		req.Header.Set("User-Agent", pluginUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		fallbackHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 核心断言 1: 直连 18443 绝不降级到 generativelanguage，必须保持 daily-cloudcode-pa 通道
		if capturedHost != "daily-cloudcode-pa.googleapis.com" {
			t.Errorf("Expected host 'daily-cloudcode-pa.googleapis.com', got %q", capturedHost)
		}

		// 核心断言 2: 目标路径必须保持 v1internal 原生接口
		expectedPath := "/v1internal:generateContent"
		if capturedPath != expectedPath {
			t.Errorf("Expected path %q, got %q", expectedPath, capturedPath)
		}

		// 核心断言 3: 自动注入官方兜底项目 ID favorable-synapse-ttvcb
		if !strings.Contains(string(capturedBody), `"project":"favorable-synapse-ttvcb"`) {
			t.Errorf("Expected injected default project ID in body, got %s", string(capturedBody))
		}

		// 核心断言 4: User-Agent 伪装为合法 Hub UA
		expectedHubUA := "antigravity/hub/2.21.1 (aidev_client; os_type=windows; arch=amd64)"
		if capturedUA != expectedHubUA {
			t.Errorf("Expected UA spoofed to %q, got %q", expectedHubUA, capturedUA)
		}
	})

	// =========================================================================
	// 场景 E: 新版 IDE 插件 (relayUserID == "") 的 Prompt Prefix 注入与 Tool Schema 清洗
	// =========================================================================
	t.Run("NewIDEPluginRequest_PromptPrefixAndToolCleaning", func(t *testing.T) {
		settingsMgr := settings.NewManager()
		settingsMgr.Init(t.TempDir())
		_ = settingsMgr.SetPromptPrefix("[System: Plugin Test Prefix] ")

		h.SettingsMgr = settingsMgr
		defer func() { h.SettingsMgr = nil }()

		// 构造携带复杂/不规范 JSON Schema（包含 $schema 与 additionalProperties）的工具请求
		dirtyPayload := []byte(`{"model":"gemini-3.5-flash-high","project":"favorable-synapse-ttvcb","tools":[{"functionDeclarations":[{"name":"queryWeather","parameters":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false,"properties":{"location":{"type":"string"}}}}]}]}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewReader(dirtyPayload))
		req.Host = "127.0.0.1:18443"
		req.Header.Set("User-Agent", "Mozilla/5.0 Code/1.95.0")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 断言 1: 工具声明中的 $schema 与 additionalProperties 已被成功清洗剥离
		bodyStr := string(capturedBody)
		if strings.Contains(bodyStr, "$schema") || strings.Contains(bodyStr, "additionalProperties") {
			t.Errorf("Expected tool schema to be cleaned for new IDE plugin, but found forbidden keys: %s", bodyStr)
		}
	})

	// =========================================================================
	// 场景 F: 18444 第三方中继调用 (relayUserID != "") 且无项目权限 -> 降级为 generativelanguage
	// =========================================================================
	t.Run("Relay18444ThirdPartyRequest_NoProject_FallbackGenerativeLanguage", func(t *testing.T) {
		noProjectAccMgr := account.NewManager()
		noProjectAccMgr.Init(t.TempDir())
		noProjectAccMgr.SetAntigravityCliVersion("2.21.1")
		noProjectAccount := &account.Account{
			ID:          "acc-no-proj-2",
			Email:       "no-proj-2@gmail.com",
			Provider:    "antigravity",
			AccessToken: "ya29.test-unwrapped-token",
			ProjectID:   "",
			Enabled:     true,
		}
		noProjectAccMgr.AddAccount(noProjectAccount)

		var capturedUnwrappedHost string
		var capturedUnwrappedBody []byte

		mockUnwrappedTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			capturedUnwrappedHost = req.Host
			capturedUnwrappedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"unwrapped ok"}]}}]}`)),
				Header:     make(http.Header),
			}, nil
		})

		unwrappedHandler := NewProxyHandler(
			noProjectAccMgr,
			session.NewRouter(),
			stats.NewTracker(pricing.NewManager()),
			stats.NewUsageTracker(pricing.NewManager()),
			stats.NewRetryErrorLogger(),
			stats.NewPacketCapturer(nil, nil, func() bool { return false }),
			func(s string) {},
			nil, nil,
			func(s1, s2 string) {},
			func(s string) string { return "" },
			func() int { return 0 },
			func() int { return 1 },
			func() int64 { return 10 * 1024 * 1024 },
			func() int { return 5 },
			nil, nil,
		)
		unwrappedHandler.client = &http.Client{Transport: mockUnwrappedTransport}

		// 根级 contents 且携带 project 字段（未包装在 request 内部）
		unwrappedPayload := []byte(`{"model":"gemini-1.5-flash","project":"expanded-palisade-stpfc","contents":[{"parts":[{"text":"unwrapped root prompt"}]}]}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18444/v1internal:generateContent", bytes.NewReader(unwrappedPayload))
		req.Host = "127.0.0.1:18444"
		req.Header.Set("User-Agent", "Cursor/0.42.0")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-token")
		req.Header.Set("X-Relay-User-Id", "relay-third-party-user")
		ctx := context.WithValue(req.Context(), RelayUserCtxKey, "relay-third-party-user")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		unwrappedHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		if capturedUnwrappedHost != "generativelanguage.googleapis.com" {
			t.Errorf("Expected host generativelanguage.googleapis.com, got %s", capturedUnwrappedHost)
		}

		// 断言: 降级后 project 字段已被清洗剥离
		if strings.Contains(string(capturedUnwrappedBody), `"project"`) {
			t.Errorf("Expected 'project' to be stripped during generativelanguage fallback, but got: %s", string(capturedUnwrappedBody))
		}
		if !strings.Contains(string(capturedUnwrappedBody), `"unwrapped root prompt"`) {
			t.Errorf("Expected prompt preserved in body, got: %s", string(capturedUnwrappedBody))
		}
	})

	// =========================================================================
	// 场景 G: 客户端 UA 带有 antigravity 插件特征（如 antigravity-vscode 或含 plugin）必须正确伪装
	// =========================================================================
	t.Run("NewIDEPluginRequest_PluginWithAntigravityPrefixUA_SpoofsToHub", func(t *testing.T) {
		pluginUA := "antigravity-vscode/1.0.0 (Windows NT 10.0)"
		payload := []byte(`{"model":"gemini-3.5-flash-high","project":"favorable-synapse-ttvcb","request":{"contents":[{"parts":[{"text":"antigravity extension prompt"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:generateContent", bytes.NewReader(payload))
		req.Host = "127.0.0.1:18443"
		req.Header.Set("User-Agent", pluginUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 断言: 扩展插件 UA 必须被正确伪装为官方 Hub UA，绝不能保留未认证的 antigravity-vscode 暴露给谷歌上游
		expectedHubUA := "antigravity/hub/2.21.1 (aidev_client; os_type=windows; arch=amd64)"
		if capturedUA != expectedHubUA {
			t.Errorf("Expected UA to be spoofed to Hub version, got %q", capturedUA)
		}
	})

	// =========================================================================
	// 场景 H: 白色 Antigravity 官方桌面端应用 (antigravity/2.6.0 ... google-api-nodejs-client/...) 原生透传
	// =========================================================================
	t.Run("WhiteAntigravityApp_PassthroughHeadersAndBody", func(t *testing.T) {
		whiteAppUA := "antigravity/2.6.0 windows/amd64 google-api-nodejs-client/10.3.0"
		whiteAppPayload := []byte(`{"project":"favorable-synapse-ttvcb","model":"gemini-3.5-flash-high","request":{"contents":[{"parts":[{"text":"hello from white desktop app"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(whiteAppPayload))
		req.Host = "daily-cloudcode-pa.googleapis.com"
		req.Header.Set("User-Agent", whiteAppUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-old-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 断言 1: User-Agent 必须保持原生白色桌面端 UA，绝不能被篡改
		if capturedUA != whiteAppUA {
			t.Errorf("User-Agent mismatch for white Antigravity app! Expected %q, got %q", whiteAppUA, capturedUA)
		}

		// 断言 2: 请求体 100% 原始透传，不得被重写或篡改
		if !bytes.Equal(capturedBody, whiteAppPayload) {
			t.Errorf("Request body was modified during passthrough!\nExpected:\n%s\nGot:\n%s", string(whiteAppPayload), string(capturedBody))
		}
	})

	// =========================================================================
	// 场景 I: 独立版 Antigravity IDE 携带 vscode_client 特征字符串原生透传 (不被误判为插件)
	// =========================================================================
	t.Run("StandaloneAntigravityIDE_WithVscodeClientUA_Passthrough", func(t *testing.T) {
		nativeIdeUA := "antigravity/ide/2.5.7 (vscode_client; os_type=windows; arch=amd64)"
		idePayload := []byte(`{"project":"favorable-synapse-ttvcb","model":"gemini-3.5-flash-high","request":{"contents":[{"parts":[{"text":"hello from standalone ide"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(idePayload))
		req.Host = "daily-cloudcode-pa.googleapis.com"
		req.Header.Set("User-Agent", nativeIdeUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-old-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 断言 1: User-Agent 保持原生独立 IDE UA
		if capturedUA != nativeIdeUA {
			t.Errorf("User-Agent mismatch for standalone IDE! Expected %q, got %q", nativeIdeUA, capturedUA)
		}

		// 断言 2: 请求体 100% 原始透传
		if !bytes.Equal(capturedBody, idePayload) {
			t.Errorf("Request body was modified during passthrough!\nExpected:\n%s\nGot:\n%s", string(idePayload), string(capturedBody))
		}
	})

	// =========================================================================
	// 场景 J: 官方原生客户端调用 (如 agy.exe / Hub) 但请求体未提供 project 字段
	// 预期: 自动补全兜底项目 favorable-synapse-ttvcb，保留原生 UA 与日常通道不被破坏
	// =========================================================================
	t.Run("NativeAntigravityRequest_NoProjectInPayload_InjectsFallbackProject", func(t *testing.T) {
		nativeHubUA := "antigravity/hub/2.28.2 (aidev_client; os_type=windows; arch=amd64; cl=1009437749)"
		noProjPayload := []byte(`{"model":"gemini-3.8-flash-high","request":{"contents":[{"parts":[{"text":"hello from agy hub"}]}]}}`)

		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:generateContent", bytes.NewReader(noProjPayload))
		req.Host = "127.0.0.1:18443"
		req.Header.Set("User-Agent", nativeHubUA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer ya29.client-old-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rec.Code)
		}

		// 断言 1: User-Agent 保持原生 Hub UA，不被篡改
		if capturedUA != nativeHubUA {
			t.Errorf("User-Agent mismatch! Expected %q, got %q", nativeHubUA, capturedUA)
		}

		// 断言 2: 上游目标 Host 维持 daily-cloudcode-pa 通道
		if capturedHost != "daily-cloudcode-pa.googleapis.com" {
			t.Errorf("Target host mismatch! Expected daily-cloudcode-pa.googleapis.com, got %q", capturedHost)
		}

		// 断言 3: 请求体成功注入了兜底项目 ID
		var parsedBody map[string]interface{}
		if err := json.Unmarshal(capturedBody, &parsedBody); err != nil {
			t.Fatalf("Failed to parse captured body: %v", err)
		}
		if parsedBody["project"] != "favorable-synapse-ttvcb" {
			t.Errorf("Expected project to be injected as favorable-synapse-ttvcb, got %v", parsedBody["project"])
		}

		// 断言 4: 原生请求的模型不被强制改写
		if parsedBody["model"] != "gemini-3.8-flash-high" {
			t.Errorf("Expected model to remain gemini-3.8-flash-high, got %v", parsedBody["model"])
		}
	})
}

