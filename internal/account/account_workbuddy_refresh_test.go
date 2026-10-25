package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshWorkBuddyToken_Success(t *testing.T) {
	expectedOldRT := "old-rt-12345"
	expectedNewAT := "new-access-token-jwt-67890"
	expectedNewRT := "new-refresh-token-jwt-67890"

	var receivedXRefreshToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			http.NotFound(w, r)
			return
		}
		receivedXRefreshToken = r.Header.Get("X-Refresh-Token")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "OK",
			"data": map[string]interface{}{
				"accessToken":      expectedNewAT,
				"refreshToken":     expectedNewRT,
				"expiresIn":        2592000,
				"refreshExpiresIn": 5183999,
			},
		})
	}))
	defer ts.Close() // Teardown & Clean-up

	acc := &Account{
		ID:           "test-wb-refresh-1",
		Provider:     "workbuddy",
		BaseURL:      ts.URL,
		AccessToken:  "old-access-token",
		RefreshToken: expectedOldRT,
	}

	res, err := RefreshWorkBuddyToken(acc)
	if err != nil {
		t.Fatalf("RefreshWorkBuddyToken failed: %v", err)
	}

	if receivedXRefreshToken != expectedOldRT {
		t.Errorf("X-Refresh-Token header: want %q, got %q", expectedOldRT, receivedXRefreshToken)
	}
	if res.AccessToken != expectedNewAT {
		t.Errorf("AccessToken: want %q, got %q", expectedNewAT, res.AccessToken)
	}
	if res.RefreshToken != expectedNewRT {
		t.Errorf("RefreshToken: want %q, got %q", expectedNewRT, res.RefreshToken)
	}
	if acc.GetAccessToken() != expectedNewAT {
		t.Errorf("acc.AccessToken not updated: want %q, got %q", expectedNewAT, acc.GetAccessToken())
	}
	if acc.GetRefreshToken() != expectedNewRT {
		t.Errorf("acc.RefreshToken not updated: want %q, got %q", expectedNewRT, acc.GetRefreshToken())
	}
}

func TestRefreshWorkBuddyToken_SessionExpired401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":12153,"msg":"12153:refresh token failed:400 Bad Request: invalid_grant: Offline user session not found"}`))
	}))
	defer ts.Close() // Teardown & Clean-up

	acc := &Account{
		ID:           "test-wb-expired",
		Provider:     "workbuddy",
		BaseURL:      ts.URL,
		AccessToken:  "stale-access-token",
		RefreshToken: "stale-refresh-token",
	}

	_, err := RefreshWorkBuddyToken(acc)
	if err == nil {
		t.Fatal("expected error on 401 session not found, got nil")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "需要重新登录导入") {
		t.Errorf("error message should mention 401 and 重新登录导入, got %q", err.Error())
	}
}

func TestRefreshWorkBuddyToken_EmptyRefreshToken(t *testing.T) {
	acc := &Account{
		ID:           "test-wb-no-rt",
		Provider:     "workbuddy",
		AccessToken:  "valid-token",
		RefreshToken: "",
	}
	_, err := RefreshWorkBuddyToken(acc)
	if err == nil {
		t.Fatal("expected error for empty refreshToken, got nil")
	}
	if !strings.Contains(err.Error(), "未配置 refreshToken") {
		t.Errorf("unexpected error: %v", err)
	}
}
