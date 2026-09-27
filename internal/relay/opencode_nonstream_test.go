package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

// opencode_nonstream_test.go: 免费模型「非流式请求」回归用例。
//
// 背景(2026-09-27 实测):
//   OpenCode Zen 对免费模型(big-pickle / *-free 系列)仅接受 stream=true。
//   单变量对照实验证明唯一决定因素是 stream 字段:
//     stream=true  → 200
//     stream=false → 403 {"type":"error","error":{"type":"FreeTierError",...}}
//
// 修复策略:
//   免费模型的非流式请求由中继强制向上游发流式, 本地聚合成完整 OpenAI Chat
//   响应后交回既有非流式回写路径(见 opencode_sse_aggregate.go)。
//
// 本组用例锁定:
//  1. isOpenCodeFreeModel 判定语义(含新增免费模型与误判防护);
//  2. aggregateOpenAIChatSSE 聚合正确性(文本/工具调用/错误帧/空流/畸形帧);
//  3. 端到端: 客户端非流式 + 免费模型 → 上游收到 stream=true, 客户端收到非流式 JSON;
//  4. 回归保护: 付费模型的非流式行为不被改动。

// TestIsOpenCodeFreeModel 锁定免费模型判定语义。
func TestIsOpenCodeFreeModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		// 免费: -free 后缀(含晚于代码内清单新增的模型, 验证后缀判定而非硬编码)
		{"mimo-v2.5-free", true},
		{"mimo-v2.6-flash-free", true},
		{"ling-3.0-flash-fin-free", true},
		{"nemotron-3-ultra-free", true},
		{"nemotron-3.5-lightning-free", true},
		{"longcat-2.5-preview-free", true},
		{"space-bunny-free", true},
		{"jev-1.13-free", true},
		{"muse-spark-1.2-contributor-free", true},
		{"muse-spark-1.3-contributor-free", true},
		// 免费: 无后缀特例
		{"big-pickle", true},
		// 带 opencode/ 前缀(路由表 clientModel 形态)
		{"opencode/mimo-v2.5-free", true},
		{"opencode/big-pickle", true},
		// 带变体后缀
		{"opencode/big-pickle[max]", true},
		// 大小写与空白容错
		{"  BIG-PICKLE  ", true},
		{"MIMO-V2.5-FREE", true},
		// 付费: 不应误判
		{"claude-sonnet-4-6", false},
		{"kimi-k3", false},
		{"gpt-5.4-nano", false},
		{"glm-5.3", false},
		{"opencode/claude-opus-5", false},
		{"deepseek-v4-pro", false},
		// 边界
		{"", false},
		{"free", false},
		{"freed", false},
		{"big-pickle-pro", false},
	}
	for _, c := range cases {
		if got := isOpenCodeFreeModel(c.model); got != c.want {
			t.Errorf("isOpenCodeFreeModel(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

// TestAggregateOpenAIChatSSE_Basic 验证基础文本流聚合。
func TestAggregateOpenAIChatSSE_Basic(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"big-pickle\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":2,\"total_tokens\":13}}\n\n" +
		"data: [DONE]\n\n"

	resp, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "c1" {
		t.Errorf("id = %q, want c1", resp.ID)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if resp.Model != "big-pickle" {
		t.Errorf("model = %q, want big-pickle", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "Hello world")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 13 {
		t.Errorf("usage = %+v, want 11/2/13", resp.Usage)
	}
}

// TestAggregateOpenAIChatSSE_ToolCalls 验证工具调用分片聚合(按 index 归并, arguments 顺序拼接)。
func TestAggregateOpenAIChatSSE_ToolCalls(t *testing.T) {
	sse := "data: {\"id\":\"c2\",\"model\":\"big-pickle\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c2\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"command\\\":\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c2\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"ls\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c2\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	resp, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := resp.Choices[0].Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "bash" {
		t.Errorf("tool call = %+v, want id=call_1 name=bash", tc)
	}
	if tc.Function.Arguments != `{"command":"ls"}` {
		t.Errorf("arguments = %q, want %q", tc.Function.Arguments, `{"command":"ls"}`)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

// TestAggregateOpenAIChatSSE_MultipleToolCalls 验证多工具并发分片按 index 正确归并。
func TestAggregateOpenAIChatSSE_MultipleToolCalls(t *testing.T) {
	sse := "data: {\"id\":\"c4\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"bash\",\"arguments\":\"A1\"}},{\"index\":1,\"id\":\"call_b\",\"function\":{\"name\":\"read\",\"arguments\":\"B1\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c4\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"B2\"}},{\"index\":0,\"function\":{\"arguments\":\"A2\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c4\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	resp, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tcs := resp.Choices[0].Message.ToolCalls
	if len(tcs) != 2 {
		t.Fatalf("tool_calls len = %d, want 2", len(tcs))
	}
	if tcs[0].ID != "call_a" || tcs[0].Function.Name != "bash" || tcs[0].Function.Arguments != "A1A2" {
		t.Errorf("tool[0] = %+v, want call_a/bash/A1A2", tcs[0])
	}
	if tcs[1].ID != "call_b" || tcs[1].Function.Name != "read" || tcs[1].Function.Arguments != "B1B2" {
		t.Errorf("tool[1] = %+v, want call_b/read/B1B2", tcs[1])
	}
}

// TestAggregateOpenAIChatSSE_UpstreamError 验证流中错误帧被识别为 openCodeUpstreamError。
func TestAggregateOpenAIChatSSE_UpstreamError(t *testing.T) {
	sse := "data: {\"type\":\"error\",\"error\":{\"type\":\"FreeTierError\",\"message\":\"free tier restricted\"}}\n\n"

	_, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	uerr, ok := err.(*openCodeUpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *openCodeUpstreamError", err)
	}
	if uerr.Type != "FreeTierError" {
		t.Errorf("type = %q, want FreeTierError", uerr.Type)
	}
}

// TestAggregateOpenAIChatSSE_EmptyStream 验证空流报错而非返回空响应。
func TestAggregateOpenAIChatSSE_EmptyStream(t *testing.T) {
	_, err := aggregateOpenAIChatSSE(strings.NewReader(""), "fallback")
	if err == nil {
		t.Fatal("expected error for empty stream, got nil")
	}
	if _, ok := err.(*openCodeUpstreamError); !ok {
		t.Errorf("error type = %T, want *openCodeUpstreamError", err)
	}
}

// TestAggregateOpenAIChatSSE_MalformedFrameSkipped 验证畸形帧被跳过而不中断整体聚合。
func TestAggregateOpenAIChatSSE_MalformedFrameSkipped(t *testing.T) {
	sse := "data: {\"id\":\"c3\",\"model\":\"big-pickle\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"A\"},\"finish_reason\":null}]}\n\n" +
		"data: {this is not json\n\n" +
		"data: {\"id\":\"c3\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"B\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	resp, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "AB" {
		t.Errorf("content = %q, want AB (malformed frame should be skipped)", got)
	}
}

// TestAggregateOpenAIChatSSE_ReasoningContent 验证思考文本被保留(reasoning_content)。
func TestAggregateOpenAIChatSSE_ReasoningContent(t *testing.T) {
	sse := "data: {\"id\":\"c5\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking...\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	resp, err := aggregateOpenAIChatSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := resp.Choices[0].Message
	if msg.ReasoningContent != "thinking..." {
		t.Errorf("reasoning_content = %q, want thinking...", msg.ReasoningContent)
	}
	if msg.Content != "answer" {
		t.Errorf("content = %q, want answer", msg.Content)
	}
}

// TestOpenCodeFreeModel_NonStreamForcedUpstreamStream 端到端:
// 客户端发非流式 + 免费模型时, 上游必须收到 stream=true, 且客户端收到非流式 JSON。
func TestOpenCodeFreeModel_NonStreamForcedUpstreamStream(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_nonstream_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	var upstreamStream bool
	var upstreamSawStreamOptions bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var probe struct {
			Stream        bool                   `json:"stream"`
			StreamOptions map[string]interface{} `json:"stream_options"`
		}
		_ = json.Unmarshal(bodyBytes, &probe)
		upstreamStream = probe.Stream
		upstreamSawStreamOptions = probe.StreamOptions != nil

		// 上游按流式返回 SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"agg1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"big-pickle\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"aggregated\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"agg1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)
	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-key",
		Label:        "NonStreamAccount",
		DefaultModel: "big-pickle",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	// 客户端请求: 非流式 + 免费模型
	reqBody := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test-nonstream"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !upstreamStream {
		t.Error("上游应收到 stream=true (免费模型非流式需强制上游流式), 实际 false")
	}
	if !upstreamSawStreamOptions {
		t.Error("上游应收到 stream_options(include_usage), 实际缺失")
	}
	// 客户端应收到非流式 JSON, 而非 SSE
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("客户端 Content-Type = %q, want application/json", ct)
	}
	if strings.Contains(w.Body.String(), "data: ") {
		t.Errorf("客户端不应收到 SSE 帧, 实际: %s", w.Body.String())
	}
	var out OpenAIChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("客户端响应不是合法 OpenAI Chat JSON: %v (body=%s)", err, w.Body.String())
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content != "aggregated" {
		t.Errorf("聚合内容不符: %+v", out.Choices)
	}
	if out.Usage.PromptTokens != 5 || out.Usage.CompletionTokens != 3 {
		t.Errorf("usage 应透传聚合值, 实际 %+v", out.Usage)
	}
}

// TestOpenCodeFreeModel_StreamPassthroughUnchanged 回归保护:
// 免费模型 + 客户端已要求流式时, 不应走聚合分支, 应直接透传 SSE。
func TestOpenCodeFreeModel_StreamPassthroughUnchanged(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_stream_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"big-pickle\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"streamed\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"s1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)
	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-key",
		Label:        "StreamAccount",
		DefaultModel: "big-pickle",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	reqBody := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test-stream"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// 流式路径应原样透传 SSE
	if !strings.Contains(w.Body.String(), "streamed") {
		t.Errorf("流式响应应含上游内容, 实际: %s", w.Body.String())
	}
}

// TestOpenCodePaidModel_NonStreamUnchanged 验证付费模型的非流式行为未被改动(回归保护)。
func TestOpenCodePaidModel_NonStreamUnchanged(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "opencode_paid_nonstream_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	var upstreamStream bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(bodyBytes, &probe)
		upstreamStream = probe.Stream

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "paid1",
			"object":  "chat.completion",
			"model":   "claude-sonnet-4-6",
			"choices": []map[string]interface{}{{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "paid-ok"}, "finish_reason": "stop"}},
			"usage":   map[string]interface{}{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
		})
	}))
	defer mockServer.Close()

	mgr := account.NewManager()
	mgr.Init(tempDir)
	_, _ = mgr.AddOpenCodeAccount(account.OpenCodeAccountInput{
		BaseURL:      mockServer.URL,
		AccessToken:  "sk-test-key",
		Label:        "PaidAccount",
		DefaultModel: "claude-sonnet-4-6",
	})

	h := &APICompatHandler{
		accountMgr: mgr,
		client:     mockServer.Client(),
	}

	reqBody := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/opencode/v1/chat/completions", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	h.handleOpenCode(w, req, &RelaySession{Token: "test-paid"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if upstreamStream {
		t.Error("付费模型非流式请求不应被强制改流式, 实际 stream=true")
	}
	var out OpenAIChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content != "paid-ok" {
		t.Errorf("内容不符: %+v", out.Choices)
	}
}
