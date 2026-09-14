package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExtractRoutedModelStream_ResponsesSmartStream 验证 /responses 端点与 Accept 头的智能流式感知能力
func TestExtractRoutedModelStream_ResponsesSmartStream(t *testing.T) {
	// 1. /route/v1/responses 请求未显式写 stream 字段时，默认应识别为流式 (Codex 规范)
	{
		body := []byte(`{"model":"auto","input":[{"type":"message","role":"user","content":"hi"}]}`)
		model, streaming, err := extractRoutedModelStream("/route/v1/responses", nil, body)
		if err != nil {
			t.Fatalf("extractRoutedModelStream failed: %v", err)
		}
		if model != "auto" {
			t.Errorf("expected model 'auto', got %q", model)
		}
		if !streaming {
			t.Errorf("expected streaming=true for /route/v1/responses default, got false")
		}
	}

	// 2. 带有 Accept: text/event-stream 请求头时，强制识别为流式
	{
		header := make(http.Header)
		header.Set("Accept", "text/event-stream")
		body := []byte(`{"model":"gpt-4o","stream":false}`)
		model, streaming, err := extractRoutedModelStream("/route/v1/chat/completions", header, body)
		if err != nil {
			t.Fatalf("extractRoutedModelStream failed: %v", err)
		}
		if model != "gpt-4o" {
			t.Errorf("expected model 'gpt-4o', got %q", model)
		}
		if !streaming {
			t.Errorf("expected streaming=true when Accept: text/event-stream is present, got false")
		}
	}

	// 3. /route/v1/responses 显式带 stream: false 且无 Accept SSE 时尊重非流式配置
	{
		body := []byte(`{"model":"auto","stream":false,"input":[]}`)
		model, streaming, err := extractRoutedModelStream("/route/v1/responses", nil, body)
		if err != nil {
			t.Fatalf("extractRoutedModelStream failed: %v", err)
		}
		if model != "auto" {
			t.Errorf("expected model 'auto', got %q", model)
		}
		if streaming {
			t.Errorf("expected streaming=false when explicitly configured stream:false, got true")
		}
	}
}

// TestAutoRaceCoordinator_HeaderSanitization 验证 claimVictory 安全清洗响应头，防止流式输出截断
func TestAutoRaceCoordinator_HeaderSanitization(t *testing.T) {
	rec := httptest.NewRecorder()
	coord := newAutoRaceCoordinator(rec, 2, nil)

	mockHeaders := make(http.Header)
	mockHeaders.Set("Content-Type", "text/event-stream")
	mockHeaders.Set("Content-Length", "1024")      // 必须被剔除
	mockHeaders.Set("Transfer-Encoding", "chunked") // 必须被剔除
	mockHeaders.Set("Connection", "keep-alive")     // 必须被剔除
	mockHeaders.Set("X-Custom-Model", "qwen-test")  // 应被保留

	firstChunk := []byte("event: response.created\ndata: {}\n\n")

	won := coord.claimVictory(0, "test-model", http.StatusOK, mockHeaders, firstChunk)
	if !won {
		t.Fatalf("expected claimVictory to return true for first branch")
	}

	// 验证实际写给客户端的 Headers
	if rec.Header().Get("Content-Length") != "" {
		t.Errorf("Content-Length should be stripped from real response, but got %q", rec.Header().Get("Content-Length"))
	}
	if rec.Header().Get("Transfer-Encoding") != "" {
		t.Errorf("Transfer-Encoding should be stripped from real response, but got %q", rec.Header().Get("Transfer-Encoding"))
	}
	if rec.Header().Get("Connection") != "" {
		t.Errorf("Connection should be stripped from real response, but got %q", rec.Header().Get("Connection"))
	}
	if rec.Header().Get("X-Custom-Model") != "qwen-test" {
		t.Errorf("Custom headers should be preserved, got %q", rec.Header().Get("X-Custom-Model"))
	}

	// 验证首包输出
	if !strings.Contains(rec.Body.String(), "event: response.created") {
		t.Errorf("expected firstChunk to be written to client, got %q", rec.Body.String())
	}
}
