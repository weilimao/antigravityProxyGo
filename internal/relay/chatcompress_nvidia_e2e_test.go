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
