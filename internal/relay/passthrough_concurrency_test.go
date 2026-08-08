package relay

// passthrough_concurrency_test.go 锁定 passthroughForward.run(Other 号池透传)在途并发槽
// acquire/release 的严格配对,以及 handleRoutedForward defer 兜底释放:
//   - 成功路径(上游 200):pf.run 返回 res.usedAccPtr 非 nil,handleRoutedForward 的 defer 释放。
//   - 401/403 剔除换号:pf.run 内显式 Release 当前号,换号重试最终成功后 drop defer 释放新号。
//   - 客户端取消(ctx.Canceled):pf.run 直接返回,显式 Release 当前号。
//   - 多次成功:并发计数全归 0,无泄漏。
//
// 基于其它号池 grp「other/aliyun/*」映射路由到 Other 号池(对齐 TestPassthroughForward_ClientCancelNoCooldown)。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// newOtherConcurrencyHandler 构造一个喂 Other 号池(other/aliyun)映射的 handler,
// client 指向 upstream,带超时避免测试卡死。
func newOtherConcurrencyHandler(mgr *account.Manager, mappings []settings.ModelMappingEntry, ts *httptest.Server) *APICompatHandler {
	h := &APICompatHandler{
		accountMgr:  mgr,
		settingsMgr: &stubPassThroughSettingsModelMapping{mappings: mappings},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}
	h.streamClient = &http.Client{Transport: h.client.Transport, Timeout: 0}
	_ = ts
	return h
}

// TestPassthroughConcurrency_SuccessReleaseDefer 成功路径:pf.run 不 Release,
// handleRoutedForward 的 defer 在消费完 resp.Body 后释放并发槽。
func TestPassthroughConcurrency_SuccessReleaseDefer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ali-s", Email: "ali-s@pool", Provider: "other", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})

	h := newOtherConcurrencyHandler(mgr, []settings.ModelMappingEntry{{
		ClientModel: "other/aliyun/deepseek-chat", TargetModel: "deepseek-chat",
		TargetProvider: "other", TargetGroupID: "aliyun", Expose: true,
	}}, upstream)

	body := `{"model":"other/aliyun/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Result().StatusCode, w.Body.String())
	}
	// 成功路径:handleRoutedForward 的 defer 在消费完上游 body 后释放并发槽 → 计数归 0。
	if got := mgr.AccountInFlightCount("ali-s"); got != 0 {
		t.Fatalf("after success defer release, AccountInFlightCount(ali-s) = %d, want 0", got)
	}
}

// TestPassthroughConcurrency_401FailoverRelease 401 剔除换号:bad 号在 pf.run 内显式 Release,
// good 号成功后 defer 释放,两号终态皆 0。
func TestPassthroughConcurrency_401FailoverRelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	mgr.AddAccount(&account.Account{ID: "ali-bad", Email: "bad", Provider: "other", AccessToken: "bad", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})
	mgr.AddAccount(&account.Account{ID: "ali-good", Email: "good", Provider: "other", AccessToken: "good", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})

	h := newOtherConcurrencyHandler(mgr, []settings.ModelMappingEntry{{
		ClientModel: "other/aliyun/deepseek-chat", TargetModel: "deepseek-chat",
		TargetProvider: "other", TargetGroupID: "aliyun", Expose: true,
	}}, upstream)

	body := `{"model":"other/aliyun/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after failover, got %d: %s", w.Result().StatusCode, w.Body.String())
	}
	// 两号终态并发计数都必须为 0(bad 在 pf.run break 前 Release;good 在 defer 释放)。
	if got := mgr.AccountInFlightCount("ali-bad"); got != 0 {
		t.Errorf("ali-bad in-flight = %d, want 0 (must release before failover)", got)
	}
	if got := mgr.AccountInFlightCount("ali-good"); got != 0 {
		t.Errorf("ali-good in-flight = %d, want 0 (defer must release after success)", got)
	}
}

// TestPassthroughConcurrency_ClientCancelRelease 客户端取消:pf.run ctx.Canceled 分支显式 Release。
func TestPassthroughConcurrency_ClientCancelRelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 上游等待,触发客户端取消场景
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ali-c", Email: "ali-c@pool", Provider: "other", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})

	h := newOtherConcurrencyHandler(mgr, []settings.ModelMappingEntry{{
		ClientModel: "other/aliyun/deepseek-chat", TargetModel: "deepseek-chat",
		TargetProvider: "other", TargetGroupID: "aliyun", Expose: true,
	}}, upstream)

	body := `{"model":"other/aliyun/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消 → httpClient.Do 返回 context.Canceled
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})

	// 客户端取消路径:pants 内 ctx.Canceled 分支显式 Release,且 res.usedAccPtr 为 nil
	// (未赋值成功),handleRoutedForward 的 defer 不双减 → 计数归 0。
	if got := mgr.AccountInFlightCount("ali-c"); got != 0 {
		t.Errorf("ali-c in-flight after client cancel = %d, want 0 (must release on ctx.Canceled)", got)
	}
}

// TestPassthroughConcurrency_RepeatSuccessNoLeak 连续多次成功请求:终态并发计数全 0。
func TestPassthroughConcurrency_RepeatSuccessNoLeak(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[]}`))
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{ID: "ali-loop", Email: "ali-loop@pool", Provider: "other", AccessToken: "k", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})
	mgr.AddAccount(&account.Account{ID: "ali-loop2", Email: "ali-loop2@pool", Provider: "other", AccessToken: "k2", BaseURL: upstream.URL, Enabled: true, GroupID: "aliyun", GroupName: "阿里云", Formats: []string{"openai"}, Cooldowns: map[string]int64{}})

	h := newOtherConcurrencyHandler(mgr, []settings.ModelMappingEntry{{
		ClientModel: "other/aliyun/deepseek-chat", TargetModel: "deepseek-chat",
		TargetProvider: "other", TargetGroupID: "aliyun", Expose: true,
	}}, upstream)

	body := `{"model":"other/aliyun/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	const N = 5
	for i := 0; i < N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.handleRoutedForward(w, req, &RelaySession{UserKey: "t", UserID: "u"})
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d: %s", i, w.Result().StatusCode, w.Body.String())
		}
	}
	// 终态两号并发计数全 0,证明每次 acquire/release 配对无泄漏。
	if got := mgr.AccountInFlightCount("ali-loop"); got != 0 {
		t.Errorf("ali-loop in-flight after %d iters = %d, want 0", N, got)
	}
	if got := mgr.AccountInFlightCount("ali-loop2"); got != 0 {
		t.Errorf("ali-loop2 in-flight after %d iters = %d, want 0", N, got)
	}
}
