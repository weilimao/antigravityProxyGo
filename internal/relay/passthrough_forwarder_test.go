package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// stubPassThroughSettings 给透传转发器 / 路由入口喂一个最小 settings.ManagerInterface 实现，
// 让 resolveRoutedTarget 取到测试自定义的规则表(而非默认 nvidia 兜底)。
type stubPassThroughSettings struct {
	settings.ManagerInterface
	routes []settings.ModelRouteRule
}

func (s *stubPassThroughSettings) GetRelayModelRoutes() []settings.ModelRouteRule {
	out := make([]settings.ModelRouteRule, len(s.routes))
	copy(out, s.routes)
	return out
}

// newPassThroughHandler 构造一个装配了 accountMgr + 自定义 routes 的 handler,
// client 指向 ts(模拟上游),带 5s 超时以免测试卡死。
func newPassThroughHandler(t *testing.T, mgr *account.Manager, routes []settings.ModelRouteRule, ts *httptest.Server) *APICompatHandler {
	h := &APICompatHandler{
		accountMgr:  mgr,
		settingsMgr: &stubPassThroughSettings{routes: routes},
		logFn:       func(string) {},
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{ // 测试用默认 transport，避免依赖外网
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
	// streamClient 不设全局超时(模拟生产),但走同一 upstream ts。
	h.streamClient = &http.Client{Transport: h.client.Transport, Timeout: 0}
	_ = ts
	return h
}

func TestResolveRoutedTarget_CustomRules(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubPassThroughSettings{
			routes: []settings.ModelRouteRule{
				{Pattern: "deepseek-*", TargetProvider: "deepseek", TargetModel: "", Priority: 100, Enabled: true},
				{Pattern: "deepseek-chat", TargetProvider: "deepseek-official", TargetModel: "deepseek-chat", Priority: 200, Enabled: true},
			},
		},
	}

	// 精确规则优先,带 TargetModel 改写。
	prov, tm, matched := h.resolveRoutedTarget("deepseek-chat")
	if !matched || prov != "deepseek-official" || tm != "deepseek-chat" {
		t.Errorf("exact rule mismatch: prov=%q tm=%q matched=%v", prov, tm, matched)
	}
	// 前缀规则命中,TargetModel 空则原样透传。
	prov, tm, matched = h.resolveRoutedTarget("deepseek-reasoner")
	if !matched || prov != "deepseek" || tm != "deepseek-reasoner" {
		t.Errorf("prefix rule mismatch: prov=%q tm=%q matched=%v", prov, tm, matched)
	}
	// 无规则命中 → matched=false。
	prov, tm, matched = h.resolveRoutedTarget("gpt-4o")
	if matched {
		t.Errorf("expected no match for gpt-4o, got prov=%q tm=%q", prov, tm)
	}
}

// TestPassthroughForward_HappyPath 端到端:上游 200 → 透传响应体原样回写。
func TestPassthroughForward_HappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验转发器改写了 model 与鉴权头。
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		// rule "deepseek-*" 的 TargetModel 为空 → 原样透传入站 model(deepseek-reasoner)。
		if string(body["model"]) != `"deepseek-reasoner"` {
			t.Errorf("upstream model mismatch: %s", body["model"])
		}
		if r.Header.Get("Authorization") != "Bearer test-key-1" {
			t.Errorf("upstream auth header mismatch: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	// 不调 Init(避免落盘/定时器):纯内存账号,够单测用。
	addTest := func(a *account.Account) {
		// 复用 AddAccount 但它在 nil path 下 SaveAccounts 会无副作用跳过 / 定时器未启。
		mgr.AddAccount(a)
	}
	addTest(&account.Account{
		ID:          "ds-1",
		Email:       "ds-1@pool",
		Provider:    "deepseek",
		AccessToken: "test-key-1",
		BaseURL:     upstream.URL,
		Enabled:     true,
		Cooldowns:   map[string]int64{},
	})

	h := newPassThroughHandler(t, mgr, []settings.ModelRouteRule{
		{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true},
	}, upstream)

	body := `{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleRoutedForward(w, req, &RelaySession{UserKey: "tester", UserID: "u1"})

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, mustRead(resp.Body))
	}
	if !strings.Contains(mustRead(strings.NewReader(w.Body.String())), "chat.completion") {
		// 上游原样透传应包含 chat.completion
	}
	if !strings.Contains(w.Body.String(), "chat.completion") {
		t.Errorf("upstream body not passed through: %s", w.Body.String())
	}
}

// TestPassthroughForward_RetryOn429ThenSucceed 429 退避 5 次后切号,第二号成功 → 透传。
// 这里用「首号一直 429、第二号 200」验证换号链路(单号 5 次原地退避会真实 sleep,故用最快失败:首号直接返回 401
// 以跳过 sleep 直接换号,聚焦换号逻辑而非退避时序)。
func TestPassthroughForward_FailoverOn401(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// 1-2 号凭证 key 分别为 bad / good;首号 bad 返回 401 → 冷冻换号;次号 good 返回 200。
		authz := r.Header.Get("Authorization")
		if strings.Contains(authz, "bad") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[]}`))
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ds-bad", Email: "bad", Provider: "deepseek", AccessToken: "bad", BaseURL: upstream.URL, Enabled: true, Cooldowns: map[string]int64{}})
	mgr.AddAccount(&account.Account{ID: "ds-good", Email: "good", Provider: "deepseek", AccessToken: "good", BaseURL: upstream.URL, Enabled: true, Cooldowns: map[string]int64{}})

	h := newPassThroughHandler(t, mgr, []settings.ModelRouteRule{
		{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true},
	}, upstream)

	body := `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "tester", UserID: "u1"})

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after failover, got %d: %s", w.Result().StatusCode, w.Body.String())
	}
	// bad 号被冷冻:cooldown 写入。
	bad := mgr.GetAccountByID("ds-bad")
	gotCategory := bad.Cooldowns["gemini"]
	if gotCategory <= 0 {
		t.Errorf("expected bad account cooldown set after 401, got %v", bad.Cooldowns)
	}
}

// TestPassthroughForward_StreamPassthrough 流式上游:SSE 原样透传。
func TestPassthroughForward_StreamPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ds-1", Email: "ds-1", Provider: "deepseek", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, Cooldowns: map[string]int64{}})

	h := newPassThroughHandler(t, mgr, []settings.ModelRouteRule{
		{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true},
	}, upstream)

	body := `{"model":"deepseek-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "tester", UserID: "u1"})

	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected SSE [DONE] passed through, got: %s", w.Body.String())
	}
}

// TestRouteEndpoints_UnmatchedModel 命中无规则 → 404。
func TestRouteEndpoints_UnmatchedModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = r.Body.Close() }))
	defer upstream.Close()

	mgr := account.NewManager()
	// 只有默认规则(nvidia 兜底),任何模型都会命中 nvidia —— 故想测 unmatched,需清空 routes。
	h := newPassThroughHandler(t, mgr, []settings.ModelRouteRule{}, upstream)
	// 空 routes → resolveRoutedTarget 走默认 nvidia 规则会兜底命中,故此处改用「乱填注入无规则」:
	// 用一个匹配不到 deepseek-chat 的 rule 表(只匹配 x-*)。
	h.settingsMgr = &stubPassThroughSettings{routes: []settings.ModelRouteRule{{Pattern: "x-*", TargetProvider: "x", Enabled: true}}}

	body := `{"model":"deepseek-chat","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unmatched model, got %d", w.Result().StatusCode)
	}
}

func mustRead(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}

// 保证 context 包被引用(转发器内部用 r.Context);若未来移除可删。
var _ = context.Background
