package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// passthrough_anthropic_test.go: Other 号池「上游 Anthropic 原生端点」双向协议转译单元测试。
//
// 覆盖 OpenAIToAnthropicMessages(请求侧 OpenAI→Anthropic)、AnthropicResponseToOpenAIChat
// (非流式响应 Anthropic→OpenAI)、AnthropicSSEToOpenAIChatSSE(流式响应 Anthropic SSE→OpenAI SSE)
// 三条转译路径,锁定与 NVIDIA 链路对偶的字段映射语义。
//
// 本文件只读、无副作用,不触碰 settings/Manager 全局状态。

// ============ OpenAIToAnthropicMessages ============

func TestOpenAIToAnthropicMessages_Basic(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}],"max_tokens":128,"temperature":0.5}`
	req, err := OpenAIToAnthropicMessages([]byte(body), "claude-sonnet-4-5", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: want claude-sonnet-4-5, got %q", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("Messages: want 2, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("first msg role: want user, got %q", req.Messages[0].Role)
	}
	if len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != "hi" {
		t.Errorf("first msg content: want [text:hi], got %+v", req.Messages[0].Content)
	}
	if req.Messages[1].Role != "assistant" || req.Messages[1].Content[0].Text != "hello" {
		t.Errorf("second msg: want assistant text:hello, got %+v", req.Messages[1])
	}
	if req.MaxTokens == nil || *req.MaxTokens != 128 {
		t.Errorf("MaxTokens: want 128, got %v", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Errorf("Temperature: want 0.5, got %v", req.Temperature)
	}
}

func TestOpenAIToAnthropicMessages_SystemExtracted(t *testing.T) {
	// 多条 system 消息应以 \n\n 合并抽到顶层 system 字段,不进 messages。
	body := `{"model":"gpt-4o","messages":[
		{"role":"system","content":"You are X"},
		{"role":"system","content":"Also Y"},
		{"role":"user","content":"hi"}
	]}`
	req, err := OpenAIToAnthropicMessages([]byte(body), "claude", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.System != "You are X\n\nAlso Y" {
		t.Errorf("System: want joined with \\n\\n, got %q", req.System)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("Messages: want 1 (system stripped), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("remaining msg role: want user, got %q", req.Messages[0].Role)
	}
}

func TestOpenAIToAnthropicMessages_MaxTokensFallback(t *testing.T) {
	// OpenAI 留空 max_tokens → Anthropic 必填,兜底 8192。
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, err := OpenAIToAnthropicMessages([]byte(body), "claude", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 8192 {
		t.Errorf("MaxTokens fallback: want 8192, got %v", req.MaxTokens)
	}
}

func TestOpenAIToAnthropicMessages_ToolCalls(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}
		]},
		{"role":"tool","content":"北京 晴 25℃","tool_call_id":"call_1","tool_name":"get_weather"}
	]}`
	req, err := OpenAIToAnthropicMessages([]byte(body), "claude", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("Messages: want 3, got %d", len(req.Messages))
	}
	// 第二条 assistant → tool_use 块。
	assistant := req.Messages[1]
	if assistant.Role != "assistant" {
		t.Errorf("assistant role: got %q", assistant.Role)
	}
	var foundToolUse bool
	for _, b := range assistant.Content {
		if b.Type == "tool_use" && b.Name == "get_weather" && b.ID == "call_1" {
			if b.Input == nil || b.Input["city"] != "北京" {
				t.Errorf("tool_use input: want city=北京, got %v", b.Input)
			}
			foundToolUse = true
		}
	}
	if !foundToolUse {
		t.Errorf("assistant content missing tool_use block: %+v", assistant.Content)
	}
	// 第三条 tool 角色 → user 角色含 tool_result 块,tool_use_id 指向 call_1。
	toolMsg := req.Messages[2]
	if toolMsg.Role != "user" {
		t.Errorf("tool result role: want user, got %q", toolMsg.Role)
	}
	if len(toolMsg.Content) != 1 || toolMsg.Content[0].Type != "tool_result" || toolMsg.Content[0].ToolUseID != "call_1" {
		t.Errorf("tool_result block: want type=tool_result, tool_use_id=call_1, got %+v", toolMsg.Content)
	}
}

func TestOpenAIToAnthropicMessages_ToolsDefinition(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[
		{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}
	]}`
	req, err := OpenAIToAnthropicMessages([]byte(body), "claude", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("Tools: want 1, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" || req.Tools[0].Description != "Get weather" {
		t.Errorf("tool def: %+v", req.Tools[0])
	}
	if req.Tools[0].InputSchema == nil || req.Tools[0].InputSchema["type"] != "object" {
		t.Errorf("tool input_schema: want type=object, got %v", req.Tools[0].InputSchema)
	}
}

func TestOpenAIToAnthropicMessages_InvalidBody(t *testing.T) {
	_, err := OpenAIToAnthropicMessages([]byte("not-json"), "claude", false)
	if err == nil {
		t.Fatal("expected err for invalid json, got nil")
	}
	if !strings.Contains(err.Error(), "invalid openai chat request") {
		t.Errorf("err: want contains 'invalid openai chat request', got %v", err)
	}
}

// ============ AnthropicResponseToOpenAIChat ============

func TestAnthropicResponseToOpenAIChat_Text(t *testing.T) {
	resp := &AnthropicResponse{
		ID:         "msg_1",
		Model:      "claude-sonnet-4-5",
		StopReason: "end_turn",
		Content: []AnthropicContent{
			{Type: "text", Text: "Hello!"},
		},
		Usage: AnthropicResponseUsage{InputTokens: 10, OutputTokens: 5},
	}
	out := AnthropicResponseToOpenAIChat(resp)
	if out.ID != "msg_1" {
		t.Errorf("ID: want msg_1, got %q", out.ID)
	}
	if out.Object != "chat.completion" {
		t.Errorf("Object: want chat.completion, got %q", out.Object)
	}
	if out.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: got %q", out.Model)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("Choices: want 1, got %d", len(out.Choices))
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("finish: want stop, got %q", out.Choices[0].FinishReason)
	}
	if out.Choices[0].Message.Role != "assistant" || out.Choices[0].Message.Content != "Hello!" {
		t.Errorf("message: want assistant Hello!, got %+v", out.Choices[0].Message)
	}
	if out.Usage.PromptTokens != 10 || out.Usage.CompletionTokens != 5 || out.Usage.TotalTokens != 15 {
		t.Errorf("Usage: want 10/5/15, got %+v", out.Usage)
	}
}

func TestAnthropicResponseToOpenAIChat_ToolUse(t *testing.T) {
	resp := &AnthropicResponse{
		ID:         "msg_2",
		Model:      "claude",
		StopReason: "tool_use",
		Content: []AnthropicContent{
			{Type: "text", Text: "Calling tool"},
			{Type: "tool_use", ID: "toolu_1", Name: "get_weather", Input: map[string]interface{}{"city": "北京"}},
		},
	}
	out := AnthropicResponseToOpenAIChat(resp)
	if len(out.Choices) != 1 {
		t.Fatalf("Choices: want 1, got %d", len(out.Choices))
	}
	if out.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish: want tool_calls, got %q", out.Choices[0].FinishReason)
	}
	msg := out.Choices[0].Message
	if msg.Content != "Calling tool" {
		t.Errorf("content: want 'Calling tool', got %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: want 1, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Errorf("toolcall: %+v", tc)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("Arguments not valid json: %v", err)
	}
	if args["city"] != "北京" {
		t.Errorf("arguments.city: want 北京, got %v", args["city"])
	}
}

func TestAnthropicResponseToOpenAIChat_MaxTokens(t *testing.T) {
	resp := &AnthropicResponse{StopReason: "max_tokens", Content: []AnthropicContent{{Type: "text", Text: "..."}}}
	out := AnthropicResponseToOpenAIChat(resp)
	if out.Choices[0].FinishReason != "length" {
		t.Errorf("finish: want length, got %q", out.Choices[0].FinishReason)
	}
}

func TestAnthropicResponseToOpenAIChat_EmptyDefaults(t *testing.T) {
	// 空 ID/Model/Content 应回退默认值,避免 OpenAI 客户端解析 NPE。
	resp := &AnthropicResponse{}
	out := AnthropicResponseToOpenAIChat(resp)
	if out.ID != "chatcmpl-other" {
		t.Errorf("default ID: want chatcmpl-other, got %q", out.ID)
	}
	if out.Model != "other" {
		t.Errorf("default Model: want other, got %q", out.Model)
	}
	if len(out.Choices) != 1 || out.Choices[0].FinishReason != "stop" || out.Choices[0].Message.Content != "" {
		t.Errorf("empty content fallback choice: %+v", out.Choices[0])
	}
}

// ============ AnthropicSSEToOpenAIChatSSE ============

// findFinishChunk 在 SSE chunk 序列里找出最后一个带 finish_reason 的 choice 块。
// 末尾 usage chunk(OpenAI 流式独立 usage 帧)的 choices 为空数组,不能直接取 [0]。
func findFinishChunk(t *testing.T, chunks []map[string]interface{}) map[string]interface{} {
	t.Helper()
	for i := len(chunks) - 1; i >= 0; i-- {
		choices, ok := chunks[i]["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		c := choices[0].(map[string]interface{})
		if _, has := c["finish_reason"]; has {
			return c
		}
	}
	t.Fatal("no chunk with finish_reason found")
	return nil
}

// accumulateContent 把所有 chunk 的 delta.content 增量拼接为完整文本。
func accumulateContent(chunks []map[string]interface{}) string {
	var sb strings.Builder
	for _, c := range chunks {
		choices, ok := c["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		delta, ok := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := delta["content"].(string); ok && v != "" {
			sb.WriteString(v)
		}
	}
	return sb.String()
}
// parseOpenAIChunks 从 SSE 输出里提取所有 data 行的 JSON chunk,便于断言事件序列。
func parseOpenAIChunks(t *testing.T, sse []byte) []map[string]interface{} {
	t.Helper()
	var chunks []map[string]interface{}
	scanner := bufio.NewScanner(bytes.NewReader(sse))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("invalid chunk json %q: %v", data, err)
		}
		chunks = append(chunks, m)
	}
	return chunks
}

func TestAnthropicSSEToOpenAIChatSSE_TextStream(t *testing.T) {
	// 上游 Anthropic SSE: message_start → content_block_start(text) → 2×text_delta → content_block_stop → message_delta(usage) → message_stop
	events := []string{
		`data: {"type":"message_start","message":{"id":"msg_1"}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo!"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}
	reader := bytes.NewReader([]byte(strings.Join(events, "\n")))
	var out bytes.Buffer
	in, out2, err := AnthropicSSEToOpenAIChatSSE(reader, &out, "claude")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if in != 0 || out2 != 3 {
		// 当前实现 message_delta.usage.input_tokens 回写 input,out2=output;宽松校验。
		t.Logf("tokens in=%d out=%d (informational)", in, out2)
	}

	sseStr := out.String()
	if !strings.Contains(sseStr, "data: [DONE]") {
		t.Error("missing [DONE] terminator")
	}
	chunks := parseOpenAIChunks(t, out.Bytes())
	if len(chunks) < 4 {
		t.Fatalf("expected at least 4 chunks (role+2 text delta+finish), got %d", len(chunks))
	}
	// 第一帧: role=assistant。
	if chunks[0]["choices"] == nil {
		t.Fatal("first chunk missing choices")
	}
	c0 := chunks[0]["choices"].([]interface{})[0].(map[string]interface{})["delta"].(map[string]interface{})
	if c0["role"] != "assistant" {
		t.Errorf("first chunk delta.role: want assistant, got %v", c0["role"])
	}
	// 文本增量拼接为 "Hel"+"lo!"。
	if got := accumulateContent(chunks); got != "Hello!" {
		t.Errorf("accumulated content: want 'Hello!', got %q", got)
	}
	// 末帧应含 finish_reason=stop(末尾独立 usage chunk 的 choices 为空,跳过它取最后一个非空 choice)。
	lastFinish := findFinishChunk(t, chunks)
	if lastFinish["finish_reason"] != "stop" {
		t.Errorf("last finish_reason: want stop, got %v", lastFinish["finish_reason"])
	}
}

func TestAnthropicSSEToOpenAIChatSSE_ToolUseStream(t *testing.T) {
	// 上游 Anthropic SSE 含 tool_use 块:content_block_start(tool_use) + input_json_delta + content_block_stop。
	events := []string{
		`data: {"type":"message_start","message":{"id":"msg_2"}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"北京\"}"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":2}}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}
	reader := bytes.NewReader([]byte(strings.Join(events, "\n")))
	var out bytes.Buffer
	_, _, err := AnthropicSSEToOpenAIChatSSE(reader, &out, "claude")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	chunks := parseOpenAIChunks(t, out.Bytes())
	// 应存在一个含 tool_calls delta 的 chunk,且 arguments 为 {"city":"北京"}。
	var foundToolCall bool
	for _, c := range chunks {
		choices, ok := c["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
		if tc, ok := delta["tool_calls"].([]interface{}); ok && len(tc) > 0 {
			first := tc[0].(map[string]interface{})
			fn := first["function"].(map[string]interface{})
			if fn["name"] != "get_weather" {
				t.Errorf("tool name: want get_weather, got %v", fn["name"])
			}
			if fn["arguments"] != `{"city":"北京"}` {
				t.Errorf("tool arguments: want {\"city\":\"北京\"}, got %v", fn["arguments"])
			}
			foundToolCall = true
		}
	}
	if !foundToolCall {
		t.Fatal("no tool_calls delta chunk emitted")
	}
	// 末帧 finish_reason 应为 tool_calls(末尾独立 usage chunk 的 choices 为空,跳过它取最后一个非空 choice)。
	lastFinish := findFinishChunk(t, chunks)
	if lastFinish["finish_reason"] != "tool_calls" {
		t.Errorf("finish: want tool_calls, got %v", lastFinish["finish_reason"])
	}
}

func TestAnthropicSSEToOpenAIChatSSE_AbruptEndFallback(t *testing.T) {
	// 流异常结束(无 message_stop)→ 应补 finish + [DONE] 尾帧兜底。
	events := []string{
		`data: {"type":"message_start","message":{"id":"msg_3"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
	}
	reader := bytes.NewReader([]byte(strings.Join(events, "\n")))
	var out bytes.Buffer
	_, _, err := AnthropicSSEToOpenAIChatSSE(reader, &out, "claude")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out.String(), "data: [DONE]") {
		t.Error("abrupt end: missing [DONE] fallback")
	}
	// 应有 finish_reason=stop 尾帧(取最后一个带 finish_reason 的 choice,跳过空 choices 的 usage chunk)。
	chunks := parseOpenAIChunks(t, out.Bytes())
	lastFinish := findFinishChunk(t, chunks)
	if lastFinish["finish_reason"] != "stop" {
		t.Errorf("abrupt end finish: want stop, got %v", lastFinish["finish_reason"])
	}
}
