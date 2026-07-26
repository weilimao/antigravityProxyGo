package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// nvidia_responses_test.go 覆盖 NVIDIA 链路对 Responses API("/v1/responses")的兼容：
// 入站解析、出站非流式回译、出站流式 SSE 事件序列、工具调用、上游错误透传。

// ===== 入站：Responses → OpenAIChatRequest =====

func TestResponsesToOpenAIChat_BasicInput(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5",
		"instructions": "You are a coding agent.",
		"input": [
			{"type":"message","role":"user","content":"hello"},
			{"type":"message","role":"assistant","content":"hi there"},
			{"type":"message","role":"user","content":"write a function"}
		],
		"stream": false
	}`)
	out, err := ResponsesToOpenAIChat(body, "moonshotai/kimi-k2.5")
	if err != nil {
		t.Fatalf("transform failed: %v", err)
	}
	if out.Model != "moonshotai/kimi-k2.5" {
		t.Errorf("model not mapped: %s", out.Model)
	}
	if len(out.Messages) != 4 {
		t.Fatalf("expected 4 messages (system+3), got %d: %+v", len(out.Messages), out.Messages)
	}
	if out.Messages[0].Role != "system" || out.Messages[0].Content != "You are a coding agent." {
		t.Errorf("instructions not converted to system: %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "user" || out.Messages[1].Content != "hello" {
		t.Errorf("first user message wrong: %+v", out.Messages[1])
	}
	if out.Stream {
		t.Errorf("stream must be false")
	}
	if out.StreamOptions != nil {
		t.Errorf("non-stream should not inject stream_options")
	}
}

func TestResponsesToOpenAIChat_ToolCalls(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5",
		"input": [
			{"type":"message","role":"user","content":"list files"},
			{"type":"function_call","call_id":"call_1","name":"run_bash","arguments":"{\"cmd\":\"ls\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"file1\nfile2"},
			{"type":"message","role":"user","content":"thanks"}
		],
		"stream": true
	}`)
	out, err := ResponsesToOpenAIChat(body, "z-ai/glm-5.2")
	if err != nil {
		t.Fatalf("transform failed: %v", err)
	}
	// 期望顺序: user(list files) → assistant(tool_calls) → tool(call_1) → user(thanks)
	var roles []string
	for _, m := range out.Messages {
		roles = append(roles, m.Role)
	}
	wantRoles := []string{"user", "assistant", "tool", "user"}
	if len(roles) != len(wantRoles) {
		t.Fatalf("expected %d messages, got %d (%v)", len(wantRoles), len(roles), roles)
	}
	for i, r := range wantRoles {
		if roles[i] != r {
			t.Errorf("messages[%d] role = %s, want %s (all=%v)", i, roles[i], r, roles)
		}
	}
	// assistant 消息应携带 tool_calls
	var assistantMsg *ChatMessage
	for i := range out.Messages {
		if out.Messages[i].Role == "assistant" {
			assistantMsg = &out.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message produced")
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].Function.Name != "run_bash" {
		t.Errorf("tool name wrong: %s", assistantMsg.ToolCalls[0].Function.Name)
	}
	// tool 消息应携带 tool_call_id
	var toolMsg *ChatMessage
	for i := range out.Messages {
		if out.Messages[i].Role == "tool" {
			toolMsg = &out.Messages[i]
			break
		}
	}
	if toolMsg == nil || toolMsg.ToolCallID != "call_1" {
		t.Fatalf("tool message missing tool_call_id: %+v", toolMsg)
	}
	if toolMsg.Content != "file1\nfile2" {
		t.Errorf("tool output content wrong: %q", toolMsg.Content)
	}
	// 流式必须注入 stream_options.include_usage
	if !out.Stream {
		t.Errorf("stream must be true")
	}
	if out.StreamOptions == nil || !out.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage not injected for streaming responses")
	}
}

func TestResponsesToOpenAIChat_ToolsDefinition(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5",
		"input": [{"type":"message","role":"user","content":"hi"}],
		"tools": [
			{"type":"function","name":"get_weather","description":"get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}
		]
	}`)
	out, err := ResponsesToOpenAIChat(body, "meta/llama-3.3-70b-instruct")
	if err != nil {
		t.Fatalf("transform failed: %v", err)
	}
	if len(out.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out.Tools))
	}
	if out.Tools[0].Type != "function" {
		t.Errorf("tool type wrong: %s", out.Tools[0].Type)
	}
	if out.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tool name wrong: %s", out.Tools[0].Function.Name)
	}
	if out.Tools[0].Function.Parameters == nil {
		t.Errorf("tool parameters missing")
	}
}

// ===== 出站：OpenAIChat → Responses 非流式 =====

func TestOpenAIChatToResponses_TextOnly(t *testing.T) {
	resp := &OpenAIChatResponse{
		ID:   "chatcmpl-1",
		Model: "moonshotai/kimi-k2.5",
		Choices: []OpenAIChatChoice{{
			Index: 0, Message: ChatMessage{Role: "assistant", Content: "Hello from NVIDIA"}, FinishReason: "stop",
		}},
		Usage: OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
	}
	rr := OpenAIChatToResponses(resp, "moonshotai/kimi-k2.5")
	if rr.Object != "response" {
		t.Errorf("object wrong: %s", rr.Object)
	}
	if rr.Status != "completed" {
		t.Errorf("status wrong: %s", rr.Status)
	}
	if rr.Usage.InputTokens != 10 || rr.Usage.OutputTokens != 3 || rr.Usage.TotalTokens != 13 {
		t.Errorf("usage wrong: %+v", rr.Usage)
	}
	if len(rr.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(rr.Output))
	}
	if rr.Output[0].Type != "message" {
		t.Errorf("output[0] type wrong: %s", rr.Output[0].Type)
	}
	if len(rr.Output[0].Content) != 1 || rr.Output[0].Content[0].Text != "Hello from NVIDIA" {
		t.Errorf("output text wrong: %+v", rr.Output[0].Content)
	}
}

func TestOpenAIChatToResponses_ToolCalls(t *testing.T) {
	resp := &OpenAIChatResponse{
		ID:   "chatcmpl-2",
		Model: "z-ai/glm-5.2",
		Choices: []OpenAIChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ChatToolCall{{
					ID:   "call_abc",
					Type: "function",
					Function: ChatToolCallFunction{Name: "run_bash", Arguments: `{"cmd":"ls"}`},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: OpenAIChatUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
	}
	rr := OpenAIChatToResponses(resp, "z-ai/glm-5.2")
	// 文本为空但有 tool_calls，文本 message 条目不再产生（见 openAIChoiceToResponsesItems 判定）
	var msgItems, fcItems int
	for _, it := range rr.Output {
		switch it.Type {
		case "message":
			msgItems++
		case "function_call":
			fcItems++
		}
	}
	if msgItems != 0 {
		t.Errorf("expected 0 message item when tool_calls present (no fake empty message), got %d", msgItems)
	}
	if fcItems != 1 {
		t.Fatalf("expected 1 function_call item, got %d", fcItems)
	}
	var fc *ResponsesOutputItem
	for i := range rr.Output {
		if rr.Output[i].Type == "function_call" {
			fc = &rr.Output[i]
			break
		}
	}
	if fc.CallID != "call_abc" || fc.Name != "run_bash" || fc.Arguments != `{"cmd":"ls"}` {
		t.Errorf("function_call fields wrong: %+v", fc)
	}
}

// ===== 出站：OpenAIChat SSE → Responses SSE 流式 =====

func TestOpenAIChatSSEToResponsesSSE_TextStream(t *testing.T) {
	// 构造上游 OpenAI Chat SSE：role 声明 + 两个文本增量 + finish + 末帧 usage
	var sse strings.Builder
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{"role": "assistant"}, "finish_reason": nil}},
	}))
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{"content": "Hel"}, "finish_reason": nil}},
	}))
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{"content": "lo"}, "finish_reason": nil}},
	}))
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}},
		"usage":  map[string]int{"prompt_tokens": 7, "completion_tokens": 2, "total_tokens": 9},
	}))
	sse.WriteString("data: [DONE]\n\n")

	buf := &flushBuffer{}
	fw := newFlushWriter("test_resp", bufio.NewWriter(buf))
	in, out := OpenAIChatSSEToResponsesSSE(context.Background(), strings.NewReader(sse.String()), nil, fw, "moonshotai/kimi-k2.5")
	fw.flush()

	if in != 7 || out != 2 {
		t.Errorf("usage wrong: in=%d out=%d", in, out)
	}

	events := parseSSEEvents(buf.String())
	// 必须的事件骨架
	requireEvent(t, events, "response.created")
	requireEvent(t, events, "response.in_progress")
	requireEvent(t, events, "response.output_item.added")
	requireEvent(t, events, "response.content_part.added")
	requireEvent(t, events, "response.output_text.delta")
	requireEvent(t, events, "response.output_text.done")
	requireEvent(t, events, "response.content_part.done")
	requireEvent(t, events, "response.output_item.done")
	requireEvent(t, events, "response.completed")

	// 拼回文本增量应为 "Hello"
	var deltaText strings.Builder
	for _, ev := range events {
		if ev.event == "response.output_text.delta" {
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(ev.data), &m); err == nil {
				if s, ok := m["delta"].(string); ok {
					deltaText.WriteString(s)
				}
			}
		}
	}
	if deltaText.String() != "Hello" {
		t.Errorf("delta text wrong: %q", deltaText.String())
	}

	// completed 事件应带 usage
	var completed map[string]interface{}
	for _, ev := range events {
		if ev.event == "response.completed" {
			_ = json.Unmarshal([]byte(ev.data), &completed)
			break
		}
	}
	respObj, _ := completed["response"].(map[string]interface{})
	usage, _ := respObj["usage"].(map[string]interface{})
	if int(usage["input_tokens"].(float64)) != 7 || int(usage["output_tokens"].(float64)) != 2 {
		t.Errorf("completed usage wrong: %+v", usage)
	}
}

func TestOpenAIChatSSEToResponsesSSE_ToolCallStream(t *testing.T) {
	// 上游流式 tool_calls：首帧带 id+name+arguments 增量，次帧补 arguments 增量，finish=tool_calls
	var sse strings.Builder
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"index": 0, "id": "call_x", "type": "function",
					"function": map[string]string{"name": "run_bash", "arguments": "{\"cmd\":"},
				}},
			},
			"finish_reason": nil,
		}},
	}))
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"index": 0,
					"function": map[string]string{"arguments": "\"ls\"}"},
				}},
			},
			"finish_reason": nil,
		}},
	}))
	writeSSEData(&sse, mustJSONString(map[string]interface{}{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk",
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{}, "finish_reason": "tool_calls"}},
		"usage":  map[string]int{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
	}))
	sse.WriteString("data: [DONE]\n\n")

	buf := &flushBuffer{}
	fw := newFlushWriter("test_resp", bufio.NewWriter(buf))
	in, out := OpenAIChatSSEToResponsesSSE(context.Background(), strings.NewReader(sse.String()), nil, fw, "z-ai/glm-5.2")
	fw.flush()

	if in != 3 || out != 1 {
		t.Errorf("usage wrong: in=%d out=%d", in, out)
	}

	events := parseSSEEvents(buf.String())
	requireEvent(t, events, "response.created")
	requireEvent(t, events, "response.output_item.added")
	requireEvent(t, events, "response.function_call_arguments.delta")
	requireEvent(t, events, "response.function_call_arguments.done")
	requireEvent(t, events, "response.output_item.done")
	requireEvent(t, events, "response.completed")

	// output_item.added 的 item.type 应为 function_call 且带 call_id/name
	var itemAdded map[string]interface{}
	for _, ev := range events {
		if ev.event == "response.output_item.added" {
			_ = json.Unmarshal([]byte(ev.data), &itemAdded)
			break
		}
	}
	item, _ := itemAdded["item"].(map[string]interface{})
	if item["type"] != "function_call" {
		t.Errorf("item type wrong: %v", item["type"])
	}
	if item["call_id"] != "call_x" || item["name"] != "run_bash" {
		t.Errorf("function_call meta wrong: %+v", item)
	}

	// 拼回 arguments 增量应为完整 JSON
	var args strings.Builder
	for _, ev := range events {
		if ev.event == "response.function_call_arguments.delta" {
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(ev.data), &m); err == nil {
				if s, ok := m["delta"].(string); ok {
					args.WriteString(s)
				}
			}
		}
	}
	if args.String() != `{"cmd":"ls"}` {
		t.Errorf("arguments reassembled wrong: %q", args.String())
	}
}

// ===== 端到端：handleNvidia + /nvidia/v1/responses =====

func TestHandleNvidia_ResponsesNonStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req OpenAIChatRequest
		_ = json.Unmarshal(body, &req)
		// model 经号池档位映射为 DefaultModel
		if req.Model != "moonshotai/kimi-k2.5" {
			t.Errorf("model not mapped: %s", req.Model)
		}
		// instructions 应已转成 system message
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Errorf("instructions not converted to system message: %+v", req.Messages)
		}
		// 非流式不应注入 stream_options
		if req.StreamOptions != nil {
			t.Errorf("non-stream should not inject stream_options")
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-r1", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "Hello"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-r1", "nvidia-r1", "test-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, ut := newNvidiaTestHandler(t, []*account.Account{acc})

	reqBody := []byte(`{
		"model": "gpt-5",
		"instructions": "You are helpful.",
		"input": [{"type":"message","role":"user","content":"hi"}],
		"stream": false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/responses", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-resp", UserKey: "k-resp"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp ResponsesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid responses json: %v body=%s", err, rr.Body.String())
	}
	if resp.Object != "response" || resp.Status != "completed" {
		t.Errorf("response meta wrong: %+v", resp)
	}
	if len(resp.Output) == 0 || resp.Output[0].Type != "message" {
		t.Errorf("output wrong: %+v", resp.Output)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 2 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}

	// 号池账号维度统计应已落桶（复用 nvidia_test.go 的 findUsageAccountByEmail 辅助）
	payload := ut.GetPayload()
	accBucket, ok := findUsageAccountByEmail(payload, "nvidia-r1")
	if !ok {
		t.Fatalf("usage not recorded for pool account nvidia-r1")
	}
	if accBucket.Models == nil {
		t.Fatalf("pool account bucket has no models")
	}
	mu, exists := accBucket.Models["moonshotai/kimi-k2.5"]
	if !exists {
		// 模型键可能带/不带前缀，遍历兜底查找
		for k := range accBucket.Models {
			if strings.Contains(k, "kimi-k2.5") {
				mu = accBucket.Models[k]
				exists = true
				break
			}
		}
	}
	if !exists {
		t.Errorf("model moonshotai/kimi-k2.5 not recorded in pool bucket: %v", mapKeys(accBucket.Models))
	} else if mu.InputTokens != 8 || mu.OutputTokens != 2 {
		t.Errorf("pool bucket tokens wrong: in=%d out=%d", mu.InputTokens, mu.OutputTokens)
	}
}

func mapKeys(m map[string]*stats.ModelUsage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestHandleNvidia_ResponsesStream(t *testing.T) {
	// 上游返回流式 SSE
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		writeSSEToWriter(w, flusher, mustJSONString(map[string]interface{}{
			"id": "chatcmpl-rs1", "object": "chat.completion.chunk",
			"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{"content": "Hi"}, "finish_reason": nil}},
		}))
		writeSSEToWriter(w, flusher, mustJSONString(map[string]interface{}{
			"id": "chatcmpl-rs1", "object": "chat.completion.chunk",
			"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}},
			"usage":  map[string]int{"prompt_tokens": 4, "completion_tokens": 1, "total_tokens": 5},
		}))
		fmtFprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-rs", "nvidia-rs", "test-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	reqBody := []byte(`{
		"model": "gpt-5",
		"input": [{"type":"message","role":"user","content":"hi"}],
		"stream": true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/responses", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-rstream", UserKey: "k-rstream"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	events := parseSSEEvents(rr.Body.String())
	requireEvent(t, events, "response.created")
	requireEvent(t, events, "response.in_progress")
	requireEvent(t, events, "response.output_text.delta")
	requireEvent(t, events, "response.completed")
	if rr.Body.String() == "" {
		t.Fatalf("no SSE body produced")
	}
}

func TestHandleNvidia_ResponsesUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"model overloaded"}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-err", "nvidia-err", "test-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	reqBody := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/responses", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-err", UserKey: "k-err"})

	// 单账号 5xx 后换号重试用尽，最终回写上游错误状态码 503
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 upstream error passthrough, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// ===== 测试辅助 =====

// flushBuffer 实现 io.Writer，充当 bufio.Writer 的底层 buffer，便于断言流式输出。
type flushBuffer struct{ buf strings.Builder }

func (b *flushBuffer) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *flushBuffer) String() string              { return b.buf.String() }

type sseEvent struct {
	event string
	data  string
}

// parseSSEEvents 从 SSE 文本中解析出 {event, data} 对。
func parseSSEEvents(s string) []sseEvent {
	var events []sseEvent
	var cur sseEvent
	hasEvent := false
	flush := func() {
		if hasEvent {
			events = append(events, cur)
			cur = sseEvent{}
			hasEvent = false
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "event: ") {
			cur.event = strings.TrimPrefix(line, "event: ")
			if cur.data != "" {
				hasEvent = true
			}
		} else if strings.HasPrefix(line, "data: ") {
			cur.data = strings.TrimPrefix(line, "data: ")
			if cur.event != "" {
				hasEvent = true
			}
		} else if line == "" {
			flush()
		}
	}
	flush()
	return events
}

func requireEvent(t *testing.T, events []sseEvent, name string) {
	t.Helper()
	for _, ev := range events {
		if ev.event == name {
			return
		}
	}
	t.Errorf("expected SSE event %q not found in %d events: %v", name, len(events), eventNames(events))
}

func eventNames(events []sseEvent) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.event)
	}
	return names
}

func mustJSONString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeSSEData(b *strings.Builder, jsonData string) {
	b.WriteString("data: ")
	b.WriteString(jsonData)
	b.WriteString("\n\n")
}

func writeSSEToWriter(w http.ResponseWriter, flusher http.Flusher, jsonData string) {
	_, _ = w.Write([]byte("data: " + jsonData + "\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
}

func fmtFprintf(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(s))
}
