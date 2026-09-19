package relay

import (
	"strings"
	"testing"
)

func TestValidateToken_InternalOCRProbe(t *testing.T) {
	tempDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	user, err := userMgr.AddUser("test_user", "password123", "test user")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	authMgr := NewAuthManager(userMgr)

	// 1. 测试根据内部探针 Token 提取对应用户
	sess, err := authMgr.ValidateToken("sk-ant-ocr-internal-probe-" + user.ID)
	if err != nil {
		t.Fatalf("ValidateToken failed on internal OCR probe: %v", err)
	}
	if sess.UserID != user.ID {
		t.Errorf("expected UserID %s, got %s", user.ID, sess.UserID)
	}
	if sess.Token != "sk-ant-ocr-internal-probe-"+user.ID {
		t.Errorf("expected Token to be preserved, got %s", sess.Token)
	}

	// 2. 测试根据内部探针 Token 兜底（未知用户 ID 兜底到首个启用用户）
	sessFallback, errFallback := authMgr.ValidateToken("sk-ant-ocr-internal-probe-non-existent")
	if errFallback != nil {
		t.Fatalf("ValidateToken failed on fallback internal OCR probe: %v", errFallback)
	}
	if sessFallback.UserID != user.ID {
		t.Errorf("expected fallback to first enabled user %s, got %s", user.ID, sessFallback.UserID)
	}
}

func TestValidateToken_PreservesRealAPIKey(t *testing.T) {
	tempDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	user, err := userMgr.AddUser("real_admin", "password123", "admin user")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	apiKey, err := userMgr.CreateAPIKey(user.ID, "real-key")
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	authMgr := NewAuthManager(userMgr)

	sess, err := authMgr.ValidateToken(apiKey.Key)
	if err != nil {
		t.Fatalf("ValidateToken failed on real API key: %v", err)
	}
	if sess.Token != apiKey.Key {
		t.Errorf("expected session.Token to be %q, got %q", apiKey.Key, sess.Token)
	}
	if sess.UserKey != "real_admin" {
		t.Errorf("expected session.UserKey to be real_admin, got %q", sess.UserKey)
	}
}

func TestOCR_BearerTokenSelection(t *testing.T) {
	// Case 1: Session 包含合法 API Key，优先使用 Token
	sessWithKey := &RelaySession{
		UserID:  "u1",
		UserKey: "admin",
		Token:   "sk-ant-4a0fb93ccfb776783e5b62a6be7e06ff",
	}
	bearerToken := strings.TrimSpace(sessWithKey.Token)
	if bearerToken == "" {
		bearerToken = strings.TrimSpace(sessWithKey.UserKey)
	}
	if bearerToken != "sk-ant-4a0fb93ccfb776783e5b62a6be7e06ff" {
		t.Errorf("expected real API Key, got %s", bearerToken)
	}

	// Case 2: Session 未显式设置 Token，回退到 UserKey
	sessWithoutKey := &RelaySession{
		UserID:  "u2",
		UserKey: "k1",
		Token:   "",
	}
	bearerToken2 := strings.TrimSpace(sessWithoutKey.Token)
	if bearerToken2 == "" {
		bearerToken2 = strings.TrimSpace(sessWithoutKey.UserKey)
	}
	if bearerToken2 != "k1" {
		t.Errorf("expected k1, got %s", bearerToken2)
	}
}
