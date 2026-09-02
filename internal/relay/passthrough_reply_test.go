package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/stats"
)

// passthrough_reply_test.go: proxyPassthroughAnthropic 流式 usage 嗅探单元测试。
//
// 锁定 Anthropic SSE 流式透传路径对 input_tokens 的捕获语义:
//   - message_start.message.usage.input_tokens 为输入 token 权威来源;
//   - message_delta 通常只带 output_tokens(标准 Anthropic 协议),不得清零 message_start 已设的 inUsage;
//   - 上游全程缺 input_tokens 时,流末按入站请求体估算兜底。

// makeAnthropicSSEResponse 构造一个 Content-Type: text/event-stream 的 *http.Response,
// body 为给定 SSE 文本。
func makeAnthropicSSEResponse(t *testing.T, sse string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
}

// TestProxyPassthroughAnthropic_MessageStartInputTokens 锁定:上游 message_start 携带
// 非零 input_tokens,而 message_delta 只有 output_tokens(标准 Anthropic 协议)时,
// inUsage 必须从 message_start 正确捕获,不被 message_delta 清零。
//
// 复现场景:api.radium.cloud / hal-1.0 遵循标准 Anthropic SSE——input_tokens 仅在
// message_start 给出,message_delta 只有 output_tokens。修复前 inUsage 恒为 0(↑0)。
func TestProxyPassthroughAnthropic_MessageStartInputTokens(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"hal-1.0","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":544,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		// 标准 Anthropic:message_delta 的 usage 只有 output_tokens,无 input_tokens
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":208}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	h := &APICompatHandler{}
	w := httptest.NewRecorder()
	resp := makeAnthropicSSEResponse(t, upstreamSSE)

	inUsage, outUsage, cachedUsage := h.proxyPassthroughAnthropic(w, resp, true, nil, nil)

	if inUsage != 544 {
		t.Errorf("inUsage: want 544 (from message_start), got %d", inUsage)
	}
	if outUsage != 208 {
		t.Errorf("outUsage: want 208 (from message_delta), got %d", outUsage)
	}
	if cachedUsage != 0 {
		t.Errorf("cachedUsage: want 0 (no cache fields), got %d", cachedUsage)
	}
}

// TestProxyPassthroughAnthropic_MessageDeltaInputTokensNotZeroed 锁定:当 message_delta
// 携带非零 input_tokens(部分镜像在 delta 内放累计 usage)时,仍能正确覆盖。
func TestProxyPassthroughAnthropic_MessageDeltaInputTokensNotZeroed(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude","usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":15,"cache_read_input_tokens":40000}}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	h := &APICompatHandler{}
	w := httptest.NewRecorder()
	resp := makeAnthropicSSEResponse(t, upstreamSSE)

	inUsage, outUsage, cachedUsage := h.proxyPassthroughAnthropic(w, resp, true, nil, nil)

	if inUsage != 42 {
		t.Errorf("inUsage: want 42 (message_delta overrides message_start), got %d", inUsage)
	}
	if outUsage != 15 {
		t.Errorf("outUsage: want 15, got %d", outUsage)
	}
	if cachedUsage != 40000 {
		t.Errorf("cachedUsage: want 40000, got %d", cachedUsage)
	}
}

// TestProxyPassthroughAnthropic_FallbackEstimateWhenAllZero 锁定:上游 message_start
// 的 input_tokens 为 0 且 message_delta 也未带 input_tokens 时,流末按入站请求体
// 估算兜底,inUsage 必须非零(与 non-stream 分支同口径)。
func TestProxyPassthroughAnthropic_FallbackEstimateWhenAllZero(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"hal-1.0","usage":{"input_tokens":0,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":65}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	h := &APICompatHandler{}
	w := httptest.NewRecorder()
	resp := makeAnthropicSSEResponse(t, upstreamSSE)

	// 入站请求体含较长文本,估算必然 >1
	inboundBody := []byte(`{"model":"hal-1.0","messages":[{"role":"user","content":"请用中文详细回答这个问题并给出完整推理过程"}],"max_tokens":256}`)

	inUsage, outUsage, _ := h.proxyPassthroughAnthropic(w, resp, true, stats.NewFirstByteRecorder(time.Now()), inboundBody)

	if inUsage <= 0 {
		t.Errorf("inUsage: want >0 (fallback estimate), got %d", inUsage)
	}
	if outUsage != 65 {
		t.Errorf("outUsage: want 65, got %d", outUsage)
	}
}

// TestProxyPassthroughAnthropic_TopLevelUsageFallback 锁定:部分镜像把 usage 放在
// 事件顶层(delta 平级)且含 input_tokens 时,也能正确捕获(不被 delta 内空 usage 清零)。
func TestProxyPassthroughAnthropic_TopLevelUsageFallback(t *testing.T) {
	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude","usage":{"input_tokens":0,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		// usage 在事件顶层(delta 内无 usage),input_tokens 非零
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":42,"output_tokens":15}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	h := &APICompatHandler{}
	w := httptest.NewRecorder()
	resp := makeAnthropicSSEResponse(t, upstreamSSE)

	inUsage, outUsage, _ := h.proxyPassthroughAnthropic(w, resp, true, nil, nil)

	if inUsage != 42 {
		t.Errorf("inUsage: want 42 (top-level usage fallback), got %d", inUsage)
	}
	if outUsage != 15 {
		t.Errorf("outUsage: want 15, got %d", outUsage)
	}
}
