package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"antigravity-proxy/internal/pricing"
)

// chatcompress_nvidia_e2e_test.go —— chatcompress 接入 handleNvidia 的端到端集成测试。
// 复用 nvidia_test.go 的 httptest 骨架,但手工注入开启压缩配置的 mock settings handler,
// 使 ResourceExhausted 首帧能真正进入"就地压缩 → 原地复用号重试"分支。
//
// 注意:不借用 compat_compression_test.go 的 MockSettingsManager(它嵌入 settings.ManagerInterface
// 但只覆盖少数方法,未覆盖的方法走嵌入接口会 nil deref)。这里定义独立的最小 mock,
// 仅覆盖 handleNvidia 流程实际会调用的 settingsMgr 方法:GetSessionOptimization +
// GetEnableDebuggerMode + GetResolvedDebuggerLogPath,避免污染共享 mock。

// chatcompressE2ESettings 是 chatcompress E2E 测试专用的最小 settings mock。
type chatcompressE2ESettings struct {
	settings.ManagerInterface // 嵌入以实现接口(不被调用的方法不会走进去,无 nil deref 风险)
	compCfg                   settings.SessionOptimizationConfig
}

func (m *chatcompressE2ESettings) GetSessionOptimization() settings.SessionOptimizationConfig {
	return m.compCfg
}
func (m *chatcompressE2ESettings) GetEnableDebuggerMode() bool { return false }
func (m *chatcompressE2ESettings) GetResolvedDebuggerLogPath() string { return "" }

// newNvidiaCompressTestHandler 构造一个开启 chatcompress 配置的 handler。
func newNvidiaCompressTestHandler(t *testing.T, accounts []*account.Account, threshold, keepN int) *APICompatHandler {
	t.Helper()
	accMgr := account.NewManager()
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}
	accMgr.SetNvidiaPoolMode(true)
	accMgr.SetActiveChannel("nvidia")

	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	mockSettings := &chatcompressE2ESettings{compCfg: settings.SessionOptimizationConfig{
		NvidiaCompressEnabled:         true,
		NvidiaCompressThresholdTokens: threshold,
		NvidiaCompressKeepToolResults: keepN,
	}}
	return NewAPICompatHandler(nil, accMgr, router, nil, ut, mockSettings, nil)
}

// 流式首帧 ResourceExhausted 错误体(与 nvidia_test.go:1031 真实报错一致)。
const nvidiaResourceExhaustedFrame = `data: {"error":{"message":"ResourceExhausted: Worker local total request limit reached (48/48)","type":"internal_server_error","code":500}}` + "\n\n" + `data: [DONE]` + "\n\n"

// 上下文超窗语义首帧(message 含 maximum context length)。
const nvidiaContextTooLongFrame = `data: {"error":{"message":"This model's maximum context length is 2048 tokens. However, your messages resulted in 999999 tokens. Please reduce the length of the messages.","type":"internal_server_error","code":500}}` + "\n\n" + `data: [DONE]` + "\n\n"

// TestHandleNvidia_ResourceExhausted_TriggersCompressRetry:
// 上游第 1 次返回 ResourceExhausted 首帧;第 2 次(收到压缩后的更小请求体)返回正常 SSE。
// 断言:上游被调 ≥2 次;第 2 次请求体字节数 < 第 1 次(L1 替换旧 tool 内容使体积变小);
// 客户端最终 拿到 200 正常回复;该号未被冷冻。
func TestHandleNvidia_ResourceExhausted_TriggersCompressRetry(t *testing.T) {
	var callCount int32
	var firstBodyLen, secondBodyLen int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		body, _ := io.ReadAll(r.Body)
		if n == 1 {
			firstBodyLen = len(body)
		} else if n == 2 {
			secondBodyLen = len(body)
		}

		if n == 1 {
			// 首次:抛 ResourceExhausted 首帧
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(nvidiaResourceExhaustedFrame))
			return
		}
		// 第 2 次及之后:正常 SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		sse := strings.Join([]string{
			`data: {"id":"1","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"1","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
			`data: [DONE]`,
			"",
		}, "\n\n")
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-c1", "c-1", "k", upstream.URL, "moonshotai/kimi-k2.5")
	// 用极低阈值强制压缩可被触发
	handler := newNvidiaCompressTestHandler(t, []*account.Account{acc}, 8000, 4)

	// 构造一个含多个 tool 结果的大请求(入站 OpenAI Chat),messages 含大量 tool 结果,
	// 让 chatcompress 的 L1 能清理掉旧 tool 内容,第 2 次体更小。
	var msgs []ChatMessage
	msgs = append(msgs, ChatMessage{Role: "system", Content: "sys"})
	msgs = append(msgs, ChatMessage{Role: "user", Content: "do work"})
	msgs = append(msgs, ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "c0", Type: "function", Function: ChatToolCallFunction{Name: "f", Arguments: "{}"}}}})
	for i := 0; i < 10; i++ {
		msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: "c0", ToolName: "f", Content: strings.Repeat("x", 3000)})
	}

	chatReq := &OpenAIChatRequest{
		Model:    "claude-sonnet-4-5",
		Stream:   true,
		Messages: msgs,
	}
	body, _ := json.Marshal(chatReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u1", UserKey: "k1"})

	if got := atomic.LoadInt32(&callCount); got < 2 {
		t.Fatalf("expected upstream called ≥2 times (compress+retry), got %d", got)
	}
	if secondBodyLen <= 0 || secondBodyLen >= firstBodyLen {
		t.Fatalf("expected 2nd request body bytes smaller, 1st=%d 2nd=%d", firstBodyLen, secondBodyLen)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after compress retry, got %d body=%s", rr.Code, rr.Body.String())
	}
	// 该号不应被冷冻(账号 cooldown 为空验证)
	cooldowns := acc.Cooldowns
	if len(cooldowns) > 0 {
		t.Errorf("expected account not cooled-down after successful compress retry, got cooldowns=%v", cooldowns)
	}
}

// TestHandleNvidia_ResourceExhausted_CompressFailsThenReply400:
// 上游对所有请求都只回"上下文超窗"语义首帧,且 L1/L2 压缩也无法让上游接受(永远报错)。
// 断言:压缩 3 轮耗尽后,因首帧含上下文超窗语义 → 回写 Anthropic 标准 invalid_request_error 400,
// 客户端可识别此结构触发本地 /compact 自压(治本)。
func TestHandleNvidia_ResourceExhausted_CompressFailsThenReply400(t *testing.T) {
	var callCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		// 永远回上下文超窗首帧
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(nvidiaContextTooLongFrame))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-c2", "c-2", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler := newNvidiaCompressTestHandler(t, []*account.Account{acc}, 4000, 2)

	// 用 Anthropic 入站(/nvidia/v1/messages)以验证 400 回的是 Anthropic 错误体结构
	anthReq := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		System: "sys",
		Stream: true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: strings.Repeat("a", 20000)}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u2", UserKey: "k2"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid_request_error (治本引导自压), got %d body=%s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	if !strings.Contains(out, `"invalid_request_error"`) {
		t.Errorf("expected invalid_request_error type in body, got: %s", out)
	}
	if !strings.Contains(out, "reduce the length") && !strings.Contains(out, "context length") {
		t.Errorf("expected context-too-long cue in message to trigger client compact, got: %s", out)
	}
}

// TestLooksLikeContextTooLong 单测语义判定函数。
func TestLooksLikeContextTooLong(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{nvidiaContextTooLongFrame, true},
		{nvidiaResourceExhaustedFrame, false}, // 纯 worker 限流,不含超窗文案
		{"", false},
		{"some random error", false},
		{"prompt is too long please reduce", true},
	}
	for i, c := range cases {
		if got := looksLikeContextTooLong(c.in); got != c.want {
			t.Errorf("case %d: looksLikeContextTooLong(%q)=%v want %v", i, c.in, got, c.want)
		}
	}
}

// TestHandleNvidia_AnthropicPureToolUse_NoMissingContentField:
// 回归保护:入站 Anthropic /nvidia/v1/messages 携带"纯 tool_use 的 assistant + 空 content 的 tool_result
// + 全空 user"这种历史上会触发 NVIDIA 上游 "missing field `content`" 400 的形态。
// 断言:发往上游的 OpenAI Chat body 中,每一条 message 都显式带 "content" 键(不再被 omitempty 丢弃),
// 且上游回 200 正常,整链闭环不再 400。
func TestHandleNvidia_AnthropicPureToolUse_NoMissingContentField(t *testing.T) {
	var upstreamBodies []string
	var callCount int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		body, _ := io.ReadAll(r.Body)
		upstreamBodies = append(upstreamBodies, string(body))

		// 模拟 NVIDIA 上游正常 200 响应(非流式),证明请求体被上游接受、未报 missing field content。
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"moonshotai/kimi-k2.5",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-c3", "c-3", "k", upstream.URL, "moonshotai/kimi-k2.5")
	// 关闭 chatcompress,避免压缩逻辑干扰本用例对 content 字段落盘的纯粹断言。
	handler := newNvidiaCompressTestHandler(t, []*account.Account{acc}, 1_000_000, 4)

	// 入站 Anthropic:构造多轮工具会话历史。
	//  - user: 全空(命中 anthropicUserToChat 行 172 兜底分支,补 Content:"")
	//  - assistant: 纯 tool_use 无文本(命中 anthropicAssistantToChat,Content 为空)
	//  - user/tool_result: 空 content(命中 flattenToolResultContent 返空 → tool 角色 Content:"")
	// 这三种是过去因 ChatMessage.Content omitempty 导致 "content" 键丢失、上游回 400 的典型形态。
	anthReq := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Stream: false,
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: ""}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "tool_use", ID: "toolu_1", Name: "read_file", Input: map[string]interface{}{"path": "a.go"}},
			}},
			{Role: "user", Content: []AnthropicContent{
				{Type: "tool_result", ToolUseID: "toolu_1", ToolResultContent: json.RawMessage(`"\""`)}, // 空字符串内容
			}},
		},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u3", UserKey: "k3"})

	if got := atomic.LoadInt32(&callCount); got < 1 {
		t.Fatalf("expected upstream called ≥1, got %d", got)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK (upstream accepts body), got %d body=%s", rr.Code, rr.Body.String())
	}

	// 核心断言:上游收到的 body 中,每条 message 都显式含 "content" 键(修复前会被 omitempty 丢弃)。
	var upstreamChat OpenAIChatRequest
	if err := json.Unmarshal([]byte(upstreamBodies[0]), &upstreamChat); err != nil {
		t.Fatalf("upstream body not valid OpenAI chat json: %v body=%s", err, upstreamBodies[0])
	}
	if len(upstreamChat.Messages) != 3 {
		// 实测:全空 user 经 anthropicUserToChat 兜底补成 1 条;纯 tool_use assistant 经
		// anthropicAssistantToChat 产出 1 条;tool_result 经 anthropicUserToChat 拆成 1 条 tool 消息 → 共 3 条。
		t.Fatalf("expected 3 upstream messages covering user/assistant/tool, got %d body=%s",
			len(upstreamChat.Messages), upstreamBodies[0])
	}
	// 重新逐条解析原始 JSON,验证每个 messages 元素都带 "content" 键(omitempty 修复的回归点)。
	var raw struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(upstreamBodies[0]), &raw); err != nil {
		t.Fatalf("re-parse upstream messages failed: %v", err)
	}
	for i, m := range raw.Messages {
		if _, ok := m["content"]; !ok {
			t.Errorf("upstream message[%d] (role=%s) missing \"content\" key — omitempty regression: body=%s",
				i, mustReadRole(m), upstreamBodies[0])
		}
	}
}

// mustReadRole 从原始 message map 中安全读 role 字段,供断言报错时定位。
func mustReadRole(m map[string]json.RawMessage) string {
	if raw, ok := m["role"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return "?"
}

// TestWriteNvidiaAnthropic_Non200Upstream_RepliesAnthropicError:
// 回归保护 —— NVIDIA 流式入站是 Anthropic(/nvidia/v1/messages)时,若上游回非 200 错误,
// 代理必须回写 Anthropic 标准 error 结构 {"type":"error","error":{"type":..,"message":..}},
// 而非裸透 OpenAI 的 {"error":{"message":..,"code":..}}(Claude Code/VSCode 插件无法识别后者,
// 表现为卡住或奇怪报错 —— "断了不干活"诱因之一)。文案透传上游 message 原文便于定位。
//
// 覆盖流式写回路径 writeNvidiaAnthropicStream 的非 200 分支(主循环已处理的 429/401/403/5xx
// 不会走到这里,故用上游直接回 4xx 模拟"未被主循环分类的 400 类"形态)。
func TestWriteNvidiaAnthropic_Non200Upstream_RepliesAnthropicError(t *testing.T) {
	// 上游直接回 4xx + OpenAI 错误体(模拟 NVIDIA 真实 400 形态,如 schema 不符等)。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream rejected: model not allowed","type":"invalid_request_error","code":400}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-c4", "c-4", "k", upstream.URL, "moonshotai/kimi-k2.5")
	// 关闭 chatcompress(本用例只验证非 200 错误回写格式,不涉及压缩)。
	handler := newNvidiaCompressTestHandler(t, []*account.Account{acc}, 1_000_000, 4)

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Stream:   true, // 流式入站,走 writeNvidiaAnthropicStream 非 200 分支
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u4", UserKey: "k4"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (upstream status passthrough), got %d body=%s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	// 必须是 Anthropic error 结构:Claude Code 据此识别错误并走恢复机制。
	if !strings.Contains(out, `"type":"error"`) {
		t.Errorf("expected Anthropic top-level {\"type\":\"error\"}, got: %s", out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("expected nested \"error\" object, got: %s", out)
	}
	// 上游 message 原文必须透传,便于从 CLI 报错直接定位 NVIDIA 真实原因。
	if !strings.Contains(out, "model not allowed") {
		t.Errorf("expected upstream message passthrough, got: %s", out)
	}
	// 4xx 应映射 Anthropic invalid_request_error 语义。
	if !strings.Contains(out, `"invalid_request_error"`) {
		t.Errorf("expected invalid_request_error type for 4xx, got: %s", out)
	}
	// 反向断言:不得裸透 OpenAI 原始 error 结构({"error":{"code":400}}),否则 CLI 无法识别。
	if strings.Contains(out, `"code":400`) {
		t.Errorf("should NOT passthrough raw OpenAI error {\"code\":..}, got: %s", out)
	}
}
