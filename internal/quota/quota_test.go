package quota

import (
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

func TestFetchQuota_Diagnostic(t *testing.T) {
	acc := &account.Account{
		ID:           "test-id",
		Email:        "weilimao96@gmail.com",
		AccessToken:  "invalid-access-token-to-force-refresh",
		RefreshToken: "dummy-refresh-token",
		Provider:     "antigravity",
	}

	t.Log("=== [单元测试诊断] 启动 FetchQuota 并测定连通性 ===")

	q := NewQuotaService()
	q.Init(t.TempDir())

	am := NewAuthManager(nil)

	done := make(chan bool)
	go func() {
		// 这里的 RefreshToken 会尝试访问 oauth2.googleapis.com
		_, err := q.FetchQuota(acc, am.RefreshToken, func(id, token string) {})
		t.Logf("[DEBUG] FetchQuota 最终执行完毕，结果/错误: %v", err)
		done <- true
	}()

	select {
	case <-done:
		t.Log("=== [单元测试诊断] 执行完成 ===")
	case <-time.After(25 * time.Second):
		t.Fatal("=== [单元测试诊断] 超时 (25秒)：发生了严重的挂起/死锁 ===")
	}
}
