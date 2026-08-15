package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

// fakeJWT 组装一个假的三段式 JWT(header.payload.signature),payload 为给定 map。
func fakeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	rawPayload, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(rawPayload)
	return header + "." + payload + ".sig"
}

func xaiClaims(email, sub string) map[string]any {
	return map[string]any{"email": email, "sub": sub}
}

// TestXAI_Discover_Valid 验证 x.ai 端点 host/https 校验。
func TestXAI_Discover_Valid(t *testing.T) {
	if _, err := validateXAIEndpoint("https://auth.x.ai/oauth2/token"); err != nil {
		t.Fatalf("expected valid x.ai endpoint, got error: %v", err)
	}
	if _, err := validateXAIEndpoint("https://evil.com/oauth2/token"); err == nil {
		t.Fatalf("expected error for non-x.ai host")
	}
	if _, err := validateXAIEndpoint("http://auth.x.ai/oauth2/token"); err == nil {
		t.Fatalf("expected error for non-https endpoint")
	}
	if _, err := validateXAIEndpoint(""); err == nil {
		t.Fatalf("expected error for empty endpoint")
	}
}

// TestXAI_StartDeviceFlow 验证设备码请求的 client_id/scope 表单与字段透传。
func TestXAI_StartDeviceFlow(t *testing.T) {
	var gotForm urlValues
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = urlValues{r.Form}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"device_code":"dev-123","user_code":"ABC-DEF",
			"verification_uri":"https://auth.x.ai/device",
			"verification_uri_complete":"https://auth.x.ai/device?user_code=ABC-DEF",
			"expires_in":1800,"interval":5
		}`))
	}))
	defer srv.Close()

	auth := NewXAIAuth()
	dc, err := auth.RequestDeviceCode(context.Background(), srv.URL, "https://auth.x.ai/oauth2/token")
	if err != nil {
		t.Fatalf("RequestDeviceCode error: %v", err)
	}
	if dc.DeviceCode != "dev-123" || dc.UserCode != "ABC-DEF" {
		t.Errorf("unexpected device code fields: %+v", dc)
	}
	if dc.VerificationURIComplete != "https://auth.x.ai/device?user_code=ABC-DEF" {
		t.Errorf("unexpected verification_uri_complete: %q", dc.VerificationURIComplete)
	}
	if dc.TokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Errorf("unexpected token endpoint: %q", dc.TokenEndpoint)
	}
	if gotForm.Get("client_id") != xaiClientID {
		t.Errorf("unexpected client_id form: %q", gotForm.Get("client_id"))
	}
	if gotForm.Get("scope") != xaiScope {
		t.Errorf("unexpected scope form: %q", gotForm.Get("scope"))
	}
}

// urlValues 薄封装便于断言 httptest 收到的表单。
type urlValues struct{ url.Values }

// TestXAI_PollForToken 验证轮询:先 authorization_pending 再成功,email/sub 从 id_token 解析。
func TestXAI_PollForToken(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"pending"}`))
			return
		}
		idToken := fakeJWT(xaiClaims("user@x.ai", "user-sub-123"))
		body, _ := json.Marshal(map[string]any{
			"access_token": "at-1", "refresh_token": "rt-1", "id_token": idToken,
			"token_type": "Bearer", "expires_in": 21600,
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	auth := newXAIAuthForTest(time.Millisecond)
	dc := &XAIDeviceCode{
		DeviceCode: "dev-1", UserCode: "ABC",
		VerificationURI: "https://auth.x.ai/device",
		ExpiresIn:       1800,
		Interval:        1,
		TokenEndpoint:   srv.URL + "/token",
	}
	res, err := auth.PollForToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("PollForToken error: %v", err)
	}
	if res.AccessToken != "at-1" {
		t.Errorf("expected access_token at-1, got %q", res.AccessToken)
	}
	if res.Email != "user@x.ai" {
		t.Errorf("expected email user@x.ai, got %q", res.Email)
	}
	if res.Subject != "user-sub-123" {
		t.Errorf("expected sub user-sub-123, got %q", res.Subject)
	}
}

// TestXAI_PollForToken_SlowDown 验证 slow_down 后 interval 增加并最终成功。
func TestXAI_PollForToken_SlowDown(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-2","refresh_token":"rt-2","expires_in":21600}`))
	}))
	defer srv.Close()

	auth := newXAIAuthForTest(time.Millisecond)
	dc := &XAIDeviceCode{DeviceCode: "dev-2", UserCode: "AB", VerificationURI: "u", Interval: 1, TokenEndpoint: srv.URL}
	res, err := auth.PollForToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("PollForToken error: %v", err)
	}
	if res.AccessToken != "at-2" {
		t.Errorf("expected at-2, got %q", res.AccessToken)
	}
}

// TestXAI_PollForToken_AccessDenied 验证 access_denied 报错。
func TestXAI_PollForToken_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()

	auth := NewXAIAuth()
	dc := &XAIDeviceCode{DeviceCode: "dev-3", UserCode: "AB", VerificationURI: "u", TokenEndpoint: srv.URL}
	_, err := auth.PollForToken(context.Background(), dc)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected access_denied error, got: %v", err)
	}
}

// TestXAI_RefreshTokens 验证刷新表单与返回。
func TestXAI_RefreshTokens(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form.Get("grant_type") + "|" + r.Form.Get("client_id") + "|" + r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","expires_in":21600}`))
	}))
	defer srv.Close()

	auth := NewXAIAuth()
	res, err := auth.RefreshTokens(context.Background(), "old-rt", srv.URL)
	if err != nil {
		t.Fatalf("RefreshTokens error: %v", err)
	}
	if res.AccessToken != "new-at" {
		t.Errorf("expected new-at, got %q", res.AccessToken)
	}
	if res.RefreshToken != "new-rt" {
		t.Errorf("expected new-rt, got %q", res.RefreshToken)
	}
	if gotForm != "refresh_token|"+xaiClientID+"|old-rt" {
		t.Errorf("unexpected refresh form: %q", gotForm)
	}
	// 空 refresh token 报错
	if _, err := auth.RefreshTokens(context.Background(), "", srv.URL); err == nil {
		t.Fatalf("expected error for empty refresh token")
	}
}

// TestXAI_PollForToken_ContextCancel 验证 ctx 取消退出。
func TestXAI_PollForToken_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	auth := newXAIAuthForTest(time.Millisecond)
	dc := &XAIDeviceCode{DeviceCode: "dev-4", UserCode: "AB", VerificationURI: "u", Interval: 1, TokenEndpoint: srv.URL}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := auth.PollForToken(ctx, dc)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected context cancelled error, got: %v", err)
	}
}

// TestAuthManager_GetXaiLoginStatus 锁定 GetXaiLoginStatus 的状态契约:
// pending / success / error 三态必须返回 dimascope 可识别的 status 字段,
// 且 success 态必须携带 email/access_token/refresh_token/base_url/token_endpoint 供 IPC 落库。
func TestAuthManager_GetXaiLoginStatus(t *testing.T) {
	am := NewAuthManager(nil)
	am.Lock()
	am.xaiLoginStates["xai-test"] = &xaiLoginState{status: "pending"}
	am.Unlock()

	// pending
	got, err := am.GetXaiLoginStatus("xai-test")
	if err != nil {
		t.Fatalf("pending status err: %v", err)
	}
	if got["status"] != "pending" {
		t.Fatalf("pending status=%v, want pending", got["status"])
	}

	// success
	am.Lock()
	am.xaiLoginStates["xai-test"] = &xaiLoginState{status: "success", data: &xaiLoginData{
		Email: "grok@x.ai", AccessToken: "at", RefreshToken: "rt",
		BaseURL: "https://cli-chat-proxy.grok.com/v1", TokenEndpoint: "https://auth.x.ai/oauth2/token",
	}}
	am.Unlock()
	got, err = am.GetXaiLoginStatus("xai-test")
	if err != nil {
		t.Fatalf("success status err: %v", err)
	}
	if got["status"] != "success" {
		t.Fatalf("success status=%v, want success", got["status"])
	}
	for _, k := range []string{"email", "access_token", "refresh_token", "base_url", "token_endpoint"} {
		if v, _ := got[k].(string); v == "" {
			t.Fatalf("success resp missing key %q: %+v", k, got)
		}
	}

	// error
	am.Lock()
	am.xaiLoginStates["xai-test"] = &xaiLoginState{status: "error", error: "access_denied"}
	am.Unlock()
	got, err = am.GetXaiLoginStatus("xai-test")
	if err != nil {
		t.Fatalf("error status err: %v", err)
	}
	if got["status"] != "error" || got["error"] != "access_denied" {
		t.Fatalf("error resp=%+v, want {status:error, error:access_denied}", got)
	}

	// 未知 state
	if _, err := am.GetXaiLoginStatus("no-such"); err == nil {
		t.Fatalf("expected error for unknown state")
	}
}

// newAuthManagerWithGrokAccount 构造一个 AuthManager,其 accountMgr 持有单个 grok OAuth 账号,
// 用于 refreshXaiToken 的「永久失败 → 移除」集成测试。tokenEndpoint 指向测试 httptest server,
// 使 refreshXaiToken 内部 NewXAIAuth().RefreshTokens 打到可控的桩端点。
func newAuthManagerWithGrokAccount(t *testing.T, tokenEndpoint string) (*AuthManager, *account.Account, *account.Manager) {
	t.Helper()
	tempDir := t.TempDir()
	mgr := account.NewManager()
	mgr.Init(tempDir)
	t.Cleanup(func() {
		mgr.StopCooldownMonitor()
		mgr.StopTokenRefreshMonitor()
		mgr.StopGrokAuthMonitor()
	})

	acc := &account.Account{
		ID:           "grok-test-1",
		Email:        "grok-test@x.ai",
		Provider:     "grok",
		ScopeType:    "grok",
		AccessToken:  "stale-at",
		RefreshToken: "stale-rt",
		BaseURL:      "https://cli-chat-proxy.grok.com/v1",
		TokenEndpoint: tokenEndpoint,
		Enabled:      true,
		Cooldowns:     map[string]int64{},
	}
	mgr.AddAccount(acc)

	am := NewAuthManager(mgr)
	return am, acc, mgr
}

// TestRefreshXaiToken_PermanentFailure_DisablesAccount 锁定 refreshXaiToken 在命中 invalid_grant
// 等永久失败时,将账号自动标记为停用(Enabled=false),保留在号池中供用户重新授权,绝不直接物理删除。
func TestRefreshXaiToken_PermanentFailure_DisablesAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired or revoked"}`))
	}))
	defer srv.Close()

	am, acc, mgr := newAuthManagerWithGrokAccount(t, srv.URL)

	// 前置:账号确在池中且已启用。
	if mgr.GetAccountByID(acc.ID) == nil {
		t.Fatal("precondition: grok account should exist before refresh")
	}

	_, err := am.RefreshToken(acc)
	if err == nil {
		t.Fatal("expected refresh error for permanent failure, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
		t.Fatalf("expected error containing invalid_grant, got: %v", err)
	}

	// 关键断言:永久失败后账号仍保留在池中,但已被自动停用(Enabled=false)。
	gotAcc := mgr.GetAccountByID(acc.ID)
	if gotAcc == nil {
		t.Fatalf("grok account %s should NOT be removed from pool after permanent refresh failure, but it was deleted", acc.ID)
	}
	if gotAcc.Enabled {
		t.Errorf("expected grok account to be disabled (Enabled=false), but Enabled=true")
	}
	// 池内 grok 账号数量保持为 1。
	if got := len(mgr.GetRawAccountsByProvider("grok")); got != 1 {
		t.Errorf("expected 1 grok account in pool, got %d", got)
	}
}

// TestRefreshXaiToken_TransientFailure_NoRemoval 锁定 refreshXaiToken 在命中瞬时失败
// (5xx / 非 invalid_grant 类关键词)时,不移除账号,留待下个 tick 重试。
func TestRefreshXaiToken_TransientFailure_NoRemoval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"server_error","error_description":"upstream 502"}`))
	}))
	defer srv.Close()

	am, acc, mgr := newAuthManagerWithGrokAccount(t, srv.URL)

	_, err := am.RefreshToken(acc)
	if err == nil {
		t.Fatal("expected refresh error for transient failure, got nil")
	}
	// 瞬时失败不移除:账号仍在池中。
	if mgr.GetAccountByID(acc.ID) == nil {
		t.Fatalf("grok account %s should NOT be removed after transient failure, but it's gone", acc.ID)
	}
}

// TestRefreshXaiToken_Success_NoRemoval 锁定正常刷新成功时账号保留且 access_token 已更新。
func TestRefreshXaiToken_Success_NoRemoval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","id_token":"` + fakeJWT(xaiClaims("grok-test@x.ai", "sub-1")) + `","expires_in":21600}`))
	}))
	defer srv.Close()

	am, acc, mgr := newAuthManagerWithGrokAccount(t, srv.URL)

	token, err := am.RefreshToken(acc)
	if err != nil {
		t.Fatalf("expected refresh success, got: %v", err)
	}
	if token != "new-at" {
		t.Errorf("expected new access token 'new-at', got %q", token)
	}
	// 成功刷新账号保留,token 已更新。
	got := mgr.GetAccountByID(acc.ID)
	if got == nil {
		t.Fatal("grok account should still exist after successful refresh")
	}
	if got.GetAccessToken() != "new-at" {
		t.Errorf("access token should be updated to new-at, got %q", got.GetAccessToken())
	}
}