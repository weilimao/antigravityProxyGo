package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

func TestRewriteModelInPayload(t *testing.T) {
	t.Run("根层级模型改写-标准v1internal格式", func(t *testing.T) {
		raw := `{"enabledCreditTypes":["AGENT_DEF_AI"],"model":"gemini-3.8-flash-high","project":"test-proj","request":{"contents":[{"parts":["hello"],"role":"user"}]}}`
		newBytes, modified := RewriteModelInPayload([]byte(raw), "gemini-4-argon-high")
		if !modified {
			t.Fatalf("expected modified to be true")
		}

		var res map[string]interface{}
		if err := json.Unmarshal(newBytes, &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if res["model"] != "gemini-4-argon-high" {
			t.Errorf("expected model to be gemini-4-argon-high, got %v", res["model"])
		}
		if res["project"] != "test-proj" {
			t.Errorf("expected project to be preserved, got %v", res["project"])
		}
	})

	t.Run("带models前缀形态一致性", func(t *testing.T) {
		raw := `{"model":"models/gemini-pro","request":{}}`
		newBytes, modified := RewriteModelInPayload([]byte(raw), "gemini-4-argon-high")
		if !modified {
			t.Fatalf("expected modified to be true")
		}
		var res map[string]interface{}
		if err := json.Unmarshal(newBytes, &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if res["model"] != "models/gemini-4-argon-high" {
			t.Errorf("expected models/gemini-4-argon-high, got %v", res["model"])
		}
	})

	t.Run("目标模型自带models前缀而原始无前缀", func(t *testing.T) {
		raw := `{"model":"gemini-pro"}`
		newBytes, modified := RewriteModelInPayload([]byte(raw), "models/gemini-4-argon-high")
		if !modified {
			t.Fatalf("expected modified to be true")
		}
		var res map[string]interface{}
		if err := json.Unmarshal(newBytes, &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if res["model"] != "gemini-4-argon-high" {
			t.Errorf("expected gemini-4-argon-high (trimmed prefix), got %v", res["model"])
		}
	})

	t.Run("嵌套在request内部的model字段改写", func(t *testing.T) {
		raw := `{"request":{"model":"gemini-1.5-flash","contents":[]}}`
		newBytes, modified := RewriteModelInPayload([]byte(raw), "gemini-4-argon-high")
		if !modified {
			t.Fatalf("expected modified to be true")
		}
		var res map[string]interface{}
		if err := json.Unmarshal(newBytes, &res); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		reqMap := res["request"].(map[string]interface{})
		if reqMap["model"] != "gemini-4-argon-high" {
			t.Errorf("expected inner model to be gemini-4-argon-high, got %v", reqMap["model"])
		}
	})

	t.Run("空载荷与非JSON载荷安全返回", func(t *testing.T) {
		emptyBytes, mod1 := RewriteModelInPayload(nil, "gemini-4-argon-high")
		if mod1 || len(emptyBytes) != 0 {
			t.Errorf("expected false for nil payload")
		}

		nonJson := []byte("plain text message")
		retBytes, mod2 := RewriteModelInPayload(nonJson, "gemini-4-argon-high")
		if mod2 || !reflect.DeepEqual(retBytes, nonJson) {
			t.Errorf("expected false and unmodified text for non-JSON payload")
		}
	})

	t.Run("无模型字段与相同模型幂等性", func(t *testing.T) {
		noModel := []byte(`{"user":"anonymous","count":42}`)
		ret1, mod1 := RewriteModelInPayload(noModel, "gemini-4-argon-high")
		if mod1 || !reflect.DeepEqual(ret1, noModel) {
			t.Errorf("expected no modification for payload without model field")
		}

		sameModel := []byte(`{"model":"gemini-4-argon-high"}`)
		ret2, mod2 := RewriteModelInPayload(sameModel, "gemini-4-argon-high")
		if mod2 || !reflect.DeepEqual(ret2, sameModel) {
			t.Errorf("expected no modification when target model is identical")
		}
	})

	t.Run("目标模型为空或unknown跳过", func(t *testing.T) {
		raw := []byte(`{"model":"gemini-3.8-flash-high"}`)
		_, mod1 := RewriteModelInPayload(raw, "")
		if mod1 {
			t.Errorf("expected false when targetModel is empty")
		}
		_, mod2 := RewriteModelInPayload(raw, "unknown")
		if mod2 {
			t.Errorf("expected false when targetModel is unknown")
		}
	})
}

// TestGlobalModelOverride_V1InternalEndToEnd 业务端到端测试：
// 模拟原生 Antigravity 客户端调用 v1internal 接口，验证开启全局覆写后：
// 1. 发往 Google 上游的真实 HTTP 报文里的 model 被精准覆写为目标模型；
// 2. 统计 Tracker 记录的 RequestBody 里的 model 亦同步为目标模型（解决前端弹窗脱节问题）；
// 3. Tab 补全白名单模型保持绕过无损透传；
// 4. 全局覆写关闭时保持客户端原始模型透传。
func TestGlobalModelOverride_V1InternalEndToEnd(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 初始化账号与设置
	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	poolAccount := &account.Account{
		ID:          "acc-ag-1",
		Email:       "test-ag@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.test-pool-token",
		ProjectID:   "favorable-synapse-ttvcb",
		Enabled:     true,
	}
	accMgr.AddAccount(poolAccount)

	settingsMgr := settings.NewManager()
	settingsMgr.Init(tempDir)

	sessionRouter := session.NewRouter()
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	var capturedUpstreamBody []byte
	mockTransport := mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var errRead error
		capturedUpstreamBody, errRead = io.ReadAll(req.Body)
		if errRead != nil {
			t.Errorf("failed to read upstream body: %v", errRead)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}\n\n`)),
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
		func(s string) { t.Logf("[ProxyLog] %s", s) },
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
	h.SettingsMgr = settingsMgr
	h.client = &http.Client{Transport: mockTransport}

	t.Run("用例1: 开启全局覆写-原生v1internal请求体和日志均同步改写", func(t *testing.T) {
		settingsMgr.SetCustomModelOverrideEnabled(true)
		settingsMgr.SetCustomModelOverrideID("gemini-4-argon-high")

		rawPayload := `{"enabledCreditTypes":["AGENT_DEF_AI"],"model":"gemini-3.8-flash-high","project":"reverable-synapse-1ixb","request":{"contents":[{"parts":["hello"],"role":"user"}]}}`
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(rawPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "antigravity/hub/2.22.1 (sid=v_client; os_type=windows; arch=amd64)")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		// 验证 1: 上游捕获的真实 HTTP 请求体中的 model 字段
		var upstreamMap map[string]interface{}
		if err := json.Unmarshal(capturedUpstreamBody, &upstreamMap); err != nil {
			t.Fatalf("failed to unmarshal upstream body: %v", err)
		}
		if upstreamMap["model"] != "gemini-4-argon-high" {
			t.Errorf("expected upstream model to be gemini-4-argon-high, got %v", upstreamMap["model"])
		}

		// 验证 2: statsTracker 中记录的 RequestBody
		detailsBody := statsTracker.GetRecentRequestBody()
		bodyJson, _ := json.Marshal(detailsBody)
		var trackerMap map[string]interface{}
		_ = json.Unmarshal(bodyJson, &trackerMap)
		if trackerMap["model"] != "gemini-4-argon-high" {
			t.Errorf("expected tracker recorded model to be gemini-4-argon-high, got %v", trackerMap["model"])
		}
	})

	t.Run("用例2: Tab补全模型白名单绕过-保持原始模型不被覆写", func(t *testing.T) {
		settingsMgr.SetCustomModelOverrideEnabled(true)
		settingsMgr.SetCustomModelOverrideID("gemini-4-argon-high")

		tabPayload := `{"model":"tab_flash_lite_preview","request":{"contents":[]}}`
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(tabPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "antigravity/hub/2.22.1")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var upstreamMap map[string]interface{}
		_ = json.Unmarshal(capturedUpstreamBody, &upstreamMap)
		if upstreamMap["model"] != "tab_flash_lite_preview" {
			t.Errorf("expected tab model to be bypassed and stay tab_flash_lite_preview, got %v", upstreamMap["model"])
		}
	})

	t.Run("用例3: 全局覆写关闭-保持客户端原模型透传", func(t *testing.T) {
		settingsMgr.SetCustomModelOverrideEnabled(false)

		origPayload := `{"model":"gemini-3.8-flash-high","request":{"contents":[]}}`
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18443/v1internal:streamGenerateContent?alt=sse", bytes.NewBufferString(origPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "antigravity/hub/2.22.1")
		req.Header.Set("Authorization", "Bearer ya29.client-token")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var upstreamMap map[string]interface{}
		_ = json.Unmarshal(capturedUpstreamBody, &upstreamMap)
		if upstreamMap["model"] != "gemini-3.8-flash-high" {
			t.Errorf("expected original model gemini-3.8-flash-high when disabled, got %v", upstreamMap["model"])
		}
	})
}
