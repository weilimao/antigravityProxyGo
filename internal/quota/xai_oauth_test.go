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