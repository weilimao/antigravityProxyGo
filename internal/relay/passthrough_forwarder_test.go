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

func (s *stubPassThroughSettings) GetRelayModelMapping() []settings.ModelMappingEntry {
	return nil
}

// stubPassThroughSettingsModelMapping 提供 RelayModelMapping(含 TargetProvider/TargetGroupID),
// 供需把入站 model 路由到 Other 号池某分组(如 other/aliyun/*)的测试使用。
type stubPassThroughSettingsModelMapping struct {
	settings.ManagerInterface
	mappings []settings.ModelMappingEntry
}

func (s *stubPassThroughSettingsModelMapping) GetRelayModelMapping() []settings.ModelMappingEntry {
	return s.mappings
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
	// resolveRoutedTarget 返回 4 值:(targetProvider, targetGroupID, targetModel, matched)。
	prov, _, tm, matched := h.resolveRoutedTarget("deepseek-chat")
	if !matched || prov != "deepseek-official" || tm != "deepseek-chat" {
		t.Errorf("exact rule mismatch: prov=%q tm=%q matched=%v", prov, tm, matched)
	}
	// 前缀规则命中,TargetModel 空则原样透传。
	prov, _, tm, matched = h.resolveRoutedTarget("deepseek-reasoner")
	if !matched || prov != "deepseek" || tm != "deepseek-reasoner" {
		t.Errorf("prefix rule mismatch: prov=%q tm=%q matched=%v", prov, tm, matched)
	}
	// 无规则命中 → matched=false。
	prov, _, tm, matched = h.resolveRoutedTarget("gpt-4o")
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

// TestPassthroughForward_ClientCancelNoCooldown 客户端主动取消请求(context.Canceled)
// 不应触发号池冷却:账号 cooldown 保持 0,而不是被 SetAccountCooldownForChannel 冻结 60s。
func TestPassthroughForward_ClientCancelNoCooldown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 客户端取消场景上游等待连接断开
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ali-1", Email: "ali@pool", Provider: "other", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Cooldowns: map[string]int64{}})

	// 用 RelayModelMapping 提供 TargetProvider=other + TargetGroupID=aliyun,
	// 使 resolveRoutedTarget 正确解析出 other 组的 groupId(仅靠 ModelRouteRule 无 TargetGroupID 字段)。
	h := &APICompatHandler{
		accountMgr: mgr,
		settingsMgr: &stubPassThroughSettingsModelMapping{mappings: []settings.ModelMappingEntry{{
			ClientModel:    "other/aliyun/deepseek-v4-flash-0731",
			TargetProvider: "other",
			TargetGroupID:  "aliyun",
			Expose:         true,
		}}},
		logFn:   func(string) {},
		client:  &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	body := `{"model":"other/aliyun/deepseek-v4-flash-0731","messages":[{"role":"user","content":"hi"}]}`

	// 构造一个启动后即取消的请求上下文,模拟客户端在请求发出前/中主动断开。
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消 → httpClient.Do 返回 context.Canceled

	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	// 关键断言:不管响应状态码如何,账号绝不能进冷却。
	acc := mgr.GetAccountByID("ali-1")
	if acc == nil {
		t.Fatal("account not found")
	}
	for cat, until := range acc.Cooldowns {
		if until > 0 {
			t.Errorf("client cancel should NOT trigger cooldown, but category %q cooldown=%d", cat, until)
		}
	}
	t.Logf("client-cancel path returned status %d, cooldowns=%v (expected all zero)", w.Result().StatusCode, acc.Cooldowns)
}

// TestPassthroughForward_ResponsesInboundRouting 入站 Responses API(/route/v1/responses, Codex)
// + 上游 OpenAI Chat SSE:响应必须回译为 Responses SSE(含 response.completed),
// 而非透传 OpenAI Chat SSE(choices+[DONE])——否则 Codex 报
// 「stream disconnected before completion: stream closed before response.completed」。
func TestPassthroughForward_ResponsesInboundRouting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello response\"}}]}\n\n"))
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
	mgr.AddAccount(&account.Account{ID: "ali-1", Email: "ali@pool", Provider: "other", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Cooldowns: map[string]int64{}})

	h := &APICompatHandler{
		accountMgr: mgr,
		settingsMgr: &stubPassThroughSettingsModelMapping{mappings: []settings.ModelMappingEntry{{
			ClientModel:    "other/aliyun/deepseek-v4-flash",
			TargetModel:    "deepseek-v4-flash",
			TargetProvider: "other",
			TargetGroupID:  "aliyun",
			Expose:         true,
		}}},
		logFn:        func(string) {},
		client:       &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	// Codex Responses 请求体:input[] 而非 messages[]。
	body := `{"model":"other/aliyun/deepseek-v4-flash","stream":true,"input":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/responses", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	out := w.Body.String()

	// 必须包含 Responses 协议的关键事件:response.created / response.completed。
	if !strings.Contains(out, "response.completed") {
		t.Errorf("responses inbound must emit response.completed, got: %s", out)
	}
	if !strings.Contains(out, "response.created") {
		t.Errorf("responses inbound must emit response.created, got: %s", out)
	}
	// 不应再透传 OpenAI Chat SSE 的 [DONE] 终止帧(那是 Chat 协议,Responses 客户端不认)。
	if strings.Contains(out, "[DONE]") {
		t.Errorf("responses inbound must NOT pass through OpenAI Chat [DONE], got: %s", out)
	}
	// 正文增量应出现在 output_text.delta 事件里。
	if !strings.Contains(out, "output_text.delta") {
		t.Errorf("content delta should be rewritten as output_text.delta, got: %s", out)
	}
}

// TestBuildPassthroughUpstreamReq_NormalizesDeveloperRole 验证透传上游请求体时,
// Codex 等新客户端带来的 "developer" 角色被归一化为 "system"。
// 这是 Other 号池(阿里云/商汤等 OpenAI 兼容端点)收到 400
// "developer is not one of ['system','assistant','user','tool','function']" 的根治点。
func TestBuildPassthroughUpstreamReq_NormalizesDeveloperRole(t *testing.T) {
	// OpenAI Chat 直解路径:首条 developer 必须折叠为 system。
	chatBody := `{"model":"deepseek-chat","messages":[{"role":"developer","content":"you are helpful"},{"role":"user","content":"hi"}]}`
	chatReq, err := buildPassthroughUpstreamReq([]byte(chatBody), "deepseek-chat", false)
	if err != nil {
		t.Fatalf("chat path err: %v", err)
	}
	if len(chatReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(chatReq.Messages))
	}
	if got := chatReq.Messages[0].Role; got != "system" {
		t.Errorf("chat path: first role = %q, want \"system\"", got)
	}
	if got := chatReq.Messages[1].Role; got != "user" {
		t.Errorf("chat path: second role = %q, want \"user\" (must be untouched)", got)
	}

	// Responses 路径(Codex /v1/responses):input[0] 为 developer 时同样折叠。
	respBody := `{"model":"gpt-5.4","input":[{"role":"developer","content":"you are helpful"},{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	respReq, err := buildPassthroughUpstreamReq([]byte(respBody), "deepseek-chat", false)
	if err != nil {
		t.Fatalf("responses path err: %v", err)
	}
	if len(respReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(respReq.Messages))
	}
	if got := respReq.Messages[0].Role; got != "system" {
		t.Errorf("responses path: first role = %q, want \"system\"", got)
	}
	if got := respReq.Messages[1].Role; got != "user" {
		t.Errorf("responses path: second role = %q, want \"user\" (must be untouched)", got)
	}

	// 已合规的 system 原样保留,不得被误改。
	okBody := `{"model":"deepseek-chat","messages":[{"role":"system","content":"s"},{"role":"tool","content":"t","tool_call_id":"x"}]}`
	okReq, err := buildPassthroughUpstreamReq([]byte(okBody), "deepseek-chat", false)
	if err != nil {
		t.Fatalf("ok path err: %v", err)
	}
	if got := okReq.Messages[0].Role; got != "system" {
		t.Errorf("ok path: role = %q, want \"system\" (untouched)", got)
	}
	if got := okReq.Messages[1].Role; got != "tool" {
		t.Errorf("ok path: role = %q, want \"tool\" (untouched)", got)
	}
}

// 保证 context 包被引用(转发器内部用 r.Context);若未来移除可删。
var _ = context.Background
