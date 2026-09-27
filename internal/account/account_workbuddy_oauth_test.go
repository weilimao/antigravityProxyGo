package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkBuddyOAuth_SuccessFlow(t *testing.T) {
	var pollCount int32
	var expectedState = "test-state-uuid-12345"
	var expectedAccessToken = "test-access-token-jwt"
	var expectedRefreshToken = "test-refresh-token-jwt"
	var expectedUID = "wb_user_999"
	var expectedNickname = "极速开发者"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"state":   expectedState,
					"authUrl": "https://www.workbuddy.ai/login?platform=workbuddy-ai&state=" + expectedState,
				},
			})

		case "/v2/plugin/auth/token":
			count := atomic.AddInt32(&pollCount, 1)
			if count < 3 {
				// 前两次返回 11217: 登录中
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"code": WorkBuddyRetryCode,
					"msg":  "11217:login ing...",
				})
			} else {
				// 第三次返回 code 0: 成功获取 Token
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"code": 0,
					"msg":  "OK",
					"data": map[string]interface{}{
						"accessToken":  expectedAccessToken,
						"refreshToken": expectedRefreshToken,
						"expiresIn":    2592000,
						"tokenType":    "bearerToken",
					},
				})
			}

		case "/v2/plugin/login/account":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+expectedAccessToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"uid":      expectedUID,
					"nickname": expectedNickname,
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		server.Close()
	})

	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)

	oauthMgr := NewWorkBuddyOAuthManager(mgr)
	oauthMgr.SetAuthBaseURL(server.URL)
	oauthMgr.SetPollInterval(20 * time.Millisecond)
	oauthMgr.SetTimeout(3 * time.Second)

	var callbackCalled atomic.Bool
	oauthMgr.SetOnSuccess(func(acc *Account) {
		callbackCalled.Store(true)
	})

	sess, err := oauthMgr.StartLogin(context.Background(), "5.5.2")
	if err != nil {
		t.Fatalf("StartLogin failed: %v", err)
	}

	if sess.State != expectedState {
		t.Errorf("expected state %s, got %s", expectedState, sess.State)
	}
	if sess.Status != "pending" {
		t.Errorf("expected pending status, got %s", sess.Status)
	}

	// 等待后台轮询完成
	var finalSess *WorkBuddyOAuthSession
	for i := 0; i < 50; i++ {
		time.Sleep(25 * time.Millisecond)
		s := oauthMgr.GetSession(sess.State)
		if s != nil && s.Status == "success" {
			finalSess = s
			break
		}
	}

	if finalSess == nil {
		t.Fatalf("session failed to reach success status within timeout")
	}

	if !callbackCalled.Load() {
		t.Errorf("expected onSuccess callback to be called")
	}

	if finalSess.Account == nil {
		t.Fatalf("expected session account not to be nil")
	}
	if finalSess.Account.AccessToken != expectedAccessToken {
		t.Errorf("expected token %s, got %s", expectedAccessToken, finalSess.Account.AccessToken)
	}
	if finalSess.Account.ProjectID != expectedUID {
		t.Errorf("expected uid %s, got %s", expectedUID, finalSess.Account.ProjectID)
	}
	if finalSess.Account.Email != expectedNickname {
		t.Errorf("expected nickname %s, got %s", expectedNickname, finalSess.Account.Email)
	}

	// 验证已持久化在 Manager 中
	accs := mgr.GetAccounts()
	if len(accs) != 1 {
		t.Fatalf("expected 1 account in manager, got %d", len(accs))
	}
	if accs[0].Provider != workbuddyProvider {
		t.Errorf("expected provider %s, got %s", workbuddyProvider, accs[0].Provider)
	}
}

func TestWorkBuddyOAuth_CancelFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/plugin/auth/state" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"state":   "state-cancel-test",
					"authUrl": "https://www.workbuddy.ai/login?state=state-cancel-test",
				},
			})
			return
		}
		// 轮询始终返回 11217
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": WorkBuddyRetryCode,
			"msg":  "11217:login ing...",
		})
	}))
	t.Cleanup(func() {
		server.Close()
	})

	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)

	oauthMgr := NewWorkBuddyOAuthManager(mgr)
	oauthMgr.SetAuthBaseURL(server.URL)
	oauthMgr.SetPollInterval(15 * time.Millisecond)

	sess, err := oauthMgr.StartLogin(context.Background(), "5.5.2")
	if err != nil {
		t.Fatalf("StartLogin error: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	canceled := oauthMgr.CancelLogin(sess.State)
	if !canceled {
		t.Errorf("CancelLogin returned false")
	}

	time.Sleep(30 * time.Millisecond)
	cur := oauthMgr.GetSession(sess.State)
	if cur.Status != "canceled" {
		t.Errorf("expected canceled status, got %s", cur.Status)
	}
}

func TestWorkBuddyOAuth_TimeoutFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/plugin/auth/state" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"state":   "state-timeout-test",
					"authUrl": "https://www.workbuddy.ai/login?state=state-timeout-test",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": WorkBuddyRetryCode,
			"msg":  "11217:login ing...",
		})
	}))
	t.Cleanup(func() {
		server.Close()
	})

	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)

	oauthMgr := NewWorkBuddyOAuthManager(mgr)
	oauthMgr.SetAuthBaseURL(server.URL)
	oauthMgr.SetPollInterval(10 * time.Millisecond)
	oauthMgr.SetTimeout(50 * time.Millisecond)

	sess, err := oauthMgr.StartLogin(context.Background(), "5.5.2")
	if err != nil {
		t.Fatalf("StartLogin error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	cur := oauthMgr.GetSession(sess.State)
	if cur.Status != "timeout" {
		t.Errorf("expected timeout status, got %s", cur.Status)
	}
}

func TestWorkBuddyOAuth_DomesticSuccessFlow(t *testing.T) {
	var expectedState = "domestic-state-uuid-999"
	var expectedAccessToken = "domestic-access-token-jwt"
	var expectedRefreshToken = "domestic-refresh-token-jwt"
	var expectedUID = "wb_domestic_user_666"
	var expectedNickname = "会游的鱼"

	var headerCheckPassed atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// 校验国内版专属协议头
		if r.Header.Get("X-Domain") == "www.workbuddy.cn" && r.Header.Get("X-IDE-Type") == "WorkBuddy" {
			headerCheckPassed.Store(true)
		}

		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"state":   expectedState,
					"authUrl": "https://copilot.tencent.com/login?platform=workbuddy-ai&state=" + expectedState,
				},
			})

		case "/v2/plugin/auth/token":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"accessToken":  expectedAccessToken,
					"refreshToken": expectedRefreshToken,
					"expiresIn":    2592000,
					"tokenType":    "bearerToken",
				},
			})

		case "/v2/plugin/login/account":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "OK",
				"data": map[string]interface{}{
					"uid":      expectedUID,
					"nickname": expectedNickname,
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		server.Close()
	})

	tmpDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tmpDir)

	oauthMgr := NewWorkBuddyOAuthManager(mgr)
	oauthMgr.SetDomesticAuthBaseURL(server.URL)
	oauthMgr.SetPollInterval(15 * time.Millisecond)
	oauthMgr.SetTimeout(2 * time.Second)

	var callbackCalled atomic.Bool
	var savedDomesticAcc *Account
	oauthMgr.SetOnSuccess(func(acc *Account) {
		callbackCalled.Store(true)
		savedDomesticAcc = acc
	})

	sess, err := oauthMgr.StartLoginWithEdition(context.Background(), "domestic", "5.5.6")
	if err != nil {
		t.Fatalf("StartLoginWithEdition domestic failed: %v", err)
	}

	if sess.Edition != "domestic" {
		t.Errorf("expected sess.Edition to be 'domestic', got: %s", sess.Edition)
	}
	if sess.State != expectedState {
		t.Errorf("expected state %s, got %s", expectedState, sess.State)
	}

	// 等待异步轮询完成
	time.Sleep(150 * time.Millisecond)

	cur := oauthMgr.GetSession(sess.State)
	if cur.Status != "success" {
		t.Fatalf("expected session status 'success', got '%s' (err: %s)", cur.Status, cur.ErrorMessage)
	}
	if !callbackCalled.Load() {
		t.Errorf("expected onSuccess callback to be triggered")
	}
	if !headerCheckPassed.Load() {
		t.Errorf("expected domestic headers (X-Domain, X-IDE-Type) to be passed")
	}

	if savedDomesticAcc == nil {
		t.Fatalf("savedDomesticAcc is nil")
	}
	if savedDomesticAcc.BaseURL != DefaultWorkBuddyDomesticBaseURL {
		t.Errorf("expected BaseURL %s, got %s", DefaultWorkBuddyDomesticBaseURL, savedDomesticAcc.BaseURL)
	}
	if !savedDomesticAcc.IsWorkBuddyDomestic() {
		t.Errorf("expected IsWorkBuddyDomestic() to be true")
	}
	if savedDomesticAcc.ProjectID != expectedUID {
		t.Errorf("expected ProjectID %s, got %s", expectedUID, savedDomesticAcc.ProjectID)
	}
	if savedDomesticAcc.Email != "会游的鱼 (国内版)" {
		t.Errorf("expected Email '会游的鱼 (国内版)', got '%s'", savedDomesticAcc.Email)
	}
}
