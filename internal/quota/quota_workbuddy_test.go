package quota

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

func TestWorkBuddyQuota_RealPoints(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/billing/meter/get-user-resource-summary" {
			t.Errorf("探测路径期望 /billing/meter/get-user-resource-summary, 得到 %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("请求方法期望 POST, 得到 %s", r.Method)
		}
		if r.Header.Get("User-Agent") != "WorkBuddy/5.5.2" {
			t.Errorf("User-Agent 期望 WorkBuddy/5.5.2, 得到 %s", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			t.Errorf("Authorization 期望 Bearer valid-token, 得到 %s", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"code": 0,
			"msg": "OK",
			"data": {
				"Packages": [
					{"PackageCode": "pkg1", "CycleTotalCapacity": "250", "CycleRemainCapacity": "250", "CapacityUnit": "credit"},
					{"PackageCode": "pkg2", "CycleTotalCapacity": "60", "CycleRemainCapacity": "60", "CapacityUnit": "credit"},
					{"PackageCode": "pkg3", "CycleTotalCapacity": "100", "CycleRemainCapacity": "100", "CapacityUnit": "credits"}
				],
				"SubscriptionPackageCode": "",
				"IsPaidUser": false
			}
		}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()
	acc := &account.Account{
		ID:          "acc-wb-1",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "valid-token",
		Email:       "weilimao0714@gmail.com",
	}

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("FetchQuota 失败: %v", err)
	}

	if res.Tier != "Free" {
		t.Errorf("Tier 期望 Free, 得到 %s", res.Tier)
	}
	if len(res.Buckets) != 1 {
		t.Fatalf("Buckets 长度期望 1, 得到 %d", len(res.Buckets))
	}
	if res.Buckets[0].ModelID != "积分余额: 410" {
		t.Errorf("ModelID 期望 '积分余额: 410', 得到 %s", res.Buckets[0].ModelID)
	}
	if res.Buckets[0].RemainPercent != 100 {
		t.Errorf("RemainPercent 期望 100, 得到 %d", res.Buckets[0].RemainPercent)
	}
}

func TestWorkBuddyQuota_PaidUser(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"code": 0,
			"msg": "OK",
			"data": {
				"Packages": [
					{"PackageCode": "pkg1", "CycleTotalCapacity": "1000", "CycleRemainCapacity": "800", "CapacityUnit": "credit"}
				],
				"SubscriptionPackageCode": "pro",
				"IsPaidUser": true
			}
		}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()
	acc := &account.Account{
		ID:          "acc-wb-pro",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "pro-token",
		Email:       "pro@workbuddy.ai",
	}

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("FetchQuota 失败: %v", err)
	}

	if res.Tier != "Pro" {
		t.Errorf("Tier 期望 Pro, 得到 %s", res.Tier)
	}
	if res.Buckets[0].ModelID != "积分余额: 800" {
		t.Errorf("ModelID 期望 '积分余额: 800', 得到 %s", res.Buckets[0].ModelID)
	}
	if res.Buckets[0].RemainPercent != 80 {
		t.Errorf("RemainPercent 期望 80, 得到 %d", res.Buckets[0].RemainPercent)
	}
}

func TestWorkBuddyQuota_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error_msg":"Unauthorized"}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()
	acc := &account.Account{
		ID:          "acc-wb-2",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "expired-token",
		Email:       "test@workbuddy.ai",
	}

	_, err := q.FetchQuota(acc, nil, nil)
	if err == nil {
		t.Fatal("期望 401 报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "失效") {
		t.Errorf("错误信息期望包含 '失效', 实际为 %v", err)
	}
}

// TestWorkBuddyQuota_CAMPolicyUnverifiedFallback 验证国内账号在腾讯云海外集群未开通计量包时返回 500 的优雅降级
func TestWorkBuddyQuota_CAMPolicyUnverifiedFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":20017,"msg":"20017:DescribeMeasureResourceSummary response error: \u0026{AuthFailure.UnauthorizedOperation You are not authorized to perform this operation. Check your CAM policies, and ensure that you are using the correct access keys. [[request id:9a2f743e-420b-4334-af01-f03452bfd2d3]system policy not verify]}","requestId":"bc180f9c-7e39-4b3f-a2e2-924c4963a544"}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()
	acc := &account.Account{
		ID:          "acc-wb-cam",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "cam-token",
		Email:       "weilimao",
	}

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("CAM 500 应当自动标记免配额而非抛错: %v", err)
	}
	if !acc.NoQuota {
		t.Errorf("期望 acc.NoQuota 被置为 true，实际为 false")
	}
	if len(res.Buckets) != 0 {
		t.Fatalf("Buckets 长度期望为 0（空桶），实际为 %d", len(res.Buckets))
	}

	// 验证二次调用短路：上游已关闭时，直接由 NoQuota 短路返回空桶，不发起网络请求
	ts.Close()
	res2, err2 := q.FetchQuota(acc, nil, nil)
	if err2 != nil {
		t.Fatalf("二次调用短路失败: %v", err2)
	}
	if len(res2.Buckets) != 0 {
		t.Fatalf("二次短路返回 Buckets 期望为 0，实际为 %d", len(res2.Buckets))
	}
}

// TestWorkBuddyQuota_Code20017Fallback 验证 HTTP 200 响应中 code=20017 时的自动标记免配额
func TestWorkBuddyQuota_Code20017Fallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":20017,"msg":"system policy not verify","data":{}}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()
	acc := &account.Account{
		ID:          "acc-wb-20017",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "cam-token-2",
		Email:       "user20017@workbuddy.ai",
	}

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("code 20017 应当自动标记免配额而非抛错: %v", err)
	}
	if !acc.NoQuota {
		t.Errorf("期望 acc.NoQuota 被置为 true，实际为 false")
	}
	if len(res.Buckets) != 0 {
		t.Fatalf("Buckets 长度期望为 0（空桶），实际为 %d", len(res.Buckets))
	}
}

// TestAuthManager_RefreshToken_WorkBuddyBlocked 验证 AuthManager.RefreshToken 拦截 WorkBuddy 账号，杜绝打向 Google OAuth 端点
func TestAuthManager_RefreshToken_WorkBuddyBlocked(t *testing.T) {
	am := NewAuthManager(nil)
	acc := &account.Account{
		ID:           "acc-wb-guard",
		Provider:     "workbuddy",
		Email:        "guard@workbuddy.ai",
		RefreshToken: "wb-refresh-token",
	}

	tok, err := am.RefreshToken(acc)
	if err == nil {
		t.Fatalf("期望 WorkBuddy 账号刷新被拦截抛错，实际返回 token: %s", tok)
	}
	if !strings.Contains(err.Error(), "workbuddy does not support Google OAuth refresh flow") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

// TestWorkBuddyQuota_DomesticShortCircuit 验证国内邮箱账号完全不发网络请求，国外账号正常请求
func TestWorkBuddyQuota_DomesticShortCircuit(t *testing.T) {
	reqCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"code": 0,
			"msg": "ok",
			"data": {
				"Packages": [
					{
						"CycleRemainCapacity": "410",
						"CycleTotalCapacity": "1000"
					}
				],
				"IsPaidUser": false
			}
		}`))
	}))
	defer ts.Close() // Teardown

	q := NewQuotaService()

	// 1. 国内 QQ 邮箱账号
	accDomestic := &account.Account{
		ID:          "acc-wb-qq",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "token-qq",
		Email:       "2729547911@qq.com",
	}
	resDom, errDom := q.FetchQuota(accDomestic, nil, nil)
	if errDom != nil {
		t.Fatalf("国内账号 FetchQuota 不应报错: %v", errDom)
	}
	if len(resDom.Buckets) != 0 {
		t.Errorf("国内账号 Buckets 期望为 0（不显示），实际为 %d", len(resDom.Buckets))
	}
	if reqCount != 0 {
		t.Errorf("国内账号期望 0 次网络请求，实际发送了 %d 次", reqCount)
	}

	// 2. 国外 Gmail 账号
	accForeign := &account.Account{
		ID:          "acc-wb-gmail",
		Provider:    "workbuddy",
		BaseURL:     ts.URL,
		AccessToken: "token-gmail",
		Email:       "weilimao0714@gmail.com",
	}
	resFor, errFor := q.FetchQuota(accForeign, nil, nil)
	if errFor != nil {
		t.Fatalf("国外账号 FetchQuota 不应报错: %v", errFor)
	}
	if reqCount != 1 {
		t.Errorf("国外账号期望发送 1 次网络请求，实际为 %d 次", reqCount)
	}
	if len(resFor.Buckets) != 1 {
		t.Fatalf("国外账号 Buckets 期望为 1，实际为 %d", len(resFor.Buckets))
	}
	if resFor.Buckets[0].ModelID != "积分余额: 410" {
		t.Errorf("国外账号 ModelID 期望 '积分余额: 410'，实际为 %s", resFor.Buckets[0].ModelID)
	}
	if resFor.Credits == nil || *resFor.Credits != 410 {
		t.Errorf("国外账号 Credits 期望 410，实际为 %v", resFor.Credits)
	}
}

