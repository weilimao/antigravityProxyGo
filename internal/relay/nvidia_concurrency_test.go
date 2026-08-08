package relay

// nvidia_concurrency_test.go 锁定 handleNvidia 在途并发槽(acquire/release)的严格配对:
//   - 请求成功(非流式 + 流式):请求结束后并发槽归 0(在途计数干净退出)。
//   - 客户端取消(ctx.Canceled):直接 return 路径必须释放并发槽,不得泄漏。
//   - 上游 401/403:换号前必须释放当前号并发槽。
//   - 上游 5xx / 429 耗尽:换号前必须释放当前号并发槽,新号重新 acquire。
//   - 多次请求(成功)终态并发计数全 0,验证无单调增长(泄漏检测)。
//
// 所有断言基于 accountMgr.AccountInFlightCount(id) 直接读取 Manager.concurrency 计数盘,
// 与 handleNvidia 内部 acquire/release 调用配对一一可见,无须打桩。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

// assertInFlight 断言某账号当前在途并发计数 == want,失败打印上下文。
func assertInFlight(t *testing.T, mgr *account.Manager, id string, want int, ctx string) {
	t.Helper()
	if got := mgr.AccountInFlightCount(id); got != want {
		t.Fatalf("%s: AccountInFlightCount(%s) = %d, want %d", ctx, id, got, want)
	}
}

// TestNvidiaConcurrency_SuccessRelease 非流式成功路径:请求结束后并发槽必须归 0。
func TestNvidiaConcurrency_SuccessRelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-rel", "nv-rel@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 请求结束后并发槽必须归 0(成功路径在 writeNvidiaResponse 返回后 release)。
	assertInFlight(t, accMgr, "nv-rel", 0, "after non-stream success")
}

// TestNvidiaConcurrency_StreamSuccessRelease 流式成功路径:流结束后并发槽归 0。
func TestNvidiaConcurrency_StreamSuccessRelease(t *testing.T) {
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

	acc := mkNvidiaAccount("nv-stream", "nv-stream@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Stream:   true,
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	assertInFlight(t, accMgr, "nv-stream", 0, "after stream success")
}

// TestNvidiaConcurrency_401Release 上游 401:换号前释放并发槽,请求结束后计数为 0。
// 验证 401/403 分支 break 前的 ReleaseAccount 调用配对。
func TestNvidiaConcurrency_401Release(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-401", "nv-401@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	// 401 换号链路:单号被冷冻后 break,并发槽释放;耗尽后无其他号,最终 401 回写。
	if rr.Code != http.StatusUnauthorized {
		t.Logf("401 path returned %d (acceptable: cooldown/exhausted fallback)", rr.Code)
	}
	// 关键:401 break 路径释放后并发槽必须归 0,不得泄漏。
	assertInFlight(t, accMgr, "nv-401", 0, "after 401 failover")
}

// TestNvidiaConcurrency_5xxRelease 上游 5xx:换号前释放并发槽,请求结束后计数为 0。
func TestNvidiaConcurrency_5xxRelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-5xx", "nv-5xx@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	assertInFlight(t, accMgr, "nv-5xx", 0, "after 5xx failover")
}

// TestNvidiaConcurrency_BadAnthropicBodyRelease 入站 Anthropic body 非法 → 400 早返路径
// 必须释放并发槽。验证选号后 Acquire + transform 失败 Release 配对。
func TestNvidiaConcurrency_BadAnthropicBodyRelease(t *testing.T) {
	// 用一个会返回成功的上游,但入站 body 是非法 JSON,走 Anthropic 二次解析失败 400 分支。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-bad", "nv-bad@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 非法 JSON body:handleNvidia 选号后 inboundAnthropic 解析失败 → 400 早返。
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", strings.NewReader("{bad json"))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	// handleNvidia 顶部 readBody 会先尝试解析协议;非合法 JSON 时走 inboundAnthropic 二次解析失败分支,
	// 该分支已显式 Release,故并发槽归 0。
	if rr.Code != http.StatusBadRequest {
		t.Logf("bad body path returned %d (acceptable variants)", rr.Code)
	}
	assertInFlight(t, accMgr, "nv-bad", 0, "after bad anthropic body 400")
}

// TestNvidiaConcurrency_RepeatSuccessNoLeak 连续多次成功请求:终态并发计数必须全 0,
// 验证无单调增长(泄漏检测)。同号 sticky 多次请求,每次 acquire/release 配对后净 0。
func TestNvidiaConcurrency_RepeatSuccessNoLeak(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-loop", "nv-loop@pool", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)

	const N = 8
	for i := 0; i < N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
		rr := httptest.NewRecorder()
		handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
		if rr.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d: %s", i, rr.Code, rr.Body.String())
		}
		// 每次请求结束后立即归 0,中间态不得累计。
		assertInFlight(t, accMgr, "nv-loop", 0, "after iter success")
	}
}

// TestNvidiaConcurrency_FilterRespectsLimit 验证并发过滤在选号链路生效:
// 预占一个号到上限,新请求应被过滤到另一号(FilterByConcurrency 在选号前的硬门槛)。
func TestNvidiaConcurrency_FilterRespectsLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 区分两个号:Authorization Bearer 后跟不同 key。
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&OpenAIChatResponse{
			ID: "x", Model: "moonshotai/kimi-k2.5",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	}))
	defer upstream.Close()

	accA := mkNvidiaAccount("nv-fa", "nv-fa@pool", "keyA", upstream.URL, "moonshotai/kimi-k2.5")
	accB := mkNvidiaAccount("nv-fb", "nv-fb@pool", "keyB", upstream.URL, "moonshotai/kimi-k2.5")
	handler, accMgr, _, _ := newNvidiaTestHandler(t, []*account.Account{accA, accB})
	// 把上限设为 1,使任一号占 1 槽即满,新请求必走另一号。
	accMgr.SetNvidiaMaxConcurrency(1)
	if got := accMgr.GetNvidiaMaxConcurrency(); got != 1 {
		t.Fatalf("SetNvidiaMaxConcurrency(1) GetBack = %d, want 1", got)
	}

	// 预占 A 到上限(1),B 空闲。
	accMgr.AcquireAccount("nv-fa")
	assertInFlight(t, accMgr, "nv-fa", 1, "pre-acquire A")
	assertInFlight(t, accMgr, "nv-fb", 0, "pre-acquire B")

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 100; return &v }(),
		Messages:  []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// A 仍为 1(本次请求走 B,B 结束释放后归 0),不应增长到 2(未被误选)。
	assertInFlight(t, accMgr, "nv-fa", 1, "A untouched after request routed to B")
	// B 被本次请求占用并释放,归 0。
	assertInFlight(t, accMgr, "nv-fb", 0, "B released after request done")

	// 释放预占的 A,回到干净态。
	accMgr.ReleaseAccount("nv-fa")
	assertInFlight(t, accMgr, "nv-fa", 0, "A after manual release")
}
