package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// opencode_responses_test.go: Responses-only 模型(muse-spark-*)的判定与聚合用例。
//
// 背景(2026-09-27 实测):
//   muse-spark-1.2/1.3-contributor-free 打 /chat/completions 恒返回
//   400 {"type":"ModelProtocolUnsupported"}, 改用 /responses 则 200。
//   故中继对这类模型切换上游端点并透传 Responses 协议。

// TestIsOpenCodeResponsesOnlyModel 锁定 Responses-only 判定语义。
func TestIsOpenCodeResponsesOnlyModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"muse-spark-1.2-contributor-free", true},
		{"muse-spark-1.3-contributor-free", true},
		{"opencode/muse-spark-1.3-contributor-free", true},
		{"MUSE-SPARK-1.3", true},
		{"muse-spark-1.3[max]", true},
		// 非 Responses-only
		{"big-pickle", false},
		{"mimo-v2.5-free", false},
		{"jev-1.13-free", false}, // 实测两端点均不可用, 不纳入
		{"claude-sonnet-4-6", false},
		{"", false},
		{"muse-sparkle", false},
	}
	for _, c := range cases {
		if got := isOpenCodeResponsesOnlyModel(c.model); got != c.want {
			t.Errorf("isOpenCodeResponsesOnlyModel(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

// TestPatchResponsesBodyModel 验证 Responses body 的 model 替换保留全部字段。
func TestPatchResponsesBodyModel(t *testing.T) {
	body := []byte(`{"model":"old-model","input":[{"role":"user","content":"hi"}],"max_output_tokens":100,"store":false,"include":["reasoning"],"tools":[{"type":"function","name":"bash","parameters":{"type":"object"}}],"stream":true}`)

	out, ok := patchResponsesBodyModel(body, "muse-spark-1.3-contributor-free")
	if !ok {
		t.Fatal("patchResponsesBodyModel returned ok=false")
	}
	s := string(out)
	if !strings.Contains(s, `"model":"muse-spark-1.3-contributor-free"`) {
		t.Errorf("model 未被替换: %s", s)
	}
	// Responses 专有字段必须全部保留
	for _, field := range []string{`"input"`, `"max_output_tokens"`, `"store"`, `"include"`, `"tools"`, `"stream"`} {
		if !strings.Contains(s, field) {
			t.Errorf("字段 %s 丢失: %s", field, s)
		}
	}
	if strings.Contains(s, "old-model") {
		t.Errorf("旧 model 值残留: %s", s)
	}
}

// TestPatchResponsesBodyModel_InvalidJSON 验证非法 JSON 返回 ok=false。
func TestPatchResponsesBodyModel_InvalidJSON(t *testing.T) {
	if _, ok := patchResponsesBodyModel([]byte(`not json`), "m"); ok {
		t.Error("非法 JSON 应返回 ok=false")
	}
}

// TestAggregateOpenAIResponsesSSE_Text 验证 Responses SSE 文本聚合。
func TestAggregateOpenAIResponsesSSE_Text(t *testing.T) {
	sse := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"muse-spark-1.3-contributor-free\",\"created_at\":1700000000}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"total_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":3}}}}\n\n"

	resp, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "resp_1" {
		t.Errorf("id = %q, want resp_1", resp.ID)
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "Hello world")
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 9 {
		t.Errorf("usage = %+v, want 7/2/9", resp.Usage)
	}
	if got := resp.Usage.CachedTokens(); got != 3 {
		t.Errorf("cached = %d, want 3", got)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
}

// TestAggregateOpenAIResponsesSSE_FunctionCall 验证 function_call 输出项映射为 tool_calls。
func TestAggregateOpenAIResponsesSSE_FunctionCall(t *testing.T) {
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_2\",\"model\":\"muse-spark-1.3\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_x\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"usage\":{\"input_tokens\":5,\"output_tokens\":3,\"total_tokens\":8}}}\n\n"

	resp, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := resp.Choices[0].Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_x" || tc.Function.Name != "bash" {
		t.Errorf("tool call = %+v, want id=call_x name=bash", tc)
	}
	if tc.Function.Arguments != `{"command":"ls"}` {
		t.Errorf("arguments = %q", tc.Function.Arguments)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

// TestAggregateOpenAIResponsesSSE_Failed 验证 response.failed 事件被识别为错误。
func TestAggregateOpenAIResponsesSSE_Failed(t *testing.T) {
	sse := "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_3\",\"error\":{\"type\":\"FreeTierError\",\"message\":\"free tier restricted\"}}}\n\n"

	_, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
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

// TestAggregateOpenAIResponsesSSE_TopLevelError 验证顶层 error 事件被识别。
func TestAggregateOpenAIResponsesSSE_TopLevelError(t *testing.T) {
	sse := "data: {\"type\":\"error\",\"error\":{\"type\":\"ModelProtocolUnsupported\",\"message\":\"Model does not support this protocol.\"}}\n\n"

	_, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	uerr, ok := err.(*openCodeUpstreamError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if uerr.Type != "ModelProtocolUnsupported" {
		t.Errorf("type = %q", uerr.Type)
	}
}

// TestAggregateOpenAIResponsesSSE_EmptyStream 验证空流报错。
func TestAggregateOpenAIResponsesSSE_EmptyStream(t *testing.T) {
	if _, err := aggregateOpenAIResponsesSSE(strings.NewReader(""), "fallback"); err == nil {
		t.Fatal("expected error for empty stream")
	}
}

// TestAggregateOpenAIResponsesSSE_CompletedFallbackText 验证 delta 缺失时从 completed 的 output 兜底取文本。
func TestAggregateOpenAIResponsesSSE_CompletedFallbackText(t *testing.T) {
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_4\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_4\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"fallback text\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	resp, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "fallback text" {
		t.Errorf("content = %q, want 'fallback text'", resp.Choices[0].Message.Content)
	}
}

// TestAggregateOpenAIResponsesSSE_MalformedSkipped 验证畸形帧跳过。
func TestAggregateOpenAIResponsesSSE_MalformedSkipped(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"A\"}\n\n" +
		"data: {broken json\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"B\"}\n\n"

	resp, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "AB" {
		t.Errorf("content = %q, want AB", got)
	}
}

// TestChatToResponsesBody 验证 Chat → Responses 请求体字段映射。
func TestChatToResponsesBody(t *testing.T) {
	chat := []byte(`{
		"model":"old",
		"messages":[
			{"role":"system","content":"sys prompt"},
			{"role":"user","content":"hello"}
		],
		"max_tokens":5000,
		"tools":[{"type":"function","function":{"name":"bash","description":"run","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)

	out, ok := chatToResponsesBody(chat, "muse-spark-1.3-contributor-free")
	if !ok {
		t.Fatal("chatToResponsesBody returned ok=false")
	}
	s := string(out)

	// 必须映射
	if !strings.Contains(s, `"max_output_tokens":5000`) {
		t.Errorf("max_tokens 未映射为 max_output_tokens: %s", s)
	}
	if !strings.Contains(s, `"instructions":"sys prompt"`) {
		t.Errorf("system 未映射为 instructions: %s", s)
	}
	if !strings.Contains(s, `"model":"muse-spark-1.3-contributor-free"`) {
		t.Errorf("model 未设置: %s", s)
	}
	// tools 应扁平化
	if !strings.Contains(s, `"name":"bash"`) {
		t.Errorf("tools 未扁平化: %s", s)
	}
	if strings.Contains(s, `"function":{`) {
		t.Errorf("tools 仍含嵌套 function 字段: %s", s)
	}
	// Chat 专有字段应被丢弃
	if strings.Contains(s, "stream_options") {
		t.Errorf("stream_options 应被丢弃: %s", s)
	}
	if strings.Contains(s, `"max_tokens"`) {
		t.Errorf("max_tokens 应被移除: %s", s)
	}
	// store 应显式 false
	if !strings.Contains(s, `"store":false`) {
		t.Errorf("store 应为 false: %s", s)
	}
}

// TestChatToResponsesBody_InvalidJSON 验证非法 JSON 返回 false。
func TestChatToResponsesBody_InvalidJSON(t *testing.T) {
	if _, ok := chatToResponsesBody([]byte(`nope`), "m"); ok {
		t.Error("非法 JSON 应返回 ok=false")
	}
}

// TestChatToResponsesBody_NoSystem 验证无 system 消息时不产生 instructions 字段。
func TestChatToResponsesBody_NoSystem(t *testing.T) {
	chat := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out, ok := chatToResponsesBody(chat, "m")
	if !ok {
		t.Fatal("ok=false")
	}
	if strings.Contains(string(out), "instructions") {
		t.Errorf("无 system 时不应有 instructions: %s", out)
	}
}

// TestAggregateResponses_LongLine 验证超长行(如 response.created 携带完整 tools)不被截断。
func TestAggregateResponses_LongLine(t *testing.T) {
	// 构造一个超过 64KB 的 created 事件(模拟携带完整 tools 定义)
	bigTools := make([]map[string]interface{}, 0, 100)
	for i := 0; i < 100; i++ {
		bigTools = append(bigTools, map[string]interface{}{
			"type":        "function",
			"name":        "tool_with_a_rather_long_name_" + strings.Repeat("x", 200) + string(rune('a'+i%26)),
			"description": strings.Repeat("description padding ", 100),
			"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		})
	}
	created, _ := json.Marshal(map[string]interface{}{
		"type": "response.created",
		"response": map[string]interface{}{
			"id": "resp_big", "model": "muse-spark-1.3", "created_at": 1700000000,
			"tools": bigTools,
		},
	})
	sse := "data: " + string(created) + "\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_big\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	if len(sse) < 64*1024 {
		t.Fatalf("测试数据不够大: %d bytes", len(sse))
	}
	t.Logf("SSE 总长 %d bytes", len(sse))

	resp, err := aggregateOpenAIResponsesSSE(strings.NewReader(sse), "fb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Errorf("content = %q, want hello (超长行可能被截断)", resp.Choices[0].Message.Content)
	}
}

// TestChatToResponsesBody_TokenFloor 验证 max_output_tokens 下限保护。
//
// 背景(2026-09-27 实测): reasoning 类模型(如 muse-spark-*)在 max_output_tokens
// 过小时会因思考预算不足而空流转结束(只回 response.created, 客户端收到空内容)。
// 实测: 60/100/101 → 空响应; >=200 → 正常。故设置安全下限。
func TestChatToResponsesBody_TokenFloor(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`{"messages":[],"max_tokens":60}`, 4096},
		{`{"messages":[],"max_tokens":100}`, 4096},
		{`{"messages":[],"max_tokens":4095}`, 4096},
		{`{"messages":[],"max_tokens":4096}`, 4096},
		{`{"messages":[],"max_tokens":32000}`, 32000},
		{`{"messages":[],"max_completion_tokens":50}`, 4096},
	}
	for _, c := range cases {
		out, ok := chatToResponsesBody([]byte(c.in), "m")
		if !ok {
			t.Fatalf("转换失败: %s", c.in)
		}
		var obj struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if err := json.Unmarshal(out, &obj); err != nil {
			t.Fatalf("非法 JSON: %v", err)
		}
		if obj.MaxOutputTokens != c.want {
			t.Errorf("%s → max_output_tokens=%d, want %d", c.in, obj.MaxOutputTokens, c.want)
		}
	}
}

// TestChatToResponsesBody_IncludesReasoningField 验证 include 字段被自动补入。
//
// reasoning 模型必须声明 include:["reasoning.encrypted_content"], 否则流可能提前终止。
func TestChatToResponsesBody_IncludesReasoningField(t *testing.T) {
	out, ok := chatToResponsesBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "m")
	if !ok {
		t.Fatal("转换失败")
	}
	if !strings.Contains(string(out), "reasoning.encrypted_content") {
		t.Errorf("应自动补入 include 字段: %s", out)
	}
	if !strings.Contains(string(out), `"store":false`) {
		t.Errorf("应自动补入 store=false: %s", out)
	}
}
