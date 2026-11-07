package relay

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
	"antigravity-proxy/internal/session"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHandleV1InternalStoredProjectAndIdentity(t *testing.T) {
	tempDir := t.TempDir()
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetActiveChannel("antigravity")
	accMgr.SetPoolMode(true)

	poolAcc := &account.Account{
		ID:          "acc-oauth-stored-proj",
		Email:       "oauth-user@gmail.com",
		Provider:    "antigravity",
		AccessToken: "ya29.oauth-token-123",
		ProjectID:   "", // OAuth 账号默认无静态配置 ProjectID，依赖 QuotaService 捕获存储
		Enabled:     true,
	}
	accMgr.AddAccount(poolAcc)

	sessRouter := session.NewRouter()
	h := NewAPICompatHandler(nil, accMgr, sessRouter, nil, nil, nil, func(string) {})
	h.SetStoredProjectResolver(func(email string) string {
		if email == "oauth-user@gmail.com" {
			return "captured-gcp-proj-999"
		}
		return ""
	})

	var capturedBody []byte
	h.finalRequester = func(acc *account.Account, method, url string, reqBody []byte) (*http.Response, error) {
		capturedBody = append([]byte(nil), reqBody...)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}}`)),
		}, nil
	}

	inboundPayload := []byte(`{"model":"gemini-2.5-flash","request":{"sessionId":"-3750763034362895579","contents":[{"parts":[{"text":"hello relay"}]}]}}`)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18444/v1internal:generateContent", bytes.NewReader(inboundPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.handleV1Internal(rec, req, &RelaySession{
		UserID:     "user-1",
		UserKey:    "rk-test",
		SessionKey: "sess-seed-alpha",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var outbound map[string]interface{}
	if err := json.Unmarshal(capturedBody, &outbound); err != nil {
		t.Fatalf("failed to unmarshal outbound body: %v", err)
	}

	if gotProj, _ := outbound["project"].(string); gotProj != "captured-gcp-proj-999" {
		t.Fatalf("expected stored project 'captured-gcp-proj-999' instead of public fallback, got %q", gotProj)
	}

	reReqID := regexp.MustCompile(`^agent/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	gotReqID, _ := outbound["requestId"].(string)
	if !reReqID.MatchString(gotReqID) {
		t.Fatalf("expected UUIDv4 requestId matching agent/<uuid>, got %q", gotReqID)
	}

	innerReq, _ := outbound["request"].(map[string]interface{})
	gotSessID, _ := innerReq["sessionId"].(string)
	reSessID := regexp.MustCompile(`^-[0-9]{19}$`)
	if !reSessID.MatchString(gotSessID) || gotSessID == "-3750763034362895579" {
		t.Fatalf("expected rewritten account-isolated 19-digit negative decimal sessionId, got %q", gotSessID)
	}
}

func TestAccountTransportKeyNormalization(t *testing.T) {
	t.Cleanup(func() {
		netutil.ResetAllAccountTransports()
	})

	trBase := netutil.GetAccountTransport("acc-unified-1")
	trRelay := netutil.GetAccountTransport("relay:acc-unified-1")
	trRelayStream := netutil.GetAccountTransport("relay-stream:acc-unified-1")
	trModelFetch := netutil.GetAccountTransport("modelfetch:acc-unified-1")
	trTrigger := netutil.GetAccountTransport("trigger:acc-unified-1")
	trOtherAcc := netutil.GetAccountTransport("acc-unified-2")

	if trBase != trRelay || trBase != trRelayStream || trBase != trModelFetch || trBase != trTrigger {
		t.Fatalf("expected all module calls for the same account 'acc-unified-1' to share a single *http.Transport instance")
	}
	if trBase == trOtherAcc {
		t.Fatalf("expected different accounts to have strictly isolated *http.Transport instances")
	}
}
