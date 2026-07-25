package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
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

