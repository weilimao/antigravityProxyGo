package relay

// compat_openai_protocol_test.go 锁定 antigravity 号池 Gemini<->OpenAI 协议对齐修复:
//   - P0 #1 finishReason 映射(流式 + 非流式,含 length/content_filter/tool_calls)
//   - P0 #2 model 字段统一(正文 chunk 与工具调用 chunk 同 model 且=clientModel)
//   - P1 #3 #5 OpenAI 思考走 reasoning_content(非流式 message + 流式 delta),不混入 content
//   - P1 #4 工具调用 index 递增(并行 FC 不撞车)
//   - P2 #6 tool_choice 透传到 Gemini toolConfig
//   - P2 #7 max_completion_tokens 兜底为 max_tokens
//   - P2 #8 纯工具调用时 content 输出 null
//   - P2 #9 stream_options.include_usage 末尾 usage chunk
//   - P2 #10 thoughtsTokenCount -> completion_tokens_details.reasoning_tokens

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// geminiFinishSSE 构造一帧带 finishReason 的 Gemini 上游 SSE(可指定是否带 usage)。
func geminiFinishSSE(finish string, withUsage bool, promptTok, candTok, thoughtsTok int) string {
	cand := map[string]interface{}{
		"content": map[string]interface{}{
			"role":  "model",
			"parts": []interface{}{map[string]interface{}{"text": "hi"}},
		},
		"finishReason": finish,
	}
	resp := map[string]interface{}{
		"candidates": []interface{}{cand},
	}
	if withUsage {
		um := map[string]interface{}{
			"promptTokenCount":     promptTok,
			"candidatesTokenCount": candTok,
		}
		if thoughtsTok > 0 {
			um["thoughtsTokenCount"] = thoughtsTok
		}
		resp["usageMetadata"] = um
	}
	b, _ := json.Marshal(resp)
	return "data: " + string(b) + "\n\n"
}

// runGeminiOpenAIStream 把 Gemini 上游 SSE 喂入 handleStreamResponse(openai),返回转译后 SSE 文本。
// clientModel / geminiModel 分开传以校验 model 字段一致性。
func runGeminiOpenAIStream(t *testing.T, upstream, clientModel, geminiModel string, includeUsage bool) string {
	t.Helper()
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	fc := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	h.handleStreamResponse(
		context.Background(),
		fc,
		strings.NewReader(upstream),
		&RelaySession{},
		clientModel,
		geminiModel,
		"openai",
		0,
		includeUsage,
		time.Unix(1700000000, 0),
		"/v1internal:streamGenerateContent",
		"req-test-openai",
	)
	return fc.Body.String()
}

// ===== P0 #1 finishReason 映射 =====

func TestMapGeminiFinishToOpenAI(t *testing.T) {
	cases := []struct {
		reason  string
		hasTool bool
		want    string
	}{
		{"STOP", false, "stop"},
		{"", false, "stop"},
		{"stop", false, "stop"}, // 小写容错
		{"MAX_TOKENS", false, "length"},
		{"SAFETY", false, "content_filter"},
		{"RECITATION", false, "content_filter"},
		{"BLOCKLIST", false, "content_filter"},
		{"PROHIBITED_CONTENT", false, "content_filter"},
		{"SPII", false, "content_filter"},
		{"OTHER", false, "content_filter"},
		{"STOP", true, "tool_calls"}, // 工具调用优先
		{"MAX_TOKENS", true, "tool_calls"},
		{"UNKNOWN_FUTURE", false, "stop"}, // 未知保守归 stop
	}
	for _, c := range cases {
		if got := mapGeminiFinishToOpenAI(c.reason, c.hasTool); got != c.want {
			t.Errorf("mapGeminiFinishToOpenAI(%q,%v)=%q want %q", c.reason, c.hasTool, got, c.want)
		}
	}
}

// TestOpenAIStream_FinishReasonMapping 锁定流式 finish_reason 经映射后正确透出。
func TestOpenAIStream_FinishReasonMapping(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		want   string
	}{
		{"STOP", "STOP", "stop"},
		{"MAX_TOKENS", "MAX_TOKENS", "length"},
		{"SAFETY", "SAFETY", "content_filter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			upstream := geminiFinishSSE(c.finish, false, 0, 0, 0)
			got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-1.5-pro", false)
			if !strings.Contains(got, "\"finish_reason\":\""+c.want+"\"") {
				t.Fatalf("%s: 流式应含 finish_reason=%q, got=\n%s", c.name, c.want, got)
			}
		})
	}
}

// TestOpenAINonStream_FinishReasonMapping 锁定非流式 finish_reason 映射。
func TestOpenAINonStream_FinishReasonMapping(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		want   string
	}{
		{"STOP", "STOP", "stop"},
		{"MAX_TOKENS", "MAX_TOKENS", "length"},
		{"SAFETY", "SAFETY", "content_filter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gemResp := GeminiResponse{
				Candidates: []GeminiCandidate{{
					Content:      GeminiCandidateContent{Parts: []GeminiPart{{Text: "hi"}}, Role: "model"},
					FinishReason: c.finish,
				}},
			}
			b, _ := json.Marshal(gemResp)
			h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
			rr := httptest.NewRecorder()
			h.handleNormalResponse(rr, strings.NewReader(string(b)), nil, "gpt-4o", "gemini-1.5-pro", "openai", time.Now(), "/v1/chat/completions", "req")
			var resp OpenAIResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("解析失败: %v body=%s", err, rr.Body.String())
			}
			if len(resp.Choices) == 0 || resp.Choices[0].FinishReason != c.want {
				t.Fatalf("%s: 非流式 finish_reason want %q got %q", c.name, c.want, finishReasonOf(resp))
			}
		})
	}
}

func finishReasonOf(r OpenAIResponse) string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
}

// ===== P0 #2 model 字段统一 =====

// TestOpenAIStream_ModelFieldConsistency 锁定正文 chunk 与工具调用 chunk 的 model 字段
// 都等于 clientModel(gpt-4o),而非 geminiModel。
func TestOpenAIStream_ModelFieldConsistency(t *testing.T) {
	upstream := "data: " + mustJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role": "model",
					"parts": []interface{}{
						map[string]interface{}{"text": "hello"},
						map[string]interface{}{"functionCall": map[string]interface{}{
							"name": "edit_file",
							"args": map[string]interface{}{"path": "a.go"},
						}},
					},
				},
				"finishReason": "STOP",
			},
		},
	}) + "\n\n"
	got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-1.5-pro", false)
	if strings.Contains(got, "\"model\":\"gemini-1.5-pro\"") {
		t.Fatalf("流式不应再含 geminiModel(gemini-1.5-pro),工具调用 chunk 必须统一为 clientModel, got=\n%s", got)
	}
	if !strings.Contains(got, "\"model\":\"gpt-4o\"") {
		t.Fatalf("流式应含 clientModel(gpt-4o), got=\n%s", got)
	}
}

// TestOpenAINonStream_ModelIsClientModel 锁定非流式响应 model=clientModel。
func TestOpenAINonStream_ModelIsClientModel(t *testing.T) {
	gemResp := GeminiResponse{
		Candidates: []GeminiCandidate{{Content: GeminiCandidateContent{Parts: []GeminiPart{{Text: "hi"}}, Role: "model"}, FinishReason: "STOP"}},
	}
	b, _ := json.Marshal(gemResp)
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	h.handleNormalResponse(rr, strings.NewReader(string(b)), nil, "gpt-4o", "gemini-1.5-pro", "openai", time.Now(), "/v1/chat/completions", "req")
	var resp OpenAIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("非流式 model 应为 clientModel gpt-4o, 实际 %q", resp.Model)
	}
}

// ===== P1 #3 #5 OpenAI 思考走 reasoning_content =====

// TestOpenAINonStream_ReasoningContent 锁定非流式 thought:true → message.reasoning_content 非空,
// content 不含思考文本,且 reasoning_tokens==thoughtsTokenCount。
func TestOpenAINonStream_ReasoningContent(t *testing.T) {
	gemResp := GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: GeminiCandidateContent{
				Parts: []GeminiPart{
					{Text: "思考步骤", Thought: true},
					{Text: "最终答案"},
				},
				Role: "model",
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			ThoughtsTokenCount:   7,
		},
	}
	b, _ := json.Marshal(gemResp)
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	h.handleNormalResponse(rr, strings.NewReader(string(b)), nil, "gpt-4o", "gemini-2.5-flash", "openai", time.Now(), "/v1/chat/completions", "req")
	var resp OpenAIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v body=%s", err, rr.Body.String())
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("无 choices")
	}
	msg := resp.Choices[0].Message
	if msg.ReasoningContent != "思考步骤" {
		t.Errorf("reasoning_content 期望 '思考步骤', 实际 %q", msg.ReasoningContent)
	}
	if msg.Content != "最终答案" {
		t.Errorf("content 期望 '最终答案'(不含思考), 实际 %q", msg.Content)
	}
	if strings.Contains(msg.Content, "思考") {
		t.Errorf("content 不得混入思考文本, 实际 %q", msg.Content)
	}
	if resp.Usage.CompletionTokensDetails == nil || resp.Usage.CompletionTokensDetails.ReasoningTokens != 7 {
		t.Errorf("reasoning_tokens 期望 7, 实际 %#v", resp.Usage.CompletionTokensDetails)
	}
}

// TestOpenAIStream_ReasoningDelta 锁定流式 thought → delta.reasoning_content(非 content),
// 正文 → delta.content,且末帧 usage 含 reasoning_tokens。
func TestOpenAIStream_ReasoningDelta(t *testing.T) {
	upstream := geminiThoughtSSE("思考片段", true) +
		geminiThoughtSSE("正文片段", false) +
		geminiFinishSSE("STOP", true, 10, 5, 7)
	got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-2.5-flash", true)
	chunks := parseCompatOpenAIChunks(t, got)

	var sawReasoning, sawContent bool
	for _, c := range chunks {
		for _, ch := range c.Choices {
			if ch.Delta.ReasoningContent == "思考片段" {
				sawReasoning = true
				if ch.Delta.Content != "" {
					t.Errorf("reasoning chunk 不得同时携带 content, 实际 content=%q", ch.Delta.Content)
				}
			}
			if ch.Delta.Content == "正文片段" {
				sawContent = true
				if ch.Delta.ReasoningContent != "" {
					t.Errorf("正文 chunk 不得同时携带 reasoning_content, 实际=%q", ch.Delta.ReasoningContent)
				}
			}
		}
	}
	if !sawReasoning {
		t.Fatalf("流式应出现 reasoning_content chunk, got=\n%s", got)
	}
	if !sawContent {
		t.Fatalf("流式应出现正文 content chunk, got=\n%s", got)
	}

	// 末尾 usage chunk(因 include_usage=true)含 reasoning_tokens
	var lastUsage *OpenAIResponseUsage
	for _, c := range chunks {
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}
	if lastUsage == nil {
		t.Fatalf("include_usage=true 时末尾应出现 usage chunk, got=\n%s", got)
	}
	if lastUsage.CompletionTokensDetails == nil || lastUsage.CompletionTokensDetails.ReasoningTokens != 7 {
		t.Errorf("usage chunk reasoning_tokens 期望 7, 实际 %#v", lastUsage.CompletionTokensDetails)
	}
}

// ===== P1 #4 工具调用 index 递增 =====

// TestOpenAIStream_MultiToolCallIndex 锁定两个并行 functionCall 各占不同递增 index。
func TestOpenAIStream_MultiToolCallIndex(t *testing.T) {
	upstream := "data: " + mustJSON(map[string]interface{}{
		"candidates": []interface{}{
			map[string]interface{}{
				"content": map[string]interface{}{
					"role": "model",
					"parts": []interface{}{
						map[string]interface{}{"functionCall": map[string]interface{}{
							"name": "tool_a", "args": map[string]interface{}{"x": 1},
						}},
						map[string]interface{}{"functionCall": map[string]interface{}{
							"name": "tool_b", "args": map[string]interface{}{"y": 2},
						}},
					},
				},
				"finishReason": "STOP",
			},
		},
	}) + "\n\n"
	got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-1.5-pro", false)
	chunks := parseCompatOpenAIChunks(t, got)

	var idxSet = map[int]bool{}
	for _, c := range chunks {
		for _, ch := range c.Choices {
			for _, tc := range ch.Delta.ToolCalls {
				idxSet[tc.Index] = true
			}
		}
	}
	// 应出现 0 与 1 两个不同 index,而非全部 0
	if !idxSet[0] || !idxSet[1] {
		t.Fatalf("两个并行工具调用应出现 index 0 与 1, 实际 idxSet=%v, got=\n%s", idxSet, got)
	}
}

// ===== P2 #6 tool_choice 透传 =====

func TestOpenAIToolChoiceToGemini(t *testing.T) {
	// "required" → VALIDATED
	tc := openAIToolChoiceToGemini("required")
	if tc == nil || tc.FunctionCallingConfig.Mode != "VALIDATED" {
		t.Fatalf("required 期望 VALIDATED, 实际 %#v", tc)
	}
	// "none" → NONE
	tc = openAIToolChoiceToGemini("none")
	if tc == nil || tc.FunctionCallingConfig.Mode != "NONE" {
		t.Fatalf("none 期望 NONE, 实际 %#v", tc)
	}
	// 指定函数 → VALIDATED + AllowedFunctionNames
	tc = openAIToolChoiceToGemini(map[string]interface{}{
		"type":     "function",
		"function": map[string]interface{}{"name": "edit_file"},
	})
	if tc == nil || tc.FunctionCallingConfig.Mode != "VALIDATED" ||
		len(tc.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
		tc.FunctionCallingConfig.AllowedFunctionNames[0] != "edit_file" {
		t.Fatalf("指定函数期望 AllowedFunctionNames=[edit_file], 实际 %#v", tc)
	}
	// nil → nil
	if openAIToolChoiceToGemini(nil) != nil {
		t.Fatalf("nil 期望 nil")
	}
}

// TestTranslateOpenAI_ToolChoicePassthrough 锁定 OpenAI 入站 tool_choice 被翻译进 geminiReq.ToolConfig。
func TestTranslateOpenAI_ToolChoicePassthrough(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		req := &OpenAIRequest{
			Model:      "gpt-4o",
			Messages:   []OpenAIMessage{{Role: "user", Content: "hi"}},
			Tools:      []AnthropicTool{{Name: "edit_file", InputSchema: map[string]interface{}{"type": "object"}}},
			ToolChoice: "required",
		}
		g := TranslateOpenAIToGemini(req)
		if g.ToolConfig == nil || g.ToolConfig.FunctionCallingConfig == nil ||
			g.ToolConfig.FunctionCallingConfig.Mode != "VALIDATED" {
			t.Fatalf("required 期望 ToolConfig.Mode=VALIDATED, 实际 %#v", g.ToolConfig)
		}
	})
	t.Run("none", func(t *testing.T) {
		req := &OpenAIRequest{
			Model:      "gpt-4o",
			Messages:   []OpenAIMessage{{Role: "user", Content: "hi"}},
			Tools:      []AnthropicTool{{Name: "edit_file", InputSchema: map[string]interface{}{"type": "object"}}},
			ToolChoice: "none",
		}
		g := TranslateOpenAIToGemini(req)
		if g.ToolConfig == nil || g.ToolConfig.FunctionCallingConfig.Mode != "NONE" {
			t.Fatalf("none 期望 ToolConfig.Mode=NONE, 实际 %#v", g.ToolConfig)
		}
	})
	t.Run("specify_function", func(t *testing.T) {
		req := &OpenAIRequest{
			Model:    "gpt-4o",
			Messages: []OpenAIMessage{{Role: "user", Content: "hi"}},
			Tools:    []AnthropicTool{{Name: "edit_file", InputSchema: map[string]interface{}{"type": "object"}}},
			ToolChoice: map[string]interface{}{
				"type":     "function",
				"function": map[string]interface{}{"name": "edit_file"},
			},
		}
		g := TranslateOpenAIToGemini(req)
		if g.ToolConfig == nil || len(g.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
			g.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "edit_file" {
			t.Fatalf("指定函数期望 AllowedFunctionNames=[edit_file], 实际 %#v", g.ToolConfig)
		}
	})
}

// ===== P2 #7 max_completion_tokens 兜底 =====

func TestParseUnifiedOpenAIRequest_MaxCompletionTokens(t *testing.T) {
	body := `{"model":"o3","max_completion_tokens":4096,"messages":[{"role":"user","content":"hi"}]}`
	req, err := ParseUnifiedOpenAIRequest([]byte(body))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 4096 {
		t.Fatalf("max_completion_tokens 应兜底进 MaxTokens=4096, 实际 %#v", req.MaxTokens)
	}
	if req.MaxCompletionTokens == nil || *req.MaxCompletionTokens != 4096 {
		t.Fatalf("MaxCompletionTokens 应保留原值 4096, 实际 %#v", req.MaxCompletionTokens)
	}
}

// TestParseUnifiedOpenAIRequest_MaxTokensPreferredOverMaxCompletion 锁定两者并存时 max_tokens 优先。
func TestParseUnifiedOpenAIRequest_MaxTokensPreferredOverMaxCompletion(t *testing.T) {
	body := `{"model":"o3","max_tokens":100,"max_completion_tokens":4096,"messages":[{"role":"user","content":"hi"}]}`
	req, err := ParseUnifiedOpenAIRequest([]byte(body))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Fatalf("max_tokens 优先, 期望 100, 实际 %#v", req.MaxTokens)
	}
}

// ===== P2 #8 content=null =====

// TestOpenAIMessageMarshal_NullContentWhenToolCallsOnly 锁定纯工具调用序列化为 "content":null。
func TestOpenAIMessageMarshal_NullContentWhenToolCallsOnly(t *testing.T) {
	msg := OpenAIMessage{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []OpenAIToolCall{{ID: "call_1", Type: "function", Function: OpenAIToolCallFunction{Name: "edit_file", Arguments: "{}"}}},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"content":null`) {
		t.Fatalf("纯工具调用应输出 content:null, 实际 %s", s)
	}
}

// TestOpenAIMessageMarshal_StringContentWhenText 锁定有正文时 content 为字符串。
func TestOpenAIMessageMarshal_StringContentWhenText(t *testing.T) {
	msg := OpenAIMessage{Role: "assistant", Content: "hello"}
	b, _ := json.Marshal(msg)
	s := string(b)
	if !strings.Contains(s, `"content":"hello"`) {
		t.Fatalf("有正文应输出 content 字符串, 实际 %s", s)
	}
}

// TestOpenAINonStream_ContentNullWhenToolCallsOnly 端到端锁定非流式纯工具调用响应 content=null。
func TestOpenAINonStream_ContentNullWhenToolCallsOnly(t *testing.T) {
	gemResp := GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content: GeminiCandidateContent{
				Parts: []GeminiPart{{
					FunctionCall: &GeminiFunctionCall{Name: "edit_file", Args: map[string]interface{}{"path": "a.go"}},
				}},
				Role: "model",
			},
			FinishReason: "STOP",
		}},
	}
	b, _ := json.Marshal(gemResp)
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	h.handleNormalResponse(rr, strings.NewReader(string(b)), nil, "gpt-4o", "gemini-1.5-pro", "openai", time.Now(), "/v1/chat/completions", "req")
	s := rr.Body.String()
	if !strings.Contains(s, `"content":null`) {
		t.Fatalf("纯工具调用非流式响应应含 content:null, 实际 %s", s)
	}
	if strings.Contains(s, `"content":""`) {
		t.Fatalf("纯工具调用不应输出 content 空串, 实际 %s", s)
	}
}

// ===== P2 #9 stream_options.include_usage =====

// TestOpenAIStream_IncludeUsageChunk 锁定 include_usage=true 末尾出现 choices 为空 + usage 的 chunk。
func TestOpenAIStream_IncludeUsageChunk(t *testing.T) {
	upstream := geminiFinishSSE("STOP", true, 100, 42, 0) // 末帧带 usage
	got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-1.5-pro", true)
	chunks := parseCompatOpenAIChunks(t, got)

	var sawUsageChunk bool
	for _, c := range chunks {
		if c.Usage != nil && len(c.Choices) == 0 {
			sawUsageChunk = true
			if c.Usage.PromptTokens != 100 || c.Usage.CompletionTokens != 42 {
				t.Errorf("usage chunk 数量期望 100/42, 实际 %d/%d", c.Usage.PromptTokens, c.Usage.CompletionTokens)
			}
		}
	}
	if !sawUsageChunk {
		t.Fatalf("include_usage=true 时末尾应出现 choices=[]+usage 的独立 chunk, got=\n%s", got)
	}
	// [DONE] 仍应存在
	if !strings.Contains(got, "data: [DONE]") {
		t.Fatalf("流末尾仍应含 [DONE], got=\n%s", got)
	}
}

// TestOpenAIStream_NoUsageChunkWhenNotRequested 锁定未请求 include_usage 时不发 usage chunk。
func TestOpenAIStream_NoUsageChunkWhenNotRequested(t *testing.T) {
	upstream := geminiFinishSSE("STOP", true, 100, 42, 0)
	got := runGeminiOpenAIStream(t, upstream, "gpt-4o", "gemini-1.5-pro", false)
	chunks := parseCompatOpenAIChunks(t, got)
	for _, c := range chunks {
		if c.Usage != nil {
			t.Fatalf("未请求 include_usage 时不应出现 usage chunk, 实际出现 %#v", c.Usage)
		}
	}
}

// ===== 辅助 =====

// parseCompatOpenAIChunks 解析 SSE 文本中所有 data 行(跳过 [DONE])为 OpenAIStreamChunk。
func parseCompatOpenAIChunks(t *testing.T, sse string) []OpenAIStreamChunk {
	t.Helper()
	var out []OpenAIStreamChunk
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var c OpenAIStreamChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}
