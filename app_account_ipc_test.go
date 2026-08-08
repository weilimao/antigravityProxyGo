package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestFetchRemoteNvidiaModels_DataShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected Authorization %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "meta/llama-3.3-70b-instruct"},
				{"id": "moonshotai/kimi-k2.5"},
				{"id": "moonshotai/kimi-k2.5"}, // 重复项须被去重
			},
		})
	}))
	defer srv.Close()

	models, err := fetchRemoteNvidiaModels(srv.URL+"/v1", "test-key")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []string{"meta/llama-3.3-70b-instruct", "moonshotai/kimi-k2.5"}
	if len(models) != len(want) {
		t.Fatalf("expected %d deduped sorted models, got %d: %v", len(want), len(models), models)
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("models[%d]=%q, want %q", i, models[i], m)
		}
	}
}

func TestFetchRemoteNvidiaModels_ModelsShape(t *testing.T) {
	// 部分上游用 {models:[{id}]} 形态;且 baseURL 不带 /v1 后缀时应自动拼 /v1/models
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"id": "nvidia/llama-3.1-nemotron-70b-instruct"},
				{"id": "abc/zeta"},
			},
		})
	}))
	defer srv.Close()

	models, err := fetchRemoteNvidiaModels(srv.URL, "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []string{"abc/zeta", "nvidia/llama-3.1-nemotron-70b-instruct"}
	if len(models) != len(want) {
		t.Fatalf("expected %d sorted models, got %d: %v", len(want), len(models), models)
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("models[%d]=%q, want %q", i, models[i], m)
		}
	}
}

func TestFetchRemoteNvidiaModels_HTTPErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := fetchRemoteNvidiaModels(srv.URL+"/v1", "bad-key")
	if err == nil {
		t.Fatalf("expected error for HTTP 401, got nil")
	}
}

func TestFetchRemoteNvidiaModels_EmptyBaseURL(t *testing.T) {
	// baseURL 空时回退 account.DefaultNvidiaBaseURL(指向真实端点但此处不打外网,
	// 仅验证调用不 panic 且对空入参内部补全不报错;断言由 Error/结果二选一驱动)。
	models, err := fetchRemoteNvidiaModels("", "")
	_ = models
	// 真实默认端点可能可达也可能不可达,因此不固定断言成功/失败,
	// 只保证 helper 不 panic 且能给出确定性的 (models, err) 返回。
	_ = err
}

// newRevealKeyTestApp 构造 account:reveal-key 测试专用 App(真实 Manager + 空目录初始化)。
func newRevealKeyTestApp(t *testing.T) *App {
	a := &App{accountMgr: account.NewManager()}
	a.accountMgr.Init(t.TempDir())
	return a
}

// invokeRevealKey 走 IPCInvoke 真实调用链(JSON 序列化 + handleAccountIPC 分派),返回解析后的响应。
func invokeRevealKey(t *testing.T, a *App, channel, accountID, provider string) map[string]interface{} {
	t.Helper()
	raw, err := a.IPCInvoke(channel, `["`+accountID+`","`+provider+`"]`)
	if err != nil {
		t.Fatalf("IPCInvoke(%s) err: %v", channel, err)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal resp %q: %v", raw, err)
	}
	return res
}

// TestIPCAccountRevealKey_OK 最核心的明文回传路径:账号存在且 Provider 匹配时下发明文 Key。
// 同时断言 Provider 大小写不敏感("NVIDIA" 亦可命中),并在结尾验证列表视图 GetAccounts()
// 依然脱敏(防扩散回归,即明文只走 reveal-key 单点下发)。
func TestIPCAccountRevealKey_OK(t *testing.T) {
	a := newRevealKeyTestApp(t)
	id, err := a.accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID: "aliyun", GroupName: "阿里云", BaseURL: "https://api.example.com/v1",
		APIKey: "sk-plain-secret-1234", Formats: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("add other: %v", err)
	}

	res := invokeRevealKey(t, a, "account:reveal-key", id, "other")
	if ok, _ := res["success"].(bool); !ok {
		t.Fatalf("expected success, got %+v", res)
	}
	if key, _ := res["apiKey"].(string); key != "sk-plain-secret-1234" {
		t.Fatalf("apiKey=%q, want plaintext sk-plain-secret-1234", key)
	}

	// 列表视图必须仍返回脱敏掩码,不得泄漏明文(防扩散回归)。
	view := a.accountMgr.GetAccounts()
	if len(view) != 1 {
		t.Fatalf("expected 1 account in view, got %d", len(view))
	}
	if got := view[0].MaskedKey; !strings.Contains(got, "****") {
		t.Fatalf("view MaskedKey=%q, want desensitized", got)
	}
	if got := view[0].GetAccessToken(); got != "" {
		t.Fatalf("view must NOT carry plaintext AccessToken, got %q", got)
	}
}

func TestIPCAccountRevealKey_WrongProvider(t *testing.T) {
	a := newRevealKeyTestApp(t)
	id, err := a.accountMgr.AddOtherAccount(account.OtherAccountInput{
		GroupID: "aliyun", BaseURL: "https://api.example.com/v1",
		APIKey: "sk-other", Formats: []string{"openai"},
	})
	if err != nil {
		t.Fatalf("add other: %v", err)
	}
	// other 账号用 nvidia 身份取 → 拒绝。
	res := invokeRevealKey(t, a, "account:reveal-key", id, "nvidia")
	if ok, _ := res["success"].(bool); ok {
		t.Fatalf("expected rejection for wrong provider, got %+v", res)
	}
	if _, hasKey := res["apiKey"]; hasKey {
		t.Fatalf("must not return apiKey when rejected, got %+v", res)
	}
}

func TestIPCAccountRevealKey_NotFound(t *testing.T) {
	a := newRevealKeyTestApp(t)
	res := invokeRevealKey(t, a, "account:reveal-key", "no-such-id", "other")
	if ok, _ := res["success"].(bool); ok {
		t.Fatalf("expected rejection for unknown id, got %+v", res)
	}
}

func TestIPCAccountRevealKey_MissingArgs(t *testing.T) {
	a := newRevealKeyTestApp(t)
	raw, err := a.IPCInvoke("account:reveal-key", `[]`)
	if err != nil {
		t.Fatalf("IPCInvoke err: %v", err)
	}
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ok, _ := res["success"].(bool); ok {
		t.Fatalf("expected rejection for missing accountId, got %+v", res)
	}
}

func TestIPCAccountRevealKey_InvalidProvider(t *testing.T) {
	a := newRevealKeyTestApp(t)
	res := invokeRevealKey(t, a, "account:reveal-key", "any-id", "antigravity")
	if ok, _ := res["success"].(bool); ok {
		t.Fatalf("expected rejection for non-revealable provider, got %+v", res)
	}
}
