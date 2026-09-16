package relay

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAPIHandler_HandleCert_WithAutoProvider(t *testing.T) {
	tempDir := t.TempDir()

	userMgr := NewUserManager()
	userMgr.Init(tempDir)
	authMgr := NewAuthManager(userMgr)

	// 创建测试管理员用户并生成 token
	user, err := userMgr.AddUser("admin", "pwd123", "测试管理员")
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	session, err := authMgr.Login(user.Key, "pwd123")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	certPath := filepath.Join(tempDir, "certs", "ca.pem")
	h := NewAPIHandler(authMgr, nil, nil, func(string) {}, certPath, nil, nil)

	// 1. 无 certPath 且无 provider 时，请求 /api/cert 应失败
	req := httptest.NewRequest(http.MethodGet, "/api/cert", nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	rec := httptest.NewRecorder()
	h.handleCert(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when cert missing and no provider, got %d", rec.Code)
	}

	// 2. 挂载 provider 动态自愈生成证书
	fakeCertData := []byte("-----BEGIN CERTIFICATE-----\nMIIDFakeCertTest\n-----END CERTIFICATE-----")
	h.SetCACertProvider(func() ([]byte, error) {
		_ = os.MkdirAll(filepath.Dir(certPath), 0755)
		_ = os.WriteFile(certPath, fakeCertData, 0644)
		return fakeCertData, nil
	})

	rec2 := httptest.NewRecorder()
	h.handleCert(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after provider self-healing, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if rec2.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf("unexpected Content-Type: %s", rec2.Header().Get("Content-Type"))
	}
	if rec2.Body.String() != string(fakeCertData) {
		t.Fatalf("unexpected body content: %s", rec2.Body.String())
	}
}
