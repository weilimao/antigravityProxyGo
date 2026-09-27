package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProxyPassthroughOpenAI_UsageExtraction 验证优化后的快速嗅探逻辑能够 100% 准确提取 Usage
func TestProxyPassthroughOpenAI_UsageExtraction(t *testing.T) {
	h := &APICompatHandler{
		logFn: func(s string) {},
	}

	// 模拟典型的 OpenAI 流式响应（前 3 帧为普通文本 delta，第 4 帧为带 usage 的末帧，第 5 帧为 [DONE]）
	sseBody := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hello"}}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":" world"}}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"!"}}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":15,"completion_tokens":25,"prompt_tokens_details":{"cached_tokens":5}}}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}, "")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(sseBody)),
		Header:     make(http.Header),
	}

	rec := httptest.NewRecorder()
	inUsage, outUsage, cachedUsage := h.proxyPassthroughOpenAI(rec, resp, true, nil)

	if inUsage != 15 {
		t.Errorf("expected inUsage=15, got %d", inUsage)
	}
	if outUsage != 25 {
		t.Errorf("expected outUsage=25, got %d", outUsage)
	}
	if cachedUsage != 5 {
		t.Errorf("expected cachedUsage=5, got %d", cachedUsage)
	}

	// 验证回写给客户端的数据完整性（所有帧均完整回写，无任何丢失）
	written := rec.Body.String()
	if !strings.Contains(written, "Hello") || !strings.Contains(written, "world") || !strings.Contains(written, "[DONE]") {
		t.Errorf("client output stream truncated or corrupted: %s", written)
	}
}

// TestProxyPassthroughAnthropic_UsageExtraction 验证 Anthropic 协议下流式快速跳过与 Usage 嗅探准确性
func TestProxyPassthroughAnthropic_UsageExtraction(t *testing.T) {
	h := &APICompatHandler{
		logFn: func(s string) {},
	}

	// 模拟典型的 Anthropic 流式响应
	sseBody := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":42}}}` + "\n\n",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}` + "\n\n",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n",
		`event: message_delta` + "\n" + `data: {"type":"message_delta","usage":{"output_tokens":88,"cache_read_input_tokens":10}}` + "\n\n",
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
	}, "")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(sseBody)),
		Header:     make(http.Header),
	}

	rec := httptest.NewRecorder()
	inUsage, outUsage, cachedUsage := h.proxyPassthroughAnthropic(rec, resp, true, nil, nil)

	if inUsage != 42 {
		t.Errorf("expected inUsage=42, got %d", inUsage)
	}
	if outUsage != 88 {
		t.Errorf("expected outUsage=88, got %d", outUsage)
	}
	if cachedUsage != 10 {
		t.Errorf("expected cachedUsage=10, got %d", cachedUsage)
	}

	written := rec.Body.String()
	if !strings.Contains(written, `"Hi"`) || !strings.Contains(written, `" there"`) || !strings.Contains(written, "message_stop") {
		t.Errorf("anthropic output stream truncated or corrupted: %s", written)
	}
}

// BenchmarkProxyPassthrough_Optimized 压测优化后的 SSE 解析吞吐与延迟
func BenchmarkProxyPassthrough_Optimized(b *testing.B) {
	h := &APICompatHandler{
		logFn: func(s string) {},
	}

	// 构造 100 个普通 token 帧与 1 个末尾 usage 帧
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf(`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"token_%d"}}]}%s`, i, "\n\n"))
	}
	sb.WriteString(`data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":100}}` + "\n\n")
	sb.WriteString(`data: [DONE]` + "\n\n")
	payload := sb.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(payload)),
			Header:     make(http.Header),
		}
		rec := httptest.NewRecorder()
		h.proxyPassthroughOpenAI(rec, resp, true, nil)
	}
}
