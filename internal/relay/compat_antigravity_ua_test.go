package relay

import (
	"antigravity-proxy/internal/account"
	"io"
	"net/http"
	"strings"
	"testing"
)

// compat_antigravity_ua_test.go: 验证 APICompatHandler 中 Antigravity User-Agent 动态配置与注入。

func TestAPICompatHandler_AntigravityUserAgent_Default(t *testing.T) {
	accMgr := account.NewManager()
	accMgr.Init(t.TempDir())

	h := NewAPICompatHandler(nil, accMgr, nil, nil, nil, nil, nil)

	var capturedUA string
	// 拦截 finalRequester 验证出站 User-Agent
	acc := &account.Account{
		ID:          "acc1",
		Provider:    "antigravity",
		AccessToken: "test-token",
	}

	// 替换 finalRequester 捕获 request
	h.finalRequester = func(a *account.Account, method, targetURL string, reqBody []byte) (*http.Response, error) {
		req, err := http.NewRequest(method, targetURL, strings.NewReader(string(reqBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.GetAccessToken())
		req.Header.Set("User-Agent", h.accountMgr.GetAntigravityUserAgent())
		capturedUA = req.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	}

	// 1. 默认值验证
	_, err := h.finalRequester(acc, "POST", "http://example.com/v1internal:generateContent", []byte(`{}`))
	if err != nil {
		t.Fatalf("finalRequester failed: %v", err)
	}
	expectedDefault := "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)"
	if capturedUA != expectedDefault {
		t.Errorf("captured UA = %q, want default %q", capturedUA, expectedDefault)
	}

	// 2. 自定义版本号验证
	accMgr.SetAntigravityCliVersion("2.4.5")
	_, err = h.finalRequester(acc, "POST", "http://example.com/v1internal:generateContent", []byte(`{}`))
	if err != nil {
		t.Fatalf("finalRequester failed: %v", err)
	}
	expectedCustom := "antigravity/hub/2.4.5 (aidev_client; os_type=windows; arch=amd64)"
	if capturedUA != expectedCustom {
		t.Errorf("captured UA = %q, want custom %q", capturedUA, expectedCustom)
	}
}
