package account

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// account_grok_auth_test.go: Grok 号池专用「授权过期检查→刷新→失效移除」单元测试。
// 锁定 CheckAndPurgeGrokAuth 的三态语义:
//  1. access_token 仍有效(exp 距当前 > grokAuthRefreshSkewSec)→ skipped,不打刷新端点;
//  2. 临近/已过期(exp 距当前 <= skew 或非 JWT exp==0)→ 调 RefreshAccountTokenSync 刷新:
//     成功 refreshed / 永久失败(RefreshToken 返 isPermanent 错误且账号已不在池)removed /
//     瞬时失败(账号仍在池)failed;
//  3. StartGrokAuthMonitor/StopGrokAuthMonitor 启停幂等。
// 复用 jwtExpClaim(见 account.go)解析路径,故造的 JWT 须经其识别为带 exp 的 JWT。

// fakeExpJWT 组装一个带 "exp" claim 的三段式 JWT,供 AccessTokenExp() 解析。
// exp 为 Unix 秒。与 internal/quota/xai_oauth_test.go 的 fakeJWT 同构(header.payload.signature)。
func fakeExpJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{"exp": exp})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".sig"
}

// newGrokAuthTestManager 建一个已 Init 的 Manager(临时目录),供 CheckAndPurgeGrokAuth 测试用,
// 并在测试结束清理其 goroutine 监控。
func newGrokAuthTestManager(t *testing.T) *Manager {
	t.Helper()
	tempDir := t.TempDir()
	m := NewManager()
	m.Init(tempDir)
	t.Cleanup(func() {
		m.StopCooldownMonitor()
		m.StopTokenRefreshMonitor()
		m.StopGrokAuthMonitor()
	})
	return m
}

// addGrokOAuthAccount 向 manager 注入一个 grok OAuth 账号(带 RefreshToken),返回该账号。
// token 用 fakeExpJWT(exp) 构造,使 AccessTokenExp() 返回 exp 供 CheckAndPurgeGrokAuth 判定。
func addGrokOAuthAccount(m *Manager, id, email string, exp int64) *Account {
	acc := &Account{
		ID:            id,
		Email:         email,
		Provider:      "grok",
		ScopeType:     "grok",
		AccessToken:   fakeExpJWT(exp),
		RefreshToken:  "rt-" + id,
		BaseURL:        DefaultGrokBaseURL,
		TokenEndpoint: "https://auth.x.ai/oauth2/token",
		Enabled:       true,
		Cooldowns:     map[string]int64{},
	}
	m.AddAccount(acc)
	return acc
}

// TestCheckAndPurgeGrokAuth_Skipped 锁定:access_token 仍有效(exp 距当前 > skew)时不刷新,
// 返回 skipped 且 RefreshToken 闭包从未被调用。
func TestCheckAndPurgeGrokAuth_Skipped(t *testing.T) {
	m := newGrokAuthTestManager(t)

	refreshCalled := false
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshCalled = true
		return "should-not-be-called", nil
	}

	// exp 设为「当前 + skew + 10min」,远未临近过期 → skip。
	exp := time.Now().Unix() + grokAuthRefreshSkewSec + 600
	addGrokOAuthAccount(m, "grok-valid", "valid@x.ai", exp)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != "skipped" {
		t.Errorf("expected outcome skipped, got %q", results[0].Outcome)
	}
	if refreshCalled {
		t.Error("RefreshToken should NOT be called for still-valid access_token")
	}
	// 账号仍在池中(access token 不变)。
	if m.GetAccountByID("grok-valid") == nil {
		t.Error("grok-valid should still exist after skip")
	}
}

// TestCheckAndPurgeGrokAuth_Refreshed 锁定:临近过期(exp 距当前 <= skew)且刷新成功,
// 返回 refreshed 且 access_token 已被 RefreshAccountTokenSync 间接更新。
func TestCheckAndPurgeGrokAuth_Refreshed(t *testing.T) {
	m := newGrokAuthTestManager(t)

	m.RefreshToken = func(acc *Account) (string, error) {
		// 返回一个全新的有效 JWT(exp 在很久以后),模拟刷新拿到新 token。
		return fakeExpJWT(time.Now().Unix() + grokAuthRefreshSkewSec + 3600), nil
	}

	// exp 设为「当前 + skew - 60s」,即临近过期 → 触发刷新。
	exp := time.Now().Unix() + grokAuthRefreshSkewSec - 60
	addGrokOAuthAccount(m, "grok-near", "near@x.ai", exp)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != "refreshed" {
		t.Errorf("expected outcome refreshed, got %q", results[0].Outcome)
	}
	// 账号仍在池且 token 已更新(新 token exp 远在未来,不再临近过期)。
	acc := m.GetAccountByID("grok-near")
	if acc == nil {
		t.Fatal("grok-near should still exist after successful refresh")
	}
	newExp := acc.AccessTokenExp()
	if newExp <= time.Now().Unix()+grokAuthRefreshSkewSec {
		t.Errorf("access token should be refreshed to a far-future exp, got exp=%d (now+skew=%d)",
			newExp, time.Now().Unix()+grokAuthRefreshSkewSec)
	}
}

// TestCheckAndPurgeGrokAuth_Disabled 锁定:刷新命中「永久失败」错误且账号被停用时,
// CheckAndPurgeGrokAuth 返回 disabled, 且账号依然保留在号池中(Enabled=false)。
// 模拟方式:RefreshToken 闭包返回 invalid_grant 错误,并在闭包里调用 UpdateAccountEnabled(false)
// (复刻 refreshXaiToken 永久失败链路的真实副作用),使 CheckAndPurgeGrokAuth 判定为 disabled。
func TestCheckAndPurgeGrokAuth_Disabled(t *testing.T) {
	m := newGrokAuthTestManager(t)

	m.RefreshToken = func(acc *Account) (string, error) {
		// 复刻 refreshXaiToken 永久失败的副作用:停用账号(真实链路在 internal/quota/oauth.go 内做)。
		m.UpdateAccountEnabled(acc.ID, false)
		return "", errors.New("xai token request failed with status 400: invalid_grant")
	}

	// exp 临近过期 → 触发刷新分支。
	exp := time.Now().Unix() + grokAuthRefreshSkewSec - 60
	addGrokOAuthAccount(m, "grok-dead", "dead@x.ai", exp)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != "disabled" {
		t.Errorf("expected outcome disabled, got %q", results[0].Outcome)
	}
	if !strings.Contains(results[0].Detail, "invalid_grant") {
		t.Errorf("expected detail containing invalid_grant, got %q", results[0].Detail)
	}
	gotAcc := m.GetAccountByID("grok-dead")
	if gotAcc == nil {
		t.Fatal("grok-dead should NOT be removed from pool after permanent failure")
	}
	if gotAcc.Enabled {
		t.Error("grok-dead should be disabled (Enabled=false) after permanent failure")
	}
}

// TestCheckAndPurgeGrokAuth_FailedTransient 锁定:刷新命中瞬时失败(账号仍在池)时返回 failed,不移除。
func TestCheckAndPurgeGrokAuth_FailedTransient(t *testing.T) {
	m := newGrokAuthTestManager(t)

	m.RefreshToken = func(acc *Account) (string, error) {
		// 瞬时失败:不改账号,不移除(复刻 refreshXaiToken 瞬时失败分支)。
		return "", errors.New("xai token request failed with status 502: bad gateway")
	}

	exp := time.Now().Unix() + grokAuthRefreshSkewSec - 60
	addGrokOAuthAccount(m, "grok-flaky", "flaky@x.ai", exp)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != "failed" {
		t.Errorf("expected outcome failed, got %q", results[0].Outcome)
	}
	// 瞬时失败不移除:账号仍在。
	if m.GetAccountByID("grok-flaky") == nil {
		t.Error("grok-flaky should NOT be removed after transient failure")
	}
}

// TestCheckAndPurgeGrokAuth_NonJWTExpTriggersRefresh 锁定:access_token 非 JWT(无 exp,exp==0)
// 时也触发刷新(兜底口径,与 refreshXaiToken 的 exp==0 分支一致)。
func TestCheckAndPurgeGrokAuth_NonJWTExpTriggersRefresh(t *testing.T) {
	m := newGrokAuthTestManager(t)

	refreshCalled := false
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshCalled = true
		return fakeExpJWT(time.Now().Unix() + grokAuthRefreshSkewSec + 3600), nil
	}

	// 非 JWT access_token(API Key 形态字符串):AccessTokenExp 返回 0 → 兜底触发刷新。
	acc := &Account{
		ID:            "grok-apikey",
		Email:         "apikey@x.ai",
		Provider:      "grok",
		ScopeType:     "grok",
		AccessToken:   "xai-xxxxxxxxxxxxxxxx", // 非 JWT,无 exp
		RefreshToken:  "rt-apikey",
		BaseURL:        DefaultGrokBaseURL,
		TokenEndpoint: "https://auth.x.ai/oauth2/token",
		Enabled:       true,
		Cooldowns:     map[string]int64{},
	}
	m.AddAccount(acc)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Outcome != "refreshed" {
		t.Errorf("expected outcome refreshed for non-JWT (exp==0) token, got %q", results[0].Outcome)
	}
	if !refreshCalled {
		t.Error("RefreshToken should be called for non-JWT (exp==0) access token")
	}
}

// TestCheckAndPurgeGrokAuth_SkipsAPIKeyOnlyAccount 锁定:API Key 型 grok 账号(RefreshToken=="")
// 不参与授权检查(无 OAuth 刷新概念),不出现在结果列表里。
func TestCheckAndPurgeGrokAuth_SkipsAPIKeyOnlyAccount(t *testing.T) {
	m := newGrokAuthTestManager(t)

	refreshCalled := false
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshCalled = true
		return "should-not-be-called", nil
	}

	// API Key 型 grok 账号:RefreshToken 留空(前端手动填 API Key 录入的号即此形态)。
	apiKeyAcc := &Account{
		ID:           "grok-apikeyonly",
		Email:        "apikeyonly@x.ai",
		Provider:     "grok",
		ScopeType:    "grok",
		AccessToken:  "xai-keynoauthtoken",
		RefreshToken: "", // 无 OAuth refresh token
		BaseURL:       DefaultGrokBaseURL,
		Enabled:      true,
		Cooldowns:    map[string]int64{},
	}
	m.AddAccount(apiKeyAcc)

	results := m.CheckAndPurgeGrokAuth()
	if len(results) != 0 {
		t.Fatalf("API-Key-only grok account should be skipped (no RefreshToken), got results: %+v", results)
	}
	if refreshCalled {
		t.Error("RefreshToken should NOT be called for API-Key-only grok account")
	}
}

// TestStartGrokAuthMonitor_Idempotent 锁定:重复调用 StartGrokAuthMonitor 不重复起 goroutine。
func TestStartGrokAuthMonitor_Idempotent(t *testing.T) {
	m := newGrokAuthTestManager(t)

	m.StartGrokAuthMonitor()
	m.StartGrokAuthMonitor() // 重复调用应幂等,不 panic/不重复起 ticker。

	// Stop 能正常停掉(只有第一个 Start 起的 ticker 被 Stop 停掉,验证无 hang)。
	m.StopGrokAuthMonitor()
	// 再 Stop 一次也幂等。
	m.StopGrokAuthMonitor()
}

// TestCheckAndPurgeGrokAuth_EmptyWhenNoGrok 锁定:无 grok OAuth 账号时返回 nil,不发刷新。
func TestCheckAndPurgeGrokAuth_EmptyWhenNoGrok(t *testing.T) {
	m := newGrokAuthTestManager(t)

	refreshCalled := false
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshCalled = true
		return "", errors.New("nope")
	}

	// 只注入 nvidia 账号,无 grok。
	nv := &Account{
		ID: "nv-1", Email: "nv@x.ai", Provider: "nvidia", Enabled: true,
		AccessToken: "nv-key", RefreshToken: "nv-rt",
		Cooldowns: map[string]int64{},
	}
	m.AddAccount(nv)

	results := m.CheckAndPurgeGrokAuth()
	if results != nil {
		t.Errorf("expected nil results when no grok OAuth accounts, got %+v", results)
	}
	if refreshCalled {
		t.Error("RefreshToken should NOT be called when no grok accounts present")
	}
}

// TestCheckAndPurgeGrokAuth_ConcurrentSafe 锁定:并发调用 CheckAndPurgeGrokAuth 不 panic
// (读快照串行刷新,内部 RefreshAccountTokenSync 单账号互斥,GetAccountByID 复检 RLock 不重叠)。
func TestCheckAndPurgeGrokAuth_ConcurrentSafe(t *testing.T) {
	m := newGrokAuthTestManager(t)

	var refreshMu sync.Mutex
	m.RefreshToken = func(acc *Account) (string, error) {
		refreshMu.Lock()
		defer refreshMu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return fakeExpJWT(time.Now().Unix() + grokAuthRefreshSkewSec + 3600), nil
	}

	// 注入多个临近过期的 grok 账号。
	for i := 0; i < 5; i++ {
		addGrokOAuthAccount(m, "grok-c"+itoa(i), "c"+itoa(i)+"@x.ai",
			time.Now().Unix()+grokAuthRefreshSkewSec-120)
	}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.CheckAndPurgeGrokAuth()
		}()
	}
	wg.Wait()
}

// itoa 简易整数转字符串(避免引入 strconv 仅一处用)。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
