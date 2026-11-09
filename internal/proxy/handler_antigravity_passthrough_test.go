package proxy

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
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
		expectedHubUA := accMgr.GetAntigravityUserAgent()
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
		expectedHubUA := accMgr.GetAntigravityUserAgent()
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
		expectedHubUA := accMgr.GetAntigravityUserAgent()
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
		expectedHubUA := accMgr.GetAntigravityUserAgent()
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

// TestMultiAccountSessionIsolationAndProjectRewrite 验证：
// 1. 客户端仅单号登录（或免登录携带 Mock "favorable-synapse-ttvcb" / 主号 Project），开启多个新会话时自动均分至不同号池账号；
// 2. 每个出站账号自动重写为其各自真实的专属 ProjectID（严禁将客户端的 Project 反向污染写回给其他池账号）；
// 3. 剥离 Sec-Ch-Ua / X-Client-Data 等冲突浏览器特征头，同时保持原生 User-Agent 与模型字段不变；
// 4. clientForAttempt 为不同池账号返回各自物理隔离的 *http.Transport 与 TLS Session Cache。
func TestMultiAccountSessionIsolationAndProjectRewrite(t *testing.T) {
	tempDir := t.TempDir()
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetPoolMode(true)

	accAlpha := &account.Account{
		ID:          "acc-alpha-id",
		Email:       "alpha@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-alpha",
		ProjectID:   "", // 通过 storedProjects 提供真实专属项目
		Enabled:     true,
	}
	accBeta := &account.Account{
		ID:          "acc-beta-id",
		Email:       "beta@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.token-beta",
		ProjectID:   "real-gcp-project-beta",
		Enabled:     true,
	}
	accMgr.AddAccount(accAlpha)
	accMgr.AddAccount(accBeta)

	storedProjects := map[string]string{
		"alpha@example.com": "real-gcp-project-alpha",
	}
	capturedPollutions := make(map[string]string)

	type outboundRecord struct {
		auth      string
		project   string
		model     string
		ua        string
		secChUa   string
		clientDat string
	}
	var records []outboundRecord

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]interface{}
		_ = json.Unmarshal(bodyBytes, &parsed)
		proj, _ := parsed["project"].(string)
		model, _ := parsed["model"].(string)
		records = append(records, outboundRecord{
			auth:      req.Header.Get("Authorization"),
			project:   proj,
			model:     model,
			ua:        req.Header.Get("User-Agent"),
			secChUa:   req.Header.Get("Sec-Ch-Ua"),
			clientDat: req.Header.Get("X-Client-Data"),
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		session.NewRouter(),
		stats.NewTracker(pricing.NewManager()),
		stats.NewUsageTracker(pricing.NewManager()),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(s string) {},
		nil,
		nil,
		func(email, proj string) {
			capturedPollutions[email] = proj
		},
		func(email string) string {
			return storedProjects[email]
		},
		func() int { return 1 },
		func() int { return 1 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 },
		nil,
		nil,
	)

	// 验证真实生产模式下 clientForAttempt 为不同账号分配物理隔离的 Transport
	clientAlpha := h.clientForAttempt(&routeOutcome{poolAccount: accAlpha})
	clientBeta := h.clientForAttempt(&routeOutcome{poolAccount: accBeta})
	if clientAlpha.Transport == clientBeta.Transport {
		t.Fatalf("expected isolated http.Transport per pool account, got shared transport")
	}

	// 切换为 mockTransport 拦截断言出站请求头与请求体
	h.client = &http.Client{Transport: mockTransport}

	nativeUA := "antigravity/hub/2.28.2 (aidev_client; os_type=windows; arch=amd64)"

	// 会话 1：客户端携带 Mock 项目 "favorable-synapse-ttvcb" 与新会话 UUID 1
	payloadConv1 := []byte(`{"project":"favorable-synapse-ttvcb","model":"gemini-3.5-pro","requestId":"agent/11111111-aaaa-bbbb-cccc-111111111111/req-1","request":{"contents":[{"parts":[{"text":"session 1"}]}]}}`)
	req1 := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(payloadConv1))
	req1.Host = "daily-cloudcode-pa.googleapis.com"
	req1.Header.Set("User-Agent", nativeUA)
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer ya29.ide-single-login-token")
	req1.Header.Set("Sec-Ch-Ua", `"Chromium";v="130"`)
	req1.Header.Set("X-Client-Data", "CJa2yQEIpbbJAQ==")

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("session 1 expected 200, got %d", rec1.Code)
	}

	// 会话 2：客户端同样只登录了单账号（携带客户端主号项目 "ide-logged-in-account-project"），但开启了新会话 UUID 2
	payloadConv2 := []byte(`{"project":"ide-logged-in-account-project","model":"gemini-3.5-pro","requestId":"agent/22222222-dddd-eeee-ffff-222222222222/req-1","request":{"contents":[{"parts":[{"text":"session 2"}]}]}}`)
	req2 := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(payloadConv2))
	req2.Host = "daily-cloudcode-pa.googleapis.com"
	req2.Header.Set("User-Agent", nativeUA)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer ya29.ide-single-login-token")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("session 2 expected 200, got %d", rec2.Code)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 outbound requests, got %d", len(records))
	}

	// 断言 1: 两个新会话被自动均分到两个不同的池账号（空闲账号优先策略）
	if records[0].auth == records[1].auth {
		t.Fatalf("expected new conversations to be distributed to different idle accounts, both got %s", records[0].auth)
	}

	// 断言 2: 每个账号出站时的 Authorization Token 与其专属真实 ProjectID 严格对齐，绝不沿用客户端的假 Project 或主号 Project
	expectedTokenToProj := map[string]string{
		"Bearer ya29.token-alpha": "real-gcp-project-alpha",
		"Bearer ya29.token-beta":  "real-gcp-project-beta",
	}
	for i, rec := range records {
		wantProj, ok := expectedTokenToProj[rec.auth]
		if !ok {
			t.Fatalf("record[%d] unexpected Authorization header: %s", i, rec.auth)
		}
		if rec.project != wantProj {
			t.Errorf("record[%d] token %s expected aligned project %q, got %q", i, rec.auth, wantProj, rec.project)
		}
		if rec.ua != nativeUA {
			t.Errorf("record[%d] expected native UA %q preserved, got %q", i, nativeUA, rec.ua)
		}
		if rec.model != "gemini-3.5-pro" {
			t.Errorf("record[%d] expected model gemini-3.5-pro preserved, got %q", i, rec.model)
		}
		if rec.secChUa != "" || rec.clientDat != "" {
			t.Errorf("record[%d] expected conflicting browser headers stripped, got Sec-Ch-Ua=%q X-Client-Data=%q", i, rec.secChUa, rec.clientDat)
		}
	}

	// 断言 3: 客户端请求体中的 Project 绝不能反向污染覆盖池账号 alpha 或 beta 的存储记录
	if len(capturedPollutions) > 0 {
		t.Errorf("expected zero cross-account project pollution, but setCapturedProject was called with: %v", capturedPollutions)
	}
}

func TestModel404AutoSwitchAndFetchModelsPool(t *testing.T) {
	tmpDir := t.TempDir()
	accMgr := account.NewManager()
	accMgr.Init(tmpDir)
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)
	accMgr.SetAntigravityCliVersion("2.25.0")

	// acc-no-argon: 尚未探测模型列表，但实际不支持 gemini-4-argon-medium（会返回 404）
	accNoArgon := &account.Account{
		ID:          "acc-no-argon",
		Email:       "weilimao666@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.tok-no-argon",
		ProjectID:   "proj-no-argon",
		Enabled:     true,
	}
	// acc-with-argon: 支持 gemini-4-argon-medium（返回 200 OK）
	accWithArgon := &account.Account{
		ID:          "acc-with-argon",
		Email:       "weilimao0714@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.tok-with-argon",
		ProjectID:   "proj-with-argon",
		Enabled:     true,
	}
	accMgr.AddAccount(accNoArgon)
	accMgr.AddAccount(accWithArgon)

	var attemptedAuths []string
	var firstFailedAuth string
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		auth := req.Header.Get("Authorization")
		attemptedAuths = append(attemptedAuths, auth)

		if strings.Contains(req.URL.Path, "fetchAvailableModels") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
					"models": {
						"gemini-2.5-flash": {"displayName": "Gemini 2.5 Flash", "disabled": false},
						"gemini-4-argon-medium": {"displayName": "Gemini 4 Argon (Medium)", "disabled": false},
						"claude-opus-5-5-high": {"displayName": "Claude Opus 5.5 (High)", "disabled": false}
					}
				}`)),
			}, nil
		}

		// 模拟第一个被分流命中的账号不支持 gemini-4-argon-medium 返回 404，第二个账号支持并返回 200
		if firstFailedAuth == "" {
			firstFailedAuth = auth
		}
		if auth == firstFailedAuth {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`)),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[{"content":{"parts":[{"text":"argon_ok"}]}}]}}`)),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		session.NewRouter(),
		stats.NewTracker(pricing.NewManager()),
		stats.NewUsageTracker(pricing.NewManager()),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(s string) { t.Logf("[404 Switch Log] %s", s) },
		nil, nil,
		func(email, proj string) {},
		func(email string) string { return "" },
		func() int { return 3 }, // getMaxRetries = 3
		func() int { return 0 }, // getMaxRetryDelay = 0s
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 }, // getRequestTimeout = 5s
		nil, nil,
	)
	h.client = &http.Client{Transport: mockTransport}

	// 1. 首次请求 gemini-4-argon-medium：若先命中 acc-no-argon 返回 404，应自动标记 acc-no-argon 不支持该模型并立即无缝重试 acc-with-argon 返回 200
	payload := []byte(`{"project":"favorable-synapse-ttvcb","model":"gemini-4-argon-medium","requestId":"agent/33333333-aaaa-bbbb-cccc-333333333333/req-1","request":{"contents":[{"parts":[{"text":"hello argon"}]}]}}`)
	req := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(payload))
	req.Host = "daily-cloudcode-pa.googleapis.com"
	req.Header.Set("User-Agent", "antigravity/hub/2.25.0 (aidev_client; os_type=windows; arch=amd64)")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ya29.ide-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 404 on first account to auto-switch and succeed with 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(attemptedAuths) != 2 || attemptedAuths[0] == attemptedAuths[1] {
		t.Fatalf("expected 2 attempts across 2 distinct accounts on 404 switch, got: %v", attemptedAuths)
	}
	if !accNoArgon.IsModelMarkedUnsupported("gemini-4-argon-medium", 0) && !accWithArgon.IsModelMarkedUnsupported("gemini-4-argon-medium", 0) {
		t.Fatalf("expected the 404 account to have gemini-4-argon-medium marked unsupported")
	}

	// 2. fetchAvailableModels 不走号池，保留客户端自己的 Token。
	before := len(attemptedAuths)
	fetchReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", strings.NewReader(`{"project":"favorable-synapse-ttvcb"}`))
	fetchReq.Host = "daily-cloudcode-pa.googleapis.com"
	fetchReq.Header.Set("User-Agent", "antigravity/hub/2.25.0 (aidev_client; os_type=windows; arch=amd64)")
	fetchReq.Header.Set("Content-Type", "application/json")
	fetchReq.Header.Set("Authorization", "Bearer ya29.ide-token")

	fetchRec := httptest.NewRecorder()
	h.ServeHTTP(fetchRec, fetchReq)
	if fetchRec.Code != http.StatusOK {
		t.Fatalf("expected fetchAvailableModels 200, got %d", fetchRec.Code)
	}
	if len(attemptedAuths) != before+1 || attemptedAuths[before] != "Bearer ya29.ide-token" {
		t.Fatalf("fetchAvailableModels should keep the client token, got %v", attemptedAuths[before:])
	}
}

// TestNativeLoadCodeAssistStickyAndSurgicalProject 验证官方客户端：
// loadCodeAssist 与 fetchAvailableModels 保留桌面端自己的 Token 原样转发；
// 只有模型请求换号池 Token，并且只替换顶层 project。
func TestNativeLoadCodeAssistStickyAndSurgicalProject(t *testing.T) {
	tempDir := t.TempDir()
	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)

	acc := &account.Account{
		ID:          "acc-desktop",
		Email:       "desktop@example.com",
		Provider:    "antigravity",
		AccessToken: "ya29.pool-desktop",
		ProjectID:   "real-gcp-project",
		Enabled:     true,
	}
	accMgr.AddAccount(acc)

	var outboundAuths []string
	var outboundBodies [][]byte

	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		outboundAuths = append(outboundAuths, req.Header.Get("Authorization"))
		body, _ := io.ReadAll(req.Body)
		outboundBodies = append(outboundBodies, body)
		if strings.Contains(req.URL.Path, "loadCodeAssist") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"cloudaicompanionProject":"real-gcp-project","currentTier":{"id":"g1-pro-tier"}}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"models":{}}`)),
		}, nil
	})

	h := NewProxyHandler(
		accMgr,
		session.NewRouter(),
		stats.NewTracker(pricing.NewManager()),
		stats.NewUsageTracker(pricing.NewManager()),
		stats.NewRetryErrorLogger(),
		stats.NewPacketCapturer(nil, nil, func() bool { return false }),
		func(string) {},
		nil, nil,
		func(string, string) {},
		func(string) string { return "" },
		func() int { return 0 },
		func() int { return 0 },
		func() int64 { return 10 * 1024 * 1024 },
		func() int { return 5 },
		nil, nil,
	)
	h.client = &http.Client{Transport: mockTransport}

	nativeUA := "antigravity/2.29.0 windows/amd64"
	loadBody := []byte(`{"metadata":{"ideName":"antigravity","ideVersion":"2.29.0","platform":"WINDOWS_AMD64"}}`)
	loadReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", bytes.NewReader(loadBody))
	loadReq.Host = "daily-cloudcode-pa.googleapis.com"
	loadReq.Header.Set("User-Agent", nativeUA)
	loadReq.Header.Set("Content-Type", "application/json")
	loadReq.Header.Set("Authorization", "Bearer ya29.client-login")
	loadRec := httptest.NewRecorder()
	h.ServeHTTP(loadRec, loadReq)
	if loadRec.Code != http.StatusOK {
		t.Fatalf("loadCodeAssist status %d body %s", loadRec.Code, loadRec.Body.String())
	}
	if !strings.Contains(loadRec.Body.String(), "real-gcp-project") {
		t.Fatalf("loadCodeAssist was not forwarded: %s", loadRec.Body.String())
	}
	if len(outboundBodies) != 1 || !bytes.Equal(outboundBodies[0], loadBody) {
		t.Fatalf("loadCodeAssist body changed\nwant: %s\ngot:  %s", loadBody, outboundBodies[0])
	}

	fetchReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", strings.NewReader(`{"project":"real-gcp-project"}`))
	fetchReq.Host = "daily-cloudcode-pa.googleapis.com"
	fetchReq.Header.Set("User-Agent", nativeUA)
	fetchReq.Header.Set("Content-Type", "application/json")
	fetchReq.Header.Set("Authorization", "Bearer ya29.client-login")
	fetchRec := httptest.NewRecorder()
	h.ServeHTTP(fetchRec, fetchReq)
	if fetchRec.Code != http.StatusOK {
		t.Fatalf("fetchAvailableModels status %d", fetchRec.Code)
	}
	if len(outboundAuths) != 2 || outboundAuths[0] != "Bearer ya29.client-login" || outboundAuths[1] != outboundAuths[0] {
		t.Fatalf("bootstrap calls should keep the desktop token: %v", outboundAuths)
	}

	raw := []byte(`{"enabledCreditTypes":["GOOGLE_ONE_AI"],"model":"gemini-3.5-flash","project":"client-project","userAgent":"antigravity","requestId":"agent/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/req-1","request":{"contents":[{"parts":[{"text":"keep project client-project"}]}]}}`)
	genReq := httptest.NewRequest(http.MethodPost, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(raw))
	genReq.Host = "daily-cloudcode-pa.googleapis.com"
	genReq.Header.Set("User-Agent", nativeUA)
	genReq.Header.Set("Content-Type", "application/json")
	genReq.Header.Set("Authorization", "Bearer ya29.client-login")
	genRec := httptest.NewRecorder()
	h.ServeHTTP(genRec, genReq)
	if genRec.Code != http.StatusOK {
		t.Fatalf("generateContent status %d", genRec.Code)
	}
	want, ok := replaceTopLevelJSONString(raw, "project", "real-gcp-project")
	if !ok {
		t.Fatal("fixture replace failed")
	}
	if len(outboundBodies) != 3 || !bytes.Equal(outboundBodies[2], want) {
		t.Fatalf("model body was rewritten beyond project\nwant: %s\ngot:  %s", want, outboundBodies[len(outboundBodies)-1])
	}
	if len(outboundAuths) != 3 || outboundAuths[2] != "Bearer ya29.pool-desktop" {
		t.Fatalf("model request token = %v", outboundAuths)
	}
}



