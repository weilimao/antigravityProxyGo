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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
)

// newNvidiaTestHandler 构造一个注入 mock 账号池 + sessionRouter + usageTracker 的 handler，供 handleNvidia 测试。
// usageTracker 传真实实例，使 recordNvidiaUsage 的"账号使用统计"落桶可在测试中断言。
func newNvidiaTestHandler(t *testing.T, accounts []*account.Account) (*APICompatHandler, *account.Manager, *session.Router, *stats.UsageTracker) {
	t.Helper()
	accMgr := account.NewManager()
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}
	accMgr.SetNvidiaPoolMode(true)
	accMgr.SetActiveChannel("nvidia")

	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	handler := NewAPICompatHandler(nil, accMgr, router, nil, ut, nil, nil)
	return handler, accMgr, router, ut
}

func mkNvidiaAccount(id, email, key, baseURL, model string) *account.Account {
	return &account.Account{
		ID:          id,
		Email:       email,
		Provider:    "nvidia",
		ScopeType:   "nvidia",
		AccessToken: key,
		BaseURL:     baseURL,
		Enabled:     true,
		ModelSonnet: model,
		DefaultModel: model,
		Cooldowns:   map[string]int64{},
	}
}

// ===== 非流式 Anthropic 入站 → OpenAI Chat 上游 → Anthropic 回译 =====

func TestHandleNvidia_NonStreamAnthropic(t *testing.T) {
	// mock NVIDIA 上游
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/invalid auth header: %s", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req OpenAIChatRequest
		_ = json.Unmarshal(body, &req)
		if req.Model != "moonshotai/kimi-k2.5" {
			t.Errorf("model not mapped to nvidia id: %s", req.Model)
		}
		// system message 应在 messages[0]
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Errorf("system message missing")
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-1", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "Hello from NVIDIA"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-1", "nvidia-1", "test-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		System:   "You are helpful.",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u1", UserKey: "k1"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out AnthropicResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid anthropic response json: %v body=%s", err, rr.Body.String())
	}
	if out.Role != "assistant" || out.StopReason != "end_turn" {
		t.Errorf("role/stop wrong: %s %s", out.Role, out.StopReason)
	}
	if len(out.Content) == 0 || out.Content[0].Type != "text" || out.Content[0].Text != "Hello from NVIDIA" {
		t.Errorf("content wrong: %+v", out.Content)
	}
	if out.Usage.InputTokens != 10 || out.Usage.OutputTokens != 3 {
		t.Errorf("usage wrong: %+v", out.Usage)
	}
}

// ===== 流式 Anthropic 入站 → OpenAI Chat SSE 上游 → Anthropic SSE =====

func TestHandleNvidia_StreamAnthropic(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"1","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`data: {"id":"1","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"delta":{"content":" there"}}]}`,
		`data: {"id":"1","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-1", "nvidia-1", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u2", UserKey: "k2"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	out := rr.Body.String()
	for _, ev := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(out, "event: "+ev) {
			t.Errorf("missing event %q:\n%s", ev, out)
		}
	}
	if !strings.Contains(out, `"text":"Hi"`) || !strings.Contains(out, `"text":" there"`) {
		t.Errorf("text deltas missing:\n%s", out)
	}
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("content type wrong: %s", rr.Header().Get("Content-Type"))
	}
}

// ===== 流式 flush 验证：X-Accel-Buffering + http.Flusher 逐帧 flush =====

// flushCounter 包装 httptest.ResponseRecorder 并记录 Flush() 调用次数。
type flushCounter struct {
	*httptest.ResponseRecorder
	flushCount int
	mu         sync.Mutex
}

func (f *flushCounter) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushCount++
}

func (f *flushCounter) FlushCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushCount
}

// TestWriteNvidiaAnthropicStream_FlusherInvoked 验证 writeNvidiaAnthropicStream:
//   - 响应头含 X-Accel-Buffering: no;
//   - http.Flusher.Flush() 至少被调用一次(每帧 + 收尾);
//   - 流式 SSE 事件序列完整(message_start→...→message_stop)。
func TestWriteNvidiaAnthropicStream_FlusherInvoked(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	rr := httptest.NewRecorder()
	fc := &flushCounter{ResponseRecorder: rr}

	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", strings.NewReader(""))
	// 蓄流回放架构下 writeNvidiaAnthropicStream 需 targetURL/upstreamBody 供断流重拉;
	// 本用例上游流完整(含 finish_reason+usage+[DONE]),首轮即 ready,不会真的重试,故传占位值即可。
	handler.writeNvidiaAnthropicStream(fc, req, mockResp, "z-ai/glm-5.2", &RelaySession{UserID: "u-flush"}, nil, "https://integrate.api.nvidia.com/v1/chat/completions", []byte("{}"), nvidiaLogCtx{})

	// 1) X-Accel-Buffering: no
	if fc.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("缺少 X-Accel-Buffering: no, 实际=%q", fc.Header().Get("X-Accel-Buffering"))
	}

	// 2) Content-Type: text/event-stream
	if fc.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type 期望 text/event-stream, 实际=%q", fc.Header().Get("Content-Type"))
	}

	// 3) 蓄流回放架构下 flusher.Flush() 仅在整条 ready 后回放收尾时被调一次(首帧 + 收尾)。
	// 不再像旧"边读边写"那样逐帧 flush,故期望至少 1 次(响应头推送 + 尾帧落盘)。
	if fc.FlushCount() < 1 {
		t.Errorf("Flush() 调用次数 = %d, 期望至少 1 次(蓄流回放收尾)", fc.FlushCount())
	}

	// 4) SSE 事件序列认证: 检查输出含必须事件
	out := fc.Body.String()
	for _, ev := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(out, "event: "+ev) {
			t.Errorf("缺少事件 %q:\n%s", ev, out)
		}
	}

	// 5) 文本 delta 正确回译
	if !strings.Contains(out, `"text":"Hello"`) || !strings.Contains(out, `"text":" world"`) {
		t.Errorf("文本 delta 缺失:\n%s", out)
	}

	// 6) 状态码 200
	if fc.Code != http.StatusOK {
		t.Errorf("期望 200, 实际 %d", fc.Code)
	}
}

// ===== OpenAI Chat 入站透传 =====

func TestHandleNvidia_OpenAIPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-1", "nvidia-1", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	chatReq := &OpenAIChatRequest{
		Model:    "claude-sonnet-4-5",
		Stream:   false,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	body, _ := json.Marshal(chatReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u3", UserKey: "k3"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"content":"ok"`) {
		t.Errorf("passthrough response wrong: %s", rr.Body.String())
	}
}

// ===== 换号重试:第一个 key 401，换第二个成功 =====

func TestHandleNvidia_RetriesOn401(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer bad-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
		})
	}))
	defer upstream.Close()

	acc1 := mkNvidiaAccount("nv-bad", "nvidia-bad", "bad-key", upstream.URL, "moonshotai/kimi-k2.5")
	acc2 := mkNvidiaAccount("nv-good", "nvidia-good", "good-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc1, acc2})

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u4", UserKey: "k4"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"text":"ok"`) {
		t.Fatalf("expected success content text, got %s", rr.Body.String())
	}
	if calls < 2 {
		t.Errorf("expected at least 2 upstream calls (bad+good), got %d", calls)
	}
	// bad 账号应被冷静
	bad := accMgr.GetAccountByID("nv-bad")
	if bad.CooldownUntil == 0 {
		t.Errorf("bad account should be cooled down after 401")
	}
}

// ===== 单账号 429 内部重试 5 次，5 次后切号成功 =====

func TestHandleNvidia_Retries5TimesOn429ThenSwitchesAccount(t *testing.T) {
	acc1Attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer 429-key" {
			acc1Attempts++
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "success-after-429-switch"}, FinishReason: "stop"}},
		})
	}))
	defer upstream.Close()

	acc1 := mkNvidiaAccount("nv-429", "nvidia-429", "429-key", upstream.URL, "moonshotai/kimi-k2.5")
	acc2 := mkNvidiaAccount("nv-good", "nvidia-good", "good-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc1, acc2})

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u429", UserKey: "k429"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after 429 retries and account switch, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "success-after-429-switch") {
		t.Fatalf("expected success text, got %s", rr.Body.String())
	}
	if acc1Attempts != 5 {
		t.Errorf("expected acc1 to be retried 5 times on 429, got %d attempts", acc1Attempts)
	}
	// acc1 应被冷静
	acc1Obj := accMgr.GetAccountByID("nv-429")
	if acc1Obj.CooldownUntil == 0 {
		t.Errorf("429 account should be cooled down after 5 retries")
	}
}


// ===== 空号池返回 503 =====

func TestHandleNvidia_EmptyPool503(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader([]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u5", UserKey: "k5"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for empty pool, got %d", rr.Code)
	}
}

// ===== 未支持端点返回 404 =====

func TestHandleNvidia_UnknownEndpoint404(t *testing.T) {
	acc := mkNvidiaAccount("nv-1", "n", "k", "https://integrate.api.nvidia.com", "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/embeddings", bytesReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u6", UserKey: "k6"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ===== GET /nvidia/v1/models 端点透传测试（剥离 /nvidia 前缀，请求远端 /v1/models） =====

func TestHandleNvidiaModels_StripPrefixAndPassthrough(t *testing.T) {
	// mock NVIDIA 上游 响应 GET /v1/models
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET method, got %s", r.Method)
		}
		// 校验请求远端路径：绝对不应带有 /nvidia 前缀
		if r.URL.Path != "/v1/models" {
			t.Errorf("upstream received wrong path: %s (must be /v1/models without /nvidia prefix)", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-nvidia-key" {
			t.Errorf("missing/invalid authorization header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"meta/llama-3.3-70b-instruct","object":"model"},{"id":"deepseek-ai/deepseek-r1","object":"model"}]}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-passthrough", "nvidia-passthrough", "test-nvidia-key", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()

	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u_models", UserKey: "k_models"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d body=%s", rr.Code, rr.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid json response from model list: %v", err)
	}
	data, ok := res["data"].([]interface{})
	if !ok || len(data) != 2 {
		t.Fatalf("expected data array with 2 items, got %+v", res)
	}
}

// ===== 验证免开关直通英伟达号池 =====

func TestHandleNvidia_DirectPoolWithoutToggle(t *testing.T) {
	accMgr := account.NewManager()
	acc1 := mkNvidiaAccount("nv-1", "nv1@test.com", "k1", "https://integrate.api.nvidia.com", "model-1")
	acc2 := mkNvidiaAccount("nv-2", "nv2@test.com", "k2", "https://integrate.api.nvidia.com", "model-2")
	accMgr.AddAccount(acc1)
	accMgr.AddAccount(acc2)
	// 显式将 poolMode 设为 false，验证带 /nvidia 前缀仍能拿到全量可用账号
	accMgr.SetNvidiaPoolMode(false)

	available := accMgr.GetAvailableAccountsForChannel("nvidia", "")
	if len(available) != 2 {
		t.Fatalf("expected 2 available nvidia accounts regardless of poolMode toggle, got %d", len(available))
	}
}

// ===== 验证英伟达号池双模式负载均衡算法（游标轮询 vs 粘性会话） =====

func TestHandleNvidia_LBMode_RoundRobinAndSticky(t *testing.T) {
	var requestedKeys []string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		mu.Lock()
		requestedKeys = append(requestedKeys, key)
		mu.Unlock()
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-test", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "OK"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc1 := mkNvidiaAccount("nv-1", "nv1@test.com", "key-1", upstream.URL, "moonshotai/kimi-k2.5")
	acc2 := mkNvidiaAccount("nv-2", "nv2@test.com", "key-2", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc1, acc2})

	// 1. 验证默认模式为 round-robin 游标轮询：同一 session 发起 4 次请求，会交替使用 key-1, key-2
	accMgr.SetNvidiaLBMode("round-robin")

	makeReq := func() {
		anthReq := &AnthropicRequest{
			Model:    "claude-sonnet-4-5",
			Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
		}
		body, _ := json.Marshal(anthReq)
		req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
		rr := httptest.NewRecorder()
		handler.handleNvidia(rr, req, &RelaySession{UserID: "sess-1", UserKey: "k1"})
	}

	for i := 0; i < 4; i++ {
		makeReq()
	}

	mu.Lock()
	keysRoundRobin := append([]string(nil), requestedKeys...)
	requestedKeys = nil
	mu.Unlock()

	if len(keysRoundRobin) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(keysRoundRobin))
	}
	// 应交替使用不同 key
	if keysRoundRobin[0] == keysRoundRobin[1] || keysRoundRobin[1] == keysRoundRobin[2] {
		t.Fatalf("expected round-robin alternating keys, got: %v", keysRoundRobin)
	}

	// 2. 验证切换为 sticky 粘性会话模式：同一 session 发起 4 次请求，始终锁定同一 key
	accMgr.SetNvidiaLBMode("sticky")
	for i := 0; i < 4; i++ {
		makeReq()
	}

	mu.Lock()
	keysSticky := append([]string(nil), requestedKeys...)
	mu.Unlock()

	if len(keysSticky) != 4 {
		t.Fatalf("expected 4 sticky requests, got %d", len(keysSticky))
	}
	for _, k := range keysSticky {
		if k != keysSticky[0] {
			t.Fatalf("expected all sticky requests to use same key %s, got: %v", keysSticky[0], keysSticky)
		}
	}
}

// bytesReader 包一层避免在测试文件顶部多引一个 import（bytes 已通过 nvidia.go 间接可用，这里独立引用）。
func bytesReader(b []byte) *bytesReaderImpl {
	return &bytesReaderImpl{data: b}
}

type bytesReaderImpl struct {
	data []byte
	off  int
}

func (b *bytesReaderImpl) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

// ===== 新增：NVIDIA 用量应落入“账号使用统计”(usage.json) 维度，按号池成员账号分桶 =====

// findUsageAccountByEmail 在 UsageState.Accounts 中按 email 命中账号桶。
// GetPayload 会按 email:provider 重映射展示键，故这里按 email 遍历查找，避免依赖内部键格式。
func findUsageAccountByEmail(payload interface{}, email string) (*stats.AccountUsage, bool) {
	state, ok := payload.(stats.UsageState)
	if !ok {
		return nil, false
	}
	for _, acc := range state.Accounts {
		if acc.Email == email {
			return acc, true
		}
	}
	return nil, false
}

// TestRecordNvidiaUsage_AccountBucket_NonStreamAnthropic 验证非流式 Anthropic 入站成功后，
// 承担请求的 NVIDIA 号池账号出现在 UsageTracker 账号维度统计中，且 provider/model/token 与入参一致。
func TestRecordNvidiaUsage_AccountBucket_NonStreamAnthropic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 12, CompletionTokens: 7, TotalTokens: 19},
		})
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-acct1", "nv1@nexusquantum.cloud", "k1", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, uTracker := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-acct", UserKey: "k-acct"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	payload := uTracker.GetPayload()
	accBucket, ok := findUsageAccountByEmail(payload, "nv1@nexusquantum.cloud")
	if !ok {
		t.Fatalf("expected nvidia account bucket for nv1@nexusquantum.cloud in usage stats, got: %+v", payload)
	}
	if accBucket.Provider != "nvidia" {
		t.Errorf("expected provider=nvidia, got %q", accBucket.Provider)
	}
	if accBucket.RequestCount != 1 {
		t.Errorf("expected requestCount=1, got %d", accBucket.RequestCount)
	}
	if accBucket.InputTokens != 12 || accBucket.OutputTokens != 7 {
		t.Errorf("expected tokens (12,7), got (%d,%d)", accBucket.InputTokens, accBucket.OutputTokens)
	}
	// 模型名去前缀喂入，应展示为上游模型 moonshotai/kimi-k2.5 而非 claude-sonnet-4-5 或带 nvidia/ 前缀
	mUsage, mExists := accBucket.Models["moonshotai/kimi-k2.5"]
	if !mExists {
		t.Fatalf("expected model bucket 'moonshotai/kimi-k2.5' in account stats, got models: %+v", accBucket.Models)
	}
	if mUsage.RequestCount != 1 || mUsage.InputTokens != 12 || mUsage.OutputTokens != 7 {
		t.Errorf("model bucket wrong: %+v", mUsage)
	}
}

// TestRecordNvidiaUsage_AccountBucket_OpenAIPassthrough 验证 OpenAI Chat 入站(直接透传上游)成功后，
// 承担请求的 NVIDIA 号池账号同样进入账号维度统计 —— 这是修复前完全缺失的链路。
func TestRecordNvidiaUsage_AccountBucket_OpenAIPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","model":"z-ai/glm-5.2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-acct2", "nv2@nexusquantum.cloud", "k2", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, uTracker := newNvidiaTestHandler(t, []*account.Account{acc})

	chatReq := &OpenAIChatRequest{
		Model:    "claude-sonnet-4-5",
		Stream:   false,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	body, _ := json.Marshal(chatReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-oai", UserKey: "k-oai"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"content":"ok"`) {
		t.Fatalf("passthrough body corrupted: %s", rr.Body.String())
	}

	payload := uTracker.GetPayload()
	accBucket, ok := findUsageAccountByEmail(payload, "nv2@nexusquantum.cloud")
	if !ok {
		t.Fatalf("expected nvidia account bucket for nv2@nexusquantum.cloud (OpenAI passthrough), got: %+v", payload)
	}
	if accBucket.Provider != "nvidia" {
		t.Errorf("expected provider=nvidia, got %q", accBucket.Provider)
	}
	if accBucket.RequestCount != 1 || accBucket.InputTokens != 5 || accBucket.OutputTokens != 4 {
		t.Errorf("expected (reqs=1, in=5, out=4), got %+v", accBucket)
	}
	if _, mExists := accBucket.Models["z-ai/glm-5.2"]; !mExists {
		t.Errorf("expected model bucket 'z-ai/glm-5.2' in account stats, got models: %+v", accBucket.Models)
	}
}

// TestRecordNvidiaUsage_AccountBucket_Stream 验证流式 Anthropic 入站成功后
// 末帧 usage 正确嗅探并落账号维度统计。
func TestRecordNvidiaUsage_AccountBucket_Stream(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"1","model":"moonshotai/kimi-k2.5","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-acct3", "nv3@nexusquantum.cloud", "k3", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, uTracker := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-stream", UserKey: "k-stream"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	payload := uTracker.GetPayload()
	accBucket, ok := findUsageAccountByEmail(payload, "nv3@nexusquantum.cloud")
	if !ok {
		t.Fatalf("expected nvidia account bucket for nv3@nexusquantum.cloud (stream), got: %+v", payload)
	}
	if accBucket.RequestCount != 1 || accBucket.InputTokens != 3 || accBucket.OutputTokens != 2 {
		t.Errorf("stream account bucket wrong: %+v", accBucket)
	}
}

// TestRecordNvidiaUsage_NilUsageTracker 不崩溃：当 usageTracker 未装配时，
// recordNvidiaUsage 不应 panic 也不影响 relayStatsMgr 既有落点(此处只验证不 panic)。
func TestRecordNvidiaUsage_NilUsageTracker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer upstream.Close()

	// 手工构造 usageTracker 为 nil 的 handler，仅校验不 panic。
	acc := mkNvidiaAccount("nv-nil", "nvnil@nexusquantum.cloud", "k", upstream.URL, "moonshotai/kimi-k2.5")
	accMgr := account.NewManager()
	accMgr.AddAccount(acc)
	accMgr.SetNvidiaPoolMode(true)
	accMgr.SetActiveChannel("nvidia")
	handler := NewAPICompatHandler(nil, accMgr, session.NewRouter(), nil, nil, nil, nil)

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-nil", UserKey: "k-nil"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil usageTracker (no panic), got %d body=%s", rr.Code, rr.Body.String())
	}
}

// ===== 验证"最少计数优先"选号策略: 高并发下被占用的账号被跳过, 新请求落到计数最少的账号 =====
//
// 这是本次改造的核心业务用例, 验证用户痛点的解决:
// 场景: 两个 NVIDIA 账号, acc1 在最近 1 分钟已被占用 3 次(计数 3), acc2 计数为 0。
//       新发起一个 round-robin 请求, 应优先选中 acc2(计数最少), 而非纯取模可能命中 acc1。
// 对照: 首轮两账号计数都为 0 时, 应退化为原取模轮询(交替), 与既有 LB 测试一致。

func TestHandleNvidia_LeastCountPreferred(t *testing.T) {
	var requestedKeys []string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		mu.Lock()
		requestedKeys = append(requestedKeys, key)
		mu.Unlock()
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-lc", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "OK"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc1 := mkNvidiaAccount("nv-busy", "nv-busy@test.com", "key-busy", upstream.URL, "moonshotai/kimi-k2.5")
	acc2 := mkNvidiaAccount("nv-idle", "nv-idle@test.com", "key-idle", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc1, acc2})
	accMgr.SetNvidiaLBMode("round-robin")

	// 模拟 acc1 在最近 1 分钟已被占用 3 次, acc2 保持空闲(计数 0)。
	// 直接对计数盘手动 Tick, 模拟在另一个请求路径里选择了 acc1。
	if handler.nvidiaStats == nil {
		t.Fatal("nvidiaStats should be initialized by NewAPICompatHandler")
	}
	handler.nvidiaStats.Tick("nv-busy")
	handler.nvidiaStats.Tick("nv-busy")
	handler.nvidiaStats.Tick("nv-busy")
	if got := handler.nvidiaStats.Count("nv-busy"); got != 3 {
		t.Fatalf("acc1 count precondition: want 3, got %d", got)
	}
	if got := handler.nvidiaStats.Count("nv-idle"); got != 0 {
		t.Fatalf("acc2 count precondition: want 0, got %d", got)
	}

	// 新请求(round-robin): acc1 计数 3, acc2 计数 0, 最少计数优先应选中 acc2 (key-idle)。
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	reqBody, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(reqBody))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "lc-1", UserKey: "k-lc"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d body=%s", rr.Code, rr.Body.String())
	}
	mu.Lock()
	keys := append([]string(nil), requestedKeys...)
	mu.Unlock()
	if len(keys) != 1 {
		t.Fatalf("expected exactly 1 upstream call, got %d (%v)", len(keys), keys)
	}
	if keys[0] != "Bearer key-idle" {
		t.Fatalf("least-count preferred should pick acc2(key-idle, count=0), got %s", keys[0])
	}
}

// TestHandleNvidia_FirstRoundDegradesToRoundRobin 验证首轮所有账号计数==0 时,
// 最少计数优先退化为原取模轮询: 4 次连续 round-robin 请求应交替命中两个账号(非全部打到同一个)。
// 这是既有 LB 行为在改造后必须保持兼容的回归保障。
func TestHandleNvidia_FirstRoundDegradesToRoundRobin(t *testing.T) {
	var hitID []string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 用 Authorization 前缀区分账号: "Bearer key-a" / "Bearer key-b"
		mu.Lock()
		hitID = append(hitID, r.Header.Get("Authorization"))
		mu.Unlock()
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-fr", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "OK"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc1 := mkNvidiaAccount("nv-fr1", "nv-fr1@test.com", "key-a", upstream.URL, "moonshotai/kimi-k2.5")
	acc2 := mkNvidiaAccount("nv-fr2", "nv-fr2@test.com", "key-b", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc1, acc2})
	accMgr.SetNvidiaLBMode("round-robin")

	makeReq := func() {
		anthReq := &AnthropicRequest{
			Model:    "claude-sonnet-4-5",
			MaxTokens: func() *int { v := 100; return &v }(),
			Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
		}
		reqBody, _ := json.Marshal(anthReq)
		req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(reqBody))
		rr := httptest.NewRecorder()
		handler.handleNvidia(rr, req, &RelaySession{UserID: "fr-1", UserKey: "k-fr"})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}
	}

	for i := 0; i < 4; i++ {
		makeReq()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hitID) != 4 {
		t.Fatalf("expected 4 upstream calls, got %d", len(hitID))
	}
	// 首轮退化为取模轮询: 应交替命中 key-a / key-b, 不应全部相同。
	if hitID[0] == hitID[1] || hitID[1] == hitID[2] {
		t.Fatalf("first-round should degrade to round-robin alternating, got: %v", hitID)
	}
}

// TestOpenAIChatSSEToAnthropicSSE_AnthropicUsageCompliance 验证回译流的 usage / message_stop
// payload 严格对齐 Anthropic 官方流式协议，对应改动：
//   - message_start.usage.output_tokens 初值为 1（官方惯例至少 1）；
//   - message_delta.usage.{input_tokens,output_tokens} 用上游末帧的累计真实值
//     （官方标注 "token counts shown in the usage field of the message_delta event are cumulative"）；
//   - message_stop 的 data 必须为 {"type":"message_stop"}，而非早期 "{}"。
// 这是对 "Claude Code CLI 等下次请求才整条显示" 的协议合规修正的回归保护。
func TestOpenAIChatSSEToAnthropicSSE_AnthropicUsageCompliance(t *testing.T) {
	sseInput := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":42,\"total_tokens\":142}}\n\n" +
		"data: [DONE]\n\n"

	reader := strings.NewReader(sseInput)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	in, out, err := OpenAIChatSSEToAnthropicSSE(context.Background(), reader, nil, bw, "z-ai/glm-5.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bw.Flush()
	out_str := buf.String()

	// 1) message_start.usage.output_tokens 初值应为 1
	if !strings.Contains(out_str, `"output_tokens":1`) {
		t.Errorf("message_start 应含 output_tokens:1 (官方惯例初值), stream:\n%s", out_str)
	}

	// 2) message_delta.usage 用累计真实值: output_tokens=42, input_tokens=100
	//    json.Marshal(map) 字段顺序不固定，故解析 message_delta 的 data 行而非字符串匹配。
	{
		idx := strings.Index(out_str, "event: message_delta\n")
		if idx < 0 {
			t.Fatalf("missing message_delta event, stream:\n%s", out_str)
		}
		dataIdx := strings.Index(out_str[idx:], "data: ")
		if dataIdx < 0 {
			t.Fatalf("missing message_delta data, stream:\n%s", out_str)
		}
		dataStart := idx + dataIdx + len("data: ")
		dataEnd := strings.Index(out_str[dataStart:], "\n")
		if dataEnd < 0 {
			t.Fatalf("message_delta data 未闭合, stream:\n%s", out_str)
		}
		deltaJSON := out_str[dataStart : dataStart+dataEnd]
		var parsed struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason   string `json:"stop_reason"`
				StopSequence *string `json:"stop_sequence"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(deltaJSON), &parsed); err != nil {
			t.Fatalf("message_delta JSON 解析失败: %v, raw=%s", err, deltaJSON)
		}
		if parsed.Type != "message_delta" {
			t.Errorf("message_delta.type 期望 message_delta, 实际=%q", parsed.Type)
		}
		if parsed.Delta.StopReason != "end_turn" {
			t.Errorf("message_delta.delta.stop_reason 期望 end_turn, 实际=%q", parsed.Delta.StopReason)
		}
		if parsed.Usage.InputTokens != 100 {
			t.Errorf("message_delta.usage.input_tokens 期望 100(累计真实值), 实际=%d", parsed.Usage.InputTokens)
		}
		if parsed.Usage.OutputTokens != 42 {
			t.Errorf("message_delta.usage.output_tokens 期望 42(累计真实值), 实际=%d", parsed.Usage.OutputTokens)
		}
	}

	// 3) message_stop 的 data 必须是 {"type":"message_stop"}，而非 {}
	if !strings.Contains(out_str, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n") {
		t.Errorf("message_stop payload 应为 {\"type\":\"message_stop\"}, stream:\n%s", out_str)
	}
	if strings.Contains(out_str, "event: message_stop\ndata: {}\n") {
		t.Errorf("message_stop 不应再用旧 payload {}, stream:\n%s", out_str)
	}

	// 4) 回传的真实 token 计数应为上游末帧的 100/42
	if in != 100 || out != 42 {
		t.Errorf("返回 token 计数期望 in=100 out=42, 实际 in=%d out=%d", in, out)
	}
}

func TestOpenAIChatSSEToAnthropicSSE_EmptyAndWhitespace(t *testing.T) {
	t.Run("whitespace_content_preserved", func(t *testing.T) {
		// 模拟上游吐出带有换行符和空格的 delta 块
		sseInput := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\\n\\n  hello\\n\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"

		reader := strings.NewReader(sseInput)
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		_, _, err := OpenAIChatSSEToAnthropicSSE(context.Background(), reader, nil, bw, "test-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		bw.Flush()
		out := buf.String()

		if !strings.Contains(out, "content_block_start") {
			t.Errorf("expected content_block_start event in stream")
		}
		if !strings.Contains(out, "\\n\\n  hello\\n") && !strings.Contains(out, "\n\n  hello\n") {
			t.Errorf("expected whitespace/newlines preserved in text_delta, got:\n%s", out)
		}
	})

	t.Run("zero_content_block_fallback", func(t *testing.T) {
		// 模拟上游未发任何 content，直接返回 finish_reason
		sseInput := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"

		reader := strings.NewReader(sseInput)
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		_, _, err := OpenAIChatSSEToAnthropicSSE(context.Background(), reader, nil, bw, "test-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		bw.Flush()
		out := buf.String()

		if !strings.Contains(out, "message_start") {
			t.Errorf("expected message_start")
		}
		if !strings.Contains(out, "content_block_start") {
			t.Errorf("expected fallback content_block_start to avoid undefined text trim error in client")
		}
		if !strings.Contains(out, "content_block_stop") {
			t.Errorf("expected content_block_stop")
		}
		if !strings.Contains(out, "message_stop") {
			t.Errorf("expected message_stop")
		}
	})

	t.Run("tool_calls_stop_reason_override", func(t *testing.T) {
		// 模拟上游返回了 tool_calls，但 finish_reason 返回的是 "stop" 或 null
		sseInput := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"file\\\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"

		reader := strings.NewReader(sseInput)
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		_, _, err := OpenAIChatSSEToAnthropicSSE(context.Background(), reader, nil, bw, "test-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		bw.Flush()
		out := buf.String()

		if !strings.Contains(out, "\"stop_reason\":\"tool_use\"") {
			t.Errorf("expected stop_reason to be overridden to 'tool_use' when tool_calls emitted, got:\n%s", out)
		}
	})

	t.Run("upstream_sse_error_detection", func(t *testing.T) {
		// 模拟上游返回了包含 ResourceExhausted 的 SSE Error 帧
		sseInput := "data: {\"error\":{\"message\":\"ResourceExhausted: Worker local total request limit reached (48/48)\",\"type\":\"internal_server_error\",\"code\":500}}\n\n" +
			"data: [DONE]\n\n"

		reader := strings.NewReader(sseInput)
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		_, _, err := OpenAIChatSSEToAnthropicSSE(context.Background(), reader, nil, bw, "test-model")
		if err == nil {
			t.Fatalf("expected error when upstream SSE contains error frame, got nil")
		}
		if !strings.Contains(err.Error(), "ResourceExhausted") {
			t.Errorf("expected error message to contain ResourceExhausted, got: %v", err)
		}
	})
}

// ===== NVIDIA 专属模型清单过滤(handleNvidiaModels 接入全局白名单) =====

// preferredSettings 是 handleNvidiaModels 清单过滤测试专用的最小 settings mock。
// 沿用 chatcompressE2ESettings 范式:嵌入 settings.ManagerInterface 实现接口,
// 仅重写被该方法链调用的 GetNvidiaPreferredModels;其余不被调用的方法走嵌入字段(无 nil deref 风险)。
type preferredSettings struct {
	settings.ManagerInterface
	preferred []string
}

func (m *preferredSettings) GetNvidiaPreferredModels() []string {
	if m.preferred == nil {
		return []string{}
	}
	out := make([]string, len(m.preferred))
	copy(out, m.preferred)
	return out
}

// newNvidiaTestHandlerWithSettings 在 newNvidiaTestHandler 基础上注入 settingsMgr,供清单过滤测试。
func newNvidiaTestHandlerWithSettings(t *testing.T, accounts []*account.Account, pref []string) *APICompatHandler {
	t.Helper()
	accMgr := account.NewManager()
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}
	accMgr.SetNvidiaPoolMode(true)
	accMgr.SetActiveChannel("nvidia")
	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	return NewAPICompatHandler(nil, accMgr, router, nil, ut, &preferredSettings{preferred: pref}, nil)
}

// nvidiaModelsUpstream 构造一个 mock NVIDIA /v1/models 上游,返回指定 id 列表(OpenAI list 形态)。
func nvidiaModelsUpstream(t *testing.T, ids []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("upstream path = %q, want /v1/models", r.URL.Path)
		}
		var entries []string
		for _, id := range ids {
			entries = append(entries, `{"id":"`+id+`","object":"model"}`)
		}
		body := `{"object":"list","data":[` + strings.Join(entries, ",") + `]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

// dataIDs 从 handleNvidiaModels 响应体中提取 data[].id 列表(兼容 Anthropic 与 OpenAI 两种形态)。
func dataIDs(body []byte) []string {
	var res struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil
	}
	out := make([]string, 0, len(res.Data))
	for _, d := range res.Data {
		out = append(out, d.ID)
	}
	return out
}

// 用例1:清单为空 → 不过滤,返回上游全量(现状回归)。
func TestHandleNvidiaModels_EmptyPreferred_NoFilter(t *testing.T) {
	upstream := nvidiaModelsUpstream(t, []string{"meta/llama-3.3-70b-instruct", "deepseek-ai/deepseek-r1"})
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-empty", "nv-empty", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler := newNvidiaTestHandlerWithSettings(t, []*account.Account{acc}, nil)

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := dataIDs(rr.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("expected 2 models unfiltered, got %v", got)
	}
}

// 用例2:清单非空 → 仅返回命中清单的模型(路径 c OpenAI 重写)。
func TestHandleNvidiaModels_NonEmptyPreferred_Filtered(t *testing.T) {
	upstream := nvidiaModelsUpstream(t, []string{"meta/llama-3.3-70b-instruct", "deepseek-ai/deepseek-r1"})
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-filter", "nv-filter", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler := newNvidiaTestHandlerWithSettings(t, []*account.Account{acc}, []string{"meta/llama-3.3-70b-instruct"})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := dataIDs(rr.Body.Bytes())
	if len(got) != 1 || got[0] != "meta/llama-3.3-70b-instruct" {
		t.Fatalf("expected only meta/llama-3.3-70b-instruct, got %v", got)
	}
}

// 用例3:清单非空但全部不命中上游 → 路径 c 重写为空 data 但结构合法(仍是 OpenAI list)。
func TestHandleNvidiaModels_PreferredMisses_OpenAIPassthrough(t *testing.T) {
	upstream := nvidiaModelsUpstream(t, []string{"meta/llama-3.3-70b-instruct", "deepseek-ai/deepseek-r1"})
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-miss", "nv-miss", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler := newNvidiaTestHandlerWithSettings(t, []*account.Account{acc}, []string{"nonexistent/model-x"})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	// 重写后的结构仍为合法 OpenAI list
	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid json: %v body=%s", err, rr.Body.String())
	}
	if res["object"] != "list" {
		t.Errorf("expected object=list, got %v", res["object"])
	}
	got := dataIDs(rr.Body.Bytes())
	if len(got) != 0 {
		t.Fatalf("expected empty data after filter, got %v", got)
	}
}

// 用例4:Anthropic 入站(带 anthropic-version 头)+ 清单非空 → 路径 b 输出 Anthropic 形态且只含命中项。
func TestHandleNvidiaModels_Anthropic_PreferredFiltered(t *testing.T) {
	upstream := nvidiaModelsUpstream(t, []string{"meta/llama-3.3-70b-instruct", "deepseek-ai/deepseek-r1"})
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-anth", "nv-anth", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler := newNvidiaTestHandlerWithSettings(t, []*account.Account{acc}, []string{"deepseek-ai/deepseek-r1"})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Anthropic 形态:{"data":[{"type":"model","id":...}],"has_more":false}
	var res struct {
		Data []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid json: %v body=%s", err, rr.Body.String())
	}
	if res.HasMore {
		t.Errorf("expected has_more=false, got true")
	}
	if len(res.Data) != 1 || res.Data[0].Type != "model" || res.Data[0].ID != "deepseek-ai/deepseek-r1" {
		t.Fatalf("expected 1 anthropic model deepseek-ai/deepseek-r1, got %+v", res.Data)
	}
}

// 用例5:号池空(无可用账号)→ fallback 9 个兜底 ∩ 清单,只返回命中兜底项。
func TestHandleNvidiaModels_FallbackPreferredFiltered(t *testing.T) {
	// 无账号 → 走 buildFallbackNvidiaModels
	handler := newNvidiaTestHandlerWithSettings(t, nil, []string{"meta/llama-3.3-70b-instruct", "moonshotai/kimi-k2.5"})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := dataIDs(rr.Body.Bytes())
	// fallback 9 个里命中这两个的恰好 2 个
	if len(got) != 2 {
		t.Fatalf("expected 2 fallback models matching preferred, got %v", got)
	}
	want := map[string]bool{"meta/llama-3.3-70b-instruct": true, "moonshotai/kimi-k2.5": true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected fallback id after filter: %s", id)
		}
	}
}

// 用例6:settingsMgr 为 nil(现有 newNvidiaTestHandler 范式)→ 不过滤、不 panic(回写全量)。
func TestHandleNvidiaModels_NilSettingsMgr_NoPanic(t *testing.T) {
	upstream := nvidiaModelsUpstream(t, []string{"meta/llama-3.3-70b-instruct", "deepseek-ai/deepseek-r1"})
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-nil", "nv-nil", "k", upstream.URL, "moonshotai/kimi-k2.5")
	// 原始 helper 传 settingsMgr=nil,验证守护逻辑不 panic 且不过滤
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	req := httptest.NewRequest(http.MethodGet, "/nvidia/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.handleNvidiaModels(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := dataIDs(rr.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("expected 2 models unfiltered (nil settingsmgr), got %v", got)
	}
}

// ===== 上游断流(unexpected EOF)服务端蓄流重试用例 =====
//
// 以下用例覆盖 writeNvidiaAnthropicStream 的蓄流回放重试链路(pullAnthropicStreamWithRetry):
//   - 前 N 次返回中途断流(unexpected EOF)、第 N+1 次完整 → 重试命中并回放完整 Anthropic SSE;
//   - 全程不换号(同一 poolAccount 全程复用);
//   - 单次退避用 h.nvidiaStreamRetryWait,测试覆盖为 5ms,避免 5s×N 拖垮单测;
//   - 重试用尽 → 回写 Anthropic overloaded_error(503),而非旧的 end_turn 假闭合;
//   - 客户端 ctx 取消 → 立即终止重试,不空跑退避。

// flakyNvidiaUpstream 构造一个可控的 mock NVIDIA 上游:用 failN 控制前若干次请求返回
// "上游内嵌 SSE error chunk"({"error":{"message":...}}),自第 failN+1 次起返回完整合法 SSE。
//
// 为何用 SSE error chunk 而非真 TCP unexpected EOF 重现断流:openAIChatSSEToAnthropicSSEInto 对
// 上游 SSE error chunk 会置 err 并 break、不置 streamTerminated,重试主体据此判定"本流不完整需重试"。
// 生产环境的 unexpected EOF 同样使 scanner.Err() 非 nil 走同一条"不完整→重试"判定——两条路径在
// 重试判定处等价,故用稳定可控的 SSE error chunk 复现"上游故障应重试"语义,不依赖 HTTP 层断流细节
// (真 TCP 半截 chunked 关闭复现脆弱、跨平台不稳)。
// 返回 server 与已发生请求次数指针,供断言"重试了几次"。
func flakyNvidiaUpstream(t *testing.T, failN int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	errorChunk := strings.Join([]string{
		`data: {"error":{"message":"upstream interrupted","type":"internal","code":"stream_broken"}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	completeSSE := strings.Join([]string{
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&calls, 1)
		if int(cur) <= failN {
			// 故障:上游内嵌 SSE error chunk(非完整正常流),触发重试主体的"不完整→重试"判定。
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(errorChunk))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(completeSSE))
	}))
	return srv, &calls
}

// draftChunkLines 构造"先发若干正文增量(draft)+ 再发 error chunk 触发断流"的上游 SSE 字节流。
// 用于模拟生产环境"上游吐了一截正文后中途断流"的真实形态——区别于 flakyNvidiaUpstream 的纯 error chunk
// (零正文断流)。bodyFragments 逐个作为 text chunk 的 content 发出,最后追加 error chunk + [DONE]。
// translator 对 error chunk 路径:已吐正文时直接 break,循环外 closeAll 闭合已开的 text 块(落 live stop),
// message_delta/message_stop 只 replay 不推 live,首轮断流后 live 上草稿段为"已闭合的完整 text 块"。
func draftChunkLines(bodyFragments []string) string {
	parts := make([]string, 0, len(bodyFragments)+2)
	for _, frag := range bodyFragments {
		parts = append(parts, `data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":`+ jsonString(frag) +`}}]}`)
	}
	parts = append(parts, `data: {"error":{"message":"upstream interrupted mid-stream","type":"internal","code":"stream_broken"}}`)
	parts = append(parts, `data: [DONE]`, "")
	return strings.Join(parts, "\n\n")
}

// completeChunkLines 构造"完整正常收尾(含 finish_reason + usage + [DONE])"的上游 SSE 字节流。
// body 为最终正文(与首轮草稿不同以验证"重启段正文与草稿不同")。
func completeChunkLines(body string) string {
	return strings.Join([]string{
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":` + jsonString(body) + `}}]}`,
		`data: {"id":"1","model":"z-ai/glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
}

// draftChunkLines / completeChunkLines 内联拼接 JSON 字符串字面量时复用 sse_payload.go 的
// jsonString(v interface{}):对 string 入参,json.Marshal 产出带引号的 JSON 字符串字面量,
// 与原测试本地 helper 行为逐字节等价 —— 该 helper 已收口到包级 jsonString,不再单独定义。

// flakyNvidiaUpstreamWithDraft 构造可控 mock:前 failN 次请求返回"先吐 draftFragments 草稿正文再断流"
// (模拟生产"吐了一截后断流"),自第 failN+1 次起返回完整正文 completeBody(与草稿不同)。
// 返回 server 与已发生请求次数指针。本 helper 用于验证"草稿段 + 重启段"端到端续传不重发。
func flakyNvidiaUpstreamWithDraft(t *testing.T, failN int, draftFragments []string, completeBody string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	draftSSE := draftChunkLines(draftFragments)
	completeSSE := completeChunkLines(completeBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if int(cur) <= failN {
			_, _ = w.Write([]byte(draftSSE))
			return
		}
		_, _ = w.Write([]byte(completeSSE))
	}))
	return srv, &calls
}

// TestPullAnthropicStream_RetryOnEOFThenSuccess 断流重试命中:前 2 次中途断流,第 3 次完整。
// 断言:客户端收 200 + 完整 Anthropic SSE 事件序列 + 文本 delta, 且全程未换号(同一账号)。
func TestPullAnthropicStream_RetryOnEOFThenSuccess(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 2)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-retry", "retrybot@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond // 加速退避,避免 5s×N 拖垮单测

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	start := time.Now()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-retry", UserKey: "k-retry"})
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry success, got %d body=%s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	for _, ev := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(out, "event: "+ev) {
			t.Errorf("missing event %q after retry:\n%s", ev, out)
		}
	}
	if !strings.Contains(out, `"text":"Hi"`) {
		t.Errorf("expected text delta Hi from completed attempt, got:\n%s", out)
	}
	// 重试命中:必须发生过 3 次上游请求(2 次断流 + 1 次成功)。
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("expected 3 upstream calls (2 EOF + 1 success), got %d", got)
	}
	// 全程不换号 + 退避被加逽数据双校验:总耗时应远小于生产 2×5s=10s(此处用 5ms×2 退避的毫秒级)。
	if elapsed > 2*time.Second {
		t.Errorf("retry backoff not accelerated, elapsed=%v (nvidiaStreamRetryWait workaround failed)", elapsed)
	}
}

// TestPullAnthropicStream_RetryExhausted_RepliesOverloaded 5 次均中途断流,重试用尽。
// 断言:回写 503 + Anthropic overloaded_error(取代旧的 end_turn 假闭合),调用记录为 5 次。
func TestPullAnthropicStream_RetryExhausted_RepliesOverloaded(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 10) // failN 远超 5 次,确保次次断流
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-exh", "exhbot@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-exh", UserKey: "k-exh"})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 overloaded_error after retry exhausted, got %d body=%s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	if !strings.Contains(out, `"type":"overloaded_error"`) {
		t.Errorf("expected overloaded_error payload, got:\n%s", out)
	}
	// 重试用尽:恰好 5 次上游请求(不换号,同一账号重拉满 5 次)。
	if got := atomic.LoadInt32(calls); got != 5 {
		t.Errorf("expected exactly 5 upstream calls (retry max) without switch, got %d", got)
	}
}

// TestPullAnthropicLive_RetryResumesNotReplays 锁定"正文逐块实时下发 + 断流续传不重发"端到端:
//
//	前 2 轮上游吐不同草稿正文片段后中途断流(error chunk),第 3 轮完整且正文与草稿不同。
//	首轮 tee 把草稿段实时推 live(message_start + text 块 + text_delta 草稿 + closeAll 闭合的 stop),
//	重试轮 resumeSink 跳过 message_start、惰性补闭合(liveBodyOpenIdx 已被 closeAll 清为 -1 故无草稿块待补)、
//	新正文块分配 index=liveMaxUsedIdx+1=1、提交重启段 pending 落 live。
//	最后 replayFollowingInto 据 resume 快照跳过已 live 的草稿块(start/delta/stop 全跳)、只补 message_delta+message_stop。
//
// 客户端最终流期望:
//
//	200 + message_start 恰好 1 个 +
//	草稿段(cbs text idx=0 + text_delta 草稿 + cbs_stop 0,已闭合)+
//	重启段(cbs text idx=1 + text_delta 完整正文 + cbs_stop 1)+
//	message_delta + message_stop。
//	无重复 message_start、无 index 冲突(0 与 1 各一次)、草稿与重启正文不同(续传不重发)。
func TestPullAnthropicLive_RetryResumesNotReplays(t *testing.T) {
	// 前 2 轮断流且吐不同草稿片段;第 3 轮完整,正文与草稿不同。
	upstream, calls := flakyNvidiaUpstreamWithDraft(t, 2,
		[]string{"DRAFT_A_", "PART2_"}, // 第 1 轮草稿(第 2 轮草稿也为 draftSSE 同样内容,但只取第 1 轮落 live)
		"FINAL_FULL_ANSWER", // 第 3 轮完整正文(重启段)
	)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-resume", "resumebot@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond // 加速退避

	anthReq := &AnthropicRequest{
		Model:  "claude-sonnet-4-5",
		Stream: true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-resume", UserKey: "k-resume"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after resumed retry success, got %d body=%s", rr.Code, rr.Body.String())
	}
	events := parseSSEEvents(rr.Body.String())

	// message_start 恰好 1 个(首轮发 1 个,重试轮全跳,replayFollowingInto 也跳)。
	msCount := 0
	for _, ev := range events {
		if ev.event == "message_start" {
			msCount++
		}
	}
	if msCount != 1 {
		t.Fatalf("message_start 应恰好 1 个(续传不重发),实际=%d 流=%v", msCount, eventNames(events))
	}

	// message_stop 恰好 1 个(replayFollowingInto 补发一次,首轮/重试轮都未推 live)。
	stopCount := 0
	for _, ev := range events {
		if ev.event == "message_stop" {
			stopCount++
		}
	}
	if stopCount != 1 {
		t.Fatalf("message_stop 应恰好 1 个,实际=%d 流=%v", stopCount, eventNames(events))
	}

	// content_block_start(text) 的 index 集合应恰好为 {0, 1}(草稿段 idx 0 + 重启段 idx 1),无重复、无冲突。
	var startIdx []int
	textStartCount := 0
	for _, ev := range events {
		if ev.event != "content_block_start" {
			continue
		}
		m := dataMap(t, ev)
		cb, _ := m["content_block"].(map[string]interface{})
		if cb == nil || cb["type"] != "text" {
			continue
		}
		textStartCount++
		if v, ok := m["index"].(float64); ok {
			startIdx = append(startIdx, int(v))
		}
	}
	if textStartCount != 2 {
		t.Fatalf("text 块 start 应恰好 2 个(草稿段 + 重启段),实际=%d 流=%v", textStartCount, eventNames(events))
	}
	if !equalIntSlice(startIdx, []int{0, 1}) {
		t.Fatalf("text 块 index 序列应为 [0,1](草稿 idx 0 + 重启 idx 1 单调无冲突),实际=%v", startIdx)
	}

	// 草稿段正文与非草稿正文均应出现,且二者不同(续传:重启段不重发草稿)。
	var allText string
	for _, ev := range events {
		if ev.event != "content_block_delta" {
			continue
		}
		dm := dataMap(t, ev)
		delta, _ := dm["delta"].(map[string]interface{})
		if delta == nil || delta["type"] != "text_delta" {
			continue
		}
		if s, ok := delta["text"].(string); ok {
			allText += s
		}
	}
	if !strings.Contains(allText, "DRAFT_A_PART2_") {
		t.Fatalf("草稿段正文应出现在最终流(草稿段实时下发),实际 allText=%q", allText)
	}
	if !strings.Contains(allText, "FINAL_FULL_ANSWER") {
		t.Fatalf("重启段完整正文应出现在最终流,实际 allText=%q", allText)
	}
	if strings.Contains(allText, "DRAFT_A_PART2_FINAL_FULL_ANSWER") {
		// 草稿与重启正文拼成连续串属正常(两段邻接),但若是同一串被重复回放则异常——此处分隔不同的片段,允许邻接。
	}

	// 每个 text 块 index 都应有对应 content_block_stop(完整闭合,无"开块未闭合")。
	startedIdx := map[int]bool{}
	for _, ev := range events {
		if ev.event == "content_block_start" {
			m := dataMap(t, ev)
			cb, _ := m["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "text" {
				if v, ok := m["index"].(float64); ok {
					startedIdx[int(v)] = true
				}
			}
		}
	}
	for _, ev := range events {
		if ev.event != "content_block_stop" {
			continue
		}
		if v, ok := dataMap(t, ev)["index"].(float64); ok {
			delete(startedIdx, int(v))
		}
	}
	if len(startedIdx) != 0 {
		t.Fatalf("存在未闭合的 text 块 index=%v(应全闭合),流=%v", startedIdx, eventNames(events))
	}

	// 重试命中:恰好 3 次上游请求(2 次断流草稿 + 1 次完整)。
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("expected 3 upstream calls (2 draft-broken + 1 complete), got %d", got)
	}
}

// ===== 直连蓄流重试耗尽后接兜底出站代理(pullAnthropicStreamWithRetry 兜底分支) =====
//
// 以下用例覆盖 NVIDIA Anthropic 流式链路在直连 5s×5 重试全部耗尽后,切换兜底出站代理再 1 轮的集成行为:
//   - 兜底成功:直连 5 轮断流,兜底轮完整 → 200 + 完整 Anthropic SSE,calls==6(5 直连 + 1 兜底);
//   - 兜底也失败:连同兜底轮共 6 次全断流 → 503 overloaded_error,calls==6;
//   - 启用但地址协议不支持(ftp://):GetFallbackClient 返回 err 跳过兜底 → 503,calls==5(只走直连);
//   - 未启用:enabled=false 直接跳过兜底 → 503,calls==5;
//   - 无 settingsMgr:守护 nil 不 panic,走直连耗尽 → 链路与未启用等价(已在现有用例覆盖,不重复)。
//
// 验证物理事实依据:Go http client 设置 Proxy=上游 server 自身 URL 时,Do 请求会以绝对 URI 形式发出,
// httptest server 能正确解析 r.URL.Path 到 handler(已通过独立探测 TestProxySelfProbe 确认)。
// 故 FallbackProxyAddress = upstream.URL 即可用作零额外 server 的"兜底成功转发"e2e 探针:
// 兜底轮 fbClient.Do 经 proxy 协议把请求再发一次给同一上游 server,handler 无感知区分。

// fallbackSettings 是兜底代理测试专用的最小 settings mock。
// 与 preferredSettings 不同:本测试走 handleNvidia 完整路径,链调 debugger getter 等多个方法,
// 若嵌入 nil 接口会让未重写方法落到 nil 上 panic(autogenerated)。故嵌入一个真实
// settings.NewManager()(零值 Config,所有 getter 返零值/不崩),仅重写 4 个兜底 getter
// 指向测试字段、GetNvidiaPreferredModels 返回空切片(保持与 preferredSettings 同款行为)。
// handleNvidia 全路径对 settings 的链调均为只读 getter,不触发 Manager 的 SaveConfig/写文件。
type fallbackSettings struct {
	*settings.Manager
	fbAddr    string
	fbEnabled bool
	fbUser    string
	fbPass    string
}

func (m *fallbackSettings) GetFallbackProxyAddress() string { return m.fbAddr }
func (m *fallbackSettings) GetFallbackProxyEnabled() bool   { return m.fbEnabled }
func (m *fallbackSettings) GetFallbackProxyUsername() string { return m.fbUser }
func (m *fallbackSettings) GetFallbackProxyPassword() string { return m.fbPass }
func (m *fallbackSettings) GetNvidiaPreferredModels() []string { return []string{} }

// newNvidiaTestHandlerWithFallback 在 newNvidiaTestHandler 基础上注入兜底 settings mock。
func newNvidiaTestHandlerWithFallback(t *testing.T, accounts []*account.Account, fbAddr string, fbEnabled bool) *APICompatHandler {
	t.Helper()
	accMgr := account.NewManager()
	for _, a := range accounts {
		accMgr.AddAccount(a)
	}
	accMgr.SetNvidiaPoolMode(true)
	accMgr.SetActiveChannel("nvidia")
	router := session.NewRouter()
	ut := stats.NewUsageTracker(pricing.NewManager())
	sm := &fallbackSettings{Manager: settings.NewManager(), fbAddr: fbAddr, fbEnabled: fbEnabled}
	return NewAPICompatHandler(nil, accMgr, router, nil, ut, sm, nil)
}

// TestPullAnthropicStream_FallbackRoundSucceeds 直连 5 轮全断流,兜底轮完整成功。
// 断言:200 + 完整 Anthropic SSE 事件序列 + 文本 delta + calls==6(5 直连 + 1 兜底)。
func TestPullAnthropicStream_FallbackRoundSucceeds(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 5) // call 1-5 断流、call 6(兜底)完整
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-fb-ok", "fbbot@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	// 兜底地址指向上游自身:fbClient.Do 经 proxy 协议把请求再发回同一 server(探测已验证可行),兜底轮拿到完整流。
	handler := newNvidiaTestHandlerWithFallback(t, []*account.Account{acc}, upstream.URL, true)
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-fb-ok", UserKey: "k-fb-ok"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after fallback round success, got %d body=%s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	for _, ev := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(out, "event: "+ev) {
			t.Errorf("missing event %q after fallback success:\n%s", ev, out)
		}
	}
	if !strings.Contains(out, `"text":"Hi"`) {
		t.Errorf("expected text delta Hi from fallback completed round, got:\n%s", out)
	}
	// 5 轮直连断流 + 1 轮兜底成功 = 恰好 6 次上游请求。
	if got := atomic.LoadInt32(calls); got != 6 {
		t.Errorf("expected 6 upstream calls (5 direct EOF + 1 fallback success), got %d", got)
	}
}

// TestPullAnthropicStream_FallbackAlsoFails_RepliesOverloaded 直连 5 轮 + 兜底 1 轮全断流。
// 断言:503 overloaded_error + calls==6(兜底被触达但同样失败),不换号。
func TestPullAnthropicStream_FallbackAlsoFails_RepliesOverloaded(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 10) // 持续断流,兜底轮也击中 error chunk
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-fb-fail", "fbfail@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler := newNvidiaTestHandlerWithFallback(t, []*account.Account{acc}, upstream.URL, true)
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-fb-fail", UserKey: "k-fb-fail"})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 overloaded after fallback failed, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"type":"overloaded_error"`) {
		t.Errorf("expected overloaded_error payload after fallback also failed, got:\n%s", rr.Body.String())
	}
	// 5 轮直连 + 1 轮兜底(同样失败)= 6 次,calls 必须 == 6 证明兜底分支确实被触达而非跳过。
	if got := atomic.LoadInt32(calls); got != 6 {
		t.Errorf("expected 6 upstream calls (5 direct + 1 fallback failed), got %d (fallback branch may be skipped)", got)
	}
}

// TestPullAnthropicStream_FallbackInvalidAddressSkipped 启用兜底但地址协议不支持(ftp://)。
// GetFallbackClient 返回 err → 跳过兜底 → 503 overloaded。断言 calls==5(只走直连,兜底未触达)且不崩。
func TestPullAnthropicStream_FallbackInvalidAddressSkipped(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 10)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-fb-bad", "fbbad@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler := newNvidiaTestHandlerWithFallback(t, []*account.Account{acc}, "ftp://1.2.3.4:21", true)
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-fb-bad", UserKey: "k-fb-bad"})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when fallback addr invalid (skipped), got %d body=%s", rr.Code, rr.Body.String())
	}
	// 兜底被跳过(地址无效):只走 5 轮直连,calls==5。
	if got := atomic.LoadInt32(calls); got != 5 {
		t.Errorf("expected 5 upstream calls (fallback skipped due to invalid addr), got %d", got)
	}
}

// TestPullAnthropicStream_FallbackDisabledSkipped 配置了地址但 enabled=false。
// 守护"未启用即不触达兜底"语义:calls==5(只走直连)+ 503 overloaded。
func TestPullAnthropicStream_FallbackDisabledSkipped(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 10)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-fb-off", "fboff@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler := newNvidiaTestHandlerWithFallback(t, []*account.Account{acc}, upstream.URL, false)
	handler.nvidiaStreamRetryWait = 5 * time.Millisecond

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-fb-off", UserKey: "k-fb-off"})

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when fallback disabled, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"type":"overloaded_error"`) {
		t.Errorf("expected overloaded_error payload, got:\n%s", rr.Body.String())
	}
	if got := atomic.LoadInt32(calls); got != 5 {
		t.Errorf("expected 5 upstream calls (fallback disabled), got %d", got)
	}
}

// TestPullAnthropicStream_FirstFlakyThenSuccess_NoAccountSwitch 隐式校验"不换号":
// 用唯一可用账号(池中仅 1 个),断流重试全程只能复用它。若重试换号,换号循环会因无其它账号而提前 502。
// 这里与 RetryOnEOFThenSuccess 组合,确证不换号语义(同账号重试满 5 次/calls)。已在 exhaust 用例体现。
func TestPullAnthropicStream_ClientCancelAbortsRetry(t *testing.T) {
	upstream, calls := flakyNvidiaUpstream(t, 10) // 一直断流
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-cancel", "cancelbot@nexusquantum.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	handler.nvidiaStreamRetryWait = 300 * time.Millisecond // 中等退避便于观察取消时机

	anthReq := &AnthropicRequest{
		Model:   "claude-sonnet-4-5",
		Stream:  true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body)).WithContext(ctx)

	// 另起协程跑请求(蓄流重试会阻塞退避),协程外稍后取消 ctx,验证重试立即终止而非空跑满 5×300ms。
	done := make(chan struct{})
	var rr *httptest.ResponseRecorder
	go func() {
		rec := httptest.NewRecorder()
		rr = rec
		handler.handleNvidia(rec, req, &RelaySession{UserID: "u-cancel", UserKey: "k-cancel"})
		close(done)
	}()
	// 等首轮断流 + 进入第一次退避后取消(首轮很快,50ms 足够覆盖首轮 EOF + 落到退避 select)。
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("client cancel did not abort retry within 3s (backoff loop did not honor ctx)")
	}
	// 取消后上游请求次数应远小于 5 次(理想为 1~2 次),证明重试在 ctx 取消时立即停止。
	if got := atomic.LoadInt32(calls); got >= 5 {
		t.Errorf("client cancel should stop further retries, but %d upstream calls occurred", got)
	}
	// 客户端取消后不应返回 200 成功流(本轮未完整),状态码非 200 即视为重试被正确终止。
	if rr != nil && rr.Code == http.StatusOK {
		t.Errorf("client-cancelled stream should not surface as 200 success, got %d", rr.Code)
	}
}

// TestVCRoute 验证 /vc 别名前缀路由能够正确转发至 NVIDIA 处理链路,与 /nvidia 等价。
//
// /vc 是 /nvidia 的纯别名前缀,经 compat.go:194 的 nvidiaAliasPrefixMatch 收敛后分发到同一
// handleNvidia。本用例覆盖三条端到端正向链路(GET models / POST messages / POST chat completions),
// 确保 /vc/* 在选号→翻译→上游→回译全链路与 /nvidia/* 行为一致。
func TestVCRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"z-ai/glm-5.2"}]}`))
			return
		}
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-vc", Model: "z-ai/glm-5.2",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "Hello from VC NVIDIA"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-vc", "vc@test.cloud", "k-vc", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 1. GET /vc/v1/models
	recModels := httptest.NewRecorder()
	reqModels := httptest.NewRequest(http.MethodGet, "/vc/v1/models", nil)
	handler.handleNvidia(recModels, reqModels, &RelaySession{UserID: "u-vc", UserKey: "k-vc"})
	if recModels.Code != http.StatusOK {
		t.Fatalf("GET /vc/v1/models status = %d, want 200", recModels.Code)
	}

	// 2. POST /vc/v1/messages
	recMsg := httptest.NewRecorder()
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	bodyMsg, _ := json.Marshal(anthReq)
	reqMsg := httptest.NewRequest(http.MethodPost, "/vc/v1/messages", bytesReader(bodyMsg))
	handler.handleNvidia(recMsg, reqMsg, &RelaySession{UserID: "u-vc", UserKey: "k-vc"})
	if recMsg.Code != http.StatusOK {
		t.Fatalf("POST /vc/v1/messages status = %d, want 200", recMsg.Code)
	}

	// 3. POST /vc/v1/chat/completions
	recChat := httptest.NewRecorder()
	chatReq := &OpenAIChatRequest{
		Model:    "z-ai/glm-5.2",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	bodyChat, _ := json.Marshal(chatReq)
	reqChat := httptest.NewRequest(http.MethodPost, "/vc/v1/chat/completions", bytesReader(bodyChat))
	handler.handleNvidia(recChat, reqChat, &RelaySession{UserID: "u-vc", UserKey: "k-vc"})
	if recChat.Code != http.StatusOK {
		t.Fatalf("POST /vc/v1/chat/completions status = %d, want 200", recChat.Code)
	}
}

// TestVCRoute_EmptyPool_503 覆盖 /vc 别名链路空池兜底:号池无可用账号时回 503 nvidia_pool_empty,
// 与 /nvidia 链路唯一可观测差异仅在入站前缀(Path 落 logCtx.Path),选号/兜底逻辑共用一处。
func TestVCRoute_EmptyPool_503(t *testing.T) {
	handler, _, _, _ := newNvidiaTestHandler(t, nil)

	rec := httptest.NewRecorder()
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/vc/v1/messages", bytesReader(body))
	handler.handleNvidia(rec, req, &RelaySession{UserID: "u-vcempty", UserKey: "k-vcempty"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/vc 空池 status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

// TestVCRoute_UnknownEndpoint_404 覆盖 /vc 别名链路打错端点:回 404 且文案含 /vc alias 提示,
// 让客户端知道 /vc 别名可用、仅是该端点不存在(locks nvidia.go 404 文案泛化)。
func TestVCRoute_UnknownEndpoint_404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream call for 404 case: %s", r.URL.Path)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-vc404", "vc404@test.cloud", "k-vc404", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	rec := httptest.NewRecorder()
	// 空正文也无所谓——选号在 endpoint 判定之后,404 前置返回不触达 body 读取(端点判定在读 body 前)。
	req := httptest.NewRequest(http.MethodPost, "/vc/v1/foo", bytesReader([]byte("{}")))
	handler.handleNvidia(rec, req, &RelaySession{UserID: "u-vc404", UserKey: "k-vc404"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/vc/v1/foo status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/vc") {
		t.Fatalf("/vc 404 文案应含 /vc alias 提示,实际=%s", rec.Body.String())
	}
}


