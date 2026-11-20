package relay

import (
	"testing"
)

// TestValidateToken_LocalInternalLoopback 验证本地内部运维工具专用 Token (如 sk-local-aigen) 的鉴权行为。
func TestValidateToken_LocalInternalLoopback(t *testing.T) {
	tempDir := t.TempDir()
	userMgr := NewUserManager()
	userMgr.Init(tempDir)

	// 添加一个启用的普通中继用户，确保不会被错误绑定
	user, err := userMgr.AddUser("tenant_user", "password123", "tenant user")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}

	authMgr := NewAuthManager(userMgr)

	// 1. 验证 sk-local-aigen 正常映射为本地系统管理员
	sess, err := authMgr.ValidateToken("sk-local-aigen")
	if err != nil {
		t.Fatalf("ValidateToken failed on sk-local-aigen: %v", err)
	}

	if sess.UserID != defaultLocalAdminUserID {
		t.Errorf("expected UserID %q, got %q", defaultLocalAdminUserID, sess.UserID)
	}
	if sess.UserKey != "admin" {
		t.Errorf("expected UserKey 'admin', got %q", sess.UserKey)
	}
	if sess.APIKeyID != APIKeyIDDefaultBypass {
		t.Errorf("expected APIKeyID %q, got %q", APIKeyIDDefaultBypass, sess.APIKeyID)
	}
	if !sess.IsAdmin {
		t.Errorf("expected IsAdmin true, got false")
	}
	if sess.UserID == user.ID {
		t.Errorf("sk-local-aigen must NOT be bound to tenant user ID %q", user.ID)
	}

	// 2. 验证任意以 sk-local- 开头的内部运维 Token 均同等享受该隔离保护
	sessCustom, err := authMgr.ValidateToken("sk-local-custom-tool")
	if err != nil {
		t.Fatalf("ValidateToken failed on sk-local-custom-tool: %v", err)
	}
	if sessCustom.APIKeyID != APIKeyIDDefaultBypass || sessCustom.UserID != defaultLocalAdminUserID {
		t.Errorf("expected bypass session for sk-local-custom-tool, got %+v", sessCustom)
	}

	// 3. 验证 CheckAPIKeyQuota 与 IsModelAuthorizedForAPIKey 均对 APIKeyIDDefaultBypass 予以放行
	if err := userMgr.CheckAPIKeyQuota(sess.UserID, sess.APIKeyID, "nvidia/moonshotai/kimi-k3.1-pro"); err != nil {
		t.Errorf("expected CheckAPIKeyQuota to pass for bypass session, got error: %v", err)
	}
	if err := userMgr.IsModelAuthorizedForAPIKey(sess.UserID, sess.APIKeyID, "nvidia/moonshotai/kimi-k3.1-pro"); err != nil {
		t.Errorf("expected IsModelAuthorizedForAPIKey to pass for bypass session, got error: %v", err)
	}
}
