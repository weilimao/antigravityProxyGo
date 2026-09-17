package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/stats"
)

func TestWorkBuddy_EnsureSystemPrompt(t *testing.T) {
	// Case 1: 空消息
	req1 := &OpenAIChatRequest{}
	ensureWorkBuddySystemPrompt(req1)
	if len(req1.Messages) != 1 || req1.Messages[0].Role != "system" {
		t.Fatalf("空消息未正确补齐 system prompt, got: %+v", req1.Messages)
	}

	// Case 2: 首条为 user 消息
	req2 := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello"},
		},
	}
	ensureWorkBuddySystemPrompt(req2)
	if len(req2.Messages) != 2 || req2.Messages[0].Role != "system" || req2.Messages[1].Role != "user" {
		t.Fatalf("首条非 system 消息未在前置位置插入 system prompt, got: %+v", req2.Messages)
	}

	// Case 3: 首条已经是 system 消息
	req3 := &OpenAIChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "Custom instruction"},
			{Role: "user", Content: "Hello"},
		},
	}
	ensureWorkBuddySystemPrompt(req3)
	if len(req3.Messages) != 2 || req3.Messages[0].Content != "Custom instruction" {
		t.Fatalf("已有 system 消息被篡改, got: %+v", req3.Messages)
	}
}

func TestWorkBuddy_AggregateStreamToJSON(t *testing.T) {
	mockSSE := `
data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"Thinking hard"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"content":"Hello ","reasoning_content":" about life"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"content":"world!"},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"deepseek-v4.1-flash","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":25,"total_tokens":37}}

data: [DONE]
`
	resp, err := aggregateOpenAISSEStream(strings.NewReader(mockSSE), "deepseek-v4.1-flash")
	if err != nil {
		t.Fatalf("聚合流式数据报错: %v", err)
	}

	if resp.ID != "chatcmpl-test" {
		t.Errorf("ID 期望 chatcmpl-test, 得到 %s", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices 数量期望 1, 得到 %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello world!" {
		t.Errorf("Content 期望 'Hello world!', 得到 '%s'", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].Message.ReasoningContent != "Thinking hard about life" {
		t.Errorf("ReasoningContent 期望 'Thinking hard about life', 得到 '%s'", resp.Choices[0].Message.ReasoningContent)
	}
	if resp.Usage.TotalTokens != 37 {
		t.Errorf("TotalTokens 期望 37, 得到 %d", resp.Usage.TotalTokens)
	}
}

func TestWorkBuddy_PathPrefixMatch(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		{"/workbuddy", true},
		{"/workbuddy/", true},
		{"/workbuddy/v1/chat/completions", true},
		{"/wb", true},
		{"/wb/v1/messages", true},
		{"/workbuddyfoo", false},
		{"/wbtest", false},
		{"/other/path", false},
	}

	for _, c := range cases {
		got := workbuddyAliasPrefixMatch(c.path)
		if got != c.expected {
			t.Errorf("workbuddyAliasPrefixMatch(%s) = %v, 期望 %v", c.path, got, c.expected)
		}
	}
}

func TestWorkBuddy_EndToEndMock(t *testing.T) {
	// 启动 Mock 上游 WorkBuddy 服务器
	var receivedAuthHeader string
	var receivedIDEType string
	var receivedStream bool
	var receivedFirstRole string

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		receivedIDEType = r.Header.Get("X-IDE-Type")

		var payload struct {
			Stream   bool                `json:"stream"`
			Messages []ChatMessage `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		receivedStream = payload.Stream
		if len(payload.Messages) > 0 {
			receivedFirstRole = payload.Messages[0].Role
		}

		// 模拟返回 SSE 流
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_mock\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello from WorkBuddy\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_mock\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(mockServer.Close)

	tempDir, _ := os.MkdirTemp("", "wb_relay_test_*")
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     mockServer.URL,
		AccessToken: "mock-jwt-token-12345",
		Nickname:    "mock_user",
	})

	handler := NewAPICompatHandler(nil, accMgr, nil, nil, nil, nil, nil)

	// 1. 测试客户端请求 stream: false (非流式)，中间层必须转换发 stream: true 并聚合返回完整 JSON
	reqBody := []byte(`{
		"model": "deepseek-v4.1-flash",
		"messages": [{"role": "user", "content": "Hi"}],
		"stream": false
	}`)
	httpReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/chat/completions", strings.NewReader(string(reqBody)))
	w := httptest.NewRecorder()

	handler.handleWorkBuddy(w, httpReq, &RelaySession{UserID: "test_user"})

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("请求返回非 200: %d, body: %s", resp.StatusCode, string(body))
	}

	// 校验上游收到的请求
	if receivedAuthHeader != "Bearer mock-jwt-token-12345" {
		t.Errorf("上游收到的 Auth Header 不匹配: %s", receivedAuthHeader)
	}
	if receivedIDEType != "CodeBuddy" {
		t.Errorf("上游收到的 X-IDE-Type 不匹配: %s", receivedIDEType)
	}
	if !receivedStream {
		t.Errorf("向 WorkBuddy 发出的请求强制 stream 期望为 true, 实际为 %v", receivedStream)
	}
	if receivedFirstRole != "system" {
		t.Errorf("上游收到的首条消息 role 期望为 system, 实际为 %s", receivedFirstRole)
	}

	// 校验客户端收到的响应 (非流式 JSON)
	var clientResp OpenAIChatResponse
	respBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(respBytes, &clientResp); err != nil {
		t.Fatalf("解析客户端响应 JSON 失败: %v, raw: %s", err, string(respBytes))
	}
	if len(clientResp.Choices) == 0 || clientResp.Choices[0].Message.Content != "Hello from WorkBuddy" {
		t.Errorf("客户端收到的内容不符合预期: %+v", clientResp)
	}

	// 2. 测试 Anthropic 协议入站 (/workbuddy/v1/messages)
	anthReqBody := []byte(`{
		"model": "deepseek-v4.1-flash",
		"messages": [{"role": "user", "content": "Hi from Claude"}],
		"stream": false
	}`)
	httpAnthReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/messages", strings.NewReader(string(anthReqBody)))
	wAnth := httptest.NewRecorder()

	handler.handleWorkBuddy(wAnth, httpAnthReq, &RelaySession{UserID: "test_user"})

	respAnth := wAnth.Result()
	defer respAnth.Body.Close()

	if respAnth.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respAnth.Body)
		t.Fatalf("Anthropic 入站请求返回非 200: %d, body: %s", respAnth.StatusCode, string(body))
	}

	var clientAnthResp AnthropicResponse
	respAnthBytes, _ := io.ReadAll(respAnth.Body)
	if err := json.Unmarshal(respAnthBytes, &clientAnthResp); err != nil {
		t.Fatalf("解析 Anthropic 客户端响应 JSON 失败: %v, raw: %s", err, string(respAnthBytes))
	}
	if clientAnthResp.Type != "message" || clientAnthResp.Role != "assistant" {
		t.Errorf("Anthropic 响应结构不符合预期: %+v", clientAnthResp)
	}
	if len(clientAnthResp.Content) == 0 || clientAnthResp.Content[0].Text != "Hello from WorkBuddy" {
		t.Errorf("Anthropic 响应文本内容不符合预期: %+v", clientAnthResp.Content)
	}
}

// TestWorkBuddy_Thinking_Injection 验证各客户端意图识别与顶层 reasoning_effort 注入
func TestWorkBuddy_Thinking_Injection(t *testing.T) {
	// 1. OpenCode UA 带有 output_config.effort="max"
	anthReq := &AnthropicRequest{
		UserAgent:    "opencode/1.18.18",
		OutputConfig: json.RawMessage(`{"effort":"max"}`),
	}
	mode, effort := workbuddyResolveAnthropicThinking(anthReq, true)
	if mode != workbuddyThinkOn || effort != "max" {
		t.Fatalf("OpenCode UA 期望识别为 workbuddyThinkOn(max), 实际 got mode=%v, effort=%s", mode, effort)
	}

	chatReq := &OpenAIChatRequest{}
	workbuddyApplyThinkingToChat(chatReq, mode, effort)
	if chatReq.ReasoningEffort != "max" {
		t.Errorf("chatReq.ReasoningEffort 期望 max, got: %s", chatReq.ReasoningEffort)
	}
	if chatReq.ChatTemplateKwargs != nil {
		t.Errorf("chatReq.ChatTemplateKwargs 必须为 nil, got: %+v", chatReq.ChatTemplateKwargs)
	}

	// 2. 原生 Anthropic 显式关 thinking.type="disabled"
	anthReqDisabled := &AnthropicRequest{
		Thinking: &AnthropicThinking{Type: "disabled"},
	}
	modeDis, effortDis := workbuddyResolveAnthropicThinking(anthReqDisabled, true)
	if modeDis != workbuddyThinkOff {
		t.Fatalf("disabled 期望识别为 workbuddyThinkOff, got mode=%v, effort=%s", modeDis, effortDis)
	}
	chatReqDis := &OpenAIChatRequest{}
	workbuddyApplyThinkingToChat(chatReqDis, modeDis, effortDis)
	if chatReqDis.ReasoningEffort != "" {
		t.Errorf("disabled 下 ReasoningEffort 期望为空串, got: %s", chatReqDis.ReasoningEffort)
	}

	// 3. 全局总闸关闭时强制 off
	modeGlobalOff, _ := workbuddyResolveAnthropicThinking(anthReq, false)
	if modeGlobalOff != workbuddyThinkOff {
		t.Fatalf("全局关下期望识别为 workbuddyThinkOff, got: %v", modeGlobalOff)
	}

	// 4. 入站 OpenAI 请求带 reasoning_effort
	openAIBody := []byte(`{"model":"deepseek-v4.1-flash","reasoning_effort":"high"}`)
	openAIMode, openAIEffort := workbuddyResolveOpenAIThinking(openAIBody, true)
	if openAIMode != workbuddyThinkOn || openAIEffort != "high" {
		t.Fatalf("OpenAI 请求期望识别为 workbuddyThinkOn(high), got mode=%v, effort=%s", openAIMode, openAIEffort)
	}
	chatReqOpenAI := &OpenAIChatRequest{}
	workbuddyApplyThinkingToChat(chatReqOpenAI, openAIMode, openAIEffort)
	if chatReqOpenAI.ReasoningEffort != "high" {
		t.Errorf("chatReq.ReasoningEffort 期望 high, got: %s", chatReqOpenAI.ReasoningEffort)
	}
}

// TestWorkBuddy_Thinking_SSE_Translation 验证上游 OpenAI SSE 流中 reasoning_content 能够被准确转译为 Anthropic thinking 块
func TestWorkBuddy_Thinking_SSE_Translation(t *testing.T) {
	mockUpstreamSSE := "data: {\"id\":\"wb_chunk_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"Let me think step by step\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"wb_chunk_2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Final Answer\",\"reasoning_content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"wb_chunk_3\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20,\"total_tokens\":30}}\n\n" +
		"data: [DONE]\n\n"

	var outBuf bytes.Buffer
	bw := bufio.NewWriter(&outBuf)
	inTokens, outTokens, _, err := OpenAIChatSSEToAnthropicSSE(context.Background(), strings.NewReader(mockUpstreamSSE), nil, bw, "deepseek-v4.1-flash", 10)
	if err != nil {
		t.Fatalf("转译 SSE 失败: %v", err)
	}

	outStr := outBuf.String()
	if !strings.Contains(outStr, `"type":"thinking"`) {
		t.Errorf("转译结果缺少 thinking 块开始标记, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, `"type":"thinking_delta"`) || !strings.Contains(outStr, "Let me think step by step") {
		t.Errorf("转译结果缺少 thinking_delta 内容, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Final Answer") {
		t.Errorf("转译结果缺少正文内容 Final Answer, got:\n%s", outStr)
	}
	if inTokens != 10 || outTokens != 20 {
		t.Errorf("Token 计数不匹配: in=%d, out=%d", inTokens, outTokens)
	}
}

// TestWorkBuddy_Usage_Logging 验证 recordWorkBuddyUsage 将日志推入 globalStatsTracker
func TestWorkBuddy_Usage_Logging(t *testing.T) {
	tracker := stats.NewTracker(pricing.NewManager())
	tracker.Init(t.TempDir())
	handler := &APICompatHandler{
		globalStatsTracker: tracker,
	}

	logCtx := workbuddyLogCtx{
		Method:          "POST",
		Host:            "www.codebuddy.ai",
		Path:            "/route/v1/messages",
		SessionID:       "sess-123",
		Account:         "weilimao@test.com",
		StatusCode:      http.StatusOK,
		StartTs:         time.Now().Add(-500 * time.Millisecond),
		FirstByteRec:    nil,
		ReqBody:         map[string]interface{}{"model": "deepseek-v4.1-flash"},
		ReqHeaders:      map[string]interface{}{"user-agent": "opencode/1.18.18"},
		ReasoningEffort: "max",
	}

	poolAccount := &account.Account{
		ID:       "acc-wb-1",
		Email:    "weilimao@test.com",
		Provider: "workbuddy",
	}

	handler.recordWorkBuddyUsage(nil, "deepseek-v4.1-flash", 48, 554, 0, poolAccount, logCtx)

	if tracker.GetRequestLogCount() == 0 {
		t.Fatalf("globalStatsTracker 未记录到任何请求日志")
	}
	if tracker.GetRecentRequestReasoningEffort() != "max" {
		t.Errorf("RequestLog.ReasoningEffort 期望 max, 得到: %s", tracker.GetRecentRequestReasoningEffort())
	}
}

// TestWorkBuddy_OpenCode_EndToEnd_Thinking_And_Log 端到端验证 OpenCode 携带思考意图发起请求时,
// 上游收到 reasoning_effort, 客户端收到 thinking block, 且代理成功打出请求日志
func TestWorkBuddy_OpenCode_EndToEnd_Thinking_And_Log(t *testing.T) {
	var receivedReasoningEffort string
	var receivedChatTemplateKwargs interface{}
	var upstreamRawBody string

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamRawBody = string(body)
		var rawReq map[string]interface{}
		_ = json.Unmarshal(body, &rawReq)
		var req OpenAIChatRequest
		_ = json.Unmarshal(body, &req)
		receivedReasoningEffort = req.ReasoningEffort
		receivedChatTemplateKwargs = rawReq["chat_template_kwargs"]

		// 上游返回带 reasoning_content 的流
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_mock\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\",\"reasoning_content\":\"Deep thinking in progress...\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_mock\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"9.8 is larger than 9.11\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_mock\",\"choices\":[],\"usage\":{\"prompt_tokens\":25,\"completion_tokens\":50,\"total_tokens\":75}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(mockServer.Close)

	tempDir, _ := os.MkdirTemp("", "wb_opencode_test_*")
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     mockServer.URL,
		AccessToken: "mock-jwt-token-opencode",
		Nickname:    "opencode_tester",
	})

	tracker := stats.NewTracker(pricing.NewManager())
	tracker.Init(t.TempDir())
	handler := NewAPICompatHandler(nil, accMgr, nil, nil, nil, nil, nil)
	handler.globalStatsTracker = tracker

	// 构造 OpenCode 发送的 Anthropic 流式请求
	openCodeReqBody := []byte(`{
		"model": "deepseek-v4.1-flash",
		"messages": [{"role": "user", "content": "9.11和9.8哪个大？"}],
		"output_config": {"effort": "max"},
		"stream": true
	}`)
	httpReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/messages", strings.NewReader(string(openCodeReqBody)))
	httpReq.Header.Set("User-Agent", "opencode/1.18.18")
	w := httptest.NewRecorder()

	handler.handleWorkBuddy(w, httpReq, &RelaySession{UserID: "opencode_user"})

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("请求失败, 状态码: %d, body: %s", resp.StatusCode, string(b))
	}

	// 1. 验证上游收到的请求：顶层 reasoning_effort 为 max, chat_template_kwargs 为 nil
	if receivedReasoningEffort != "max" {
		t.Errorf("上游收到的 reasoning_effort 期望 max, 得到: %s", receivedReasoningEffort)
	}
	if receivedChatTemplateKwargs != nil {
		t.Errorf("上游收到的 chat_template_kwargs 期望为 nil, 得到: %+v", receivedChatTemplateKwargs)
	}
	if strings.Contains(upstreamRawBody, "chat_template_kwargs") {
		t.Errorf("上游收到的原始请求体不应包含 chat_template_kwargs: %s", upstreamRawBody)
	}

	// 2. 验证客户端收到的 Anthropic SSE 流中包含思考块
	clientStream, _ := io.ReadAll(resp.Body)
	clientStreamStr := string(clientStream)
	if !strings.Contains(clientStreamStr, `"type":"thinking"`) {
		t.Errorf("客户端收到的流缺少 thinking 块, got:\n%s", clientStreamStr)
	}
	if !strings.Contains(clientStreamStr, "Deep thinking in progress...") {
		t.Errorf("客户端收到的流缺少思考文本, got:\n%s", clientStreamStr)
	}
	if !strings.Contains(clientStreamStr, "9.8 is larger than 9.11") {
		t.Errorf("客户端收到的流缺少正文答案, got:\n%s", clientStreamStr)
	}

	// 3. 验证 globalStatsTracker 中成功打出请求日志
	if tracker.GetRequestLogCount() == 0 {
		t.Fatalf("使用详情未打出请求日志")
	}
	if tracker.GetRecentRequestReasoningEffort() != "max" {
		t.Errorf("日志 ReasoningEffort 期望 max, 得到: %s", tracker.GetRecentRequestReasoningEffort())
	}
}
