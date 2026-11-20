package quota

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

func TestAntigravityOAuth_BuildAuthURLAndPKCE(t *testing.T) {
	verifier, challenge, err := generatePKCEPair()
	if err != nil {
		t.Fatalf("generatePKCEPair error: %v", err)
	}
	if len(verifier) < 43 {
		t.Fatalf("unexpected PKCE verifier length: %d", len(verifier))
	}
	sum := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != expectedChallenge {
		t.Fatalf("PKCE challenge mismatch: got %q, want %q", challenge, expectedChallenge)
	}

	state1, err := generateOAuthState()
	if err != nil {
		t.Fatalf("generateOAuthState 1 error: %v", err)
	}
	state2, err := generateOAuthState()
	if err != nil {
		t.Fatalf("generateOAuthState 2 error: %v", err)
	}
	if state1 == "" || state1 == state2 {
		t.Fatalf("expected non-empty distinct states, got %q and %q", state1, state2)
	}

	redirectURI := "http://localhost:54321/auth/callback"
	clientID, _ := getCredentials("antigravity")
	authURL, err := buildAntigravityAuthURL(antigravityOAuthAuthURL, clientID, redirectURI, state1, challenge)
	if err != nil {
		t.Fatalf("buildAntigravityAuthURL error: %v", err)
	}

	if !strings.HasPrefix(authURL, "https://accounts.google.com/o/oauth2/auth?") {
		t.Fatalf("unexpected auth endpoint prefix: %s", authURL)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse error: %v", err)
	}
	q := parsed.Query()
	checks := map[string]string{
		"access_type":           "offline",
		"client_id":             clientID,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"prompt":                "consent",
		"redirect_uri":          redirectURI,
		"response_type":         "code",
		"state":                 state1,
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}

	scopeFields := strings.Fields(q.Get("scope"))
	scopeSet := make(map[string]bool, len(scopeFields))
	for _, s := range scopeFields {
		scopeSet[s] = true
	}
	if scopeSet["openid"] {
		t.Errorf("antigravity scope must not include openid, got %v", scopeFields)
	}
	for _, required := range antigravityOAuthScopes {
		if !scopeSet[required] {
			t.Errorf("missing required scope %q in %v", required, scopeFields)
		}
	}
	if len(scopeFields) != len(antigravityOAuthScopes) {
		t.Errorf("scope count = %d, want %d (%v)", len(scopeFields), len(antigravityOAuthScopes), scopeFields)
	}
}

func TestAntigravityOAuth_DualStackLoopback(t *testing.T) {
	listeners, port, err := listenLoopbackDualStack()
	if err != nil {
		t.Fatalf("listenLoopbackDualStack error: %v", err)
	}
	defer closeListeners(listeners)

	if port <= 0 || len(listeners) == 0 {
		t.Fatalf("invalid listeners=%d port=%d", len(listeners), port)
	}

	conn4, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("IPv4 loopback dial failed: %v", err)
	}
	_ = conn4.Close()

	if len(listeners) == 2 {
		conn6, err6 := net.DialTimeout("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)), time.Second)
		if err6 != nil {
			t.Fatalf("IPv6 loopback listener present but dial failed: %v", err6)
		}
		_ = conn6.Close()
	}
}

func TestAntigravityOAuth_StartLoginEndToEndSuccess(t *testing.T) {
	var (
		mu             sync.Mutex
		tokenUserAgent string
		infoUserAgent  string
		tokenForm      url.Values
	)

	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm failed: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			mu.Lock()
			tokenUserAgent = r.Header.Get("User-Agent")
			tokenForm = r.PostForm
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TokenResponse{
				AccessToken:  "ya29.mock-access-token",
				RefreshToken: "1//mock-refresh-token",
				ExpiresIn:    3599,
				TokenType:    "Bearer",
			})
		case "/oauth2/v2/userinfo":
			if r.Header.Get("Authorization") != "Bearer ya29.mock-access-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mu.Lock()
			infoUserAgent = r.Header.Get("User-Agent")
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(UserInfo{Email: "tester@gmail.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mockGoogle.Close)

	am := NewAuthManager(nil)
	am.endpoints = oauthEndpoints{
		authURL:        mockGoogle.URL + "/o/oauth2/auth",
		tokenURL:       mockGoogle.URL + "/token",
		userInfoURL:    mockGoogle.URL + "/oauth2/v2/userinfo",
		authSuccessURL: defaultAntigravityAuthSuccessURL,
	}
	t.Cleanup(func() {
		netutil.CloseAccountTransport("oauth-login:test-code-123")
	})

	var (
		capturedChallenge string
		capturedRedirect  string
		faviconStatus     int
		callbackStatus    int
		callbackLocation  string
	)

	res, err := am.StartLogin("antigravity", func(authURL string) {
		go func() {
			parsed, parseErr := url.Parse(authURL)
			if parseErr != nil {
				t.Errorf("invalid authURL %q: %v", authURL, parseErr)
				return
			}
			q := parsed.Query()
			capturedChallenge = q.Get("code_challenge")
			capturedRedirect = q.Get("redirect_uri")
			state := q.Get("state")

			redirURL, parseRedirErr := url.Parse(capturedRedirect)
			if parseRedirErr != nil {
				t.Errorf("invalid redirect_uri %q: %v", capturedRedirect, parseRedirErr)
				return
			}

			noFollowClient := &http.Client{
				Timeout: 5 * time.Second,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			favURL := "http://" + net.JoinHostPort("127.0.0.1", redirURL.Port()) + "/favicon.ico"
			if favResp, favErr := noFollowClient.Get(favURL); favErr == nil {
				faviconStatus = favResp.StatusCode
				_ = favResp.Body.Close()
			}

			cbURL := "http://" + net.JoinHostPort("127.0.0.1", redirURL.Port()) + antigravityCallbackPath +
				"?code=" + url.QueryEscape("test-code-123") + "&state=" + url.QueryEscape(state)
			cbResp, cbErr := noFollowClient.Get(cbURL)
			if cbErr != nil {
				t.Errorf("callback GET failed: %v", cbErr)
				return
			}
			callbackStatus = cbResp.StatusCode
			callbackLocation = cbResp.Header.Get("Location")
			_ = cbResp.Body.Close()
		}()
	})
	if err != nil {
		t.Fatalf("StartLogin failed: %v", err)
	}

	if faviconStatus != http.StatusNotFound {
		t.Errorf("favicon status = %d, want 404", faviconStatus)
	}
	if callbackStatus != http.StatusFound || callbackLocation != defaultAntigravityAuthSuccessURL {
		t.Errorf("callback redirect = (%d, %q), want (302, %q)", callbackStatus, callbackLocation, defaultAntigravityAuthSuccessURL)
	}
	if res["email"] != "tester@gmail.com" || res["access_token"] != "ya29.mock-access-token" ||
		res["refresh_token"] != "1//mock-refresh-token" || res["provider"] != "antigravity" {
		t.Fatalf("unexpected login result: %#v", res)
	}

	mu.Lock()
	gotTokenUA := tokenUserAgent
	gotInfoUA := infoUserAgent
	gotForm := tokenForm
	mu.Unlock()

	if strings.Contains(strings.ToLower(gotTokenUA), "antigravity/") || !strings.HasPrefix(gotTokenUA, "Go-http-client/") {
		t.Errorf("unexpected token endpoint User-Agent: %q", gotTokenUA)
	}
	if strings.Contains(strings.ToLower(gotInfoUA), "antigravity/") || !strings.HasPrefix(gotInfoUA, "Go-http-client/") {
		t.Errorf("unexpected userinfo endpoint User-Agent: %q", gotInfoUA)
	}

	verifier := gotForm.Get("code_verifier")
	if verifier == "" {
		t.Fatal("missing code_verifier in token exchange form")
	}
	sum := sha256.Sum256([]byte(verifier))
	if gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:]); gotChallenge != capturedChallenge {
		t.Errorf("token code_verifier challenge=%q != auth code_challenge=%q", gotChallenge, capturedChallenge)
	}
	if gotForm.Get("redirect_uri") != capturedRedirect || gotForm.Get("grant_type") != "authorization_code" {
		t.Errorf("unexpected token form: %v", gotForm)
	}
}

func TestAntigravityOAuth_StateMismatchAndCancel(t *testing.T) {
	var tokenHits atomic.Int32
	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		http.Error(w, "should not be reached", http.StatusInternalServerError)
	}))
	t.Cleanup(mockGoogle.Close)

	am := NewAuthManager(nil)
	am.endpoints = oauthEndpoints{
		authURL:  mockGoogle.URL + "/o/oauth2/auth",
		tokenURL: mockGoogle.URL + "/token",
	}

	var mismatchStatus atomic.Int32
	_, err := am.StartLogin("antigravity", func(authURL string) {
		go func() {
			parsed, _ := url.Parse(authURL)
			redirURL, _ := url.Parse(parsed.Query().Get("redirect_uri"))
			badCB := "http://" + net.JoinHostPort("127.0.0.1", redirURL.Port()) + antigravityCallbackPath +
				"?code=evil-code&state=wrong-state"
			resp, reqErr := (&http.Client{Timeout: 5 * time.Second}).Get(badCB)
			if reqErr == nil {
				mismatchStatus.Store(int32(resp.StatusCode))
				_ = resp.Body.Close()
			}
			am.CancelLogin()
		}()
	})

	if err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("expected cancellation error after state mismatch, got: %v", err)
	}
	if mismatchStatus.Load() != http.StatusBadRequest {
		t.Errorf("mismatched state HTTP status = %d, want 400", mismatchStatus.Load())
	}
	if tokenHits.Load() != 0 {
		t.Errorf("token endpoint hit %d times on mismatched state, want 0", tokenHits.Load())
	}
}

func TestAntigravityOAuth_RefreshTokenNoAntigravityUA(t *testing.T) {
	var (
		mu         sync.Mutex
		gotUA      string
		gotGrant   string
		gotRefresh string
	)

	mockToken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		gotUA = r.Header.Get("User-Agent")
		gotGrant = r.PostForm.Get("grant_type")
		gotRefresh = r.PostForm.Get("refresh_token")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "ya29.refreshed-token",
			ExpiresIn:   3599,
			TokenType:   "Bearer",
		})
	}))
	t.Cleanup(mockToken.Close)

	const testAccID = "test-antigravity-refresh-acc"
	t.Cleanup(func() {
		netutil.CloseAccountTransport(testAccID)
	})

	am := NewAuthManager(nil)
	am.endpoints.tokenURL = mockToken.URL

	acc := &account.Account{
		ID:           testAccID,
		Email:        "refresh@gmail.com",
		Provider:     "antigravity",
		RefreshToken: "1//refresh-xyz",
	}
	tok, err := am.RefreshToken(acc)
	if err != nil {
		t.Fatalf("RefreshToken error: %v", err)
	}
	if tok != "ya29.refreshed-token" {
		t.Fatalf("access token = %q, want ya29.refreshed-token", tok)
	}

	mu.Lock()
	ua := gotUA
	grant := gotGrant
	rt := gotRefresh
	mu.Unlock()

	if strings.Contains(strings.ToLower(ua), "antigravity/") || !strings.HasPrefix(ua, "Go-http-client/") {
		t.Errorf("unexpected RefreshToken User-Agent: %q", ua)
	}
	if grant != "refresh_token" || rt != "1//refresh-xyz" {
		t.Errorf("unexpected refresh form params: grant=%q refresh_token=%q", grant, rt)
	}
}
