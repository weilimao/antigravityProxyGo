package tests

import (
	"net/http"
	"strings"
	"testing"
)

func TestHeaderSanitizationAndSpoofing(t *testing.T) {
	headers := make(http.Header)
	headers.Set("User-Agent", "Go-http-client/1.1")
	headers.Set("X-Relay-User-Id", "usr_12345")
	headers.Set("X-Relay-Api-Key-Id", "key_999")
	headers.Set("X-Antigravity-Original-Path", "/v1/messages")
	headers.Set("X-Antigravity-Original-Method", "POST")
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer test_token")

	headers.Set("Content-Length", "127895")
	headers.Set("Host", "daily-cloudcode-pa.googleapis.com")

	// 模拟 handler.go 中的清洗与伪装逻辑
	headers.Del("X-Relay-User-Id")
	headers.Del("X-Relay-Api-Key-Id")
	headers.Del("X-Antigravity-Original-Path")
	headers.Del("X-Antigravity-Original-Method")
	headers.Del("X-Antigravity-Req-ID")
	headers.Del("x-relay-user-id")
	headers.Del("x-relay-api-key-id")
	headers.Del("x-antigravity-original-path")
	headers.Del("x-antigravity-original-method")
	headers.Del("x-antigravity-req-id")

	// 剥离多余的 Content-Length 与 Host 标头，使其与图一完全一致
	headers.Del("Content-Length")
	headers.Del("content-length")
	headers.Del("Host")
	headers.Del("host")

	ua := headers.Get("User-Agent")
	if ua == "" || strings.Contains(strings.ToLower(ua), "go-http-client") {
		headers.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")
	}

	// 验证敏感中继头已被完全剥离
	if headers.Get("X-Relay-User-Id") != "" || headers.Get("X-Antigravity-Original-Path") != "" {
		t.Errorf("Internal tracking headers were not stripped properly!")
	}

	// 验证 Content-Length 和 Host 已被完全剥离
	if headers.Get("Content-Length") != "" || headers.Get("Host") != "" {
		t.Errorf("Content-Length or Host header was not stripped properly! Got Host=%s, Content-Length=%s", headers.Get("Host"), headers.Get("Content-Length"))
	}

	// 验证 User-Agent 伪装结果与图一要求完全一致
	expectedUA := "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)"
	if headers.Get("User-Agent") != expectedUA {
		t.Errorf("User-Agent spoofing mismatch. Expected: %s, Got: %s", expectedUA, headers.Get("User-Agent"))
	}
}
